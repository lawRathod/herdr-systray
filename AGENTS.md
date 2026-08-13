# How herdr-systray tracks agents

herdr-systray connects to a running [Herdr](https://herdr.dev) server via its Unix socket and tracks coding agent states in real time.

## Data sources

### 1. Initial state — `agent.list` (one-shot)

On startup, the app calls `agent.list` over the Unix socket to get every agent currently tracked by the Herdr server. This populates the tray menu and sets the initial tray icon.

### 2. Real-time updates — `pane.agent_status_changed` (subscription)

For each known agent pane, the app subscribes to `pane.agent_status_changed` events. These fire immediately when Herdr detects a state transition (`idle → working`, `working → blocked`, etc.).

Each event carries:
- `pane_id` — which terminal pane
- `agent` — the agent kind (e.g. `pi`, `opencode`, `claude`)
- `agent_status` — the new state

### 3. Poll fallback — `agent.list` (every 3s)

Every 3 seconds the app re-fetches the full agent list as a safety net. This catches:
- New agents that appeared since startup
- Agents that were released
- Any status changes the subscription might have missed

When the poll detects a change, it closes the current subscription connection so it reconnects with an updated pane list.

### 4. Subscription reconnection

If the Herdr server restarts or the socket disconnects, the app automatically reconnects every 3 seconds.

## Agent states

| State | Tray icon | Menu symbol | Meaning |
|---|---|---|---|
| `blocked` | 🔴 Red stop sign | 🚧 | Waiting for user input / approval |
| `working` | 🟠 Amber pulsing circle | ⏳ + spinner | Actively processing |
| `done` | 🟢 Green checkmark | ✅ | Finished |
| `idle` | 🟢 Green filled circle | 💤 | Ready, waiting |
| `unknown` | ⚫ Gray question mark | ❓ | Present but unclassified |

## Urgency model

The tray icon always shows the **most urgent** state across all agents:

```
blocked > working > done > idle > unknown
```

If any agent is blocked, you see the stop sign. If none blocked but one is working, you see the pulsing amber circle. Only when all are idle do you see the green circle.

## Clicking an agent in the menu

Clicking an agent menu item runs:

```sh
herdr agent focus <pane_id>
```

This focuses that agent's terminal pane within the Herdr UI.

## Click command

The tray menu items are **not disabled** — clicking one executes `herdr agent focus <pane_id>` via `os/exec`. This requires the `herdr` CLI to be on `$PATH`.

## Socket path

The socket is resolved from `$HERDR_SOCKET_PATH` if set, otherwise `~/.config/herdr/herdr.sock`.

## Telegram notifications

On agent state transitions to `blocked` or `done` (configurable via `HERDR_TELEGRAM_NOTIFY`),
the app POSTs to the Telegram Bot API. Config comes from `~/.config/herdr-systray/config`
(KEY=VALUE, env vars override): `HERDR_TELEGRAM_TOKEN` (required), `HERDR_TELEGRAM_CHAT_ID`
(auto-detected via `getUpdates` on startup if empty). Startup sends a "connected" test
message. The token is never logged. Transitions are detected in `handleStatusChanged`
and the poll fallback in `pollAgents`; the `notifyHook` field lets tests observe
notifications without hitting the network.

The app also long-polls `getUpdates` while running (`telegramPollLoop`) and
answers bot commands via `handleTelegramUpdate`: `/status` →
`agentStatusReport()`, `/off` + `/on` → toggle `tg.notifyOn` (persisted to
`~/.config/herdr-systray/notify_state`). `notifyStateChange` skips sends while
`notifyOn` is false. Poll offset is seeded from the startup chat-id lookup
(`resolveTelegramChatID`) so updates aren't re-delivered on restart.

Status reports and notifications include the project label: workspace ids map
to labels via `herdr workspace list` (same socket API, `workspaceListResult`),
fetched at startup and every 30s in `pollAgents` (`fetchWorkspaceLabels`).
Fallback is the raw workspace id.

## Development on Bazzite / Fedora Silverblue (ostree)

The host is an immutable ostree system — GTK3 and libayatana-appindicator3
dev headers cannot be installed on the host without rpm-ostree layering.
All development/build commands run inside the Fedora distrobox container:

```sh
distrobox enter fedora -- make build
distrobox enter fedora -- make test
```

The container has gcc, gtk3-devel, and libayatana-appindicator3-devel
installed. Build the binary with an rpath to user-space runtime libs:

```sh
distrobox enter fedora -- go build -ldflags "-r /var/home/law/.local/lib" -o herdr-systray .
```

Runtime note: the host lacks `libayatana-appindicator3.so.1` and
`libayatana-ido3.so.1` — they live in `~/.local/lib` (copied from the
container). Any binary built for this machine must link with that rpath,
and `~/.local/lib` must contain those two libraries.
