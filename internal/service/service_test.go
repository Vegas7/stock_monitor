package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/state"
)

// --- harness ---------------------------------------------------------------

type tgFake struct {
	mu   sync.Mutex
	msgs []string
	srv  *httptest.Server
}

func newTG(t *testing.T) *tgFake {
	f := &tgFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.msgs = append(f.msgs, b.Text)
		f.mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *tgFake) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.msgs...)
}

func (f *tgFake) count(prefix string) int {
	n := 0
	for _, m := range f.all() {
		if strings.HasPrefix(m, prefix) || strings.Contains(m, prefix) {
			n++
		}
	}
	return n
}

type counter struct {
	n   atomic.Int32
	srv *httptest.Server
}

func webServer(t *testing.T, body string) *counter {
	c := &counter{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.n.Add(1)
		io.WriteString(w, body)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

type harness struct {
	t          *testing.T
	dir        string
	cfgPath    string
	statePath  string
	tg         *tgFake
	opts       Options
	sleepsMu   sync.Mutex
	sleeps     []time.Duration
	now        atomic.Pointer[time.Time]
	cancel     context.CancelFunc
	done       chan error
}

func newHarness(t *testing.T, cfg *config.Config) *harness {
	h := &harness{t: t, dir: t.TempDir(), tg: newTG(t)}
	h.cfgPath = filepath.Join(h.dir, "config.json")
	h.statePath = filepath.Join(h.dir, "state.json")
	n := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC) // 12:00 Tokyo
	h.now.Store(&n)
	if cfg != nil {
		h.writeConfig(cfg)
	}
	h.opts = Options{
		ConfigPath: h.cfgPath, StatePath: h.statePath, Version: "v0.1.0",
		TelegramBaseURL: h.tg.srv.URL,
		Now:             func() time.Time { return *h.now.Load() },
		Sleep: func(ctx context.Context, d time.Duration) bool {
			h.sleepsMu.Lock()
			h.sleeps = append(h.sleeps, d)
			h.sleepsMu.Unlock()
			select {
			case <-ctx.Done():
				return false
			case <-time.After(2 * time.Millisecond):
				return true
			}
		},
		ReloadEvery: 10 * time.Millisecond,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return h
}

func (h *harness) writeConfig(c *config.Config) {
	h.t.Helper()
	if c.Telegram.BotToken == "" && c.Telegram.ChatID == "" && h.tg != nil {
		// leave unset: caller decides
	}
	if err := config.Save(h.cfgPath, c); err != nil {
		h.t.Fatal(err)
	}
	// force a visible mtime change for reload detection
	future := time.Now().Add(time.Duration(len(c.Items)+int(time.Now().UnixNano()%1000)) * time.Millisecond)
	os.Chtimes(h.cfgPath, future, future)
}

func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	go func() { h.done <- Run(ctx, h.opts) }()
}

func (h *harness) stop() error {
	h.cancel()
	select {
	case err := <-h.done:
		return err
	case <-time.After(5 * time.Second):
		h.t.Fatal("Run did not stop")
		return nil
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func baseConfig(tgOn bool) *config.Config {
	c := config.Default()
	if tgOn {
		c.Telegram = config.Telegram{BotToken: "111:TESTTOKENxxxxxxxx", ChatID: "42"}
	}
	return c
}

func webItem(id, url, keyword, means string) config.Item {
	return config.Item{ID: id, Name: "item " + id, Type: config.TypeWeb, Enabled: true,
		Web: &config.WebItem{URL: url, Match: "keyword", Value: keyword, PresentMeans: means}}
}

func readState(t *testing.T, p string) state.State {
	s, err := state.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return s.Snapshot()
}

// --- tests -----------------------------------------------------------------

func TestRunWithoutTelegram(t *testing.T) {
	web := webServer(t, "Sold out")
	c := baseConfig(false)
	c.Items = []config.Item{webItem("a", web.srv.URL, "sold out", "out_of_stock")}
	h := newHarness(t, c)
	h.start()
	eventually(t, "a check", func() bool { return web.n.Load() >= 2 })
	if err := h.stop(); err != nil {
		t.Fatal(err)
	}
	if st := readState(t, h.statePath); st.Items["a"] == nil || st.Items["a"].Stock != "out_of_stock" {
		t.Fatal(st.Items)
	}
	if len(h.tg.all()) != 0 {
		t.Fatal("no telegram configured, nothing should be sent")
	}
}

func TestStartupMessageAndRestartFlag(t *testing.T) {
	c := baseConfig(true)
	h := newHarness(t, c)
	h.start()
	eventually(t, "start msg", func() bool { return h.tg.count("stockmon started") == 1 })
	h.stop()
	if m := h.tg.all()[0]; m != "stockmon started v0.1.0: 0 items" {
		t.Fatalf("fresh start: %q", m)
	}
	// simulate crash: clean_shutdown false in existing state
	s, _ := state.Open(h.statePath)
	s.Update(func(st *state.State) { st.CleanShutdown = false })
	h.start()
	eventually(t, "2nd start msg", func() bool { return h.tg.count("stockmon started") == 2 })
	h.stop()
	if m := h.tg.all()[1]; !strings.HasSuffix(m, "(after a restart)") {
		t.Fatalf("restart: %q", m)
	}
}

func TestIntervalWithinBounds(t *testing.T) {
	web := webServer(t, "x")
	c := baseConfig(false)
	c.Items = []config.Item{webItem("a", web.srv.URL, "x", "in_stock")}
	h := newHarness(t, c)
	h.start()
	eventually(t, "checks", func() bool { return web.n.Load() >= 6 })
	h.stop()
	h.sleepsMu.Lock()
	defer h.sleepsMu.Unlock()
	inRange := 0
	for i, d := range h.sleeps {
		switch {
		case i == 0 && d <= 10*time.Second:
		case d >= 30*time.Second && d <= 60*time.Second:
			inRange++
		default:
			t.Fatalf("sleep %d out of bounds: %v", i, d)
		}
	}
	if inRange < 4 {
		t.Fatalf("only %d interval sleeps", inRange)
	}
}

func misakaFake(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/session/login":
			io.WriteString(w, `{"user":{"id":1},"access_token":"A","refresh_token":"R"}`)
		case "/api/mc2/regions":
			io.WriteString(w, `[{"id":11,"slug":"nrt04","available":true}]`)
		case "/api/mc2/regions/11/plans":
			io.WriteString(w, `[{"id":42,"slug":"s3n-1c1g","name":"S3N","available":true}]`)
		case "/api/mc2/images":
			io.WriteString(w, `[{"id":5,"slug":"debian-13"}]`)
		case "/api/mc2/instances/":
			posts.Add(1)
			io.WriteString(w, `{"orders":[1],"invoice_id":9}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &posts
}

func TestInStockTriggersSingleOrder(t *testing.T) {
	srv, posts := misakaFake(t)
	c := baseConfig(true)
	c.Misaka = config.MisakaAccount{Username: "u@example.com", Password: "pw-secret"}
	c.Items = []config.Item{{ID: "m", Name: "Tokyo", Type: config.TypeMisaka, Enabled: true,
		Misaka: &config.MisakaItem{Region: "nrt04", Plan: "s3n-1c1g", Image: "debian-13",
			AutoOrder: config.AutoOrder{Enabled: true, ArmID: "arm-x", BillingCycle: "monthly"}}}}
	h := newHarness(t, c)
	h.opts.MisakaBaseURL = srv.URL + "/api/"
	h.start()
	eventually(t, "order msg", func() bool { return h.tg.count("🛒 Order placed: ") == 1 })
	time.Sleep(100 * time.Millisecond) // several more in-stock checks
	h.stop()
	if posts.Load() != 1 {
		t.Fatalf("posts=%d", posts.Load())
	}
	if h.tg.count("🟢 IN STOCK: ") != 1 {
		t.Fatal(h.tg.all())
	}
}

func TestReloadRemovesAndRestartsItems(t *testing.T) {
	a := webServer(t, "x")
	b1 := webServer(t, "x")
	b2 := webServer(t, "x")
	c := baseConfig(true)
	c.Items = []config.Item{webItem("a", a.srv.URL, "x", "in_stock"), webItem("b", b1.srv.URL, "x", "in_stock")}
	h := newHarness(t, c)
	h.start()
	eventually(t, "both checked", func() bool { return a.n.Load() > 0 && b1.n.Load() > 0 })
	c.Items = []config.Item{webItem("b", b2.srv.URL, "x", "in_stock")}
	h.writeConfig(c)
	eventually(t, "b2 hit", func() bool { return b2.n.Load() > 1 })
	aAfter, b1After := a.n.Load(), b1.n.Load()
	time.Sleep(100 * time.Millisecond)
	h.stop()
	if a.n.Load() > aAfter+1 || b1.n.Load() > b1After+1 {
		t.Fatalf("old loops still running: a %d->%d b1 %d->%d", aAfter, a.n.Load(), b1After, b1.n.Load())
	}
	if h.tg.count("⚠️ Warning: ") != 0 {
		t.Fatal(h.tg.all())
	}
	if st := readState(t, h.statePath); st.Items["a"] != nil {
		t.Fatal("removed item state should be dropped")
	}
}

func TestInvalidReloadKeepsOldConfig(t *testing.T) {
	web := webServer(t, "x")
	c := baseConfig(true)
	c.Items = []config.Item{webItem("a", web.srv.URL, "x", "in_stock")}
	h := newHarness(t, c)
	h.start()
	eventually(t, "check", func() bool { return web.n.Load() > 0 })
	os.WriteFile(h.cfgPath, []byte("{broken"), 0o640)
	future := time.Now().Add(time.Hour)
	os.Chtimes(h.cfgPath, future, future)
	eventually(t, "reload warning", func() bool { return h.tg.count("config reload failed") == 1 })
	n := web.n.Load()
	eventually(t, "still checking", func() bool { return web.n.Load() > n+2 })
	h.stop()
	if h.tg.count("config reload failed") != 1 {
		t.Fatal("warn once per mtime")
	}
}

func TestHeartbeatOncePerDay(t *testing.T) {
	web := webServer(t, "x")
	c := baseConfig(true)
	c.Items = []config.Item{webItem("a", web.srv.URL, "x", "in_stock")}
	h := newHarness(t, c)
	nine := time.Date(2026, 11, 21, 0, 5, 0, 0, time.UTC) // 09:05 Tokyo
	h.now.Store(&nine)
	var latestCalls atomic.Int32
	h.opts.Latest = func(ctx context.Context) (string, error) { latestCalls.Add(1); return "v9.9.9", nil }
	h.start()
	eventually(t, "heartbeat", func() bool { return h.tg.count("💓 stockmon daily: ") == 1 })
	time.Sleep(80 * time.Millisecond)
	h.stop()
	if h.tg.count("💓 stockmon daily: ") != 1 || latestCalls.Load() != 1 {
		t.Fatal(h.tg.all(), latestCalls.Load())
	}
	var hb string
	for _, m := range h.tg.all() {
		if strings.HasPrefix(m, "💓") {
			hb = m
		}
	}
	if !strings.Contains(hb, "item a: ") || !strings.Contains(hb, "update available: v0.1.0 → v9.9.9, run sudo stockmon update") {
		t.Fatal(hb)
	}
}

func TestCleanShutdownPersisted(t *testing.T) {
	h := newHarness(t, baseConfig(false))
	h.start()
	eventually(t, "state file", func() bool { _, err := os.Stat(h.statePath); return err == nil })
	h.stop()
	if !readState(t, h.statePath).CleanShutdown {
		t.Fatal("clean_shutdown should be true")
	}
}

func TestMemoryBudget(t *testing.T) {
	if raceEnabled {
		t.Skip("race detector inflates memory")
	}
	// Same limit the systemd unit sets (GOMEMLIMIT=24MiB). Note: this process also
	// hosts the fake web server, so the real service uses less than measured here.
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(24 << 20))
	web := webServer(t, strings.Repeat("<p>filler</p>", 20000)+"Sold out")
	c := baseConfig(false)
	for i := 0; i < 10; i++ {
		id := string(rune('a' + i))
		c.Items = append(c.Items, webItem(id, web.srv.URL, "sold out", "out_of_stock"))
	}
	h := newHarness(t, c)
	h.start()
	eventually(t, "many checks", func() bool { return web.n.Load() > 100 })
	h.stop()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	// Sys includes heap already returned to the OS; resident ≈ Sys - HeapReleased.
	resident := ms.Sys - ms.HeapReleased
	t.Logf("resident≈%dMB heapInuse=%dMB", resident>>20, ms.HeapInuse>>20)
	if resident > 20<<20 || ms.HeapInuse > 8<<20 {
		t.Fatalf("resident≈%d MB, heap in use %d MB", resident>>20, ms.HeapInuse>>20)
	}
}
