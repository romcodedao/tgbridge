// Package telegram is a minimal Bot API client using raw HTTP (rich-message methods are newer than libraries).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string { return fmt.Sprintf("telegram %d: %s", e.Code, e.Description) }

type Client struct {
	base  string
	http  *http.Client
	sleep func(time.Duration) // tests only; nil means a context-aware wait
}

func New(token, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	return &Client{base: baseURL + "/bot" + token + "/", http: &http.Client{Timeout: 90 * time.Second}}
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// Call posts params as JSON to method and decodes result into out (may be nil).
// A 429 is retried up to 4 times after retry_after, unless ctx ends first.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = c.call(ctx, method, params, out)
		ae, ok := err.(*APIError)
		if !ok || ae.Code != 429 {
			return err
		}
		wait := time.Duration(max(ae.RetryAfter, 1)) * time.Second
		if c.sleep != nil {
			c.sleep(wait)
			continue
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
	return err
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// never include the URL: it carries the token
		return fmt.Errorf("telegram %s: request failed", method)
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: bad response (%d)", method, resp.StatusCode)
	}
	if !env.OK {
		return &APIError{Code: env.ErrorCode, Description: env.Description, RetryAfter: env.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func withThread(p map[string]any, thread int) map[string]any {
	if thread != 0 {
		p["message_thread_id"] = thread
	}
	return p
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	return &u, c.Call(ctx, "getMe", map[string]any{}, &u)
}

type SendMessageParams struct {
	ChatID    int64
	ThreadID  int
	Text      string
	ParseMode string
	Markup    *InlineKeyboard
}

func (c *Client) SendMessage(ctx context.Context, p SendMessageParams) (*Message, error) {
	m := withThread(map[string]any{"chat_id": p.ChatID, "text": p.Text, "link_preview_options": map[string]any{"is_disabled": true}}, p.ThreadID)
	if p.ParseMode != "" {
		m["parse_mode"] = p.ParseMode
	}
	if p.Markup != nil {
		m["reply_markup"] = p.Markup
	}
	var out Message
	return &out, c.Call(ctx, "sendMessage", m, &out)
}

func (c *Client) SendRichMessage(ctx context.Context, chatID int64, thread int, msg RichMessage, markup *InlineKeyboard) (*Message, error) {
	m := withThread(map[string]any{"chat_id": chatID, "rich_message": msg}, thread)
	if markup != nil {
		m["reply_markup"] = markup
	}
	var out Message
	return &out, c.Call(ctx, "sendRichMessage", m, &out)
}

func (c *Client) SendRichDraft(ctx context.Context, chatID int64, thread int, draftID int64, msg RichMessage, canStop bool) error {
	m := withThread(map[string]any{"chat_id": chatID, "draft_id": draftID, "rich_message": msg}, thread)
	if canStop {
		m["can_stop"] = true
	}
	return c.Call(ctx, "sendRichMessageDraft", m, nil)
}

func (c *Client) EditMessageText(ctx context.Context, chatID int64, msgID int, text, parseMode string, markup *InlineKeyboard) error {
	m := map[string]any{"chat_id": chatID, "message_id": msgID, "text": text}
	if parseMode != "" {
		m["parse_mode"] = parseMode
	}
	if markup != nil {
		m["reply_markup"] = markup
	}
	return c.Call(ctx, "editMessageText", m, nil)
}

func (c *Client) EditMessageRich(ctx context.Context, chatID int64, msgID int, msg RichMessage, markup *InlineKeyboard) error {
	m := map[string]any{"chat_id": chatID, "message_id": msgID, "rich_message": msg}
	if markup != nil {
		m["reply_markup"] = markup
	}
	return c.Call(ctx, "editMessageText", m, nil)
}

func (c *Client) CreateForumTopic(ctx context.Context, chatID int64, name string) (int, error) {
	var t struct {
		MessageThreadID int `json:"message_thread_id"`
	}
	err := c.Call(ctx, "createForumTopic", map[string]any{"chat_id": chatID, "name": name}, &t)
	return t.MessageThreadID, err
}

func (c *Client) EditForumTopic(ctx context.Context, chatID int64, thread int, name string) error {
	return c.Call(ctx, "editForumTopic", map[string]any{"chat_id": chatID, "message_thread_id": thread, "name": name}, nil)
}

// MarkTopicClosed renames a topic with a 🔒 prefix: closeForumTopic is rejected in private chats.
func (c *Client) MarkTopicClosed(ctx context.Context, chatID int64, thread int, title string) error {
	name := "🔒 " + title
	if r := []rune(name); len(r) > 128 {
		name = string(r[:128])
	}
	return c.EditForumTopic(ctx, chatID, thread, name)
}

func (c *Client) PinChatMessage(ctx context.Context, chatID int64, msgID int) error {
	return c.Call(ctx, "pinChatMessage", map[string]any{"chat_id": chatID, "message_id": msgID, "disable_notification": true}, nil)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	return c.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

func (c *Client) SetMessageReaction(ctx context.Context, chatID int64, msgID int, emoji string) error {
	r := []map[string]any{}
	if emoji != "" {
		r = append(r, map[string]any{"type": "emoji", "emoji": emoji})
	}
	return c.Call(ctx, "setMessageReaction", map[string]any{"chat_id": chatID, "message_id": msgID, "reaction": r}, nil)
}

func (c *Client) LeaveChat(ctx context.Context, chatID int64) error {
	return c.Call(ctx, "leaveChat", map[string]any{"chat_id": chatID}, nil)
}
