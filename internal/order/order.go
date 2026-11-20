// Package order places at most one Misaka order per arm id.
package order

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
	"github.com/Vegas7/stock_monitor/internal/monitor"
	"github.com/Vegas7/stock_monitor/internal/redact"
	"github.com/Vegas7/stock_monitor/internal/state"
)

const unknownMsg = "Order status unknown — check https://app.misaka.io/iaas/vm/ and billing before re-arming."

// Armed reports whether an item should order on its next in-stock check.
func Armed(it config.Item, st state.State) bool {
	if it.Type != config.TypeMisaka || it.Misaka == nil {
		return false
	}
	ao := it.Misaka.AutoOrder
	return ao.Enabled && ao.ArmID != "" && !st.ArmUsed(ao.ArmID)
}

// Place spends the item's arm on exactly one order attempt and returns the Telegram message.
// It never retries.
func Place(ctx context.Context, cl *misaka.Client, store *state.Store, it config.Item, r *redact.Redactor) string {
	m := it.Misaka
	ao := m.AutoOrder
	// Look up IDs first: if this fails no order is sent, so the arm is kept for the next check.
	req, err := resolve(ctx, cl, m)
	if err != nil {
		return fmt.Sprintf("%s%s\nCouldn't prepare the order, so nothing was sent (still armed; will retry on the next in-stock check): %s",
			monitor.PrefixOrderProb, it.Name, r.Err(err))
	}
	idx := -1
	var already bool
	err = store.Update(func(s *state.State) {
		if s.ArmUsed(ao.ArmID) {
			already = true
			return
		}
		s.UsedArmIDs = append(s.UsedArmIDs, ao.ArmID)
		s.Orders = append(s.Orders, state.OrderRecord{ItemID: it.ID, ArmID: ao.ArmID, Status: state.OrderPlacing, At: time.Now()})
		idx = len(s.Orders) - 1
	})
	if already {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("%s%s\nCould not record the order attempt, so no order was placed: %s", monitor.PrefixOrderProb, it.Name, r.Err(err))
	}
	finish := func(status string, rec func(*state.OrderRecord)) {
		store.Update(func(s *state.State) {
			o := &s.Orders[idx]
			o.Status = status
			if rec != nil {
				rec(o)
			}
		})
	}

	res, err := cl.CreateOrder(ctx, req)
	switch {
	case err == nil:
		finish(state.OrderPlaced, func(o *state.OrderRecord) { o.OrderIDs, o.InvoiceID = res.Orders, res.InvoiceID })
		pay := "https://app.misaka.io/iaas/vm/create/wait/" + strings.Join(res.Orders, ",")
		if res.InvoiceID != "" {
			pay = "https://app.misaka.io/billing/invoice/" + res.InvoiceID
		}
		return fmt.Sprintf("%s%s\nOrders: %s\nPay: %s\nPay the invoice to get the server. Auto-order is now disarmed.",
			monitor.PrefixOrdered, it.Name, strings.Join(res.Orders, ", "), pay)
	case misaka.Unclear(err):
		detail := r.Err(err)
		finish(state.OrderUnknown, func(o *state.OrderRecord) { o.Message = detail })
		return fmt.Sprintf("%s%s\n%s\n(%s)", monitor.PrefixOrderProb, it.Name, unknownMsg, detail)
	default:
		detail := r.Err(err)
		var ae *misaka.APIError
		if errors.As(err, &ae) {
			detail = r.String(ae.Detail)
			if ae.InternalCode != "" {
				detail += " (" + ae.InternalCode + ")"
			}
		}
		finish(state.OrderFailed, func(o *state.OrderRecord) { o.Message = detail })
		return fmt.Sprintf("%s%s\n%s\nRe-arm in the menu to try again.", monitor.PrefixOrderProb, it.Name, detail)
	}
}

func resolve(ctx context.Context, cl *misaka.Client, m *config.MisakaItem) (misaka.OrderRequest, error) {
	var req misaka.OrderRequest
	regions, err := cl.Regions(ctx)
	if err != nil {
		return req, err
	}
	for _, rg := range regions {
		if strings.EqualFold(rg.Slug, m.Region) {
			req.Region = rg.ID
		}
	}
	if req.Region == nil {
		return req, fmt.Errorf("region %q not found", m.Region)
	}
	plans, err := cl.Plans(ctx, req.Region)
	if err != nil {
		return req, err
	}
	for _, p := range plans {
		if strings.EqualFold(p.Slug, m.Plan) {
			req.Plan = p.ID
		}
	}
	if req.Plan == nil {
		return req, fmt.Errorf("plan %q not found", m.Plan)
	}
	images, err := cl.Images(ctx)
	if err != nil {
		return req, err
	}
	for _, im := range images {
		if strings.EqualFold(im.Slug, m.Image) {
			req.Image = im.ID
		}
	}
	if req.Image == nil {
		return req, fmt.Errorf("image %q not found", m.Image)
	}
	ao := m.AutoOrder
	host := ao.Hostname
	if host == "" {
		host = m.Plan + "-1"
	}
	req.Count = 1
	req.IssueInvoice = true
	req.Keys = ao.SSHKeyIDs
	if req.Keys == nil {
		req.Keys = []string{}
	}
	req.BillingCycle = ao.BillingCycle
	if req.BillingCycle == "" {
		req.BillingCycle = "monthly"
	}
	req.Config = []misaka.OrderName{{Name: host}}
	req.Coupon = ao.Coupon
	return req, nil
}

// RecoverPending marks orders left "placing" by a crash as unknown and returns alerts.
func RecoverPending(store *state.Store) []string {
	var msgs []string
	store.Update(func(s *state.State) {
		for i := range s.Orders {
			if s.Orders[i].Status == state.OrderPlacing {
				s.Orders[i].Status = state.OrderUnknown
				msgs = append(msgs, fmt.Sprintf("%sitem %s\nstockmon stopped while ordering. %s", monitor.PrefixOrderProb, s.Orders[i].ItemID, unknownMsg))
			}
		}
	})
	return msgs
}
