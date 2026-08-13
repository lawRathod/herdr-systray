package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// socketPath tests
// ---------------------------------------------------------------------------

func TestSocketPath_default(t *testing.T) {
	// Unset env if set
	os.Unsetenv("HERDR_SOCKET_PATH")
	home, _ := os.UserHomeDir()
	want := home + "/.config/herdr/herdr.sock"
	if got := socketPath(); got != want {
		t.Errorf("socketPath() = %q, want %q", got, want)
	}
}

func TestSocketPath_env(t *testing.T) {
	os.Setenv("HERDR_SOCKET_PATH", "/tmp/test.sock")
	defer os.Unsetenv("HERDR_SOCKET_PATH")
	if got := socketPath(); got != "/tmp/test.sock" {
		t.Errorf("socketPath() = %q, want /tmp/test.sock", got)
	}
}

// ---------------------------------------------------------------------------
// statusSymbol tests
// ---------------------------------------------------------------------------

func TestStatusSymbol(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"working", "⏳"},
		{"blocked", "🚧"},
		{"done", "✅"},
		{"idle", "💤"},
		{"unknown", "❓"},
		{"", "❓"},
		{"anything", "❓"},
	}
	for _, tc := range tests {
		got := statusSymbol(tc.status)
		if got != tc.want {
			t.Errorf("statusSymbol(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// agentListResult parsing
// ---------------------------------------------------------------------------

func TestParseAgentList(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "agent_list",
		"agents": [
			{
				"agent": "opencode",
				"pane_id": "wM:p1",
				"workspace_id": "wM",
				"agent_status": "working",
				"focused": true
			},
			{
				"agent": "pi",
				"pane_id": "wT:p1",
				"workspace_id": "wT",
				"agent_status": "idle",
				"focused": false
			}
		]
	}`)

	var res agentListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal agent list: %v", err)
	}
	if res.Type != "agent_list" {
		t.Errorf("type = %q, want agent_list", res.Type)
	}
	if len(res.Agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(res.Agents))
	}
	if res.Agents[0].Agent != "opencode" {
		t.Errorf("agents[0].Agent = %q, want opencode", res.Agents[0].Agent)
	}
	if res.Agents[0].AgentStatus != "working" {
		t.Errorf("agents[0].AgentStatus = %q, want working", res.Agents[0].AgentStatus)
	}
	if res.Agents[1].AgentStatus != "idle" {
		t.Errorf("agents[1].AgentStatus = %q, want idle", res.Agents[1].AgentStatus)
	}
}

// ---------------------------------------------------------------------------
// agentStatusChangedData parsing
// ---------------------------------------------------------------------------

func TestParseAgentStatusChanged(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "pane_agent_status_changed",
		"pane_id": "wM:p1",
		"agent": "opencode",
		"agent_status": "idle",
		"workspace_id": "wM"
	}`)

	var d agentStatusChangedData
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.PaneID != "wM:p1" {
		t.Errorf("PaneID = %q, want wM:p1", d.PaneID)
	}
	if d.Agent != "opencode" {
		t.Errorf("Agent = %q, want opencode", d.Agent)
	}
	if d.AgentStatus != "idle" {
		t.Errorf("AgentStatus = %q, want idle", d.AgentStatus)
	}
	if d.WorkspaceID != "wM" {
		t.Errorf("WorkspaceID = %q, want wM", d.WorkspaceID)
	}
}

// ---------------------------------------------------------------------------
// paneClosedData parsing
// ---------------------------------------------------------------------------

func TestParsePaneClosed(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "pane_closed",
		"pane_id": "wM:p1",
		"workspace_id": "wM"
	}`)

	var d paneClosedData
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.PaneID != "wM:p1" {
		t.Errorf("PaneID = %q, want wM:p1", d.PaneID)
	}
}

// ---------------------------------------------------------------------------
// herdrEvent envelope parsing
// ---------------------------------------------------------------------------

func TestParseHerdrEvent(t *testing.T) {
	raw := []byte(`{"event":"pane_agent_status_changed","data":{"type":"pane_agent_status_changed","pane_id":"wM:p1","agent":"opencode","agent_status":"working","workspace_id":"wM"}}`)

	var evt herdrEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if evt.Event != "pane_agent_status_changed" {
		t.Errorf("Event = %q, want pane_agent_status_changed", evt.Event)
	}
	if evt.Data == nil {
		t.Fatal("Data is nil")
	}

	// Parse inner data
	var d agentStatusChangedData
	if err := json.Unmarshal(evt.Data, &d); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if d.AgentStatus != "working" {
		t.Errorf("AgentStatus = %q, want working", d.AgentStatus)
	}
}

// ---------------------------------------------------------------------------
// updateWorkingFlagLocked tests
// ---------------------------------------------------------------------------

func TestUpdateWorkingFlagLocked_noAgents(t *testing.T) {
	a := &app{agents: make(map[string]*agentInfo)}
	a.updateWorkingFlagLocked()
	if a.anyWorking {
		t.Error("anyWorking = true, want false (no agents)")
	}
}

func TestUpdateWorkingFlagLocked_allIdle(t *testing.T) {
	a := &app{agents: map[string]*agentInfo{
		"wM:p1": {Agent: "pi", AgentStatus: "idle"},
		"wT:p1": {Agent: "opencode", AgentStatus: "done"},
		"wS:p1": {Agent: "claude", AgentStatus: "blocked"},
	}}
	a.updateWorkingFlagLocked()
	if a.anyWorking {
		t.Error("anyWorking = true, want false (all idle/done/blocked)")
	}
}

func TestUpdateWorkingFlagLocked_oneWorking(t *testing.T) {
	a := &app{agents: map[string]*agentInfo{
		"wM:p1": {Agent: "pi", AgentStatus: "working"},
		"wT:p1": {Agent: "opencode", AgentStatus: "idle"},
	}}
	a.updateWorkingFlagLocked()
	if !a.anyWorking {
		t.Error("anyWorking = false, want true (one working)")
	}
}

func TestUpdateWorkingFlagLocked_multipleWorking(t *testing.T) {
	a := &app{agents: map[string]*agentInfo{
		"wM:p1": {Agent: "pi", AgentStatus: "working"},
		"wT:p1": {Agent: "opencode", AgentStatus: "working"},
	}}
	a.updateWorkingFlagLocked()
	if !a.anyWorking {
		t.Error("anyWorking = false, want true (both working)")
	}
}

// ---------------------------------------------------------------------------
// handleStatusChanged tests
// ---------------------------------------------------------------------------

func TestHandleStatusChanged_existingAgent(t *testing.T) {
	a := &app{
		agents: map[string]*agentInfo{
			"wM:p1": {PaneID: "wM:p1", Agent: "opencode", AgentStatus: "working", WorkspaceID: "wM"},
		},
	}
	a.anyWorking = true
	a.mu = sync.RWMutex{}

	raw := json.RawMessage(`{
		"type": "pane_agent_status_changed",
		"pane_id": "wM:p1",
		"agent": "opencode",
		"agent_status": "idle",
		"workspace_id": "wM"
	}`)

	a.handleStatusChanged(raw)

	if a.agents["wM:p1"].AgentStatus != "idle" {
		t.Errorf("AgentStatus = %q, want idle", a.agents["wM:p1"].AgentStatus)
	}
	if a.anyWorking {
		t.Error("anyWorking = true, want false (agent went idle)")
	}
}

func TestHandleStatusChanged_newAgent(t *testing.T) {
	a := &app{
		agents: make(map[string]*agentInfo),
	}
	a.mu = sync.RWMutex{}

	raw := json.RawMessage(`{
		"type": "pane_agent_status_changed",
		"pane_id": "wX:p1",
		"agent": "pi",
		"agent_status": "working",
		"workspace_id": "wX"
	}`)

	a.handleStatusChanged(raw)

	if _, ok := a.agents["wX:p1"]; !ok {
		t.Fatal("agent wX:p1 not added to map")
	}
	if a.agents["wX:p1"].Agent != "pi" {
		t.Errorf("Agent = %q, want pi", a.agents["wX:p1"].Agent)
	}
	if a.agents["wX:p1"].AgentStatus != "working" {
		t.Errorf("AgentStatus = %q, want working", a.agents["wX:p1"].AgentStatus)
	}
	if !a.anyWorking {
		t.Error("anyWorking = false, want true")
	}
}

// ---------------------------------------------------------------------------
// handlePaneClosed tests
// ---------------------------------------------------------------------------

func TestHandlePaneClosed_existing(t *testing.T) {
	a := &app{
		agents: map[string]*agentInfo{
			"wM:p1": {PaneID: "wM:p1", Agent: "opencode", AgentStatus: "working", WorkspaceID: "wM"},
		},
	}
	a.anyWorking = true
	a.mu = sync.RWMutex{}

	raw := json.RawMessage(`{
		"type": "pane_closed",
		"pane_id": "wM:p1",
		"workspace_id": "wM"
	}`)

	a.handlePaneClosed(raw)

	if _, ok := a.agents["wM:p1"]; ok {
		t.Error("agent wM:p1 still in map after close")
	}
	if a.anyWorking {
		t.Error("anyWorking = true, want false (agent removed)")
	}
}

func TestHandlePaneClosed_notInMap(t *testing.T) {
	a := &app{
		agents: make(map[string]*agentInfo),
	}
	a.mu = sync.RWMutex{}

	raw := json.RawMessage(`{
		"type": "pane_closed",
		"pane_id": "wM:p1",
		"workspace_id": "wM"
	}`)

	a.handlePaneClosed(raw)

	// Should not panic, map should remain empty
	if len(a.agents) != 0 {
		t.Errorf("agents = %d, want 0", len(a.agents))
	}
}

// ---------------------------------------------------------------------------
// fetchAgentList integration with mock socket
// ---------------------------------------------------------------------------

func TestShowingIdleFlag(t *testing.T) {
	a := &app{
		agents:      make(map[string]*agentInfo),
		anyWorking:  false,
		showingIdle: true,
	}

	// Simulate applyIcon logic without calling systray.SetIcon
	a.anyWorking = true
	a.showingIdle = !a.anyWorking
	if a.showingIdle {
		t.Error("showingIdle = true, want false (anyWorking=true)")
	}

	a.anyWorking = false
	a.showingIdle = !a.anyWorking
	if !a.showingIdle {
		t.Error("showingIdle = false, want true (anyWorking=false)")
	}
}

// ---------------------------------------------------------------------------
// Telegram config + notification tests
// ---------------------------------------------------------------------------

func TestReadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	content := "# comment line\n\nHERDR_TELEGRAM_TOKEN=123:abc\nSPACED = value here\ngarbage line\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	vals := readEnvFile(path)
	if len(vals) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(vals), vals)
	}
	if vals["HERDR_TELEGRAM_TOKEN"] != "123:abc" {
		t.Errorf("token = %q, want 123:abc", vals["HERDR_TELEGRAM_TOKEN"])
	}
	if vals["SPACED"] != "value here" {
		t.Errorf("SPACED = %q, want value here", vals["SPACED"])
	}
}

func TestReadEnvFile_missing(t *testing.T) {
	vals := readEnvFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if len(vals) != 0 {
		t.Errorf("got %d entries, want 0", len(vals))
	}
}

func TestLoadTelegramConfig_defaults(t *testing.T) {
	os.Setenv("HERDR_TELEGRAM_TOKEN", "123:abc")
	defer os.Unsetenv("HERDR_TELEGRAM_TOKEN")

	cfg := loadTelegramConfig()
	if !cfg.enabled {
		t.Error("enabled = false, want true (token set)")
	}
	if cfg.token != "123:abc" {
		t.Errorf("token = %q, want 123:abc", cfg.token)
	}
	if !cfg.notify["blocked"] || !cfg.notify["done"] {
		t.Error("default notify should be blocked,done")
	}
	if cfg.notify["working"] {
		t.Error("working should not notify by default")
	}
}

func TestLoadTelegramConfig_customNotify(t *testing.T) {
	os.Setenv("HERDR_TELEGRAM_TOKEN", "123:abc")
	os.Setenv("HERDR_TELEGRAM_NOTIFY", "working")
	defer os.Unsetenv("HERDR_TELEGRAM_TOKEN")
	defer os.Unsetenv("HERDR_TELEGRAM_NOTIFY")

	cfg := loadTelegramConfig()
	if !cfg.notify["working"] {
		t.Error("notify[working] = false, want true")
	}
	if cfg.notify["blocked"] || cfg.notify["done"] {
		t.Error("custom notify should not include blocked/done")
	}
}

func TestLoadTelegramConfig_disabled(t *testing.T) {
	// Empty value blocks seeding from the real config file (~/.config/...)
	os.Setenv("HERDR_TELEGRAM_TOKEN", "")
	os.Setenv("HERDR_TELEGRAM_CHAT_ID", "")
	cfg := loadTelegramConfig()
	if cfg.enabled {
		t.Error("enabled = true, want false (no token)")
	}
}

func TestNotifyStateChange_triggers(t *testing.T) {
	a := &app{tg: telegramConfig{
		enabled:  true,
		token:    "tok",
		chatID:   "42",
		notify:   map[string]bool{"blocked": true, "done": true},
		notifyOn: true,
	}}
	var got []string
	a.notifyHook = func(ag *agentInfo, from string) {
		got = append(got, from+"->"+ag.AgentStatus+" "+ag.Agent)
	}

	a.notifyStateChange(&agentInfo{Agent: "opencode", AgentStatus: "blocked"}, "working")
	a.notifyStateChange(&agentInfo{Agent: "pi", AgentStatus: "done"}, "working")
	if len(got) != 2 {
		t.Fatalf("got %d notifications, want 2: %v", len(got), got)
	}
	if got[0] != "working->blocked opencode" {
		t.Errorf("got[0] = %q", got[0])
	}
}

func TestNotifyStateChange_noTriggerForNonNotifyState(t *testing.T) {
	a := &app{tg: telegramConfig{
		enabled:  true,
		token:    "tok",
		chatID:   "42",
		notify:   map[string]bool{"blocked": true},
		notifyOn: true,
	}}
	called := false
	a.notifyHook = func(ag *agentInfo, from string) { called = true }

	a.notifyStateChange(&agentInfo{Agent: "opencode", AgentStatus: "idle"}, "working")
	if called {
		t.Error("notification fired for idle, want none")
	}
}

func TestNotifyStateChange_disabled(t *testing.T) {
	a := &app{tg: telegramConfig{}}
	called := false
	a.notifyHook = func(ag *agentInfo, from string) { called = true }

	a.notifyStateChange(&agentInfo{Agent: "opencode", AgentStatus: "blocked"}, "working")
	if called {
		t.Error("notification fired with disabled config")
	}
}

func TestHandleStatusChanged_firesNotification(t *testing.T) {
	a := &app{
		agents: map[string]*agentInfo{
			"wM:p1": {PaneID: "wM:p1", Agent: "opencode", AgentStatus: "working", WorkspaceID: "wM"},
		},
		tg: telegramConfig{
			enabled:  true,
			token:    "tok",
			chatID:   "42",
			notify:   map[string]bool{"blocked": true},
			notifyOn: true,
		},
	}
	a.anyWorking = true
	a.mu = sync.RWMutex{}

	var got []string
	a.notifyHook = func(ag *agentInfo, from string) {
		got = append(got, from+"->"+ag.AgentStatus)
	}

	raw := json.RawMessage(`{
		"type": "pane_agent_status_changed",
		"pane_id": "wM:p1",
		"agent": "opencode",
		"agent_status": "blocked",
		"workspace_id": "wM"
	}`)
	a.handleStatusChanged(raw)

	if len(got) != 1 || got[0] != "working->blocked" {
		t.Errorf("notifications = %v, want [working->blocked]", got)
	}
}

func TestHandleStatusChanged_noNotifyOnSameStatus(t *testing.T) {
	a := &app{
		agents: map[string]*agentInfo{
			"wM:p1": {PaneID: "wM:p1", Agent: "opencode", AgentStatus: "blocked", WorkspaceID: "wM"},
		},
		tg: telegramConfig{
			enabled:  true,
			token:    "tok",
			chatID:   "42",
			notify:   map[string]bool{"blocked": true},
			notifyOn: true,
		},
	}
	a.mu = sync.RWMutex{}
	called := false
	a.notifyHook = func(ag *agentInfo, from string) { called = true }

	raw := json.RawMessage(`{
		"type": "pane_agent_status_changed",
		"pane_id": "wM:p1",
		"agent": "opencode",
		"agent_status": "blocked",
		"workspace_id": "wM"
	}`)
	a.handleStatusChanged(raw)

	if called {
		t.Error("notification fired for same-status event")
	}
}

// ---------------------------------------------------------------------------
// agentStatusReport tests
// ---------------------------------------------------------------------------

func TestAgentStatusReport_ordered(t *testing.T) {
	a := &app{
		agents: map[string]*agentInfo{
			"wT:p1": {PaneID: "wT:p1", Agent: "pi", AgentStatus: "idle", WorkspaceID: "wT"},
			"wM:p1": {PaneID: "wM:p1", Agent: "opencode", AgentStatus: "working", WorkspaceID: "wM"},
			"wS:p1": {PaneID: "wS:p1", Agent: "claude", AgentStatus: "blocked", WorkspaceID: "wS"},
		},
		workspaceLabels: map[string]string{"wM": "work-M", "wS": "work-S"},
		tg: telegramConfig{
			notify:   map[string]bool{"blocked": true, "done": true},
			notifyOn: true,
		},
	}
	a.mu = sync.RWMutex{}

	got := a.agentStatusReport()
	want := "Herdr agents (3):\n🚧 claude [wS] — blocked (work-S)\n⏳ opencode [wM] — working (work-M)\n💤 pi [wT] — idle (wT)\n\nNotifications: ON (blocked, done)"
	if got != want {
		t.Errorf("report:\n%q\nwant:\n%q", got, want)
	}
}

func TestAgentStatusReport_empty(t *testing.T) {
	a := &app{
		agents: make(map[string]*agentInfo),
		tg: telegramConfig{
			notify:   map[string]bool{"blocked": true},
			notifyOn: true,
		},
	}
	a.mu = sync.RWMutex{}
	want := "Herdr: no agents detected\n\nNotifications: ON (blocked)"
	if got := a.agentStatusReport(); got != want {
		t.Errorf("report = %q, want %q", got, want)
	}
}

func TestGetTelegramUpdates_parse(t *testing.T) {
	// Parse-only: verify the update shape matches the Telegram API
	raw := []byte(`{"ok":true,"result":[{"update_id":42,"message":{"message_id":7,"chat":{"id":123456},"text":"/status"}}]}`)
	var up struct {
		Result []telegramUpdate `json:"result"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(up.Result) != 1 {
		t.Fatalf("got %d updates, want 1", len(up.Result))
	}
	u := up.Result[0]
	if u.UpdateID != 42 {
		t.Errorf("UpdateID = %d, want 42", u.UpdateID)
	}
	if u.Message.Text != "/status" {
		t.Errorf("Text = %q, want /status", u.Message.Text)
	}
	if u.Message.Chat.ID != 123456 {
		t.Errorf("Chat.ID = %v, want 123456", u.Message.Chat.ID)
	}
}

// ---------------------------------------------------------------------------
// telegram command dispatch (/status, /on, /off) tests
// ---------------------------------------------------------------------------

func mkTelegramUpdate(id int64, chatID float64, text string) telegramUpdate {
	u := telegramUpdate{UpdateID: id}
	u.Message.Chat.ID = chatID
	u.Message.Text = text
	return u
}

func TestHandleTelegramUpdate_status(t *testing.T) {
	a := &app{
		agents: map[string]*agentInfo{
			"w6:p1": {PaneID: "w6:p1", Agent: "opencode", AgentStatus: "working", WorkspaceID: "w6"},
		},
		workspaceLabels: map[string]string{"w6": "thesis"},
		tg: telegramConfig{
			notify:   map[string]bool{"blocked": true, "done": true},
			notifyOn: true,
		},
	}
	a.mu = sync.RWMutex{}

	reply := a.handleTelegramUpdate(mkTelegramUpdate(1, 42, "/status"), "tok", "42")
	want := "Herdr agents (1):\n⏳ opencode [w6] — working (thesis)\n\nNotifications: ON (blocked, done)"
	if reply != want {
		t.Errorf("reply:\n%q\nwant:\n%q", reply, want)
	}
}

func TestHandleTelegramUpdate_otherChat(t *testing.T) {
	a := &app{agents: make(map[string]*agentInfo)}
	a.mu = sync.RWMutex{}
	if reply := a.handleTelegramUpdate(mkTelegramUpdate(1, 999, "/status"), "tok", "42"); reply != "" {
		t.Errorf("reply = %q, want empty (other chat)", reply)
	}
}

func TestHandleTelegramUpdate_unknown(t *testing.T) {
	a := &app{agents: make(map[string]*agentInfo)}
	a.mu = sync.RWMutex{}
	if reply := a.handleTelegramUpdate(mkTelegramUpdate(1, 42, "/foo"), "tok", "42"); reply != "" {
		t.Errorf("reply = %q, want empty (unknown command)", reply)
	}
}

func TestHandleTelegramUpdate_toggle(t *testing.T) {
	dir := t.TempDir()
	old := notifyStatePath
	notifyStatePath = func() string { return dir + "/notify_state" }
	defer func() { notifyStatePath = old }()

	a := &app{agents: make(map[string]*agentInfo), tg: telegramConfig{notifyOn: true}}
	a.mu = sync.RWMutex{}

	reply := a.handleTelegramUpdate(mkTelegramUpdate(1, 42, "/off"), "tok", "42")
	if !strings.Contains(reply, "OFF") {
		t.Errorf("off reply = %q", reply)
	}
	if a.tg.notifyOn {
		t.Error("notifyOn = true after /off")
	}
	b, _ := os.ReadFile(dir + "/notify_state")
	if string(b) != "off\n" {
		t.Errorf("state file = %q, want off\n", b)
	}

	reply = a.handleTelegramUpdate(mkTelegramUpdate(2, 42, "/on"), "tok", "42")
	if !strings.Contains(reply, "ON") {
		t.Errorf("on reply = %q", reply)
	}
	if !a.tg.notifyOn {
		t.Error("notifyOn = false after /on")
	}
	b, _ = os.ReadFile(dir + "/notify_state")
	if string(b) != "on\n" {
		t.Errorf("state file = %q, want on\n", b)
	}
}

func TestLoadNotifyState(t *testing.T) {
	dir := t.TempDir()
	old := notifyStatePath
	notifyStatePath = func() string { return dir + "/notify_state" }
	defer func() { notifyStatePath = old }()

	if !loadNotifyState() {
		t.Error("missing file should default to on")
	}
	_ = os.WriteFile(dir+"/notify_state", []byte("off\n"), 0600)
	if loadNotifyState() {
		t.Error("file with off should return false")
	}
	_ = os.WriteFile(dir+"/notify_state", []byte("on\n"), 0600)
	if !loadNotifyState() {
		t.Error("file with on should return true")
	}
}

func TestNotifyStateChange_notifyOff(t *testing.T) {
	a := &app{tg: telegramConfig{
		enabled:  true,
		token:    "tok",
		chatID:   "42",
		notify:   map[string]bool{"blocked": true},
		notifyOn: false,
	}}
	called := false
	a.notifyHook = func(ag *agentInfo, from string) { called = true }

	a.notifyStateChange(&agentInfo{Agent: "opencode", AgentStatus: "blocked"}, "working")
	if called {
		t.Error("notification fired while notifyOn = false")
	}
}

func TestWorkspaceLabel(t *testing.T) {
	a := &app{workspaceLabels: map[string]string{"w6": "thesis"}}
	a.mu = sync.RWMutex{}
	if got := a.workspaceLabel("w6"); got != "thesis" {
		t.Errorf("workspaceLabel(w6) = %q, want thesis", got)
	}
	if got := a.workspaceLabel("wX"); got != "wX" {
		t.Errorf("workspaceLabel(wX) = %q, want wX (fallback)", got)
	}
}
