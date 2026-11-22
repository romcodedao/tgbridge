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
