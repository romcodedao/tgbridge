package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
)

// MaxApprovalUnits keeps a whole approval request inside one Telegram message (limits count UTF-16 units).
const MaxApprovalUnits = 3000

// invisible are printable-category runes that render as nothing (known spoofing tricks).
var invisible = map[rune]bool{0x115F: true, 0x1160: true, 0x3164: true, 0xFFA0: true, 0x2800: true,
	0x034F: true, 0x17B4: true, 0x17B5: true, 0x180B: true, 0x180C: true, 0x180D: true, 0x180F: true} // ...and invisible marks

// isRTL covers the Unicode blocks of every right-to-left script (bidi classes R and AL); such text can
// display reordered without any control character.
func isRTL(r rune) bool {
	return r >= 0x0590 && r <= 0x08FF || r >= 0xFB1D && r <= 0xFDFF || r >= 0xFE70 && r <= 0xFEFF ||
		r >= 0x10800 && r <= 0x10FFF || r >= 0x1E800 && r <= 0x1EFFF
}

// emojiBase is a pictographic symbol a presentation selector or ZWJ may follow (not ASCII ^ ` or digits).
// shortcut: approximates Extended_Pictographic by category and range; use the real table if false alarms show up.
func emojiBase(r rune) bool {
	return r == 0xA9 || r == 0xAE || r >= 0x2000 && unicode.In(r, unicode.So, unicode.Sk)
}

func keycapBase(r rune) bool { return r >= '0' && r <= '9' || r == '#' || r == '*' }

// isVS reports a variation selector: invisible marks that can carry hidden data after any letter.
func isVS(r rune) bool { return r >= 0xFE00 && r <= 0xFE0F || r >= 0xE0100 && r <= 0xE01EF }

// legit reports whether an otherwise-suspicious rune is part of ordinary text: emoji presentation
// selectors, keycaps and ZWJ sequences, and up to 3 combining marks on a letter (NFD Vietnamese, Indic).
func legit(rs []rune, i, marks int, base rune) bool {
	r := rs[i]
	if invisible[r] {
		return false
	}
	var prev, next rune
	if i > 0 {
		prev = rs[i-1]
	}
	if i+1 < len(rs) {
		next = rs[i+1]
	}
	switch {
	case r == 0xFE0E || r == 0xFE0F:
		return emojiBase(prev) || keycapBase(prev) && next == 0x20E3
	case isVS(r):
		return false
	case r == 0x20E3:
		return prev == 0xFE0F || keycapBase(prev)
	case r == 0x200D:
		return (emojiBase(prev) || prev == 0xFE0F) && emojiBase(next)
	case unicode.Is(unicode.Mn, r):
		return unicode.IsLetter(base) && marks <= 3
	}
	return false
}

// ApprovalDetail renders a tool call for an approval prompt: readable (no HTML escaping), with invisible
// or control characters shown as <U+XXXX>, right-to-left text flagged, and whether it fits one Telegram message.
func ApprovalDetail(tool string, input json.RawMessage) (detail string, hidden bool, fits bool) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if len(input) == 0 || !json.Valid(input) {
		input = json.RawMessage("{}")
	}
	enc.Encode(map[string]any{"tool": tool, "input": input})
	rs := []rune(strings.TrimSpace(buf.String()))
	var sb strings.Builder
	marks, base := 0, rune(0)
	for i, r := range rs {
		if unicode.Is(unicode.Mn, r) {
			marks++
		} else {
			marks, base = 0, r
		}
		if isRTL(r) {
			hidden = true // kept as is, but the owner is warned the line may display reordered
		}
		suspicious := r != '\n' && (!unicode.IsPrint(r) && r != ' ' || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) || invisible[r])
		if suspicious && !legit(rs, i, marks, base) {
			hidden = true
			fmt.Fprintf(&sb, "<U+%04X>", r)
			continue
		}
		sb.WriteRune(r)
	}
	detail = sb.String()
	return detail, hidden, len(utf16.Encode([]rune(detail))) <= MaxApprovalUnits
}
