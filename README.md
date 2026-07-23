# herdr-systray

System tray monitor for [Herdr](https://herdr.dev) coding agents — shows agent status at a glance and lets you jump straight to any agent.

![tray states](https://img.shields.io/badge/status-working-%23dd9922?logo=go)
[![Go](https://github.com/lawRathod/herdr-systray/actions/workflows/go.yml/badge.svg)](https://github.com/lawRathod/herdr-systray/actions/workflows/go.yml)

## Features

- **Real-time agent tracking** — connects to the Herdr Unix socket and subscribes to `pane.agent_status_changed` events
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

## Requirements

- Go 1.26+ with `CGO_ENABLED=1`
- Linux with `gtk3` and `libayatana-appindicator3-dev`
- A running [Herdr](https://herdr.dev) server (0.7.5+)

## Install

```sh
git clone https://github.com/lawRathod/herdr-systray.git
cd herdr-systray
CGO_ENABLED=1 go build -o herdr-systray .
./herdr-systray
```

## Usage

Start the app in a regular terminal (ideally outside Herdr). It connects to `~/.config/herdr/herdr.sock` (or `$HERDR_SOCKET_PATH`) and shows an icon in the system tray.

| Interaction | Result |
|---|---|
| Look at tray icon | See the most urgent agent state at a glance |
| Open tray menu | See all agents with status symbols |
| Click an agent | Focus that agent's pane in Herdr |
| Ctrl+C / Quit | Exit cleanly |

## Test

```sh
# Unit tests
CGO_ENABLED=1 go test -count=1 ./...

# Integration test (requires live herdr server)
bash test_integration.sh
```

## How it works

See [AGENTS.md](AGENTS.md) for the full architecture — socket protocol, event subscriptions, urgency model, and click handling.
