package menu

import (
	"sync"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
	"github.com/Vegas7/stock_monitor/internal/redact"
)

// memTokens keeps menu sessions out of the service's state file.
type memTokens struct {
	mu sync.Mutex
	t  misaka.Tokens
}

func (s *memTokens) LoadTokens() misaka.Tokens { s.mu.Lock(); defer s.mu.Unlock(); return s.t }
func (s *memTokens) SaveTokens(t misaka.Tokens) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.t = t
	return nil
}

func (m *menu) misakaClient(c *config.Config) *misaka.Client {
	return misaka.New(m.o.MisakaBaseURL, c.Misaka.Username, c.Misaka.Password, &memTokens{}, m.o.HTTP)
}

func (m *menu) redactor(c *config.Config) *redact.Redactor {
	return redact.New(c.Misaka.Password, c.Telegram.BotToken)
}

func (m *menu) misakaAccount(c *config.Config) {
	user := m.askDefault("Misaka email", c.Misaka.Username)
	pw := m.secret("Misaka password (hidden; blank keeps the current one): ")
	if pw == "" {
		pw = c.Misaka.Password
	}
	if user == "" || pw == "" {
		m.printf("Email and password are both needed. Nothing saved.\n")
		return
	}
	c.Misaka = config.MisakaAccount{Username: user, Password: pw}
	if !m.save(c) {
		return
	}
	m.printf("Saved. Testing the login...\n")
	if err := m.misakaClient(c).Login(m.ctx); err != nil {
		m.printf("Login failed: %s\n", m.redactor(c).Err(err))
		return
	}
	m.printf("Login OK.\n")
}
