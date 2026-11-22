package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/romcodedao/tgbridge/internal/streamjson"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

func types(bs []telegram.Block) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b["type"].(string))
	}
	return out
}

func TestMarkdownBlocks(t *testing.T) {
	bs := MarkdownBlocks("# T\n\nhi **b** and [l](https://x.io)\n\n```go\nx\n```\n\n- a\n- b")
	if got := strings.Join(types(bs), ","); got != "heading,paragraph,pre,list" {
		t.Fatalf("types=%s", got)
	}
	if bs[2]["language"] != "go" || bs[2]["text"] != "x" {
		t.Fatalf("pre=%v", bs[2])
	}
	j, _ := json.Marshal(bs[1]["text"])
	if !strings.Contains(string(j), `"type":"bold"`) || !strings.Contains(string(j), `"url":"https://x.io"`) {
		t.Fatalf("paragraph=%s", j)
	}
}

func TestMarkdownTable(t *testing.T) {
	bs := MarkdownBlocks("| a | b |\n|---|---|\n| 1 | 2 |\n")
	if len(bs) != 1 || bs[0]["type"] != "table" {
		t.Fatalf("got %v", types(bs))
	}
	rows := bs[0]["cells"].([][]telegram.Cell)
	if len(rows) != 2 || !rows[0][0].IsHeader || rows[1][1].Text != "2" {
		t.Fatalf("rows=%+v", rows)
	}
}

func turn() *streamjson.Turn {
	return &streamjson.Turn{Thinking: "let me think", Text: "**done**", ThinkingDur: 3 * time.Second,
		Tools: []streamjson.ToolCall{{ID: "1", Name: "Bash", Summary: "echo hi", Done: true}, {ID: "2", Name: "Read", Summary: "/a"}}}
}

func TestDraftShowsThinkingAndChecklist(t *testing.T) {
	bs := Draft(turn()).Blocks
	if bs[0]["type"] != "thinking" || bs[1]["type"] != "list" {
		t.Fatalf("types=%v", types(bs))
	}
	items := bs[1]["items"].([]telegram.ListItem)
	if !items[0].HasCheckbox || !items[0].IsChecked || items[1].IsChecked {
		t.Fatalf("items=%+v", items)
	}
}

func TestDraftEmptyShowsPlaceholder(t *testing.T) {
	bs := Draft(&streamjson.Turn{}).Blocks
	if len(bs) != 1 || bs[0]["type"] != "thinking" || bs[0]["text"] != "Thinking…" {
		t.Fatalf("%v", bs)
	}
}

func TestFinalCollapsesThinking(t *testing.T) {
	msgs := Final(turn(), "ctx 1k")
	bs := msgs[0].Blocks
	if bs[0]["type"] != "details" || bs[0]["is_open"] != nil || !strings.HasPrefix(bs[0]["summary"].(string), "💭 Thought for") {
		t.Fatalf("first=%v", bs[0])
	}
	if bs[1]["type"] != "details" || !strings.Contains(bs[1]["summary"].(string), "2 tool calls") {
		t.Fatalf("second=%v", bs[1])
	}
	last := msgs[len(msgs)-1].Blocks
	if last[len(last)-1]["type"] != "footer" {
		t.Fatal("no footer")
	}
}

func TestFinalSplitsOversizeAnswer(t *testing.T) {
	var sb strings.Builder
	for i := 0; sb.Len() < 100000; i++ {
		sb.WriteString("paragraph number ")
		sb.WriteString(strings.Repeat("x", 300))
		sb.WriteString("\n\n")
	}
	tr := &streamjson.Turn{Text: sb.String()}
	msgs := Final(tr, "f")
	if len(msgs) < 3 {
		t.Fatalf("msgs=%d", len(msgs))
	}
	var all strings.Builder
	for _, m := range msgs {
		j, _ := json.Marshal(m)
		if len(j) > MaxRichBytes {
			t.Fatalf("message %d bytes", len(j))
		}
		for _, b := range m.Blocks {
			if b["type"] == "paragraph" {
				all.WriteString(b["text"].(string))
			}
		}
	}
	if strings.Count(all.String(), "paragraph number") != strings.Count(sb.String(), "paragraph number") {
		t.Fatal("lost paragraphs")
	}
}

func TestFinalCutsHugeSingleBlock(t *testing.T) {
	tr := &streamjson.Turn{Text: "```\n" + strings.Repeat("y", 70000) + "\n```"}
	msgs := Final(tr, "f")
	total := 0
	for _, m := range msgs {
		j, _ := json.Marshal(m)
		if len(j) > MaxRichBytes {
			t.Fatalf("message %d bytes", len(j))
		}
		for _, b := range m.Blocks {
			if b["type"] == "pre" {
				total += len(b["text"].(string))
			}
		}
	}
	if total != 70000 {
		t.Fatalf("pre total=%d", total)
	}
}

func TestFallbackHTMLEscapes(t *testing.T) {
	tr := &streamjson.Turn{Thinking: "t", Text: "<script>" + strings.Repeat("z", 9000)}
	chunks := FallbackHTML(tr, "f")
	if !strings.Contains(chunks[0], "&lt;script&gt;") || !strings.Contains(chunks[0], "<blockquote expandable>") {
		t.Fatalf("chunk0=%.200s", chunks[0])
	}
	for _, c := range chunks {
		if len([]rune(c)) > 4096 {
			t.Fatalf("chunk %d runes", len([]rune(c)))
		}
	}
}

func TestFooter(t *testing.T) {
	if got := Footer(64200, 134*time.Second, 0.123); got != "ctx 64k · 2m14s · $0.12" {
		t.Fatalf("got %q", got)
	}
	if got := Footer(900, 5*time.Second, 0); got != "ctx 0.9k · 5s" {
		t.Fatalf("got %q", got)
	}
}
