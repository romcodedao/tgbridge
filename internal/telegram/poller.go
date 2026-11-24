package telegram

import (
	"context"
	"log"
	"time"
)

// UpdateSource delivers updates; a webhook receiver can implement it later.
type UpdateSource interface {
	Updates(ctx context.Context) <-chan Update
}

type Poller struct {
	c       *Client
	timeout int
}

func NewPoller(c *Client, timeout int) *Poller { return &Poller{c: c, timeout: timeout} }

var allowedUpdates = []string{"message", "edited_message", "callback_query", "my_chat_member", "stopped_message_generation"}

func (p *Poller) Updates(ctx context.Context) <-chan Update {
	ch := make(chan Update)
	go func() {
		defer close(ch)
		offset := 0
		for ctx.Err() == nil {
			var ups []Update
			err := p.c.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": p.timeout, "allowed_updates": allowedUpdates}, &ups)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("getUpdates: %v", err)
					time.Sleep(3 * time.Second)
				}
				continue
			}
			for _, u := range ups {
				offset = u.UpdateID + 1
				select {
				case ch <- u:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch
}

// FirstSender waits for a private message from a person sent at or after since, and returns its
// sender. Groups, bots and older queued messages are skipped, so a stranger's earlier message
// can't become owner_id. It confirms the update, so a later serve doesn't handle it again.
// Used by `tgbridge whoami` before owner_id is set.
func (c *Client) FirstSender(ctx context.Context, since time.Time) (*User, error) {
	offset := 0
	for {
		var ups []Update
		if err := c.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"message"}}, &ups); err != nil {
			return nil, err
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			if m := u.Message; m != nil && m.From != nil && !m.From.IsBot && m.Chat.Type == "private" && m.Date >= since.Unix() {
				c.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 0}, nil)
				return m.From, nil
			}
		}
	}
}
