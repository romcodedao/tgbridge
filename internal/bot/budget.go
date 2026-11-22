package bot

import (
	"context"
	"fmt"

	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/streamjson"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

const summaryPrompt = "Write a handoff summary of this conversation so a fresh session can continue it: goal, facts found, decisions, open items, and references (files, Orca sessions, PRs). Be concise. Output only the summary."

func (b *Bot) afterResult(ctx context.Context, thread int, r *streamjson.Result) {
	b.mu.Lock()
	prev, compacting := b.compacting[thread]
	delete(b.compacting, thread)
	b.mu.Unlock()
	tp, _ := b.st.UpdateTopic(thread, func(t *store.Topic) { t.ContextTokens = r.ContextTokens })
	switch {
	case compacting && r.ContextTokens >= prev*8/10:
		b.requestRollover(ctx, thread, "♻️ /compact didn't shrink the context; starting fresh with a summary instead.")
	case r.ContextTokens >= b.cfg.Context.HardLimit:
		b.requestRollover(ctx, thread, fmt.Sprintf("♻️ Context reached %dk tokens; writing a summary and starting fresh.", r.ContextTokens/1000))
	case r.ContextTokens >= b.cfg.Context.SoftLimit && !tp.SoftWarned:
		b.st.UpdateTopic(thread, func(t *store.Topic) { t.SoftWarned = true })
		kb := &telegram.InlineKeyboard{InlineKeyboard: [][]telegram.InlineButton{{
			{Text: "🗜 Compact", CallbackData: fmt.Sprintf("b:c:%d", thread)},
			{Text: "♻️ Start fresh with summary", CallbackData: fmt.Sprintf("b:r:%d", thread)},
		}}}
		b.tg.SendRichMessage(ctx, b.chat(), thread, telegram.RichMessage{Blocks: []telegram.Block{
			telegram.Paragraph(fmt.Sprintf("Context is %dk tokens (warning at %dk, automatic fresh start at %dk). Long contexts cost more and answer worse.",
				r.ContextTokens/1000, b.cfg.Context.SoftLimit/1000, b.cfg.Context.HardLimit/1000))}}, kb)
	}
}

// requestRollover rolls over now if the session is idle, else once its queued turns are done
// (so the summary is never mixed up with an answer the owner is waiting for).
func (b *Bot) requestRollover(ctx context.Context, thread int, notice string) {
	if b.mgr.Busy(key(thread)) {
		b.mu.Lock()
		b.rollWait[thread] = notice
		b.mu.Unlock()
		return
	}
	b.rollover(ctx, thread, notice, nil)
}

// runWaitingRollover starts a deferred rollover once the session is idle. Called after each result.
func (b *Bot) runWaitingRollover(ctx context.Context, thread int) {
	b.mu.Lock()
	notice, ok := b.rollWait[thread]
	if ok && !b.mgr.Busy(key(thread)) {
		delete(b.rollWait, thread)
	} else {
		ok = false
	}
	b.mu.Unlock()
	if ok {
		b.rollover(ctx, thread, notice, nil)
	}
}

// rollover asks the current session for a summary, then starts a fresh conversation that carries it.
// Messages the owner sends meanwhile are kept and replayed into the fresh conversation.
func (b *Bot) rollover(ctx context.Context, thread int, notice string, then func()) {
	if notice != "" {
		b.reply(ctx, b.chat(), thread, notice)
	}
	b.submitInternal(ctx, thread, summaryPrompt, func(summary string) {
		queued := b.mgr.Reset(key(thread)) // synchronous: nothing queued may reach the old session
		b.clearGrants(thread)
		b.st.UpdateTopic(thread, func(t *store.Topic) {
			t.ClaudeSessionID, t.ContextTokens, t.SoftWarned, t.CarryOver = "", 0, false, summary
		})
		go func() {
			b.tg.SendRichMessage(ctx, b.chat(), thread, telegram.RichMessage{Blocks: []telegram.Block{
				telegram.Divider(), telegram.Paragraph("♻️ new context (summary carried over)"),
				telegram.Details("Summary", false, telegram.Paragraph(summary))}}, nil)
			if then != nil {
				then()
			}
			for _, q := range queued {
				b.submit(ctx, thread, q)
			}
		}()
	})
}

func (b *Bot) compact(ctx context.Context, thread int) {
	tp, _ := b.st.Topic(thread)
	if tp.ClaudeSessionID == "" {
		b.reply(ctx, b.chat(), thread, "Nothing to compact yet.")
		return
	}
	if b.mgr.Busy(key(thread)) {
		b.reply(ctx, b.chat(), thread, "Claude is still working; let this turn finish, then send /compact.")
		return
	}
	b.mu.Lock()
	b.compacting[thread] = tp.ContextTokens
	b.mu.Unlock()
	b.submit(ctx, thread, "/compact")
}
