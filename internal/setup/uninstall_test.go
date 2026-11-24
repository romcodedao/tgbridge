package setup

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/romcodedao/tgbridge/internal/config"
)

const ourHooks = `{"model":"opus","hooks":{"Stop":[{"matcher":"*","hooks":[{"type":"command","command":"/b/tgbridge hook Stop"}]}]}}`

// uninstallRig is a full install on a temp home: service file, settings + backup, config, token, state, uploads, binary, log.
func uninstallRig(t *testing.T, goos string) (Env, *config.Config, *[]string) {
	home := t.TempDir()
	run, calls := fake(nil, nil)
	svc, err := ForOS(goos, home, run)
	if err != nil {
		t.Fatal(err)
	}
	j := func(p string) string { return filepath.Join(home, p) }
	cfg := &config.Config{TokenFile: j(".config/tgbridge/token"), StateDir: j(".local/state/tgbridge"), ClaudeBin: "claude",
		Uploads: config.Uploads{Dir: j(".cache/tgbridge/uploads")}}
	e := Env{ConfigPath: j(".config/tgbridge/config.yaml"), Home: home, GOOS: goos, Bin: j(".local/bin/tgbridge"),
		Run: run, Svc: svc, ClaudeSettings: j(".claude/settings.json")}
	for p, body := range map[string]string{
		svc.Path(): "unit", e.ClaudeSettings: ourHooks, e.ClaudeSettings + ".tgbridge.bak": "{}", e.ConfigPath: "owner_id: 1",
		cfg.TokenFile: "tok", cfg.StateDir + "/state.json": "{}", cfg.Uploads.Dir + "/a.png": "x", e.Bin: "bin",
		j("Library/Logs/tgbridge.log"): "log",
	} {
		must(t, write(p, body))
	}
	return e, cfg, calls
}

func paths(do []Action) []string {
	var out []string
	for _, a := range do {
		out = append(out, a.Path)
	}
	return out
}

func apply(t *testing.T, do []Action) {
	for _, a := range do {
		if err := a.Do(context.Background()); err != nil {
			t.Fatalf("%s: %v", a.Desc, err)
		}
	}
}

func hasKept(kept []string, sub string) bool {
	for _, k := range kept {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}

func testDefaultPlan(t *testing.T, goos string, wantCalls []string) {
	e, cfg, calls := uninstallRig(t, goos)
	do, kept := UninstallPlan(e, cfg, false)
	if want := []string{e.Svc.Path(), e.ClaudeSettings}; !reflect.DeepEqual(paths(do), want) {
		t.Fatalf("plan %v, want %v", paths(do), want)
	}
	if !strings.HasPrefix(do[0].Desc, "stop and remove ") {
		t.Fatal(do[0].Desc)
	}
	for _, k := range []string{filepath.Dir(e.ConfigPath), cfg.TokenFile, cfg.StateDir, cfg.Uploads.Dir, e.ClaudeSettings + ".tgbridge.bak"} {
		if !hasKept(kept, k) {
			t.Errorf("kept %v lacks %s", kept, k)
		}
	}
	apply(t, do)
	if e.Svc.Installed() {
		t.Fatal("service file left")
	}
	for _, c := range wantCalls {
		if !strings.Contains(strings.Join(*calls, "\n"), c) {
			t.Errorf("calls %v lack %q", *calls, c)
		}
	}
	if s := read(t, e.ClaudeSettings); strings.Contains(s, "tgbridge") || !strings.Contains(s, "opus") {
		t.Fatalf("settings: %s", s)
	}
	for _, p := range []string{e.ConfigPath, cfg.TokenFile, cfg.StateDir, cfg.Uploads.Dir, e.Bin} {
		if !exists(p) {
			t.Errorf("%s removed without --purge", p)
		}
	}
}

func TestUninstallPlanSystemd(t *testing.T) {
	testDefaultPlan(t, "linux", []string{"systemctl --user disable --now tgbridge", "systemctl --user daemon-reload", "claude mcp remove --scope user tgbridge"})
}

func TestUninstallPlanLaunchd(t *testing.T) {
	testDefaultPlan(t, "darwin", []string{"launchctl bootout gui/", "claude mcp remove --scope user tgbridge"})
}

func TestUninstallPlanPurge(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			e, cfg, _ := uninstallRig(t, goos)
			do, kept := UninstallPlan(e, cfg, true)
			// the token lives in the config dir, so deleting the dir covers it
			want := []string{e.Svc.Path(), e.ClaudeSettings, filepath.Dir(e.ConfigPath), cfg.StateDir, cfg.Uploads.Dir}
			if goos == "darwin" {
				want = append(want, filepath.Join(e.Home, "Library/Logs/tgbridge.log"))
			}
			want = append(want, filepath.Join(e.Home, ".local/bin/tgbridge"))
			if !reflect.DeepEqual(paths(do), want) {
				t.Fatalf("plan\n%v\nwant\n%v", paths(do), want)
			}
			if !hasKept(kept, e.ClaudeSettings+".tgbridge.bak") {
				t.Fatalf("kept %v", kept)
			}
			apply(t, do)
			for _, p := range append(want[2:], e.Svc.Path(), cfg.TokenFile) {
				if exists(p) {
					t.Errorf("%s still there", p)
				}
			}
			if !exists(e.ClaudeSettings) || !exists(e.ClaudeSettings+".tgbridge.bak") {
				t.Fatal("Claude settings or backup removed")
			}
		})
	}
}

func TestPurgeBinaryElsewhereKept(t *testing.T) {
	e, cfg, _ := uninstallRig(t, "linux")
	e.Bin = filepath.Join(e.Home, "go/bin/tgbridge")
	must(t, write(e.Bin, "bin"))
	do, kept := UninstallPlan(e, cfg, true)
	for _, p := range paths(do) {
		if strings.Contains(p, "bin/tgbridge") {
			t.Fatalf("plan removes binary %s", p)
		}
	}
	if !hasKept(kept, "remove it yourself: rm "+e.Bin) {
		t.Fatalf("kept %v", kept)
	}
}

func TestPurgeUnsafePaths(t *testing.T) {
	outside := t.TempDir()
	must(t, write(filepath.Join(outside, "precious"), "x"))
	cases := map[string]func(e *Env, c *config.Config){
		"state=home":   func(e *Env, c *config.Config) { c.StateDir = e.Home },
		"state=/":      func(e *Env, c *config.Config) { c.StateDir = "/" },
		"state=/var/x": func(e *Env, c *config.Config) { c.StateDir = "/var/x" },
		"uploads=/":    func(e *Env, c *config.Config) { c.Uploads.Dir = "/" },
		"uploads=../":  func(e *Env, c *config.Config) { c.Uploads.Dir = e.Home + "/../tgbridge" },
		"config in home": func(e *Env, c *config.Config) {
			e.ConfigPath = filepath.Join(e.Home, "x.yaml")
			must(t, write(e.ConfigPath, "owner_id: 1"))
		},
		"symlink out of home": func(e *Env, c *config.Config) {
			must(t, os.Symlink(outside, filepath.Join(e.Home, "tgbridge-link")))
			c.StateDir = filepath.Join(e.Home, "tgbridge-link/precious")
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			e, cfg, _ := uninstallRig(t, "linux")
			mod(&e, cfg)
			do, kept := UninstallPlan(e, cfg, true)
			for _, a := range do {
				if strings.HasPrefix(a.Desc, "delete ") && !safeToRemove(e.Home, a.Path) {
					t.Fatalf("unsafe path in plan: %s", a.Path)
				}
			}
			if !hasKept(kept, "skipped (not a tgbridge folder)") {
				t.Fatalf("kept %v", kept)
			}
			apply(t, do)
			if !exists(filepath.Join(outside, "precious")) || !exists(e.Home) {
				t.Fatal("deleted outside tgbridge's folders")
			}
		})
	}
}

func TestSafeToRemove(t *testing.T) {
	for p, want := range map[string]bool{
		"/h/.config/tgbridge": true, "/h/tgbridge.yaml": true, "/h": false, "/h/": false, "/": false, "/var/tgbridge": false,
		"/h/../tgbridge": false, "/h/x.yaml": false, "rel/tgbridge": false, "/h/.config": false,
	} {
		if got := safeToRemove("/h", p); got != want {
			t.Errorf("safeToRemove(/h, %q) = %v", p, got)
		}
	}
}

func TestUninstallTwice(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			e, cfg, _ := uninstallRig(t, goos)
			must(t, os.Remove(e.ClaudeSettings)) // partial install: no Claude settings
			for i := 0; i < 2; i++ {
				do, _ := UninstallPlan(e, cfg, true)
				apply(t, do)
			}
			if exists(e.ClaudeSettings) {
				t.Fatal("settings.json created")
			}
			if do, _ := UninstallPlan(e, cfg, true); len(do) != 1 || do[0].Path != e.ClaudeSettings {
				t.Fatalf("second plan %v", paths(do))
			}
		})
	}
}
