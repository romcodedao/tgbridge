// Package streamjson parses `claude -p --output-format stream-json` lines.
package streamjson

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Event interface{ isEvent() }

type (
	Init          struct{ SessionID string }
	ThinkingDelta struct{ Text string }
	TextDelta     struct{ Text string }
	ToolStart     struct{ ID, Name string }
	ToolInput     struct {
		ID, Name string
		Input    map[string]any
	}
	ToolResult struct {
		ID      string
		IsError bool
		Output  string
	}
	// Usage is the context size of the latest API call (input + cache creation + cache read).
	Usage  struct{ ContextTokens int }
	Result struct {
		SessionID     string
		IsError       bool
		Subtype       string
		ContextTokens int
		OutputTokens  int
		CostUSD       float64
		Duration      time.Duration
		Text          string
	}
	// Crashed is emitted by the engine when the process exits mid-turn.
	Crashed struct{ Err string }
	// Queued is emitted by the engine manager when no process slot is free.
	Queued struct{}
	Other  struct{ Type string }
)

func (Init) isEvent()          {}
func (ThinkingDelta) isEvent() {}
func (TextDelta) isEvent()     {}
func (ToolStart) isEvent()     {}
func (ToolInput) isEvent()     {}
func (ToolResult) isEvent()    {}
func (Usage) isEvent()         {}
func (Result) isEvent()        {}
func (Crashed) isEvent()       {}
func (Queued) isEvent()        {}
func (Other) isEvent()         {}

type usage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

func (u usage) context() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type line struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Event     *struct {
		Type         string `json:"type"`
		ContentBlock *block `json:"content_block"`
		Delta        *struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	} `json:"event"`
	Message *struct {
		Content json.RawMessage `json:"content"`
		Usage   *usage          `json:"usage"`
	} `json:"message"`
	IsError      bool    `json:"is_error"`
	Usage        *usage  `json:"usage"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	Result       string  `json:"result"`
}

// Parse converts one line into an Event. A line can carry several facts (an assistant message with
// tool_use blocks); Parse returns them as a Multi.
func Parse(b []byte) (Event, error) {
	var l line
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("stream-json: %w", err)
	}
	switch l.Type {
	case "system":
		if l.Subtype == "init" {
			return Init{SessionID: l.SessionID}, nil
		}
	case "stream_event":
		if l.Event == nil {
			break
		}
		switch l.Event.Type {
		case "content_block_start":
			if cb := l.Event.ContentBlock; cb != nil && cb.Type == "tool_use" {
				return ToolStart{ID: cb.ID, Name: cb.Name}, nil
			}
		case "content_block_delta":
			if d := l.Event.Delta; d != nil {
				switch d.Type {
				case "text_delta":
					return TextDelta{Text: d.Text}, nil
				case "thinking_delta":
					if d.Thinking != "" {
						return ThinkingDelta{Text: d.Thinking}, nil
					}
				}
			}
		}
	case "assistant":
		if l.Message == nil {
			break
		}
		var m Multi
		var blocks []block
		json.Unmarshal(l.Message.Content, &blocks)
		for _, bl := range blocks {
			if bl.Type == "tool_use" {
				m = append(m, ToolInput{ID: bl.ID, Name: bl.Name, Input: bl.Input})
			}
		}
		if l.Message.Usage != nil {
			m = append(m, Usage{ContextTokens: l.Message.Usage.context()})
		}
		return m, nil
	case "user":
		if l.Message == nil {
			break
		}
		var m Multi
		var blocks []block
		json.Unmarshal(l.Message.Content, &blocks)
		for _, bl := range blocks {
			if bl.Type == "tool_result" {
				m = append(m, ToolResult{ID: bl.ToolUseID, IsError: bl.IsError, Output: contentText(bl.Content)})
			}
		}
		return m, nil
	case "result":
		r := Result{SessionID: l.SessionID, IsError: l.IsError || l.Subtype != "success", Subtype: l.Subtype,
			CostUSD: l.TotalCostUSD, Duration: time.Duration(l.DurationMS) * time.Millisecond, Text: l.Result}
		if l.Usage != nil {
			r.ContextTokens, r.OutputTokens = l.Usage.context(), l.Usage.OutputTokens
		}
		return r, nil
	}
	return Other{Type: l.Type}, nil
}

// Multi groups several events from one line.
type Multi []Event

func (Multi) isEvent() {}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &parts)
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// UserLine is one stream-json input line carrying a user message.
func UserLine(text string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	return append(b, '\n')
}

// InterruptLine asks the running turn to stop.
func InterruptLine(id string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "interrupt"}})
	return append(b, '\n')
}
