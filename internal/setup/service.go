package setup

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const label = "io.github.romcodedao.tgbridge"

// Service is tgbridge's background service on this OS.
type Service interface {
	Name() string
	Path() string
	Installed() bool
	Active(ctx context.Context) bool
	Install(ctx context.Context, bin, pathEnv string) error
	Stop(ctx context.Context) error
	Uninstall(ctx context.Context) error
}

// ForOS returns the service manager for goos; Windows is WSL2 only.
func ForOS(goos, home string, run Runner) (Service, error) {
	switch goos {
	case "linux":
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

// SystemdUnit renders the user unit.
func SystemdUnit(bin, pathEnv string) string {
	return `[Unit]
Description=tgbridge - Claude Code over Telegram
After=network-online.target

[Service]
ExecStart="` + pct(bin) + `" serve
Restart=on-failure
RestartSec=5
Environment="PATH=` + pct(pathEnv) + `"

[Install]
WantedBy=default.target
`
}

// LaunchdPlist renders the launch agent.
func LaunchdPlist(bin, pathEnv, logPath string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>` + label + `</string>
<key>ProgramArguments</key><array><string>` + esc(bin) + `</string><string>serve</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>` + esc(pathEnv) + `</string></dict>
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

func (s systemd) Install(ctx context.Context, bin, pathEnv string) error {
	if err := write(s.Path(), SystemdUnit(bin, pathEnv)); err != nil {
		return err
	}
	for _, a := range [][]string{{"daemon-reload"}, {"enable", "tgbridge"}, {"restart", "tgbridge"}} {
		if err := s.ctl(ctx, a...); err != nil {
			return err
		}
	}
	return nil
}

func (s systemd) Stop(ctx context.Context) error {
	if !s.Installed() {
		return nil
	}
	return s.ctl(ctx, "stop", "tgbridge")
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

func (l launchd) Install(ctx context.Context, bin, pathEnv string) error {
	log := filepath.Join(l.home, "Library/Logs/tgbridge.log")
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		return err
	}
	if err := write(l.Path(), LaunchdPlist(bin, pathEnv, log)); err != nil {
		return err
	}
	_ = l.Stop(ctx) // not loaded yet is fine
	// bootout is asynchronous, so an early bootstrap can fail with an I/O error.
	var err error
	for i := 0; i < 5; i++ {
		if i > 0 {
			l.sleep(500 * time.Millisecond)
		}
		if _, err = l.run(ctx, "launchctl", "bootstrap", l.domain(), l.Path()); err == nil {
			break
		}
	}
	return err
}

func (l launchd) Stop(ctx context.Context) error {
	_, _ = l.run(ctx, "launchctl", "bootout", l.domain()+"/"+label)
	return nil
}

func (l launchd) Uninstall(ctx context.Context) error {
	_ = l.Stop(ctx)
	return removeFile(l.Path())
}
