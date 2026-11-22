package render

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/romcodedao/tgbridge/internal/streamjson"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

// MaxRichBytes caps one rich message's JSON size. The Bot API page states no limit; this is a
// conservative guess, checked by the e2e test.
const MaxRichBytes = 32000

const (
	draftThinkingRunes = 1500
	finalThinkingRunes = 8000
	maxToolItems       = 50
	chunkRunes         = 3500
)

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

func head(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func checklist(tools []streamjson.ToolCall) telegram.Block {
	var items []telegram.ListItem
	start := max(0, len(tools)-maxToolItems)
	for _, tc := range tools[start:] {
		label := tc.Name
		if tc.Summary != "" {
			label += ": " + tc.Summary
		}
		if tc.Failed {
			label = "❌ " + label
		}
		items = append(items, telegram.ListItem{Blocks: []telegram.Block{telegram.Paragraph(label)}, HasCheckbox: true, IsChecked: tc.Done})
	}
	return telegram.List(items...)
}

// Draft renders the in-progress turn for sendRichMessageDraft.
func Draft(t *streamjson.Turn) telegram.RichMessage {
	think := "Thinking…"
	if t.Thinking != "" {
		think = tail(t.Thinking, draftThinkingRunes)
	}
	bs := []telegram.Block{telegram.Thinking(think)}
	if len(t.Tools) > 0 {
		bs = append(bs, checklist(t.Tools))
	}
	if strings.TrimSpace(t.Text) != "" {
		bs = append(bs, MarkdownBlocks(tail(t.Text, chunkRunes))...)
	}
	msg := telegram.RichMessage{Blocks: bs}
	if size(msg) > MaxRichBytes { // keep the draft valid even if Markdown expands a lot
		msg.Blocks = append(bs[:min(len(bs), 2)], telegram.Paragraph(tail(t.Text, 1500)))
	}
	return msg
}

// Final renders the finished turn as one or more rich messages.
func Final(t *streamjson.Turn, footer string) []telegram.RichMessage {
	var bs []telegram.Block
	if strings.TrimSpace(t.Thinking) != "" {
		secs := int(t.ThinkingDur.Round(time.Second).Seconds())
		var paras []telegram.Block
		for _, p := range splitRunes(head(t.Thinking, finalThinkingRunes), chunkRunes) {
			paras = append(paras, telegram.Paragraph(p))
		}
		bs = append(bs, telegram.Details(fmt.Sprintf("💭 Thought for %ds", max(secs, 1)), false, paras...))
	}
	if n := len(t.Tools); n > 0 {
		word := "tool calls"
		if n == 1 {
			word = "tool call"
		}
		bs = append(bs, telegram.Details(fmt.Sprintf("🔧 %d %s", n, word), false, checklist(t.Tools)))
	}
	bs = append(bs, MarkdownBlocks(t.Text)...)
	if footer != "" {
		bs = append(bs, telegram.Footer(footer))
	}
	return pack(bs)
}

func size(v any) int { b, _ := json.Marshal(v); return len(b) }

// pack groups blocks into messages under MaxRichBytes, cutting any single oversize block.
func pack(bs []telegram.Block) []telegram.RichMessage {
	var out []telegram.RichMessage
	cur := telegram.RichMessage{}
	budget := MaxRichBytes - 64
	used := 0
	flush := func() {
		if len(cur.Blocks) > 0 {
			out = append(out, cur)
			cur, used = telegram.RichMessage{}, 0
		}
	}
	for _, b := range bs {
		for _, piece := range cut(b, budget) {
			s := size(piece) + 1
			if used+s > budget {
				flush()
			}
			cur.Blocks = append(cur.Blocks, piece)
			used += s
		}
	}
	flush()
	if len(out) == 0 {
		out = append(out, telegram.RichMessage{Blocks: []telegram.Block{telegram.Paragraph("(empty reply)")}})
	}
	return out
}

func cut(b telegram.Block, budget int) []telegram.Block {
	if size(b) <= budget {
		return []telegram.Block{b}
	}
	typ, _ := b["type"].(string)
	txt, isStr := b["text"].(string)
	if !isStr {
		raw, _ := json.Marshal(b)
		txt, typ = string(raw), "pre"
	}
	var out []telegram.Block
	for _, part := range splitRunes(txt, chunkRunes) {
		if typ == "pre" {
			lang, _ := b["language"].(string)
			out = append(out, telegram.Pre(part, lang))
		} else {
			out = append(out, telegram.Paragraph(part))
		}
	}
	return out
}

func splitRunes(s string, n int) []string {
	r := []rune(s)
	var out []string
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	if len(r) > 0 || len(out) == 0 {
		out = append(out, string(r))
	}
	return out
}

// Footer is the small status line under a final message.
func Footer(ctxTokens int, dur time.Duration, cost float64) string {
	var k string
	if ctxTokens >= 10000 {
		k = fmt.Sprintf("%dk", (ctxTokens+500)/1000)
	} else {
		k = strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", float64(ctxTokens)/1000), "0"), ".") + "k"
	}
	parts := []string{"ctx " + k, dur.Round(time.Second).String()}
	if cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", cost))
	}
	return strings.Join(parts, " · ")
}

// BlocksText flattens rich blocks to plain text (used when Telegram rejects a rich message).
func BlocksText(bs []telegram.Block) string {
	var sb strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			sb.WriteString(v)
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			for _, k := range []string{"summary", "text", "blocks", "items", "cells"} {
				if x, ok := v[k]; ok {
					walk(x)
				}
			}
		case []telegram.Block:
			for _, b := range v {
				walk(b)
				sb.WriteString("\n\n")
			}
		case []telegram.ListItem:
			for _, it := range v {
				sb.WriteString("• ")
				walk(it.Blocks)
			}
		case [][]telegram.Cell:
			for _, row := range v {
				for i, c := range row {
					if i > 0 {
						sb.WriteString(" | ")
					}
					walk(c.Text)
				}
				sb.WriteString("\n")
			}
		}
	}
	walk(bs)
	return strings.TrimSpace(sb.String())
}

// SplitText cuts s into pieces of at most n runes.
func SplitText(s string, n int) []string { return splitRunes(s, n) }
