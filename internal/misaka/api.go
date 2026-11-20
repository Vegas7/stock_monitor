package misaka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ID is a Misaka object id, kept as raw JSON so it is sent back exactly as received.
type ID []byte

func (i ID) MarshalJSON() ([]byte, error) {
	if len(i) == 0 {
		return []byte("null"), nil
	}
	return i, nil
}

func (i *ID) UnmarshalJSON(b []byte) error {
	*i = append((*i)[:0], b...)
	return nil
}

// String returns the id without JSON quotes.
func (i ID) String() string {
	var s string
	if json.Unmarshal(i, &s) == nil {
		return s
	}
	return string(i)
}

type Region struct {
	ID                ID     `json:"id"`
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	Available         *bool  `json:"available"`
	UnavailableReason string `json:"unavailable_reason"`
}

type Plan struct {
	ID                ID                 `json:"id"`
	Slug              string             `json:"slug"`
	Name              string             `json:"name"`
	Available         *bool              `json:"available"`
	UnavailableReason string             `json:"unavailable_reason"`
	Prices            map[string]float64 `json:"-"`
}

func (p *Plan) UnmarshalJSON(b []byte) error {
	type plain Plan
	if err := json.Unmarshal(b, (*plain)(p)); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	p.Prices = map[string]float64{}
	for k, v := range all {
		cycle, ok := strings.CutPrefix(k, "price_")
		if !ok {
			continue
		}
		if f, ok := number(v); ok {
			p.Prices[cycle] = f
		}
	}
	return nil
}

func number(v json.RawMessage) (float64, bool) {
	if t := bytes.TrimSpace(v); len(t) == 0 || string(t) == "null" {
		return 0, false
	}
	var f float64
	if json.Unmarshal(v, &f) == nil {
		return f, true
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

type Image struct {
	ID   ID     `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type SSHKey struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

type PriceRequest struct {
	Plan         ID     `json:"plan"`
	Count        int    `json:"count"`
	BillingCycle string `json:"billing_cycle"`
	Coupon       string `json:"coupon,omitempty"`
}

type OrderName struct {
	Name string `json:"name"`
}

type OrderRequest struct {
	Region       ID          `json:"region"`
	Plan         ID          `json:"plan"`
	Image        ID          `json:"image"`
	Count        int         `json:"count"`
	Keys         []string    `json:"keys"`
	BillingCycle string      `json:"billing_cycle"`
	Config       []OrderName `json:"config"`
	Coupon       string      `json:"coupon,omitempty"`
	IssueInvoice bool        `json:"issue_invoice"`
}

type OrderResult struct {
	Orders    []string
	InvoiceID string
}

func (r *OrderResult) UnmarshalJSON(b []byte) error {
	var raw struct {
		Orders    []json.RawMessage `json:"orders"`
		InvoiceID json.RawMessage   `json:"invoice_id"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for _, o := range raw.Orders {
		r.Orders = append(r.Orders, ID(o).String())
	}
	if len(raw.InvoiceID) > 0 && string(raw.InvoiceID) != "null" {
		r.InvoiceID = ID(raw.InvoiceID).String()
	}
	return nil
}

func (c *Client) Regions(ctx context.Context) ([]Region, error) {
	var out []Region
	return out, c.do(ctx, http.MethodGet, "mc2/regions", nil, &out)
}

func (c *Client) Plans(ctx context.Context, regionID ID) ([]Plan, error) {
	var out []Plan
	return out, c.do(ctx, http.MethodGet, "mc2/regions/"+regionID.String()+"/plans", nil, &out)
}

func (c *Client) Images(ctx context.Context) ([]Image, error) {
	var out []Image
	return out, c.do(ctx, http.MethodGet, "mc2/images?type=os&all=yes", nil, &out)
}

// SSHKeys lists the account's SSH keys, following pagination.
func (c *Client) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	var all []SSHKey
	next := "mc2/sshkeys/"
	for page := 0; next != "" && page < 50; page++ {
		var raw json.RawMessage
		if err := c.do(ctx, http.MethodGet, next, nil, &raw); err != nil {
			return nil, err
		}
		if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '[' {
			var ks []SSHKey
			if err := json.Unmarshal(t, &ks); err != nil {
				return nil, fmt.Errorf("ssh keys: %v: %w", err, ErrFormatChanged)
			}
			return append(all, ks...), nil
		}
		var pg struct {
			Results []SSHKey `json:"results"`
			Next    *string  `json:"next"`
		}
		if err := json.Unmarshal(raw, &pg); err != nil {
			return nil, fmt.Errorf("ssh keys: %v: %w", err, ErrFormatChanged)
		}
		all = append(all, pg.Results...)
		next = ""
		if pg.Next != nil && strings.HasPrefix(*pg.Next, strings.TrimSuffix(c.BaseURL, "api/")) {
			next = *pg.Next
		}
	}
	return all, nil
}

func (c *Client) CalculatePrice(ctx context.Context, r PriceRequest) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodPost, "mc2/instances/calculate_price/", r, &out)
}

// CreateOrder places an order. The caller must never retry it.
func (c *Client) CreateOrder(ctx context.Context, r OrderRequest) (OrderResult, error) {
	var out OrderResult
	if err := c.do(ctx, http.MethodPost, "mc2/instances/", r, &out); err != nil {
		return out, err
	}
	if len(out.Orders) == 0 && out.InvoiceID == "" {
		return out, fmt.Errorf("order response has no order or invoice id: %w", ErrFormatChanged)
	}
	return out, nil
}
