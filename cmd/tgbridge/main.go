// Command tgbridge drives Claude Code sessions from a private Telegram chat.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/romcodedao/tgbridge/internal/bot"
	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/engine"
	"github.com/romcodedao/tgbridge/internal/handoff"
	"github.com/romcodedao/tgbridge/internal/hookcmd"
	"github.com/romcodedao/tgbridge/internal/install"
	"github.com/romcodedao/tgbridge/internal/ipc"
	"github.com/romcodedao/tgbridge/internal/mcp"
	"github.com/romcodedao/tgbridge/internal/orca"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

var version = "dev"

func usage() {
	fmt.Fprintln(os.Stderr, `usage: tgbridge <command>
  serve               run the bridge (config: $TGBRIDGE_CONFIG or ~/.config/tgbridge/config.yaml)
  whoami              wait for a message to the bot and print the sender's user ID (for owner_id)
  install             register hooks and the MCP server with Claude Code
  uninstall           remove them
  mcp                 stdio MCP server (started by Claude)
  hook <event>        Claude Code hook command
  version`)
	os.Exit(2)
}

func configPath() string {
	if p := os.Getenv("TGBRIDGE_CONFIG"); p != "" {
		return p
	}
	return config.DefaultPath()
}

// socketPath works without a config file (mcp and hook run inside other people's sessions).
func socketPath() string {
	if p := os.Getenv("TGBRIDGE_SOCKET"); p != "" {
		return p
	}
	if c, err := config.Load(configPath()); err == nil {
		return c.SocketPath
	}
	return config.DefaultSocket()
}

func self() string {
	p, err := os.Executable()
	if err != nil {
		return "tgbridge"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func claudeSettings() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "settings.json")
}

func runClaude(args ...string) error {
	cmd := exec.Command("claude", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "serve":
		if err := serve(); err != nil {
			log.Fatal(err)
		}
	case "whoami":
		if err := whoami(); err != nil {
			log.Fatal(err)
		}
	case "mcp":
		sock := socketPath()
		mcp.ServeContext(os.Stdin, os.Stdout, func(ctx context.Context, r ipc.Request) (ipc.Response, error) { return ipc.Call(ctx, sock, r) })
	case "hook":
		if len(os.Args) < 3 {
			usage()
		}
		ev, sock := os.Args[2], socketPath()
		timeout := 25 * time.Second
		if ev == "PermissionRequest" {
			timeout = 650 * time.Second
		}
		os.Exit(hookcmd.Run(ev, os.Stdin, os.Stdout, func(r ipc.Request) (ipc.Response, error) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			return ipc.Call(ctx, sock, r)
		}))
	case "install":
		if err := install.Install(claudeSettings(), self(), runClaude); err != nil {
			log.Fatal(err)
		}
		fmt.Println("installed: hooks in ~/.claude/settings.json (backup: settings.json.tgbridge.bak) and user-scope MCP server 'tgbridge'")
	case "uninstall":
		if err := install.Uninstall(claudeSettings(), runClaude); err != nil {
			log.Fatal(err)
		}
		fmt.Println("uninstalled")
	default:
		usage()
	}
}

// whoami runs before owner_id is set, so it reads the config without that check.
// The service must be stopped: two getUpdates callers on one token conflict.
func whoami() error {
	cfg, err := config.Read(configPath())
	if errors.Is(err, fs.ErrNotExist) {
		cfg, err = config.Defaults(), nil
	}
	if err != nil {
		return err
	}
	token, err := cfg.Token()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tg := telegram.New(token, "")
	me, err := tg.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("getMe: %w (check the token)", err)
	}
	since := time.Now().Add(-time.Minute) // clock skew margin; older queued messages are ignored
	fmt.Printf("Send any message to @%s now, in a private chat (Ctrl+C to cancel)...\n", me.Username)
	u, err := tg.FirstSender(ctx, since)
	if err != nil {
		return whoamiErr(err)
	}
	name := u.FirstName
	if u.Username != "" {
		name += " (@" + u.Username + ")"
	}
	fmt.Printf("Message from %s, user ID %d. Check this is you: this account will control Claude on this machine.\nPut this line in %s:\nowner_id: %d\n", name, u.ID, configPath(), u.ID)
	return nil
}

// whoamiErr explains 409 Conflict: another getUpdates caller (the running service) or a webhook owns the token.
func whoamiErr(err error) error {
	var ae *telegram.APIError
	if errors.As(err, &ae) && ae.Code == 409 {
		return fmt.Errorf("%w\nThe bot is already being read elsewhere. Stop the service first: systemctl --user stop tgbridge\n(or, if a webhook is set: curl https://api.telegram.org/bot<token>/deleteWebhook)", err)
	}
	return err
}

// orcaFor returns the Orca CLI only when the integration is on; nil means serve never calls Orca.
func orcaFor(cfg *config.Config) *orca.CLI {
	if !cfg.OrcaEnabled {
		return nil
	}
	return orca.New(cfg.OrcaCLI)
}

func serve() error {
	cfg, err := config.Load(configPath())
	if err != nil {
		return err
	}
	token, err := cfg.Token()
	if err != nil {
		return err
	}
	os.Unsetenv("TGBRIDGE_TOKEN") // never inherited by claude, MCP servers or approved commands
	if cfg.HandoffPermissionTimeout > 10*time.Minute {
		log.Printf("handoff_permission_timeout capped at 10m (the hook times out at 11m)")
		cfg.HandoffPermissionTimeout = 10 * time.Minute
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tg := telegram.New(token, "")
	me, err := tg.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("getMe: %w (check the token)", err)
	}
	if !me.HasTopicsEnabled {
		log.Printf("warning: Threaded Mode is off for @%s (BotFather Mini App → Threads Settings); topics will fail", me.Username)
	}
	st, err := store.Open(cfg.StateDir)
	if err != nil {
		return err
	}
	mgr := engine.NewManager(engine.ManagerConfig{MaxLive: cfg.Engine.MaxLive, IdleTimeout: cfg.Engine.IdleTimeout}, engine.Start)
	deps := bot.Deps{Cfg: cfg, TG: tg, Mgr: mgr, Store: st, Audit: store.Audit(cfg.StateDir), BotUsername: me.Username, SelfBin: self()}
	oc := orcaFor(cfg)
	if oc != nil {
		deps.Orca = oc // set only when enabled: a nil *orca.CLI in the interface would look enabled to the bot
	}
	b := bot.New(deps)
	if oc != nil {
		home, _ := os.UserHomeDir()
		h := handoff.New(handoff.Deps{TG: tg, Store: st, Orca: oc, Ask: b.Ask, Owner: cfg.OwnerID, ClaudeHome: filepath.Join(home, ".claude"),
			PermTimeout: cfg.HandoffPermissionTimeout, BotUser: me.Username, UploadsDir: cfg.Uploads.Dir})
		b.SetHandoff(h)
		h.Start(ctx)
	}

	go func() {
		if err := ipc.Serve(ctx, cfg.SocketPath, b.HandleIPC); err != nil && ctx.Err() == nil {
			log.Printf("ipc: %v", err)
			stop()
		}
	}()
	go b.Run(ctx)
	log.Printf("tgbridge %s serving @%s for owner %d", version, me.Username, cfg.OwnerID)
	b.Updates(ctx, telegram.NewPoller(tg, 50))
	log.Printf("shutting down")
	mgr.Shutdown()
	return nil
}
