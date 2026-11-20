package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

const tok = "123456:SECRETTOKENabcdefghijkl"

func newTest(h http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(h)
	c := New(tok)
	c.BaseURL = srv.URL
	c.Backoff = []time.Duration{0, 0, 0}
	return c, srv
}

func TestGetMe(t *testing.T) {
	c, srv := newTest(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+tok+"/getMe" {
			t.Errorf("path %s", r.URL.Path)
		}
		io.WriteString(w, `{"ok":true,"result":{"username":"x_bot"}}`)
	})
	defer srv.Close()
	u, err := c.GetMe(context.Background())
	if err != nil || u != "x_bot" {
		t.Fatal(u, err)
	}
}

func TestSendRetriesThenSucceeds(t *testing.T) {
	var n atomic.Int32
	c, srv := newTest(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(500)
			io.WriteString(w, `{"ok":false,"description":"boom"}`)
			return
		}
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	defer srv.Close()
	if err := c.Send(context.Background(), "42", "hi"); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 {
		t.Fatalf("requests %d", n.Load())
	}
}

func TestSendNoRetryOn400(t *testing.T) {
	var n atomic.Int32
	c, srv := newTest(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(400)
		io.WriteString(w, `{"ok":false,"description":"Bad Request: chat not found"}`)
	})
	defer srv.Close()
	err := c.Send(context.Background(), "42", "hi")
	if err == nil || !strings.Contains(err.Error(), "chat not found") || n.Load() != 1 {
		t.Fatal(err, n.Load())
	}
}

func TestSendTruncates(t *testing.T) {
	var got string
	c, srv := newTest(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		got = body.Text
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	defer srv.Close()
	c.Send(context.Background(), "42", strings.Repeat("あ", 5000))
	if utf8.RuneCountInString(got) > 3500 || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d", utf8.RuneCountInString(got))
	}
}

func TestErrorsNeverContainToken(t *testing.T) {
	c, srv := newTest(func(w http.ResponseWriter, r *http.Request) {})
	srv.Close()
	err := c.Send(context.Background(), "42", "hi")
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Fatal(err)
	}
	_, err = c.GetMe(context.Background())
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Fatal(err)
	}
}

func TestChatsFromUpdates(t *testing.T) {
	c, srv := newTest(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true,"result":[
		 {"update_id":1,"message":{"chat":{"id":12345,"first_name":"V","type":"private"}}},
		 {"update_id":2,"message":{"chat":{"id":12345,"first_name":"V","type":"private"}}}]}`)
	})
	defer srv.Close()
	chats, err := c.Chats(context.Background())
	if err != nil || len(chats) != 1 || chats[0].ID != "12345" {
		t.Fatal(chats, err)
	}
}

func TestNotifierFor(t *testing.T) {
	var chat string
	c, srv := newTest(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		chat, _ = body["chat_id"].(string)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	})
	defer srv.Close()
	var n Notifier = c.For("777")
	if err := n.Notify(context.Background(), "x"); err != nil || chat != "777" {
		t.Fatal(err, chat)
	}
}
