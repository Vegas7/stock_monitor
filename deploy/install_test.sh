#!/usr/bin/env bash
# Exercises install.sh install/update/uninstall against a fake local release.
# Runs on Linux (CI or a golang container); never touches real system paths.
set -euo pipefail
cd "$(dirname "$0")/.."

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "unsupported arch" >&2; exit 1 ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

make_release() { # $1 = version, $2 = dir
  local v=$1 rel=$2
  mkdir -p "$rel"
  CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -trimpath -ldflags "-s -w -X main.version=$v" -o "$rel/stockmon_linux_$ARCH" ./cmd/stockmon
  cp deploy/stockmon.service "$rel/stockmon.service"
  (cd "$rel" && sha256sum "stockmon_linux_$ARCH" stockmon.service > checksums.txt)
  cat > "$rel/latest.json" <<JSON
{"tag_name": "$v", "assets": [
  {"name": "stockmon_linux_$ARCH", "browser_download_url": "file://$rel/stockmon_linux_$ARCH"},
  {"name": "stockmon.service", "browser_download_url": "file://$rel/stockmon.service"},
  {"name": "checksums.txt", "browser_download_url": "file://$rel/checksums.txt"}
]}
JSON
}

run_installer() { # $1 = release dir, $2 = prefix, rest = args
  local rel=$1 prefix=$2
  shift 2
  STOCKMON_RELEASE_API="file://$rel/latest.json" STOCKMON_PREFIX="$prefix" STOCKMON_SKIP_SYSTEMD=1 bash install.sh "$@"
}

root="$tmp/root"
make_release v0.0.1 "$tmp/r1"
run_installer "$tmp/r1" "$root" install
[ "$("$root/usr/local/bin/stockmon" version)" = v0.0.1 ] || fail "installed version"
[ "$(stat -c %a "$root/etc/stockmon")" = 2750 ] || fail "config dir mode $(stat -c %a "$root/etc/stockmon")"
[ "$(stat -c %a "$root/var/lib/stockmon")" = 750 ] || fail "state dir mode"
[ -f "$root/etc/systemd/system/stockmon.service" ] || fail "unit not installed"
echo "ok: install"

make_release v0.0.2 "$tmp/r2"
run_installer "$tmp/r2" "$root" update
[ "$("$root/usr/local/bin/stockmon" version)" = v0.0.2 ] || fail "updated version"
[ "$("$root/usr/local/bin/stockmon.prev" version)" = v0.0.1 ] || fail "previous version kept"
run_installer "$tmp/r2" "$root" update | grep -q "up to date" || fail "second update should be a no-op"
echo "ok: update"

bad="$tmp/badroot"
make_release v0.0.3 "$tmp/r3"
sed -i '1s/^./0/; 1s/^\(.\)./\10/' "$tmp/r3/checksums.txt"
if run_installer "$tmp/r3" "$bad" install; then fail "install with bad checksum succeeded"; fi
[ ! -e "$bad/usr/local/bin/stockmon" ] || fail "binary left after bad checksum"
echo "ok: checksum mismatch rejected"

printf 'no\n' | run_installer "$tmp/r2" "$root" uninstall
[ -x "$root/usr/local/bin/stockmon" ] || fail "declined uninstall removed the binary"
printf 'yes\nn\n' | run_installer "$tmp/r2" "$root" uninstall
[ ! -e "$root/usr/local/bin/stockmon" ] || fail "binary still there"
[ ! -e "$root/usr/local/bin/stockmon.prev" ] || fail "prev still there"
[ ! -e "$root/etc/systemd/system/stockmon.service" ] || fail "unit still there"
[ -d "$root/etc/stockmon" ] || fail "config dir should be kept"
echo "ok: uninstall"
echo "ALL PASS"
