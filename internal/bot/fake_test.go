package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/engine"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

const owner = int64(123456789)

type tgCall struct {
	Method string
	Body   map[string]any
}

type fakeTG struct {
	mu      sync.Mutex
	calls   []tgCall
	thread  int
	msgID   int
	fail    map[string]bool
	failSub string
}

func (f *fakeTG) handler(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/file/") {
		f.mu.Lock()
		f.calls = append(f.calls, tgCall{"file", nil})
		f.mu.Unlock()
		io.WriteString(w, "PNGDATA")
		return
	}
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	b, _ := io.ReadAll(r.Body)
	var body map[string]any
	json.Unmarshal(b, &body)
	f.mu.Lock()
	f.calls = append(f.calls, tgCall{method, body})
	fail := f.fail[method] || (f.failSub != "" && method == "sendRichMessage" && strings.Contains(string(b), f.failSub))
	f.thread++
	f.msgID++
	th, id := 100+f.thread, f.msgID
	f.mu.Unlock()
	if fail {
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: rejected"}`)
		return
	}
	switch method {
	case "createForumTopic":
		fmt.Fprintf(w, `{"ok":true,"result":{"message_thread_id":%d,"name":"x"}}`, th)
	case "sendMessage", "sendRichMessage":
		fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d,"chat":{"id":%d,"type":"private"}}}`, id, owner)
	case "getFile":
		io.WriteString(w, `{"ok":true,"result":{"file_id":"F","file_size":7,"file_path":"docs/f.bin"}}`)
	case "getMe":
		io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"testbot","has_topics_enabled":true}}`)
	default:
		io.WriteString(w, `{"ok":true,"result":true}`)
	}
}

func (f *fakeTG) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.Method)
	}
	return out
}

func (f *fakeTG) find(method string) []tgCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tgCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeTG) textContains(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		var buf strings.Builder
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.Encode(c.Body)
		if strings.Contains(buf.String(), s) {
			return true
		}
	}
	return false
}

type harness struct {
	b  *Bot
	tg *fakeTG
	st *store.Store
}

func newHarness(t *testing.T, mut func(*config.Config)) *harness {
	t.Helper()
	f := &fakeTG{fail: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	cfg := config.Defaults()
	cfg.OwnerID = owner
	cfg.StateDir = t.TempDir()
	cfg.DefaultCwd = t.TempDir()
	cfg.DraftInterval = 20 * time.Millisecond
	cfg.Uploads.Dir = t.TempDir()
	fake, _ := filepath.Abs("../engine/testdata/fakeclaude.sh")
	cfg.ClaudeBin = fake
	if mut != nil {
		mut(cfg)
	}
	st, _ := store.Open(cfg.StateDir)
	mgr := engine.NewManager(engine.ManagerConfig{MaxLive: cfg.Engine.MaxLive, IdleTimeout: time.Hour}, engine.Start)
	t.Cleanup(mgr.Shutdown)
	b := New(Deps{Cfg: cfg, TG: telegram.New("T", srv.URL), Mgr: mgr, Store: st, Audit: store.Audit(cfg.StateDir), BotUsername: "testbot", SelfBin: "/bin/true"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go b.Run(ctx)
	return &harness{b: b, tg: f, st: st}
}

func msg(thread int, from int64, text string) telegram.Update {
	return telegram.Update{UpdateID: 1, Message: &telegram.Message{MessageID: 1, MessageThreadID: thread, IsTopicMessage: thread != 0,
		From: &telegram.User{ID: from}, Chat: telegram.Chat{ID: from, Type: "private"}, Text: text}}
}

func photoMsg(thread int, caption string) telegram.Update {
	u := msg(thread, owner, "")
	u.Message.Caption = caption
	u.Message.Photo = []telegram.PhotoSize{{FileID: "S", FileSize: 10}, {FileID: "F", FileSize: 7}}
	return u
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

// failIf makes the fake reject calls whose body contains substr.
func (f *fakeTG) failIf(substr string) {
	f.mu.Lock()
	f.failSub = substr
	f.mu.Unlock()
}
