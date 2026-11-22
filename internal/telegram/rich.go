package telegram

// RichMessage is InputRichMessage with blocks. A Block is one InputRichBlock as a JSON object;
// RichText values are a string, an inline node map, or a []any mixing both.
type RichMessage struct {
	Blocks []Block `json:"blocks"`
}

type Block = map[string]any

// ListItem is one InputRichBlockListItem.
type ListItem struct {
	Blocks      []Block `json:"blocks"`
	HasCheckbox bool    `json:"has_checkbox,omitempty"`
	IsChecked   bool    `json:"is_checked,omitempty"`
}

// Cell is one RichBlockTableCell.
type Cell struct {
	Text     any    `json:"text,omitempty"`
	IsHeader bool   `json:"is_header,omitempty"`
	Align    string `json:"align"`
	Valign   string `json:"valign"`
}

func Paragraph(text any) Block         { return Block{"type": "paragraph", "text": text} }
func Heading(size int, text any) Block { return Block{"type": "heading", "size": size, "text": text} }
func Thinking(text any) Block          { return Block{"type": "thinking", "text": text} }
func Footer(text any) Block            { return Block{"type": "footer", "text": text} }
func Divider() Block                   { return Block{"type": "divider"} }

func Pre(text, lang string) Block {
	b := Block{"type": "pre", "text": text}
	if lang != "" {
		b["language"] = lang
	}
	return b
}

func List(items ...ListItem) Block { return Block{"type": "list", "items": items} }

func Details(summary any, open bool, blocks ...Block) Block {
	b := Block{"type": "details", "summary": summary, "blocks": blocks}
	if open {
		b["is_open"] = true
	}
	return b
}

func Table(rows [][]Cell) Block { return Block{"type": "table", "cells": rows, "is_compact": true} }

// Inline rich-text nodes.
func Bold(t any) map[string]any   { return map[string]any{"type": "bold", "text": t} }
func Italic(t any) map[string]any { return map[string]any{"type": "italic", "text": t} }
func Code(t any) map[string]any   { return map[string]any{"type": "code", "text": t} }
