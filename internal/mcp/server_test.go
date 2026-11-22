package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/romcodedao/tgbridge/internal/ipc"
)

func run(t *testing.T, input string, call func(ipc.Request) (ipc.Response, error)) []map[string]any {
	t.Helper()
	var out strings.Builder
	// like Claude, keep stdin open while calls are in flight (EOF cancels pending calls)
	Serve(io.MultiReader(strings.NewReader(input), slowEOF{}), &out, call)
	var res []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad line %q", sc.Text())
		}
		res = append(res, m)
	}
	return res
}

const initLine = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
	`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"

func TestMCPToolsList(t *testing.T) {
	res := run(t, initLine+`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n", nil)
	if len(res) != 2 {
		t.Fatalf("responses=%d %v", len(res), res)
	}
	if res[0]["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("init=%v", res[0])
	}
	var names []string
	for _, tl := range res[1]["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "approve,telegram_handoff,telegram_send,telegram_status" {
		t.Fatalf("names=%v", names)
	}
}

func TestMCPApproveReturnsBehaviorJSON(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-9")
	t.Setenv("ORCA_TERMINAL_HANDLE", "term_1")
	t.Setenv("TGBRIDGE_THREAD", "42")
	var got ipc.Request
	res := run(t, initLine+`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"ls"}}}}`+"\n",
		func(r ipc.Request) (ipc.Response, error) {
			got = r
			return ipc.Response{Text: `{"behavior":"allow","updatedInput":{"command":"ls"}}`}, nil
		})
	if got.Op != "approve" || got.SessionID != "sess-9" || got.TerminalHandle != "term_1" || got.Thread != 42 {
		t.Fatalf("req=%+v", got)
	}
	content := res[1]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
	var beh map[string]any
	json.Unmarshal([]byte(content["text"].(string)), &beh)
	if beh["behavior"] != "allow" {
		t.Fatalf("content=%v", content)
	}
}

func TestMCPDaemonDownIsError(t *testing.T) {
	res := run(t, initLine+`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"telegram_send","arguments":{"text":"x"}}}`+"\n",
		func(ipc.Request) (ipc.Response, error) { return ipc.Response{}, errors.New("dial") })
	r := res[1]["result"].(map[string]any)
	if r["isError"] != true || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), "tgbridge daemon not running") {
		t.Fatalf("r=%v", r)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	res := run(t, `{"jsonrpc":"2.0","id":5,"method":"bogus"}`+"\n", nil)
	if res[0]["error"] == nil {
		t.Fatalf("%v", res[0])
	}
}

func TestCancelledNotificationCancelsCall(t *testing.T) {
	pr, pw := io.Pipe()
	var out strings.Builder
	done := make(chan struct{})
	gotCancel := make(chan struct{})
	go func() {
		ServeContext(pr, &out, func(ctx context.Context, r ipc.Request) (ipc.Response, error) {
			<-ctx.Done()
			close(gotCancel)
			return ipc.Response{}, ctx.Err()
		})
		close(done)
	}()
	io.WriteString(pw, initLine+`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{}}}}`+"\n")
	time.Sleep(50 * time.Millisecond)
	io.WriteString(pw, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`+"\n")
	select {
	case <-gotCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("call not cancelled")
	}
	pw.Close()
	<-done
	if !strings.Contains(out.String(), "cancelled") || strings.Contains(out.String(), "not running") {
		t.Fatalf("cancelled call reported wrongly: %s", out.String())
	}
}

type slowEOF struct{}

func (slowEOF) Read([]byte) (int, error) { time.Sleep(300 * time.Millisecond); return 0, io.EOF }
