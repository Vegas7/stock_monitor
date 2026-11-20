package order

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
	"github.com/Vegas7/stock_monitor/internal/state"
)

type env struct {
	cl        *misaka.Client
	store     *state.Store
	statePath string
	posts     atomic.Int32
	lastBody  map[string]any
	orderH    http.HandlerFunc
}

func newEnv(t *testing.T, orderH http.HandlerFunc) *env {
	e := &env{orderH: orderH}
	e.statePath = filepath.Join(t.TempDir(), "state.json")
	e.store, _ = state.Open(e.statePath)
	e.store.SaveTokens(misaka.Tokens{Access: "A"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/mc2/regions":
			io.WriteString(w, `[{"id":11,"slug":"nrt04","available":true}]`)
		case "/api/mc2/regions/11/plans":
			io.WriteString(w, `[{"id":42,"slug":"s3n-1c1g","name":"S3N 1C1G","available":true}]`)
		case "/api/mc2/images":
			io.WriteString(w, `[{"id":"img-d13","slug":"debian-13"}]`)
		case "/api/mc2/instances/":
			e.posts.Add(1)
			json.NewDecoder(r.Body).Decode(&e.lastBody)
			e.orderH(w, r)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	hc := &http.Client{Timeout: 200 * time.Millisecond}
	e.cl = misaka.New(srv.URL+"/api/", "u", "pass", e.store, hc)
	return e
}

func ok(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }
}

func armedItem() config.Item {
	return config.Item{ID: "m1", Name: "Tokyo 1C1G", Type: config.TypeMisaka, Enabled: true,
		Misaka: &config.MisakaItem{Region: "nrt04", Plan: "s3n-1c1g", Image: "debian-13",
			AutoOrder: config.AutoOrder{Enabled: true, ArmID: "arm-1", BillingCycle: "monthly", SSHKeyIDs: []string{"7"}, Hostname: "tokyo-1"}}}
}

var ctx = context.Background()

func TestArmed(t *testing.T) {
	st := state.State{}
	it := armedItem()
	if !Armed(it, st) {
		t.Fatal("fresh arm should be armed")
	}
	web := config.Item{Type: config.TypeWeb, Web: &config.WebItem{}}
	if Armed(web, st) {
		t.Fatal("web")
	}
	off := armedItem()
	off.Misaka.AutoOrder.Enabled = false
	if Armed(off, st) {
		t.Fatal("disabled")
	}
	st.UsedArmIDs = []string{"arm-1"}
	if Armed(it, st) {
		t.Fatal("used arm")
	}
}

func TestPlaceSuccess(t *testing.T) {
	e := newEnv(t, ok(`{"orders":[901],"invoice_id":555}`))
	msg := Place(ctx, e.cl, e.store, armedItem(), nil)
	if !strings.HasPrefix(msg, "🛒 Order placed: Tokyo 1C1G") || !strings.Contains(msg, "https://app.misaka.io/billing/invoice/555") {
		t.Fatal(msg)
	}
	snap := e.store.Snapshot()
	if len(snap.Orders) != 1 || snap.Orders[0].Status != state.OrderPlaced || snap.Orders[0].InvoiceID != "555" || !snap.ArmUsed("arm-1") {
		t.Fatal(snap.Orders)
	}
	if e.posts.Load() != 1 {
		t.Fatal(e.posts.Load())
	}
}

func TestPlaceNoInvoiceUsesWaitPage(t *testing.T) {
	e := newEnv(t, ok(`{"orders":[901,902]}`))
	msg := Place(ctx, e.cl, e.store, armedItem(), nil)
	if !strings.Contains(msg, "https://app.misaka.io/iaas/vm/create/wait/901,902") {
		t.Fatal(msg)
	}
}

func TestPlaceSendsIssueInvoiceAndCountOne(t *testing.T) {
	e := newEnv(t, ok(`{"orders":[1]}`))
	Place(ctx, e.cl, e.store, armedItem(), nil)
	b := e.lastBody
	if b["issue_invoice"] != true || b["count"].(float64) != 1 || b["region"].(float64) != 11 || b["plan"].(float64) != 42 ||
		b["image"] != "img-d13" || b["billing_cycle"] != "monthly" {
		t.Fatal(b)
	}
	if cfg := b["config"].([]any)[0].(map[string]any); cfg["name"] != "tokyo-1" {
		t.Fatal(cfg)
	}
}

func TestDefaultHostname(t *testing.T) {
	e := newEnv(t, ok(`{"orders":[1]}`))
	it := armedItem()
	it.Misaka.AutoOrder.Hostname = ""
	Place(ctx, e.cl, e.store, it, nil)
	if cfg := e.lastBody["config"].([]any)[0].(map[string]any); cfg["name"] != "s3n-1c1g-1" {
		t.Fatal(cfg)
	}
}

func TestPlaceTwiceSameArmOrdersOnce(t *testing.T) {
	e := newEnv(t, ok(`{"orders":[1]}`))
	it := armedItem()
	Place(ctx, e.cl, e.store, it, nil)
	if Armed(it, e.store.Snapshot()) {
		t.Fatal("still armed after placing")
	}
}

func TestPlaceTimeoutNoRetry(t *testing.T) {
	e := newEnv(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(500 * time.Millisecond) })
	msg := Place(ctx, e.cl, e.store, armedItem(), nil)
	if e.posts.Load() != 1 || !strings.HasPrefix(msg, "🚨 Order problem: ") || !strings.Contains(msg, "status unknown") {
		t.Fatal(e.posts.Load(), msg)
	}
	if e.store.Snapshot().Orders[0].Status != state.OrderUnknown {
		t.Fatal("want unknown")
	}
}

func TestPlace5xxNoRetry(t *testing.T) {
	e := newEnv(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) })
	msg := Place(ctx, e.cl, e.store, armedItem(), nil)
	if e.posts.Load() != 1 || !strings.Contains(msg, "status unknown") {
		t.Fatal(msg)
	}
}

func TestPlaceClearErrorFailed(t *testing.T) {
	e := newEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"detail":"Plan unavailable"}`)
	})
	msg := Place(ctx, e.cl, e.store, armedItem(), nil)
	if !strings.Contains(msg, "Plan unavailable") || !strings.Contains(msg, "Re-arm in the menu") {
		t.Fatal(msg)
	}
	if e.store.Snapshot().Orders[0].Status != state.OrderFailed {
		t.Fatal("want failed")
	}
}

func TestPlacingWrittenBeforePOST(t *testing.T) {
	var sawPlacing bool
	var e *env
	e = newEnv(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := os.ReadFile(e.statePath)
		var st state.State
		json.Unmarshal(b, &st)
		sawPlacing = len(st.Orders) == 1 && st.Orders[0].Status == state.OrderPlacing && st.ArmUsed("arm-1")
		io.WriteString(w, `{"orders":[1]}`)
	})
	Place(ctx, e.cl, e.store, armedItem(), nil)
	if !sawPlacing {
		t.Fatal("placing record must be on disk before the POST")
	}
}

func TestRecoverPending(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	store, _ := state.Open(p)
	store.Update(func(s *state.State) {
		s.Orders = []state.OrderRecord{{ItemID: "m1", ArmID: "a", Status: state.OrderPlacing}, {ItemID: "m2", Status: state.OrderPlaced}}
	})
	msgs := RecoverPending(store)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "status unknown") {
		t.Fatal(msgs)
	}
	if store.Snapshot().Orders[0].Status != state.OrderUnknown || store.Snapshot().Orders[1].Status != state.OrderPlaced {
		t.Fatal("statuses")
	}
}

func TestResolveFailureKeepsArmAndSendsNothing(t *testing.T) {
	e := newEnv(t, ok(`{"orders":[1]}`))
	it := armedItem()
	it.Misaka.Image = "no-such-image"
	msg := Place(ctx, e.cl, e.store, it, nil)
	if e.posts.Load() != 0 || !strings.Contains(msg, "still armed") {
		t.Fatal(e.posts.Load(), msg)
	}
	snap := e.store.Snapshot()
	if snap.ArmUsed("arm-1") || len(snap.Orders) != 0 || !Armed(it, snap) {
		t.Fatal("arm must not be spent when no order was sent", snap)
	}
}

func TestEmptyOrderResponseIsUnknown(t *testing.T) {
	e := newEnv(t, ok(`{}`))
	msg := Place(ctx, e.cl, e.store, armedItem(), nil)
	if !strings.Contains(msg, "status unknown") || strings.Contains(msg, "Order placed") {
		t.Fatal(msg)
	}
}
