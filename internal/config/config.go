// Package config defines stockmon's configuration file. Only the menu writes it.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Vegas7/stock_monitor/internal/fsutil"
)

const (
	TypeMisaka = "misaka_plan"
	TypeWeb    = "web"

	MatchKeyword  = "keyword"
	MatchSelector = "selector"

	MeansInStock    = "in_stock"
	MeansOutOfStock = "out_of_stock"

	// MinIntervalSec is the floor for check intervals (spec anti-goal: don't hammer).
	MinIntervalSec = 30
)

type Config struct {
	Telegram Telegram      `json:"telegram"`
	Misaka   MisakaAccount `json:"misaka"`
	Settings Settings      `json:"settings"`
	Items    []Item        `json:"items"`
}

type Telegram struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type MisakaAccount struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Settings struct {
	IntervalMinSec     int    `json:"interval_min_sec"`
	IntervalMaxSec     int    `json:"interval_max_sec"`
	FailWarnAfter      int    `json:"fail_warn_after"`
	FailRemindMinutes  int    `json:"fail_remind_minutes"`
	DailyHeartbeatHour int    `json:"daily_heartbeat_hour"`
	Timezone           string `json:"timezone"`
}

type Item struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Type    string      `json:"type"`
	Enabled bool        `json:"enabled"`
	Misaka  *MisakaItem `json:"misaka,omitempty"`
	Web     *WebItem    `json:"web,omitempty"`
}

type MisakaItem struct {
	Region    string    `json:"region"`
	Plan      string    `json:"plan"`
	Image     string    `json:"image"`
	AutoOrder AutoOrder `json:"auto_order"`
}

type AutoOrder struct {
	Enabled      bool     `json:"enabled"`
	ArmID        string   `json:"arm_id"`
	BillingCycle string   `json:"billing_cycle"`
	SSHKeyIDs    []string `json:"ssh_key_ids"`
	Hostname     string   `json:"hostname"`
	Coupon       string   `json:"coupon"`
}

type WebItem struct {
	URL          string `json:"url"`
	Match        string `json:"match"`
	Value        string `json:"value"`
	ValueText    string `json:"value_text,omitempty"`
	PresentMeans string `json:"present_means"`
}

func defaultSettings() Settings {
	return Settings{IntervalMinSec: 30, IntervalMaxSec: 60, FailWarnAfter: 3,
		FailRemindMinutes: 60, DailyHeartbeatHour: 9, Timezone: "Asia/Tokyo"}
}

// Default returns a config with default settings and no items.
func Default() *Config {
	return &Config{Settings: defaultSettings(), Items: []Item{}}
}

// Load reads, defaults and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	c.fillDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) fillDefaults() {
	d := defaultSettings()
	s := &c.Settings
	if s.IntervalMinSec == 0 {
		s.IntervalMinSec = d.IntervalMinSec
	}
	if s.IntervalMaxSec == 0 {
		s.IntervalMaxSec = d.IntervalMaxSec
	}
	if s.FailWarnAfter == 0 {
		s.FailWarnAfter = d.FailWarnAfter
	}
	if s.FailRemindMinutes == 0 {
		s.FailRemindMinutes = d.FailRemindMinutes
	}
	// DailyHeartbeatHour 0 (midnight) is a valid choice; only default when the whole block was absent.
	if s.Timezone == "" {
		s.Timezone = d.Timezone
		if s.DailyHeartbeatHour == 0 {
			s.DailyHeartbeatHour = d.DailyHeartbeatHour
		}
	}
	if c.Items == nil {
		c.Items = []Item{}
	}
}

// Validate checks settings and items.
func (c *Config) Validate() error {
	s := c.Settings
	if s.IntervalMinSec < MinIntervalSec {
		return fmt.Errorf("interval_min_sec must be at least %d", MinIntervalSec)
	}
	if s.IntervalMaxSec < s.IntervalMinSec {
		return errors.New("interval_max_sec must be >= interval_min_sec")
	}
	if s.FailWarnAfter < 1 {
		return errors.New("fail_warn_after must be at least 1")
	}
	if s.FailRemindMinutes < 1 {
		return errors.New("fail_remind_minutes must be at least 1")
	}
	if s.DailyHeartbeatHour < 0 || s.DailyHeartbeatHour > 23 {
		return errors.New("daily_heartbeat_hour must be 0-23")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("timezone %q: %w", s.Timezone, err)
	}
	seen := map[string]bool{}
	for i, it := range c.Items {
		if it.ID == "" {
			return fmt.Errorf("item %d: missing id", i)
		}
		if seen[it.ID] {
			return fmt.Errorf("duplicate item id %q", it.ID)
		}
		seen[it.ID] = true
		if err := it.validate(); err != nil {
			return fmt.Errorf("item %q: %w", it.Name, err)
		}
	}
	return nil
}

func (it Item) validate() error {
	switch it.Type {
	case TypeMisaka:
		m := it.Misaka
		if m == nil {
			return errors.New("missing misaka block")
		}
		if m.Region == "" || m.Plan == "" {
			return errors.New("misaka region and plan are required")
		}
		if m.AutoOrder.Enabled {
			if m.AutoOrder.ArmID == "" {
				return errors.New("auto_order enabled without arm_id")
			}
			if m.Image == "" {
				return errors.New("auto_order needs an image")
			}
		}
	case TypeWeb:
		w := it.Web
		if w == nil {
			return errors.New("missing web block")
		}
		if w.URL == "" || w.Value == "" {
			return errors.New("web url and value are required")
		}
		if w.Match != MatchKeyword && w.Match != MatchSelector {
			return fmt.Errorf("match must be %q or %q", MatchKeyword, MatchSelector)
		}
		if w.PresentMeans != MeansInStock && w.PresentMeans != MeansOutOfStock {
			return fmt.Errorf("present_means must be %q or %q", MeansInStock, MeansOutOfStock)
		}
	default:
		return fmt.Errorf("unknown type %q", it.Type)
	}
	return nil
}

// Save validates then atomically writes the config with mode 0640.
func Save(path string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, append(b, '\n'), 0o640)
}

// Location returns the configured time zone (UTC if invalid).
func (c *Config) Location() *time.Location {
	if l, err := time.LoadLocation(c.Settings.Timezone); err == nil {
		return l
	}
	return time.UTC
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// NewID returns an 8-hex-char item id.
func NewID() string { return randHex(4) }

// NewArmID returns a 16-hex-char arm id.
func NewArmID() string { return randHex(8) }
