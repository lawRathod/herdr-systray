package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"

	"herdr-systray/icons"

	"github.com/getlantern/systray"
)

const version = "0.2.0"

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

type workspaceListResult struct {
	Type       string `json:"type"`
	Workspaces []struct {
		WorkspaceID string `json:"workspace_id"`
		Label       string `json:"label"`
	} `json:"workspaces"`
}

type subscribeParams struct {
	Subscriptions []subscriptionEntry `json:"subscriptions"`
}

// ---------------------------------------------------------------------------
// Telegram notifications
// ---------------------------------------------------------------------------

type telegramConfig struct {
	mu       sync.Mutex
	token    string
	chatID   string
	notify   map[string]bool // states that trigger a notification
	notifyOn bool            // /on /off kill-switch, persisted across restarts
	enabled  bool
}

// notifyStatePath is the file holding the /on /off toggle ("on" or "off").
var notifyStatePath = func() string {
	home, _ := os.UserHomeDir()
	return home + "/.config/herdr-systray/notify_state"
}

func loadNotifyState() bool {
	b, err := os.ReadFile(notifyStatePath())
	if err != nil {
		return true // default: notifications on
	}
	return strings.TrimSpace(string(b)) == "on"
}

func (a *app) setNotifyOn(on bool) {
	a.tg.mu.Lock()
	a.tg.notifyOn = on
	a.tg.mu.Unlock()
	state := "off"
	if on {
		state = "on"
	}
	// security: state file is user-controlled, best-effort write
	if err := os.WriteFile(notifyStatePath(), []byte(state+"\n"), 0600); err != nil {
		log.Printf("telegram: persist notify state: %v", err)
	}
	log.Printf("telegram: notifications %s", state)
}

// telegramConfigFile is the KEY=VALUE config file, read before env vars.
func telegramConfigFile() string {
	home, _ := os.UserHomeDir()
	return home + "/.config/herdr-systray/config"
}

func readEnvFile(path string) map[string]string {
	vals := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return vals
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return vals
}

func loadTelegramConfig() telegramConfig {
	cfg := telegramConfig{notify: make(map[string]bool), notifyOn: loadNotifyState()}

	// Seed env from config file, but never override real env vars
	for k, v := range readEnvFile(telegramConfigFile()) {
		if _, ok := os.LookupEnv(k); !ok {
			_ = os.Setenv(k, v)
		}
	}

	cfg.token = os.Getenv("HERDR_TELEGRAM_TOKEN")
	cfg.chatID = os.Getenv("HERDR_TELEGRAM_CHAT_ID")

	states := os.Getenv("HERDR_TELEGRAM_NOTIFY")
	if states == "" {
		states = "blocked,done"
	}
	for _, s := range strings.Split(states, ",") {
		if s = strings.TrimSpace(s); s != "" {
			cfg.notify[s] = true
		}
	}
	cfg.enabled = cfg.token != ""
	return cfg
}

// initTelegram resolves the chat id (if missing) and sends a test message.
// Runs in a goroutine so tray startup never blocks on the network.
func (a *app) initTelegram() {
	if !a.tg.enabled {
		return
	}
	token := a.tg.token
	chatID := a.tg.chatID
	var lastID int64
	if chatID == "" {
		id, last, err := resolveTelegramChatID(token)
		if err != nil {
			log.Printf("telegram: chat id lookup: %v", err)
			return
		}
		chatID = id
		lastID = last
		a.tg.mu.Lock()
		a.tg.chatID = id
		a.tg.mu.Unlock()
		log.Printf("telegram: chat id auto-detected")
	}
	if err := sendTelegramMessage(token, chatID, "herdr-systray connected — agent notifications active"); err != nil {
		log.Printf("telegram: test message: %v", err)
		return
	}
	log.Printf("telegram notifications enabled (notify on: %s)", os.Getenv("HERDR_TELEGRAM_NOTIFY"))

	go a.telegramPollLoop(token, chatID, lastID)
}

var telegramHTTP = &http.Client{Timeout: 15 * time.Second}
var telegramPollHTTP = &http.Client{Timeout: 70 * time.Second}

type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  struct {
		Chat struct {
			ID float64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// getTelegramUpdates long-polls getUpdates (server holds the connection up to
// timeoutSec) and returns the delivered updates, if any.
func getTelegramUpdates(token string, offset int64, timeoutSec int) ([]telegramUpdate, error) {
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=%d", token, offset, timeoutSec), nil)
	if err != nil {
		return nil, err
	}
	resp, err := telegramPollHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("telegram api %d: %s", resp.StatusCode, b)
	}
	var up struct {
		Result []telegramUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&up); err != nil {
		return nil, err
	}
	return up.Result, nil
}

// sendTelegramMessage posts a plain-text message to the bot's chat.
// Never include the token in error/log output.
func sendTelegramMessage(token, chatID, text string) error {
	body, err := json.Marshal(map[string]string{
		"chat_id": chatID,
		"text":    text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := telegramHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("telegram api %d: %s", resp.StatusCode, b)
	}
	return nil
}

// resolveTelegramChatID returns the chat id of the most recent message the
// user sent to the bot (they must have messaged it first, e.g. /start),
// plus the highest update id seen so the poll loop can start past it.
func resolveTelegramChatID(token string) (string, int64, error) {
	updates, err := getTelegramUpdates(token, 0, 30)
	if err != nil {
		return "", 0, err
	}
	var lastID int64
	for i := len(updates) - 1; i >= 0; i-- {
		u := updates[i]
		if u.UpdateID > lastID {
			lastID = u.UpdateID
		}
		if u.Message.Chat.ID != 0 {
			return fmt.Sprintf("%.0f", u.Message.Chat.ID), lastID, nil
		}
	}
	return "", lastID, fmt.Errorf("no chat found — message the bot first (e.g. /start)")
}

// telegramPollLoop long-polls for updates and answers /status commands with
// the current agent report. Runs until the process exits.
func (a *app) telegramPollLoop(token, chatID string, startOffset int64) {
	offset := startOffset + 1
	for {
		updates, err := getTelegramUpdates(token, offset, 50)
		if err != nil {
			log.Printf("telegram: poll: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			reply := a.handleTelegramUpdate(u, token, chatID)
			if reply != "" {
				if err := sendTelegramMessage(token, chatID, reply); err != nil {
					log.Printf("telegram: reply: %v", err)
				}
			}
		}
	}
}

// handleTelegramUpdate processes one bot update from our chat and returns the
// reply text, or "" if there is nothing to answer. Pure dispatch — testable
// without the network.
func (a *app) handleTelegramUpdate(u telegramUpdate, token, chatID string) string {
	if fmt.Sprintf("%.0f", u.Message.Chat.ID) != chatID {
		return ""
	}
	switch strings.TrimSpace(u.Message.Text) {
	case "/status":
		return a.agentStatusReport()
	case "/off":
		a.setNotifyOn(false)
		return "Notifications OFF — no more agent alerts"
	case "/on":
		a.setNotifyOn(true)
		return "Notifications ON — agent alerts will be sent"
	}
	return ""
}

// agentStatusReport renders all agents sorted by urgency (blocked first),
// then pane id. Safe to call from any goroutine.
func (a *app) agentStatusReport() string {
	a.mu.Lock()
	agents := make([]*agentInfo, 0, len(a.agents))
	for _, ag := range a.agents {
		cp := *ag
		agents = append(agents, &cp)
	}
	total := len(agents)
	a.mu.Unlock()

	a.mu.Lock()
	labels := make(map[string]string, len(a.workspaceLabels))
	for id, l := range a.workspaceLabels {
		labels[id] = l
	}
	a.mu.Unlock()

	a.tg.mu.Lock()
	notifyOn := a.tg.notifyOn
	states := make([]string, 0, len(a.tg.notify))
	for s := range a.tg.notify {
		states = append(states, s)
	}
	a.tg.mu.Unlock()
	sort.Strings(states)

	rank := map[string]int{"blocked": 0, "working": 1, "done": 2, "idle": 3, "unknown": 4}
	sort.Slice(agents, func(i, j int) bool {
		ri, rj := rank[agents[i].AgentStatus], rank[agents[j].AgentStatus]
		if ri != rj {
			return ri < rj
		}
		return agents[i].PaneID < agents[j].PaneID
	})
	notify := "OFF"
	if notifyOn {
		notify = "ON"
	}
	var b strings.Builder
	if total == 0 {
		b.WriteString("Herdr: no agents detected\n")
	} else {
		fmt.Fprintf(&b, "Herdr agents (%d):\n", total)
		for _, ag := range agents {
			label := labels[ag.WorkspaceID]
			if label == "" {
				label = ag.WorkspaceID
			}
			fmt.Fprintf(&b, "%s %s [%s] — %s (%s)\n",
				statusSymbol(ag.AgentStatus), ag.Agent, ag.WorkspaceID, ag.AgentStatus, label)
		}
	}
	fmt.Fprintf(&b, "\nNotifications: %s (%s)", notify, strings.Join(states, ", "))
	return b.String()
}

// notifyStateChange sends a Telegram notification for a state transition.
// Non-blocking; failures are logged without the token.
func (a *app) notifyStateChange(ag *agentInfo, from string) {
	a.tg.mu.Lock()
	if !a.tg.enabled || !a.tg.notify[ag.AgentStatus] || !a.tg.notifyOn || a.tg.chatID == "" {
		a.tg.mu.Unlock()
		return
	}
	token, chatID := a.tg.token, a.tg.chatID
	hook := a.notifyHook
	a.tg.mu.Unlock()
	if hook != nil {
		hook(ag, from)
		return
	}

	label := a.workspaceLabel(ag.WorkspaceID)
	text := fmt.Sprintf("%s %s [%s] (%s): %s → %s",
		statusSymbol(ag.AgentStatus), ag.Agent, ag.PaneID, label, from, ag.AgentStatus)
	go func() {
		if err := sendTelegramMessage(token, chatID, text); err != nil {
			log.Printf("telegram: send: %v", err)
		}
	}()
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

	quitCh chan struct{}

	tg telegramConfig
	// notifyHook replaces the Telegram HTTP call — set only in tests
	notifyHook func(ag *agentInfo, from string)

	workspaceLabels map[string]string // workspace_id -> label (project name)
	polls           int               // poll counter for periodic workspace refresh
}

type dynamicItem struct {
	item   *systray.MenuItem
	paneID string
	done   chan struct{}
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	flag.Parse()

	// config subcommand — handle before help/version/daemon
	if args := flag.Args(); len(args) > 0 {
		switch args[0] {
		case "config":
			handleConfig(args[1:])
			return
		case "uninstall":
			handleUninstall()
			return
		}
	}

	if cliHelp {
		fmt.Print(`herdr-systray — system tray monitor for Herdr coding agents

Usage:
  herdr-systray [flags]
  herdr-systray config autostart [remove|status]
  herdr-systray uninstall

Flags:
  -d, --daemon       fork into background (detach from terminal)
  -h, --help         show this help
  -v, --version      show version
  -l, --log <path>   log file path (default /tmp/herdr-systray.log)
  -s, --socket <path>  herdr socket path (default ~/.config/herdr/herdr.sock)

Subcommands:
  config autostart         Install autostart entry (starts -d on login)
  config autostart remove  Remove autostart entry
  config autostart status  Check autostart status
  uninstall                Remove binary and autostart entry

The log file defaults to /tmp/herdr-systray.log — it's a 100 KB circular
buffer that never grows beyond that size.

Environment variables (also read from ~/.config/herdr-systray/config):
  HERDR_SOCKET_PATH       overrides the default herdr socket path
  HERDR_TELEGRAM_TOKEN    Telegram bot token (enables phone notifications)
  HERDR_TELEGRAM_CHAT_ID  Telegram chat id (auto-detected if empty)
  HERDR_TELEGRAM_NOTIFY   states that notify, comma-separated (default blocked,done)
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
		// security: re-exec self — os.Args[0] is the running binary's path, set by kernel
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
		tg:      loadTelegramConfig(),
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

	// Initial agent list
	if err := a.fetchAgentList(); err != nil {
		log.Printf("initial agent list: %v", err)
	}
	a.fetchWorkspaceLabels()
	a.rebuildMenu()
	a.applyIcon()

	// quitCh is used to forward click events from whichever quit item
	// is currently active (quit items are recreated during rebuildMenu
	// to keep Quit at the bottom after all agent items).
	a.quitCh = make(chan struct{}, 1)

	// Persistent subscription + polling
	a.animDone = make(chan struct{})
	a.animTick = time.NewTicker(400 * time.Millisecond)
	go a.animationLoop()

	a.pollDone = make(chan struct{})
	a.pollTick = time.NewTicker(3 * time.Second)
	go a.pollLoop()

	go a.subscriptionLoop()

	go a.initTelegram()

	// Menu quit + Ctrl+C — use os.Exit because systray.Quit() doesn't
	// reliably unblock systray.Run() on Linux+appindicator.
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		select {
		case <-a.quitCh:
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
		// security: cleanup during shutdown — error expected if conn is already closed
		_ = a.conn.Close()
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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		log.Printf("ring log: open %s: %v — falling back to stderr", path, err)
		return os.Stderr
	}
	// Trim if file somehow grew beyond max (e.g. user editing)
	if fi, _ := f.Stat(); fi != nil && fi.Size() > maxSize {
		_ = f.Truncate(maxSize) // security: best-effort trim — log file is disposable
		_, _ = f.Seek(0, io.SeekEnd)
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
	// security: best-effort ring buffer — seek errors mean we skip trim;
	// log may grow slightly beyond maxSize but recovers on next write.
	if _, err := w.f.Seek(over, io.SeekStart); err != nil {
		return n, nil
	}
	if _, err := io.ReadFull(w.f, dst); err != nil {
		_, _ = w.f.Seek(0, io.SeekEnd)
		return n, nil
	}
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return n, nil
	}
	if _, err := w.f.Write(dst); err != nil {
		_, _ = w.f.Seek(0, io.SeekEnd)
		return n, nil
	}
	_ = w.f.Truncate(w.maxSize) // best-effort: if this fails, file has trailing garbage but remains valid
	_, _ = w.f.Seek(0, io.SeekEnd)

	return n, nil
}

// File exposes the underlying *os.File for passing as stderr to the
// daemon child process so logs continue there.
func (w *ringLogWriter) File() *os.File { return w.f }

func (a *app) fetchWorkspaceLabels() {
	raw, err := oneShotRequest("workspace.list", struct{}{})
	if err != nil {
		return
	}
	var res workspaceListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return
	}
	a.mu.Lock()
	a.workspaceLabels = make(map[string]string, len(res.Workspaces))
	for _, ws := range res.Workspaces {
		a.workspaceLabels[ws.WorkspaceID] = ws.Label
	}
	a.mu.Unlock()
}

// workspaceLabel returns the project label for a workspace id, or the raw id.
func (a *app) workspaceLabel(wsID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if l, ok := a.workspaceLabels[wsID]; ok && l != "" {
		return l
	}
	return wsID
}

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
	a.polls++
	if a.polls%10 == 0 {
		a.fetchWorkspaceLabels()
	}
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
		// Collect real transitions (existing agent, status differs) so we
		// notify outside the lock. New agents and removals don't notify.
		var notifs []*agentInfo
		var froms []string
		for pid, ag := range newAgents {
			if old, ok := a.agents[pid]; ok && old.AgentStatus != ag.AgentStatus {
				cp := *ag
				notifs = append(notifs, &cp)
				froms = append(froms, old.AgentStatus)
			}
		}

		a.agents = newAgents
		a.updateWorkingFlagLocked()
		cur := a.mostUrgent
		a.mu.Unlock()

		for i, n := range notifs {
			a.notifyStateChange(n, froms[i])
		}

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
			// security: closing stale subscription connection; error is expected if already closed
			_ = conn.Close()
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
			// security: error path — close is best-effort
			_ = a.conn.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		// Read acknowledgement
		if !a.scanner.Scan() {
			log.Printf("sub ack: %v — reconnect", a.scanner.Err())
			// security: error path — close is best-effort
			_ = a.conn.Close()
			time.Sleep(3 * time.Second)
			continue
		}
		var ack rpcResponse
		if err := json.Unmarshal(a.scanner.Bytes(), &ack); err != nil {
			log.Printf("sub ack parse: %v", err)
		} else if ack.Error != nil {
			log.Printf("sub ack error: %s - %s", ack.Error.Code, ack.Error.Message)
			// security: error path — close is best-effort
			_ = a.conn.Close()
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
		var maybeRpc struct {
			ID string `json:"id"`
		}
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
	// security: cleanup after stream ends — close is best-effort
	_ = a.conn.Close()
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

	// Capture the transition (existing agent whose status actually changed)
	// so we can notify outside the lock. Brand-new agents don't notify.
	var notif *agentInfo
	var from string
	if existing, ok := a.agents[d.PaneID]; ok {
		if existing.AgentStatus != d.AgentStatus {
			from = existing.AgentStatus
			cp := *existing
			cp.AgentStatus = d.AgentStatus
			notif = &cp
		}
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

	if notif != nil {
		a.notifyStateChange(notif, from)
	}

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
	for {
		select {
		case <-di.item.ClickedCh:
			focusAgent(di.paneID)
		case <-di.done:
			return
		}
	}
}

func focusAgent(paneID string) {
	// Validate paneID: only allow word chars, colons, hyphens, underscores
	// This is defense-in-depth — paneID originates from the local Herdr server
	// but we validate before passing it to the CLI to prevent argument injection.
	for _, r := range paneID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ':' || r == '-' || r == '_' || r == '.' {
			continue
		}
		log.Printf("agent focus: rejected invalid paneID %q", paneID)
		return
	}
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
		close(di.done)
		di.item.Hide()
	}
	a.mItems = nil

	// Hide old quit item (we create a fresh one below)
	if a.quitItem != nil {
		a.quitItem.Hide()
	}

	a.updateStatusLabelLocked()

	for _, ag := range a.agents {
		item := systray.AddMenuItem(
			fmt.Sprintf("%s [%s]  %s", ag.Agent, ag.WorkspaceID, statusSymbol(ag.AgentStatus)),
			fmt.Sprintf("Pane: %s", ag.PaneID),
		)
		di := &dynamicItem{item: item, paneID: ag.PaneID, done: make(chan struct{})}
		go a.watchClick(di)
		a.mItems = append(a.mItems, di)
	}

	// Tail separator + quit — always at the bottom, after all agents
	systray.AddSeparator()
	a.quitItem = systray.AddMenuItem("Quit", "Quit Herdr systray")
	go func() {
		<-a.quitItem.ClickedCh
		a.quitCh <- struct{}{}
	}()
}

func (a *app) updateMenu() {
	// Quick check: do we have any genuinely new agents (not just updates)?
	// If so, do a full rebuild to keep agents before Quit in the right order.
	a.mu.Lock()
	needsRebuild := len(a.agents) > len(a.mItems)
	if !needsRebuild {
		seen := make(map[string]bool, len(a.mItems))
		for _, di := range a.mItems {
			seen[di.paneID] = true
		}
		for pid := range a.agents {
			if !seen[pid] {
				needsRebuild = true
				break
			}
		}
	}
	a.mu.Unlock()

	if needsRebuild {
		a.rebuildMenu()
		return
	}

	// Incremental update — just update titles and remove stale items.
	// No new agents, so ordering is preserved.
	a.mu.Lock()
	defer a.mu.Unlock()

	a.updateStatusLabelLocked()

	seen := make(map[string]bool, len(a.agents))
	for _, ag := range a.agents {
		seen[ag.PaneID] = true
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
		for _, di := range a.mItems {
			if di.paneID == ag.PaneID {
				di.item.SetTitle(fmt.Sprintf("%s [%s]  %s", ag.Agent, ag.WorkspaceID, statusSymbol(ag.AgentStatus)))
				break
			}
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
	if blocked > 0 {
		parts += fmt.Sprintf(" %d🚧", blocked)
	}
	if working > 0 {
		parts += fmt.Sprintf(" %d🔥", working)
	}
	if doneCnt > 0 {
		parts += fmt.Sprintf(" %d✅", doneCnt)
	}
	if idle > 0 {
		parts += fmt.Sprintf(" %d💤", idle)
	}
	if unknown > 0 {
		parts += fmt.Sprintf(" %d❓", unknown)
	}
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
