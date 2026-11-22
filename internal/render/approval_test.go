package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApprovalDetailReadable(t *testing.T) {
	d, warn, fits := ApprovalDetail("Bash", json.RawMessage(`{"command":"make && ls <x> 2>&1"}`))
	if !strings.Contains(d, "make && ls <x> 2>&1") || warn || !fits {
		t.Fatalf("detail=%q warn=%v fits=%v", d, warn, fits)
	}
}

func TestApprovalDetailFlagsHiddenCharacters(t *testing.T) {
	d, warn, _ := ApprovalDetail("Bash", json.RawMessage(`{"command":"rm -rf /tmp/a‮/b"}`))
	if !warn || strings.ContainsRune(d, '‮') || !strings.Contains(d, "U+202E") {
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
		"emoji VS16":     "git commit -m 'fix ❤️'",
		"ZWJ family":     "echo 👨‍👩‍👧",
		"keycap":         "echo 1️⃣",
		"NFD Vietnamese": "ls Tiếng",
		"Hindi virama":   "echo हिन्दी",
		"skin tone":      "echo 👍🏽",
	} {
		in, _ := json.Marshal(map[string]string{"command": s})
		if d, warn, _ := ApprovalDetail("Bash", in); warn {
			t.Errorf("%s flagged: %s", name, d)
		}
	}
}

func TestApprovalDetailStillFlags(t *testing.T) {
	for name, s := range map[string]string{
		"Adlam RTL":           "\U0001E900 1 2 \U0001E901",
		"Phoenician RTL":      "\U00010900 1",
		"zalgo marks":         "á́́́́",
		"mark after space":    "a ́b",
		"ZWJ between letters": "ab‍cd",
		"VS16 after letter":   "a️",
		"NBSP":                "rm -rf /tmp/x /",
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
