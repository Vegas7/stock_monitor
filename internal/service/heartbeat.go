package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Vegas7/stock_monitor/internal/check"
	"github.com/Vegas7/stock_monitor/internal/monitor"
	"github.com/Vegas7/stock_monitor/internal/state"
	"github.com/Vegas7/stock_monitor/internal/version"
)

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// heartbeat sends the daily summary once, at the configured local hour.
func (s *svc) heartbeat(ctx context.Context) {
	cfg, _ := s.current()
	loc := cfg.Location()
	now := s.o.Now().In(loc)
	snap := s.store.Snapshot()
	if now.Hour() != cfg.Settings.DailyHeartbeatHour || sameDay(snap.LastHeartbeat.In(loc), now) {
		return
	}
	latest := snap.LatestVersion
	if s.o.Latest != nil && now.Sub(snap.LastVersionCheck) >= 24*time.Hour {
		if v, err := s.o.Latest(ctx); err == nil {
			latest = v
		} else {
			s.o.Log.Warn("release check failed", "err", s.redactor().Err(err))
		}
		s.store.Update(func(st *state.State) { st.LastVersionCheck = now; st.LatestVersion = latest })
	}
	var b strings.Builder
	b.WriteString(monitor.PrefixDaily)
	b.WriteString(now.Format("2006-01-02"))
	for _, it := range cfg.Items {
		fmt.Fprintf(&b, "\n%s: ", it.Name)
		if !it.Enabled {
			b.WriteString("disabled")
			continue
		}
		is := snap.Items[it.ID]
		switch {
		case is == nil || is.Stock == check.Unknown:
			b.WriteString("unknown")
		case is.Stock == check.InStock:
			b.WriteString("in stock")
		default:
			b.WriteString("out of stock")
		}
		if is != nil && is.ConsecutiveFails > 0 {
			fmt.Fprintf(&b, ", failing %d", is.ConsecutiveFails)
		}
	}
	if len(cfg.Items) == 0 {
		b.WriteString("\nno items configured")
	}
	if latest != "" && version.Newer(s.o.Version, latest) {
		fmt.Fprintf(&b, "\nupdate available: %s → %s, run sudo stockmon update", s.o.Version, latest)
	}
	s.store.Update(func(st *state.State) { st.LastHeartbeat = now })
	s.notify(ctx, b.String())
}
