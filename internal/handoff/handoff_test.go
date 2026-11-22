package handoff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/romcodedao/tgbridge/internal/ipc"
	"github.com/romcodedao/tgbridge/internal/orca"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

type fakeTG struct {
	mu     sync.Mutex
	calls  []string
	reject string // sendRichMessage bodies containing this get a 400
}

func (f *fakeTG) serve(t *testing.T) *telegram.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		f.calls = append(f.calls, method+" "+string(b))
		n := len(f.calls)
		bad := f.reject != "" && method == "sendRichMessage" && strings.Contains(string(b), f.reject)
		f.mu.Unlock()
		if bad {
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: rejected"}`)
			return
		}
		switch method {
		case "createForumTopic":
			fmt.Fprintf(w, `{"ok":true,"result":{"message_thread_id":%d}}`, 300+n)
		case "sendMessage", "sendRichMessage":
			fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d,"chat":{"id":1,"type":"private"}}}`, n)
		default:
			io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	t.Cleanup(srv.Close)
	return telegram.New("T", srv.URL)
}

func (f *fakeTG) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

type fakeOrca struct {
	mu     sync.Mutex
	sent   []string
	idle   bool
	closed []string
	agents []orca.Agent
	terms  []orca.Terminal
}

func (o *fakeOrca) List(context.Context) ([]orca.Agent, error)         { return o.agents, nil }
func (o *fakeOrca) Terminals(context.Context) ([]orca.Terminal, error) { return o.terms, nil }
func (o *fakeOrca) Send(_ context.Context, h, text string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sent = append(o.sent, h+":"+text)
	return nil
}
func (o *fakeOrca) WaitIdle(context.Context, string, time.Duration) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.idle, nil
}
func (o *fakeOrca) CloseTerminal(_ context.Context, h string) error {
	o.closed = append(o.closed, h)
	return nil
}
func (o *fakeOrca) sentList() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string{}, o.sent...)
}

type env struct {
	m     *Manager
	tg    *fakeTG
	or    *fakeOrca
	st    *store.Store
	home  string
	ask   chan string
	trans string
}

const sid = "11111111-2222-3333-4444-555555555555"

func setup(t *testing.T, stateDir string) *env {
	t.Helper()
	home := t.TempDir()
	proj := filepath.Join(home, "projects", "-w")
	os.MkdirAll(proj, 0o700)
	trans := filepath.Join(proj, sid+".jsonl")
	os.WriteFile(trans, []byte(`{"type":"user","message":{"role":"user","content":"old prompt"}}`+"\n"), 0o600)
	if stateDir == "" {
		stateDir = t.TempDir()
	}
	st, _ := store.Open(stateDir)
	f := &fakeTG{}
	e := &env{tg: f, or: &fakeOrca{idle: true}, st: st, home: home, ask: make(chan string, 1), trans: trans}
	e.m = New(Deps{TG: f.serve(t), Store: st, UploadsDir: filepath.Join(stateDir, "uploads"), Orca: e.or, Owner: 1, ClaudeHome: home, PermTimeout: 200 * time.Millisecond, Poll: 20 * time.Millisecond, StopSettle: 150 * time.Millisecond,
		Ask: func(ctx context.Context, thread int, title, detail string, timeout time.Duration) string {
			if strings.Contains(detail, strings.Repeat("x", 4000)) {
				e.ask <- "asked-long"
				return "allow"
			}
			select {
			case d := <-e.ask:
				return d
			case <-time.After(timeout):
				return "timeout"
			case <-ctx.Done():
				return "timeout"
			}
		}})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.m.Start(ctx)
	return e
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func appendLine(t *testing.T, path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(line + "\n")
	f.Close()
}

var bg = context.Background()

func handoffOn(t *testing.T, e *env) int {
	r := e.m.Handle(bg, ipc.Request{Op: "handoff", SessionID: sid, TerminalHandle: "term_1", Args: json.RawMessage(`{"state":"on"}`)})
	if r.Error != "" || !strings.Contains(r.Text, "Handed off") {
		t.Fatalf("on: %+v", r)
	}
	tp, ok := e.st.TopicBySession(sid)
	if !ok || tp.Kind != store.KindHandoff {
		t.Fatal("no handoff topic")
	}
	return tp.ThreadID
}

func TestHandoffRefusedWithoutTerminal(t *testing.T) {
	e := setup(t, "")
	r := e.m.Handle(bg, ipc.Request{Op: "handoff", SessionID: sid, Args: json.RawMessage(`{"state":"on"}`)})
	if !strings.Contains(r.Error, "not an Orca terminal session") {
		t.Fatalf("%+v", r)
	}
}

func TestHandoffTailPostsBlocks(t *testing.T) {
	e := setup(t, "")
	handoffOn(t, e)
	appendLine(t, e.trans, `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"pondering deeply"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la"}}]}}`)
	appendLine(t, e.trans, `{"type":"assistant","message":{"content":[{"type":"text","text":"All **done**"}]}}`)
	eventually(t, func() bool { return strings.Contains(e.tg.all(), "All ") })
	all := e.tg.all()
	if strings.Contains(all, "old prompt") {
		t.Fatal("posted history from before the handoff")
	}
	iT, iB, iD := strings.Index(all, "pondering deeply"), strings.Index(all, "ls -la"), strings.Index(all, "All ")
	if iT < 0 || iB < iT || iD < iB {
		t.Fatalf("order wrong: %d %d %d\n%s", iT, iB, iD, all)
	}
}

func TestHandoffResumesFromOffset(t *testing.T) {
	dir := t.TempDir()
	e := setup(t, dir)
	handoffOn(t, e)
	appendLine(t, e.trans, `{"type":"assistant","message":{"content":[{"type":"text","text":"first-block"}]}}`)
	eventually(t, func() bool { return strings.Contains(e.tg.all(), "first-block") })
	eventually(t, func() bool {
		tp, _ := e.st.TopicBySession(sid)
		fi, _ := os.Stat(e.trans)
		return tp.Offset == fi.Size()
	})
	// restart with the same state and transcript
	e2 := setup(t, dir)
	os.Remove(e2.trans)
	os.Rename(e.trans, e2.trans)
	e2.st.UpdateTopic(mustTopic(t, e2.st).ThreadID, func(tp *store.Topic) { tp.TranscriptPath = e2.trans })
	e2.m.Start(context.Background())
	appendLine(t, e2.trans, `{"type":"assistant","message":{"content":[{"type":"text","text":"second-block"}]}}`)
	eventually(t, func() bool { return strings.Contains(e2.tg.all(), "second-block") })
	if strings.Contains(e2.tg.all(), "first-block") {
		t.Fatal("duplicate post after restart")
	}
}

func mustTopic(t *testing.T, st *store.Store) store.Topic {
	tp, ok := st.TopicBySession(sid)
	if !ok {
		t.Fatal("no topic")
	}
	return tp
}

func TestInputSentWhenIdle(t *testing.T) {
	e := setup(t, "")
	th := handoffOn(t, e)
	if note := e.m.Input(bg, th, "do the thing"); note != "" {
		t.Fatalf("note=%q", note)
	}
	if s := e.or.sentList(); len(s) != 1 || s[0] != "term_1:do the thing" {
		t.Fatalf("sent=%v", s)
	}
}

func TestInputQueuedWhilePermissionPending(t *testing.T) {
	e := setup(t, "")
	th := handoffOn(t, e)
	done := make(chan ipc.Response)
	go func() {
		done <- e.m.Handle(bg, ipc.Request{Op: "hook.PermissionRequest", SessionID: sid, TerminalHandle: "term_1",
			Args: json.RawMessage(`{"session_id":"` + sid + `","tool_name":"Bash","tool_input":{"command":"rm x"}}`)})
	}()
	eventually(t, func() bool { return e.m.pending(th) })
	if note := e.m.Input(bg, th, "hello"); !strings.Contains(note, "queued") {
		t.Fatalf("note=%q", note)
	}
	if len(e.or.sentList()) != 0 {
		t.Fatal("typed into a pending permission dialog")
	}
	e.ask <- "allow"
	r := <-done
	if !strings.Contains(string(r.Output), `"behavior":"allow"`) {
		t.Fatalf("out=%s", r.Output)
	}
	e.m.Handle(bg, ipc.Request{Op: "hook.Stop", SessionID: sid, Args: json.RawMessage(`{"session_id":"` + sid + `"}`)})
	eventually(t, func() bool { return len(e.or.sentList()) == 1 })
}

func TestPermissionHookTimeoutReturnsEmpty(t *testing.T) {
	e := setup(t, "")
	handoffOn(t, e)
	r := e.m.Handle(bg, ipc.Request{Op: "hook.PermissionRequest", SessionID: sid, Args: json.RawMessage(`{"session_id":"` + sid + `","tool_name":"Bash","tool_input":{}}`)})
	if len(r.Output) != 0 {
		t.Fatalf("out=%s", r.Output)
	}
}

func TestHooksNoopWhenNotHandedOff(t *testing.T) {
	e := setup(t, "")
	start := time.Now()
	r := e.m.Handle(bg, ipc.Request{Op: "hook.PermissionRequest", SessionID: "other", Args: json.RawMessage(`{"session_id":"other"}`)})
	if len(r.Output) != 0 || time.Since(start) > 100*time.Millisecond || strings.Contains(e.tg.all(), "sendRichMessage") {
		t.Fatalf("not a noop: %+v", r)
	}
}

func TestOffStopsTail(t *testing.T) {
	e := setup(t, "")
	handoffOn(t, e)
	r := e.m.Handle(bg, ipc.Request{Op: "handoff", SessionID: sid, Args: json.RawMessage(`{"state":"off"}`)})
	if !strings.Contains(r.Text, "Stopped") {
		t.Fatalf("%+v", r)
	}
	appendLine(t, e.trans, `{"type":"assistant","message":{"content":[{"type":"text","text":"after-off"}]}}`)
	time.Sleep(150 * time.Millisecond)
	if strings.Contains(e.tg.all(), "after-off") {
		t.Fatal("posted after off")
	}
	if tp, _ := e.st.Topic(mustClosedTopic(t, e.st)); !tp.Closed {
		t.Fatal("topic not closed")
	}
	if all := e.tg.all(); strings.Contains(all, "closeForumTopic") || !strings.Contains(all, "editForumTopic") {
		t.Fatal("must rename the topic, not closeForumTopic (rejected in private chats)")
	}
}

func mustClosedTopic(t *testing.T, st *store.Store) int {
	for _, tp := range st.Topics() {
		return tp.ThreadID
	}
	t.Fatal("no topics")
	return 0
}

func TestOnOrcaSessionResolvesHandle(t *testing.T) {
	e := setup(t, "")
	os.MkdirAll(filepath.Join(e.home, "sessions"), 0o700)
	os.WriteFile(filepath.Join(e.home, "sessions", "42.json"), []byte(`{"pid":42,"sessionId":"`+sid+`","cwd":"/w","kind":"interactive","updatedAt":5}`), 0o600)
	e.or.agents = []orca.Agent{{SessionID: "orca-1", Kind: "terminal", Agent: "claude", PaneKey: "tab1:leaf1", Title: "fix bug"}}
	e.or.terms = []orca.Terminal{{Handle: "term_7", TabID: "tab1", LeafID: "leaf1", WorktreePath: "/w"}}
	th, err := e.m.OnOrcaSession(bg, "orca-1")
	if err != nil {
		t.Fatal(err)
	}
	tp, _ := e.st.Topic(th)
	if tp.TerminalHandle != "term_7" || tp.ClaudeSessionID != sid {
		t.Fatalf("%+v", tp)
	}
}

func TestStopPostsAfterFinalText(t *testing.T) {
	e := setup(t, "")
	handoffOn(t, e)
	e.m.Handle(bg, ipc.Request{Op: "hook.Stop", SessionID: sid, Args: json.RawMessage(`{"session_id":"` + sid + `"}`)})
	// Claude writes the final text line just after the Stop hook fires
	time.Sleep(30 * time.Millisecond)
	appendLine(t, e.trans, `{"type":"assistant","message":{"content":[{"type":"text","text":"final-words"}]}}`)
	eventually(t, func() bool { return strings.Contains(e.tg.all(), "turn done") })
	all := e.tg.all()
	if i, j := strings.Index(all, "final-words"), strings.Index(all, "turn done"); i < 0 || i > j {
		t.Fatalf("turn done before final text:\n%s", all)
	}
}

func TestDrainRejectedMessageFallsBackOnce(t *testing.T) {
	e := setup(t, "")
	handoffOn(t, e)
	e.tg.mu.Lock()
	e.tg.reject = "QQBAD"
	e.tg.mu.Unlock()
	appendLine(t, e.trans, `{"type":"assistant","message":{"content":[{"type":"text","text":"QQBAD block"}]}}`)
	appendLine(t, e.trans, `{"type":"assistant","message":{"content":[{"type":"text","text":"after-bad"}]}}`)
	eventually(t, func() bool { return strings.Contains(e.tg.all(), "after-bad") })
	time.Sleep(150 * time.Millisecond)
	all := e.tg.all()
	if n := strings.Count(all, "sendRichMessage"+" "); n > 0 && strings.Count(all, "QQBAD block") > 2 {
		t.Fatalf("rejected message re-posted in a loop:\n%s", all)
	}
	if !strings.Contains(all, "sendMessage {") || strings.Count(all, "after-bad") != 1 {
		t.Fatalf("no plain-text fallback or duplicate posts:\n%s", all)
	}
}

func TestPermissionCancelledClearsPending(t *testing.T) {
	e := setup(t, "")
	th := handoffOn(t, e)
	e.m.d.PermTimeout = time.Minute
	ctx, cancel := context.WithCancel(bg)
	done := make(chan struct{})
	go func() {
		e.m.Handle(ctx, ipc.Request{Op: "hook.PermissionRequest", SessionID: sid, Args: json.RawMessage(`{"session_id":"` + sid + `","tool_name":"Bash","tool_input":{}}`)})
		close(done)
	}()
	eventually(t, func() bool { return e.m.pending(th) })
	cancel() // the hook process went away (Esc in the terminal)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("permission wait ignored the cancelled hook")
	}
	if e.m.pending(th) {
		t.Fatal("pending stuck after the hook went away")
	}
}

func TestOverlongHandoffPermissionGoesToTerminal(t *testing.T) {
	e := setup(t, "")
	handoffOn(t, e)
	long, _ := json.Marshal(map[string]any{"session_id": sid, "tool_name": "Bash", "tool_input": map[string]any{"command": strings.Repeat("x", 4000)}})
	r := e.m.Handle(bg, ipc.Request{Op: "hook.PermissionRequest", SessionID: sid, Args: long})
	if len(r.Output) != 0 {
		t.Fatalf("decided an unreviewable request: %s", r.Output)
	}
	select {
	case <-e.ask:
		t.Fatal("asked the owner to approve a truncated request")
	default:
	}
	if !strings.Contains(e.tg.all(), "too long") {
		t.Fatal("owner not told why")
	}
}

func TestOffRemovesUploads(t *testing.T) {
	e := setup(t, "")
	thread := handoffOn(t, e)
	dir := filepath.Join(e.m.d.UploadsDir, strconv.Itoa(thread))
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "1-photo.jpg"), []byte("x"), 0o600)
	e.m.Handle(bg, ipc.Request{Op: "handoff", SessionID: sid, Args: json.RawMessage(`{"state":"off"}`)})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("uploads left after handoff off")
	}
}
