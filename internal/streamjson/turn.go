package streamjson

import "time"

type ToolCall struct {
	ID, Name, Summary string
	Done, Failed      bool
	Output            string
}

// Turn accumulates one user turn's output.
type Turn struct {
	SessionID   string
	Thinking    string
	Text        string
	Tools       []ToolCall
	Started     time.Time
	ThinkingDur time.Duration
	Result      *Result
	lastCtx     int
	thinkStart  time.Time
	thinkEnd    time.Time
}

func (t *Turn) tool(id string) *ToolCall {
	for i := range t.Tools {
		if t.Tools[i].ID == id {
			return &t.Tools[i]
		}
	}
	return nil
}

func (t *Turn) Apply(e Event) {
	now := time.Now()
	if t.Started.IsZero() {
		t.Started = now
	}
	switch e := e.(type) {
	case Multi:
		for _, x := range e {
			t.Apply(x)
		}
	case Init:
		t.SessionID = e.SessionID
	case ThinkingDelta:
		if t.thinkStart.IsZero() {
			t.thinkStart = now
		}
		t.thinkEnd = now
		t.ThinkingDur = t.thinkEnd.Sub(t.thinkStart)
		t.Thinking += e.Text
	case TextDelta:
		t.Text += e.Text
	case ToolStart:
		if t.tool(e.ID) == nil {
			t.Tools = append(t.Tools, ToolCall{ID: e.ID, Name: e.Name})
		}
		if t.Text != "" && !endsWithBreak(t.Text) {
			t.Text += "\n\n"
		}
	case ToolInput:
		tc := t.tool(e.ID)
		if tc == nil {
			t.Tools = append(t.Tools, ToolCall{ID: e.ID, Name: e.Name})
			tc = &t.Tools[len(t.Tools)-1]
		}
		tc.Summary = Summary(e.Name, e.Input)
	case ToolResult:
		if tc := t.tool(e.ID); tc != nil {
			tc.Done, tc.Failed, tc.Output = true, e.IsError, e.Output
		}
	case Usage:
		t.lastCtx = e.ContextTokens
	case Result:
		if t.lastCtx > 0 {
			e.ContextTokens = t.lastCtx // context of the last call, not the turn's sum
		}
		if t.SessionID == "" {
			t.SessionID = e.SessionID
		}
		t.Result = &e
	}
}

func endsWithBreak(s string) bool { return len(s) >= 2 && s[len(s)-2:] == "\n\n" }

// Summary is a tool call's short form for checklists, at most 80 runes.
func Summary(name string, in map[string]any) string {
	key := map[string]string{"Bash": "command", "Read": "file_path", "Edit": "file_path", "Write": "file_path",
		"NotebookEdit": "notebook_path", "Grep": "pattern", "Glob": "pattern", "WebFetch": "url", "WebSearch": "query", "Agent": "description", "Task": "description"}[name]
	s, _ := in[key].(string)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return s
}
