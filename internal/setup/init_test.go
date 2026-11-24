package setup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	goodToken  = "123:good-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	wrongToken = "123:wrong-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // well formed, but Telegram rejects it
)

// events is the shared, ordered log of fake Telegram and fake service calls.
type events struct {
	mu  sync.Mutex
	log []string
}

func (e *events) add(s string) { e.mu.Lock(); e.log = append(e.log, s); e.mu.Unlock() }
func (e *events) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.log)
}

type fakeBot struct {
	ev      *events
	topics  bool
	mu      sync.Mutex
	senders []int64 // returned one per long poll
}

func (f *fakeBot) serve(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/bot"), "/")
		token, method := parts[0], parts[len(parts)-1]
		if token != goodToken {
			f.ev.add("rejected " + token)
			w.WriteHeader(401)
			io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
			return
		}
		switch method {
		case "getMe":
			fmt.Fprintf(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"B","username":"testbot","has_topics_enabled":%v}}`, f.topics)
		case "getUpdates":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["timeout"] == float64(0) { // confirm call
				io.WriteString(w, `{"ok":true,"result":[]}`)
				return
			}
			f.ev.add("getUpdates")
			f.mu.Lock()
			if len(f.senders) == 0 {
				f.mu.Unlock()
				<-r.Context().Done()
				return
			}
			id := f.senders[0]
			f.senders = f.senders[1:]
			f.mu.Unlock()
			fmt.Fprintf(w, `{"ok":true,"result":[{"update_id":%d,"message":{"message_id":1,"date":%d,"from":{"id":%d,"first_name":"U%d"},"chat":{"id":%d,"type":"private"},"text":"hi"}}]}`,
				id, time.Now().Unix(), id, id, id)
		default:
			io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type fakeSvc struct {
	ev                *events
	installed, active bool
	installs          int
	env               map[string]string // from the last Install
	bak               string            // Install reports this backup
}

func (s *fakeSvc) Name() string                    { return "fakesvc" }
func (s *fakeSvc) Path() string                    { return "/fake/unit" }
func (s *fakeSvc) Installed() bool                 { return s.installed }
func (s *fakeSvc) Active(context.Context) bool     { return s.active }
func (s *fakeSvc) Uninstall(context.Context) error { s.installed = false; return nil }
func (s *fakeSvc) Stop(context.Context) error      { s.ev.add("stop"); s.active = false; return nil }
func (s *fakeSvc) Install(_ context.Context, bin, _, path string, env map[string]string) (string, error) {
	s.ev.add("install")
	s.installs++
	s.installed, s.active, s.env = true, true, env
	return s.bak, nil
}

type rig struct {
	env  Env
	ev   *events
	bot  *fakeBot
	svc  *fakeSvc
	home string
}

func newRig(t *testing.T, senders ...int64) *rig {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TGBRIDGE_TOKEN", "")
	t.Setenv("USER", "me")
	ex, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	bot := &fakeBot{ev: ev, topics: true, senders: senders}
	svc := &fakeSvc{ev: ev}
	run, _ := fake(map[string]string{
		"/x/claude --version":                     "2.1.0 (Claude Code)",
		"/x/claude auth status":                   `{"loggedIn":true,"authMethod":"claude.ai"}`,
		"loginctl show-user me -p Linger --value": "yes\n",
	}, nil)
	return &rig{ev: ev, bot: bot, svc: svc, home: home, env: Env{
		ConfigPath: filepath.Join(home, ".config/tgbridge/config.yaml"), Home: home, GOOS: "linux",
		Bin: "/b/tgbridge", Version: "v0.1.0", TelegramBase: bot.serve(t), Example: ex,
		Run: run, LookPath: found, Svc: svc, ClaudeSettings: filepath.Join(home, ".claude/settings.json"),
	}}
}

func (r *rig) tokenPath() string { return filepath.Join(r.home, ".config/tgbridge/token") }

// prompt answers Secret from secrets in order; a Secret call past the list fails the test.
func prompt(t *testing.T, in string, secrets ...string) (Prompt, *bytes.Buffer, *int) {
	out, n := &bytes.Buffer{}, new(int)
	return Prompt{In: bufio.NewReader(strings.NewReader(in)), Out: out, Secret: func() (string, error) {
		if *n >= len(secrets) {
			t.Fatalf("unexpected Secret call %d", *n+1)
		}
		*n++
		return secrets[*n-1], nil
	}}, out, n
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mode(t *testing.T, p string) os.FileMode {
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitFreshMachine(t *testing.T) {
	r := newRig(t, 4242)
	p, out, _ := prompt(t, "y\n", goodToken)
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if mode(t, r.env.ConfigPath) != 0o600 || mode(t, r.tokenPath()) != 0o600 || mode(t, filepath.Dir(r.env.ConfigPath)) != 0o700 {
		t.Fatal("config dir 0700, config and token 0600")
	}
	if read(t, r.tokenPath()) != goodToken {
		t.Fatal("token not saved")
	}
	if !strings.Contains(read(t, r.env.ConfigPath), "\nowner_id: 4242\n") && !strings.HasPrefix(read(t, r.env.ConfigPath), "owner_id: 4242\n") {
		t.Fatalf("owner_id not set:\n%s", read(t, r.env.ConfigPath))
	}
	if r.svc.installs != 1 {
		t.Fatal("service not installed")
	}
	s := out.String()
	for _, w := range []string{"U4242", "4242", "Is this you? [y/N]", "ok", "Done. Send @testbot a message"} {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in\n%s", w, s)
		}
	}
}

func TestInitIdempotent(t *testing.T) {
	r := newRig(t, 4242)
	p, _, _ := prompt(t, "y\n", goodToken)
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatal(err)
	}
	cfg, tok := read(t, r.env.ConfigPath), read(t, r.tokenPath())
	p, out, n := prompt(t, "") // nothing to answer: any question fails
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if *n != 0 || strings.Contains(out.String(), "Is this you") {
		t.Fatalf("second run asked something:\n%s", out)
	}
	if read(t, r.env.ConfigPath) != cfg || read(t, r.tokenPath()) != tok {
		t.Fatal("config or token changed")
	}
	if r.svc.installs != 2 {
		t.Fatalf("service must be refreshed, installs=%d", r.svc.installs)
	}
	if got := strings.Count(strings.Join(r.ev.list(), " "), "getUpdates"); got != 1 {
		t.Fatalf("second run must not wait for a message, getUpdates=%d", got)
	}
}

func TestInitBadTokenRetries(t *testing.T) {
	r := newRig(t, 4242)
	p, out, n := prompt(t, "y\n", wrongToken, goodToken)
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if *n != 2 || read(t, r.tokenPath()) != goodToken {
		t.Fatalf("want a re-prompt and only the good token saved, calls=%d", *n)
	}

	r = newRig(t)
	p, _, _ = prompt(t, "", "bad1", "bad2", "bad3")
	if err := Init(context.Background(), r.env, p, false); err == nil {
		t.Fatal("want an error after 3 bad tokens")
	}
	if _, err := os.Stat(r.tokenPath()); !os.IsNotExist(err) {
		t.Fatal("a bad token was saved")
	}
}

func TestInitStopsServiceBeforeWhoami(t *testing.T) {
	r := newRig(t, 4242)
	r.svc.installed, r.svc.active = true, true
	p, out, _ := prompt(t, "y\n", goodToken)
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	ev := r.ev.list()
	stop, get := slices.Index(ev, "stop"), slices.Index(ev, "getUpdates")
	if stop < 0 || get < 0 || stop > get {
		t.Fatalf("the service must be stopped before getUpdates: %v", ev)
	}
}

func TestInitRejectOwnerWaitsForNext(t *testing.T) {
	r := newRig(t, 111, 222)
	p, out, _ := prompt(t, "n\ny\n", goodToken)
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c := read(t, r.env.ConfigPath)
	if !strings.Contains(c, "owner_id: 222\n") || strings.Contains(c, "111") {
		t.Fatalf("want the second sender:\n%s", c)
	}
}

func TestInitNoService(t *testing.T) {
	r := newRig(t, 4242)
	p, out, _ := prompt(t, "y\n", goodToken)
	if err := Init(context.Background(), r.env, p, true); err != nil {
		t.Fatal(err)
	}
	if r.svc.installs != 0 || !strings.Contains(out.String(), "tgbridge service install") {
		t.Fatalf("--no-service must skip the install with a hint:\n%s", out)
	}
}

func TestSetOwner(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string][2]string{
		"replace": {"# head\nowner_id: 0   # your ID\nfoo: 1  # keep\n", "# head\nowner_id: 42\nfoo: 1  # keep\n"},
		"prepend": {"# head\nfoo: 1\n", "owner_id: 42\n# head\nfoo: 1\n"},
	} {
		p := filepath.Join(dir, name)
		must(t, os.WriteFile(p, []byte(c[0]), 0o600))
		if err := SetOwner(p, 42); err != nil {
			t.Fatal(err)
		}
		if got := read(t, p); got != c[1] || mode(t, p) != 0o600 {
			t.Errorf("%s: got %q mode %v", name, got, mode(t, p))
		}
	}
}

func TestInitOfflineKeepsToken(t *testing.T) {
	r := newRig(t)
	must(t, os.MkdirAll(filepath.Dir(r.env.ConfigPath), 0o700))
	must(t, os.WriteFile(r.tokenPath(), []byte(goodToken), 0o600))
	r.env.TelegramBase = "http://127.0.0.1:1" // nothing listens: a network error, not a bad token
	p, _, n := prompt(t, "")
	if err := Init(context.Background(), r.env, p, false); err == nil || *n != 0 || read(t, r.tokenPath()) != goodToken {
		t.Fatalf("offline must fail without asking for a new token: err=%v asks=%d", err, *n)
	}
}

func TestInitOwnerAnswers(t *testing.T) {
	for in, want := range map[string]string{"\ny\n": "8", "YES\n": "7", " y \n": "7", "x\ny\n": "7", "no\ny\n": "8"} {
		r := newRig(t, 7, 8)
		p, out, _ := prompt(t, in, goodToken)
		if err := Init(context.Background(), r.env, p, false); err != nil {
			t.Fatalf("%q: %v\n%s", in, err, out)
		}
		if c := read(t, r.env.ConfigPath); !strings.Contains(c, "owner_id: "+want+"\n") {
			t.Errorf("%q: want owner %s:\n%s", in, want, c)
		}
		polls := strings.Count(strings.Join(r.ev.list(), " "), "getUpdates")
		if in == "x\ny\n" && (polls != 1 || !strings.Contains(out.String(), "Please answer y or n")) {
			t.Errorf("an unclear answer must ask again without polling: polls=%d\n%s", polls, out)
		}
	}
	r := newRig(t, 7)
	p, _, _ := prompt(t, "", goodToken)
	if err := Init(context.Background(), r.env, p, false); err == nil {
		t.Fatal("EOF must be an error")
	}
}

func TestInitCancelledAfterStopSaysServiceStopped(t *testing.T) {
	r := newRig(t) // no sender: FirstSender waits until cancelled
	r.svc.installed, r.svc.active = true, true
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p, out, _ := prompt(t, "", goodToken)
	if err := Init(ctx, r.env, p, false); err == nil {
		t.Fatal("want the cancel error")
	}
	if !strings.Contains(out.String(), "service is stopped; start it with: tgbridge service install") {
		t.Fatalf("want the restart hint:\n%s", out)
	}
}

func TestInitTightensConfigDir(t *testing.T) {
	r := newRig(t, 7)
	must(t, os.MkdirAll(filepath.Dir(r.env.ConfigPath), 0o755))
	p, _, _ := prompt(t, "y\n", goodToken)
	must(t, Init(context.Background(), r.env, p, false))
	if mode(t, filepath.Dir(r.env.ConfigPath)) != 0o700 || mode(t, r.env.ConfigPath) != 0o600 || mode(t, r.tokenPath()) != 0o600 {
		t.Fatal("dir 0700, files 0600")
	}
	ents, _ := os.ReadDir(filepath.Dir(r.env.ConfigPath))
	if len(ents) != 2 {
		t.Fatalf("temp files left behind: %v", ents)
	}
}

func TestInitLeavesForeignConfigDir(t *testing.T) {
	r := newRig(t, 7)
	dir := filepath.Join(t.TempDir(), "mine")
	must(t, os.Mkdir(dir, 0o755))
	r.env.ConfigPath = filepath.Join(dir, "tgbridge.yaml") // e.g. TGBRIDGE_CONFIG=~/tgbridge.yaml
	p, _, _ := prompt(t, "y\n", goodToken)
	must(t, Init(context.Background(), r.env, p, false))
	if mode(t, dir) != 0o755 {
		t.Fatalf("a dir that isn't tgbridge's own must keep its mode, got %v", mode(t, dir))
	}
}

func TestInitCustomConfigReachesService(t *testing.T) {
	r := newRig(t, 7)
	r.env.ConfigPath = filepath.Join(r.home, "x.yaml") // TGBRIDGE_CONFIG=~/x.yaml
	p, out, _ := prompt(t, "y\n", goodToken)
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.svc.env["TGBRIDGE_CONFIG"] != r.env.ConfigPath {
		t.Fatalf("service env %v", r.svc.env)
	}
}

func TestInitDefaultConfigNoServiceEnv(t *testing.T) {
	r := newRig(t, 7)
	p, _, _ := prompt(t, "y\n", goodToken)
	must(t, Init(context.Background(), r.env, p, false))
	if r.svc.installs != 1 || r.svc.env != nil {
		t.Fatalf("default config: no env, got %v", r.svc.env)
	}
}

func TestInitWritesEnvTokenToFile(t *testing.T) {
	r := newRig(t, 7)
	t.Setenv("TGBRIDGE_TOKEN", goodToken)
	p, out, n := prompt(t, "y\n") // no Secret answers: the env token must be used
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if *n != 0 || read(t, r.tokenPath()) != goodToken || mode(t, r.tokenPath()) != 0o600 {
		t.Fatalf("env token not saved to the token file, asks=%d", *n)
	}
	if !strings.Contains(out.String(), "TGBRIDGE_TOKEN") {
		t.Fatalf("want a note about TGBRIDGE_TOKEN:\n%s", out)
	}
}

func TestInitEnvTokenKeepsExistingFile(t *testing.T) {
	r := newRig(t, 7)
	must(t, os.MkdirAll(filepath.Dir(r.tokenPath()), 0o700))
	must(t, os.WriteFile(r.tokenPath(), []byte("999:other"), 0o600))
	t.Setenv("TGBRIDGE_TOKEN", goodToken)
	p, out, _ := prompt(t, "y\n")
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if read(t, r.tokenPath()) != "999:other" {
		t.Fatal("an existing token file must not be overwritten")
	}
}

func TestInitReportsUnitBackup(t *testing.T) {
	r := newRig(t, 7)
	r.svc.bak = "/fake/unit.bak"
	p, out, _ := prompt(t, "y\n", goodToken)
	must(t, Init(context.Background(), r.env, p, false))
	if !strings.Contains(out.String(), "previous unit saved to /fake/unit.bak") {
		t.Fatalf("want the backup note:\n%s", out)
	}
}

func TestInitNoServiceAfterStopSaysServiceStopped(t *testing.T) {
	r := newRig(t, 7)
	r.svc.installed, r.svc.active = true, true
	p, out, _ := prompt(t, "y\n", goodToken)
	must(t, Init(context.Background(), r.env, p, true))
	if !strings.Contains(out.String(), "service is stopped; start it with: tgbridge service install") {
		t.Fatalf("want the restart hint:\n%s", out)
	}
}

func TestInitTempBinarySkipsService(t *testing.T) {
	r := newRig(t, 7)
	r.env.Bin = filepath.Join(os.TempDir(), "go-build1/b001/exe/tgbridge") // go run
	p, out, _ := prompt(t, "y\n", goodToken)
	must(t, Init(context.Background(), r.env, p, false))
	if r.svc.installs != 0 || !strings.Contains(out.String(), "a temporary file; install tgbridge first (see docs/install.md)") {
		t.Fatalf("a temp binary must not be installed as the service (installs=%d):\n%s", r.svc.installs, out)
	}
}

func TestInitSaysWhyNoService(t *testing.T) {
	r := newRig(t, 7)
	r.env.Svc, r.env.SvcErr = nil, errors.New("systemd isn't running: enable it in /etc/wsl.conf")
	p, out, _ := prompt(t, "y\n", goodToken)
	must(t, Init(context.Background(), r.env, p, false))
	if !strings.Contains(out.String(), "/etc/wsl.conf") {
		t.Fatalf("want the reason:\n%s", out)
	}
}

func TestSetOwnerKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "dotfiles-config.yaml"), filepath.Join(dir, "config.yaml")
	must(t, os.WriteFile(real, []byte("owner_id: 0\n"), 0o600))
	must(t, os.Symlink(real, link))
	must(t, SetOwner(link, 42))
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a file")
	}
	if read(t, real) != "owner_id: 42\n" {
		t.Fatalf("target not updated: %q", read(t, real))
	}
}

func TestInitTokenFormat(t *testing.T) {
	r := newRig(t, 7)
	p, out, n := prompt(t, "y\n", "not a token", "123:short", goodToken)
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if *n != 3 || strings.Count(out.String(), "that doesn't look like a bot token (123456:ABC...)") != 2 {
		t.Fatalf("want two format errors and a third ask, asks=%d:\n%s", *n, out)
	}
	if ev := strings.Join(r.ev.list(), " "); strings.Contains(ev, "rejected") {
		t.Fatalf("a malformed token reached Telegram: %s", ev)
	}

	r = newRig(t)
	p, _, _ = prompt(t, "", "x", "y", "z") // malformed tries count
	if err := Init(context.Background(), r.env, p, false); err == nil || !strings.Contains(err.Error(), "3 tries") {
		t.Fatalf("want the 3 tries error, got %v", err)
	}
}

// A pasted token with an extra newline leaves a blank line in the shared reader: it must not answer "Is this you?".
func TestInitBufferedNewlineDoesNotConfirm(t *testing.T) {
	r := newRig(t, 7)
	in := bufio.NewReader(strings.NewReader(goodToken + "\n\n"))
	out := &bytes.Buffer{}
	p := Prompt{In: in, Out: out, Secret: func() (string, error) { s, err := in.ReadString('\n'); return strings.TrimSpace(s), err }}
	if err := Init(context.Background(), r.env, p, false); err == nil || !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF waiting for an answer, got %v\n%s", err, out)
	}
	if c := read(t, r.env.ConfigPath); strings.Contains(c, "owner_id: 7") {
		t.Fatalf("owner_id saved without an answer:\n%s", c)
	}
}

// Re-running init keeps a running service when nothing changed, and restarts it when the binary at the same path was
// replaced (install.sh or make setup): the unit names the binary's version and sha256.
func TestInitRerunRestartsOnlyForNewBinary(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	for _, c := range []struct{ goos, activeCmd, activeOut, restartCmd string }{
		{"linux", "systemctl --user is-active tgbridge", "active\n", "systemctl --user restart tgbridge"},
		{"darwin", "launchctl print gui/" + uid + "/io.github.romcodedao.tgbridge", "state = running", "launchctl bootstrap gui/" + uid},
	} {
		t.Run(c.goos, func(t *testing.T) {
			r := newRig(t, 7)
			t.Setenv("TMPDIR", filepath.Join(r.home, "tmp")) // the binary below must not look like a go run build
			r.env.Bin = filepath.Join(r.home, ".local/bin/tgbridge")
			must(t, write(r.env.Bin, "binary v1"))
			run, calls := fake(map[string]string{
				"/x/claude --version":   "2.1.0 (Claude Code)",
				"/x/claude auth status": `{"loggedIn":true,"authMethod":"claude.ai"}`,
				c.activeCmd:             c.activeOut,
			}, nil)
			r.env.Run, r.env.GOOS = run, c.goos
			r.env.Svc, _ = ForOS(c.goos, r.home, run)
			restarts := func() int { return strings.Count(strings.Join(*calls, "\n"), c.restartCmd) }
			initOnce := func(in string, secrets ...string) {
				t.Helper()
				p, out, _ := prompt(t, in, secrets...)
				if err := Init(context.Background(), r.env, p, false); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			}
			initOnce("y\n", goodToken)
			if restarts() != 1 {
				t.Fatalf("first run must start the service: %v", *calls)
			}
			initOnce("")
			if restarts() != 1 {
				t.Fatalf("same path, same binary: no restart, got %v", *calls)
			}
			must(t, write(r.env.Bin, "binary v2")) // an upgrade replaces the file in place
			initOnce("")
			if restarts() != 2 {
				t.Fatalf("same path, new binary: must restart, got %v", *calls)
			}
		})
	}
}

// Typed-ahead terminal input is flushed right before each "Is this you?".
func TestInitFlushesBeforeConfirm(t *testing.T) {
	r := newRig(t, 7, 8)
	p, out, _ := prompt(t, "n\ny\n", goodToken)
	p.Flush = func() { out.WriteString("<flush>") }
	if err := Init(context.Background(), r.env, p, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s := out.String()
	if strings.Count(s, "<flush>Is this you?") != 2 || strings.Count(s, "<flush>") != 2 {
		t.Fatalf("want a flush right before each confirm:\n%s", s)
	}
}
