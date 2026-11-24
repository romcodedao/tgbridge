package setup

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/romcodedao/tgbridge/internal/config"
)

func TestSystemdUnit(t *testing.T) {
	u := SystemdUnit("/b/tgbridge", "/a:/b", nil)
	for _, w := range []string{`ExecStart="/b/tgbridge" serve`, `Environment="PATH=/a:/b"`, "WantedBy=default.target"} {
		if !strings.Contains(u, w) {
			t.Errorf("missing %q in\n%s", w, u)
		}
	}
}

func TestLaunchdPlist(t *testing.T) {
	p := LaunchdPlist("/b/tgbridge", "/a:/b", "/l/tgbridge.log", nil)
	for _, w := range []string{"io.github.romcodedao.tgbridge", "<string>/b/tgbridge</string><string>serve</string>", "/l/tgbridge.log"} {
		if !strings.Contains(p, w) {
			t.Errorf("missing %q in\n%s", w, p)
		}
	}
	if err := xml.Unmarshal([]byte(p), new(struct{})); err != nil {
		t.Fatal(err)
	}
}

func TestSystemdInstallCommands(t *testing.T) {
	home := t.TempDir()
	run, calls := fake(nil, nil)
	s, _ := ForOS("linux", home, run)
	if s.Installed() {
		t.Fatal("installed before install")
	}
	if _, err := s.Install(context.Background(), "/b/tgbridge", "/a", nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable tgbridge", "systemctl --user restart tgbridge"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("%v", *calls)
	}
	if s.Path() != filepath.Join(home, ".config/systemd/user/tgbridge.service") || !s.Installed() {
		t.Fatal(s.Path())
	}
	b, _ := os.ReadFile(s.Path())
	if string(b) != SystemdUnit("/b/tgbridge", "/a", nil) {
		t.Fatal("unit content")
	}
}

func TestSystemdActive(t *testing.T) {
	run, _ := fake(map[string]string{"systemctl --user is-active tgbridge": "active\n"}, nil)
	s, _ := ForOS("linux", t.TempDir(), run)
	if !s.Active(context.Background()) {
		t.Fatal("want active")
	}
	run, _ = fake(map[string]string{"systemctl --user is-active tgbridge": "inactive\n"}, nil)
	s, _ = ForOS("linux", t.TempDir(), run)
	if s.Active(context.Background()) {
		t.Fatal("want inactive")
	}
}

func TestLaunchdInstallCommands(t *testing.T) {
	home := t.TempDir()
	run, calls := fake(nil, map[string]error{})
	s, _ := ForOS("darwin", home, run)
	uid := strconv.Itoa(os.Getuid())
	if _, err := s.Install(context.Background(), "/b/tgbridge", "/a", nil); err != nil {
		t.Fatal(err)
	}
	pl := filepath.Join(home, "Library/LaunchAgents/io.github.romcodedao.tgbridge.plist")
	want := []string{"launchctl bootout gui/" + uid + "/io.github.romcodedao.tgbridge", "launchctl bootstrap gui/" + uid + " " + pl}
	if !reflect.DeepEqual(*calls, want) || s.Path() != pl || !s.Installed() {
		t.Fatalf("%v", *calls)
	}
	b, _ := os.ReadFile(pl)
	if string(b) != LaunchdPlist("/b/tgbridge", "/a", filepath.Join(home, "Library/Logs/tgbridge.log"), nil) {
		t.Fatal("plist content")
	}
	run, _ = fake(map[string]string{"launchctl print gui/" + uid + "/io.github.romcodedao.tgbridge": "state = running"}, nil)
	s, _ = ForOS("darwin", home, run)
	if !s.Active(context.Background()) {
		t.Fatal("want active")
	}
}

func TestUninstallAbsentIsOK(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		run, _ := fake(nil, map[string]error{
			"systemctl --user disable --now tgbridge":                                               errors.New("not loaded"),
			"launchctl bootout gui/" + strconv.Itoa(os.Getuid()) + "/io.github.romcodedao.tgbridge": errors.New("no such"),
		})
		s, _ := ForOS(goos, t.TempDir(), run)
		if err := s.Uninstall(context.Background()); err != nil {
			t.Errorf("%s: %v", goos, err)
		}
	}
}

func TestUninstallRemovesFile(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		run, _ := fake(nil, nil)
		s, _ := ForOS(goos, t.TempDir(), run)
		if _, err := s.Install(context.Background(), "/b", "/a", nil); err != nil {
			t.Fatal(err)
		}
		if err := s.Uninstall(context.Background()); err != nil || s.Installed() {
			t.Errorf("%s: %v", goos, err)
		}
	}
}

func TestForOSWindows(t *testing.T) {
	if _, err := ForOS("windows", "", nil); err == nil || !strings.Contains(err.Error(), "WSL2") {
		t.Fatal(err)
	}
}

func TestServicePATH(t *testing.T) {
	h, _ := os.UserHomeDir()
	rest := ":/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin"
	if got, want := ServicePATH("/opt/c/claude"), "/opt/c:"+h+"/.local/bin"+rest; got != want {
		t.Fatalf("%s != %s", got, want)
	}
	// claude's dir already in the list: no duplicate.
	if got, want := ServicePATH("/usr/bin/claude"), "/usr/bin:"+h+"/.local/bin:/usr/local/bin:/opt/homebrew/bin:/bin"; got != want {
		t.Fatalf("%s != %s", got, want)
	}
}

func TestLaunchdPlistEscapes(t *testing.T) {
	bin := "/a&b/<tg>"
	p := LaunchdPlist(bin, "/p&q:/x", "/l<og", nil)
	var v struct {
		S []string `xml:"dict>array>string"`
	}
	if err := xml.Unmarshal([]byte(p), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.S) != 2 || v.S[0] != bin {
		t.Fatalf("%q", v.S)
	}
}

func TestSystemdUnitQuotes(t *testing.T) {
	u := SystemdUnit("/my dir/100%/tgbridge", "/a b:/c", nil)
	for _, w := range []string{`ExecStart="/my dir/100%%/tgbridge" serve`, `Environment="PATH=/a b:/c"`} {
		if !strings.Contains(u, w) {
			t.Errorf("missing %q in\n%s", w, u)
		}
	}
}

func TestLaunchdBootstrapRetry(t *testing.T) {
	home := t.TempDir()
	n, slept, always := 0, 0, false
	run := func(_ context.Context, name string, args ...string) (string, error) {
		if args[0] == "bootstrap" {
			if n++; n == 1 || always {
				return "", errors.New("Input/output error")
			}
		}
		return "", nil
	}
	l := launchd{home, 501, run, func(time.Duration) { slept++ }}
	if _, err := l.Install(context.Background(), "/b", "/a", nil); err != nil || n != 2 || slept != 1 {
		t.Fatalf("%v %d %d", err, n, slept)
	}
	n, slept, always = 0, 0, true
	if _, err := l.Install(context.Background(), "/b", "/a", nil); err == nil || slept != 4 {
		t.Fatalf("%v %d", err, slept)
	}
}

// A unit can be running even when its file is gone, so Stop always asks systemd and ignores errors.
func TestSystemdStopNotInstalled(t *testing.T) {
	run, calls := fake(nil, map[string]error{"systemctl --user stop tgbridge": errors.New("not loaded")})
	s, _ := ForOS("linux", t.TempDir(), run)
	if err := s.Stop(context.Background()); err != nil || !reflect.DeepEqual(*calls, []string{"systemctl --user stop tgbridge"}) {
		t.Fatalf("%v %v", err, *calls)
	}
}

func TestServicePATHSkipsEmpty(t *testing.T) {
	t.Setenv("HOME", "")
	for _, c := range []string{"", "claude"} {
		got := ServicePATH(c)
		if got != "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin" {
			t.Errorf("%q: %s", c, got)
		}
	}
}

// Tests must not depend on the machine running systemd; TestForOSNoSystemd overrides this.
func init() { systemdDir = os.TempDir() }

func TestForOSNoSystemd(t *testing.T) {
	old := systemdDir
	defer func() { systemdDir = old }()
	systemdDir = filepath.Join(t.TempDir(), "none")
	if s, err := ForOS("linux", t.TempDir(), nil); s != nil || err == nil || !strings.Contains(err.Error(), "/etc/wsl.conf") || !strings.Contains(err.Error(), "systemd=true") {
		t.Fatalf("%v %v", s, err)
	}
}

func TestServiceEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if env := ServiceEnv(config.DefaultPath()); env != nil {
		t.Fatalf("default config needs no env: %v", env)
	}
	if env := ServiceEnv("/h/x.yaml"); env["TGBRIDGE_CONFIG"] != "/h/x.yaml" || len(env) != 1 {
		t.Fatalf("%v", env)
	}
}

func TestSystemdUnitCustomConfig(t *testing.T) {
	u := SystemdUnit("/b/tgbridge", "/a", map[string]string{"TGBRIDGE_CONFIG": "/h/100%/x y.yaml"})
	if !strings.Contains(u, "Environment=\"TGBRIDGE_CONFIG=/h/100%%/x y.yaml\"\n") {
		t.Fatalf("missing TGBRIDGE_CONFIG in\n%s", u)
	}
}

func TestLaunchdPlistCustomConfig(t *testing.T) {
	p := LaunchdPlist("/b/tgbridge", "/a", "/l", map[string]string{"TGBRIDGE_CONFIG": "/h/a&b.yaml"})
	if !strings.Contains(p, "<key>TGBRIDGE_CONFIG</key><string>/h/a&amp;b.yaml</string>") {
		t.Fatalf("missing TGBRIDGE_CONFIG in\n%s", p)
	}
	if err := xml.Unmarshal([]byte(p), new(struct{})); err != nil {
		t.Fatal(err)
	}
}

func TestInstallBacksUpEditedUnit(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		run, _ := fake(nil, nil)
		s, _ := ForOS(goos, t.TempDir(), run)
		if bak, err := s.Install(context.Background(), "/b", "/a", nil); err != nil || bak != "" {
			t.Fatalf("%s: first install: %q %v", goos, bak, err)
		}
		if bak, err := s.Install(context.Background(), "/b", "/a", nil); err != nil || bak != "" {
			t.Fatalf("%s: unchanged unit needs no backup: %q %v", goos, bak, err)
		}
		must(t, os.WriteFile(s.Path(), []byte("hand edited"), 0o644))
		bak, err := s.Install(context.Background(), "/b", "/a", nil)
		if err != nil || bak != s.Path()+".bak" || read(t, bak) != "hand edited" {
			t.Fatalf("%s: %q %v", goos, bak, err)
		}
	}
}

// Re-running init must not restart a running service whose unit is unchanged: that would end a Claude turn.
func TestInstallUnchangedRunningSkipsRestart(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	for _, c := range []struct {
		goos, activeCmd, activeOut string
		restart                    func(calls []string) bool
	}{
		{"linux", "systemctl --user is-active tgbridge", "active\n", func(calls []string) bool { return slices.Contains(calls, "systemctl --user restart tgbridge") }},
		{"darwin", "launchctl print gui/" + uid + "/io.github.romcodedao.tgbridge", "state = running", func(calls []string) bool {
			return slices.ContainsFunc(calls, func(s string) bool { return strings.Contains(s, "bootstrap") || strings.Contains(s, "bootout") })
		}},
	} {
		t.Run(c.goos, func(t *testing.T) {
			home := t.TempDir()
			install := func(active bool, bin string) []string {
				out := map[string]string{}
				if active {
					out[c.activeCmd] = c.activeOut
				}
				run, calls := fake(out, nil)
				s, _ := ForOS(c.goos, home, run)
				if _, err := s.Install(context.Background(), bin, "/a", nil); err != nil {
					t.Fatal(err)
				}
				return *calls
			}
			if calls := install(true, "/b"); !c.restart(calls) {
				t.Fatalf("a new unit must start: %v", calls)
			}
			calls := install(true, "/b")
			if c.restart(calls) {
				t.Fatalf("unchanged and running: no restart, got %v", calls)
			}
			if c.goos == "linux" && !slices.Contains(calls, "systemctl --user enable tgbridge") {
				t.Fatalf("enable must still run: %v", calls)
			}
			if calls := install(false, "/b"); !c.restart(calls) {
				t.Fatalf("unchanged but stopped: must start, got %v", calls)
			}
			if calls := install(true, "/moved/b"); !c.restart(calls) {
				t.Fatalf("changed unit: must restart, got %v", calls)
			}
		})
	}
}
