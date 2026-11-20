package monitor

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Vegas7/stock_monitor/internal/check"
	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/redact"
	"github.com/Vegas7/stock_monitor/internal/state"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func settings() config.Settings { return config.Default().Settings }

func webItem() config.Item {
	return config.Item{ID: "w", Name: "Shop thing", Type: config.TypeWeb,
		Web: &config.WebItem{URL: "https://shop.example/p"}}
}

func misakaItem() config.Item {
	return config.Item{ID: "m", Name: "Tokyo 1C1G", Type: config.TypeMisaka,
		Misaka: &config.MisakaItem{Region: "nrt04", Plan: "s3n-1c1g", Image: "debian-13"}}
}

var ok = func(s check.Status) check.Result { return check.Result{Status: s, Detail: "d"} }
var netErr = errors.New("dial tcp: connection refused")

func apply(prev state.ItemState, res check.Result, err error, now time.Time) (state.ItemState, []string, bool) {
	return Apply(prev, webItem(), res, err, now, settings(), redact.New("hunter22"))
}

func TestFirstCheckOutOfStockIsSilent(t *testing.T) {
	next, msgs, became := apply(state.ItemState{}, ok(check.OutOfStock), nil, t0)
	if len(msgs) != 0 || became || next.Stock != check.OutOfStock || next.LastCheckAt != t0 {
		t.Fatal(next, msgs)
	}
}

func TestFirstCheckInStockAlerts(t *testing.T) {
	_, msgs, became := apply(state.ItemState{}, ok(check.InStock), nil, t0)
	if !became || len(msgs) != 1 || !strings.HasPrefix(msgs[0], "🟢 IN STOCK: Shop thing") || !strings.Contains(msgs[0], "https://shop.example/p") {
		t.Fatal(msgs)
	}
}

func TestMisakaLinkInMessage(t *testing.T) {
	_, msgs, _ := Apply(state.ItemState{}, misakaItem(), ok(check.InStock), nil, t0, settings(), nil)
	if !strings.Contains(msgs[0], "https://app.misaka.io/iaas/vm/create/nrt04/s3n-1c1g/debian-13") {
		t.Fatal(msgs)
	}
}

func TestInToOutAlerts(t *testing.T) {
	next, msgs, became := apply(state.ItemState{Stock: check.InStock}, ok(check.OutOfStock), nil, t0)
	if became || len(msgs) != 1 || !strings.HasPrefix(msgs[0], "⚪ Sold out again: Shop thing") || next.LastChangeAt != t0 {
		t.Fatal(msgs)
	}
}

func TestOutToOutSilent(t *testing.T) {
	_, msgs, _ := apply(state.ItemState{Stock: check.OutOfStock}, ok(check.OutOfStock), nil, t0)
	if len(msgs) != 0 {
		t.Fatal(msgs)
	}
}

func TestInToInSilent(t *testing.T) {
	_, msgs, became := apply(state.ItemState{Stock: check.InStock}, ok(check.InStock), nil, t0)
	if len(msgs) != 0 || became {
		t.Fatal(msgs)
	}
}

func TestWarnAfterThreeFailures(t *testing.T) {
	st := state.ItemState{Stock: check.OutOfStock}
	for i := 1; i <= 3; i++ {
		var msgs []string
		st, msgs, _ = apply(st, check.Result{}, netErr, t0)
		if i < 3 && len(msgs) != 0 {
			t.Fatalf("fail %d: %v", i, msgs)
		}
		if i == 3 && (len(msgs) != 1 || !strings.HasPrefix(msgs[0], "⚠️ Warning: Shop thing")) {
			t.Fatalf("fail 3: %v", msgs)
		}
	}
	if !st.Warned || st.ConsecutiveFails != 3 || st.LastWarnAt != t0 {
		t.Fatal(st)
	}
	// 4th failure right after: no new message
	_, msgs, _ := apply(st, check.Result{}, netErr, t0.Add(time.Minute))
	if len(msgs) != 0 {
		t.Fatal(msgs)
	}
}

func TestUrgentWarnsImmediately(t *testing.T) {
	_, msgs, _ := apply(state.ItemState{}, check.Result{}, fmt.Errorf("plan gone: %w", check.ErrFormatChanged), t0)
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "⚠️ Warning: ") {
		t.Fatal(msgs)
	}
}

func TestReminderAfterRemindInterval(t *testing.T) {
	st := state.ItemState{Warned: true, LastWarnAt: t0, ConsecutiveFails: 5}
	st2, msgs, _ := apply(st, check.Result{}, netErr, t0.Add(59*time.Minute))
	if len(msgs) != 0 {
		t.Fatal(msgs)
	}
	_, msgs, _ = apply(st2, check.Result{}, netErr, t0.Add(60*time.Minute))
	if len(msgs) != 1 || !strings.Contains(msgs[0], "still failing, 7 checks") {
		t.Fatal(msgs)
	}
}

func TestRecovered(t *testing.T) {
	st := state.ItemState{Stock: check.OutOfStock, Warned: true, ConsecutiveFails: 4, LastError: "x"}
	next, msgs, _ := apply(st, ok(check.OutOfStock), nil, t0)
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "✅ Recovered: Shop thing") || next.Warned || next.ConsecutiveFails != 0 || next.LastError != "" {
		t.Fatal(next, msgs)
	}
}

func TestRecoveredAndInStockBothSent(t *testing.T) {
	st := state.ItemState{Stock: check.OutOfStock, Warned: true, ConsecutiveFails: 4}
	_, msgs, became := apply(st, ok(check.InStock), nil, t0)
	if len(msgs) != 2 || !became || !strings.HasPrefix(msgs[1], "🟢 IN STOCK: ") {
		t.Fatal(msgs)
	}
}

func TestErrorKeepsStock(t *testing.T) {
	st, _, _ := apply(state.ItemState{Stock: check.InStock}, check.Result{}, netErr, t0)
	if st.Stock != check.InStock {
		t.Fatal(st.Stock)
	}
	_, _, became := apply(st, ok(check.InStock), nil, t0)
	if became {
		t.Fatal("recovery while in stock must not re-trigger")
	}
}

func TestWarningRedacted(t *testing.T) {
	st, msgs, _ := apply(state.ItemState{}, check.Result{}, fmt.Errorf("pw hunter22: %w", check.ErrFormatChanged), t0)
	if strings.Contains(msgs[0], "hunter22") || strings.Contains(st.LastError, "hunter22") {
		t.Fatal(msgs)
	}
}
