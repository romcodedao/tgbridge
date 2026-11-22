package bot

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

const helpText = `<b>tgbridge</b> — Claude Code from Telegram
Send any message in General to start a new session (one topic each). Messages in a topic continue it.

/new [path] &lt;task&gt; — new session (optionally in a folder)
/sessions — list sessions and Orca agents
/status &lt;orca-session&gt; — last messages of an Orca agent
/stop — interrupt the running turn
/reset or /clear — fresh conversation in this topic
/mode ask|auto|full — how much Claude may do without asking (this topic)
/compact — shrink this conversation's context
/close or /exit — end this session
/handoff off — stop forwarding a handed-off Orca session
/whoami — your Telegram user ID`

// command handles a slash command; false means "not ours, send it to Claude".
func (b *Bot) command(ctx context.Context, m *telegram.Message, cmd, arg string) bool {
	thread := m.MessageThreadID
	switch cmd {
	case "/whoami":
		b.reply(ctx, b.chat(), thread, fmt.Sprintf("Your user ID: %d (owner)", m.From.ID))
	case "/start", "/help":
		b.replyHTML(ctx, thread, helpText, nil)
	case "/new":
		cwd, task := b.cfg.DefaultCwd, arg
		if first, rest, _ := strings.Cut(arg, " "); first != "" {
			if st, err := os.Stat(expand(first)); err == nil && st.IsDir() {
				cwd, task = expand(first), strings.TrimSpace(rest)
			}
		}
		if task == "" {
			b.reply(ctx, b.chat(), thread, "Usage: /new [path] <task>")
			return true
		}
		b.newSession(ctx, cwd, titleFrom(task), task, nil)
	case "/sessions":
		b.sessions(ctx, thread)
	case "/status":
		if b.orca == nil || arg == "" {
			b.reply(ctx, b.chat(), thread, "Usage: /status <orca-session-id> (see /sessions)")
			return true
		}
		out, err := b.orca.Read(ctx, arg, 10)
		if err != nil {
			b.reply(ctx, b.chat(), thread, "orca: "+err.Error())
			return true
		}
		b.tg.SendRichMessage(ctx, b.chat(), thread, telegram.RichMessage{Blocks: []telegram.Block{telegram.Pre(tailRunes(out, 3500), "")}}, nil)
	case "/stop":
		if !b.stopThread(ctx, thread) {
			b.reply(ctx, b.chat(), thread, "Nothing is running here.")
		}
	case "/mode":
		b.setMode(ctx, thread, arg)
	case "/reset", "/clear":
		if thread == 0 {
			b.reply(ctx, b.chat(), 0, "Use /reset inside a session topic.")
			return true
		}
		b.mgr.Close(key(thread))
		b.clearGrants(thread)
		b.removeUploads(thread)
		b.st.UpdateTopic(thread, func(t *store.Topic) {
			t.ClaudeSessionID, t.ContextTokens, t.SoftWarned, t.CarryOver = "", 0, false, ""
		})
		b.reply(ctx, b.chat(), thread, "🆕 Fresh conversation in this topic.")
	case "/compact":
		if thread != 0 {
			b.compact(ctx, thread)
		}
	case "/close", "/exit":
		b.closeThread(ctx, thread)
	case "/handoff":
		tp, ok := b.st.Topic(thread)
		if !ok || tp.Kind != store.KindHandoff || b.hand == nil {
			b.reply(ctx, b.chat(), thread, "To hand off an Orca session use /sessions, or say \"notify me on Telegram\" inside that session.")
			return true
		}
		if arg == "off" {
			b.hand.Off(ctx, thread, false)
		} else {
			b.reply(ctx, b.chat(), thread, "This session is already handed off. Use /handoff off to stop.")
		}
	default:
		return false
	}
	return true
}

// mode is the topic's permission mode, else the config default.
func (b *Bot) mode(tp store.Topic) string {
	if tp.Mode != "" {
		return tp.Mode
	}
	return b.cfg.Engine.PermissionMode
}

var modeHelp = map[string]string{
	"ask":  "ask: read-only tools run freely; anything else asks you here",
	"auto": "auto: Claude Code's auto mode decides; only risky actions ask you",
	"full": "full: no permission prompts at all (secret files stay blocked)",
}

func (b *Bot) setMode(ctx context.Context, thread int, arg string) {
	tp, ok := b.st.Topic(thread)
	if thread == 0 || !ok || tp.Kind != store.KindEngine {
		b.reply(ctx, b.chat(), thread, "Use /mode inside a session topic. New sessions start in "+b.cfg.Engine.PermissionMode+" (engine.permission_mode in config).")
		return
	}
	if _, valid := config.PermissionModes[arg]; !valid {
		b.reply(ctx, b.chat(), thread, "Usage: /mode ask|auto|full\nNow: "+modeHelp[b.mode(tp)])
		return
	}
	if b.mgr.Busy(key(thread)) {
		b.reply(ctx, b.chat(), thread, "Claude is still working; send /mode again when this turn finishes.")
		return
	}
	b.st.UpdateTopic(thread, func(t *store.Topic) { t.Mode = arg })
	b.mgr.Close(key(thread)) // the next message resumes the same conversation with the new mode
	b.audit.Log("mode", map[string]any{"thread": thread, "mode": arg})
	b.reply(ctx, b.chat(), thread, "✅ Mode "+modeHelp[arg])
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return h + p[1:]
	}
	return p
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

func (b *Bot) closeThread(ctx context.Context, thread int) {
	tp, ok := b.st.Topic(thread)
	if thread == 0 || !ok {
		b.reply(ctx, b.chat(), thread, "Use /close inside a session topic.")
		return
	}
	if tp.Kind == store.KindHandoff {
		kb := &telegram.InlineKeyboard{InlineKeyboard: [][]telegram.InlineButton{{
			{Text: "Stop handoff only", CallbackData: fmt.Sprintf("x:%d:0", thread)},
			{Text: "Also close the terminal", CallbackData: fmt.Sprintf("x:%d:1", thread)},
		}}}
		b.replyHTML(ctx, thread, "End this handoff?", kb)
		return
	}
	b.mgr.Close(key(thread))
	b.clearGrants(thread)
	b.removeUploads(thread)
	b.st.UpdateTopic(thread, func(t *store.Topic) { t.Closed = true })
	b.reply(ctx, b.chat(), thread, "✅ Session closed.")
	b.tg.MarkTopicClosed(ctx, b.chat(), thread, tp.Title)
	b.audit.Log("close", map[string]any{"thread": thread})
}

func (b *Bot) topicLink(thread int) string {
	return fmt.Sprintf("https://t.me/%s/%d", b.user, thread)
}

func (b *Bot) sessions(ctx context.Context, thread int) {
	var sb strings.Builder
	sb.WriteString("<b>Telegram sessions</b>\n")
	n := 0
	for _, tp := range b.st.Topics() {
		if tp.Closed {
			continue
		}
		n++
		state := "idle"
		if b.mgr.Busy(key(tp.ThreadID)) {
			state = "working"
		} else if !b.mgr.Live(key(tp.ThreadID)) {
			state = "asleep"
		}
		if tp.Kind == store.KindHandoff {
			state = "handed off"
		}
		fmt.Fprintf(&sb, "• <a href=\"%s\">%s</a> — %s, ctx %dk, %s ago\n", b.topicLink(tp.ThreadID), esc(tp.Title), state, tp.ContextTokens/1000, time.Since(tp.LastActive).Round(time.Minute))
	}
	if n == 0 {
		sb.WriteString("none\n")
	}
	var rows [][]telegram.InlineButton
	if b.orca != nil {
		agents, err := b.orca.List(ctx)
		sb.WriteString("\n<b>Orca agents</b>\n")
		if err != nil {
			sb.WriteString("orca: " + esc(err.Error()) + "\n")
		}
		for _, a := range agents {
			title := a.Title
			if title == "" {
				title = a.Workspace
			}
			fmt.Fprintf(&sb, "• %s — %s (%s, %s) <code>%s</code>\n", esc(title), esc(a.State), esc(a.Kind), esc(a.Workspace), esc(a.SessionID))
			if a.Kind == "terminal" && a.Agent == "claude" && b.hand != nil && len(rows) < 8 {
				rows = append(rows, []telegram.InlineButton{{Text: "📲 Hand off: " + truncate(title, 30), CallbackData: "h:" + a.SessionID}})
			}
		}
	}
	var kb *telegram.InlineKeyboard
	if len(rows) > 0 {
		kb = &telegram.InlineKeyboard{InlineKeyboard: rows}
	}
	b.replyHTML(ctx, thread, sb.String(), kb)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (b *Bot) onCallback(ctx context.Context, q *telegram.CallbackQuery) {
	if q.From.ID != b.cfg.OwnerID {
		b.tg.AnswerCallbackQuery(ctx, q.ID, "Not allowed.")
		return
	}
	kind, rest, _ := strings.Cut(q.Data, ":")
	switch kind {
	case "a":
		b.onApprovalCallback(ctx, q)
		return
	case "b":
		action, th, _ := strings.Cut(rest, ":")
		thread, _ := strconv.Atoi(th)
		b.tg.AnswerCallbackQuery(ctx, q.ID, "OK")
		if action == "c" {
			go b.compact(context.WithoutCancel(ctx), thread)
		} else {
			go b.requestRollover(context.WithoutCancel(ctx), thread, "♻️ Starting fresh with a summary.")
		}
		return
	case "k":
		thread, _ := strconv.Atoi(rest)
		b.stopThread(ctx, thread)
		b.tg.AnswerCallbackQuery(ctx, q.ID, "Stopping")
		return
	case "h":
		if b.hand == nil {
			b.tg.AnswerCallbackQuery(ctx, q.ID, "Handoff unavailable")
			return
		}
		thread, err := b.hand.OnOrcaSession(ctx, rest)
		if err != nil {
			b.tg.AnswerCallbackQuery(ctx, q.ID, truncate(err.Error(), 190))
			return
		}
		b.tg.AnswerCallbackQuery(ctx, q.ID, "Handed off")
		b.replyHTML(ctx, 0, fmt.Sprintf("📲 Handed off: <a href=\"%s\">open topic</a>", b.topicLink(thread)), nil)
		return
	case "x":
		th, c, _ := strings.Cut(rest, ":")
		thread, _ := strconv.Atoi(th)
		if b.hand != nil {
			b.hand.Off(ctx, thread, c == "1")
		}
		b.tg.AnswerCallbackQuery(ctx, q.ID, "Done")
		return
	}
	b.tg.AnswerCallbackQuery(ctx, q.ID, "")
}
