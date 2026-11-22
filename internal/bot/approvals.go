package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/romcodedao/tgbridge/internal/ipc"
	"github.com/romcodedao/tgbridge/internal/render"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

// Decision values returned by Ask.
const (
	Allow        = "allow"
	AllowSession = "session"
	Deny         = "deny"
	Timeout      = "timeout"
)

func nonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Ask posts a permission prompt with buttons in thread and waits for the owner's tap or the timeout.
func (b *Bot) Ask(ctx context.Context, thread int, title, detail string, timeout time.Duration) string {
	n := nonce()
	ch := make(chan string, 1)
	b.mu.Lock()
	b.waits[n] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.waits, n); b.mu.Unlock() }()

	kb := &telegram.InlineKeyboard{InlineKeyboard: [][]telegram.InlineButton{{
		{Text: "✅ Allow", CallbackData: "a:" + n + ":y"},
		{Text: "✅ Allow for session", CallbackData: "a:" + n + ":s"},
		{Text: "⛔ Deny", CallbackData: "a:" + n + ":n"},
	}}}
	msg := telegram.RichMessage{Blocks: []telegram.Block{telegram.Heading(3, "🔐 "+title), telegram.Pre(detail, "json"),
		telegram.Footer(fmt.Sprintf("expires in %s", timeout.Round(time.Second)))}}
	m, err := b.tg.SendRichMessage(ctx, b.chat(), thread, msg, kb)
	if err != nil {
		m, err = b.replyHTML(ctx, thread, "🔐 <b>"+esc(title)+"</b>\n<pre>"+esc(detail)+"</pre>", kb)
		if err != nil {
			return Timeout // the owner never saw it: no answer, not a refusal
		}
	}
	b.st.PutApproval(store.Approval{Nonce: n, ThreadID: thread, MsgID: m.MessageID, Expires: time.Now().Add(timeout)})
	b.audit.Log("approval_asked", map[string]any{"thread": thread, "title": title})
	select {
	case d := <-ch:
		return d
	case <-time.After(timeout):
	case <-ctx.Done():
	}
	if _, ok := b.st.TakeApproval(n); ok {
		b.markDecided(ctx, m.MessageID, title, "⌛ No answer — "+map[bool]string{true: "timed out", false: "cancelled"}[ctx.Err() == nil])
	}
	b.audit.Log("approval", map[string]any{"thread": thread, "decision": Timeout})
	return Timeout
}

func (b *Bot) markDecided(ctx context.Context, msgID int, title, outcome string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second) // also on shutdown
	defer cancel()
	b.tg.EditMessageRich(ctx, b.chat(), msgID, telegram.RichMessage{Blocks: []telegram.Block{telegram.Paragraph("🔐 " + title), telegram.Paragraph(outcome)}}, nil)
}

func (b *Bot) onApprovalCallback(ctx context.Context, q *telegram.CallbackQuery) {
	parts := strings.Split(q.Data, ":")
	if len(parts) != 3 {
		return
	}
	a, ok := b.st.TakeApproval(parts[1])
	if !ok {
		b.tg.AnswerCallbackQuery(ctx, q.ID, "This request has expired.")
		return
	}
	d, label := Deny, "⛔ Denied"
	switch parts[2] {
	case "y":
		d, label = Allow, "✅ Allowed"
	case "s":
		d, label = AllowSession, "✅ Allowed for this session"
	}
	b.mu.Lock()
	ch := b.waits[parts[1]]
	b.mu.Unlock()
	if ch != nil {
		ch <- d
	}
	b.audit.Log("approval", map[string]any{"thread": a.ThreadID, "decision": d})
	b.tg.AnswerCallbackQuery(ctx, q.ID, label)
	b.markDecided(ctx, a.MsgID, "Permission request", label)
}

type approveArgs struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

func behavior(allow bool, input json.RawMessage, msg string) ipc.Response {
	var v map[string]any
	if allow {
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		v = map[string]any{"behavior": "allow", "updatedInput": input}
	} else {
		v = map[string]any{"behavior": "deny", "message": msg}
	}
	j, _ := json.Marshal(v)
	return ipc.Response{Text: string(j)}
}

// approveIPC answers the engine's --permission-prompt-tool call.
func (b *Bot) approveIPC(ctx context.Context, r ipc.Request) ipc.Response {
	var a approveArgs
	json.Unmarshal(r.Args, &a)
	tp, ok := b.engineTopic(r)
	if !ok {
		return behavior(false, nil, "tgbridge: unknown session")
	}
	gk := grantKey(a.ToolName, a.Input)
	b.mu.Lock()
	auto := b.allowTools[tp.ThreadID][gk]
	b.mu.Unlock()
	if auto {
		return behavior(true, a.Input, "")
	}
	detail, hidden, fits := render.ApprovalDetail(a.ToolName, a.Input)
	if !fits { // never ask the owner to approve text they can't fully see
		b.reply(ctx, b.chat(), tp.ThreadID, "⛔ Denied a "+a.ToolName+" call: its input is too long to review on a phone. Claude was told to split it.")
		return behavior(false, nil, fmt.Sprintf("tgbridge: input too long to review on Telegram (limit %d characters); split it into smaller steps", render.MaxApprovalUnits))
	}
	if hidden {
		detail = "⚠️ contains hidden or right-to-left characters (hidden ones shown as <U+…>); read carefully\n" + detail
	}
	switch b.Ask(ctx, tp.ThreadID, "Claude wants to use "+a.ToolName, detail, b.cfg.Engine.PermissionTimeout) {
	case AllowSession:
		b.mu.Lock()
		if b.allowTools[tp.ThreadID] == nil {
			b.allowTools[tp.ThreadID] = map[string]bool{}
		}
		b.allowTools[tp.ThreadID][gk] = true
		b.mu.Unlock()
		return behavior(true, a.Input, "")
	case Allow:
		return behavior(true, a.Input, "")
	case Deny:
		return behavior(false, nil, "The owner denied this on Telegram.")
	default:
		return behavior(false, nil, "no answer from owner")
	}
}

// engineTopic finds the engine topic behind an MCP call: by the thread tgbridge put in the engine's MCP
// env (stable across /clear, /compact and resume, which change Claude's session id), else by session id.
func (b *Bot) engineTopic(r ipc.Request) (store.Topic, bool) {
	tp, ok := b.st.Topic(r.Thread)
	if r.Thread == 0 || !ok {
		tp, ok = b.st.TopicBySession(r.SessionID)
	}
	return tp, ok && tp.Kind == store.KindEngine && !tp.Closed
}

// grantKey scopes "Allow for session": Bash to the exact command, file tools to the exact path,
// WebFetch to the URL's host; any other tool (MCP connectors, Agent…) to its exact input.
func grantKey(tool string, input json.RawMessage) string {
	var in struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		URL          string `json:"url"`
	}
	json.Unmarshal(input, &in)
	switch tool {
	case "Bash": // the command and every option (sandbox, timeout…); a new description alone needs no new tap
		var all map[string]any
		if json.Unmarshal(input, &all) != nil || all == nil {
			return tool + "\x00" + string(input) // not an object: exact bytes
		}
		delete(all, "description")
		canon, _ := json.Marshal(all) // map keys come out sorted
		return tool + "\x00" + string(canon)
	case "Edit", "Write", "MultiEdit":
		return tool + "\x00" + in.FilePath
	case "NotebookEdit":
		return tool + "\x00" + in.NotebookPath
	case "WebFetch":
		host := in.URL
		if u, err := url.Parse(in.URL); err == nil && u.Host != "" {
			host = u.Host
		}
		return tool + "\x00" + host
	}
	return tool + "\x00" + string(input)
}

func (b *Bot) clearGrants(thread int) {
	b.mu.Lock()
	delete(b.allowTools, thread)
	b.mu.Unlock()
}

func (b *Bot) sendIPC(ctx context.Context, r ipc.Request) ipc.Response {
	var a struct {
		Text string `json:"text"`
	}
	json.Unmarshal(r.Args, &a)
	if strings.TrimSpace(a.Text) == "" {
		return ipc.Response{Error: "text is empty"}
	}
	thread := 0
	if tp, ok := b.engineTopic(r); ok {
		thread = tp.ThreadID
	} else if tp, ok := b.st.TopicBySession(r.SessionID); ok {
		thread = tp.ThreadID
	}
	if _, err := b.tg.SendRichMessage(ctx, b.chat(), thread, telegram.RichMessage{Blocks: []telegram.Block{telegram.Paragraph("📨 " + a.Text)}}, nil); err != nil {
		b.reply(ctx, b.chat(), thread, "📨 "+a.Text)
	}
	return ipc.Response{Text: "sent"}
}
