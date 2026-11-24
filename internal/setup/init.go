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
func Init(ctx context.Context, e Env, p Prompt, noService bool) (err error) {
	say := func(f string, a ...any) { fmt.Fprintf(p.Out, f+"\n", a...) }
	stopped := false // the owner step stopped a running service
	defer func() {
		if err != nil && stopped {
			say("service is stopped; start it with: tgbridge service install")
		}
	}()

	say("Claude Code:")
	ci, cerr := DetectClaude(ctx, e.ClaudeBin(), e.LookPath, e.Run)
	Print(p.Out, ClaudeChecks(ci, cerr))

	dir := filepath.Dir(e.ConfigPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if strings.Contains(filepath.Base(dir), "tgbridge") { // never chmod a shared dir like HOME or /etc
		if err := os.Chmod(dir, 0o700); err != nil { // the token usually lives here too
			return err
		}
	}
	if _, err := os.Stat(e.ConfigPath); missing(err) {
		if err := writeSecret(e.ConfigPath, e.Example); err != nil {
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
			if err := writeSecret(cfg.TokenFile, []byte(tok)); err != nil {
				return err
			}
			say("Saved the token to %s", cfg.TokenFile)
			me, token = m, tok
		}
	}
	if t := strings.TrimSpace(os.Getenv("TGBRIDGE_TOKEN")); t != "" && t == token {
		if _, err := os.Stat(cfg.TokenFile); missing(err) { // the service never sees this shell's TGBRIDGE_TOKEN
			if err := os.MkdirAll(filepath.Dir(cfg.TokenFile), 0o700); err != nil {
				return err
			}
			if err := writeSecret(cfg.TokenFile, []byte(t)); err != nil {
				return err
			}
			say("Saved TGBRIDGE_TOKEN to %s, so the service can read it", cfg.TokenFile)
		}
	}
	say("Bot: @%s", me.Username)

	if cfg.OwnerID == 0 {
		if err := askOwner(ctx, e, p, me, token, say, &stopped); err != nil {
			return err
		}
	}

	installed := false
	switch {
	case noService:
		say("Skipped the service (--no-service). Later: tgbridge service install")
	case e.Svc == nil:
		say("No service: %s", e.noService())
	case inTemp(e.Bin):
		say("Skipped the service: the service will run %s, a temporary file; install tgbridge first (see docs/install.md)", e.Bin)
	default:
		bak, err := e.Svc.Install(ctx, e.Bin, ServicePATH(ci.Path), ServiceEnv(e.ConfigPath))
		if bak != "" {
			say("previous unit saved to %s", bak)
		}
		if err != nil {
			say("Service install failed: %v", err)
		} else {
			installed = true
			for end := time.Now().Add(activeWait); !e.Svc.Active(ctx) && time.Now().Before(end); {
				time.Sleep(250 * time.Millisecond)
			}
			say("Installed the %s service: %s", e.Svc.Name(), e.Svc.Path())
		}
	}
	if stopped && !installed {
		say("service is stopped; start it with: tgbridge service install")
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
func askOwner(ctx context.Context, e Env, p Prompt, me *telegram.User, token string, say func(string, ...any), stopped *bool) error {
	if e.Svc != nil && e.Svc.Active(ctx) {
		if err := e.Svc.Stop(ctx); err != nil {
			return fmt.Errorf("stop the service: %w", err)
		}
		*stopped = true
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
		fmt.Fprintf(p.Out, "Message from %s, user ID %d. This account will control Claude on this machine.\n", name, u.ID)
		yes, err := confirm(ctx, p)
		if err != nil {
			return err
		}
		if !yes {
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

// confirm asks "Is this you?" until the answer is yes ("", y, yes) or no (n, no).
func confirm(ctx context.Context, p Prompt) (bool, error) {
	for {
		fmt.Fprint(p.Out, "Is this you? [Y/n] ")
		ans, err := readLine(ctx, p.In)
		if err != nil {
			return false, fmt.Errorf("read answer: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.Out, "Please answer y or n")
	}
}

// readLine reads one line, returning early when ctx is cancelled; an unterminated last line counts.
func readLine(ctx context.Context, in *bufio.Reader) (string, error) {
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	go func() { s, err := in.ReadString('\n'); ch <- res{s, err} }() // shortcut: leaks a blocked reader on cancel; init exits right after
	select {
	case r := <-ch:
		if r.err != nil && r.s != "" {
			r.err = nil
		}
		return r.s, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// inTemp reports whether bin is under the temp dir, like a `go run` build.
func inTemp(bin string) bool {
	tmp := os.TempDir()
	if r, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = r // macOS: /var/folders -> /private/var/folders, as self() resolves
	}
	for _, d := range []string{os.TempDir(), tmp} {
		if strings.HasPrefix(filepath.Clean(bin), filepath.Clean(d)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// writeSecret replaces path atomically with a 0600 file; a symlink's target is replaced, keeping the link.
func writeSecret(path string, b []byte) error {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // no-op after the rename
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
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
	return writeSecret(path, b)
}
