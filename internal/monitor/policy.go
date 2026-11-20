// Package monitor turns check results into state changes and messages.
package monitor

import (
	"fmt"
	"time"

	"github.com/Vegas7/stock_monitor/internal/check"
	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/redact"
	"github.com/Vegas7/stock_monitor/internal/state"
)

// Message prefixes (Global Constraints).
const (
	PrefixInStock   = "🟢 IN STOCK: "
	PrefixSoldOut   = "⚪ Sold out again: "
	PrefixWarning   = "⚠️ Warning: "
	PrefixRecovered = "✅ Recovered: "
	PrefixOrdered   = "🛒 Order placed: "
	PrefixOrderProb = "🚨 Order problem: "
	PrefixDaily     = "💓 stockmon daily: "
)

// Link returns the page a person should open for an item.
func Link(it config.Item) string {
	switch {
	case it.Misaka != nil:
		u := fmt.Sprintf("https://app.misaka.io/iaas/vm/create/%s/%s", it.Misaka.Region, it.Misaka.Plan)
		if it.Misaka.Image != "" {
			u += "/" + it.Misaka.Image
		}
		return u
	case it.Web != nil:
		return it.Web.URL
	}
	return ""
}

// Apply is the pure status/notification policy for one check.
func Apply(prev state.ItemState, item config.Item, res check.Result, err error, now time.Time,
	s config.Settings, r *redact.Redactor) (next state.ItemState, msgs []string, becameInStock bool) {
	next = prev
	next.LastCheckAt = now
	link := Link(item)

	if err != nil {
		errText := r.Err(err)
		next.ConsecutiveFails++
		next.LastError = errText
		switch {
		case !prev.Warned && (check.Urgent(err) || next.ConsecutiveFails >= s.FailWarnAfter):
			next.Warned = true
			next.LastWarnAt = now
			msgs = append(msgs, fmt.Sprintf("%s%s\n%s", PrefixWarning, item.Name, errText))
		case prev.Warned && now.Sub(prev.LastWarnAt) >= time.Duration(s.FailRemindMinutes)*time.Minute:
			next.LastWarnAt = now
			msgs = append(msgs, fmt.Sprintf("%s%s (still failing, %d checks)\n%s", PrefixWarning, item.Name, next.ConsecutiveFails, errText))
		}
		return next, msgs, false
	}

	if prev.Warned {
		msgs = append(msgs, PrefixRecovered+item.Name)
	}
	next.Warned = false
	next.ConsecutiveFails = 0
	next.LastError = ""
	next.Detail = res.Detail

	if res.Status != prev.Stock {
		next.LastChangeAt = now
	}
	switch {
	case res.Status == check.InStock && prev.Stock != check.InStock:
		becameInStock = true
		msgs = append(msgs, fmt.Sprintf("%s%s\n%s\n%s", PrefixInStock, item.Name, res.Detail, link))
	case res.Status == check.OutOfStock && prev.Stock == check.InStock:
		msgs = append(msgs, fmt.Sprintf("%s%s\n%s\n%s", PrefixSoldOut, item.Name, res.Detail, link))
	}
	next.Stock = res.Status
	return next, msgs, becameInStock
}
