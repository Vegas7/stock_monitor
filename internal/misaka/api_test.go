package misaka

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRegionsAndPlans(t *testing.T) {
	f := newFake(t)
	f.set("GET mc2/regions", jsonH(200, regionsJSON))
	f.set("GET mc2/regions/11/plans", jsonH(200, plansJSON))
	c := f.client(nil)
	rs, err := c.Regions(ctx)
	if err != nil || len(rs) != 1 || rs[0].Slug != "nrt04" || rs[0].Available == nil || !*rs[0].Available {
		t.Fatal(rs, err)
	}
	ps, err := c.Plans(ctx, rs[0].ID)
	if err != nil || len(ps) != 1 {
		t.Fatal(ps, err)
	}
	if *ps[0].Available || ps[0].UnavailableReason != "Out of stock" || ps[0].ID.String() != "42" {
		t.Fatal(ps[0])
	}
}

func TestPlansParsesPrices(t *testing.T) {
	var p Plan
	if err := json.Unmarshal([]byte(`{"id":1,"slug":"x","available":true,"price_monthly":10,"price_annual":"100.50","price_quarterly":null}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Prices["monthly"] != 10 || p.Prices["annual"] != 100.5 || len(p.Prices) != 2 {
		t.Fatal(p.Prices)
	}
}

func TestPlansHTMLResponseIsFormatError(t *testing.T) {
	f := newFake(t)
	f.set("GET mc2/regions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html>maintenance</html>")
	})
	_, err := f.client(nil).Regions(ctx)
	if !errors.Is(err, ErrFormatChanged) {
		t.Fatal(err)
	}
}

func TestPlanMissingAvailable(t *testing.T) {
	var p Plan
	json.Unmarshal([]byte(`{"id":1,"slug":"x"}`), &p)
	if p.Available != nil {
		t.Fatal("want nil")
	}
}

func TestIDRoundTrip(t *testing.T) {
	var a, b ID
	json.Unmarshal([]byte(`42`), &a)
	json.Unmarshal([]byte(`"abc"`), &b)
	out, _ := json.Marshal(OrderRequest{Region: a, Plan: b, Image: a, Count: 1})
	s := string(out)
	if !strings.Contains(s, `"region":42`) || !strings.Contains(s, `"plan":"abc"`) {
		t.Fatal(s)
	}
	if b.String() != "abc" || a.String() != "42" {
		t.Fatal(a.String(), b.String())
	}
}

func TestCreateOrderBody(t *testing.T) {
	f := newFake(t)
	f.set("POST mc2/instances/", jsonH(200, `{"orders":[123]}`))
	req := OrderRequest{Region: ID("11"), Plan: ID("42"), Image: ID(`"deb"`), Count: 1, Keys: []string{"7"},
		BillingCycle: "monthly", Config: []OrderName{{Name: "tokyo-1"}}, IssueInvoice: true}
	if _, err := f.client(nil).CreateOrder(ctx, req); err != nil {
		t.Fatal(err)
	}
	b := f.requests("POST mc2/instances/")[0].Body
	for _, k := range []string{"region", "plan", "image", "count", "keys", "billing_cycle", "config", "issue_invoice"} {
		if _, ok := b[k]; !ok {
			t.Fatalf("missing %s in %v", k, b)
		}
	}
	if _, ok := b["coupon"]; ok {
		t.Fatal("empty coupon must be omitted")
	}
	if b["issue_invoice"] != true || b["count"].(float64) != 1 {
		t.Fatal(b)
	}
}

func TestCreateOrderResult(t *testing.T) {
	f := newFake(t)
	f.set("POST mc2/instances/", jsonH(200, `{"orders":[123,"124"],"invoice_id":456}`))
	res, err := f.client(nil).CreateOrder(ctx, OrderRequest{Count: 1})
	if err != nil || strings.Join(res.Orders, ",") != "123,124" || res.InvoiceID != "456" {
		t.Fatal(res, err)
	}
}

func TestCreateOrderClearError(t *testing.T) {
	f := newFake(t)
	f.set("POST mc2/instances/", jsonH(400, `{"detail":"Plan unavailable","internal_code":"plan_unavailable"}`))
	_, err := f.client(nil).CreateOrder(ctx, OrderRequest{Count: 1})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Detail != "Plan unavailable" || ae.InternalCode != "plan_unavailable" || Unclear(err) {
		t.Fatal(err)
	}
}

func TestCreateOrderTimeoutIsUnclear(t *testing.T) {
	f := newFake(t)
	f.set("POST mc2/instances/", func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) })
	c := f.client(nil)
	c.HTTP = &http.Client{Timeout: 100 * time.Millisecond}
	_, err := c.CreateOrder(ctx, OrderRequest{Count: 1})
	if err == nil || !Unclear(err) {
		t.Fatal(err)
	}
}

func TestImagesSSHKeysAndPrice(t *testing.T) {
	f := newFake(t)
	f.set("GET mc2/images", jsonH(200, `[{"id":"img1","slug":"debian-13","name":"Debian 13"}]`))
	f.set("GET mc2/sshkeys/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			jsonH(200, `{"results":[{"id":8,"name":"b"}],"next":null}`)(w, r)
			return
		}
		jsonH(200, `{"results":[{"id":7,"name":"a"}],"next":"`+f.srv.URL+`/api/mc2/sshkeys/?page=2"}`)(w, r)
	})
	f.set("POST mc2/instances/calculate_price/", jsonH(200, `{"recurring_price":10}`))
	c := f.client(nil)
	imgs, err := c.Images(ctx)
	if err != nil || imgs[0].Slug != "debian-13" {
		t.Fatal(imgs, err)
	}
	if q := f.requests("GET mc2/images"); len(q) != 1 {
		t.Fatal("images path")
	}
	keys, err := c.SSHKeys(ctx)
	if err != nil || len(keys) != 2 || keys[1].ID.String() != "8" {
		t.Fatal(keys, err)
	}
	p, err := c.CalculatePrice(ctx, PriceRequest{Plan: ID("42"), Count: 1, BillingCycle: "monthly"})
	if err != nil || p["recurring_price"].(float64) != 10 {
		t.Fatal(p, err)
	}
}

func TestCreateOrderEmptyResultIsUnclear(t *testing.T) {
	f := newFake(t)
	f.set("POST mc2/instances/", jsonH(201, `{"detail":"accepted"}`))
	_, err := f.client(nil).CreateOrder(ctx, OrderRequest{Count: 1})
	if err == nil || !Unclear(err) || !errors.Is(err, ErrFormatChanged) {
		t.Fatal(err)
	}
}
