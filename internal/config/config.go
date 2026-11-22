// Package config loads tgbridge's YAML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Engine struct {
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	MaxAge            time.Duration `yaml:"max_age"`
	MaxLive           int           `yaml:"max_live"`
	PermissionTimeout time.Duration `yaml:"permission_timeout"`
	PermissionMode    string        `yaml:"permission_mode"` // ask | auto | full; a topic can override it with /mode
}

type Context struct {
	SoftLimit int `yaml:"soft_limit"`
	HardLimit int `yaml:"hard_limit"`
}

type Config struct {
	TokenFile                string        `yaml:"token_file"`
	OwnerID                  int64         `yaml:"owner_id"`
	DefaultCwd               string        `yaml:"default_cwd"`
	OrcaCLI                  string        `yaml:"orca_cli"`
	ClaudeBin                string        `yaml:"claude_bin"`
	Model                    string        `yaml:"model"`
	Engine                   Engine        `yaml:"engine"`
	Context                  Context       `yaml:"context"`
	DraftInterval            time.Duration `yaml:"draft_interval"`
	HandoffPermissionTimeout time.Duration `yaml:"handoff_permission_timeout"`
	RateLimitPerMin          int           `yaml:"rate_limit_per_min"`
	StateDir                 string        `yaml:"state_dir"`
	SocketPath               string        `yaml:"socket_path"`
}

// PermissionModes maps tgbridge's mode names to claude --permission-mode values ("" = Claude's default:
// anything not auto-allowed asks the owner on Telegram).
var PermissionModes = map[string]string{"ask": "", "auto": "auto", "full": "bypassPermissions"}

func home() string { h, _ := os.UserHomeDir(); return h }

// DefaultPath is ~/.config/tgbridge/config.yaml.
func DefaultPath() string { return filepath.Join(home(), ".config", "tgbridge", "config.yaml") }

// DefaultSocket is $XDG_RUNTIME_DIR/tgbridge.sock (falls back to the state dir).
func DefaultSocket() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "tgbridge.sock")
	}
	return filepath.Join(home(), ".local", "state", "tgbridge", "tgbridge.sock")
}

// Defaults returns a Config with every default applied and no owner.
func Defaults() *Config {
	return &Config{
		TokenFile:                filepath.Join(home(), ".config", "tgbridge", "token"),
		DefaultCwd:               home(),
		ClaudeBin:                "claude",
		Engine:                   Engine{IdleTimeout: 15 * time.Minute, MaxAge: 24 * time.Hour, MaxLive: 3, PermissionTimeout: 10 * time.Minute, PermissionMode: "ask"},
		Context:                  Context{SoftLimit: 80000, HardLimit: 150000},
		DraftInterval:            700 * time.Millisecond,
		HandoffPermissionTimeout: 10 * time.Minute,
		RateLimitPerMin:          30,
		StateDir:                 filepath.Join(home(), ".local", "state", "tgbridge"),
		SocketPath:               DefaultSocket(),
	}
}

// Load reads the YAML file at path over the defaults.
func Load(path string) (*Config, error) {
	c := Defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, p := range []*string{&c.TokenFile, &c.DefaultCwd, &c.StateDir, &c.SocketPath, &c.OrcaCLI, &c.ClaudeBin} {
		*p = expand(*p)
	}
	if _, ok := PermissionModes[c.Engine.PermissionMode]; !ok {
		return nil, fmt.Errorf("config: engine.permission_mode must be ask, auto or full (got %q)", c.Engine.PermissionMode)
	}
	if c.OwnerID == 0 {
		return nil, errors.New("config: owner_id is required (send /whoami to the bot to find it)")
	}
	if c.OrcaCLI == "" {
		c.OrcaCLI = os.Getenv("ORCA_CLI_COMMAND")
	}
	if c.OrcaCLI == "" {
		c.OrcaCLI = filepath.Join(home(), ".config", "orca", "cli", "bin", "orca")
	}
	return c, nil
}

// Token returns the bot token from TGBRIDGE_TOKEN or the token file (mode must be 0600 or stricter).
func (c *Config) Token() (string, error) {
	if t := strings.TrimSpace(os.Getenv("TGBRIDGE_TOKEN")); t != "" {
		return t, nil
	}
	st, err := os.Stat(c.TokenFile)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s must not be readable by group/others (chmod 600)", c.TokenFile)
	}
	b, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Clean(filepath.Join(home(), p[1:]))
	}
	return p
}
