// Command tgbridge drives Claude Code sessions from a private Telegram chat.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/romcodedao/tgbridge"
	"github.com/romcodedao/tgbridge/internal/bot"
	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/engine"
	"github.com/romcodedao/tgbridge/internal/handoff"
	"github.com/romcodedao/tgbridge/internal/hookcmd"
	"github.com/romcodedao/tgbridge/internal/install"
	"github.com/romcodedao/tgbridge/internal/ipc"
	"github.com/romcodedao/tgbridge/internal/mcp"
	"github.com/romcodedao/tgbridge/internal/orca"
	"github.com/romcodedao/tgbridge/internal/setup"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

var version = "dev"

func usage() {
	fmt.Fprintln(os.Stderr, `usage: tgbridge <command>
  init [--no-service] set up (or repair) tgbridge step by step; safe to re-run
  doctor              check the install and print how to fix each problem
  service install|uninstall|status
                      the background service (systemd user unit or launchd agent)
  hooks install|uninstall
                      register or remove the hooks and the MCP server with Claude Code
  serve               run the bridge (config: $TGBRIDGE_CONFIG or ~/.config/tgbridge/config.yaml)
  whoami              wait for a message to the bot and print the sender's user ID (for owner_id)
  install             same as hooks install
  uninstall           remove the hooks and the MCP server
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
		fmt.Println(ver())
	case "init":
		noSvc := len(os.Args) > 2 && os.Args[2] == "--no-service"
		if len(os.Args) > 2 && !noSvc {
			usage()
		}
		in := bufio.NewReader(os.Stdin)
		if err := setup.Init(context.Background(), env(), setup.Prompt{In: in, Out: os.Stdout, Secret: secret(in)}, noSvc); err != nil {
			log.Fatal(err)
		}
	case "doctor":
		cs := setup.Doctor(context.Background(), env())
		setup.Print(os.Stdout, cs)
		if setup.Failed(cs) {
			os.Exit(1)
		}
	case "service":
		if len(os.Args) < 3 {
			usage()
		}
		os.Exit(service(os.Args[2]))
	case "hooks":
		if len(os.Args) < 3 {
			usage()
		}
		switch os.Args[2] {
		case "install":
			hooksInstall()
		case "uninstall":
			hooksUninstall()
		default:
			usage()
		}
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
		hooksInstall()
	case "uninstall":
		hooksUninstall()
	default:
		usage()
	}
}

func hooksInstall() {
	if err := install.Install(claudeSettings(), self(), runClaude); err != nil {
		log.Fatal(err)
	}
	fmt.Println("installed: hooks in ~/.claude/settings.json (backup: settings.json.tgbridge.bak) and user-scope MCP server 'tgbridge'")
}

func hooksUninstall() {
	if err := install.Uninstall(claudeSettings(), runClaude); err != nil {
		log.Fatal(err)
	}
	fmt.Println("uninstalled")
}

// ver is the release version, or the module version for `go install` builds.
func ver() string {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

func env() setup.Env {
	home, _ := os.UserHomeDir()
	svc, _ := setup.ForOS(runtime.GOOS, home, run)
	return setup.Env{ConfigPath: configPath(), Home: home, GOOS: runtime.GOOS, Bin: self(), Version: ver(),
		Example: tgbridge.ExampleConfig, Run: run, LookPath: exec.LookPath, Svc: svc, ClaudeSettings: claudeSettings()}
}

// secret reads a line from in with terminal echo off; Ctrl+C restores echo before exiting.
func secret(in *bufio.Reader) func() (string, error) {
	return func() (string, error) {
		stty := func(arg string) {
			c := exec.Command("stty", arg)
			c.Stdin = os.Stdin
			_ = c.Run() // not a terminal (piped input): nothing to hide
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		done := make(chan struct{})
		defer func() { signal.Stop(sig); close(done) }()
		go func() {
			select {
			case <-sig:
				stty("echo")
				fmt.Println()
				os.Exit(130)
			case <-done:
			}
		}()
		stty("-echo")
		defer stty("echo")
		line, err := in.ReadString('\n')
		fmt.Println()
		return strings.TrimSpace(line), err
	}
}

// service runs `tgbridge service <cmd>` and returns the exit code.
func service(cmd string) int {
	e, ctx := env(), context.Background()
	if e.Svc == nil {
		fmt.Fprintf(os.Stderr, "no service support on %s: run tgbridge inside WSL2, or run tgbridge serve yourself\n", e.GOOS)
		return 1
	}
	yes := map[bool]string{true: "yes", false: "no"}
	switch cmd {
	case "install":
		claude, _ := exec.LookPath(e.ClaudeBin())
		if err := e.Svc.Install(ctx, e.Bin, setup.ServicePATH(claude)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("installed and started: %s\n", e.Svc.Path())
	case "uninstall":
		if err := e.Svc.Uninstall(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("removed: %s\n", e.Svc.Path())
	case "status":
		active := e.Svc.Active(ctx)
		fmt.Printf("manager:   %s\nunit:      %s\ninstalled: %s\nactive:    %s\n", e.Svc.Name(), e.Svc.Path(), yes[e.Svc.Installed()], yes[active])
		if !active {
			return 1
		}
	default:
		usage()
	}
	return 0
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
	log.Printf("tgbridge %s serving @%s for owner %d", ver(), me.Username, cfg.OwnerID)
	b.Updates(ctx, telegram.NewPoller(tg, 50))
	log.Printf("shutting down")
	mgr.Shutdown()
	return nil
}
