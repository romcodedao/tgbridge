package engine

import (
	"context"
	"sync"
	"time"

	"github.com/romcodedao/tgbridge/internal/streamjson"
)

type ManagerConfig struct {
	MaxLive     int
	IdleTimeout time.Duration
	ReapEvery   time.Duration
}

type StartFunc func(context.Context, Spec) (*Session, error)

type entry struct {
	key      string
	sess     *Session
	spec     func() Spec
	sink     func(streamjson.Event)
	queue    []string
	lastUsed time.Time
	waiting  bool
	starting bool
}

// Manager keeps at most MaxLive processes; conversations (keys) are unlimited.
type Manager struct {
	cfg     ManagerConfig
	start   StartFunc
	mu      sync.Mutex
	entries map[string]*entry
	waiters []string
	stop    chan struct{}
	pumps   sync.WaitGroup
}

func NewManager(cfg ManagerConfig, start StartFunc) *Manager {
	if cfg.ReapEvery == 0 {
		cfg.ReapEvery = 30 * time.Second
	}
	m := &Manager{cfg: cfg, start: start, entries: map[string]*entry{}, stop: make(chan struct{})}
	go m.reap()
	return m
}

func (m *Manager) live() int {
	n := 0
	for _, e := range m.entries {
		if e.sess != nil {
			n++
		}
	}
	return n
}

// Submit sends text to key's session, starting (or resuming) it when needed. sink receives every event.
func (m *Manager) Submit(ctx context.Context, key string, spec func() Spec, text string, sink func(streamjson.Event)) {
	if m.stopping() {
		return
	}
	m.mu.Lock()
	e := m.entries[key]
	if e == nil {
		e = &entry{key: key}
		m.entries[key] = e
	}
	e.spec, e.sink, e.lastUsed = spec, sink, time.Now()
	if e.sess != nil {
		if e.sess.Busy() || len(e.queue) > 0 {
			e.queue = append(e.queue, text)
		} else if err := e.sess.Send(text); err != nil {
			e.queue = append(e.queue, text)
		}
		m.mu.Unlock()
		return
	}
	e.queue = append(e.queue, text)
	if e.waiting || e.starting {
		m.mu.Unlock()
		return
	}
	if !m.ensureSlot() {
		e.waiting = true
		m.waiters = append(m.waiters, key)
		m.mu.Unlock()
		sink(streamjson.Queued{})
		return
	}
	e.starting = true
	m.mu.Unlock()
	m.launch(ctx, e)
}

// ensureSlot frees a slot by closing the least recently used idle session. Caller holds mu.
func (m *Manager) ensureSlot() bool {
	if m.live() < m.cfg.MaxLive {
		return true
	}
	var lru *entry
	for _, o := range m.entries {
		if o.sess != nil && !o.sess.Busy() && len(o.queue) == 0 && (lru == nil || o.lastUsed.Before(lru.lastUsed)) {
			lru = o
		}
	}
	if lru == nil {
		return false
	}
	s := lru.sess
	lru.sess = nil
	go s.Close()
	return true
}

func (m *Manager) launch(ctx context.Context, e *entry) {
	m.mu.Lock()
	spec := e.spec
	m.mu.Unlock()
	sp := spec()
	s, err := m.start(ctx, sp)
	if err != nil {
		m.mu.Lock()
		e.queue, e.starting = nil, false
		sink := e.sink
		m.mu.Unlock()
		sink(streamjson.Crashed{Err: err.Error()})
		return
	}
	m.mu.Lock()
	e.sess, e.starting = s, false
	m.mu.Unlock()
	m.pumps.Add(1)
	go m.pump(e, s)
	m.next(e, s)
}

// next sends the next queued text if the session is idle.
func (m *Manager) next(e *entry, s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.sess != s || s.Busy() || len(e.queue) == 0 {
		return
	}
	t := e.queue[0]
	e.queue = e.queue[1:]
	e.lastUsed = time.Now()
	s.Send(t)
}

func (m *Manager) pump(e *entry, s *Session) {
	defer m.pumps.Done()
	for ev := range s.Events() {
		m.mu.Lock()
		sink := e.sink
		m.mu.Unlock()
		sink(ev)
		if _, ok := ev.(streamjson.Result); ok {
			m.next(e, s)
			m.wake()
		}
	}
	m.mu.Lock()
	if e.sess == s {
		e.sess = nil
		e.queue = nil // crashed or closed: the next message resumes
	}
	m.mu.Unlock()
	m.wake()
}

// wake starts a waiting key if a slot can be freed.
func (m *Manager) wake() {
	if m.stopping() {
		return
	}
	m.mu.Lock()
	if len(m.waiters) == 0 || !m.ensureSlot() {
		m.mu.Unlock()
		return
	}
	key := m.waiters[0]
	m.waiters = m.waiters[1:]
	e := m.entries[key]
	if e == nil {
		m.mu.Unlock()
		return
	}
	e.waiting, e.starting = false, true
	m.mu.Unlock()
	m.launch(context.Background(), e)
}

func (m *Manager) Live(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[key]
	return e != nil && e.sess != nil
}

func (m *Manager) Busy(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[key]
	return e != nil && e.sess != nil && (e.sess.Busy() || len(e.queue) > 0)
}

// Stop interrupts the running turn and drops queued messages.
func (m *Manager) Stop(key string) {
	m.mu.Lock()
	e := m.entries[key]
	if e == nil || e.sess == nil {
		m.mu.Unlock()
		return
	}
	e.queue = nil
	s := e.sess
	m.mu.Unlock()
	s.Interrupt()
}

// Close ends key's process and forgets it (queued texts are dropped; see Reset).
func (m *Manager) Close(key string) { m.Reset(key) }

// Reset ends key's process, forgets it, and returns the texts still queued for it.
// No further events from the old process reach the sink.
func (m *Manager) Reset(key string) []string {
	m.mu.Lock()
	e := m.entries[key]
	delete(m.entries, key)
	var queued []string
	if e != nil {
		queued = e.queue
		e.queue = nil
		e.sink = func(streamjson.Event) {}
	}
	for i, w := range m.waiters {
		if w == key {
			m.waiters = append(m.waiters[:i], m.waiters[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	if e != nil && e.sess != nil {
		e.sess.Close()
	}
	return queued
}

func (m *Manager) stopping() bool {
	select {
	case <-m.stop:
		return true
	default:
		return false
	}
}

func (m *Manager) reap() {
	t := time.NewTicker(m.cfg.ReapEvery)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
		m.mu.Lock()
		var idle []*Session
		for _, e := range m.entries {
			if e.sess != nil && !e.sess.Busy() && len(e.queue) == 0 && time.Since(e.lastUsed) > m.cfg.IdleTimeout {
				idle = append(idle, e.sess)
				e.sess = nil
			}
		}
		m.mu.Unlock()
		for _, s := range idle {
			s.Close()
		}
	}
}

// Shutdown closes every process.
func (m *Manager) Shutdown() {
	close(m.stop)
	m.mu.Lock()
	var all []*Session
	for _, e := range m.entries {
		if e.sess != nil {
			all = append(all, e.sess)
			e.sess = nil
		}
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func() { defer wg.Done(); s.Close() }()
	}
	wg.Wait()
	m.pumps.Wait() // every event, including final ones, has reached its sink
}
