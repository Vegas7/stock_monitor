package check

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
)

type mem struct{ t misaka.Tokens }

func (m *mem) LoadTokens() misaka.Tokens        { return m.t }
func (m *mem) SaveTokens(t misaka.Tokens) error { m.t = t; return nil }

func misakaServer(t *testing.T, regions, plans string) *misaka.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/mc2/regions":
			io.WriteString(w, regions)
		case "/api/mc2/regions/11/plans":
			io.WriteString(w, plans)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return misaka.New(srv.URL+"/api/", "u", "pass", &mem{t: misaka.Tokens{Access: "A"}}, srv.Client())
}

const rOK = `[{"id":11,"slug":"nrt04","available":true}]`

func mItem() config.MisakaItem {
	return config.MisakaItem{Region: "nrt04", Plan: "s3n-1c1g", Image: "debian-13"}
}

func TestMisakaInStock(t *testing.T) {
	cl := misakaServer(t, rOK, `[{"id":42,"slug":"s3n-1c1g","name":"S3N 1C1G","available":true}]`)
	r, err := NewMisaka(mItem(), cl).Check(context.Background())
	if err != nil || r.Status != InStock || r.Detail != "S3N 1C1G available in NRT04" {
		t.Fatal(r, err)
	}
}

func TestMisakaPlanUnavailableReason(t *testing.T) {
	cl := misakaServer(t, rOK, `[{"id":42,"slug":"s3n-1c1g","available":false,"unavailable_reason":"Out of stock"}]`)
	r, err := NewMisaka(mItem(), cl).Check(context.Background())
	if err != nil || r.Status != OutOfStock || r.Detail != "Out of stock" {
		t.Fatal(r, err)
	}
}

func TestMisakaRegionUnavailable(t *testing.T) {
	cl := misakaServer(t, `[{"id":11,"slug":"nrt04","available":false,"unavailable_reason":"Region full"}]`,
		`[{"id":42,"slug":"s3n-1c1g","available":true}]`)
	r, err := NewMisaka(mItem(), cl).Check(context.Background())
	if err != nil || r.Status != OutOfStock || r.Detail != "Region full" {
		t.Fatal(r, err)
	}
}

func TestMisakaPlanMissingIsFormatChanged(t *testing.T) {
	cl := misakaServer(t, rOK, `[{"id":43,"slug":"other","available":true}]`)
	_, err := NewMisaka(mItem(), cl).Check(context.Background())
	if !errors.Is(err, ErrFormatChanged) || !Urgent(err) || !strings.Contains(err.Error(), "s3n-1c1g") {
		t.Fatal(err)
	}
	cl = misakaServer(t, rOK, `[{"id":42,"slug":"s3n-1c1g"}]`)
	if _, err := NewMisaka(mItem(), cl).Check(context.Background()); !errors.Is(err, ErrFormatChanged) {
		t.Fatal("missing available must be format error:", err)
	}
}

func TestMisakaSlugCaseInsensitive(t *testing.T) {
	cl := misakaServer(t, rOK, `[{"id":42,"slug":"s3n-1c1g","available":true}]`)
	it := config.MisakaItem{Region: "NRT04", Plan: "S3N-1C1G"}
	if r, err := NewMisaka(it, cl).Check(context.Background()); err != nil || r.Status != InStock {
		t.Fatal(r, err)
	}
}

func TestUrgentLogin(t *testing.T) {
	if !Urgent(misaka.ErrLogin) || !Urgent(misaka.ErrLoginBackoff) {
		t.Fatal("login should be urgent")
	}
	if Urgent(&net.OpError{Op: "dial", Err: errors.New("refused")}) {
		t.Fatal("net error not urgent")
	}
}
