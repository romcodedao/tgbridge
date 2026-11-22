// Package hookcmd implements `tgbridge hook <event>`: forward a Claude Code hook payload to the daemon.
package hookcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/romcodedao/tgbridge/internal/ipc"
)

// Run always returns 0 and prints `{}` unless the daemon returned a decision, so it never blocks or breaks Claude.
func Run(event string, stdin io.Reader, stdout io.Writer, call func(ipc.Request) (ipc.Response, error)) int {
	payload, _ := io.ReadAll(io.LimitReader(stdin, 4<<20))
	var p struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(payload, &p)
	if !json.Valid(payload) {
		payload = []byte("{}")
	}
	resp, err := call(ipc.Request{Op: "hook." + event, SessionID: p.SessionID, TerminalHandle: os.Getenv("ORCA_TERMINAL_HANDLE"), Args: payload})
	if err != nil || len(resp.Output) == 0 || !json.Valid(resp.Output) {
		fmt.Fprintln(stdout, "{}")
		return 0
	}
	fmt.Fprintln(stdout, string(resp.Output))
	return 0
}
