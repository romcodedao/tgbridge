package setup

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/romcodedao/tgbridge/internal/config"
)

const label = "io.github.romcodedao.tgbridge"

// Service is tgbridge's background service on this OS.
type Service interface {
	Name() string
	Path() string
	Installed() bool
	Active(ctx context.Context) bool
	// Install writes the unit with PATH and env, then (re)starts it. A hand-edited unit is saved first: bak is its path, or "".
	Install(ctx context.Context, bin, pathEnv string, env map[string]string) (bak string, err error)
	Stop(ctx context.Context) error
	Uninstall(ctx context.Context) error
}

// NoServiceError says why this machine has no service manager and how to get one.
type NoServiceError struct{ Why, Fix string }

func (e *NoServiceError) Error() string { return e.Why + ": " + e.Fix }

// systemdDir exists only while systemd runs (sd_booted); a var for tests.
var systemdDir = "/run/systemd/system"

// ForOS returns the service manager for goos; Windows is WSL2 only.
func ForOS(goos, home string, run Runner) (Service, error) {
	switch goos {
	case "linux":
		if !exists(systemdDir) {
			return nil, &NoServiceError{"systemd isn't running (no " + systemdDir + ")",
				"on WSL2, add [boot] systemd=true to /etc/wsl.conf, run wsl --shutdown in Windows and reopen WSL, then tgbridge service install; otherwise run tgbridge serve yourself"}
		}
		return systemd{home, run}, nil
	case "darwin":
		return launchd{home, os.Getuid(), run, time.Sleep}, nil
	}
	return nil, fmt.Errorf("no service support on %s: run tgbridge inside WSL2", goos)
}

// pct escapes systemd specifiers.
func pct(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// esc XML-escapes s.
func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// ServiceEnv is the extra environment the service needs: TGBRIDGE_CONFIG when the config isn't at the default path.
func ServiceEnv(configPath string) map[string]string {
	if configPath == "" || configPath == config.DefaultPath() {
		return nil
	}
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}
	return map[string]string{"TGBRIDGE_CONFIG": configPath}
}

// SystemdUnit renders the user unit.
func SystemdUnit(bin, pathEnv string, env map[string]string) string {
	extra := ""
	for _, k := range slices.Sorted(maps.Keys(env)) {
		extra += `Environment="` + pct(k+"="+env[k]) + "\"\n"
	}
	return `[Unit]
Description=tgbridge - Claude Code over Telegram
After=network-online.target

[Service]
ExecStart="` + pct(bin) + `" serve
Restart=on-failure
RestartSec=5
Environment="PATH=` + pct(pathEnv) + `"
` + extra + `
[Install]
WantedBy=default.target
`
}

// LaunchdPlist renders the launch agent.
func LaunchdPlist(bin, pathEnv, logPath string, env map[string]string) string {
	extra := ""
	for _, k := range slices.Sorted(maps.Keys(env)) {
		extra += "<key>" + esc(k) + "</key><string>" + esc(env[k]) + "</string>"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>` + label + `</string>
<key>ProgramArguments</key><array><string>` + esc(bin) + `</string><string>serve</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>` + esc(pathEnv) + `</string>` + extra + `</dict>
<key>StandardOutPath</key><string>` + esc(logPath) + `</string>
<key>StandardErrorPath</key><string>` + esc(logPath) + `</string>
</dict></plist>
`
}

// ServicePATH is claude's dir first, then the usual bin dirs, without duplicates.
func ServicePATH(claudePath string) string {
	h, _ := os.UserHomeDir()
	dirs := []string{"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin"}
	if h != "" {
		dirs = append([]string{filepath.Join(h, ".local/bin")}, dirs...)
	}
	if claudePath != "" {
		dirs = append([]string{filepath.Dir(claudePath)}, dirs...)
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range dirs {
		if d != "" && d != "." && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return strings.Join(out, ":")
}

// writeUnit writes body to path, first copying a different existing file to path.bak (returned).
func writeUnit(path, body string) (bak string, err error) {
	if old, err := os.ReadFile(path); err == nil && string(old) != body {
		bak = path + ".bak"
		if err := os.WriteFile(bak, old, 0o644); err != nil {
			return "", err
		}
	}
	return bak, write(path, body)
}

func write(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

type systemd struct {
	home string
	run  Runner
}

func (s systemd) Name() string    { return "systemd" }
func (s systemd) Path() string    { return filepath.Join(s.home, ".config/systemd/user/tgbridge.service") }
func (s systemd) Installed() bool { return exists(s.Path()) }

func (s systemd) ctl(ctx context.Context, args ...string) error {
	_, err := s.run(ctx, "systemctl", append([]string{"--user"}, args...)...)
	return err
}

func (s systemd) Active(ctx context.Context) bool {
	out, _ := s.run(ctx, "systemctl", "--user", "is-active", "tgbridge")
	return strings.TrimSpace(out) == "active"
}

func (s systemd) Install(ctx context.Context, bin, pathEnv string, env map[string]string) (string, error) {
	bak, err := writeUnit(s.Path(), SystemdUnit(bin, pathEnv, env))
	if err != nil {
		return "", err
	}
	for _, a := range [][]string{{"daemon-reload"}, {"enable", "tgbridge"}, {"restart", "tgbridge"}} {
		if err := s.ctl(ctx, a...); err != nil {
			return bak, err
		}
	}
	return bak, nil
}

// Stop runs even without a unit file (one may still be running) and, like launchd, ignores "not loaded".
func (s systemd) Stop(ctx context.Context) error {
	_ = s.ctl(ctx, "stop", "tgbridge")
	return nil
}

func (s systemd) Uninstall(ctx context.Context) error {
	_ = s.ctl(ctx, "disable", "--now", "tgbridge")
	if err := removeFile(s.Path()); err != nil {
		return err
	}
	return s.ctl(ctx, "daemon-reload")
}

type launchd struct {
	home  string
	uid   int
	run   Runner
	sleep func(time.Duration)
}

func (l launchd) Name() string { return "launchd" }
func (l launchd) Path() string {
	return filepath.Join(l.home, "Library/LaunchAgents", label+".plist")
}
func (l launchd) Installed() bool { return exists(l.Path()) }
func (l launchd) domain() string  { return fmt.Sprintf("gui/%d", l.uid) }

func (l launchd) Active(ctx context.Context) bool {
	out, _ := l.run(ctx, "launchctl", "print", l.domain()+"/"+label)
	return strings.Contains(out, "state = running")
}

func (l launchd) Install(ctx context.Context, bin, pathEnv string, env map[string]string) (string, error) {
	log := filepath.Join(l.home, "Library/Logs/tgbridge.log")
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		return "", err
	}
	bak, err := writeUnit(l.Path(), LaunchdPlist(bin, pathEnv, log, env))
	if err != nil {
		return "", err
	}
	_ = l.Stop(ctx) // not loaded yet is fine
	// bootout is asynchronous, so an early bootstrap can fail with an I/O error.
	for i := 0; i < 5; i++ {
		if i > 0 {
			l.sleep(500 * time.Millisecond)
		}
		if _, err = l.run(ctx, "launchctl", "bootstrap", l.domain(), l.Path()); err == nil {
			break
		}
	}
	return bak, err
}

func (l launchd) Stop(ctx context.Context) error {
	_, _ = l.run(ctx, "launchctl", "bootout", l.domain()+"/"+label)
	return nil
}

func (l launchd) Uninstall(ctx context.Context) error {
	_ = l.Stop(ctx)
	return removeFile(l.Path())
}
