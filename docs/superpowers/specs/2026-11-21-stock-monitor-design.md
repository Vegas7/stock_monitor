# stockmon — Design

Date: 2026-11-21
Status: approved 2026-11-21

## 1. Intent

Misaka's Tokyo VPS plan (region `nrt04`, plan `s3n-1c1g`, image `debian-13`, about $10/month) sells out, and restocks don't last long. The owner wants the plan ordered the moment it comes back. The service should place the order itself so that Misaka issues an unpaid invoice. Then it sends a Telegram message, and the owner pays the invoice by hand.

It's also a general stock monitor. Any web page can be watched for a keyword or a page element that means "in stock" or "out of stock", with a Telegram alert when that changes.

It runs on a small VPS, so memory use matters a lot. It's set up and managed through a menu in the terminal on the VPS.

## 2. Goals

- A long-running service on a Debian VPS, run by systemd. Target memory use is under 20 MB RSS.
- Each item is checked at a **random interval between 30 and 60 seconds**. The random wait is drawn fresh for every check, and each item has its own.
- Telegram notifications for: in stock, sold out again, order placed (with the invoice link), **any failure** (as a warning), and recovery after a failure.
- An interactive terminal menu to:
  - add, edit, remove, and test monitored items;
  - set up the Telegram bot and chat ID and send a test message;
  - enter the Misaka email and password and test the login;
  - configure auto-order, turn it on or off, and run a "price check" that does a dry run without buying;
  - **update** the program to the latest release, and **uninstall** it cleanly.
- Two kinds of item:
  1. **Misaka plan**: availability comes from Misaka's API, and auto-order is supported.
  2. **Web page**: a keyword or CSS selector, plus whether finding it means in stock or out of stock.
- Auto-order (Misaka only): when an armed item goes from out of stock to in stock, place **exactly one** order that produces an unpaid invoice, send the invoice link on Telegram, then disarm.

## 3. Non-goals

- Paying invoices, or handling any payment details.
- Rendering JavaScript or running a headless browser (too much memory). Web-page items only see the raw HTML.
- A web UI, multiple users, or a database.
- Auto-ordering for web-page items. Only Misaka gets auto-order for now. The code should leave room for other providers later.
- Two-factor login. The owner's Misaka account doesn't use 2FA.

## 4. Anti-goals (failure even if it "works")

- **Any personal data in the public GitHub repo.** That includes email, password, tokens, Telegram bot token, chat ID, the real config or state files, and logs. The repo holds code, docs, and an example config with placeholder values only.
- **More than one order per arming.** This includes duplicates caused by retries, restarts, or timeouts.
- **Failing silently.** A monitor that has stopped working but looks fine.
- **Hammering Misaka.** No checks faster than every 30 seconds, and no bursts of parallel requests.
- Secrets showing up in logs, Telegram messages, or error text.

## 5. Constraints and known facts

- The repo `github.com/Vegas7/stock_monitor` is **public**.
- The target VPS runs Debian and may have only 1 GB of RAM.
- Misaka's **official API** (docs.misaka.io, v3alpha, `https://api.misaka.io/v3/...`) has no endpoints for regions, plans, stock, images, orders, or invoices. It can't be used for this.
- We'll use the **same internal API the Misaka web console uses** (`app.misaka.io`). This was worked out by reading the console's public JavaScript, without logging in. It's unofficial and Misaka can change it without warning. Their docs ask scripts to use API tokens, and those tokens don't cover ordering, so a script logging in like the website is a gray area. We keep request rates modest because of that.

What the console code shows (paths are relative to the console's API base, which is believed to be `https://app.misaka.io/api/`):

| Purpose | Call | Notes |
|---|---|---|
| Login | `POST session/login` body `{username, password}` | No `Authorization` header on `session/*`. The response has `user` and either `access_token` + `refresh_token` or `sliding_token`. |
| Refresh | `POST session/refresh` body `{refresh}` | Returns `access` and sometimes a new `refresh`. |
| Expired token | Any response with JSON `code: "token_not_valid"` | Refresh once, then retry. If refresh fails, log in again. |
| Regions | `GET mc2/regions` | Each has `id`, `slug`, `available`, `unavailable_reason`. |
| Plans | `GET mc2/regions/{regionId}/plans` | Each has `id`, `slug`, `name`, `available`, `unavailable_reason`, `tags`, `price_<cycle>`. |
| Images | `GET mc2/images?type=os&all=yes` | Find the image by slug (e.g. `debian-13`). |
| SSH keys | `GET mc2/sshkeys/` | A paged list. |
| Price check | `POST mc2/instances/calculate_price/` body `{plan, count, billing_cycle, coupon?}` | Places no order. Good for a dry run. |
| Order | `POST mc2/instances/` body `{region, plan, image, count, keys, billing_cycle, config:[{name}], coupon, issue_invoice}` | The response has `orders` (ids) and/or `invoice_id`. With too little account credit and `issue_invoice` false, the error has `internal_code: "insufficient_funds"`. The console then retries with `issue_invoice: true`. |

- Headers the console sends: `Accept` and `Content-Type: application/json`, `Authorization: Bearer <access>` (not on `session/*`), `X-Misaka-User-Agent`, `X-Misaka-User-Time` (ISO time), `X-Misaka-User-TZ`, and `X-Misaka-User-Locale`.
- Billing cycle values seen: `monthly`, `semiannual`, `annual`, and likely `quarterly`. Prices come from `price_<cycle>` on the plan.
- Useful console links: invoice at `https://app.misaka.io/billing/invoice/{invoice_id}`, order wait page at `https://app.misaka.io/iaas/vm/create/wait/{orderIds,comma-separated}`.
- **Not yet checked against a real login:** the exact API base path, the login response shape, how long tokens last, whether the `X-Misaka-*` headers are required, and whether `issue_invoice: true` skips account credit. These get checked live with the owner during the build (see §11).

## 6. Approaches considered

| Approach | Memory | Trade-off | Verdict |
|---|---|---|---|
| **A. A single Go program** (one static file that is both the service and the menu) | ~10–15 MB | Needs a rebuild to change, not edited on the server. Good HTTP, JSON, and CSS-selector libraries. Nothing to install on the VPS. | **Chosen.** This was my call: the owner put memory first and didn't pick. |
| B. A Python script using only the standard library | ~25–40 MB | Can be edited on the VPS. Matching CSS selectors without extra packages is clumsy. | Rejected because of memory. |
| C. Shell scripts with cron, curl, and jq | Near zero between runs | Login, token refresh, ordering, and menu state get fragile. | Rejected. |
| D. A headless browser (Chromium/Playwright) | 200–400 MB | Works on JavaScript pages, but goes against the memory goal. | Rejected. |

The owner chose **automatic ordering that produces an unpaid invoice** over a Telegram "order now" button. Speed matters more because restocks sell out within minutes.

## 7. Design

### 7.1 Program and files

One program, `stockmon`:

- `stockmon` or `stockmon menu`: the interactive menu.
- `stockmon run`: the service (run by systemd).
- `stockmon version`.
- `stockmon update [--check]`: update to the latest release (§7.9).
- `stockmon uninstall`: remove everything (§7.9).
- A global `--config <path>` option. The default is `/etc/stockmon/config.json`.

| File | Written by | Contents | Permissions |
|---|---|---|---|
| `/etc/stockmon/config.json` | menu only | Items, Telegram bot token and chat ID, Misaka email and password, auto-order settings, general settings | owned by `root:stockmon`, mode `0640`; directory `0750` |
| `/var/lib/stockmon/state.json` | service only | Last status per item, failure counters, last-warning times, Misaka tokens, used arm IDs, order history | owned by `stockmon`, mode `0600` |

Each file has exactly one writer. Writes are atomic: write a temporary file, fsync it, then rename it into place. Each check cycle, the service looks at the config file's modification time and reloads it when it has changed. If the new config is invalid, the service keeps the old one and sends a Telegram warning. The menu reads `state.json` to show status.

The repo has `config.example.json` with placeholder values. `.gitignore` excludes `config.json`, `state.json`, `*.local.*`, and build output.

### 7.2 Config shape (illustrative)

```json
{
  "telegram": { "bot_token": "", "chat_id": "" },
  "misaka":   { "username": "", "password": "" },
  "settings": { "interval_min_sec": 30, "interval_max_sec": 60,
                "fail_warn_after": 3, "fail_remind_minutes": 60,
                "daily_heartbeat_hour": 9, "timezone": "Asia/Tokyo" },
  "items": [
    { "id": "a1b2", "name": "Misaka Tokyo 1C1G", "type": "misaka_plan", "enabled": true,
      "misaka": { "region": "nrt04", "plan": "s3n-1c1g", "image": "debian-13",
                  "auto_order": { "enabled": true, "arm_id": "r4nd0m", "billing_cycle": "monthly",
                                  "ssh_key_ids": [], "hostname": "tokyo-1", "coupon": "" } } },
    { "id": "c3d4", "name": "Some shop item", "type": "web", "enabled": true,
      "web": { "url": "https://...", "match": "keyword", "value": "Sold out",
               "present_means": "out_of_stock" } }
  ]
}
```

The interval is enforced to be at least 30 seconds, and the maximum must be at least the minimum.

### 7.3 Checkers

**Misaka plan**

1. Make sure there's a session. Use the access token from state. If there isn't one, log in.
2. Resolve slugs to IDs: region from `mc2/regions`, plan from `mc2/regions/{id}/plans`, image from `mc2/images`. Slugs are matched case-insensitively. IDs are cached in memory and resolved again when a call fails.
3. Each check calls `GET mc2/regions/{regionId}/plans` and finds the plan. The item is **in stock** when both the plan's and the region's `available` are true. Otherwise it's **out of stock**, and the `unavailable_reason` is recorded.
4. If the plan or region isn't in the list, or a field is missing or has the wrong type, that's an **error** ("response format changed"), never "out of stock".
5. On `token_not_valid`: refresh, then log in again, then fail. All Misaka items share one session, protected by a mutex so only one login happens at a time.
6. A failed login warns immediately. A wrong password won't fix itself, and retrying risks an account lockout. After a login failure, retries back off: 5 minutes, then 15, then 60, until the config changes.

**Web page**

- `GET` the URL with a 20-second timeout and a normal browser User-Agent. At most 2 MB of the body is read, to bound memory.
- `match: "keyword"`: a case-insensitive substring search of the HTML. `match: "selector"`: a CSS selector (goquery). If `value_text` is set, the element's text must also contain it.
- `present_means: "in_stock" | "out_of_stock"` decides what a match means.
- A non-2xx response, a timeout, or a body that couldn't be parsed counts as an error.
- Pages built by JavaScript won't work. The menu's "test" shows the HTTP status, page size, and whether the rule matched, so the owner can see this.

### 7.4 Scheduling

Each enabled item runs in its own goroutine. After each check it waits a random time drawn uniformly from `[interval_min_sec, interval_max_sec]`, fresh every time. The first check after startup gets a random 0–10 second offset so items don't fire together. Config reloads start, stop, and restart item loops as needed.

### 7.5 Status, notifications, warnings

Each item has a status of `in_stock`, `out_of_stock`, or `error`, plus a count of consecutive failures.

| Event | Telegram |
|---|---|
| Service starts | "stockmon started: N items" (and "after a restart" when the last shutdown wasn't clean) |
| `out_of_stock` → `in_stock` (or the first check finds it in stock) | 🟢 **IN STOCK**: name, link, reason cleared |
| `in_stock` → `out_of_stock` | ⚪ sold out again |
| `fail_warn_after` failures in a row (default 3, about 1.5–3 minutes) | ⚠️ warning: item, error summary (secrets removed) |
| Still failing | A reminder every `fail_remind_minutes` (default 60) |
| First success after a warning | ✅ recovered |
| Misaka login failure, response format changed, config reload failed | ⚠️ sent **immediately** |
| Any order failure or unclear order result | 🚨 sent **immediately** |
| Order placed | 🛒 order placed: order IDs, **invoice link**, "pay it to get the server" |
| Daily heartbeat (at `daily_heartbeat_hour`) | 💓 a one-line summary of each item's status. A missing heartbeat tells the owner the service is down. |

A Telegram send that fails is retried 3 times with backoff, then logged to the systemd journal. Telegram failures can't be reported on Telegram. The heartbeat and journal cover that case.

Errors are passed through a redaction function before logging or sending. It removes the password, tokens, and the bot token.

### 7.6 Auto-order (Misaka only)

**Arming.** Auto-order is armed when `auto_order.enabled` is true **and** the item's `arm_id` isn't in the state file's `used_arm_ids`. Each time the menu turns auto-order on, it writes a fresh random `arm_id`. That's how the owner re-arms after an order.

**Trigger.** A check finds the item in stock while it's armed. This applies to the first check after startup too.

**Steps:**
1. Before sending anything, write `{arm_id, status: "placing", at}` to state and fsync. The arm is now used.
2. `POST mc2/instances/` with `{region, plan, image, count: 1, keys: ssh_key_ids, billing_cycle, config: [{name: hostname}], coupon (omitted if empty), issue_invoice: true}`.
3. On success, record the order IDs and invoice ID and send 🛒 with `https://app.misaka.io/billing/invoice/{invoice_id}` (or the order wait page if there's no invoice ID).
4. On a clear error (4xx with a message, such as sold out again or invalid key), send 🚨 with the message. **The arm stays used and nothing is retried.** The owner re-arms from the menu.
5. On an unclear result (timeout, 5xx, or a response that can't be parsed), send 🚨 "order status unknown, check the console". **No retry.** A duplicate order is worse than a missed one.
6. After a restart, any entry still marked `placing` is treated as unclear (step 5).

`issue_invoice: true` is sent from the start because the owner pays invoices by hand. It's still unchecked whether Misaka uses existing account credit even then (§11).

**Price check (dry run)** in the menu: `POST mc2/instances/calculate_price/` with the item's plan, billing cycle, count, and coupon. It shows the price and checks that login, the plan, and the billing cycle are valid, without ordering.

### 7.7 The menu

It uses plain numbered prompts, which work fine over SSH. Passwords and tokens are typed hidden (`golang.org/x/term`).

```
stockmon v0.1.0 — service: running — 2 items
 1) Show items and status
 2) Add item
 3) Edit / enable / disable / remove item
 4) Test an item now
 5) Telegram: set bot & chat ID, send test
 6) Misaka account: set login, test login
 7) Auto-order: settings, arm/disarm, price check
 8) Settings (interval, warnings, heartbeat)
 9) Update / uninstall
 0) Exit
```

- **Update / uninstall.** Shows the installed version and the latest release. Offers "update now" or "uninstall", using the same code as `stockmon update` and `stockmon uninstall` (§7.9).

- **Add Misaka item.** Paste a console URL such as `https://app.misaka.io/iaas/vm/create/nrt04/s3n-1c1g/debian-13`, and region, plan, and image are read from it. Or enter them by hand. The menu then runs a live test: it logs in, resolves the IDs, and shows whether the plan is available right now.
- **Add web item.** Enter the URL, then keyword or selector, then the value, then whether a match means in stock or out of stock. A live test runs right away.
- **Telegram.** Enter the bot token, which is checked with `getMe`. Then the menu asks the owner to send `/start` to the bot, reads `getUpdates` to find the chat ID automatically (manual entry also works), and sends a test message.
- **Misaka account.** Enter the email and password (hidden), then test the login. The menu reports success or failure and never prints the token.
- **Auto-order.** Choose the billing cycle (from the plan's prices), SSH keys (listed from the account, zero or more), hostname, and coupon. Then arm or disarm it, or run a price check. Arming asks for confirmation and spells out what happens next.
- Service status comes from `systemctl is-active stockmon`. The menu doesn't need the service to be running.

### 7.8 Deployment

- GitHub Actions builds static Linux binaries for `amd64` and `arm64` (`CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`) when a `v*` tag is pushed, and attaches them to a GitHub Release.
- `install.sh` in the repo, run with sudo on the VPS. It has three modes:
  - `install.sh` (or `install.sh install`):
    1. downloads the latest release binary for the machine's architecture and checks its SHA-256 against the release's `checksums.txt`;
    2. creates the `stockmon` system user and the folders;
    3. installs the systemd unit;
    4. tells the owner to run `sudo stockmon menu` and then `systemctl enable --now stockmon`.

    If stockmon is already installed, this does the same as `update`.
  - `install.sh update`: the same steps as `stockmon update` (§7.9).
  - `install.sh uninstall`: the same steps as `stockmon uninstall` (§7.9).

  The script has its own update and uninstall so they still work if the binary is broken or missing.
- systemd unit: `Restart=always`, `RestartSec=10`, `User=stockmon`, `MemoryMax=64M`, `Environment=GOMEMLIMIT=24MiB`, plus the usual hardening (`ProtectSystem=strict`, `ReadWritePaths=/var/lib/stockmon`, `NoNewPrivileges`, `PrivateTmp`).
- Logs go to the systemd journal.
- `README.md` covers install, first-time setup, upgrade, uninstall, and the warning that the Misaka API is unofficial.

### 7.9 Update and uninstall

Both need root, so the owner runs them with `sudo` (from the menu, `stockmon update` / `stockmon uninstall`, or `install.sh update` / `install.sh uninstall`). Both work the same whichever way they're started.

**Update**
1. Get the latest release from the GitHub API (`repos/Vegas7/stock_monitor/releases/latest`). Show the installed and latest versions. Stop here if it's already current, or if `--check` was given.
2. Download the binary for this machine's architecture and `checksums.txt` to a temporary file in the same folder. Check the SHA-256. On a mismatch, stop and change nothing.
3. Keep the current binary as `/usr/local/bin/stockmon.prev`, then put the new one in place with an atomic rename.
4. If the service is running, restart it. The new version sends its own "stockmon started vX.Y.Z" message.
5. If the new version fails to start (the service isn't active 10 seconds after the restart), put `stockmon.prev` back, restart again, and say so on screen and on Telegram.
6. Config and state are never touched. If a future version changes the config format, it upgrades the config itself when it loads it.

**Uninstall**
1. Show what will be removed and ask for confirmation (type `yes`).
2. Ask separately whether to also delete config and state. These hold the Misaka password and the Telegram token, so the menu recommends deleting them. Keeping them makes reinstalling quicker.
3. Send a Telegram message "stockmon uninstalled from <hostname>" while the config still exists. If that fails, carry on anyway.
4. Stop and disable the service, then remove the systemd unit and run `systemctl daemon-reload`.
5. Remove `/usr/local/bin/stockmon` and `stockmon.prev`.
6. If chosen, remove `/etc/stockmon` and `/var/lib/stockmon`.
7. Remove the `stockmon` system user.
8. Print what was removed and what was kept.

**New-version notice** (my call): once a day, the service checks the latest release. The daily heartbeat adds a line "update available: vX → vY, run `sudo stockmon update`". Updates are never installed automatically.

### 7.10 Code layout (suggested)

```
cmd/stockmon/        main, subcommands
internal/config/     load/validate/atomic save, example config
internal/state/      atomic state store
internal/monitor/    scheduler, status machine, notifications policy
internal/check/      checker interface; web.go; misaka.go
internal/misaka/     API client (session, refresh, regions, plans, images, keys, price, order)
internal/telegram/   send, getMe, getUpdates
internal/menu/       interactive menu
internal/lifecycle/  update (release lookup, checksum, swap, rollback), uninstall
deploy/              stockmon.service, install.sh
```

Checkers implement one shared interface, `Check(ctx) (Status, detail, error)`. Ordering sits behind an `Orderer` interface so other providers can be added later.

## 8. Testing

- Unit tests use `httptest` fake servers for the Misaka API (login, refresh, expired token, available or unavailable, changed format, order success, clear error, timeout), for web pages (keyword, selector, present-means rules, 2 MB cap, non-2xx), and for Telegram.
- State machine tests cover: transitions and their messages, the warning after N failures, reminders, recovery, and the heartbeat.
- **Order-once tests:** a double trigger, a restart in the middle of an order, a timeout with no retry, re-arming through a new `arm_id`.
- A config test makes sure `config.example.json` holds no real-looking secrets, and that redaction removes them from error text.
- Update tests use a fake release server: already current, a newer version, a checksum mismatch (nothing changes), and a failed start that rolls back to `stockmon.prev`. Uninstall is tested on paths in a temporary folder, both keeping and deleting config, and runs no systemctl. `install.sh install/update/uninstall` is tried once on a throwaway Debian VM or container.
- A memory check: run the service against fake servers with about 10 items and confirm RSS stays under 20 MB.

## 9. What's left to the builder

- Library choices beyond goquery and x/term. Keep dependencies minimal.
- Log format, exact Telegram message wording, the menu's input checks.
- How to send the `X-Misaka-*` headers (copy what the console sends).
- Paging for `mc2/sshkeys/`.

## 10. Later

- Auto-ordering for other providers, behind the `Orderer` interface.
- Optional Telegram commands (`/status`, `/arm`, `/disarm`).

## 10a. Verified live (2026-11-21, owner's account, local test build)

- API base `https://app.misaka.io/api/` is correct. `POST session/login {username, password}` works; the access/refresh tokens are **inside `user`** (`user.access_token`, `user.refresh_token`), as the console's `Vt(e.user)` stores them, not at the top level.
- `GET mc2/regions` + `GET mc2/regions/{id}/plans` give availability: NRT04 `s3n-1c1g` read `available: false`, `unavailable_reason: "out_of_stock"`; MIA01 `s3n-1c1g` read available.
- `POST mc2/instances/calculate_price/` with `billing_cycle: "monthly"` works for an in-stock plan (response fields include `amount_due_today`, `recurring_price`, `start_date_text`, `end_date_text`; its `billing_cycle` echoes an internal code, e.g. `"7"`). For a sold-out plan it returns **HTTP 400 "Plan not available, please contact support."** — a clear error.
- Still unverified: the order request itself (`POST mc2/instances/`), SSH key ID format, and whether `issue_invoice: true` uses account credit. Token lifetime is covered by the multi-hour run.

## 11. Must be checked live with the owner during the build

The owner enters the password; the builder never types it. Using a test build with the owner's account:
1. The API base path, and that the login with `username` = email works. Note the response fields.
2. Token lifetime and refresh behavior. Leave it running for a few hours.
3. That `GET mc2/regions/{id}/plans` shows `available` for `s3n-1c1g` in `nrt04`.
4. That the price check (`calculate_price`) works for the chosen billing cycle.
5. That the order body is accepted. This can't be fully tested without ordering. Before arming, the owner decides whether to do one real test order on a cheap in-stock plan and cancel or leave the invoice unpaid. It's also when to learn whether `issue_invoice: true` uses account credit.
