package streamjson

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func replay(t *testing.T, file string) *Turn {
	t.Helper()
	f, err := os.Open("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := &Turn{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		ev, err := Parse(sc.Bytes())
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		tr.Apply(ev)
	}
	return tr
}

func TestParseToolTurn(t *testing.T) {
	tr := replay(t, "turn_tool.jsonl")
	if len(tr.Tools) != 1 || tr.Tools[0].Name != "Bash" || tr.Tools[0].Summary != "echo hi" || !tr.Tools[0].Done || tr.Tools[0].Failed {
		t.Fatalf("tools=%+v", tr.Tools)
	}
	if strings.TrimSpace(tr.Text) == "" || tr.Result == nil || tr.Result.ContextTokens <= 0 || tr.Result.SessionID == "" {
		t.Fatalf("text=%q result=%+v", tr.Text, tr.Result)
	}
	if tr.SessionID == "" {
		t.Fatal("init session id missing")
	}
}

func TestParseThinkingTurn(t *testing.T) {
	tr := replay(t, "turn_thinking.jsonl")
	if !strings.HasPrefix(tr.Thinking, "I'm working out") {
		t.Fatalf("thinking=%q", tr.Thinking[:min(40, len(tr.Thinking))])
	}
	if tr.Text == "" {
		t.Fatal("no text")
	}
}

func TestUnknownTypeIsOther(t *testing.T) {
	ev, err := Parse([]byte(`{"type":"rate_limit_event"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ev.(Other); !ok {
		t.Fatalf("got %T", ev)
	}
}

func TestToolResultError(t *testing.T) {
	tr := &Turn{}
	tr.Apply(ToolStart{ID: "t1", Name: "Read"})
	tr.Apply(ToolInput{ID: "t1", Name: "Read", Input: map[string]any{"file_path": "/x/y.go"}})
	tr.Apply(ToolResult{ID: "t1", IsError: true, Output: "nope"})
	if tr.Tools[0].Summary != "/x/y.go" || !tr.Tools[0].Failed || !tr.Tools[0].Done {
		t.Fatalf("%+v", tr.Tools[0])
	}
}

func TestLines(t *testing.T) {
	var u map[string]any
	json.Unmarshal(UserLine("hi \"there\""), &u)
	if u["type"] != "user" || u["message"].(map[string]any)["content"] != `hi "there"` {
		t.Fatalf("%v", u)
	}
	var i map[string]any
	json.Unmarshal(InterruptLine("r1"), &i)
	if i["type"] != "control_request" || i["request"].(map[string]any)["subtype"] != "interrupt" {
		t.Fatalf("%v", i)
	}
}
