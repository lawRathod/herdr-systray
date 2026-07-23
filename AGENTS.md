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
