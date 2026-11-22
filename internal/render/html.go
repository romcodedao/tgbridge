package render

import (
	"html"
	"strings"

	"github.com/romcodedao/tgbridge/internal/streamjson"
)

// FallbackHTML renders a turn as Telegram-HTML sendMessage chunks (≤4096 runes each), used when a
// rich call is rejected.
func FallbackHTML(t *streamjson.Turn, footer string) []string {
	var parts []string
	if strings.TrimSpace(t.Thinking) != "" {
		parts = append(parts, "💭 <blockquote expandable>"+html.EscapeString(head(t.Thinking, 3000))+"</blockquote>")
	}
	if len(t.Tools) > 0 {
		var sb strings.Builder
		for _, tc := range t.Tools {
			mark := "☐"
			if tc.Done {
				mark = "☑"
			}
			if tc.Failed {
				mark = "❌"
			}
			sb.WriteString(mark + " " + html.EscapeString(tc.Name+" "+tc.Summary) + "\n")
		}
		parts = append(parts, "🔧 <blockquote expandable>"+strings.TrimSpace(sb.String())+"</blockquote>")
	}
	for _, p := range splitRunes(t.Text, 3800) {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, html.EscapeString(p))
		}
	}
	if footer != "" {
		parts = append(parts, "<i>"+html.EscapeString(footer)+"</i>")
	}
	var out []string
	cur := ""
	for _, p := range parts {
		if cur != "" && len([]rune(cur))+len([]rune(p))+2 > 4096 {
			out = append(out, cur)
			cur = ""
		}
		if cur != "" {
			cur += "\n\n"
		}
		cur += p
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
