package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/engine"
	"github.com/romcodedao/tgbridge/internal/render"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/streamjson"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

type turnState struct {
	thread   int
	turn     *streamjson.Turn
	draftID  int64
	dirty    bool
	lastSent time.Time
	stopped  bool
	internal func(text string) // set for tgbridge's own turns (summaries): not rendered
}

func key(thread int) string { return strconv.Itoa(thread) }

const carryHeader = "Context carried over from the previous conversation (summary):\n\n"

// submit sends text to thread's engine session, resuming or rolling over as needed.
func (b *Bot) submit(ctx context.Context, thread int, text string) {
	tp, _ := b.st.Topic(thread)
	if tp.ClaudeSessionID != "" && !b.mgr.Live(key(thread)) && !tp.LastActive.IsZero() && time.Since(tp.LastActive) > b.cfg.Engine.MaxAge {
		b.rollover(ctx, thread, "♻️ This conversation was quiet for over "+b.cfg.Engine.MaxAge.String()+"; starting fresh with a summary.", func() { b.submit(ctx, thread, text) })
		return
	}
	if tp.CarryOver != "" {
		text = carryHeader + tp.CarryOver + "\n\n---\n\n" + text
	}
	b.st.UpdateTopic(thread, func(t *store.Topic) { t.LastActive, t.CarryOver = time.Now(), "" })
	b.mu.Lock()
	if b.turns[thread] == nil && !b.mgr.Busy(key(thread)) {
		b.turns[thread] = b.newTurn(thread)
	}
	b.mu.Unlock()
	b.mgr.Submit(ctx, key(thread), func() engine.Spec { return b.spec(thread) }, text, b.sink(ctx, thread))
}

// submitInternal runs a turn whose output goes to fn instead of the chat.
func (b *Bot) submitInternal(ctx context.Context, thread int, text string, fn func(string)) {
	b.mu.Lock()
	ts := b.newTurn(thread)
	ts.internal = fn
	b.turns[thread] = ts
	b.mu.Unlock()
	b.mgr.Submit(ctx, key(thread), func() engine.Spec { return b.spec(thread) }, text, b.sink(ctx, thread))
}

// newTurn must be called with mu held.
func (b *Bot) newTurn(thread int) *turnState {
	b.nextDraft++
	return &turnState{thread: thread, turn: &streamjson.Turn{Started: time.Now()}, draftID: b.nextDraft, dirty: true}
}

func (b *Bot) spec(thread int) engine.Spec {
	tp, _ := b.st.Topic(thread)
	cwd := tp.Cwd
	if cwd == "" {
		cwd = b.cfg.DefaultCwd
	}
	args := append(engine.BuildArgs(b.cfg, b.settingsPath, tp.ClaudeSessionID, b.prompt()), "--mcp-config", b.mcpConfigFor(thread))
	if m := config.PermissionModes[b.mode(tp)]; m != "" {
		args = append(args, "--permission-mode", m)
	}
	return engine.Spec{Key: key(thread), Cwd: cwd, ResumeID: tp.ClaudeSessionID, Binary: b.cfg.ClaudeBin, Args: args, Env: engine.CleanEnv(os.Environ())}
}

func (b *Bot) sink(ctx context.Context, thread int) func(streamjson.Event) {
	return func(ev streamjson.Event) { b.onEvent(ctx, thread, ev) }
}

func (b *Bot) onEvent(ctx context.Context, thread int, ev streamjson.Event) {
	switch ev := ev.(type) {
	case streamjson.Init:
		var changed bool
		b.st.UpdateTopic(thread, func(t *store.Topic) {
			changed = t.ClaudeSessionID != "" && t.ClaudeSessionID != ev.SessionID
			t.ClaudeSessionID = ev.SessionID
		})
		if changed {
			b.clearGrants(thread)
		}
		return
	case streamjson.Queued:
		b.postQueued(ctx, thread)
		return
	}
	b.mu.Lock()
	ts := b.turns[thread]
	if ts == nil {
		ts = b.newTurn(thread)
		b.turns[thread] = ts
	}
	switch ev.(type) {
	case streamjson.Result, streamjson.Crashed:
		delete(b.turns, thread)
	}
	ts.turn.Apply(ev)
	ts.dirty = true
	b.mu.Unlock()

	switch ev := ev.(type) {
	case streamjson.Result:
		if ev.SessionID != "" { // keep the id --resume needs current even if Claude switched sessions
			b.st.UpdateTopic(thread, func(t *store.Topic) { t.ClaudeSessionID = ev.SessionID })
		}
		b.finish(ctx, ts, "")
	case streamjson.Crashed:
		msg := strings.TrimSpace(ev.Err)
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		b.finish(ctx, ts, "⚠️ crashed")
		b.reply(ctx, b.chat(), thread, "⚠️ The Claude session crashed. Your next message resumes it.\n"+msg)
	}
}

func (b *Bot) flushDrafts(ctx context.Context) {
	b.mu.Lock()
	var due []struct {
		ts  *turnState
		msg telegram.RichMessage
	}
	now := time.Now()
	for _, ts := range b.turns {
		if ts.internal != nil {
			continue
		}
		if (ts.dirty && now.Sub(ts.lastSent) >= b.cfg.DraftInterval) || now.Sub(ts.lastSent) >= 20*time.Second {
			ts.dirty, ts.lastSent = false, now
			due = append(due, struct {
				ts  *turnState
				msg telegram.RichMessage
			}{ts, render.Draft(ts.turn)})
		}
	}
	b.mu.Unlock()
	for _, d := range due {
		b.mu.Lock()
		live := b.turns[d.ts.thread] == d.ts
		b.mu.Unlock()
		if !live { // the final message already went out
			continue
		}
		if err := b.tg.SendRichDraft(ctx, b.chat(), d.ts.thread, d.ts.draftID, d.msg, true); err != nil {
			log.Printf("draft thread %d: %v", d.ts.thread, err)
		}
	}
}

// finish sends the final message(s) for a turn and applies the context budget.
func (b *Bot) finish(ctx context.Context, ts *turnState, note string) {
	t := ts.turn
	if ts.internal != nil {
		text := t.Text
		if t.Result != nil && t.Result.Text != "" {
			text = t.Result.Text
		}
		ts.internal(text)
		return
	}
	var ctxTokens int
	var cost float64
	if t.Result != nil {
		ctxTokens, cost = t.Result.ContextTokens, t.Result.CostUSD
	}
	footer := render.Footer(ctxTokens, time.Since(t.Started), cost)
	if ts.stopped {
		note = "⏹ stopped"
	}
	if note != "" {
		footer += " · " + note
	}
	if t.Result != nil && t.Result.IsError && !ts.stopped && strings.TrimSpace(t.Text) == "" {
		t.Text = "⚠️ " + t.Result.Subtype + " " + t.Result.Text
	}
	b.sendFinal(ctx, ts.thread, t, footer)
	if t.Result != nil {
		b.afterResult(ctx, ts.thread, t.Result)
	}
	b.runWaitingRollover(ctx, ts.thread)
}

func (b *Bot) sendFinal(ctx context.Context, thread int, t *streamjson.Turn, footer string) {
	msgs := render.Final(t, footer)
	for i, m := range msgs {
		if _, err := b.tg.SendRichMessage(ctx, b.chat(), thread, m, nil); err != nil {
			log.Printf("rich final part %d rejected (%v); using fallback", i, err)
			if i == 0 {
				for _, h := range render.FallbackHTML(t, footer) {
					b.sendHTMLOrPlain(ctx, thread, h)
				}
				return
			}
			for _, chunk := range render.SplitText(render.BlocksText(m.Blocks), 3800) {
				b.sendHTMLOrPlain(ctx, thread, esc(chunk))
			}
		}
	}
}

// sendHTMLOrPlain sends Telegram HTML, retrying as plain text if Telegram rejects the markup.
func (b *Bot) sendHTMLOrPlain(ctx context.Context, thread int, htmlText string) {
	if _, err := b.replyHTML(ctx, thread, htmlText, nil); err == nil {
		return
	}
	plain := html.UnescapeString(tagRe.ReplaceAllString(htmlText, ""))
	if _, err := b.tg.SendMessage(ctx, telegram.SendMessageParams{ChatID: b.chat(), ThreadID: thread, Text: plain}); err != nil {
		log.Printf("thread %d: reply could not be delivered: %v", thread, err)
	}
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func (b *Bot) onStopDraft(ctx context.Context, thread int, draftID int64) {
	b.mu.Lock()
	ts := b.turns[thread]
	match := ts != nil && ts.draftID == draftID
	if match {
		ts.stopped = true
	}
	b.mu.Unlock()
	if match {
		b.mgr.Stop(key(thread))
	}
}

func (b *Bot) stopThread(ctx context.Context, thread int) bool {
	b.mu.Lock()
	ts := b.turns[thread]
	if ts != nil {
		ts.stopped = true
	}
	b.mu.Unlock()
	if !b.mgr.Busy(key(thread)) {
		return false
	}
	b.mgr.Stop(key(thread))
	return true
}

func (b *Bot) postQueued(ctx context.Context, thread int) {
	var rows [][]telegram.InlineButton
	for _, tp := range b.st.Topics() {
		if tp.ThreadID != thread && tp.Kind == store.KindEngine && b.mgr.Busy(key(tp.ThreadID)) {
			rows = append(rows, []telegram.InlineButton{{Text: "⏹ Stop " + tp.Title, CallbackData: fmt.Sprintf("k:%d", tp.ThreadID)}})
		}
	}
	var kb *telegram.InlineKeyboard
	if len(rows) > 0 {
		kb = &telegram.InlineKeyboard{InlineKeyboard: rows}
	}
	b.replyHTML(ctx, thread, "⏳ All Claude slots are busy. Your message is queued and starts when one frees up.", kb)
}
