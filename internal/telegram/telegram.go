// Package telegram is a minimal Telegram Bot API client.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Vegas7/stock_monitor/internal/redact"
)

// MaxText is the longest text sent (Telegram's hard limit is 4096).
const MaxText = 3500

// Notifier sends one message to a fixed chat.
type Notifier interface {
	Notify(ctx context.Context, text string) error
}

type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
	Backoff []time.Duration
}

// Chat is a chat that has messaged the bot.
type Chat struct {
	ID   string
	Name string
}

func New(token string) *Client {
	return &Client{
		Token:   token,
		BaseURL: "https://api.telegram.org",
		HTTP:    &http.Client{Timeout: 20 * time.Second},
		Backoff: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
	}
}

type apiResp struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

type statusErr struct {
	code int
	msg  string
}

func (e *statusErr) Error() string { return fmt.Sprintf("telegram: HTTP %d: %s", e.code, e.msg) }

func (c *Client) call(ctx context.Context, method string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/bot"+c.Token+"/"+method, rdr)
	if err != nil {
		return c.clean(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return c.clean(err)
	}
	defer resp.Body.Close()
	var r apiResp
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(data, &r)
	if resp.StatusCode != 200 || !r.OK {
		msg := r.Description
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return c.clean(&statusErr{resp.StatusCode, msg})
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// clean removes the bot token from error text (url.Error embeds the URL).
func (c *Client) clean(err error) error {
	var se *statusErr
	if errors.As(err, &se) {
		return &statusErr{se.code, redact.New(c.Token).String(se.msg)}
	}
	return errors.New(redact.New(c.Token).Err(err))
}

func retryable(err error) bool {
	var se *statusErr
	if errors.As(err, &se) {
		return se.code >= 500 || se.code == 429
	}
	return true // network error
}

// GetMe validates the token and returns the bot's username.
func (c *Client) GetMe(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := c.call(ctx, "getMe", nil, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}

// Truncate cuts text to MaxText runes, ending with "…".
func Truncate(s string) string {
	if utf8.RuneCountInString(s) <= MaxText {
		return s
	}
	r := []rune(s)
	return string(r[:MaxText-1]) + "…"
}

// Send posts a plain-text message, retrying network errors, 5xx and 429.
func (c *Client) Send(ctx context.Context, chatID, text string) error {
	body := map[string]any{"chat_id": chatID, "text": Truncate(text), "disable_web_page_preview": true}
	var err error
	for attempt := 0; ; attempt++ {
		err = c.call(ctx, "sendMessage", body, nil)
		if err == nil || !retryable(err) || attempt >= len(c.Backoff) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(c.Backoff[attempt]):
		}
	}
}

// Chats lists distinct chats found in pending updates.
func (c *Client) Chats(ctx context.Context) ([]Chat, error) {
	var ups []struct {
		Message *struct {
			Chat struct {
				ID        int64  `json:"id"`
				Title     string `json:"title"`
				FirstName string `json:"first_name"`
				Username  string `json:"username"`
			} `json:"chat"`
		} `json:"message"`
	}
	if err := c.call(ctx, "getUpdates", nil, &ups); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Chat
	for _, u := range ups {
		if u.Message == nil {
			continue
		}
		ch := u.Message.Chat
		id := strconv.FormatInt(ch.ID, 10)
		if seen[id] {
			continue
		}
		seen[id] = true
		name := ch.Title
		if name == "" {
			name = ch.FirstName
		}
		if ch.Username != "" {
			name += " (@" + ch.Username + ")"
		}
		out = append(out, Chat{ID: id, Name: name})
	}
	return out, nil
}

type chatNotifier struct {
	c    *Client
	chat string
}

func (n chatNotifier) Notify(ctx context.Context, text string) error { return n.c.Send(ctx, n.chat, text) }

// For returns a Notifier bound to chatID.
func (c *Client) For(chatID string) Notifier { return chatNotifier{c, chatID} }
