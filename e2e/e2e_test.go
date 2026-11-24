//go:build e2e

// End-to-end checks against the real Telegram bot, real `claude` (haiku) and, if installed, real Orca.
// Needs ~/.config/tgbridge/token and TGBRIDGE_E2E_OWNER (Telegram user id). Messages appear in the owner's chat.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/romcodedao/tgbridge/internal/bot"
	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/engine"
	"github.com/romcodedao/tgbridge/internal/handoff"
	"github.com/romcodedao/tgbridge/internal/ipc"
	"github.com/romcodedao/tgbridge/internal/orca"
	"github.com/romcodedao/tgbridge/internal/render"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/streamjson"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

// proxy forwards Bot API calls to Telegram and records request and response bodies.
type proxy struct {
	mu    sync.Mutex
	calls []rec
}
type rec struct{ Method, Req, Resp string }

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	resp, err := http.Post("https://api.telegram.org"+r.URL.Path, "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "proxy", 502)
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	p.mu.Lock()
	p.calls = append(p.calls, rec{r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], string(body), string(out)})
	p.mu.Unlock()
	w.WriteHeader(resp.StatusCode)
	w.Write(out)
}

func (p *proxy) has(method, substr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.calls {
		if (method == "" || c.Method == method) && strings.Contains(c.Req, substr) {
			return true
		}
	}
	return false
}

func (p *proxy) failures() []rec {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []rec
	for _, c := range p.calls {
		// answerCallbackQuery fails only because the e2e taps are synthetic (no real query id)
		if !strings.Contains(c.Resp, `"ok":true`) && c.Method != "getUpdates" && c.Method != "answerCallbackQuery" {
			out = append(out, c)
		}
	}
	return out
}

type rig struct {
	cfg  *config.Config
	tg   *telegram.Client
	px   *proxy
	st   *store.Store
	mgr  *engine.Manager
	b    *bot.Bot
	bin  string
	sock string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	owner, _ := strconv.ParseInt(os.Getenv("TGBRIDGE_E2E_OWNER"), 10, 64)
	if owner == 0 {
		t.Skip("set TGBRIDGE_E2E_OWNER")
	}
	home, _ := os.UserHomeDir()
	dir, _ := os.MkdirTemp("", "tgb-e2e-")
	t.Cleanup(func() { os.RemoveAll(dir) })
	bin := filepath.Join(dir, "tgbridge")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/tgbridge").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cfg := config.Defaults()
	cfg.OwnerID, cfg.Model = owner, "haiku"
	cfg.TokenFile = filepath.Join(home, ".config", "tgbridge", "token")
	cfg.StateDir, cfg.SocketPath = filepath.Join(dir, "state"), filepath.Join(dir, "s.sock")
	cfg.DefaultCwd = filepath.Join(dir, "work")
	os.MkdirAll(cfg.DefaultCwd, 0o700)
	cfg.OrcaCLI, cfg.OrcaEnabled = filepath.Join(home, ".config", "orca", "cli", "bin", "orca"), true
	token, err := cfg.Token()
	if err != nil {
		t.Fatal(err)
	}
	px := &proxy{}
	srv := httptest.NewServer(px)
	t.Cleanup(srv.Close)
	tg := telegram.New(token, srv.URL)
	me, err := tg.GetMe(context.Background())
	if err != nil || !me.HasTopicsEnabled {
		t.Fatalf("getMe: %v topics=%v", err, me.HasTopicsEnabled)
	}
	st, _ := store.Open(cfg.StateDir)
	mgr := engine.NewManager(engine.ManagerConfig{MaxLive: 3, IdleTimeout: time.Hour}, engine.Start)
	t.Cleanup(mgr.Shutdown)
	oc := orca.New(cfg.OrcaCLI)
	b := bot.New(bot.Deps{Cfg: cfg, TG: tg, Mgr: mgr, Store: st, Audit: store.Audit(cfg.StateDir), Orca: oc, BotUsername: me.Username, SelfBin: bin})
	h := handoff.New(handoff.Deps{TG: tg, Store: st, Orca: oc, Ask: b.Ask, Owner: owner, ClaudeHome: filepath.Join(home, ".claude"),
		PermTimeout: 5 * time.Minute, BotUser: me.Username})
	b.SetHandoff(h)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ipc.Serve(ctx, cfg.SocketPath, b.HandleIPC)
	h.Start(ctx)
	go b.Run(ctx)
	return &rig{cfg: cfg, tg: tg, px: px, st: st, mgr: mgr, b: b, bin: bin, sock: cfg.SocketPath}
}

func waitUntil(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (r *rig) msg(thread int, text string) telegram.Update {
	return telegram.Update{Message: &telegram.Message{MessageID: 1, MessageThreadID: thread, IsTopicMessage: thread != 0,
		From: &telegram.User{ID: r.cfg.OwnerID}, Chat: telegram.Chat{ID: r.cfg.OwnerID, Type: "private"}, Text: text}}
}

// approveNext taps Allow on the next pending approval, as the owner.
func (r *rig) approveNext(t *testing.T) {
	t.Helper()
	var n string
	waitUntil(t, 3*time.Minute, "an approval prompt", func() bool {
		if a := r.st.Approvals(); len(a) > 0 {
			n = a[0].Nonce
			return true
		}
		return false
	})
	r.b.HandleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{ID: "e2e", From: telegram.User{ID: r.cfg.OwnerID},
		Data: "a:" + n + ":y", Message: &telegram.Message{Chat: telegram.Chat{ID: r.cfg.OwnerID, Type: "private"}}}})
}

func TestTelegramRealRender(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	thread, err := r.tg.CreateForumTopic(ctx, r.cfg.OwnerID, "🧪 tgbridge e2e render")
	if err != nil {
		t.Fatal(err)
	}
	turn := &streamjson.Turn{Thinking: "Checking how the renderer handles each block type.", ThinkingDur: 2 * time.Second,
		Tools: []streamjson.ToolCall{{ID: "1", Name: "Bash", Summary: "go test ./...", Done: true}, {ID: "2", Name: "Read", Summary: "README.md", Done: true, Failed: true}},
		Text:  "## Result\n\nAll **good**, see [docs](https://core.telegram.org/bots/api) and `code`.\n\n```go\nfmt.Println(\"hi\")\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- one\n- two\n\n> quoted\n\n---\n\n~~old~~ done"}
	if err := r.tg.SendRichDraft(ctx, r.cfg.OwnerID, thread, 4242, render.Draft(turn), true); err != nil {
		t.Fatalf("draft: %v", err)
	}
	for _, m := range render.Final(turn, render.Footer(64200, 134*time.Second, 0.12)) {
		if _, err := r.tg.SendRichMessage(ctx, r.cfg.OwnerID, thread, m, nil); err != nil {
			t.Fatalf("final: %v", err)
		}
	}
	for _, h := range render.FallbackHTML(turn, "fallback footer") {
		if _, err := r.tg.SendMessage(ctx, telegram.SendMessageParams{ChatID: r.cfg.OwnerID, ThreadID: thread, Text: h, ParseMode: "HTML"}); err != nil {
			t.Fatalf("fallback: %v", err)
		}
	}
	big := &streamjson.Turn{Text: strings.Repeat("A long paragraph to test splitting. ", 3000)}
	for _, m := range render.Final(big, "split test") {
		if _, err := r.tg.SendRichMessage(ctx, r.cfg.OwnerID, thread, m, nil); err != nil {
			t.Fatalf("split part (%d blocks): %v", len(m.Blocks), err)
		}
	}
	r.tg.MarkTopicClosed(ctx, r.cfg.OwnerID, thread, "tgbridge e2e render")
}

func TestFullLoopRealClaude(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	marker := filepath.Join(r.cfg.DefaultCwd, "e2e-marker.txt")
	r.b.HandleUpdate(ctx, r.msg(0, "🧪 e2e: Remember the word PAPAYA. Then use the Bash tool to run exactly: echo e2e-ok > "+marker+" ; then reply with one short sentence."))
	r.approveNext(t)
	waitUntil(t, 3*time.Minute, "the marker file", func() bool { b, _ := os.ReadFile(marker); return strings.TrimSpace(string(b)) == "e2e-ok" })
	var tp store.Topic
	waitUntil(t, 3*time.Minute, "the first result", func() bool {
		ts := r.st.Topics()
		if len(ts) == 1 && ts[0].ContextTokens > 0 {
			tp = ts[0]
			return true
		}
		return false
	})
	if !r.px.has("sendRichMessageDraft", `"thinking"`) || !r.px.has("sendRichMessage", "footer") {
		t.Fatal("no draft with a thinking block or no final rich message")
	}
	// Close the process; the next message must resume the same conversation.
	r.mgr.Close(strconv.Itoa(tp.ThreadID))
	r.b.HandleUpdate(ctx, r.msg(tp.ThreadID, "Reply with only the word I asked you to remember, in capitals."))
	waitUntil(t, 3*time.Minute, "the resumed answer", func() bool { return r.px.has("sendRichMessage", "PAPAYA") })
	tp2, _ := r.st.Topic(tp.ThreadID)
	if tp2.ClaudeSessionID != tp.ClaudeSessionID {
		t.Fatalf("session changed on resume: %s -> %s", tp.ClaudeSessionID, tp2.ClaudeSessionID)
	}
	r.b.HandleUpdate(ctx, r.msg(tp.ThreadID, "/exit"))
	waitUntil(t, 30*time.Second, "close", func() bool { tp3, _ := r.st.Topic(tp.ThreadID); return tp3.Closed })
	if f := r.px.failures(); len(f) > 0 {
		t.Fatalf("rejected API calls: %+v", f)
	}
}

func orcaJSON(t *testing.T, bin string, args ...string) map[string]any {
	t.Helper()
	out, err := exec.Command(bin, append(args, "--json")...).Output()
	if err != nil {
		t.Fatalf("orca %v: %v %s", args, err, out)
	}
	var m map[string]any
	json.Unmarshal(out, &m)
	return m
}

func TestHandoffRealOrca(t *testing.T) {
	r := newRig(t)
	if _, err := os.Stat(r.cfg.OrcaCLI); err != nil {
		t.Skip("Orca not installed (optional)")
	}
	ctx := context.Background()
	root, _ := filepath.Abs("..")
	dir := filepath.Dir(r.sock)
	hooks := map[string]any{}
	for ev, to := range map[string]int{"Stop": 30, "Notification": 30, "PermissionRequest": 660} {
		hooks[ev] = []any{map[string]any{"matcher": "*", "hooks": []any{map[string]any{"type": "command", "command": r.bin + " hook " + ev, "timeout": to}}}}
	}
	sj, _ := json.Marshal(map[string]any{"hooks": hooks})
	os.WriteFile(filepath.Join(dir, "hooks.json"), sj, 0o600)
	mj, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"tgbridge": map[string]any{"type": "stdio", "command": r.bin, "args": []string{"mcp"}, "env": map[string]string{"TGBRIDGE_SOCKET": r.sock}}}})
	os.WriteFile(filepath.Join(dir, "mcp.json"), mj, 0o600)
	cmd := fmt.Sprintf("TGBRIDGE_SOCKET=%s claude --model haiku --permission-mode default --settings %s --mcp-config %s --allowedTools mcp__tgbridge__telegram_handoff",
		r.sock, filepath.Join(dir, "hooks.json"), filepath.Join(dir, "mcp.json"))
	res := orcaJSON(t, r.cfg.OrcaCLI, "terminal", "create", "--worktree", "path:"+root, "--title", "tgbridge-e2e", "--command", cmd)
	term, _ := res["result"].(map[string]any)["terminal"].(map[string]any)
	handle, _ := term["handle"].(string)
	if handle == "" {
		b, _ := json.Marshal(res)
		t.Fatalf("no handle: %s", b)
	}
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command(r.cfg.OrcaCLI, "terminal", "read", "--terminal", handle, "--screen").Output()
			t.Logf("terminal screen:\n%s", out)
			r.px.mu.Lock()
			for _, c := range r.px.calls {
				if c.Method == "sendRichMessage" || c.Method == "sendMessage" {
					t.Logf("sent: %.300s", c.Req)
				}
			}
			r.px.mu.Unlock()
		}
		exec.Command(r.cfg.OrcaCLI, "terminal", "close", "--terminal", handle, "--json").Run()
	})
	oc := orca.New(r.cfg.OrcaCLI)
	time.Sleep(5 * time.Second)
	if ok, _ := oc.WaitIdle(ctx, handle, 60*time.Second); !ok {
		t.Log("tui-idle not reported; continuing")
	}
	if err := oc.Send(ctx, handle, "Call the telegram_handoff tool with state on. Then reply OK."); err != nil {
		t.Fatal(err)
	}
	var thread int
	waitUntil(t, 3*time.Minute, "a handoff topic", func() bool {
		for _, tp := range r.st.Topics() {
			if tp.Kind == store.KindHandoff && !tp.Closed {
				thread = tp.ThreadID
				return true
			}
		}
		return false
	})
	waitUntil(t, 2*time.Minute, "turn done after handoff", func() bool { return r.px.has("sendRichMessage", "turn done") })

	marker := filepath.Join(dir, "handoff-marker")
	r.b.HandleUpdate(ctx, r.msg(thread, "Use the Bash tool to run exactly: touch "+marker))
	r.approveNext(t)
	waitUntil(t, 3*time.Minute, "the handoff marker file", func() bool { _, err := os.Stat(marker); return err == nil })

	waitUntil(t, 2*time.Minute, "idle before next input", func() bool { ok, _ := oc.WaitIdle(ctx, handle, 5*time.Second); return ok })
	r.b.HandleUpdate(ctx, r.msg(thread, "Reply with exactly: PONG-E2E"))
	waitUntil(t, 3*time.Minute, "PONG mirrored to Telegram", func() bool { return r.px.has("sendRichMessage", "PONG-E2E") })

	r.b.HandleUpdate(ctx, r.msg(thread, "/handoff off"))
	waitUntil(t, 30*time.Second, "handoff off", func() bool { tp, _ := r.st.Topic(thread); return tp.Closed })
	if f := r.px.failures(); len(f) > 0 {
		t.Fatalf("rejected API calls: %+v", f)
	}
}
