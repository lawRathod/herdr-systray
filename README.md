# herdr-systray

System tray monitor for [Herdr](https://herdr.dev) coding agents — shows agent status at a glance
and lets you jump straight to any agent.

![tray states](https://img.shields.io/badge/status-working-%23dd9922?logo=go)
[![Go](https://github.com/lawRathod/herdr-systray/actions/workflows/go.yml/badge.svg)](https://github.com/lawRathod/herdr-systray/actions/workflows/go.yml)

## Features

- **Real-time agent tracking** — connects to the Herdr Unix socket and subscribes to
  `pane.agent_status_changed` events
- **Urgency-based tray icon** — the icon shows the most critical state across all agents:
  - 🔴 Red stop sign — agent blocked (needs input)
  - 🟠 Amber pulsing circle — agent working
  - 🟢 Green checkmark — agent done
  - 🟢 Green filled circle — all idle
  - ⚫ Gray question mark — unclassified
- **Animated menu text** — working agents show a spinning ⏳ in the menu
- **Click to focus** — click any agent in the menu to run `herdr agent focus` and jump to its pane
- **Poll fallback** — refreshes every 3 seconds in case events are missed
- **Auto-reconnect** — survives Herdr server restarts

## Install

### Quick (curl | sh)

```sh
curl -fsSL https://raw.githubusercontent.com/lawRathod/herdr-systray/main/install.sh | sh
```

Detects your OS/arch, downloads the latest release binary, and installs it to
`~/.local/bin` or `/usr/local/bin`. Optionally sets up autostart on login.

### From source

Requirements: Go 1.26+, CGO, GTK3, libayatana-appindicator3-dev.

```sh
git clone https://github.com/lawRathod/herdr-systray.git
cd herdr-systray
make build
sudo make install   # copies to $GOPATH/bin
```

Or manually:

```sh
CGO_ENABLED=1 go build -o herdr-systray .
```

### Download a release

Grab the right binary from the [releases page](https://github.com/lawRathod/herdr-systray/releases):

| Asset | Platform |
|---|---|
| `herdr-systray-linux-amd64` | Linux x86_64 |
| `herdr-systray-linux-arm64` | Linux ARM64 (e.g. Raspberry Pi) |
| `herdr-systray-darwin-amd64` | macOS Intel |
| `herdr-systray-darwin-arm64` | macOS Apple Silicon |
| `herdr-systray-windows-amd64.exe` | Windows x86_64 |

## Usage

```sh
# Foreground (Ctrl+C to quit)
herdr-systray

# Daemonise into background
herdr-systray -d

# All flags
herdr-systray -h
```

| Flag | Description |
|---|---|
| `-d`, `--daemon` | Fork into background (detach from terminal) |
| `-l`, `--log <path>` | Log file path (default `/tmp/herdr-systray.log`) |
| `-s`, `--socket <path>` | Herdr socket path (default `~/.config/herdr/herdr.sock`) |
| `-v`, `--version` | Show version |
| `-h`, `--help` | Show help |

### Autostart

Run on login without thinking about it:

```sh
herdr-systray config autostart         # install autostart entry
herdr-systray config autostart remove  # remove it
herdr-systray config autostart status  # check status
```

- **Linux** — creates an XDG `.desktop` file in `~/.config/autostart/`
- **macOS** — creates a launchd plist in `~/Library/LaunchAgents/`
- **Windows** — adds a `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` key

### Uninstall

```sh
herdr-systray uninstall
```

Removes the autostart entry and deletes the binary.

| Interaction | Result |
|---|---|
| Look at tray icon | See the most urgent agent state at a glance |
| Open tray menu | See all agents with status symbols |
| Click an agent | Focus that agent's pane in Herdr |
| Ctrl+C / Quit | Exit cleanly |

## Test

```sh
# Unit tests
make test

# Integration test (requires live herdr server)
make integration-test
```

## How it works

See [AGENTS.md](AGENTS.md) for the full architecture — socket protocol, event subscriptions,
urgency model, and click handling.
