#!/usr/bin/env bash
# Integration test for herdr-systray
# Tests: connectivity, agent list, subscription, exit
set -euo pipefail

BINARY="${1:-./herdr-systray}"
PASS=0
FAIL=0

ok()   { PASS=$((PASS+1)); echo "  ok $1"; }
fail() { FAIL=$((FAIL+1)); echo "  FAIL $1"; }

echo "=== herdr-systray integration test ==="
echo ""

# 1. Check herdr server
echo "--- prerequisite: herdr server running ---"
if herdr status 2>&1 | grep -q "status: running"; then
    ok "herdr server is running"
else
    fail "herdr server not running -- start with 'herdr' first"
    echo ""
    echo "Results: $PASS passed, $FAIL failed"
    exit 1
fi

# 2. Start the systray app
echo "--- starting herdr-systray ---"
LOG=$(mktemp /tmp/herdr-test.XXXXXX)
"$BINARY" > "$LOG" 2>&1 &
TRAY_PID=$!
sleep 2

if kill -0 "$TRAY_PID" 2>/dev/null; then
    ok "binary started (PID $TRAY_PID)"
else
    fail "binary failed to start"
    cat "$LOG"
    exit 1
fi

# 3. Check log for successful connection
echo "--- checking connectivity ---"
if grep -q "agent list:" "$LOG"; then
    ok "agent list fetched"
else
    fail "no agent list in log"
fi

if grep -q "subscription started" "$LOG"; then
    ok "event subscription established"
else
    fail "no subscription in log"
fi

# 4. Check agent count
echo "--- checking agent tracking ---"
AGENT_COUNT=$(grep -oP 'agent list: \K\d+' "$LOG" | head -1)
if [ -n "$AGENT_COUNT" ] && [ "$AGENT_COUNT" -ge 0 ] 2>/dev/null; then
    ok "tracking $AGENT_COUNT agent(s)"
else
    fail "could not determine agent count"
fi

# 5. Check no errors
echo "--- checking for errors ---"
if grep -qi "error\|panic\|fatal" "$LOG" 2>/dev/null; then
    fail "errors found in log"
    grep -i "error\|panic\|fatal" "$LOG" | head -5
else
    ok "no errors in log"
fi

# 6. Test exit
echo "--- testing exit ---"
TRAY_PID=$(pgrep -n herdr-systray 2>/dev/null || echo "$TRAY_PID")
kill -INT "$TRAY_PID" 2>/dev/null || true
sleep 1

if ! kill -0 "$TRAY_PID" 2>/dev/null; then
    ok "exited cleanly on SIGINT"
else
    fail "process still alive after SIGINT -- killing"
    kill -9 "$TRAY_PID" 2>/dev/null || true
fi

# 7. Check shutdown in log
echo "--- checking shutdown log ---"
if grep -q "SIGINT" "$LOG"; then
    ok "SIGINT log message present"
else
    fail "missing SIGINT log message"
fi

# ---- results ----
rm -f "$LOG"
echo ""
echo "Results: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
