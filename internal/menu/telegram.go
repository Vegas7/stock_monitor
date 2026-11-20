package menu

import (
	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/telegram"
)

func (m *menu) tgClient(token string) *telegram.Client {
	tg := telegram.New(token)
	if m.o.TelegramBaseURL != "" {
		tg.BaseURL = m.o.TelegramBaseURL
	}
	return tg
}

func (m *menu) telegramSetup(c *config.Config) {
	m.printf("Create a bot with @BotFather in Telegram and copy its token.\n")
	token := m.secret("Bot token (hidden; blank keeps the current one): ")
	if token == "" {
		token = c.Telegram.BotToken
	}
	if token == "" {
		m.printf("No token given.\n")
		return
	}
	tg := m.tgClient(token)
	name, err := tg.GetMe(m.ctx)
	if err != nil {
		m.printf("That token didn't work: %v\nNothing saved.\n", err)
		return
	}
	m.printf("Bot OK: @%s\n", name)
	m.ask("Open Telegram, send /start to @" + name + ", then press Enter here.")

	chatID := ""
	chats, err := tg.Chats(m.ctx)
	switch {
	case err != nil:
		m.printf("Couldn't read messages: %v\n", err)
	case len(chats) == 1:
		chatID = chats[0].ID
		m.printf("Found chat: %s (%s)\n", chats[0].Name, chatID)
	case len(chats) > 1:
		for i, ch := range chats {
			m.printf(" %d) %s (%s)\n", i+1, ch.Name, ch.ID)
		}
		if n := m.choose("Which chat should receive alerts? ", len(chats)); n > 0 {
			chatID = chats[n-1].ID
		}
	}
	if chatID == "" {
		chatID = m.askDefault("Chat ID not found automatically. Enter it", c.Telegram.ChatID)
	}
	if chatID == "" {
		m.printf("No chat ID. Nothing saved.\n")
		return
	}
	c.Telegram = config.Telegram{BotToken: token, ChatID: chatID}
	if !m.save(c) {
		return
	}
	if err := tg.Send(m.ctx, chatID, "✅ stockmon test message — Telegram is connected."); err != nil {
		m.printf("Saved, but the test message failed: %v\n", err)
		return
	}
	m.printf("Saved. A test message was sent — check Telegram.\n")
}
