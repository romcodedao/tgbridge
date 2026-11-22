// Package engine runs `claude -p` stream-json processes and multiplexes them by key (topic).
package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/streamjson"
)

type Spec struct {
	Key      string
	Cwd      string
	ResumeID string
	Binary   string
	Args     []string
	Env      []string
}

// BuildArgs returns the claude flags tgbridge always uses (verified on Claude Code 2.1.330).
func BuildArgs(c *config.Config, settingsPath, resumeID, systemPrompt string) []string {
	a := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--include-partial-messages", "--verbose",
		"--thinking-display", "summarized", "--permission-prompt-tool", "mcp__tgbridge__approve",
		"--settings", settingsPath, "--disallowedTools", "mcp__tgbridge__telegram_handoff"}
	if systemPrompt != "" {
		a = append(a, "--append-system-prompt", systemPrompt)
	}
	if c.Model != "" {
		a = append(a, "--model", c.Model)
	}
	if resumeID != "" {
		a = append(a, "--resume", resumeID)
	}
	return a
}

// CleanEnv drops Orca and Claude session variables so the child is a fresh, non-Orca session,
// and tgbridge's own (TGBRIDGE_TOKEN must never reach claude, its MCP servers or approved commands).
func CleanEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "ORCA_") || strings.HasPrefix(kv, "CLAUDE_CODE_") || strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "TGBRIDGE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

type Session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan streamjson.Event
	done   chan struct{}

	mu        sync.Mutex
	wmu       sync.Mutex // serializes stdin writes
	busy      bool
	closing   bool
	sessionID string
	stderr    strings.Builder
}

func Start(ctx context.Context, sp Spec) (*Session, error) {
	cmd := exec.Command(sp.Binary, sp.Args...)
	cmd.Dir, cmd.Env = sp.Cwd, sp.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &Session{cmd: cmd, stdin: in, events: make(chan streamjson.Event, 512), done: make(chan struct{}), sessionID: sp.ResumeID}
	cmd.Stderr = &limited{s: s}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	go s.read(out)
	return s, nil
}

type limited struct{ s *Session }

func (l *limited) Write(p []byte) (int, error) {
	l.s.mu.Lock()
	defer l.s.mu.Unlock()
	if l.s.stderr.Len() < 4096 {
		l.s.stderr.Write(p[:min(len(p), 4096-l.s.stderr.Len())])
	}
	return len(p), nil
}

func (s *Session) emit(e streamjson.Event) {
	if m, ok := e.(streamjson.Multi); ok {
		for _, x := range m {
			s.emit(x)
		}
		return
	}
	switch e := e.(type) {
	case streamjson.Init:
		s.mu.Lock()
		s.sessionID = e.SessionID
		s.mu.Unlock()
	case streamjson.Result:
		s.mu.Lock()
		s.busy = false
		if e.SessionID != "" {
			s.sessionID = e.SessionID
		}
		s.mu.Unlock()
	case streamjson.Other:
		return
	}
	s.events <- e
}

func (s *Session) read(out io.Reader) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if ev, err := streamjson.Parse(sc.Bytes()); err == nil {
			s.emit(ev)
		}
	}
	err := s.cmd.Wait()
	s.mu.Lock()
	crashed := !s.closing && (s.busy || err != nil)
	msg := strings.TrimSpace(s.stderr.String())
	s.busy = false
	s.mu.Unlock()
	if crashed {
		if msg == "" && err != nil {
			msg = err.Error()
		}
		s.events <- streamjson.Crashed{Err: msg}
	}
	close(s.events)
	close(s.done)
}

func (s *Session) Events() <-chan streamjson.Event { return s.events }

func (s *Session) SessionID() string { s.mu.Lock(); defer s.mu.Unlock(); return s.sessionID }

func (s *Session) Busy() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.busy }

func (s *Session) Send(text string) error {
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.stdin.Write(streamjson.UserLine(text))
	return err
}

// Interrupt asks the running turn to stop; if no result arrives within 5s the process gets SIGINT.
func (s *Session) Interrupt() error {
	s.wmu.Lock()
	_, err := s.stdin.Write(streamjson.InterruptLine(fmt.Sprintf("int-%d", time.Now().UnixNano())))
	s.wmu.Unlock()
	go func() {
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			if s.Busy() {
				s.cmd.Process.Signal(syscall.SIGINT)
			}
		}
	}()
	return err
}

// Close ends the process: close stdin, wait up to 5s, then kill the process group.
func (s *Session) Close() error {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.stdin.Close()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		<-s.done
	}
	return nil
}
