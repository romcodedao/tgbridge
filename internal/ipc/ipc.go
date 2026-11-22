// Package ipc is the daemon's local unix-socket protocol: one JSON request and response per connection.
package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"
)

type Request struct {
	Op             string          `json:"op"`
	SessionID      string          `json:"session_id,omitempty"`
	TerminalHandle string          `json:"terminal_handle,omitempty"`
	Thread         int             `json:"thread,omitempty"` // set for tgbridge's own engine sessions
	Args           json.RawMessage `json:"args,omitempty"`
}

type Response struct {
	Text   string          `json:"text,omitempty"`
	Error  string          `json:"error,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
}

type Handler func(context.Context, Request) Response

// Serve listens on path (mode 0600) until ctx ends.
func Serve(ctx context.Context, path string, h Handler) error {
	os.Remove(path)
	old := umask(0o177)
	ln, err := net.Listen("unix", path)
	umask(old)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	go func() { <-ctx.Done(); ln.Close(); os.Remove(path) }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			continue
		}
		go handle(ctx, c, h)
	}
}

func handle(ctx context.Context, c net.Conn, h Handler) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var req Request
	dec := json.NewDecoder(c)
	if err := dec.Decode(&req); err != nil {
		return
	}
	c.SetReadDeadline(time.Time{})
	// The caller sends nothing more; a read returning means it hung up, so cancel the work
	// (a permission prompt must not outlive the request behind it).
	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		var b [1]byte
		c.Read(b[:])
		cancel()
	}()
	resp := h(hctx, req)
	json.NewEncoder(c).Encode(resp)
}

// Call sends one request and waits for the response (ctx bounds the wait).
func Call(ctx context.Context, path string, req Request) (Response, error) {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	c, err := d.DialContext(dctx, "unix", path)
	cancel()
	if err != nil {
		return Response{}, err
	}
	defer c.Close()
	go func() { <-ctx.Done(); c.Close() }()
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		return Response{}, err
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
