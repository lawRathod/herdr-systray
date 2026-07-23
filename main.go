package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"time"

	"herdr-systray/icons"

	"github.com/getlantern/systray"
)

const version = "0.1.0"

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

func init() {
	flag.BoolVar(&cliDaemon, "d", false, "daemonise into background")
	flag.BoolVar(&cliDaemon, "daemon", false, "")
	flag.BoolVar(&cliHelp, "h", false, "show this help")
	flag.BoolVar(&cliHelp, "help", false, "")
	flag.BoolVar(&cliVersion, "v", false, "show version")
	flag.BoolVar(&cliVersion, "version", false, "")
	flag.StringVar(&cliLog, "l", "", "log file path (default /tmp/herdr-systray.log)")
	flag.StringVar(&cliLog, "log", "", "")
	flag.StringVar(&cliSocket, "s", "", "herdr socket path (default ~/.config/herdr/herdr.sock)")
	flag.StringVar(&cliSocket, "socket", "", "")
}

var (
	cliDaemon  bool
	cliHelp    bool
	cliVersion bool
	cliLog     string
	cliSocket  string
)

// ---------------------------------------------------------------------------
// Herdr socket API — request / response types
// ---------------------------------------------------------------------------

type rpcRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type rpcResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type agentListResult struct {
	Type   string      `json:"type"`
	Agents []agentInfo `json:"agents"`
}

type agentInfo struct {
	Agent       string `json:"agent"`
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	AgentStatus string `json:"agent_status"`
	Focused     bool   `json:"focused"`
}

type subscribeParams struct {
	Subscriptions []subscriptionEntry `json:"subscriptions"`
}

type subscriptionEntry struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

// ---------------------------------------------------------------------------
// Herdr socket API — pushed event types
// ---------------------------------------------------------------------------

type herdrEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type agentStatusChangedData struct {
	Type        string `json:"type"`
	PaneID      string `json:"pane_id"`
	Agent       string `json:"agent"`
	AgentStatus string `json:"agent_status"`
	WorkspaceID string `json:"workspace_id"`
}

type paneCreatedData struct {
	Type string   `json:"type"`
	Pane paneInfo `json:"pane"`
}

type paneClosedData struct {
	Type        string `json:"type"`
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
}

type agentDetectedData struct {
	Type        string `json:"type"`
	PaneID      string `json:"pane_id"`
	Agent       string `json:"agent,omitempty"`
	WorkspaceID string `json:"workspace_id"`
	FinalStatus string `json:"final_status,omitempty"`
	Released    bool   `json:"released,omitempty"`
}

type paneInfo struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent,omitempty"`
	AgentStatus string `json:"agent_status"`
	Focused     bool   `json:"focused"`
}

// ---------------------------------------------------------------------------
// app
// ---------------------------------------------------------------------------

type app struct {
	mu     sync.RWMutex
	agents map[string]*agentInfo // paneID -> agent

	conn         net.Conn
	scanner      *bufio.Scanner
	subscribedAt int64 // unix nanos when we last subscribed (to avoid stale events)

	anyWorking  bool
	anyBlocked  bool
	anyDone     bool
	anyUnknown  bool
	mostUrgent  string // "blocked" > "working" > "done" > "idle" > "unknown"
	showingIdle bool
	frame       int
	running     bool

	spinnerFrame int // separate from tray icon frame for speed

	mItems      []*dynamicItem
	statusLabel *systray.MenuItem
	quitItem    *systray.MenuItem

	animTick   *time.Ticker
	animDone   chan struct{}
	animFrames [4][]byte
	idleIcon   []byte

	pollTick *time.Ticker
	pollDone chan struct{}
}

type dynamicItem struct {
	item   *systray.MenuItem
	paneID string
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	flag.Parse()

	if cliHelp {
		fmt.Print(`herdr-systray — system tray monitor for Herdr coding agents

Usage:
  herdr-systray [flags]

Flags:
  -d, --daemon       fork into background (detach from terminal)
  -h, --help         show this help
  -v, --version      show version
  -l, --log <path>   log file path (default /tmp/herdr-systray.log)
  -s, --socket <path>  herdr socket path (default ~/.config/herdr/herdr.sock)

The log file is a 100 KB circular buffer — it never grows beyond that
size. When running in daemon mode all output goes there so you can
debug issues by inspecting /tmp/herdr-systray.log.

Environment variables:
  HERDR_SOCKET_PATH  overrides the default herdr socket path
`)
		return
	}

	if cliVersion {
		fmt.Printf("herdr-systray v%s\n", version)
		return
	}

	// Set up circular log
	logPath := cliLog
	if logPath == "" {
		logPath = "/tmp/herdr-systray.log"
	}
	logWriter := newRingLogWriter(logPath, 100*1024) // 100 KB
	log.SetOutput(logWriter)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	// Daemonise
	if cliDaemon {
		// Re-exec self without -d flag, detached from terminal
		var args []string
		for i, a := range os.Args {
			if a == "-d" || a == "--daemon" {
				continue
			}
			if i == 0 {
				args = append(args, a)
			} else if a == "-d" || a == "--daemon" {
				continue
			} else {
				args = append(args, a)
			}
		}
		if len(args) == 0 {
			args = []string{os.Args[0]}
		}
		devnull, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
		if err != nil {
			log.Fatalf("daemon: %v", err)
		}
		proc, err := os.StartProcess(os.Args[0], args, &os.ProcAttr{
			Files: []*os.File{devnull, devnull, logWriter.(*ringLogWriter).File()},
			Env:   os.Environ(),
		})
		if err != nil {
			log.Fatalf("daemon: %v", err)
		}
		fmt.Printf("herdr-systray daemon started (PID %d)\n", proc.Pid)
		fmt.Printf("log: %s\n", logPath)
		os.Exit(0)
	}

	log.Printf("herdr-systray v%s starting", version)

	a := &app{
		agents:  make(map[string]*agentInfo),
		running: true,
	}
	systray.Run(a.onReady, a.onExit)
}

func (a *app) onReady() {
	a.idleIcon = icons.Idle()
	a.animFrames = icons.WorkingFrames()
	systray.SetIcon(a.idleIcon)
	systray.SetTitle("H")
	systray.SetTooltip("Herdr Agent Tracker")

	a.statusLabel = systray.AddMenuItem("connecting...", "")
	a.statusLabel.Disable()
	systray.AddSeparator()
	a.quitItem = systray.AddMenuItem("Quit", "Quit Herdr systray")

	// Initial agent list
	if err := a.fetchAgentList(); err != nil {
		log.Printf("initial agent list: %v", err)
	}
	a.rebuildMenu()
	a.applyIcon()

	// Persistent subscription + polling
	a.animDone = make(chan struct{})
	a.animTick = time.NewTicker(400 * time.Millisecond)
	go a.animationLoop()

	a.pollDone = make(chan struct{})
	a.pollTick = time.NewTicker(3 * time.Second)
	go a.pollLoop()

	go a.subscriptionLoop()

	// Menu quit + Ctrl+C — use os.Exit because systray.Quit() doesn't
	// reliably unblock systray.Run() on Linux+appindicator.
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		select {
		case <-a.quitItem.ClickedCh:
			log.Print("quit from menu")
		case <-ch:
			log.Print("SIGINT, exiting")
		}
		signal.Stop(ch)
		a.shutdown()
		os.Exit(0)
	}()
}

func (a *app) onExit() {}

func (a *app) shutdown() {
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()
	if a.animTick != nil {
		a.animTick.Stop()
	}
	if a.pollTick != nil {
		a.pollTick.Stop()
	}
	// Signal goroutines to stop via their done channels.
	// Each goroutine reads from its done channel and returns without closing it.
	select {
	case <-a.animDone:
	default:
		close(a.animDone)
	}
	select {
	case <-a.pollDone:
	default:
		close(a.pollDone)
	}
	// Close the subscription connection to unblock readEvents.
	if a.conn != nil {
		a.conn.Close()
	}
}

// ---------------------------------------------------------------------------
// one-shot requests (fresh connection each time)
// ---------------------------------------------------------------------------

func oneShotRequest(method string, params any) (json.RawMessage, error) {
	path := socketPath()
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	req := rpcRequest{
		ID:     fmt.Sprintf("os_%d", time.Now().UnixNano()),
		Method: method,
		Params: params,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 256*1024)
	if !sc.Scan() {
		return nil, fmt.Errorf("read: %w", sc.Err())
	}
	var resp rpcResponse
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("rpc: %s - %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func socketPath() string {
	if cliSocket != "" {
		return cliSocket
	}
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return home + "/.config/herdr/herdr.sock"
}

// ---------------------------------------------------------------------------
// ring buffer log writer — 100 KB circular file, never grows beyond
// ---------------------------------------------------------------------------

type ringLogWriter struct {
	f       *os.File
	path    string
	maxSize int64
	mu      sync.Mutex
}

func newRingLogWriter(path string, maxSize int64) io.Writer {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("ring log: open %s: %v — falling back to stderr", path, err)
		return os.Stderr
	}
	// Trim if file somehow grew beyond max (e.g. user editing)
	if fi, _ := f.Stat(); fi != nil && fi.Size() > maxSize {
		f.Truncate(maxSize)
		f.Seek(0, io.SeekEnd)
	}
	return &ringLogWriter{f: f, path: path, maxSize: maxSize}
}

func (w *ringLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.f.Write(p)
	if err != nil {
		return n, err
	}

	fi, err := w.f.Stat()
	if err != nil {
		return n, nil
	}
	if fi.Size() <= w.maxSize {
		return n, nil
	}

	// Discard oldest bytes: read last maxSize, rewrite from top
	over := fi.Size() - w.maxSize
	dst := make([]byte, w.maxSize)
	w.f.Seek(over, io.SeekStart)
	if _, err := io.ReadFull(w.f, dst); err != nil {
		return n, nil
	}
	w.f.Seek(0, io.SeekStart)
	w.f.Write(dst)
	w.f.Truncate(w.maxSize)
	w.f.Seek(0, io.SeekEnd)

	return n, nil
}

// File exposes the underlying *os.File for passing as stderr to the
// daemon child process so logs continue there.
func (w *ringLogWriter) File() *os.File { return w.f }

func (a *app) fetchAgentList() error {
	raw, err := oneShotRequest("agent.list", struct{}{})
	if err != nil {
		return err
	}
	var res agentListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ag := range res.Agents {
		cp := ag
		a.agents[ag.PaneID] = &cp
		_ = cp
	}
	a.updateWorkingFlagLocked()
	log.Printf("agent list: %d agents (urgent=%s)", len(a.agents), a.mostUrgent)
	return nil
}

// ---------------------------------------------------------------------------
// poll loop — fallback to catch any status changes missed by events
// ---------------------------------------------------------------------------

func (a *app) pollLoop() {
	for {
		select {
		case <-a.pollTick.C:
			if !a.isRunning() {
				return
			}
			a.pollAgents()
		case <-a.pollDone:
			return
		}
	}
}

func (a *app) pollAgents() {
	raw, err := oneShotRequest("agent.list", struct{}{})
	if err != nil {
		log.Printf("poll error: %v", err)
		return
	}
	var res agentListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		log.Printf("poll parse: %v", err)
		return
	}

	a.mu.Lock()
	prev := a.mostUrgent

	// Build new map from poll
	newAgents := make(map[string]*agentInfo, len(res.Agents))
	for _, ag := range res.Agents {
		cp := ag
		newAgents[ag.PaneID] = &cp
	}
	// Detect additions / updates
	changed := false
	for pid, ag := range newAgents {
		existing, ok := a.agents[pid]
		if !ok || existing.AgentStatus != ag.AgentStatus || existing.Agent != ag.Agent {
			changed = true
		}
	}
	// Detect removals
	for pid := range a.agents {
		if _, ok := newAgents[pid]; !ok {
			changed = true
		}
	}

	if changed {
		a.agents = newAgents
		a.updateWorkingFlagLocked()
		cur := a.mostUrgent
		a.mu.Unlock()

		log.Printf("poll: agents changed (%d total, urgent=%s)", len(a.agents), cur)
		a.updateMenu()
		if prev != cur {
			a.mu.Lock()
			a.showingIdle = false
			if cur == "working" {
				a.frame = 0
			}
			a.mu.Unlock()
		}

		// Reconnect subscription so per-pane status_changed events match
		a.mu.Lock()
		conn := a.conn
		a.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
	} else {
		a.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// persistent subscription stream
// ---------------------------------------------------------------------------

func (a *app) subscriptionLoop() {
	for {
		if !a.isRunning() {
			return
		}
		if err := a.connectSubscription(); err != nil {
			log.Printf("sub connect: %v — retry 3s", err)
			time.Sleep(3 * time.Second)
			continue
		}

		// Build subscription list with per-pane agent_status_changed
		a.mu.Lock()
		var subs []subscriptionEntry
		// Only subscribe to status_changed per-pane.
		// Lifecycle events (agent_detected, pane.created, pane.closed) produce
		// stale replay on every reconnect, so we ignore them and rely on
		// the poll loop to reconcile the agent list every 3 s.
		// Per-pane status change subscriptions
		for pid := range a.agents {
			subs = append(subs, subscriptionEntry{
				Type:   "pane.agent_status_changed",
				PaneID: pid,
			})
		}
		subscribedAt := time.Now().UnixNano()
		a.mu.Unlock()

		req := rpcRequest{
			ID:     fmt.Sprintf("sub_%d", subscribedAt),
			Method: "events.subscribe",
			Params: subscribeParams{Subscriptions: subs},
		}
		if err := json.NewEncoder(a.conn).Encode(req); err != nil {
			log.Printf("sub send: %v — reconnect", err)
			a.conn.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		// Read acknowledgement
		if !a.scanner.Scan() {
			log.Printf("sub ack: %v — reconnect", a.scanner.Err())
			a.conn.Close()
			time.Sleep(3 * time.Second)
			continue
		}
		var ack rpcResponse
		if err := json.Unmarshal(a.scanner.Bytes(), &ack); err != nil {
			log.Printf("sub ack parse: %v", err)
		} else if ack.Error != nil {
			log.Printf("sub ack error: %s - %s", ack.Error.Code, ack.Error.Message)
			a.conn.Close()
			time.Sleep(3 * time.Second)
			continue
		} else {
			a.mu.Lock()
			a.subscribedAt = subscribedAt
			a.mu.Unlock()
			log.Printf("subscription started (%d pane subs)", len(subs))
		}

		a.readEvents()
	}
}

func (a *app) connectSubscription() error {
	path := socketPath()
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", path, err)
	}
	a.conn = conn
	a.scanner = bufio.NewScanner(conn)
	a.scanner.Buffer(make([]byte, 128*1024), 512*1024)
	return nil
}

func (a *app) readEvents() {
	for a.scanner.Scan() {
		if !a.isRunning() {
			return
		}
		line := a.scanner.Bytes()

		// Skip RPC responses (acks)
		var maybeRpc struct{ ID string `json:"id"` }
		if json.Unmarshal(line, &maybeRpc) == nil && maybeRpc.ID != "" {
			continue
		}

		var evt herdrEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}

		switch evt.Event {
		case "pane_agent_status_changed":
			a.handleStatusChanged(evt.Data)
		case "pane_closed":
			a.handlePaneClosed(evt.Data)
		}
		// pane_agent_detected and pane_created produce stale replay on
		// every subscription reconnect. Ignore them — poll reconciles.
	}
	log.Printf("event stream ended: %v", a.scanner.Err())
	a.conn.Close()
}

// ---------------------------------------------------------------------------
// event handlers
// ---------------------------------------------------------------------------

func (a *app) handleStatusChanged(raw json.RawMessage) {
	var d agentStatusChangedData
	if err := json.Unmarshal(raw, &d); err != nil {
		return
	}
	log.Printf("status: %s → %s (%s)", d.PaneID, d.AgentStatus, d.Agent)

	a.mu.Lock()
	prev := a.mostUrgent

	if existing, ok := a.agents[d.PaneID]; ok {
		existing.AgentStatus = d.AgentStatus
	} else {
		a.agents[d.PaneID] = &agentInfo{
			PaneID:      d.PaneID,
			WorkspaceID: d.WorkspaceID,
			Agent:       d.Agent,
			AgentStatus: d.AgentStatus,
		}
	}
	a.updateWorkingFlagLocked()
	cur := a.mostUrgent
	a.mu.Unlock()

	a.updateMenu()
	if prev != cur {
		a.mu.Lock()
		a.showingIdle = false
		if cur == "working" {
			a.frame = 0
		}
		a.mu.Unlock()
	}
}



func (a *app) handlePaneClosed(raw json.RawMessage) {
	var d paneClosedData
	if err := json.Unmarshal(raw, &d); err != nil {
		return
	}
	a.mu.Lock()
	if _, existed := a.agents[d.PaneID]; existed {
		prev := a.mostUrgent
		delete(a.agents, d.PaneID)
		a.updateWorkingFlagLocked()
		a.mu.Unlock()
		a.updateMenu()
		if prev != a.mostUrgent {
			a.mu.Lock()
			a.showingIdle = false
			a.mu.Unlock()
		}
	} else {
		a.mu.Unlock()
	}
	// Poll will rebuild subscriptions on next cycle; no need to reconnect.
}

// ---------------------------------------------------------------------------
// click handling
// ---------------------------------------------------------------------------

// watchClick listens for clicks on a dynamic agent menu item and focuses
// that agent pane within Herdr.
func (a *app) watchClick(di *dynamicItem) {
	for range di.item.ClickedCh {
		focusAgent(di.paneID)
	}
}

func focusAgent(paneID string) {
	if err := exec.Command("herdr", "agent", "focus", paneID).Run(); err != nil {
		log.Printf("agent focus %s: %v", paneID, err)
	}
}

func (a *app) isRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

func (a *app) updateWorkingFlagLocked() {
	a.anyWorking = false
	a.anyBlocked = false
	a.anyDone = false
	a.anyUnknown = false
	for _, ag := range a.agents {
		switch ag.AgentStatus {
		case "working":
			a.anyWorking = true
		case "blocked":
			a.anyBlocked = true
		case "done":
			a.anyDone = true
		case "unknown":
			a.anyUnknown = true
		}
	}
	switch {
	case a.anyBlocked:
		a.mostUrgent = "blocked"
	case a.anyWorking:
		a.mostUrgent = "working"
	case a.anyDone:
		a.mostUrgent = "done"
	case a.anyUnknown:
		a.mostUrgent = "unknown"
	default:
		a.mostUrgent = "idle"
	}
}

func (a *app) applyIcon() {
	icon := a.iconForUrgency()
	if icon != nil {
		systray.SetIcon(icon)
	}
}

// iconForUrgency returns the static icon for the most urgent state,
// or nil if the state uses animated frames (working).
func (a *app) iconForUrgency() []byte {
	a.updateWorkingFlagLocked()
	switch a.mostUrgent {
	case "blocked":
		return icons.Blocked()
	case "working":
		return nil // animation loop handles this
	case "done":
		return icons.Done()
	case "unknown":
		return icons.Unknown()
	default:
		return a.idleIcon
	}
}

// ---------------------------------------------------------------------------
// animation
// ---------------------------------------------------------------------------

// spinnerChars cycles to animate the menu text for working agents.
// Each frame updates the symbol after the agent name so you see motion.
var spinnerChars = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

func (a *app) animationLoop() {
	for {
		select {
		case <-a.animTick.C:
			if !a.isRunning() {
				return
			}
			a.mu.Lock()
			needsAnim := a.mostUrgent == "working"
			if needsAnim {
				a.frame = (a.frame + 1) % 4
				systray.SetIcon(a.animFrames[a.frame])
				a.showingIdle = false
				// Spin menu symbols for working agents
				a.spinnerFrame = (a.spinnerFrame + 1) % len(spinnerChars)
				sp := spinnerChars[a.spinnerFrame]
				for _, di := range a.mItems {
					ag, ok := a.agents[di.paneID]
					if ok && ag.AgentStatus == "working" {
						di.item.SetTitle(fmt.Sprintf("%s [%s]  %s", ag.Agent, ag.WorkspaceID, sp))
					}
				}
			} else if !a.showingIdle {
				// Transition to a static state icon
				icon := a.iconForUrgency()
				if icon != nil {
					systray.SetIcon(icon)
				}
				a.showingIdle = true
				// Restore static symbols for all agents
				for _, di := range a.mItems {
					ag, ok := a.agents[di.paneID]
					if ok {
						di.item.SetTitle(fmt.Sprintf("%s [%s]  %s", ag.Agent, ag.WorkspaceID, statusSymbol(ag.AgentStatus)))
					}
				}
			}
			a.mu.Unlock()
		case <-a.animDone:
			return
		}
	}
}

// ---------------------------------------------------------------------------
// menu
// ---------------------------------------------------------------------------

func (a *app) rebuildMenu() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, di := range a.mItems {
		di.item.Hide()
	}
	a.mItems = nil
	a.updateStatusLabelLocked()

	for _, ag := range a.agents {
		item := systray.AddMenuItem(
			fmt.Sprintf("%s [%s]  %s", ag.Agent, ag.WorkspaceID, statusSymbol(ag.AgentStatus)),
			fmt.Sprintf("Pane: %s", ag.PaneID),
		)
		di := &dynamicItem{item: item, paneID: ag.PaneID}
		go a.watchClick(di)
		a.mItems = append(a.mItems, di)
	}
}

func (a *app) updateMenu() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.updateStatusLabelLocked()

	seen := make(map[string]bool, len(a.agents))
	for _, ag := range a.agents {
		seen[ag.PaneID] = true
	}
	existing := make(map[string]*dynamicItem, len(a.mItems))
	for _, di := range a.mItems {
		existing[di.paneID] = di
	}

	var keep []*dynamicItem
	for _, di := range a.mItems {
		if seen[di.paneID] {
			keep = append(keep, di)
		} else {
			di.item.Hide()
		}
	}
	a.mItems = keep

	for _, ag := range a.agents {
		if di, ok := existing[ag.PaneID]; ok {
			di.item.SetTitle(fmt.Sprintf("%s [%s]  %s", ag.Agent, ag.WorkspaceID, statusSymbol(ag.AgentStatus)))
		} else {
			item := systray.AddMenuItem(
				fmt.Sprintf("%s [%s]  %s", ag.Agent, ag.WorkspaceID, statusSymbol(ag.AgentStatus)),
				fmt.Sprintf("Pane: %s", ag.PaneID),
			)
			di := &dynamicItem{item: item, paneID: ag.PaneID}
			go a.watchClick(di)
			a.mItems = append(a.mItems, di)
		}
	}
}

func (a *app) updateStatusLabelLocked() {
	if a.statusLabel == nil {
		return
	}
	total := len(a.agents)
	if total == 0 {
		a.statusLabel.SetTitle("no agents detected")
		return
	}
	blocked, working, doneCnt, idle, unknown := 0, 0, 0, 0, 0
	for _, ag := range a.agents {
		switch ag.AgentStatus {
		case "blocked":
			blocked++
		case "working":
			working++
		case "done":
			doneCnt++
		case "idle":
			idle++
		default:
			unknown++
		}
	}
	// Build a compact summary
	parts := ""
	if blocked > 0 { parts += fmt.Sprintf(" %d🚧", blocked) }
	if working > 0 { parts += fmt.Sprintf(" %d🔥", working) }
	if doneCnt > 0  { parts += fmt.Sprintf(" %d✅", doneCnt) }
	if idle > 0    { parts += fmt.Sprintf(" %d💤", idle) }
	if unknown > 0 { parts += fmt.Sprintf(" %d❓", unknown) }
	a.statusLabel.SetTitle(fmt.Sprintf("%d agent(s) —%s", total, parts))
}

func statusSymbol(s string) string {
	switch s {
	case "working":
		return "⏳" // actively processing (matches animated spinner icon)
	case "blocked":
		return "🚧" // waiting on input/permission
	case "done":
		return "✅" // finished
	case "idle":
		return "💤" // ready, not active
	default:
		return "❓"
	}
}
