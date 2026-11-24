package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestApprovalDetailReadable(t *testing.T) {
	d, warn, fits := ApprovalDetail("Bash", json.RawMessage(`{"command":"make && ls <x> 2>&1"}`))
	if !strings.Contains(d, "make && ls <x> 2>&1") || warn || !fits {
		t.Fatalf("detail=%q warn=%v fits=%v", d, warn, fits)
	}
}

func TestApprovalDetailFlagsHiddenCharacters(t *testing.T) {
	d, warn, _ := ApprovalDetail("Bash", json.RawMessage("{\"command\":\"rm -rf /tmp/a\u202e/b\"}"))
	if !warn || strings.ContainsRune(d, '\u202e') || !strings.Contains(d, "U+202E") {
		t.Fatalf("detail=%q warn=%v", d, warn)
	}
}

func TestApprovalDetailCountsUTF16(t *testing.T) {
	_, _, fits := ApprovalDetail("Bash", json.RawMessage(`{"command":"`+strings.Repeat("😀", 1600)+`"}`))
	if fits {
		t.Fatal("1600 emoji are 3200 UTF-16 units; must not fit")
	}
}

func TestApprovalDetailFlagsInvisibleAndRTL(t *testing.T) {
	for name, s := range map[string]string{
		"hangul filler":      "aㅤb",
		"variation selector": "a️b",
		"combining joiner":   "a͏b",
		"braille blank":      "a⠀b",
		"C1 control":         "a\u0085b",
		"DEL":                "a\u007fb",
		"RTL letters":        "א 1 2 ב",
	} {
		in, _ := json.Marshal(map[string]string{"command": s})
		if _, warn, _ := ApprovalDetail("Bash", in); !warn {
			t.Errorf("%s not flagged", name)
		}
	}
	if _, warn, _ := ApprovalDetail("Bash", json.RawMessage(`{"command":"xin chào thế giới && ls"}`)); warn {
		t.Error("ordinary Vietnamese text flagged")
	}
}

func TestApprovalDetailNoFalseAlarms(t *testing.T) {
	for name, s := range map[string]string{
		"emoji VS16":                  "git commit -m 'fix ❤️'",
		"ZWJ family":                  "echo 👨‍👩‍👧",
		"keycap":                      "echo 1️⃣",
		"NFD Vietnamese":              "ls Tiếng",
		"Hindi virama":                "echo हिन्दी",
		"skin tone":                   "echo 👍🏽",
		"Tifinagh joiner in Tifinagh": "\u2D30\u2D7F\u2D31",
	} {
		in, _ := json.Marshal(map[string]string{"command": s})
		if d, warn, _ := ApprovalDetail("Bash", in); warn {
			t.Errorf("%s flagged: %s", name, d)
		}
	}
}

func TestApprovalDetailStillFlags(t *testing.T) {
	for name, s := range map[string]string{
		"Adlam RTL":                   "\U0001E900 1 2 \U0001E901",
		"Phoenician RTL":              "\U00010900 1",
		"zalgo marks":                 "á́́́́",
		"mark after space":            "a ́b",
		"ZWJ between letters":         "ab\u200dcd",
		"VS16 after letter":           "a️",
		"Tifinagh joiner after Latin": "a\u2D7Fb",
		"Khitan filler after Latin":   "a\U00016FE4b",
		"Duployan selector 1":         "a\U0001BC9Db",
		"Duployan selector 2":         "a\U0001BC9Eb",
		"NBSP":                        "rm -rf /tmp/x /",
	} {
		in, _ := json.Marshal(map[string]string{"command": s})
		if _, warn, _ := ApprovalDetail("Bash", in); !warn {
			t.Errorf("%s not flagged", name)
		}
	}
}

func TestApprovalDetailFlagsVariationSelectorSmuggling(t *testing.T) {
	for name, s := range map[string]string{
		"VS1-3 after letter":  "h︀︁︂ello",
		"VS17+ after letter":  "h\U000E0100\U000E01EFello",
		"VS16 after caret":    "^️",
		"VS16 after backtick": "`︎",
		"VS16 on bare digit":  "1️",
	} {
		in, _ := json.Marshal(map[string]string{"command": s})
		if _, warn, _ := ApprovalDetail("Bash", in); !warn {
			t.Errorf("%s not flagged", name)
		}
	}
}

func TestApprovalDetailEmojiNotSo(t *testing.T) {
	for name, s := range map[string]string{
		"left right arrow": "\u2194\uFE0F", "double bang": "\u203C\uFE0F", "bang question": "\u2049\uFE0F",
		"info": "\u2139\uFE0F", "wavy dash": "\u3030\uFE0F", "part alternation": "\u303D\uFE0F",
		"arrow curving": "\u2934\uFE0F", "copyright": "\u00A9\uFE0F", "registered": "\u00AE\uFE0F",
		"man skin tone red hair": "\U0001F468\U0001F3FD\u200D\U0001F9B0",
	} {
		in, _ := json.Marshal(map[string]string{"command": s})
		if d, warn, _ := ApprovalDetail("Bash", in); warn {
			t.Errorf("%s flagged: %s", name, d)
		}
	}
}

// Documents finding 2: VS16/ZWJ can still carry a little data between emoji, but not after non-emoji.
func TestApprovalDetailLowCapacityChannels(t *testing.T) {
	for s, want := range map[string]bool{
		"\U0001F44D\u200D\U0001F44D": false, "\u2500\uFE0F": true, "\uFF3E\uFE0F": true, "\uA700\uFE0F": true,
	} {
		in, _ := json.Marshal(map[string]string{"command": s})
		if _, warn, _ := ApprovalDetail("Bash", in); warn != want {
			t.Errorf("%+q warn=%v want %v", s, warn, want)
		}
	}
}

func TestExtPictTable(t *testing.T) {
	for _, r := range []rune{0xA9, 0x203C, 0x2194, 0x1F3F4, 0x1FAEF} {
		if !unicode.Is(extPict, r) {
			t.Errorf("%U not in table", r)
		}
	}
	for _, r := range []rune{0x41, 0x2500, 0x1F3FB} {
		if unicode.Is(extPict, r) {
			t.Errorf("%U in table", r)
		}
	}
}

func TestApprovalDetailEmojiTagSequences(t *testing.T) {
	tags := func(s string) string {
		var sb strings.Builder
		for _, b := range []byte(s) {
			sb.WriteRune(rune(0xE0000 + int(b)))
		}
		return sb.String()
	}
	const flag, cancel = "\U0001F3F4", "\U000E007F"
	for in, want := range map[string]bool{
		flag + tags("gbeng") + cancel:                                 false,
		flag + tags("ustx") + cancel:                                  false,
		flag + tags("gbeng") + cancel + flag + tags("gbsct") + cancel: false,
		flag + tags("001a") + cancel:                                  false,
		"a" + tags("gbeng") + cancel:                                  true,
		flag + tags("gbeng"):                                          true,
		flag + tags("GBENG") + cancel:                                 true,
		flag + tags("gbabcde") + cancel:                               true,
		flag + tags("gb") + cancel:                                    true,
		tags("hello"):                                                 true,
		flag + cancel:                                                 true,
		flag + "\uFE0F" + tags("gbeng") + cancel:                      true,
		flag + tags("rm -rf") + cancel:                                true,
		flag + tags("gba") + cancel:                                   false,
		flag + tags("001abcd") + cancel:                               false,
		flag + tags("gbeng") + cancel + cancel:                        true,
		flag + tags("gb") + cancel + tags("eng") + cancel:             true,
	} {
		raw, _ := json.Marshal(map[string]string{"command": in})
		if _, warn, _ := ApprovalDetail("Bash", raw); warn != want {
			t.Errorf("%+q warn=%v want %v", in, warn, want)
		}
	}
}

func TestApprovalDetailLongTagRunIsLinear(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"command": "\U0001F3F4" + strings.Repeat("\U000E0061", 200000) + "\U000E007F"})
	start := time.Now()
	if _, warn, _ := ApprovalDetail("Bash", in); !warn {
		t.Error("long tag run not flagged")
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("200k tags took %v; tag scan must be linear", d)
	}
}

// extpict.go is generated from Unicode 17.0.0; regenerate it when Go's tables move on.
func TestExtPictMatchesUnicodeVersion(t *testing.T) {
	if unicode.Version != "17.0.0" {
		t.Fatalf("unicode.Version is %s; regenerate extpict.go from that version's emoji-data.txt", unicode.Version)
	}
}
