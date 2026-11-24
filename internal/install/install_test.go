package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const orcaHook = `{"model":"opus","hooks":{"PermissionRequest":[{"matcher":"*","hooks":[{"type":"command","command":"orca-hook PermissionRequest"}]}],"Stop":[{"hooks":[{"type":"command","command":"orca-hook Stop"}]}]}}`

func read(t *testing.T, p string) map[string]any {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func entries(m map[string]any, ev string) []any { return m["hooks"].(map[string]any)[ev].([]any) }

func cmdOf(e any) string {
	return e.(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
}

func TestInstallMergesWithoutTouchingOthers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(orcaHook), 0o600)
	var mcp []string
	run := func(args ...string) error { mcp = append(mcp, strings.Join(args, " ")); return nil }
	for i := 0; i < 2; i++ {
		if err := Install(p, "/opt/tgbridge", run); err != nil {
			t.Fatal(err)
		}
	}
	m := read(t, p)
	if m["model"] != "opus" {
		t.Fatal("lost other keys")
	}
	pr := entries(m, "PermissionRequest")
	if len(pr) != 2 || cmdOf(pr[0]) != "orca-hook PermissionRequest" || cmdOf(pr[1]) != "/opt/tgbridge hook PermissionRequest" {
		t.Fatalf("PermissionRequest=%v", pr)
	}
	timeout := pr[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)["timeout"].(float64)
	if timeout < 600 {
		t.Fatalf("timeout=%v", timeout)
	}
	if len(entries(m, "Stop")) != 2 || len(entries(m, "Notification")) != 1 {
		t.Fatalf("hooks=%v", m["hooks"])
	}
	if _, err := os.Stat(p + ".tgbridge.bak"); err != nil {
		t.Fatal("no backup")
	}
	if len(mcp) == 0 || !strings.Contains(mcp[len(mcp)-1], "mcp add --scope user tgbridge -- /opt/tgbridge mcp") {
		t.Fatalf("mcp=%v", mcp)
	}
}

func TestInstallCreatesMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := Install(p, "/b/tgbridge", func(...string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(entries(read(t, p), "Stop")) != 1 {
		t.Fatal("no Stop hook")
	}
}

func TestUninstallRemovesOnlyOurs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(orcaHook), 0o600)
	noop := func(...string) error { return nil }
	Install(p, "/opt/tgbridge", noop)
	if err := Uninstall(p, noop); err != nil {
		t.Fatal(err)
	}
	m := read(t, p)
	if len(entries(m, "PermissionRequest")) != 1 || cmdOf(entries(m, "PermissionRequest")[0]) != "orca-hook PermissionRequest" {
		t.Fatalf("%v", m["hooks"])
	}
	if _, ok := m["hooks"].(map[string]any)["Notification"]; ok {
		t.Fatal("empty Notification left behind")
	}
}

func TestUninstallNoSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	var mcp []string
	if err := Uninstall(p, func(a ...string) error { mcp = append(mcp, strings.Join(a, " ")); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("settings.json created: %v", err)
	}
	if len(mcp) != 1 || mcp[0] != "mcp remove --scope user tgbridge" {
		t.Fatalf("mcp=%v", mcp)
	}
}

func TestUninstallLeavesUnrelatedSettingsUntouched(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	orig := "{\n    \"model\": \"opus\",   \"hooks\": {}\n}\n" // odd formatting a rewrite would change
	os.WriteFile(p, []byte(orig), 0o644)
	if err := Uninstall(p, func(...string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != orig {
		t.Fatalf("settings rewritten:\n%s", b)
	}
}
