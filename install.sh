#!/bin/sh
set -eu

REPO="lawRathod/herdr-systray"
BIN="herdr-systray"

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

info()  { printf "\033[32m·\033[0m %s\n" "$*"; }
warn()  { printf "\033[33m·\033[0m %s\n" "$*" >&2; }
die()   { printf "\033[31merror:\033[0m %s\n" "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# detect platform
# ---------------------------------------------------------------------------

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

case "$OS" in
  linux)
    ASSET="herdr-systray-linux-${ARCH}"
    ;;
  darwin)
    ASSET="herdr-systray-darwin-${ARCH}"
    ;;
  mingw*|cygwin*|msys*)
    OS="windows"
    ASSET="herdr-systray-windows-amd64.exe"
    ARCH="amd64"
    ;;
  *)
    die "unsupported OS: $OS"
    ;;
esac

# ---------------------------------------------------------------------------
# resolve latest release tag
# ---------------------------------------------------------------------------

info "detected ${OS}/${ARCH}"

if command -v curl >/dev/null 2>&1; then
  FETCH="curl -fsSL"
elif command -v wget >/dev/null 2>&1; then
  FETCH="wget -qO-"
else
  die "need curl or wget"
fi

info "fetching latest release from github.com/$REPO"

TAG=$($FETCH "https://api.github.com/repos/${REPO}/releases/latest" \
  | tr -d '\n' | sed 's/.*"tag_name":"//; s/".*//')

if [ -z "$TAG" ]; then
  die "could not determine latest release tag"
fi

info "latest release: $TAG"

# ---------------------------------------------------------------------------
# download
# ---------------------------------------------------------------------------

URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"
TMPFILE=$(mktemp "/tmp/${BIN}.XXXXXXXX")

cleanup() { rm -f "$TMPFILE"; }
trap cleanup EXIT

info "downloading $ASSET"
$FETCH "$URL" > "$TMPFILE" || die "download failed"

chmod +x "$TMPFILE"

# ---------------------------------------------------------------------------
# install
# ---------------------------------------------------------------------------

# Prefer user-local bin directory, fall back to /usr/local/bin
if [ -d "$HOME/.local/bin" ] && echo "$PATH" | grep -qF "$HOME/.local/bin"; then
  DEST="$HOME/.local/bin/${BIN}"
elif [ -w "/usr/local/bin" ]; then
  DEST="/usr/local/bin/${BIN}"
elif [ -w "$HOME/.local/bin" ]; then
  DEST="$HOME/.local/bin/${BIN}"
  warn "add ~/.local/bin to your PATH"
else
  DEST="$HOME/.local/bin/${BIN}"
  mkdir -p "$HOME/.local/bin"
  warn "add ~/.local/bin to your PATH"
fi

mv "$TMPFILE" "$DEST"
trap - EXIT

info "installed to $DEST"

# ---------------------------------------------------------------------------
# optional: set up autostart
# ---------------------------------------------------------------------------

if [ "$OS" != "windows" ]; then
  printf "\n  ✔ installed! Run with:  %s\n\n" "${BIN}"
  printf "  autostart on login? [Y/n] "
  read -r REPLY
  case "$REPLY" in
    n|N|no) info "skipping autostart" ;;
    *)
      if "${DEST}" config autostart; then
        info "autostart configured"
      else
        warn "autostart setup failed (will still work manually)"
      fi
      ;;
  esac
fi

printf "\n  ✔ done\n"
