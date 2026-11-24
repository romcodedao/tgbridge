// Package install registers tgbridge's hooks and MCP server with Claude Code, leaving everything else as it is.
package install

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

// Hook timeouts (seconds). PermissionRequest waits for the owner's tap up to handoff_permission_timeout (10m).
var events = map[string]int{"Stop": 30, "Notification": 30, "PermissionRequest": 660}
var order = []string{"Stop", "Notification", "PermissionRequest"}

func ours(entry any, ev string) bool {
	e, _ := entry.(map[string]any)
	hs, _ := e["hooks"].([]any)
	for _, h := range hs {
		c, _ := h.(map[string]any)["command"].(string)
		if strings.HasSuffix(c, "tgbridge hook "+ev) || strings.Contains(c, "/tgbridge hook "+ev) {
			return true
		}
	}
	return false
}

func load(path string) (map[string]any, []byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	m := map[string]any{}
	if len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, nil, err
		}
	}
	return m, b, nil
}

func save(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tgbridge.tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// strip removes tgbridge's entries from every event, dropping events left empty.
func strip(hooks map[string]any) {
	for _, ev := range order {
		list, _ := hooks[ev].([]any)
		var keep []any
		for _, e := range list {
			if !ours(e, ev) {
				keep = append(keep, e)
			}
		}
		if len(keep) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = keep
		}
	}
}

// Install appends `<bin> hook <Event>` entries to settingsPath (idempotent, backup kept) and runs
// `claude mcp add --scope user tgbridge -- <bin> mcp` through run.
func Install(settingsPath, bin string, run func(args ...string) error) error {
	m, orig, err := load(settingsPath)
	if err != nil {
		return err
	}
	if orig != nil {
		if err := os.WriteFile(settingsPath+".tgbridge.bak", orig, 0o600); err != nil {
			return err
		}
	}
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	strip(hooks)
	for _, ev := range order {
		list, _ := hooks[ev].([]any)
		hooks[ev] = append(list, map[string]any{"matcher": "*", "hooks": []any{map[string]any{"type": "command", "command": bin + " hook " + ev, "timeout": events[ev]}}})
	}
	m["hooks"] = hooks
	if err := save(settingsPath, m); err != nil {
		return err
	}
	run("mcp", "remove", "--scope", "user", "tgbridge") // ignore: may not exist yet
	return run("mcp", "add", "--scope", "user", "tgbridge", "--", bin, "mcp")
}

// Uninstall removes only tgbridge's hooks and MCP server; a missing settings file stays missing.
func Uninstall(settingsPath string, run func(args ...string) error) error {
	m, orig, err := load(settingsPath)
	if err != nil {
		return err
	}
	if orig == nil {
		run("mcp", "remove", "--scope", "user", "tgbridge")
		return nil
	}
	if hooks, ok := m["hooks"].(map[string]any); ok {
		strip(hooks)
		if len(hooks) == 0 {
			delete(m, "hooks")
		}
	}
	if err := save(settingsPath, m); err != nil {
		return err
	}
	run("mcp", "remove", "--scope", "user", "tgbridge")
	return nil
}
