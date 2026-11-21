package menu

import (
	"strconv"

	"github.com/Vegas7/stock_monitor/internal/config"
)

func (m *menu) intDefault(prompt string, def int) int {
	v := m.askDefault(prompt, strconv.Itoa(def))
	n, err := strconv.Atoi(v)
	if err != nil {
		m.printf("%q is not a number; keeping %d.\n", v, def)
		return def
	}
	return n
}

func (m *menu) settings(c *config.Config) {
	s := &c.Settings
	s.IntervalMinSec = m.intDefault("Shortest wait between checks, seconds (min 30)", s.IntervalMinSec)
	s.IntervalMaxSec = m.intDefault("Longest wait between checks, seconds", s.IntervalMaxSec)
	s.FailWarnAfter = m.intDefault("Warn after this many failed checks in a row", s.FailWarnAfter)
	s.FailRemindMinutes = m.intDefault("Remind every N minutes while still failing", s.FailRemindMinutes)
	s.DailyHeartbeatHour = m.intDefault("Daily summary hour (0-23)", s.DailyHeartbeatHour)
	s.Timezone = m.askDefault("Time zone", s.Timezone)
	if err := c.Validate(); err != nil {
		m.printf("Invalid: %v — not saved.\n", err)
		return
	}
	if m.save(c) {
		m.printf("Saved.\n")
	}
}

func (m *menu) maintenance() {
	for !m.eof {
		m.screen()
		m.printf("\nInstalled version: %s\n 1) Update to the latest release\n 2) Uninstall stockmon\n 0) Back\n", m.o.Version)
		switch m.pick("> ", 2) {
		case 0:
			return
		case 1:
			if m.o.Update == nil {
				m.printf("Update isn't available here.\n")
				continue
			}
			if err := m.o.Update(m.ctx); err != nil {
				m.printf("Update failed: %v\n", err)
			}
		case 2:
			if m.o.Uninstall == nil {
				m.printf("Uninstall isn't available here.\n")
				continue
			}
			if err := m.o.Uninstall(m.ctx); err != nil {
				m.printf("Uninstall failed: %v\n", err)
				continue
			}
			m.eof = true // binary is gone; leave the menu
			return
		}
	}
}
