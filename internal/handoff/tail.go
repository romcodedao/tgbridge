package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/romcodedao/tgbridge/internal/render"
	"github.com/romcodedao/tgbridge/internal/store"
	"github.com/romcodedao/tgbridge/internal/streamjson"
	"github.com/romcodedao/tgbridge/internal/telegram"
)

func (m *Manager) tail(ctx context.Context, thread int) {
	t := time.NewTicker(m.d.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.drain(ctx, thread)
		}
	}
}

func (m *Manager) threadLock(thread int) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks == nil {
		m.locks = map[int]*sync.Mutex{}
	}
	if m.locks[thread] == nil {
		m.locks[thread] = &sync.Mutex{}
	}
	return m.locks[thread]
}

// drain posts every complete transcript line after the saved offset. The offset advances after each
// line, so a failure never re-posts earlier lines; a message Telegram rejects is sent once as plain text.
// Only this thread's lock is held, never the manager's, during network calls.
func (m *Manager) drain(ctx context.Context, thread int) {
	l := m.threadLock(thread)
	l.Lock()
	defer l.Unlock()
	if ctx.Err() != nil {
		return
	}
	tp, ok := m.d.Store.Topic(thread)
	if !ok || tp.Closed {
		return
	}
	if tp.TranscriptPath == "" {
		p := m.findTranscript(tp.ClaudeSessionID)
		if p == "" {
			return
		}
		tp.TranscriptPath = p
		m.d.Store.UpdateTopic(thread, func(t *store.Topic) { t.TranscriptPath = p })
	}
	f, err := os.Open(tp.TranscriptPath)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(tp.Offset, io.SeekStart); err != nil {
		return
	}
	data, _ := io.ReadAll(io.LimitReader(f, 8<<20))
	off := tp.Offset
	for {
		nl := bytes.IndexByte(data, '\n')
		if nl < 0 {
			if len(data) == 8<<20 { // one huge line: skip it rather than stall forever
				off += int64(len(data))
				m.d.Store.UpdateTopic(thread, func(t *store.Topic) { t.Offset = off })
			}
			return
		}
		line := data[:nl]
		data = data[nl+1:]
		if blocks := transcriptBlocks(line); len(blocks) > 0 {
			for _, msg := range packBlocks(blocks) {
				if !m.send(ctx, thread, msg) {
					return // transient failure: retry this line next poll
				}
			}
		}
		off += int64(nl) + 1
		m.d.Store.UpdateTopic(thread, func(t *store.Topic) { t.Offset = off; t.LastActive = time.Now() })
	}
}

// send posts one rich message; false means a transient failure (retry later).
func (m *Manager) send(ctx context.Context, thread int, msg telegram.RichMessage) bool {
	_, err := m.d.TG.SendRichMessage(ctx, m.d.Owner, thread, msg, nil)
	if err == nil {
		return true
	}
	var ae *telegram.APIError
	if !errors.As(err, &ae) || ae.Code == 429 {
		return false
	}
	text := render.BlocksText(msg.Blocks)
	if r := []rune(text); len(r) > 4000 {
		text = string(r[:4000]) + "…"
	}
	if _, err := m.d.TG.SendMessage(ctx, telegram.SendMessageParams{ChatID: m.d.Owner, ThreadID: thread, Text: text}); err != nil {
		log.Printf("handoff thread %d: dropped a message Telegram rejected: %v", thread, err)
	}
	return true
}

func packBlocks(bs []telegram.Block) []telegram.RichMessage {
	var out []telegram.RichMessage
	cur := telegram.RichMessage{}
	size := 0
	for _, b := range bs {
		j, _ := json.Marshal(b)
		if size+len(j) > render.MaxRichBytes-256 && len(cur.Blocks) > 0 {
			out = append(out, cur)
			cur, size = telegram.RichMessage{}, 0
		}
		if len(j) > render.MaxRichBytes-256 {
			b = telegram.Pre(string([]rune(string(j))[:3000])+"…", "")
		}
		cur.Blocks = append(cur.Blocks, b)
		size += len(j)
	}
	if len(cur.Blocks) > 0 {
		out = append(out, cur)
	}
	return out
}

type tLine struct {
	Type    string `json:"type"`
	Message *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	IsMeta bool `json:"isMeta"`
}

type tBlock struct {
	Type     string         `json:"type"`
	Text     string         `json:"text"`
	Thinking string         `json:"thinking"`
	Name     string         `json:"name"`
	Input    map[string]any `json:"input"`
	IsError  bool           `json:"is_error"`
}

// transcriptBlocks renders one transcript line: thinking → collapsed details, tool use → list item, text → Markdown.
func transcriptBlocks(line []byte) []telegram.Block {
	var l tLine
	if json.Unmarshal(line, &l) != nil || l.Message == nil || l.IsMeta {
		return nil
	}
	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		if l.Type == "user" && strings.TrimSpace(s) != "" && !strings.HasPrefix(s, "<") {
			return []telegram.Block{telegram.Paragraph([]any{telegram.Bold("👤 "), truncate(s, 1500)})}
		}
		return nil
	}
	var bs []tBlock
	json.Unmarshal(l.Message.Content, &bs)
	var out []telegram.Block
	for _, b := range bs {
		switch {
		case l.Type == "assistant" && b.Type == "thinking" && strings.TrimSpace(b.Thinking) != "":
			out = append(out, telegram.Details("💭 Thinking", false, telegram.Paragraph(truncate(b.Thinking, 3500))))
		case l.Type == "assistant" && b.Type == "text" && strings.TrimSpace(b.Text) != "":
			out = append(out, render.MarkdownBlocks(b.Text)...)
		case l.Type == "assistant" && b.Type == "tool_use":
			label := b.Name
			if s := streamjson.Summary(b.Name, b.Input); s != "" {
				label += ": " + s
			}
			out = append(out, telegram.List(telegram.ListItem{Blocks: []telegram.Block{telegram.Paragraph("🔧 " + label)}}))
		case l.Type == "user" && b.Type == "tool_result" && b.IsError:
			out = append(out, telegram.Paragraph("❌ tool failed"))
		}
	}
	return out
}
