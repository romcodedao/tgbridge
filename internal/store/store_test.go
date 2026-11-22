package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tp := Topic{ThreadID: 7, Kind: KindEngine, ClaudeSessionID: "c1", Cwd: "/w", LastActive: time.Unix(100, 0).UTC(), ContextTokens: 5}
	if err := s.PutTopic(tp); err != nil {
		t.Fatal(err)
	}
	s.PutTopic(Topic{ThreadID: 8, Kind: KindHandoff, ClaudeSessionID: "h1", TerminalHandle: "term", Offset: 42})
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Topic(7)
	if !ok || got != tp {
		t.Fatalf("got %+v", got)
	}
	if h, ok := s2.TopicBySession("h1"); !ok || h.Offset != 42 {
		t.Fatalf("by session %+v", h)
	}
	if len(s2.Topics()) != 2 {
		t.Fatal("topics")
	}
	st, _ := os.Stat(filepath.Join(dir, "state.json"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
}

func TestTakeApprovalOnce(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.PutApproval(Approval{Nonce: "n1", ThreadID: 3, MsgID: 9})
	if a, ok := s.TakeApproval("n1"); !ok || a.MsgID != 9 {
		t.Fatal("first take")
	}
	if _, ok := s.TakeApproval("n1"); ok {
		t.Fatal("second take succeeded")
	}
}

func TestAuditAppends(t *testing.T) {
	dir := t.TempDir()
	a := Audit(dir)
	a.Log("cmd", map[string]any{"text": "/new"})
	a.Log("approval", map[string]any{"decision": "allow"})
	b, _ := os.ReadFile(filepath.Join(dir, "audit.log"))
	if strings.Count(string(b), "\n") != 2 || !strings.Contains(string(b), `"kind":"approval"`) {
		t.Fatalf("%s", b)
	}
}
