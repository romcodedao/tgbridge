// Package store persists tgbridge state as one JSON file, plus an append-only audit log.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	KindEngine  = "engine"
	KindHandoff = "handoff"
)

type Topic struct {
	ThreadID        int       `json:"thread_id"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title,omitempty"`
	ClaudeSessionID string    `json:"claude_session_id,omitempty"`
	Cwd             string    `json:"cwd,omitempty"`
	LastActive      time.Time `json:"last_active"`
	Closed          bool      `json:"closed,omitempty"`
	ContextTokens   int       `json:"context_tokens,omitempty"`
	SoftWarned      bool      `json:"soft_warned,omitempty"`
	StatusMsgID     int       `json:"status_msg_id,omitempty"`
	TerminalHandle  string    `json:"terminal_handle,omitempty"`
	TranscriptPath  string    `json:"transcript_path,omitempty"`
	Offset          int64     `json:"offset,omitempty"`
	CarryOver       string    `json:"carry_over,omitempty"`
	Mode            string    `json:"mode,omitempty"` // ask | auto | full; empty = config default
}

type Approval struct {
	Nonce    string    `json:"nonce"`
	ThreadID int       `json:"thread_id"`
	MsgID    int       `json:"msg_id"`
	Expires  time.Time `json:"expires"`
}

type state struct {
	Topics    map[int]Topic       `json:"topics"`
	Approvals map[string]Approval `json:"approvals"`
}

type Store struct {
	mu   sync.Mutex
	path string
	st   state
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "state.json"), st: state{Topics: map[int]Topic{}, Approvals: map[string]Approval{}}}
	b, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.st); err != nil {
			return nil, err
		}
	}
	if s.st.Topics == nil {
		s.st.Topics = map[int]Topic{}
	}
	if s.st.Approvals == nil {
		s.st.Approvals = map[string]Approval{}
	}
	return s, nil
}

// save writes atomically. Caller holds mu.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.st, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Topic(id int) (Topic, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.st.Topics[id]
	return t, ok
}

func (s *Store) PutTopic(t Topic) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Topics[t.ThreadID] = t
	return s.save()
}

// UpdateTopic applies fn to an existing topic and saves it.
func (s *Store) UpdateTopic(id int, fn func(*Topic)) (Topic, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.st.Topics[id]
	if !ok {
		return t, false
	}
	fn(&t)
	s.st.Topics[id] = t
	s.save()
	return t, true
}

func (s *Store) TopicBySession(claudeID string) (Topic, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.st.Topics {
		if t.ClaudeSessionID == claudeID && !t.Closed {
			return t, true
		}
	}
	return Topic{}, false
}

func (s *Store) Topics() []Topic {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Topic, 0, len(s.st.Topics))
	for _, t := range s.st.Topics {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ThreadID < out[j].ThreadID })
	return out
}

func (s *Store) PutApproval(a Approval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Approvals[a.Nonce] = a
	return s.save()
}

// TakeApproval removes and returns the approval; a nonce can be taken once.
func (s *Store) TakeApproval(nonce string) (Approval, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.st.Approvals[nonce]
	if ok {
		delete(s.st.Approvals, nonce)
		s.save()
	}
	return a, ok
}

func (s *Store) Approvals() []Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Approval
	for _, a := range s.st.Approvals {
		out = append(out, a)
	}
	return out
}

type AuditLog struct {
	mu   sync.Mutex
	path string
}

func Audit(dir string) *AuditLog {
	os.MkdirAll(dir, 0o700)
	return &AuditLog{path: filepath.Join(dir, "audit.log")}
}

// Log appends one JSON line {time, kind, ...fields}.
func (a *AuditLog) Log(kind string, fields map[string]any) {
	rec := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "kind": kind}
	for k, v := range fields {
		rec[k] = v
	}
	b, _ := json.Marshal(rec)
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}
