package setup

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSystemdUnit(t *testing.T) {
	u := SystemdUnit("/b/tgbridge", "/a:/b")
	for _, w := range []string{`ExecStart="/b/tgbridge" serve`, `Environment="PATH=/a:/b"`, "WantedBy=default.target"} {
		if !strings.Contains(u, w) {
			t.Errorf("missing %q in\n%s", w, u)
		}
	}
}

func TestLaunchdPlist(t *testing.T) {
	p := LaunchdPlist("/b/tgbridge", "/a:/b", "/l/tgbridge.log")
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
	if err := s.Install(context.Background(), "/b/tgbridge", "/a"); err != nil {
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
	if string(b) != SystemdUnit("/b/tgbridge", "/a") {
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
	if err := s.Install(context.Background(), "/b/tgbridge", "/a"); err != nil {
		t.Fatal(err)
	}
	pl := filepath.Join(home, "Library/LaunchAgents/io.github.romcodedao.tgbridge.plist")
	want := []string{"launchctl bootout gui/" + uid + "/io.github.romcodedao.tgbridge", "launchctl bootstrap gui/" + uid + " " + pl}
	if !reflect.DeepEqual(*calls, want) || s.Path() != pl || !s.Installed() {
		t.Fatalf("%v", *calls)
	}
	b, _ := os.ReadFile(pl)
	if string(b) != LaunchdPlist("/b/tgbridge", "/a", filepath.Join(home, "Library/Logs/tgbridge.log")) {
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
		if err := s.Install(context.Background(), "/b", "/a"); err != nil {
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
	p := LaunchdPlist(bin, "/p&q:/x", "/l<og")
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
	u := SystemdUnit("/my dir/100%/tgbridge", "/a b:/c")
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
	if err := l.Install(context.Background(), "/b", "/a"); err != nil || n != 2 || slept != 1 {
		t.Fatalf("%v %d %d", err, n, slept)
	}
	n, slept, always = 0, 0, true
	if err := l.Install(context.Background(), "/b", "/a"); err == nil || slept != 4 {
		t.Fatalf("%v %d", err, slept)
	}
}

func TestSystemdStopNotInstalled(t *testing.T) {
	run, calls := fake(nil, nil)
	s, _ := ForOS("linux", t.TempDir(), run)
	if err := s.Stop(context.Background()); err != nil || len(*calls) != 0 {
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
