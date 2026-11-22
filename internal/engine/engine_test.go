package engine

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/streamjson"
)

func fakeSpec(key string) Spec {
	p, _ := filepath.Abs("testdata/fakeclaude.sh")
	return Spec{Key: key, Binary: p, Cwd: ".", Env: []string{"PATH=/usr/bin:/bin"}}
}

func collect(t *testing.T, ch <-chan streamjson.Event, until func(streamjson.Event) bool) []streamjson.Event {
	t.Helper()
	var evs []streamjson.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return evs
			}
			evs = append(evs, e)
			if until(e) {
				return evs
			}
		case <-timeout:
			t.Fatalf("timeout; got %v", evs)
		}
	}
}

func isResult(e streamjson.Event) bool { _, ok := e.(streamjson.Result); return ok }

func TestSessionTwoTurns(t *testing.T) {
	s, err := Start(context.Background(), fakeSpec("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Send("one")
	collect(t, s.Events(), isResult)
	if s.Busy() {
		t.Fatal("busy after result")
	}
	s.Send("two")
	evs := collect(t, s.Events(), isResult)
	if r := evs[len(evs)-1].(streamjson.Result); r.Text != "echo:two" {
		t.Fatalf("second result=%+v", r)
	}
	if s.SessionID() == "" {
		t.Fatal("no session id")
	}
}

func TestSessionCrashMidTurnEmitsCrashed(t *testing.T) {
	s, _ := Start(context.Background(), fakeSpec("a"))
	s.Send("CRASH")
	evs := collect(t, s.Events(), func(streamjson.Event) bool { return false })
	if _, ok := evs[len(evs)-1].(streamjson.Crashed); !ok {
		t.Fatalf("last=%T %v", evs[len(evs)-1], evs)
	}
}

func TestSessionCloseIdleNoCrash(t *testing.T) {
	s, _ := Start(context.Background(), fakeSpec("a"))
	s.Close()
	for e := range s.Events() {
		if _, ok := e.(streamjson.Crashed); ok {
			t.Fatal("idle close reported as crash")
		}
	}
}

type sinkRec struct {
	mu  sync.Mutex
	evs map[string][]streamjson.Event
}

func (r *sinkRec) sink(key string) func(streamjson.Event) {
	return func(e streamjson.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.evs[key] = append(r.evs[key], e)
	}
}

func (r *sinkRec) results(key string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.evs[key] {
		if res, ok := e.(streamjson.Result); ok {
			out = append(out, res.Text)
		}
	}
	return out
}

func (r *sinkRec) has(key string, want streamjson.Event) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.evs[key] {
		if e == want {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func newMgr(maxLive int, idle time.Duration) *Manager {
	return NewManager(ManagerConfig{MaxLive: maxLive, IdleTimeout: idle, ReapEvery: 20 * time.Millisecond}, Start)
}

func TestManagerEvictsLRUIdle(t *testing.T) {
	m := newMgr(1, time.Hour)
	defer m.Shutdown()
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	m.Submit(context.Background(), "a", func() Spec { return fakeSpec("a") }, "x", r.sink("a"))
	waitFor(t, func() bool { return len(r.results("a")) == 1 })
	m.Submit(context.Background(), "b", func() Spec { return fakeSpec("b") }, "y", r.sink("b"))
	waitFor(t, func() bool { return len(r.results("b")) == 1 })
	if m.Live("a") || !m.Live("b") {
		t.Fatalf("live a=%v b=%v", m.Live("a"), m.Live("b"))
	}
}

func TestManagerQueuesWhileBusy(t *testing.T) {
	m := newMgr(2, time.Hour)
	defer m.Shutdown()
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	spec := func() Spec { return fakeSpec("a") }
	m.Submit(context.Background(), "a", spec, "SLOW1", r.sink("a"))
	m.Submit(context.Background(), "a", spec, "second", r.sink("a"))
	waitFor(t, func() bool { return len(r.results("a")) == 2 })
	if got := r.results("a"); !slices.Equal(got, []string{"echo:SLOW1", "echo:second"}) {
		t.Fatalf("order=%v", got)
	}
}

func TestManagerAllBusyQueues(t *testing.T) {
	m := newMgr(1, time.Hour)
	defer m.Shutdown()
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	m.Submit(context.Background(), "a", func() Spec { return fakeSpec("a") }, "SLOW", r.sink("a"))
	waitFor(t, func() bool { return m.Busy("a") })
	m.Submit(context.Background(), "b", func() Spec { return fakeSpec("b") }, "later", r.sink("b"))
	if !r.has("b", streamjson.Queued{}) {
		t.Fatal("b not told it is queued")
	}
	waitFor(t, func() bool { return len(r.results("b")) == 1 })
	if len(r.results("a")) != 1 {
		t.Fatal("a did not finish")
	}
}

func TestManagerIdleReaper(t *testing.T) {
	m := newMgr(2, 50*time.Millisecond)
	defer m.Shutdown()
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	m.Submit(context.Background(), "a", func() Spec { return fakeSpec("a") }, "x", r.sink("a"))
	waitFor(t, func() bool { return len(r.results("a")) == 1 })
	waitFor(t, func() bool { return !m.Live("a") })
}

func TestManagerStopInterrupts(t *testing.T) {
	m := newMgr(2, time.Hour)
	defer m.Shutdown()
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	m.Submit(context.Background(), "a", func() Spec { return fakeSpec("a") }, "SLOW", r.sink("a"))
	m.Submit(context.Background(), "a", func() Spec { return fakeSpec("a") }, "queued", r.sink("a"))
	waitFor(t, func() bool { return m.Busy("a") })
	m.Stop("a")
	waitFor(t, func() bool { return !m.Busy("a") })
	time.Sleep(1200 * time.Millisecond)
	for _, s := range r.results("a") {
		if s == "echo:queued" {
			t.Fatal("queued message sent after stop")
		}
	}
}

func TestBuildArgsAndCleanEnv(t *testing.T) {
	c := config.Defaults()
	c.Model = "haiku"
	args := strings.Join(BuildArgs(c, "/s.json", "sid-1", "SYS"), " ")
	for _, want := range []string{"-p", "--input-format stream-json", "--output-format stream-json", "--include-partial-messages", "--verbose",
		"--thinking-display summarized", "--permission-prompt-tool mcp__tgbridge__approve", "--settings /s.json",
		"--disallowedTools mcp__tgbridge__telegram_handoff", "--append-system-prompt SYS", "--model haiku", "--resume sid-1"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q: %s", want, args)
		}
	}
	env := CleanEnv([]string{"PATH=/bin", "ORCA_TERMINAL_HANDLE=x", "CLAUDE_CODE_SESSION_ID=y", "CLAUDECODE=1", "TGBRIDGE_TOKEN=secret", "HOME=/h"})
	if !slices.Equal(env, []string{"PATH=/bin", "HOME=/h"}) {
		t.Fatalf("env=%v", env)
	}
}

func TestManagerConcurrentSubmitsStartOnce(t *testing.T) {
	var mu sync.Mutex
	starts := 0
	slowStart := func(ctx context.Context, sp Spec) (*Session, error) {
		mu.Lock()
		starts++
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		return Start(ctx, sp)
	}
	m := NewManager(ManagerConfig{MaxLive: 3, IdleTimeout: time.Hour}, slowStart)
	defer m.Shutdown()
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	spec := func() Spec { return fakeSpec("a") }
	go m.Submit(context.Background(), "a", spec, "one", r.sink("a"))
	time.Sleep(20 * time.Millisecond)
	m.Submit(context.Background(), "a", spec, "two", r.sink("a"))
	waitFor(t, func() bool { return len(r.results("a")) == 2 })
	mu.Lock()
	defer mu.Unlock()
	if starts != 1 {
		t.Fatalf("starts=%d", starts)
	}
}

func TestCloseDuringTurnIsNotACrash(t *testing.T) {
	s, _ := Start(context.Background(), fakeSpec("a"))
	s.Send("SLOW")
	time.Sleep(100 * time.Millisecond)
	go s.Close()
	for e := range s.Events() {
		if _, ok := e.(streamjson.Crashed); ok {
			t.Fatal("deliberate close reported as crash")
		}
	}
}

func TestManagerCloseSilencesSink(t *testing.T) {
	m := newMgr(2, time.Hour)
	defer m.Shutdown()
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	m.Submit(context.Background(), "a", func() Spec { return fakeSpec("a") }, "SLOW", r.sink("a"))
	waitFor(t, func() bool { return m.Busy("a") })
	m.Close("a")
	time.Sleep(1200 * time.Millisecond)
	if len(r.results("a")) != 0 || r.has("a", streamjson.Crashed{}) {
		t.Fatal("events delivered after Close")
	}
}

func TestResetReturnsQueued(t *testing.T) {
	m := newMgr(2, time.Hour)
	defer m.Shutdown()
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	spec := func() Spec { return fakeSpec("a") }
	m.Submit(context.Background(), "a", spec, "SLOW", r.sink("a"))
	m.Submit(context.Background(), "a", spec, "keep-me", r.sink("a"))
	waitFor(t, func() bool { return m.Busy("a") })
	if q := m.Reset("a"); !slices.Equal(q, []string{"keep-me"}) {
		t.Fatalf("queued=%v", q)
	}
	if m.Live("a") {
		t.Fatal("still live after reset")
	}
}

func TestShutdownDoesNotStartWaiters(t *testing.T) {
	var mu sync.Mutex
	starts := 0
	counting := func(ctx context.Context, sp Spec) (*Session, error) {
		mu.Lock()
		starts++
		mu.Unlock()
		return Start(ctx, sp)
	}
	m := NewManager(ManagerConfig{MaxLive: 1, IdleTimeout: time.Hour}, counting)
	r := &sinkRec{evs: map[string][]streamjson.Event{}}
	m.Submit(context.Background(), "a", func() Spec { return fakeSpec("a") }, "SLOW", r.sink("a"))
	waitFor(t, func() bool { return m.Busy("a") })
	m.Submit(context.Background(), "b", func() Spec { return fakeSpec("b") }, "x", r.sink("b"))
	m.Shutdown()
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if starts != 1 {
		t.Fatalf("starts=%d (a waiter was started during shutdown)", starts)
	}
}
