#!/bin/sh
# bandwave installer for Linux.
#   curl -fsSL https://raw.githubusercontent.com/KigoJomo/bandwave/main/install.sh | sh
#
# Puts bandwave in ~/.local/bin (override with BANDWAVE_BIN_DIR) and starts the
# agent at login so other PCs can see this one. Set BANDWAVE_NO_AGENT=1 to skip
# the agent.
set -eu

REPO="KigoJomo/bandwave"
BIN_DIR="${BANDWAVE_BIN_DIR:-$HOME/.local/bin}"

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf 'bandwave install: %s\n' "$*" >&2; exit 1; }

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) arch="" ;;
esac

mkdir -p "$BIN_DIR"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

url="https://github.com/$REPO/releases/latest/download/bandwave-linux-$arch"
if [ -n "$arch" ] && curl -fsSL -o "$tmp/bandwave" "$url" 2>/dev/null; then
  say "Downloaded the latest release."
elif command -v go >/dev/null 2>&1; then
  say "No release binary available, building from source with Go..."
  GOBIN="$tmp" CGO_ENABLED=0 go install "github.com/$REPO@latest"
else
  die "no release binary for $(uname -m), and Go isn't installed to build one"
fi
install -m 0755 "$tmp/bandwave" "$BIN_DIR/bandwave"
say "Installed $BIN_DIR/bandwave"

if ! command -v parec >/dev/null 2>&1 && ! command -v pw-record >/dev/null 2>&1; then
  echo "Note: audio capture needs parec (libpulse) or pw-record (pipewire)."
fi

if [ "${BANDWAVE_NO_AGENT:-}" != 1 ] && command -v systemctl >/dev/null 2>&1; then
  "$BIN_DIR/bandwave" install || echo "Couldn't start the agent; run 'bandwave install' later."
fi

case ":$PATH:" in
  *":$BIN_DIR:"*) say "Done. Run: bandwave" ;;
  *)
    echo
    echo "$BIN_DIR is not on your PATH. Add this to your shell profile:"
    echo "  export PATH=\"$BIN_DIR:\$PATH\""
    say "Then run: bandwave"
    ;;
esac
