# stock_monitor (`stockmon`)

A small, low-memory stock monitor for a Linux VPS. It watches product pages,
sends alerts through your own Telegram bot, and for supported providers can
**automatically place one order** the moment an item comes back in stock.
The order is created as an **unpaid invoice** — you pay it yourself.

- One static Go program, ~10–15 MB of memory, no dependencies on the server.
- Checks each item at a random interval between 30 and 60 seconds.
- Telegram alerts: back in stock, sold out again, order placed (with invoice link),
  and a warning whenever something fails — plus a daily "still alive" summary.
- Set up entirely from a menu in the terminal.

## Install (Debian / Ubuntu)

```bash
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Vegas7/stock_monitor/main/install.sh)"
```

The script downloads the latest release for your CPU, checks its SHA-256, creates
a `stockmon` system user and installs a systemd service (not started yet).

## First-time setup

```bash
sudo stockmon menu
```

1. **5 — Telegram:** create a bot with @BotFather, paste its token, send `/start`
   to the bot, and stockmon finds your chat ID and sends a test message.
2. **6 — Account:** if you use a supported provider, enter your login; the menu
   tests it.
3. **2 — Add item:** paste a product link, or add a web page with a keyword / CSS
   selector and whether finding it means in stock or out of stock. Every new item
   is tested immediately.
4. **7 — Auto-order:** choose billing cycle, SSH keys, server name and coupon, run a
   **price check** (no order), then **arm** it.

Then start the service:

```bash
sudo systemctl enable --now stockmon
```

## How auto-order works

When an armed item goes from out of stock to in stock, stockmon immediately
places **one** order as an unpaid invoice, sends you the invoice link on
Telegram, and **disarms itself**. It never retries an order: if the result is
unclear (timeout, server error) you get an urgent message to check your account.
Re-arm from the menu when you want another order.

## Update

```bash
sudo stockmon update            # or: sudo stockmon update --check
```

The new version is checksum-verified, the previous one is kept as
`/usr/local/bin/stockmon.prev`, and if the service does not come back up it is
rolled back automatically. The daily summary tells you when an update exists.
Menu option 9 does the same.

## Uninstall

```bash
sudo stockmon uninstall
```

You will be asked whether to also delete the config and state (recommended — they
contain your password and bot token).

## Where things live

| Path | What |
|---|---|
| `/etc/stockmon/config.json` | your settings, items, **password and bot token** (root:stockmon, 0640) |
| `/var/lib/stockmon/state.json` | runtime state, session tokens, order history (0600) |
| `/usr/local/bin/stockmon` | the program |
| `journalctl -u stockmon -f` | logs |

Secrets only ever live on your server. This repository contains code and
`config.example.json` with empty placeholders — never commit a real config.

## Development

```bash
go test -race ./...
bash deploy/install_test.sh   # Linux only (or inside a golang container)
```

Releases are built by GitHub Actions when a `v*` tag is pushed.
