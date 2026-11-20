package check

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vegas7/stock_monitor/internal/config"
)

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runWeb(t *testing.T, srv *httptest.Server, it config.WebItem) (Result, error) {
	t.Helper()
	it.URL = srv.URL
	return NewWeb(it, srv.Client()).Check(context.Background())
}

func TestKeywordPresentMeansOut(t *testing.T) {
	srv := serve(t, 200, "<p>SOLD OUT</p>")
	r, err := runWeb(t, srv, config.WebItem{Match: "keyword", Value: "sold out", PresentMeans: "out_of_stock"})
	if err != nil || r.Status != OutOfStock {
		t.Fatal(r, err)
	}
	if !strings.Contains(r.Detail, `keyword "sold out" found`) || !strings.Contains(r.Detail, "HTTP 200") {
		t.Fatal(r.Detail)
	}
}

func TestKeywordAbsentMeansIn(t *testing.T) {
	srv := serve(t, 200, "<p>Buy now</p>")
	r, err := runWeb(t, srv, config.WebItem{Match: "keyword", Value: "sold out", PresentMeans: "out_of_stock"})
	if err != nil || r.Status != InStock || !strings.Contains(r.Detail, "not found") {
		t.Fatal(r, err)
	}
}

func TestKeywordPresentMeansIn(t *testing.T) {
	srv := serve(t, 200, "<button>Add to cart</button>")
	r, err := runWeb(t, srv, config.WebItem{Match: "keyword", Value: "Add to cart", PresentMeans: "in_stock"})
	if err != nil || r.Status != InStock {
		t.Fatal(r, err)
	}
}

func TestSelectorExists(t *testing.T) {
	srv := serve(t, 200, `<button class="buy">Buy</button>`)
	r, err := runWeb(t, srv, config.WebItem{Match: "selector", Value: `button.buy:not([disabled])`, PresentMeans: "in_stock"})
	if err != nil || r.Status != InStock {
		t.Fatal(r, err)
	}
	srv2 := serve(t, 200, `<button class="buy" disabled>Buy</button>`)
	r, err = runWeb(t, srv2, config.WebItem{Match: "selector", Value: `button.buy:not([disabled])`, PresentMeans: "in_stock"})
	if err != nil || r.Status != OutOfStock {
		t.Fatal(r, err)
	}
}

func TestSelectorWithValueText(t *testing.T) {
	srv := serve(t, 200, `<span class="st">Coming soon</span>`)
	r, err := runWeb(t, srv, config.WebItem{Match: "selector", Value: ".st", ValueText: "in stock", PresentMeans: "in_stock"})
	if err != nil || r.Status != OutOfStock {
		t.Fatal(r, err)
	}
}

func TestNon2xxIsError(t *testing.T) {
	srv := serve(t, 503, "down")
	_, err := runWeb(t, srv, config.WebItem{Match: "keyword", Value: "x", PresentMeans: "in_stock"})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal(err)
	}
}

func TestBodyCappedAt2MB(t *testing.T) {
	body := strings.Repeat("a", 2_500_000) + "NEEDLE" + strings.Repeat("b", 500_000)
	srv := serve(t, 200, body)
	r, err := runWeb(t, srv, config.WebItem{Match: "keyword", Value: "needle", PresentMeans: "in_stock"})
	if err != nil || r.Status != OutOfStock {
		t.Fatal(r.Status, err)
	}
}

func TestSendsBrowserUA(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ua = r.UserAgent() }))
	defer srv.Close()
	NewWeb(config.WebItem{URL: srv.URL, Match: "keyword", Value: "x", PresentMeans: "in_stock"}, srv.Client()).Check(context.Background())
	if ua != BrowserUA || !strings.Contains(ua, "Mozilla/5.0") {
		t.Fatal(ua)
	}
}

func TestUrgentFormatChanged(t *testing.T) {
	if !Urgent(ErrFormatChanged) || Urgent(io.EOF) || Urgent(nil) {
		t.Fatal()
	}
}
