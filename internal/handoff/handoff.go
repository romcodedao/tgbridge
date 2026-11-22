// Package handoff mirrors a running Orca terminal Claude session into a Telegram topic and back.
package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/romcodedao/tgbridge/internal/ipc"
	"github.com/romcodedao/tgbridge/internal/orca"
	"github.com/romcodedao/tgbridge/internal/render"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

type Orca interface {
	List(ctx context.Context) ([]orca.Agent, error)
	Terminals(ctx context.Context) ([]orca.Terminal, error)
	Send(ctx context.Context, handle, text string) error
	WaitIdle(ctx context.Context, handle string, timeout time.Duration) (bool, error)
	CloseTerminal(ctx context.Context, handle string) error
}

// AskFunc posts a permission prompt and returns "allow", "session", "deny" or "timeout".
type AskFunc func(ctx context.Context, thread int, title, detail string, timeout time.Duration) string

type Deps struct {
	TG          *telegram.Client
	Store       *store.Store
	Orca        Orca
	Ask         AskFunc
	Owner       int64
	ClaudeHome  string // ~/.claude
	PermTimeout time.Duration
	Poll        time.Duration
	BotUser     string
	StopSettle  time.Duration // wait after the Stop hook for Claude to flush the final transcript lines
}

type live struct {
	cancel  context.CancelFunc
	queue   []string
	pending bool
}

type Manager struct {
	d     Deps
	mu    sync.Mutex
	ctx   context.Context
	on    map[int]*live // thread -> live handoff
	locks map[int]*sync.Mutex
}

func New(d Deps) *Manager {
	if d.Poll == 0 {
		d.Poll = time.Second
	}
	if d.StopSettle == 0 {
		d.StopSettle = 1500 * time.Millisecond
	}
	return &Manager{d: d, on: map[int]*live{}, ctx: context.Background()}
}

// Start resumes tails for handoffs saved in the store.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	for _, tp := range m.d.Store.Topics() {
		if tp.Kind == store.KindHandoff && !tp.Closed {
			m.startTail(tp.ThreadID)
		}
	}
}

func (m *Manager) startTail(thread int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l := m.on[thread]; l != nil {
		l.cancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.on[thread] = &live{cancel: cancel}
	go m.tail(ctx, thread)
}

func (m *Manager) findTranscript(sessionID string) string {
	matches, _ := filepath.Glob(filepath.Join(m.d.ClaudeHome, "projects", "*", sessionID+".jsonl"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// On starts a handoff for a Claude session running in an Orca terminal.
func (m *Manager) On(ctx context.Context, sessionID, handle, title string) (int, error) {
	if handle == "" {
		return 0, errors.New("not an Orca terminal session (no ORCA_TERMINAL_HANDLE); handoff works only for sessions running inside Orca")
	}
	if sessionID == "" {
		return 0, errors.New("unknown Claude session id")
	}
	if tp, ok := m.d.Store.TopicBySession(sessionID); ok && tp.Kind == store.KindHandoff {
		return tp.ThreadID, nil
	}
	path := m.findTranscript(sessionID)
	var off int64
	if fi, err := os.Stat(path); err == nil {
		off = fi.Size()
	}
	if title == "" {
		title = "Orca session " + sessionID[:min(8, len(sessionID))]
	}
	thread, err := m.d.TG.CreateForumTopic(ctx, m.d.Owner, "📲 "+truncate(title, 60))
	if err != nil {
		return 0, err
	}
	m.d.Store.PutTopic(store.Topic{ThreadID: thread, Kind: store.KindHandoff, Title: title, ClaudeSessionID: sessionID,
		TerminalHandle: handle, TranscriptPath: path, Offset: off, LastActive: time.Now()})
	m.post(ctx, thread, telegram.Paragraph("📲 Handed off. Claude's output from the Orca terminal appears here; what you type here goes to that terminal. Say \"stop notifying Telegram\" there, or send /handoff off here, to end."))
	m.startTail(thread)
	return thread, nil
}

// OnOrcaSession hands off an Orca agent picked from /sessions.
func (m *Manager) OnOrcaSession(ctx context.Context, orcaID string) (int, error) {
	agents, err := m.d.Orca.List(ctx)
	if err != nil {
		return 0, err
	}
	var ag *orca.Agent
	for i := range agents {
		if agents[i].SessionID == orcaID {
			ag = &agents[i]
		}
	}
	if ag == nil || ag.Kind != "terminal" {
		return 0, errors.New("that Orca agent is not a running terminal session")
	}
	terms, err := m.d.Orca.Terminals(ctx)
	if err != nil {
		return 0, err
	}
	var term *orca.Terminal
	for i := range terms {
		if terms[i].TabID+":"+terms[i].LeafID == ag.PaneKey {
			term = &terms[i]
		}
	}
	if term == nil {
		return 0, errors.New("could not find that agent's terminal")
	}
	sid := m.claudeSessionFor(term.WorktreePath)
	if sid == "" {
		return 0, errors.New("could not find the Claude session in that terminal; say \"notify me on Telegram\" inside it instead")
	}
	return m.On(ctx, sid, term.Handle, ag.Title)
}

// claudeSessionFor picks the most recently updated interactive Claude session in cwd (~/.claude/sessions/*.json).
func (m *Manager) claudeSessionFor(cwd string) string {
	files, _ := filepath.Glob(filepath.Join(m.d.ClaudeHome, "sessions", "*.json"))
	best, bestAt := "", int64(-1)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
			Kind      string `json:"kind"`
			UpdatedAt int64  `json:"updatedAt"`
		}
		if json.Unmarshal(b, &s) != nil || s.Cwd != cwd || s.Kind != "interactive" {
			continue
		}
		if s.UpdatedAt > bestAt {
			best, bestAt = s.SessionID, s.UpdatedAt
		}
	}
	return best
}

// Off ends the handoff of thread; closeTerminal also closes the Orca terminal.
func (m *Manager) Off(ctx context.Context, thread int, closeTerminal bool) error {
	m.mu.Lock()
	if l := m.on[thread]; l != nil {
		l.cancel()
		delete(m.on, thread)
	}
	m.mu.Unlock()
	tp, ok := m.d.Store.UpdateTopic(thread, func(t *store.Topic) { t.Closed = true })
	if !ok {
		return errors.New("no such handoff")
	}
	msg := "✅ Handoff stopped. The session keeps running in Orca."
	if closeTerminal && tp.TerminalHandle != "" {
		if err := m.d.Orca.CloseTerminal(ctx, tp.TerminalHandle); err != nil {
			msg = "Handoff stopped, but closing the terminal failed: " + err.Error()
		} else {
			msg = "✅ Handoff stopped and the Orca terminal closed."
		}
	}
	m.post(ctx, thread, telegram.Paragraph(msg))
	m.d.TG.MarkTopicClosed(ctx, m.d.Owner, thread, tp.Title)
	return nil
}

func (m *Manager) pending(thread int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.on[thread]
	return l != nil && l.pending
}

// Input types text into the handed-off terminal when it is safe, else queues it until the turn ends.
func (m *Manager) Input(ctx context.Context, thread int, text string) string {
	tp, ok := m.d.Store.Topic(thread)
	if !ok || tp.Closed {
		return "This handoff has ended."
	}
	m.mu.Lock()
	l := m.on[thread]
	if l == nil {
		m.mu.Unlock()
		return "This handoff is not active."
	}
	if l.pending || len(l.queue) > 0 {
		l.queue = append(l.queue, text)
		m.mu.Unlock()
		return "⏳ queued: Claude is waiting on a permission prompt or busy; it is sent when the turn ends."
	}
	m.mu.Unlock()
	if idle, _ := m.d.Orca.WaitIdle(ctx, tp.TerminalHandle, 2*time.Second); !idle {
		m.mu.Lock()
		l.queue = append(l.queue, text)
		m.mu.Unlock()
		return "⏳ queued: Claude is working; it is sent when this turn ends."
	}
	if err := m.d.Orca.Send(ctx, tp.TerminalHandle, text); err != nil {
		return "Could not type into the terminal: " + err.Error()
	}
	return ""
}

func (m *Manager) flushQueue(ctx context.Context, thread int) {
	tp, _ := m.d.Store.Topic(thread)
	m.mu.Lock()
	l := m.on[thread]
	if l == nil || len(l.queue) == 0 || l.pending {
		m.mu.Unlock()
		return
	}
	text := strings.Join(l.queue, "\n\n")
	l.queue = nil
	m.mu.Unlock()
	go func() {
		time.Sleep(500 * time.Millisecond) // let the TUI return to its prompt after Stop
		if err := m.d.Orca.Send(ctx, tp.TerminalHandle, text); err != nil {
			m.post(ctx, thread, telegram.Paragraph("Could not type the queued message: "+err.Error()))
		}
	}()
}

// Handle serves the handoff/status MCP ops and hook events.
func (m *Manager) Handle(ctx context.Context, r ipc.Request) ipc.Response {
	switch r.Op {
	case "handoff":
		var a struct {
			State string `json:"state"`
		}
		json.Unmarshal(r.Args, &a)
		if a.State == "off" {
			tp, ok := m.d.Store.TopicBySession(r.SessionID)
			if !ok || tp.Kind != store.KindHandoff {
				return ipc.Response{Text: "This session was not handed off."}
			}
			m.Off(ctx, tp.ThreadID, false)
			return ipc.Response{Text: "Stopped sending this session to Telegram."}
		}
		thread, err := m.On(ctx, r.SessionID, r.TerminalHandle, "")
		if err != nil {
			return ipc.Response{Error: err.Error()}
		}
		return ipc.Response{Text: fmt.Sprintf("Handed off to Telegram (topic %s). Keep working; output and permission prompts go to the owner's phone.", m.link(thread))}
	case "status":
		if tp, ok := m.d.Store.TopicBySession(r.SessionID); ok && tp.Kind == store.KindHandoff {
			return ipc.Response{Text: "Handed off to Telegram: " + m.link(tp.ThreadID)}
		}
		return ipc.Response{Text: "Not handed off."}
	}
	tp, ok := m.d.Store.TopicBySession(r.SessionID)
	if !ok || tp.Kind != store.KindHandoff {
		return ipc.Response{} // not handed off: hooks are no-ops
	}
	var p struct {
		Message        string          `json:"message"`
		ToolName       string          `json:"tool_name"`
		ToolInput      json.RawMessage `json:"tool_input"`
		TranscriptPath string          `json:"transcript_path"`
	}
	json.Unmarshal(r.Args, &p)
	if p.TranscriptPath != "" && p.TranscriptPath != tp.TranscriptPath {
		m.d.Store.UpdateTopic(tp.ThreadID, func(t *store.Topic) { t.TranscriptPath = p.TranscriptPath })
	}
	switch r.Op {
	case "hook.Stop":
		go func() { // the hook returns at once; Claude writes the last lines after it
			time.Sleep(m.d.StopSettle)
			bctx := context.WithoutCancel(ctx)
			m.drain(bctx, tp.ThreadID)
			if cur, ok := m.d.Store.Topic(tp.ThreadID); !ok || cur.Closed {
				return
			}
			m.post(bctx, tp.ThreadID, telegram.Paragraph("✅ turn done"))
			m.flushQueue(bctx, tp.ThreadID)
		}()
	case "hook.Notification":
		msg := p.Message
		if msg == "" {
			msg = "Claude needs your attention."
		}
		m.post(ctx, tp.ThreadID, telegram.Paragraph("🔔 "+msg))
	case "hook.PermissionRequest":
		return m.permission(ctx, tp.ThreadID, p.ToolName, p.ToolInput)
	}
	return ipc.Response{}
}

func (m *Manager) permission(ctx context.Context, thread int, tool string, input json.RawMessage) ipc.Response {
	m.mu.Lock()
	if l := m.on[thread]; l != nil {
		l.pending = true
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if l := m.on[thread]; l != nil {
			l.pending = false
		}
		m.mu.Unlock()
	}()
	m.drain(ctx, thread)
	detail, hidden, fits := render.ApprovalDetail(tool, input)
	if hidden {
		detail = "⚠️ contains hidden or right-to-left characters (hidden ones shown as <U+…>); read carefully\n" + detail
	}
	if !fits { // too long to review on a phone: the terminal prompt decides
		m.post(ctx, thread, telegram.Paragraph("🔐 Claude wants to use "+tool+", but the request is too long to review here. Answer it in the Orca terminal."))
		return ipc.Response{}
	}
	d := m.d.Ask(ctx, thread, "Claude (Orca terminal) wants to use "+tool, detail, m.d.PermTimeout)
	var beh map[string]any
	switch d {
	case "allow", "session":
		beh = map[string]any{"behavior": "allow"}
	case "deny":
		beh = map[string]any{"behavior": "deny", "message": "The owner denied this on Telegram."}
	default:
		return ipc.Response{} // no decision: the normal terminal prompt appears
	}
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": beh}})
	return ipc.Response{Output: out}
}

func (m *Manager) link(thread int) string {
	if m.d.BotUser == "" {
		return fmt.Sprint(thread)
	}
	return fmt.Sprintf("https://t.me/%s/%d", m.d.BotUser, thread)
}

func (m *Manager) post(ctx context.Context, thread int, blocks ...telegram.Block) {
	if _, err := m.d.TG.SendRichMessage(ctx, m.d.Owner, thread, telegram.RichMessage{Blocks: blocks}, nil); err != nil {
		log.Printf("handoff post: %v", err)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
