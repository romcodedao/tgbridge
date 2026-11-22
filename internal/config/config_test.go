package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(write(t, "owner_id: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Engine.MaxLive != 3 || c.Engine.IdleTimeout != 15*time.Minute || c.Engine.MaxAge != 24*time.Hour {
		t.Fatalf("engine defaults: %+v", c.Engine)
	}
	if c.Context.SoftLimit != 80000 || c.Context.HardLimit != 150000 {
		t.Fatalf("context defaults: %+v", c.Context)
	}
	if c.DraftInterval != 700*time.Millisecond || c.Engine.PermissionTimeout != 10*time.Minute || c.HandoffPermissionTimeout != 10*time.Minute {
		t.Fatalf("timing defaults: %+v", c)
	}
}

func TestLoadParsesDurations(t *testing.T) {
	c, err := Load(write(t, "owner_id: 1\nengine:\n  idle_timeout: 5m\n  max_live: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Engine.IdleTimeout != 5*time.Minute || c.Engine.MaxLive != 2 {
		t.Fatalf("got %+v", c.Engine)
	}
}

func TestLoadRequiresOwner(t *testing.T) {
	_, err := Load(write(t, "default_cwd: /tmp\n"))
	if err == nil || !strings.Contains(err.Error(), "owner_id") {
		t.Fatalf("want owner_id error, got %v", err)
	}
}

func TestTokenRejectsWideMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "token")
	os.WriteFile(p, []byte("abc:def\n"), 0o644)
	c := &Config{TokenFile: p}
	t.Setenv("TGBRIDGE_TOKEN", "")
	if _, err := c.Token(); err == nil {
		t.Fatal("want error for 0644 token file")
	}
	os.Chmod(p, 0o600)
	tok, err := c.Token()
	if err != nil || tok != "abc:def" {
		t.Fatalf("got %q %v", tok, err)
	}
}

func TestTokenFromEnv(t *testing.T) {
	t.Setenv("TGBRIDGE_TOKEN", "x:y")
	c := &Config{TokenFile: "/nonexistent"}
	if tok, err := c.Token(); err != nil || tok != "x:y" {
		t.Fatalf("got %q %v", tok, err)
	}
}

func TestLoadExpandsTilde(t *testing.T) {
	c, err := Load(write(t, "owner_id: 1\ntoken_file: ~/x/token\ndefault_cwd: ~/\nstate_dir: ~/s\n"))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := os.UserHomeDir()
	if c.TokenFile != filepath.Join(h, "x/token") || c.DefaultCwd != h || c.StateDir != filepath.Join(h, "s") {
		t.Fatalf("%q %q %q", c.TokenFile, c.DefaultCwd, c.StateDir)
	}
}

func TestPermissionModeDefaultAndValidation(t *testing.T) {
	c, err := Load(write(t, "owner_id: 1\n"))
	if err != nil || c.Engine.PermissionMode != "ask" {
		t.Fatalf("mode=%q err=%v", c.Engine.PermissionMode, err)
	}
	if _, err := Load(write(t, "owner_id: 1\nengine:\n  permission_mode: yolo\n")); err == nil || !strings.Contains(err.Error(), "permission_mode") {
		t.Fatalf("want permission_mode error, got %v", err)
	}
}

func TestUploadsDefaultsAndOverride(t *testing.T) {
	c, err := Load(write(t, "owner_id: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Uploads.Dir != filepath.Join(home(), ".cache", "tgbridge", "uploads") || c.Uploads.MaxAge != 168*time.Hour || c.Uploads.MaxTotalMB != 500 {
		t.Fatalf("defaults: %+v", c.Uploads)
	}
	c, err = Load(write(t, "owner_id: 1\nuploads:\n  dir: ~/up\n  max_age: 1h\n  max_total_mb: 10\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Uploads.Dir != filepath.Join(home(), "up") || c.Uploads.MaxAge != time.Hour || c.Uploads.MaxTotalMB != 10 {
		t.Fatalf("override: %+v", c.Uploads)
	}
}

func TestUploadsLimitsMustBePositive(t *testing.T) {
	for _, y := range []string{"uploads:\n  max_age: 0s\n", "uploads:\n  max_total_mb: -1\n"} {
		if _, err := Load(write(t, "owner_id: 1\n"+y)); err == nil || !strings.Contains(err.Error(), "uploads") {
			t.Fatalf("%q: want uploads error, got %v", y, err)
		}
	}
}
