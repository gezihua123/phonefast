#!/bin/bash
# =============================================================================
# phonefast 准出测试 (Gate Test)
# 发布/PR 合并前运行，覆盖基础功能、MCP 协议、性能基准。
# =============================================================================
# 用法:
#   bash tests/gate.sh              # 全部测试
#   bash tests/gate.sh --quick      # 快速模式（跳过性能基准）
#   bash tests/gate.sh --perf       # 仅性能基准
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
BINARY="$PROJECT_DIR/dist/phonefast"

START_TIME=$(date +%s)
PASS=0
FAIL=0
SKIP=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# ── Helpers ──────────────────────────────────────────────────────────────────
pass()  { PASS=$((PASS+1)); echo -e "  ${GREEN}✓${NC}  $1"; }
fail()  { FAIL=$((FAIL+1)); echo -e "  ${RED}✗${NC}  $1  ${RED}($2)${NC}"; }
skip()  { SKIP=$((SKIP+1)); echo -e "  ${YELLOW}⊘${NC}  $1  (skipped: $2)"; }
header(){ echo -e "\n${CYAN}━━━ $1 ━━━${NC}"; }
elapsed(){ echo "$(($(date +%s) - START_TIME))s"; }

QUICK=false
PERF_ONLY=false
case "${1:-}" in
    --quick) QUICK=true ;;
    --perf)  PERF_ONLY=true ;;
esac

# ── 0. Prerequisites ─────────────────────────────────────────────────────────
header "0. Prerequisites"

if [ ! -f "$BINARY" ]; then
    fail "phonefast binary" "not found at $BINARY, run 'go build -o dist/phonefast ./cmd/phonefast/'"
else
    pass "binary exists ($(du -h "$BINARY" | cut -f1))"
fi

# scrcpy-server.jar is now embedded in the binary via Go embed — no external file needed.
pass "scrcpy-server jar (embedded in binary)"

if ! command -v ffmpeg &>/dev/null; then
    fail "ffmpeg" "not in PATH (screenshot requires ffmpeg)"
else
    pass "ffmpeg available ($(ffmpeg -version 2>&1 | head -1 | cut -d' ' -f3))"
fi

if ! command -v adb &>/dev/null; then
    fail "adb" "not in PATH"
    echo "FATAL: adb required"; exit 1
fi

DEVICE_COUNT=$("$BINARY" devices 2>/dev/null | grep -c "device" || true)
if [ "$DEVICE_COUNT" -eq 0 ]; then
    fail "adb device" "no device connected"
    echo "FATAL: connect a device via USB or TCP"
    exit 1
fi
pass "device connected ($DEVICE_COUNT device(s))"

# ── 1. Go unit tests ─────────────────────────────────────────────────────────
header "1. Go Unit Tests"

go_test_out=$(cd "$PROJECT_DIR" && go test ./... 2>&1) && go_ok=true || go_ok=false
if $go_ok; then
    pass "go test ./..."
else
    echo "$go_test_out" | tail -20
    fail "go test ./..." "unit tests failed"
fi

# ── 2. Build ─────────────────────────────────────────────────────────────────
if ! $PERF_ONLY; then
header "2. Build"

if go build -o "$BINARY" ./cmd/phonefast/ 2>&1; then
    pass "go build"
else
    fail "go build" "compilation failed"
    exit 1
fi
fi

# ── 3. Release Smoke Test ────────────────────────────────────────────────────
if ! $PERF_ONLY; then
header "3. Release Smoke Test (tests/test_release.py)"

smoke_out=$(python3 "$SCRIPT_DIR/test_release.py" 2>&1) && smoke_ok=true || smoke_ok=false
echo "$smoke_out"
smoke_pass=$(echo "$smoke_out" | grep -c "✓" || true)
smoke_fail=$(echo "$smoke_out" | grep -c "✗" || true)
if $smoke_ok && [ "$smoke_fail" -eq 0 ]; then
    pass "test_release.py ($smoke_pass checks passed)"
else
    fail "test_release.py" "$smoke_fail failures"
fi
fi

# ── 4. MCP Protocol Test ─────────────────────────────────────────────────────
if ! $PERF_ONLY; then
header "4. MCP Protocol Test (tests/test_mcp.py)"

mcp_out=$(python3 "$SCRIPT_DIR/test_mcp.py" 2>&1) && mcp_ok=true || mcp_ok=false
echo "$mcp_out" | tail -20
mcp_pass=$(echo "$mcp_out" | grep -c "✓" || true)
mcp_fail=$(echo "$mcp_out" | grep -c "✗" || true)
if $mcp_ok && [ "$mcp_fail" -eq 0 ]; then
    pass "test_mcp.py ($mcp_pass checks passed)"
else
    fail "test_mcp.py" "$mcp_fail failures"
fi
fi

# ── 5. Daemon Health Check ───────────────────────────────────────────────────
if ! $PERF_ONLY; then
header "5. Daemon Health"

# Start daemon
"$BINARY" daemon --stop 2>/dev/null || true
sleep 1
"$BINARY" daemon 2>/dev/null || true
sleep 3

daemon_status=$("$BINARY" daemon --status 2>&1) || true
if echo "$daemon_status" | grep -q "running"; then
    pass "daemon running"
else
    fail "daemon" "$daemon_status"
fi

# Quick control test
if "$BINARY" --daemon back >/dev/null 2>&1; then
    pass "daemon back"
else
    fail "daemon back" "control failed"
fi

if "$BINARY" --daemon screenshot /tmp/gate_daemon_screen.png >/dev/null 2>&1; then
    FSIZE=$(stat -f%z /tmp/gate_daemon_screen.png 2>/dev/null || stat -c%s /tmp/gate_daemon_screen.png 2>/dev/null || echo 0)
    if [ "$FSIZE" -gt 1000 ]; then
        pass "daemon screenshot ($FSIZE bytes)"
    else
        fail "daemon screenshot" "file too small: $FSIZE"
    fi
else
    fail "daemon screenshot" "command failed"
fi

rm -f /tmp/gate_daemon_screen.png
fi

# ── 6. Device Cleanup ─────────────────────────────────────────────────────────
if ! $PERF_ONLY; then
header "6. Device Cleanup"

# Stop daemon before cleanup
"$BINARY" daemon --stop 2>/dev/null || true
sleep 1

# Remove scrcpy-server.jar from device
DEV_JAR="/data/local/tmp/scrcpy-server.apk"
if adb shell "test -f $DEV_JAR && echo exists" 2>/dev/null | grep -q "exists"; then
    if adb shell "rm $DEV_JAR" 2>/dev/null; then
        pass "device jar removed"
    else
        fail "device jar removal" "adb rm failed"
    fi
else
    pass "device jar absent (already clean)"
fi
fi

# ── 7. Performance Benchmark ─────────────────────────────────────────────────
if ! $QUICK; then
header "7. Performance Benchmark (tests/benchmark.py --quick)"

bench_out=$(python3 "$SCRIPT_DIR/benchmark.py" --quick 2>&1) && bench_ok=true || bench_ok=false
echo "$bench_out"
if $bench_ok; then
    pass "benchmark.py --quick"
else
    fail "benchmark.py --quick" "benchmark failed"
fi
else
    skip "Performance Benchmark" "--quick mode"
fi

# ── 8. Benchmark daemon latency threshold ─────────────────────────────────────
if ! $QUICK; then
header "8. Daemon Latency Threshold"

LAT_OK=true
for i in 1 2 3; do
    start=$(perl -MTime::HiRes=time -e 'printf "%.6f", time')
    "$BINARY" --daemon back >/dev/null 2>&1
    end=$(perl -MTime::HiRes=time -e 'printf "%.6f", time')
    lat=$(perl -e "printf '%.0f', ($end - $start) * 1000")
    if [ "$lat" -gt 100 ]; then
        echo "  back #$i: ${RED}${lat}ms${NC} (threshold: 100ms)"
        LAT_OK=false
    else
        echo "  back #$i: ${GREEN}${lat}ms${NC}"
    fi
done

if $LAT_OK; then
    pass "daemon latency < 100ms"
else
    fail "daemon latency" "exceeded 100ms threshold"
fi
fi

# ── Summary ──────────────────────────────────────────────────────────────────
header "Summary"

TOTAL_TIME=$(elapsed)
echo ""
echo "  Total time: ${TOTAL_TIME}"
echo "  Pass:       ${GREEN}${PASS}${NC}"
echo "  Fail:       ${RED}${FAIL}${NC}"
echo "  Skip:       ${YELLOW}${SKIP}${NC}"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo -e "${RED}GATE FAILED — ${FAIL} check(s) failed${NC}"
    exit 1
else
    echo -e "${GREEN}GATE PASSED — all ${PASS} checks passed${NC}"
    exit 0
fi
