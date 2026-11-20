// Package service is the long-running monitor (`stockmon run`).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Vegas7/stock_monitor/internal/check"
	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
	"github.com/Vegas7/stock_monitor/internal/monitor"
	"github.com/Vegas7/stock_monitor/internal/order"
	"github.com/Vegas7/stock_monitor/internal/redact"
	"github.com/Vegas7/stock_monitor/internal/state"
	"github.com/Vegas7/stock_monitor/internal/telegram"
)

type Options struct {
	ConfigPath, StatePath, Version string
	HTTP                           *http.Client
	MisakaBaseURL, TelegramBaseURL string
	Now                            func() time.Time
	Rand                           func() float64
	Sleep                          func(ctx context.Context, d time.Duration) bool
	Latest                         func(ctx context.Context) (string, error)
	Log                            *slog.Logger
	ReloadEvery                    time.Duration
}

func (o *Options) defaults() {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if o.MisakaBaseURL == "" {
		o.MisakaBaseURL = misaka.DefaultBaseURL
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Rand == nil {
		o.Rand = rand.Float64
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) bool {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-t.C:
				return true
			}
		}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if o.ReloadEvery == 0 {
		o.ReloadEvery = 5 * time.Second
	}
	if o.Version == "" {
		o.Version = "dev"
	}
}

type runner struct {
	cancel context.CancelFunc
	sig    string
}

type svc struct {
	o     Options
	store *state.Store

	mu      sync.Mutex
	cfg     *config.Config
	tg      *telegram.Client
	mc      *misaka.Client
	mcCreds string

	runners map[string]*runner
	wg      sync.WaitGroup
}

// Run monitors until ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	o.defaults()
	s := &svc{o: o, runners: map[string]*runner{}}

	cfg, mtime := s.waitForConfig(ctx)
	if cfg == nil {
		return nil // cancelled while waiting
	}
	s.setConfig(cfg)

	_, statErr := os.Stat(o.StatePath)
	existed := statErr == nil
	store, err := state.Open(o.StatePath)
	if err != nil {
		s.notify(ctx, fmt.Sprintf("%sstate file unreadable, stockmon stopped (no orders will be placed): %v", monitor.PrefixWarning, err))
		return err
	}
	s.store = store
	restart := existed && !store.Snapshot().CleanShutdown
	if err := store.Update(func(st *state.State) { st.CleanShutdown = false }); err != nil {
		return err
	}

	start := fmt.Sprintf("stockmon started %s: %d items", o.Version, enabledCount(cfg))
	if restart {
		start += " (after a restart)"
	}
	s.notify(ctx, start)
	for _, m := range order.RecoverPending(store) {
		s.notify(ctx, m)
	}

	s.syncRunners(ctx, cfg)
	badMtime := time.Time{}
	tick := time.NewTicker(o.ReloadEvery)
	defer tick.Stop()
	s.heartbeat(ctx)
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-tick.C:
		}
		if fi, err := os.Stat(o.ConfigPath); err == nil && !fi.ModTime().Equal(mtime) {
			newCfg, err := config.Load(o.ConfigPath)
			if err != nil {
				if !fi.ModTime().Equal(badMtime) {
					badMtime = fi.ModTime()
					s.notify(ctx, fmt.Sprintf("%sconfig reload failed, keeping the previous config\n%s", monitor.PrefixWarning, s.redactor().Err(err)))
				}
			} else {
				mtime = fi.ModTime()
				s.setConfig(newCfg)
				s.syncRunners(ctx, newCfg)
			}
		}
		s.heartbeat(ctx)
	}

	s.mu.Lock()
	for _, r := range s.runners {
		r.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return store.Update(func(st *state.State) { st.CleanShutdown = true })
}

func enabledCount(c *config.Config) int {
	n := 0
	for _, it := range c.Items {
		if it.Enabled {
			n++
		}
	}
	return n
}

func (s *svc) waitForConfig(ctx context.Context) (*config.Config, time.Time) {
	logged := ""
	for {
		fi, statErr := os.Stat(s.o.ConfigPath)
		cfg, err := config.Load(s.o.ConfigPath)
		if err == nil {
			return cfg, fi.ModTime()
		}
		if errors.Is(statErr, fs.ErrNotExist) {
			err = fmt.Errorf("no config at %s yet; run `sudo stockmon menu`", s.o.ConfigPath)
		}
		if err.Error() != logged {
			s.o.Log.Warn("waiting for a valid config", "err", err)
			logged = err.Error()
		}
		if !s.o.Sleep(ctx, s.o.ReloadEvery) {
			return nil, time.Time{}
		}
	}
}

func (s *svc) setConfig(c *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = c
	if c.Telegram.BotToken != "" && c.Telegram.ChatID != "" {
		s.tg = telegram.New(c.Telegram.BotToken)
		if s.o.TelegramBaseURL != "" {
			s.tg.BaseURL = s.o.TelegramBaseURL
		}
	} else {
		s.tg = nil
	}
	creds := c.Misaka.Username + "\x00" + c.Misaka.Password
	if s.mc == nil || creds != s.mcCreds {
		s.mcCreds = creds
		s.mc = nil // rebuilt lazily once the store exists
	}
}

func (s *svc) misakaClient() *misaka.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mc == nil {
		s.mc = misaka.New(s.o.MisakaBaseURL, s.cfg.Misaka.Username, s.cfg.Misaka.Password, s.store, s.o.HTTP)
		s.mc.Now = s.o.Now
	}
	return s.mc
}

func (s *svc) current() (*config.Config, *telegram.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, s.tg
}

func (s *svc) redactor() *redact.Redactor {
	cfg, _ := s.current()
	secrets := []string{cfg.Misaka.Password, cfg.Telegram.BotToken}
	if s.store != nil {
		t := s.store.LoadTokens()
		secrets = append(secrets, t.Access, t.Refresh, t.Sliding)
	}
	return redact.New(secrets...)
}

func (s *svc) notify(ctx context.Context, text string) {
	if text == "" {
		return
	}
	text = s.redactor().String(text)
	s.o.Log.Info("notify", "text", text)
	cfg, tg := s.current()
	if tg == nil {
		return
	}
	if err := tg.Send(context.WithoutCancel(ctx), cfg.Telegram.ChatID, text); err != nil {
		s.o.Log.Error("telegram send failed", "err", s.redactor().Err(err))
	}
}

func signature(it config.Item) string {
	b, _ := json.Marshal(it)
	return string(b)
}

// syncRunners starts, stops and restarts item loops to match cfg.
func (s *svc) syncRunners(ctx context.Context, cfg *config.Config) {
	want := map[string]config.Item{}
	for _, it := range cfg.Items {
		if it.Enabled {
			want[it.ID] = it
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.runners {
		if it, ok := want[id]; !ok || signature(it) != r.sig {
			r.cancel()
			delete(s.runners, id)
		}
	}
	for id, it := range want {
		if _, ok := s.runners[id]; ok {
			continue
		}
		rctx, cancel := context.WithCancel(ctx)
		s.runners[id] = &runner{cancel: cancel, sig: signature(it)}
		s.wg.Add(1)
		go func(it config.Item) {
			defer s.wg.Done()
			s.runItem(rctx, it)
		}(it)
	}
	ids := map[string]bool{}
	for _, it := range cfg.Items {
		ids[it.ID] = true
	}
	if s.store != nil {
		s.store.Update(func(st *state.State) {
			for id := range st.Items {
				if !ids[id] {
					delete(st.Items, id)
				}
			}
		})
	}
}

func (s *svc) checker(it config.Item) check.Checker {
	if it.Type == config.TypeMisaka {
		return check.NewMisaka(*it.Misaka, s.misakaClient())
	}
	return check.NewWeb(*it.Web, s.o.HTTP)
}

func (s *svc) runItem(ctx context.Context, it config.Item) {
	if !s.o.Sleep(ctx, time.Duration(s.o.Rand()*float64(10*time.Second))) {
		return
	}
	for {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		res, err := s.checker(it).Check(cctx)
		cancel()
		if ctx.Err() != nil {
			return // item removed/changed or shutting down: don't record a fake failure
		}
		cfg, _ := s.current()
		var msgs []string
		var became bool
		red := s.redactor() // must not be called inside store.Update (it reads the store)
		s.store.Update(func(st *state.State) {
			prev := state.ItemState{}
			if p := st.Items[it.ID]; p != nil {
				prev = *p
			}
			next, m, b := monitor.Apply(prev, it, res, err, s.o.Now(), cfg.Settings, red)
			st.Items[it.ID] = &next
			msgs, became = m, b
		})
		for _, m := range msgs {
			s.notify(ctx, m)
		}
		if became && order.Armed(it, s.store.Snapshot()) {
			// An order must not be interrupted by a config reload or shutdown.
			s.notify(ctx, order.Place(context.WithoutCancel(ctx), s.misakaClient(), s.store, it, s.redactor()))
		}
		lo, hi := cfg.Settings.IntervalMinSec, cfg.Settings.IntervalMaxSec
		d := time.Duration((float64(lo) + s.o.Rand()*float64(hi-lo)) * float64(time.Second))
		if !s.o.Sleep(ctx, d) {
			return
		}
	}
}
