package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/romcodedao/tgbridge/internal/telegram"
)

// Prompt is where init asks its questions. Secret reads a line without echo.
type Prompt struct {
	In     *bufio.Reader
	Out    io.Writer
	Secret func() (string, error)
}

// activeWait is how long init waits for the service to come up after installing it.
var activeWait = 5 * time.Second

// Init sets up tgbridge step by step, skipping every step that is already done, then prints the doctor summary.
func Init(ctx context.Context, e Env, p Prompt, noService bool) error {
	say := func(f string, a ...any) { fmt.Fprintf(p.Out, f+"\n", a...) }

	say("Claude Code:")
	ci, cerr := DetectClaude(ctx, e.ClaudeBin(), e.LookPath, e.Run)
	Print(p.Out, ClaudeChecks(ci, cerr))

	if _, err := os.Stat(e.ConfigPath); missing(err) {
		if err := os.MkdirAll(filepath.Dir(e.ConfigPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(e.ConfigPath, e.Example, 0o600); err != nil {
			return err
		}
		say("Wrote %s", e.ConfigPath)
	} else if err != nil {
		return err
	}
	cfg, err := e.readConfig()
	if err != nil {
		return err
	}

	var me *telegram.User
	token, err := cfg.Token()
	if err == nil {
		var ae *telegram.APIError
		if me, err = getMe(ctx, e, token); errors.As(err, &ae) {
			me = nil // Telegram rejected the token: ask for a new one
		} else if err != nil {
			return fmt.Errorf("can't reach Telegram to check the saved token: %w", err)
		}
	}
	if me == nil {
		say("Create a bot with @BotFather (/newbot) and paste its token here (input is hidden).")
		for i := 0; me == nil; i++ {
			if i == 3 {
				return errors.New("no working token after 3 tries: check it with @BotFather, then run tgbridge init again")
			}
			fmt.Fprint(p.Out, "Bot token: ")
			tok, err := p.Secret()
			if err != nil && tok == "" {
				return fmt.Errorf("read token: %w", err)
			}
			tok = strings.TrimSpace(tok)
			m, err := getMe(ctx, e, tok)
			if err != nil {
				say("That token didn't work (%v).", err)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(cfg.TokenFile), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(cfg.TokenFile, []byte(tok), 0o600); err != nil {
				return err
			}
			if err := os.Chmod(cfg.TokenFile, 0o600); err != nil {
				return err
			}
			say("Saved the token to %s", cfg.TokenFile)
			me, token = m, tok
		}
	}
	say("Bot: @%s", me.Username)

	if cfg.OwnerID == 0 {
		if err := askOwner(ctx, e, p, me, token, say); err != nil {
			return err
		}
	}

	switch {
	case noService:
		say("Skipped the service (--no-service). Later: tgbridge service install")
	case e.Svc == nil:
		say("No service manager on %s: on Windows, run tgbridge inside WSL2; otherwise run tgbridge serve yourself.", e.GOOS)
	default:
		if err := e.Svc.Install(ctx, e.Bin, ServicePATH(ci.Path)); err != nil {
			say("Service install failed: %v", err)
		} else {
			for end := time.Now().Add(activeWait); !e.Svc.Active(ctx) && time.Now().Before(end); {
				time.Sleep(250 * time.Millisecond)
			}
			say("Installed the %s service: %s", e.Svc.Name(), e.Svc.Path())
		}
	}

	say("\nSummary:")
	cs := Doctor(ctx, e)
	Print(p.Out, cs)
	if Failed(cs) {
		say("\nFix the FAIL lines above, then run tgbridge doctor.")
	} else {
		say("\nDone. Send @%s a message: a new topic starts your first session.", me.Username)
	}
	return nil
}

// askOwner finds the owner from the first private message to the bot.
// A running service also reads getUpdates (409 Conflict), so it is stopped first.
func askOwner(ctx context.Context, e Env, p Prompt, me *telegram.User, token string, say func(string, ...any)) error {
	if e.Svc != nil && e.Svc.Active(ctx) {
		if err := e.Svc.Stop(ctx); err != nil {
			return fmt.Errorf("stop the service: %w", err)
		}
	}
	tg := telegram.New(token, e.TelegramBase)
	since := time.Now().Add(-time.Minute) // clock skew margin
	say("Send any message to @%s now, in a private chat (Ctrl+C to cancel)...", me.Username)
	for {
		u, err := tg.FirstSender(ctx, since)
		if err != nil {
			return fmt.Errorf("wait for a message: %w", err)
		}
		name := u.FirstName
		if u.Username != "" {
			name += " (@" + u.Username + ")"
		}
		fmt.Fprintf(p.Out, "Message from %s, user ID %d. This account will control Claude on this machine.\nIs this you? [Y/n] ", name, u.ID)
		ans, err := p.In.ReadString('\n')
		if err != nil && ans == "" {
			return fmt.Errorf("read answer: %w", err)
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ans)), "n") {
			say("Waiting for the next message...")
			continue
		}
		if err := SetOwner(e.ConfigPath, u.ID); err != nil {
			return err
		}
		say("Saved owner_id: %d", u.ID)
		return nil
	}
}

var ownerLine = regexp.MustCompile(`(?m)^owner_id:.*$`)

// SetOwner sets owner_id in the config at path, leaving every other byte as it was.
func SetOwner(path string, id int64) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("owner_id: %d", id)
	if ownerLine.Match(b) {
		b = ownerLine.ReplaceAllLiteral(b, []byte(line))
	} else {
		b = append([]byte(line+"\n"), b...)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
