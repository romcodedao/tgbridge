package orca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Terminal is one row of `orca terminal list --json`.
type Terminal struct {
	Handle       string `json:"handle"`
	TabID        string `json:"tabId"`
	LeafID       string `json:"leafId"`
	WorktreePath string `json:"worktreePath"`
	Title        string `json:"title"`
	Connected    bool   `json:"connected"`
	Writable     bool   `json:"writable"`
}

// CLI runs the Orca CLI. Flags verified against Orca 1.4.267 (`--help` of each command).
type CLI struct{ Bin string }

func New(bin string) *CLI { return &CLI{Bin: bin} }

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  any             `json:"error"`
}

func (c *CLI) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.Bin, args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("orca %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)+string(out)))
		}
		return out, fmt.Errorf("orca %s: %w", args[0], err)
	}
	return out, nil
}

func (c *CLI) json(ctx context.Context, timeout time.Duration, out any, args ...string) error {
	b, err := c.run(ctx, timeout, append(args, "--json")...)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("orca %s: bad JSON", args[0])
	}
	if !env.OK {
		return fmt.Errorf("orca %s: %v", args[0], env.Error)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func (c *CLI) List(ctx context.Context) ([]Agent, error) {
	var r struct {
		Agents []Agent `json:"agents"`
	}
	return r.Agents, c.json(ctx, 15*time.Second, &r, "agent", "list")
}

func (c *CLI) Terminals(ctx context.Context) ([]Terminal, error) {
	var ts []Terminal
	return ts, c.json(ctx, 15*time.Second, &ts, "terminal", "list")
}

func (c *CLI) Read(ctx context.Context, id string, limit int) (string, error) {
	b, err := c.run(ctx, 15*time.Second, "agent", "read", id, "--limit", strconv.Itoa(limit))
	return string(b), err
}

// Send types text into the terminal and presses Enter (bracketed paste for a running agent).
func (c *CLI) Send(ctx context.Context, handle, text string) error {
	return c.json(ctx, 20*time.Second, nil, "terminal", "send", "--terminal", handle, "--text", text, "--enter")
}

// WaitIdle reports whether the agent TUI in the terminal becomes idle within timeout.
func (c *CLI) WaitIdle(ctx context.Context, handle string, timeout time.Duration) (bool, error) {
	err := c.json(ctx, timeout+5*time.Second, nil, "terminal", "wait", "--terminal", handle, "--for", "tui-idle", "--timeout-ms", strconv.Itoa(int(timeout.Milliseconds())))
	return err == nil, nil
}

func (c *CLI) CloseTerminal(ctx context.Context, handle string) error {
	return c.json(ctx, 15*time.Second, nil, "terminal", "close", "--terminal", handle)
}
