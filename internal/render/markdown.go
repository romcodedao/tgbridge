// Package render turns Claude turns into Telegram rich drafts, rich messages and HTML fallbacks.
package render

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/romcodedao/tgbridge/internal/telegram"
)

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// MarkdownBlocks converts Claude's Markdown into rich blocks.
func MarkdownBlocks(src string) []telegram.Block {
	b := []byte(src)
	doc := md.Parser().Parse(text.NewReader(b))
	var out []telegram.Block
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		out = append(out, block(n, b)...)
	}
	return out
}

func block(n ast.Node, src []byte) []telegram.Block {
	switch n := n.(type) {
	case *ast.Heading:
		return []telegram.Block{telegram.Heading(n.Level, inline(n, src))}
	case *ast.Paragraph, *ast.TextBlock:
		return []telegram.Block{telegram.Paragraph(inline(n, src))}
	case *ast.FencedCodeBlock:
		return []telegram.Block{telegram.Pre(lines(n, src), string(n.Language(src)))}
	case *ast.CodeBlock:
		return []telegram.Block{telegram.Pre(lines(n, src), "")}
	case *ast.ThematicBreak:
		return []telegram.Block{telegram.Divider()}
	case *ast.List:
		var items []telegram.ListItem
		for li := n.FirstChild(); li != nil; li = li.NextSibling() {
			var bs []telegram.Block
			for c := li.FirstChild(); c != nil; c = c.NextSibling() {
				bs = append(bs, block(c, src)...)
			}
			if len(bs) == 0 {
				bs = []telegram.Block{telegram.Paragraph("")}
			}
			items = append(items, telegram.ListItem{Blocks: bs})
		}
		return []telegram.Block{telegram.List(items...)}
	case *ast.Blockquote:
		var bs []telegram.Block
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			bs = append(bs, block(c, src)...)
		}
		return []telegram.Block{{"type": "blockquote", "blocks": bs}}
	case *east.Table:
		var rows [][]telegram.Cell
		cols := 0
		for r := n.FirstChild(); r != nil; r = r.NextSibling() {
			_, header := r.(*east.TableHeader)
			var row []telegram.Cell
			for c := r.FirstChild(); c != nil; c = c.NextSibling() {
				row = append(row, telegram.Cell{Text: inline(c, src), IsHeader: header, Align: "left", Valign: "top"})
			}
			cols = max(cols, len(row))
			rows = append(rows, row)
		}
		if cols > 20 {
			return []telegram.Block{telegram.Pre(string(n.Lines().Value(src)), "")}
		}
		return []telegram.Block{telegram.Table(rows)}
	case *ast.HTMLBlock:
		return []telegram.Block{telegram.Pre(lines(n, src), "html")}
	}
	if t := plain(n, src); strings.TrimSpace(t) != "" {
		return []telegram.Block{telegram.Paragraph(t)}
	}
	return nil
}

func lines(n ast.Node, src []byte) string {
	var sb strings.Builder
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		s := l.At(i)
		sb.Write(s.Value(src))
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// inline returns RichText: a plain string when there is no formatting, else []any of strings and nodes.
func inline(n ast.Node, src []byte) any {
	var parts []any
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		parts = append(parts, inlineNode(c, src)...)
	}
	return compact(parts)
}

func compact(parts []any) any {
	var out []any
	for _, p := range parts {
		if s, ok := p.(string); ok && len(out) > 0 {
			if prev, ok := out[len(out)-1].(string); ok {
				out[len(out)-1] = prev + s
				continue
			}
		}
		out = append(out, p)
	}
	switch len(out) {
	case 0:
		return ""
	case 1:
		if s, ok := out[0].(string); ok {
			return s
		}
	}
	return out
}

func inlineNode(n ast.Node, src []byte) []any {
	switch n := n.(type) {
	case *ast.Text:
		s := string(n.Segment.Value(src))
		if n.HardLineBreak() || n.SoftLineBreak() {
			s += "\n"
		}
		return []any{s}
	case *ast.String:
		return []any{string(n.Value)}
	case *ast.CodeSpan:
		return []any{telegram.Code(plain(n, src))}
	case *ast.Emphasis:
		if n.Level >= 2 {
			return []any{telegram.Bold(inline(n, src))}
		}
		return []any{telegram.Italic(inline(n, src))}
	case *east.Strikethrough:
		return []any{map[string]any{"type": "strikethrough", "text": inline(n, src)}}
	case *ast.Link:
		return []any{map[string]any{"type": "url", "text": inline(n, src), "url": string(n.Destination)}}
	case *ast.AutoLink:
		u := string(n.URL(src))
		return []any{map[string]any{"type": "url", "text": u, "url": u}}
	case *ast.RawHTML:
		var sb strings.Builder
		for i := 0; i < n.Segments.Len(); i++ {
			s := n.Segments.At(i)
			sb.Write(s.Value(src))
		}
		return []any{sb.String()}
	}
	var out []any
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, inlineNode(c, src)...)
	}
	return out
}

func plain(n ast.Node, src []byte) string {
	var sb strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			sb.Write(c.Segment.Value(src))
		case *ast.String:
			sb.Write(c.Value)
		}
		return ast.WalkContinue, nil
	})
	return sb.String()
}
