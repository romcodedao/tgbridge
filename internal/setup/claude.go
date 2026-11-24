package setup

import (
	"context"
	"encoding/json"
	"strings"
)

// ClaudeInfo describes the local Claude Code install.
type ClaudeInfo struct {
	Path, Version string
	LoggedIn      bool
	AuthMethod    string
	AuthErr       error // the login state could not be read
}

// DetectClaude finds bin with look and reads its version and login state. The error means not found.
func DetectClaude(ctx context.Context, bin string, look func(string) (string, error), run Runner) (ClaudeInfo, error) {
	p, err := look(bin)
	if err != nil {
		return ClaudeInfo{}, err
	}
	ci := ClaudeInfo{Path: p}
	if out, err := run(ctx, p, "--version"); err == nil {
		ci.Version, _, _ = strings.Cut(strings.TrimSpace(out), "\n")
	}
	out, err := run(ctx, p, "auth", "status")
	var st struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if err == nil {
		err = json.Unmarshal([]byte(out), &st)
	}
	ci.LoggedIn, ci.AuthMethod, ci.AuthErr = st.LoggedIn, st.AuthMethod, err
	return ci, nil
}

const loginFix = "run `claude`, then /login"

// ClaudeChecks turns DetectClaude's result into check rows.
func ClaudeChecks(ci ClaudeInfo, err error) []Check {
	if err != nil {
		return []Check{{Fail, "claude", "not found on PATH", "install Claude Code: https://docs.claude.com/en/docs/claude-code/setup"}}
	}
	cs := []Check{{OK, "claude", strings.TrimSpace(ci.Path + " " + ci.Version), ""}}
	switch {
	case ci.AuthErr != nil:
		cs = append(cs, Check{Warn, "claude login", "couldn't check login", loginFix})
	case !ci.LoggedIn:
		cs = append(cs, Check{Fail, "claude login", "not logged in", loginFix})
	default:
		cs = append(cs, Check{OK, "claude login", "logged in (" + ci.AuthMethod + ")", ""})
	}
	return cs
}
