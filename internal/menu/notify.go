package menu

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Vegas7/stock_monitor/internal/config"
)

// notice tells Telegram about a change made in the menu. The change is already
// saved; a failed notice is only reported here.
func (m *menu) notice(c *config.Config, text string) {
	if text == "" || c.Telegram.BotToken == "" || c.Telegram.ChatID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	if err := m.tgClient(c.Telegram.BotToken).Send(ctx, c.Telegram.ChatID, text); err != nil {
		m.printf("(Telegram notice not sent: %s)\n", m.redactor(c).Err(err))
	}
}

// describe says what an item checks, for notices.
func describe(it config.Item) string {
	switch {
	case it.Misaka != nil:
		s := fmt.Sprintf("Plan %s in %s", it.Misaka.Plan, strings.ToUpper(it.Misaka.Region))
		if it.Misaka.Image != "" {
			s += ", image " + it.Misaka.Image
		}
		return s
	case it.Web != nil:
		return fmt.Sprintf("%s — %s %q", it.Web.URL, it.Web.Match, it.Web.Value)
	}
	return ""
}

func aoSummary(ao config.AutoOrder) string {
	keys, coupon := "none", "none"
	if len(ao.SSHKeyIDs) > 0 {
		keys = strings.Join(ao.SSHKeyIDs, ",")
	}
	if ao.Coupon != "" {
		coupon = ao.Coupon
	}
	return fmt.Sprintf("Billing %s, SSH keys %s, hostname %q, coupon %s", ao.BillingCycle, keys, ao.Hostname, coupon)
}
