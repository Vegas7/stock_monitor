package menu

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
)

func (m *menu) autoOrder(c *config.Config) {
	i := m.pickItem(c, func(it config.Item) bool { return it.Type == config.TypeMisaka })
	if i < 0 {
		return
	}
	id := c.Items[i].ID
	for !m.eof {
		c, err := m.load()
		if err != nil {
			m.printf("Config error: %v\n", err)
			return
		}
		idx := -1
		for j := range c.Items {
			if c.Items[j].ID == id {
				idx = j
			}
		}
		if idx < 0 {
			return
		}
		it := &c.Items[idx]
		ao := &it.Misaka.AutoOrder
		st := m.snapshot()
		armed := ao.Enabled && ao.ArmID != "" && !st.ArmUsed(ao.ArmID)
		m.printf("\nAuto-order for %s: %s\n  billing %s, ssh keys %v, hostname %q, coupon %q\n", it.Name,
			map[bool]string{true: "ARMED", false: "disarmed"}[armed], ao.BillingCycle, ao.SSHKeyIDs, ao.Hostname, ao.Coupon)
		m.printf(" 1) Order settings\n 2) Arm\n 3) Disarm\n 4) Price check (no order)\n 0) Back\n")
		switch m.choose("> ", 4) {
		case 0:
			return
		case 1:
			m.orderSettings(c, it)
		case 2:
			if it.Misaka.Image == "" {
				m.printf("Set an OS image first (main menu 3 → Edit).\n")
				continue
			}
			m.printf("When %s is in stock, stockmon will immediately place ONE order (unpaid invoice) and then disarm.\n", it.Misaka.Plan)
			if m.ask("Type yes to arm: ") != "yes" {
				m.printf("Not armed.\n")
				continue
			}
			ao.Enabled = true
			ao.ArmID = config.NewArmID()
			if m.save(c) {
				m.printf("Armed. The service picks this up within a few seconds.\n")
			}
		case 3:
			ao.Enabled = false
			if m.save(c) {
				m.printf("Disarmed.\n")
			}
		case 4:
			m.priceCheck(c, it)
		}
	}
}

func (m *menu) findPlan(c *config.Config, mi *config.MisakaItem) (*misaka.Plan, error) {
	cl := m.misakaClient(c)
	regions, err := cl.Regions(m.ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range regions {
		if strings.EqualFold(r.Slug, mi.Region) {
			plans, err := cl.Plans(m.ctx, r.ID)
			if err != nil {
				return nil, err
			}
			for _, p := range plans {
				if strings.EqualFold(p.Slug, mi.Plan) {
					return &p, nil
				}
			}
			return nil, fmt.Errorf("plan %q not found in %s", mi.Plan, mi.Region)
		}
	}
	return nil, fmt.Errorf("region %q not found", mi.Region)
}

func (m *menu) orderSettings(c *config.Config, it *config.Item) {
	ao := &it.Misaka.AutoOrder
	loggedIn := c.Misaka.Username != "" && c.Misaka.Password != ""
	var plan *misaka.Plan
	if loggedIn {
		p, err := m.findPlan(c, it.Misaka)
		if err != nil {
			m.printf("Couldn't load plan details: %s\n", m.redactor(c).Err(err))
		}
		plan = p
	}
	if plan != nil && len(plan.Prices) > 0 {
		cycles := make([]string, 0, len(plan.Prices))
		for k := range plan.Prices {
			cycles = append(cycles, k)
		}
		sort.Slice(cycles, func(a, b int) bool { return plan.Prices[cycles[a]] < plan.Prices[cycles[b]] })
		for n, cy := range cycles {
			m.printf(" %d) %s — $%g\n", n+1, cy, plan.Prices[cy])
		}
		if n := m.choose("Billing cycle (0 = keep "+ao.BillingCycle+"): ", len(cycles)); n > 0 {
			ao.BillingCycle = cycles[n-1]
		}
	} else {
		ao.BillingCycle = m.askDefault("Billing cycle (monthly, quarterly, semiannual, annual)", ao.BillingCycle)
	}

	var keys []misaka.SSHKey
	if loggedIn {
		ks, err := m.misakaClient(c).SSHKeys(m.ctx)
		if err != nil {
			m.printf("Couldn't list SSH keys: %s\n", m.redactor(c).Err(err))
		}
		keys = ks
	}
	if len(keys) > 0 {
		for n, k := range keys {
			m.printf(" %d) %s\n", n+1, k.Name)
		}
		v := m.askDefault("SSH keys to install (numbers, comma separated; - for none)", strings.Join(keyNums(keys, ao.SSHKeyIDs), ","))
		ao.SSHKeyIDs = []string{}
		if v != "-" {
			for _, part := range strings.Split(v, ",") {
				n, err := strconv.Atoi(strings.TrimSpace(part))
				if err == nil && n >= 1 && n <= len(keys) {
					ao.SSHKeyIDs = append(ao.SSHKeyIDs, keys[n-1].ID.String())
				}
			}
		}
	} else {
		v := m.askDefault("SSH key IDs (comma separated; - for none)", strings.Join(ao.SSHKeyIDs, ","))
		ao.SSHKeyIDs = []string{}
		if v != "-" {
			for _, part := range strings.Split(v, ",") {
				if p := strings.TrimSpace(part); p != "" {
					ao.SSHKeyIDs = append(ao.SSHKeyIDs, p)
				}
			}
		}
	}
	ao.Hostname = m.askDefault("Server name (hostname)", ao.Hostname)
	ao.Coupon = m.askDefault("Coupon code (- for none)", ao.Coupon)
	if ao.Coupon == "-" {
		ao.Coupon = ""
	}
	if m.save(c) {
		m.printf("Saved order settings.\n")
	}
}

func keyNums(keys []misaka.SSHKey, ids []string) []string {
	var out []string
	for n, k := range keys {
		for _, id := range ids {
			if k.ID.String() == id {
				out = append(out, strconv.Itoa(n+1))
			}
		}
	}
	return out
}

func (m *menu) priceCheck(c *config.Config, it *config.Item) {
	if c.Misaka.Username == "" || c.Misaka.Password == "" {
		m.printf("Set your Misaka login first (main menu 6).\n")
		return
	}
	plan, err := m.findPlan(c, it.Misaka)
	if err != nil {
		m.printf("Price check failed: %s\n", m.redactor(c).Err(err))
		return
	}
	ao := it.Misaka.AutoOrder
	res, err := m.misakaClient(c).CalculatePrice(m.ctx, misaka.PriceRequest{Plan: plan.ID, Count: 1, BillingCycle: ao.BillingCycle, Coupon: ao.Coupon})
	if err != nil {
		m.printf("Price check failed: %s\n", m.redactor(c).Err(err))
		return
	}
	b, _ := json.MarshalIndent(res, "  ", "  ")
	m.printf("Login, plan and billing cycle OK. Misaka quotes:\n  %s\n(No order was placed.)\n", b)
}
