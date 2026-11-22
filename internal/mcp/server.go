// Package mcp is a minimal stdio MCP server exposing tgbridge's tools to Claude.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"sync"

	"github.com/romcodedao/tgbridge/internal/ipc"
)

type rpc struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var tools = []tool{
	{"approve", "Internal: ask the tgbridge owner on Telegram to allow or deny a tool call.",
		obj(map[string]any{"tool_name": map[string]any{"type": "string"}, "input": map[string]any{"type": "object"}, "tool_use_id": map[string]any{"type": "string"}}, "tool_name", "input")},
	{"telegram_handoff", "Hand this Orca terminal session off to the owner's Telegram (state=on) or stop (state=off). Use when the user says e.g. 'notify me on Telegram' or 'stop notifying Telegram'.",
		obj(map[string]any{"state": map[string]any{"type": "string", "enum": []string{"on", "off"}}}, "state")},
	{"telegram_send", "Send a short message to the owner's Telegram chat.",
		obj(map[string]any{"text": map[string]any{"type": "string"}}, "text")},
	{"telegram_status", "Show whether this session is handed off to Telegram.", obj(map[string]any{})},
}

var opFor = map[string]string{"approve": "approve", "telegram_handoff": "handoff", "telegram_send": "send", "telegram_status": "status"}

// Serve runs the JSON-RPC loop until in ends. Tool calls run concurrently (approve can block for minutes).
func Serve(in io.Reader, out io.Writer, call func(ipc.Request) (ipc.Response, error)) {
	ServeContext(in, out, func(_ context.Context, r ipc.Request) (ipc.Response, error) { return call(r) })
}

// ServeContext is Serve with per-call contexts: notifications/cancelled (or stdin closing) cancels a call.
func ServeContext(in io.Reader, out io.Writer, call func(context.Context, ipc.Request) (ipc.Response, error)) {
	root, stopAll := context.WithCancel(context.Background())
	defer stopAll()
	var cmu sync.Mutex
	cancels := map[string]context.CancelFunc{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	write := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		out.Write(append(b, '\n'))
		mu.Unlock()
	}
	reply := func(id json.RawMessage, result any) {
		write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r rpc
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if len(r.ID) == 0 { // notification
			if r.Method == "notifications/cancelled" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				json.Unmarshal(r.Params, &p)
				cmu.Lock()
				if c := cancels[string(p.RequestID)]; c != nil {
					c()
				}
				cmu.Unlock()
			}
			continue
		}
		switch r.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(r.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			reply(r.ID, map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": "tgbridge", "version": "1"}})
		case "ping":
			reply(r.ID, map[string]any{})
		case "tools/list":
			reply(r.ID, map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			json.Unmarshal(r.Params, &p)
			id := r.ID
			ctx, cancel := context.WithCancel(root)
			cmu.Lock()
			cancels[string(id)] = cancel
			cmu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				res := callTool(ctx, p.Name, p.Arguments, call)
				cmu.Lock()
				delete(cancels, string(id))
				cmu.Unlock()
				cancel()
				reply(id, res)
			}()
		default:
			write(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	stopAll() // stdin closed: Claude is gone, abandon pending calls
	wg.Wait()
}

func callTool(ctx context.Context, name string, args json.RawMessage, call func(context.Context, ipc.Request) (ipc.Response, error)) map[string]any {
	text := func(s string, isErr bool) map[string]any {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": s}}, "isError": isErr}
	}
	op, ok := opFor[name]
	if !ok {
		return text("unknown tool "+name, true)
	}
	thread, _ := strconv.Atoi(os.Getenv("TGBRIDGE_THREAD"))
	resp, err := call(ctx, ipc.Request{Op: op, SessionID: os.Getenv("CLAUDE_CODE_SESSION_ID"), TerminalHandle: os.Getenv("ORCA_TERMINAL_HANDLE"), Thread: thread, Args: args})
	if err != nil {
		if ctx.Err() != nil {
			return text("cancelled", true)
		}
		if resp.Error != "" {
			return text(resp.Error, true)
		}
		if name == "approve" {
			return text(`{"behavior":"deny","message":"tgbridge daemon not running"}`, false)
		}
		return text("tgbridge daemon not running", true)
	}
	return text(resp.Text, false)
}
