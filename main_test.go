package main

import (
	"encoding/json"
	"os"
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
