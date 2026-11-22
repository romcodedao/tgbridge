package hookcmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/romcodedao/tgbridge/internal/ipc"
)

func TestShimDaemonDownIsNoop(t *testing.T) {
	var out strings.Builder
	start := time.Now()
	code := Run("PermissionRequest", strings.NewReader(`{"session_id":"s"}`), &out, func(ipc.Request) (ipc.Response, error) {
		return ipc.Response{}, errors.New("dial unix: no such file")
	})
	if code != 0 || strings.TrimSpace(out.String()) != "{}" || time.Since(start) > time.Second {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestHookPrintsDecision(t *testing.T) {
	t.Setenv("ORCA_TERMINAL_HANDLE", "term_9")
	var out strings.Builder
	var got ipc.Request
	Run("PermissionRequest", strings.NewReader(`{"session_id":"s1","tool_name":"Bash"}`), &out, func(r ipc.Request) (ipc.Response, error) {
		got = r
		return ipc.Response{Output: []byte(`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`)}, nil
	})
	if got.Op != "hook.PermissionRequest" || got.SessionID != "s1" || got.TerminalHandle != "term_9" || !strings.Contains(out.String(), `"behavior":"allow"`) {
		t.Fatalf("req=%+v out=%q", got, out.String())
	}
}
