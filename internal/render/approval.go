package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
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
func emojiBase(r rune) bool {
	return unicode.Is(extPict, r) || r >= 0x1F3FB && r <= 0x1F3FF // skin-tone modifiers
}

func keycapBase(r rune) bool { return r >= '0' && r <= '9' || r == '#' || r == '*' }

// isVS reports a variation selector: invisible marks that can carry hidden data after any letter.
func isVS(r rune) bool { return r >= 0xFE00 && r <= 0xFE0F || r >= 0xE0100 && r <= 0xE01EF }

// legit reports whether an otherwise-suspicious rune is part of ordinary text: emoji presentation
// selectors, keycaps and ZWJ sequences, and up to 3 combining marks on a letter of the same script (NFD Vietnamese, Indic).
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
	case r >= 0xE0020 && r <= 0xE007F:
		return tagSeq(rs, i)
	case unicode.Is(unicode.Mn, r):
		return unicode.IsLetter(base) && marks <= 3 && (unicode.Is(unicode.Inherited, r) || sameScript(base, r))
	}
	return false
}

// subdivID is a UTS #35 subdivision id: a 2-letter region or 3-digit code plus a 1-4 char suffix.
var subdivID = regexp.MustCompile(`^([a-z]{2}|[0-9]{3})[a-z0-9]{1,4}$`)

// tagSeq reports whether the tag rune rs[i] lies in a well-formed UTS #51 emoji tag sequence: U+1F3F4,
// a lowercase subdivision id in tag letters, then exactly one cancel tag U+E007F ending the run.
// Anywhere else tag characters are an ASCII-smuggling channel.
func tagSeq(rs []rune, i int) bool {
	// A valid run is at most 8 tags (7-char id + cancel); scanning no further keeps long runs linear.
	s, e := i, i
	for s > 0 && rs[s-1] >= 0xE0020 && rs[s-1] <= 0xE007F && i-s < 8 {
		s--
	}
	for e+1 < len(rs) && rs[e+1] >= 0xE0020 && rs[e+1] <= 0xE007F && e-i < 8 {
		e++
	}
	if e-s+1 > 8 || s == 0 || rs[s-1] != 0x1F3F4 || rs[e] != 0xE007F {
		return false
	}
	id := make([]byte, 0, e-s)
	for _, r := range rs[s:e] {
		id = append(id, byte(r-0xE0000))
	}
	return subdivID.Match(id)
}

// sameScript reports whether a and b belong to the same Unicode script.
func sameScript(a, b rune) bool {
	for _, t := range unicode.Scripts {
		if unicode.Is(t, a) {
			return unicode.Is(t, b)
		}
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
