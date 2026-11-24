package setup

import (
	"errors"
	"io/fs"

	"github.com/romcodedao/tgbridge/internal/config"
)

// Env is everything init and doctor touch on this machine, injectable for tests.
type Env struct {
	ConfigPath, Home, GOOS, Bin, Version, TelegramBase string // TelegramBase "" is the real API
	Example                                            []byte // config.example.yaml, written on first run
	Run                                                Runner
	LookPath                                           func(string) (string, error)
	Svc                                                Service // nil: no service manager on this OS
	SvcErr                                             error   // why Svc is nil, from ForOS
	ClaudeSettings                                     string  // ~/.claude/settings.json
}

// readConfig returns the config, or nil and the error; a missing file is fs.ErrNotExist.
func (e Env) readConfig() (*config.Config, error) { return config.Read(e.ConfigPath) }

// ClaudeBin is claude_bin from the config, or "claude" before there is one.
func (e Env) ClaudeBin() string {
	if c, err := e.readConfig(); err == nil {
		return c.ClaudeBin
	}
	return "claude"
}

func missing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// noService says why there is no service and what to do instead.
func (e Env) noService() string {
	if e.SvcErr != nil {
		return e.SvcErr.Error()
	}
	return "no service manager on " + e.GOOS + ": on Windows, run tgbridge inside WSL2; otherwise run tgbridge serve yourself"
}
