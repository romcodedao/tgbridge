package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/romcodedao/tgbridge/internal/telegram"
)

const (
	initFix     = "run tgbridge init"
	threadedFix = "in @BotFather open the Mini App → Bot Settings → Threads Settings and turn on Threaded Mode"
)

// logHint is where an inactive service's logs are.
func logHint(goos string) string {
	if goos == "darwin" {
		return "see the log: tail -n 50 ~/Library/Logs/tgbridge.log"
	}
	return "see the log: journalctl --user -u tgbridge -n 50"
}

// getMe checks a token with a 10s timeout, so a dead network can't hang init or doctor.
func getMe(ctx context.Context, e Env, token string) (*telegram.User, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return telegram.New(token, e.TelegramBase).GetMe(ctx)
}

// Doctor checks the whole install and says how to fix each problem.
func Doctor(ctx context.Context, e Env) []Check {
	cs := []Check{{Info, "version", "tgbridge " + e.Version, ""}}
	cs = append(cs, ClaudeChecks(DetectClaude(ctx, e.ClaudeBin(), e.LookPath, e.Run))...)

	cfg, err := e.readConfig()
	switch {
	case missing(err):
		cs = append(cs, Check{Fail, "config", "no config: run tgbridge init", initFix})
	case err != nil:
		cs = append(cs, Check{Fail, "config", err.Error(), "fix " + e.ConfigPath + " or move it away and " + initFix})
	default:
		cs = append(cs, Check{OK, "config", e.ConfigPath, ""})
		if tok, err := cfg.Token(); err != nil {
			cs = append(cs, Check{Fail, "token", err.Error(), initFix})
		} else if me, err := getMe(ctx, e, tok); err != nil {
			cs = append(cs, Check{Fail, "token", "getMe: " + err.Error(), "check the network, or " + initFix + " to enter a new token"})
		} else if !me.HasTopicsEnabled {
			cs = append(cs, Check{Fail, "token", "@" + me.Username + ": Threaded Mode is off", threadedFix})
		} else {
			cs = append(cs, Check{OK, "token", "@" + me.Username, ""})
		}
		if cfg.OwnerID == 0 {
			cs = append(cs, Check{Fail, "owner_id", "not set", initFix})
		} else {
			cs = append(cs, Check{OK, "owner_id", fmt.Sprint(cfg.OwnerID), ""})
		}
	}

	var nse *NoServiceError
	switch {
	case errors.As(e.SvcErr, &nse):
		cs = append(cs, Check{Warn, "service", nse.Why, nse.Fix})
	case e.Svc == nil:
		cs = append(cs, Check{Info, "service", e.noService(), ""})
	case !e.Svc.Installed():
		cs = append(cs, Check{Fail, "service", "not installed", "tgbridge service install"})
	case !e.Svc.Active(ctx):
		cs = append(cs, Check{Fail, "service", e.Svc.Name() + " " + e.Svc.Path() + " installed but not running", logHint(e.GOOS)})
	default:
		cs = append(cs, Check{OK, "service", e.Svc.Name() + " running", ""})
	}

	if e.GOOS == "linux" {
		name := os.Getenv("USER")
		if u, err := user.Current(); name == "" && err == nil {
			name = u.Username
		}
		out, _ := e.Run(ctx, "loginctl", "show-user", name, "-p", "Linger", "--value")
		if strings.TrimSpace(out) == "yes" {
			cs = append(cs, Check{OK, "linger", "on: the service runs while you're logged out", ""})
		} else {
			cs = append(cs, Check{Warn, "linger", "off: the service stops when you log out", "sudo loginctl enable-linger $USER"})
		}
	}

	if cfg != nil && cfg.OrcaEnabled {
		if _, err := os.Stat(cfg.OrcaCLI); err == nil {
			cs = append(cs, Check{OK, "orca", cfg.OrcaCLI, ""})
		} else {
			cs = append(cs, Check{Warn, "orca", "orca_enabled but " + cfg.OrcaCLI + " not found", "set orca_cli in " + e.ConfigPath + ", or remove orca_enabled"})
		}
	}

	if b, _ := os.ReadFile(e.ClaudeSettings); strings.Contains(string(b), "tgbridge hook") {
		cs = append(cs, Check{OK, "hooks", "installed", ""})
	} else {
		cs = append(cs, Check{Info, "hooks", "not installed", "optional: tgbridge hooks install"})
	}
	return cs
}
