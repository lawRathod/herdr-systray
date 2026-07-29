#!/bin/sh
set -eu

REPO="lawRathod/herdr-systray"
BIN="herdr-systray"

# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

DRY_RUN=false
for arg in "$@"; do
  case "$arg" in
    --dry-run|-n) DRY_RUN=true ;;
    --help|-h)
      echo "Usage: curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh [--dry-run]"
      echo ""
      echo "  --dry-run, -n  Print what would be downloaded and installed without doing it"
      exit 0
      ;;
  esac
done

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

info "detected ${OS}/${ARCH}"

# ---------------------------------------------------------------------------
# resolve latest release tag
# ---------------------------------------------------------------------------

if command -v curl >/dev/null 2>&1; then
  FETCH="curl -fsSL"
elif command -v wget >/dev/null 2>&1; then
  FETCH="wget -qO-"
else
  die "need curl or wget"
fi

info "fetching latest release from github.com/$REPO"

# Extract tag_name robustly: grep for the field, take first match, cut value
# Note: GitHub API may return "tag_name":  "vX.Y.Z" with variable whitespace after colon
TAG=$($FETCH "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep -o '"tag_name":[[:space:]]*"[^"]*"' \
  | head -1 | cut -d'"' -f4 || true)

case "$TAG" in
  v[0-9]*) : ;;                                  # looks valid
  "") die "could not determine latest release tag" ;;
  *) die "unexpected tag format: $TAG (full API response may be rate-limited)" ;;
esac

info "latest release: $TAG"

# ---------------------------------------------------------------------------
# resolve destination
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

# ---------------------------------------------------------------------------
# dry-run
# ---------------------------------------------------------------------------

URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"

if $DRY_RUN; then
  echo ""
  info "DRY RUN — nothing will be written"
  echo ""
  printf "  platform:  %s/%s\n" "$OS" "$ARCH"
  printf "  release:   %s\n" "$TAG"
  printf "  asset:     %s\n" "$ASSET"
  printf "  url:       %s\n" "$URL"
  printf "  dest:      %s\n" "$DEST"
  echo ""
  printf "  Would download asset, make executable, and install to %s\n" "$DEST"
  if [ "$OS" != "windows" ]; then
    printf "  Would prompt for autostart configuration\n"
  fi
  echo ""
  exit 0
fi

# ---------------------------------------------------------------------------
# download
# ---------------------------------------------------------------------------

TMPFILE=$(mktemp "/tmp/${BIN}.XXXXXXXX")
cleanup() { rm -f "$TMPFILE"; }
trap cleanup EXIT

info "downloading $ASSET"
$FETCH "$URL" > "$TMPFILE" || die "download failed"

chmod +x "$TMPFILE"

# ---------------------------------------------------------------------------
# install
# ---------------------------------------------------------------------------

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
