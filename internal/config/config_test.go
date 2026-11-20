package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validMisaka() Item {
	return Item{ID: "a1", Name: "m", Type: TypeMisaka, Enabled: true,
		Misaka: &MisakaItem{Region: "nrt04", Plan: "s3n-1c1g", Image: "debian-13"}}
}

func validWeb() Item {
	return Item{ID: "b2", Name: "w", Type: TypeWeb, Enabled: true,
		Web: &WebItem{URL: "https://example.com", Match: "keyword", Value: "Sold out", PresentMeans: "out_of_stock"}}
}

func TestDefaultSettings(t *testing.T) {
	s := Default().Settings
	if s.IntervalMinSec != 30 || s.IntervalMaxSec != 60 || s.FailWarnAfter != 3 ||
		s.FailRemindMinutes != 60 || s.DailyHeartbeatHour != 9 || s.Timezone != "Asia/Tokyo" {
		t.Fatalf("defaults %+v", s)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(c *Config){
		"min 29":            func(c *Config) { c.Settings.IntervalMinSec = 29 },
		"max<min":           func(c *Config) { c.Settings.IntervalMaxSec = 40; c.Settings.IntervalMinSec = 50 },
		"bad tz":            func(c *Config) { c.Settings.Timezone = "Mars/X" },
		"hour 24":           func(c *Config) { c.Settings.DailyHeartbeatHour = 24 },
		"unknown type":      func(c *Config) { c.Items[0].Type = "ftp" },
		"regex match":       func(c *Config) { c.Items[1].Web.Match = "regex" },
		"present maybe":     func(c *Config) { c.Items[1].Web.PresentMeans = "maybe" },
		"misaka no plan":    func(c *Config) { c.Items[0].Misaka.Plan = "" },
		"dup ids":           func(c *Config) { c.Items[1].ID = c.Items[0].ID },
		"armed no arm_id":   func(c *Config) { c.Items[0].Misaka.AutoOrder.Enabled = true },
		"misaka nil block":  func(c *Config) { c.Items[0].Misaka = nil },
		"web empty url":     func(c *Config) { c.Items[1].Web.URL = "" },
		"web empty value":   func(c *Config) { c.Items[1].Web.Value = "" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default()
			c.Items = []Item{validMisaka(), validWeb()}
			if err := c.Validate(); err != nil {
				t.Fatalf("baseline invalid: %v", err)
			}
			mut(c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadFillsDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"items":[]}`), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings != Default().Settings {
		t.Fatalf("settings %+v", c.Settings)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	c := Default()
	m := validMisaka()
	m.Misaka.AutoOrder = AutoOrder{Enabled: true, ArmID: NewArmID(), BillingCycle: "monthly", SSHKeyIDs: []string{"7"}, Hostname: "tokyo-1"}
	c.Items = []Item{m, validWeb()}
	c.Telegram = Telegram{BotToken: "123:abc", ChatID: "42"}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Items[0].Misaka.AutoOrder.SSHKeyIDs[0] != "7" || got.Items[1].Web.Value != "Sold out" || got.Telegram.ChatID != "42" {
		t.Fatalf("round trip %+v", got)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestExampleConfigIsValidAndEmpty(t *testing.T) {
	c, err := Load("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if c.Telegram.BotToken != "" || c.Telegram.ChatID != "" || c.Misaka.Username != "" || c.Misaka.Password != "" {
		t.Fatal("example config must not contain secrets")
	}
	if len(c.Items) == 0 {
		t.Fatal("example should show items")
	}
}

func TestIDs(t *testing.T) {
	if len(NewID()) != 8 || len(NewArmID()) != 16 || NewArmID() == NewArmID() {
		t.Fatal("ids")
	}
	if strings.Trim(NewID(), "0123456789abcdef") != "" {
		t.Fatal("not hex")
	}
}
