// Package menu is the interactive terminal menu (`stockmon menu`).
package menu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
	"github.com/Vegas7/stock_monitor/internal/state"
)

type Options struct {
	ConfigPath, StatePath, Version string
	In                             io.Reader
	Out                            io.Writer
	ReadSecret                     func(prompt string) (string, error)
	HTTP                           *http.Client
	MisakaBaseURL, TelegramBaseURL string
	ServiceStatus                  func() string
	Update                         func(ctx context.Context) error
	Uninstall                      func(ctx context.Context) error
}

type menu struct {
	o   Options
	in  *bufio.Reader
	eof bool
	ctx context.Context
}

// Run shows the menu until the user exits or input ends.
func Run(ctx context.Context, o Options) error {
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	if o.MisakaBaseURL == "" {
		o.MisakaBaseURL = misaka.DefaultBaseURL
	}
	if o.ServiceStatus == nil {
		o.ServiceStatus = func() string { return "unknown" }
	}
	m := &menu{o: o, in: bufio.NewReader(o.In), ctx: ctx}
	return m.main()
}

func (m *menu) printf(format string, a ...any) { fmt.Fprintf(m.o.Out, format, a...) }

// ask prints a prompt and reads one trimmed line ("" at EOF).
func (m *menu) ask(prompt string) string {
	m.printf("%s", prompt)
	line, err := m.in.ReadString('\n')
	if err != nil {
		m.eof = true
		if line == "" {
			m.printf("\n")
		}
	}
	return strings.TrimSpace(line)
}

func (m *menu) askDefault(prompt, def string) string {
	p := prompt
	if def != "" {
		p += " [" + def + "]"
	}
	if v := m.ask(p + ": "); v != "" {
		return v
	}
	return def
}

func (m *menu) yes(prompt string) bool {
	return strings.EqualFold(m.ask(prompt+" [y/N]: "), "y")
}

// choose reads a number in 0..n (0 = back/cancel). Invalid input returns -1.
func (m *menu) choose(prompt string, n int) int {
	v := m.ask(prompt)
	i, err := strconv.Atoi(v)
	if err != nil || i < 0 || i > n {
		if !m.eof && v != "" {
			m.printf("Please enter a number from 0 to %d.\n", n)
		}
		return -1
	}
	return i
}

func (m *menu) secret(prompt string) string {
	s, err := m.o.ReadSecret(prompt)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// load reads the config, starting from defaults when the file does not exist.
func (m *menu) load() (*config.Config, error) {
	c, err := config.Load(m.o.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return config.Default(), nil
	}
	return c, err
}

func (m *menu) save(c *config.Config) bool {
	if err := config.Save(m.o.ConfigPath, c); err != nil {
		m.printf("Could not save: %v\nNothing was saved.\n", err)
		return false
	}
	return true
}

func (m *menu) snapshot() state.State {
	st, err := state.Open(m.o.StatePath)
	if err != nil {
		return state.State{Items: map[string]*state.ItemState{}}
	}
	return st.Snapshot()
}

func (m *menu) main() error {
	for !m.eof {
		c, err := m.load()
		if err != nil {
			m.printf("Config error: %v\nFix or delete %s, then run the menu again.\n", err, m.o.ConfigPath)
			return nil
		}
		m.printf("\nstockmon %s — service: %s — %d items\n", m.o.Version, m.o.ServiceStatus(), len(c.Items))
		m.printf(" 1) Show items and status\n 2) Add item\n 3) Edit / enable / disable / remove item\n 4) Test an item now\n" +
			" 5) Telegram: set bot & chat ID, send test\n 6) Misaka account: set login, test login\n" +
			" 7) Auto-order: settings, arm/disarm, price check\n 8) Settings (interval, warnings, heartbeat)\n" +
			" 9) Update / uninstall\n 0) Exit\n")
		switch m.choose("> ", 9) {
		case 0:
			return nil
		case 1:
			m.showStatus(c)
		case 2:
			m.addItem(c)
		case 3:
			m.editItem(c)
		case 4:
			m.testItem(c)
		case 5:
			m.telegramSetup(c)
		case 6:
			m.misakaAccount(c)
		case 7:
			m.autoOrder(c)
		case 8:
			m.settings(c)
		case 9:
			m.maintenance()
		}
	}
	return nil
}

func (m *menu) pickItem(c *config.Config, filter func(config.Item) bool) int {
	var idx []int
	for i, it := range c.Items {
		if filter == nil || filter(it) {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		m.printf("No matching items.\n")
		return -1
	}
	for n, i := range idx {
		it := c.Items[i]
		m.printf(" %d) %s (%s)%s\n", n+1, it.Name, it.Type, map[bool]string{false: " [disabled]", true: ""}[it.Enabled])
	}
	n := m.choose("Item (0 = back): ", len(idx))
	if n <= 0 {
		return -1
	}
	return idx[n-1]
}

func (m *menu) showStatus(c *config.Config) {
	st := m.snapshot()
	if len(c.Items) == 0 {
		m.printf("No items yet. Choose 2 to add one.\n")
	}
	for _, it := range c.Items {
		is := st.Items[it.ID]
		stock, last, fails := "unknown", "never", 0
		if is != nil {
			if is.Stock != "" {
				stock = strings.ReplaceAll(string(is.Stock), "_", " ")
			}
			if !is.LastCheckAt.IsZero() {
				last = is.LastCheckAt.In(c.Location()).Format("2006-01-02 15:04:05")
			}
			fails = is.ConsecutiveFails
		}
		armed := ""
		if it.Misaka != nil {
			armed = ", auto-order: disarmed"
			if ao := it.Misaka.AutoOrder; ao.Enabled && ao.ArmID != "" && !st.ArmUsed(ao.ArmID) {
				armed = ", auto-order: ARMED"
			}
		}
		en := ""
		if !it.Enabled {
			en = " [disabled]"
		}
		m.printf("- %s%s: %s (last check %s", it.Name, en, stock, last)
		if fails > 0 {
			m.printf(", failing %d", fails)
		}
		m.printf("%s)\n", armed)
		if is != nil && is.LastError != "" {
			m.printf("    last error: %s\n", is.LastError)
		}
	}
	if n := len(st.Orders); n > 0 {
		o := st.Orders[n-1]
		m.printf("Latest order: %s for item %s at %s", o.Status, o.ItemID, o.At.In(c.Location()).Format("2006-01-02 15:04"))
		if o.InvoiceID != "" {
			m.printf(", invoice https://app.misaka.io/billing/invoice/%s", o.InvoiceID)
		}
		if o.Message != "" {
			m.printf(" — %s", o.Message)
		}
		m.printf("\n")
	}
}
