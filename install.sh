#!/usr/bin/env bash
# stockmon installer for Debian/Ubuntu.
#   sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Vegas7/stock_monitor/main/install.sh)"              install (or update)
#   sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/Vegas7/stock_monitor/main/install.sh)" _ uninstall  remove stockmon
#   (or download it and run: sudo bash install.sh [install|update|uninstall])
# Testing overrides: STOCKMON_RELEASE_API, STOCKMON_PREFIX, STOCKMON_SKIP_SYSTEMD=1,
#   STOCKMON_SYSTEMCTL (a stand-in systemctl, used even with STOCKMON_SKIP_SYSTEMD=1)
set -euo pipefail

RELEASE_API="${STOCKMON_RELEASE_API:-https://api.github.com/repos/Vegas7/stock_monitor/releases/latest}"
PREFIX="${STOCKMON_PREFIX:-}"
SKIP_SYSTEMD="${STOCKMON_SKIP_SYSTEMD:-0}"

BIN="$PREFIX/usr/local/bin/stockmon"
PREV="$BIN.prev"
UNIT="$PREFIX/etc/systemd/system/stockmon.service"
CONF_DIR="$PREFIX/etc/stockmon"
STATE_DIR="$PREFIX/var/lib/stockmon"

die() { echo "error: $*" >&2; exit 1; }
say() { echo "==> $*"; }

[ -n "$PREFIX" ] || [ "$(id -u)" -eq 0 ] || die "run with sudo"
command -v curl >/dev/null || die "curl is required (apt install curl)"
command -v sha256sum >/dev/null || die "sha256sum is required"

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "unsupported CPU architecture: $(uname -m)" ;;
esac
ASSET="stockmon_linux_$ARCH"

SYSTEMCTL="${STOCKMON_SYSTEMCTL:-}"
systemd() {
  if [ -n "$SYSTEMCTL" ]; then
    "$SYSTEMCTL" "$@"
  elif [ "$SKIP_SYSTEMD" != 1 ]; then
    systemctl "$@"
  fi
}

RELEASE_JSON=""
release_json() {
  [ -n "$RELEASE_JSON" ] || RELEASE_JSON=$(curl -fsSL "$RELEASE_API") || die "could not reach $RELEASE_API"
  printf '%s' "$RELEASE_JSON"
}
latest_tag() { release_json | tr -d '\n' | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'; }
asset_url() {
  release_json | tr ',{}' '\n' | grep '"browser_download_url"' | grep "/$1\"" |
    sed -n 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

# download_verified DIR: fetches the binary, unit and checksums into DIR and verifies them.
download_verified() {
  local dir=$1 name url
  for name in "$ASSET" stockmon.service checksums.txt; do
    url=$(asset_url "$name")
    [ -n "$url" ] || die "release $(latest_tag) has no $name"
    curl -fsSL "$url" -o "$dir/$name" || die "download failed: $name"
  done
  (cd "$dir" && grep -E "  \*?($ASSET|stockmon\.service)\$" checksums.txt | sha256sum -c --quiet -) ||
    die "checksum mismatch; nothing was changed"
  chmod 0755 "$dir/$ASSET"
}

installed_version() { [ -x "$BIN" ] && "$BIN" version 2>/dev/null || echo none; }

install_unit() {
  mkdir -p "$(dirname "$UNIT")"
  install -m 0644 "$1/stockmon.service" "$UNIT"
  systemd daemon-reload
}

do_install() {
  if [ -x "$BIN" ]; then
    say "stockmon $(installed_version) is already installed; updating instead"
    do_update
    return
  fi
  local tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  say "Downloading stockmon $(latest_tag) for $ARCH"
  download_verified "$tmp"
  mkdir -p "$(dirname "$BIN")"
  install -m 0755 "$tmp/$ASSET" "$BIN"
  if [ "$SKIP_SYSTEMD" != 1 ] && ! id -u stockmon >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin stockmon
  fi
  # setgid so files the menu writes stay readable by the stockmon group
  install -d -m 2750 "$CONF_DIR"
  install -d -m 0750 "$STATE_DIR"
  if [ "$SKIP_SYSTEMD" != 1 ]; then
    chown root:stockmon "$CONF_DIR"
    chown stockmon:stockmon "$STATE_DIR"
  fi
  install_unit "$tmp"
  # Start now and on every boot. Until the menu saves a config the service just waits.
  systemd enable --now stockmon
  say "Installed stockmon $(installed_version); the service is running and starts on every boot."
  cat <<MSG

Set up with: sudo stockmon menu      (Telegram, account login, items, auto-order)
Changes from the menu reach the running service within a few seconds.
  Status: systemctl status stockmon
  Logs:   journalctl -u stockmon -f
MSG
  # Interactive install: go straight into setup.
  if [ -z "$PREFIX" ] && [ -t 0 ] && [ -t 1 ]; then
    say "Opening the setup menu..."
    "$BIN" menu || true
  fi
}

do_update() {
  [ -x "$BIN" ] || die "stockmon is not installed; run: sudo bash install.sh"
  local cur tag tmp new
  cur=$(installed_version)
  tag=$(latest_tag)
  [ -n "$tag" ] || die "could not read the latest release"
  if [ "$cur" = "$tag" ]; then
    say "Already up to date ($cur)."
    return
  fi
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  say "Updating $cur -> $tag"
  download_verified "$tmp"
  local was_active=0
  [ "$SKIP_SYSTEMD" != 1 ] && systemctl is-active --quiet stockmon && was_active=1
  cp -p "$BIN" "$PREV.tmp" && mv -f "$PREV.tmp" "$PREV"
  new="$(dirname "$BIN")/.stockmon-new"
  install -m 0755 "$tmp/$ASSET" "$new" && mv -f "$new" "$BIN"
  install_unit "$tmp"
  if [ "$was_active" = 1 ]; then
    systemctl restart stockmon
    local _
    for _ in $(seq 10); do
      systemctl is-active --quiet stockmon && break
      sleep 1
    done
    if ! systemctl is-active --quiet stockmon; then
      cp -p "$PREV" "$new" && mv -f "$new" "$BIN"
      systemctl restart stockmon || true
      die "$tag did not start; rolled back to $cur"
    fi
  fi
  say "Updated to $(installed_version)."
}

do_uninstall() {
  # Prefer the program's own uninstall (it also sends a Telegram goodbye).
  if [ -z "$PREFIX" ] && [ -x "$BIN" ] && "$BIN" version >/dev/null 2>&1; then
    exec "$BIN" uninstall
  fi
  local ans del
  read -r -p "This stops stockmon and removes the service and program. Type yes to continue: " ans || true
  if [ "$ans" != yes ]; then
    echo "Cancelled; nothing was changed."
    return
  fi
  read -r -p "Also delete config and state? They contain your account password and Telegram token (recommended) [Y/n]: " del || true
  systemd disable --now stockmon 2>/dev/null || true
  rm -f "$UNIT"
  systemd daemon-reload || true
  rm -f "$BIN" "$PREV"
  case "$del" in
    n | N | no | NO) echo "kept: $CONF_DIR $STATE_DIR" ;;
    *) rm -rf "$CONF_DIR" "$STATE_DIR"; echo "removed: $CONF_DIR $STATE_DIR" ;;
  esac
  if [ "$SKIP_SYSTEMD" != 1 ] && id -u stockmon >/dev/null 2>&1; then
    userdel stockmon || true
  fi
  say "stockmon uninstalled."
}

case "${1:-install}" in
  install) do_install ;;
  update)
    if [ -z "$PREFIX" ] && [ -x "$BIN" ] && "$BIN" version >/dev/null 2>&1; then
      exec "$BIN" update
    fi
    do_update
    ;;
  uninstall) do_uninstall ;;
  *) die "usage: install.sh [install|update|uninstall]" ;;
esac
