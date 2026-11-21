package menu

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vegas7/stock_monitor/internal/config"
)

func tgCfg() *config.Config {
	c := misakaCfg()
	c.Telegram = config.Telegram{BotToken: "1:TOKENxyz", ChatID: "555"}
	return c
}

func (e *env) sent() string {
	e.tgMu.Lock()
	defer e.tgMu.Unlock()
	return strings.Join(e.tgSent, "\n---\n")
}

func TestAddItemSendsTelegramNotice(t *testing.T) {
	e := newEnv(t)
	e.telegramFake()
	e.save(tgCfg())
	url := page(t, "<p>Sold out</p>")
	e.run("2", "2", "Widget", url, "1", "Sold out", "2", "0")
	if s := e.sent(); !strings.Contains(s, "555:") || !strings.Contains(s, "Item added") || !strings.Contains(s, "Widget") {
		t.Fatalf("got %q", s)
	}
}

func TestArmAndDisarmSendTelegramNotice(t *testing.T) {
	e := newEnv(t)
	e.telegramFake()
	e.save(tgCfg())
	e.run("7", "1", "2", "yes", "3", "0", "0")
	s := e.sent()
	if !strings.Contains(s, "Auto-order ARMED") || !strings.Contains(s, "Tokyo") || !strings.Contains(s, "Auto-order disarmed") {
		t.Fatalf("got %q", s)
	}
}

func TestNotArmingSendsNothing(t *testing.T) {
	e := newEnv(t)
	e.telegramFake()
	e.save(tgCfg())
	e.run("7", "1", "2", "no", "0", "0")
	if s := e.sent(); s != "" {
		t.Fatalf("nothing changed, nothing should be sent; got %q", s)
	}
}

func TestAutoOrderSettingsSendTelegramNotice(t *testing.T) {
	e := newEnv(t)
	e.misakaFake(true)
	e.telegramFake()
	c := tgCfg()
	c.Misaka = config.MisakaAccount{Username: "u@example.com", Password: "pw-secret"}
	e.save(c)
	e.run("7", "1", "1", "1", "1", "tokyo-a", "", "0", "0")
	s := e.sent()
	if !strings.Contains(s, "Auto-order settings changed") || !strings.Contains(s, "Tokyo") || !strings.Contains(s, "tokyo-a") {
		t.Fatalf("got %q", s)
	}
	if strings.Contains(s, "pw-secret") || strings.Contains(s, "TOKENxyz") {
		t.Fatalf("secret leaked: %q", s)
	}
}

func TestEditItemSendsTelegramNotice(t *testing.T) {
	e := newEnv(t)
	e.telegramFake()
	e.save(tgCfg())
	e.run("3", "1", "1", "0") // disable
	e.run("3", "1", "2", "Osaka", "0")
	e.run("3", "1", "3", "y", "0") // remove
	s := e.sent()
	for _, want := range []string{"Item disabled: Tokyo", "Item renamed: Tokyo → Osaka", "Item removed: Osaka"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %q", want, s)
		}
	}
}

func TestTelegramNoticeFailureKeepsTheSave(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(srv.Close)
	e.opts.TelegramBaseURL = srv.URL
	e.save(tgCfg())
	out := e.run("3", "1", "1", "0")
	if e.cfg().Items[0].Enabled {
		t.Fatal("the change must be saved even when Telegram fails")
	}
	if !strings.Contains(out, "Telegram notice not sent") || strings.Contains(out, "TOKENxyz") {
		t.Fatalf("%q", out)
	}
}
