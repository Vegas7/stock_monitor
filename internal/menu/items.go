package menu

import (
	"fmt"
	"strings"

	"github.com/Vegas7/stock_monitor/internal/check"
	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
)

func (m *menu) addItem(c *config.Config) {
	m.printf("Type:\n 1) Misaka plan\n 2) Web page\n")
	var it config.Item
	var ok bool
	switch m.choose("> ", 2) {
	case 1:
		it, ok = m.newMisakaItem(c)
	case 2:
		it, ok = m.newWebItem()
	default:
		return
	}
	if !ok {
		return
	}
	it.ID = config.NewID()
	it.Enabled = true
	c.Items = append(c.Items, it)
	if m.save(c) {
		m.printf("Saved %q. The service picks up changes within a few seconds.\n", it.Name)
		m.notice(c, fmt.Sprintf("➕ Item added: %s\n%s", it.Name, describe(it)))
	}
}

func (m *menu) newWebItem() (config.Item, bool) {
	w := &config.WebItem{}
	name := m.ask("Name: ")
	w.URL = m.ask("Page URL: ")
	if name == "" || !(strings.HasPrefix(w.URL, "http://") || strings.HasPrefix(w.URL, "https://")) {
		m.printf("A name and an http(s) URL are required.\n")
		return config.Item{}, false
	}
	m.printf("Match by:\n 1) keyword (text in the page)\n 2) CSS selector (page element)\n")
	switch m.choose("> ", 2) {
	case 1:
		w.Match = config.MatchKeyword
		w.Value = m.ask("Keyword: ")
	case 2:
		w.Match = config.MatchSelector
		w.Value = m.ask("CSS selector: ")
		w.ValueText = m.ask("Element text must contain (blank = any): ")
	default:
		return config.Item{}, false
	}
	if w.Value == "" {
		m.printf("A keyword or selector is required.\n")
		return config.Item{}, false
	}
	m.printf("When it is found on the page, the item is:\n 1) in stock\n 2) out of stock\n")
	switch m.choose("> ", 2) {
	case 1:
		w.PresentMeans = config.MeansInStock
	case 2:
		w.PresentMeans = config.MeansOutOfStock
	default:
		return config.Item{}, false
	}
	it := config.Item{Name: name, Type: config.TypeWeb, Web: w}

	m.printf("Testing the page now...\n")
	res, err := check.NewWeb(*w, m.o.HTTP).Check(m.ctx)
	if err != nil {
		m.printf("Test failed: %v\n", err)
		return it, m.yes("Save anyway?")
	}
	m.printf("Now: %s (%s)\n", label(res.Status), res.Detail)
	if res.Status == check.InStock && w.PresentMeans == config.MeansOutOfStock {
		m.printf("This page currently reads IN STOCK. If the item is really sold out, the keyword/selector isn't in the raw HTML (the page may need JavaScript).\n")
		return it, m.yes("Keep this item?")
	}
	return it, true
}

func (m *menu) newMisakaItem(c *config.Config) (config.Item, bool) {
	mi := &config.MisakaItem{AutoOrder: config.AutoOrder{BillingCycle: "monthly"}}
	link := m.ask("Misaka console link, e.g. https://app.misaka.io/iaas/vm/create/nrt04/s3n-1c1g/debian-13\n(blank to type region/plan/image): ")
	if link != "" {
		r, p, i, err := misaka.ParseConsoleURL(link)
		if err != nil {
			m.printf("%v\n", err)
			return config.Item{}, false
		}
		mi.Region, mi.Plan, mi.Image = r, p, i
	} else {
		mi.Region = m.ask("Region slug (e.g. nrt04): ")
		mi.Plan = m.ask("Plan slug (e.g. s3n-1c1g): ")
	}
	if mi.Region == "" || mi.Plan == "" {
		m.printf("Region and plan are required.\n")
		return config.Item{}, false
	}
	if mi.Image == "" {
		mi.Image = m.ask("OS image slug for auto-order (e.g. debian-13, blank = none): ")
	}
	name := m.askDefault("Name", fmt.Sprintf("Misaka %s %s", strings.ToUpper(mi.Region), mi.Plan))
	it := config.Item{Name: name, Type: config.TypeMisaka, Misaka: mi}
	m.testMisaka(c, it)
	return it, true
}

func (m *menu) testMisaka(c *config.Config, it config.Item) {
	if c.Misaka.Username == "" || c.Misaka.Password == "" {
		m.printf("Set your Misaka login (main menu 6) to test this item.\n")
		return
	}
	m.printf("Checking Misaka now...\n")
	res, err := check.NewMisaka(*it.Misaka, m.misakaClient(c)).Check(m.ctx)
	if err != nil {
		m.printf("Test failed: %s\n", m.redactor(c).Err(err))
		return
	}
	m.printf("Now: %s (%s)\n", label(res.Status), res.Detail)
}

func label(s check.Status) string {
	if s == check.InStock {
		return "IN STOCK"
	}
	return "OUT OF STOCK"
}

func (m *menu) testItem(c *config.Config) {
	i := m.pickItem(c, nil)
	if i < 0 {
		return
	}
	it := c.Items[i]
	if it.Type == config.TypeMisaka {
		m.testMisaka(c, it)
		return
	}
	res, err := check.NewWeb(*it.Web, m.o.HTTP).Check(m.ctx)
	if err != nil {
		m.printf("Test failed: %v\n", err)
		return
	}
	m.printf("Now: %s (%s)\n", label(res.Status), res.Detail)
}

func (m *menu) editItem(c *config.Config) {
	i := m.pickItem(c, nil)
	if i < 0 {
		return
	}
	it := &c.Items[i]
	name := it.Name
	toggle := "Disable"
	if !it.Enabled {
		toggle = "Enable"
	}
	var msg string
	m.printf(" 1) %s\n 2) Rename\n 3) Remove\n 4) Edit what is checked\n 0) Back\n", toggle)
	switch m.choose("> ", 4) {
	case 1:
		it.Enabled = !it.Enabled
		msg = "⏸ Item disabled: " + name
		if it.Enabled {
			msg = "▶️ Item enabled: " + name
		}
	case 2:
		it.Name = m.askDefault("New name", it.Name)
		msg = fmt.Sprintf("✏️ Item renamed: %s → %s", name, it.Name)
	case 3:
		if !m.yes(fmt.Sprintf("Remove %q?", it.Name)) {
			return
		}
		c.Items = append(c.Items[:i], c.Items[i+1:]...)
		msg = "🗑 Item removed: " + name
	case 4:
		if it.Web != nil {
			it.Web.URL = m.askDefault("Page URL", it.Web.URL)
			it.Web.Value = m.askDefault("Keyword / selector", it.Web.Value)
			if it.Web.Match == config.MatchSelector {
				it.Web.ValueText = m.askDefault("Element text must contain", it.Web.ValueText)
			}
			msg = fmt.Sprintf("✏️ Item changed: %s\n%s", name, describe(*it))
		} else {
			before := *it.Misaka
			it.Misaka.Region = m.askDefault("Region", it.Misaka.Region)
			it.Misaka.Plan = m.askDefault("Plan", it.Misaka.Plan)
			it.Misaka.Image = m.askDefault("Image", it.Misaka.Image)
			changed := before.Region != it.Misaka.Region || before.Plan != it.Misaka.Plan || before.Image != it.Misaka.Image
			if changed && it.Misaka.AutoOrder.Enabled {
				it.Misaka.AutoOrder.Enabled = false
				m.printf("Auto-order was disarmed because what it would order changed. Re-arm in menu 7.\n")
			}
			msg = fmt.Sprintf("✏️ Item changed: %s\n%s", name, describe(*it))
			if changed && !it.Misaka.AutoOrder.Enabled && before.AutoOrder.Enabled {
				msg += "\nAuto-order was disarmed; re-arm it in the menu."
			}
		}
	default:
		return
	}
	if m.save(c) {
		m.printf("Saved.\n")
		m.notice(c, msg)
	}
}
