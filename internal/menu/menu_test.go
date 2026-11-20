package menu

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Vegas7/stock_monitor/internal/config"
)

type env struct {
	t       *testing.T
	dir     string
	cfgPath string
	out     bytes.Buffer
	secrets []string
	opts    Options
	tgSent  []string
	tgMu    sync.Mutex
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, dir: t.TempDir()}
	e.cfgPath = filepath.Join(e.dir, "config.json")
	e.opts = Options{
		ConfigPath: e.cfgPath, StatePath: filepath.Join(e.dir, "state.json"), Version: "v0.1.0",
		Out: &e.out,
		ReadSecret: func(prompt string) (string, error) {
			if len(e.secrets) == 0 {
				return "", io.EOF
			}
			s := e.secrets[0]
			e.secrets = e.secrets[1:]
			return s, nil
		},
		ServiceStatus: func() string { return "inactive" },
	}
	return e
}

func (e *env) run(lines ...string) string {
	e.t.Helper()
	e.opts.In = strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := Run(context.Background(), e.opts); err != nil {
		e.t.Fatal(err)
	}
	return e.out.String()
}

func (e *env) cfg() *config.Config {
	e.t.Helper()
	c, err := config.Load(e.cfgPath)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) save(c *config.Config) {
	if err := config.Save(e.cfgPath, c); err != nil {
		e.t.Fatal(err)
	}
}

func page(t *testing.T, body string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (e *env) telegramFake() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"username":"my_stock_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			io.WriteString(w, `{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":98765,"first_name":"V"}}}]}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var b struct {
				ChatID string `json:"chat_id"`
				Text   string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			e.tgMu.Lock()
			e.tgSent = append(e.tgSent, b.ChatID+":"+b.Text)
			e.tgMu.Unlock()
			io.WriteString(w, `{"ok":true,"result":{}}`)
		}
	}))
	e.t.Cleanup(srv.Close)
	e.opts.TelegramBaseURL = srv.URL
}

func (e *env) misakaFake(loginOK bool) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/login":
			if !loginOK {
				w.WriteHeader(400)
				io.WriteString(w, `{"detail":"Invalid credentials"}`)
				return
			}
			io.WriteString(w, `{"user":{"id":1},"access_token":"ACCESS-TOKEN-XYZ","refresh_token":"REFRESH-TOKEN-XYZ"}`)
		case "/api/mc2/regions":
			io.WriteString(w, `[{"id":11,"slug":"nrt04","available":true}]`)
		case "/api/mc2/regions/11/plans":
			io.WriteString(w, `[{"id":42,"slug":"s3n-1c1g","name":"S3N 1C1G","available":false,"unavailable_reason":"Out of stock","price_monthly":10}]`)
		case "/api/mc2/sshkeys/":
			io.WriteString(w, `[{"id":7,"name":"laptop"}]`)
		default:
			w.WriteHeader(404)
		}
	}))
	e.t.Cleanup(srv.Close)
	e.opts.MisakaBaseURL = srv.URL + "/api/"
}

// --- tests -----------------------------------------------------------------

func TestMissingConfigStartsFromDefault(t *testing.T) {
	e := newEnv(t)
	out := e.run("0")
	if !strings.Contains(out, "stockmon v0.1.0 — service: inactive — 0 items") || !strings.Contains(out, "9) Update / uninstall") {
		t.Fatal(out)
	}
}

func TestAddWebItemSaves(t *testing.T) {
	e := newEnv(t)
	url := page(t, "<p>Sold out</p>")
	// 2=add, 2=web, name, url, 1=keyword, value, 2=present means out of stock
	out := e.run("2", "2", "Widget", url, "1", "Sold out", "2", "0")
	c := e.cfg()
	if len(c.Items) != 1 || c.Items[0].Web.Value != "Sold out" || c.Items[0].Web.PresentMeans != "out_of_stock" || !c.Items[0].Enabled {
		t.Fatal(c.Items)
	}
	if !strings.Contains(out, "HTTP 200") || !strings.Contains(out, "found") {
		t.Fatal(out)
	}
}

func TestAddWebItemWarnsWhenInStockOnAdd(t *testing.T) {
	e := newEnv(t)
	url := page(t, "<div id=app></div>") // JS shell: "Sold out" never in raw HTML
	out := e.run("2", "2", "JS shop", url, "1", "Sold out", "2", "n", "0")
	if !strings.Contains(out, "reads IN STOCK") {
		t.Fatal(out)
	}
	if c, err := config.Load(e.cfgPath); err == nil && len(c.Items) != 0 {
		t.Fatal("should not be saved")
	}
}

func TestAddMisakaItemFromURL(t *testing.T) {
	e := newEnv(t)
	out := e.run("2", "1", "https://app.misaka.io/iaas/vm/create/nrt04/s3n-1c1g/debian-13", "", "0")
	c := e.cfg()
	m := c.Items[0].Misaka
	if m == nil || m.Region != "nrt04" || m.Plan != "s3n-1c1g" || m.Image != "debian-13" || m.AutoOrder.Enabled {
		t.Fatal(c.Items)
	}
	if !strings.Contains(out, "Misaka login") {
		t.Fatal("should hint to set login:", out)
	}
}

func TestAddMisakaItemLiveTest(t *testing.T) {
	e := newEnv(t)
	e.misakaFake(true)
	c := config.Default()
	c.Misaka = config.MisakaAccount{Username: "u@example.com", Password: "pw-secret"}
	e.save(c)
	out := e.run("2", "1", "https://app.misaka.io/iaas/vm/create/nrt04/s3n-1c1g/debian-13", "", "0")
	if !strings.Contains(out, "OUT OF STOCK") || !strings.Contains(out, "Out of stock") {
		t.Fatal(out)
	}
}

func TestTelegramSetupFindsChatID(t *testing.T) {
	e := newEnv(t)
	e.telegramFake()
	e.secrets = []string{"123:BOTTOKENabcdefg"}
	out := e.run("5", "", "0")
	c := e.cfg()
	if c.Telegram.BotToken != "123:BOTTOKENabcdefg" || c.Telegram.ChatID != "98765" {
		t.Fatal(c.Telegram)
	}
	if len(e.tgSent) != 1 || !strings.HasPrefix(e.tgSent[0], "98765:") {
		t.Fatal(e.tgSent)
	}
	if !strings.Contains(out, "@my_stock_bot") || strings.Contains(out, "BOTTOKEN") {
		t.Fatal(out)
	}
}

func TestMisakaLoginTestNeverPrintsToken(t *testing.T) {
	e := newEnv(t)
	e.misakaFake(true)
	e.secrets = []string{"pw-secret"}
	out := e.run("6", "u@example.com", "0")
	if !strings.Contains(out, "Login OK") || strings.Contains(out, "ACCESS-TOKEN") || strings.Contains(out, "pw-secret") {
		t.Fatal(out)
	}
	if c := e.cfg(); c.Misaka.Username != "u@example.com" || c.Misaka.Password != "pw-secret" {
		t.Fatal(c.Misaka)
	}
}

func TestMisakaLoginFailureShown(t *testing.T) {
	e := newEnv(t)
	e.misakaFake(false)
	e.secrets = []string{"pw-secret"}
	out := e.run("6", "u@example.com", "0")
	if !strings.Contains(out, "Login failed") || !strings.Contains(out, "Invalid credentials") {
		t.Fatal(out)
	}
}

func misakaCfg() *config.Config {
	c := config.Default()
	c.Items = []config.Item{{ID: "m1", Name: "Tokyo", Type: config.TypeMisaka, Enabled: true,
		Misaka: &config.MisakaItem{Region: "nrt04", Plan: "s3n-1c1g", Image: "debian-13", AutoOrder: config.AutoOrder{BillingCycle: "monthly"}}}}
	return c
}

func TestArmRequiresYes(t *testing.T) {
	e := newEnv(t)
	e.save(misakaCfg())
	e.run("7", "1", "2", "y", "0", "0")
	if e.cfg().Items[0].Misaka.AutoOrder.Enabled {
		t.Fatal("'y' must not arm")
	}
	out := e.run("7", "1", "2", "yes", "0", "0")
	ao := e.cfg().Items[0].Misaka.AutoOrder
	if !ao.Enabled || len(ao.ArmID) != 16 {
		t.Fatal(ao)
	}
	if !strings.Contains(out, "place ONE order (unpaid invoice) and then disarm") {
		t.Fatal(out)
	}
}

func TestRearmChangesArmID(t *testing.T) {
	e := newEnv(t)
	e.save(misakaCfg())
	e.run("7", "1", "2", "yes", "0", "0")
	first := e.cfg().Items[0].Misaka.AutoOrder.ArmID
	e.run("7", "1", "3", "0", "0") // disarm
	if e.cfg().Items[0].Misaka.AutoOrder.Enabled {
		t.Fatal("disarm")
	}
	e.run("7", "1", "2", "yes", "0", "0")
	if second := e.cfg().Items[0].Misaka.AutoOrder.ArmID; second == first || second == "" {
		t.Fatal(first, second)
	}
}

func TestAutoOrderSettings(t *testing.T) {
	e := newEnv(t)
	e.misakaFake(true)
	c := misakaCfg()
	c.Misaka = config.MisakaAccount{Username: "u@example.com", Password: "pw-secret"}
	e.save(c)
	// 7 → item 1 → 1 settings → cycle 1 (monthly) → keys "1" → hostname → coupon blank
	e.run("7", "1", "1", "1", "1", "tokyo-a", "", "0", "0")
	ao := e.cfg().Items[0].Misaka.AutoOrder
	if ao.BillingCycle != "monthly" || len(ao.SSHKeyIDs) != 1 || ao.SSHKeyIDs[0] != "7" || ao.Hostname != "tokyo-a" {
		t.Fatal(ao)
	}
}

func TestRemoveItem(t *testing.T) {
	e := newEnv(t)
	e.save(misakaCfg())
	e.run("3", "1", "3", "y", "0")
	if len(e.cfg().Items) != 0 {
		t.Fatal("not removed")
	}
}

func TestToggleItem(t *testing.T) {
	e := newEnv(t)
	e.save(misakaCfg())
	e.run("3", "1", "1", "0")
	if e.cfg().Items[0].Enabled {
		t.Fatal("not disabled")
	}
}

func TestSettingsRejectsBadInterval(t *testing.T) {
	e := newEnv(t)
	e.save(config.Default())
	out := e.run("8", "10", "", "", "", "", "", "0")
	if !strings.Contains(out, "not saved") || e.cfg().Settings.IntervalMinSec != 30 {
		t.Fatal(out)
	}
}

func TestUpdateEntryCallsHook(t *testing.T) {
	e := newEnv(t)
	called := false
	e.opts.Update = func(ctx context.Context) error { called = true; return nil }
	e.run("9", "1", "0", "0")
	if !called {
		t.Fatal("update not called")
	}
}

func TestAddWebItemFoundInStockNoJSWarning(t *testing.T) {
	e := newEnv(t)
	url := page(t, "<button>Add to cart</button>")
	out := e.run("2", "2", "Cart", url, "1", "Add to cart", "1", "0")
	if strings.Contains(out, "reads IN STOCK") {
		t.Fatal(out)
	}
	if len(e.cfg().Items) != 1 {
		t.Fatal("should be saved without asking")
	}
}
