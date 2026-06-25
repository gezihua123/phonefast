#!/usr/bin/env bash
# MCP 协议测试 — 使用 STDIO 传输 + 命名管道
# 用法: bash test_mcp.sh [--quick]
set -uo pipefail
# Note: set -e NOT used — arithmetic expressions like ((rpc_id++))
# return exit code 1 when the value is 0, which would kill the script.

BINARY="${PHONEFAST_BIN:-../dist/phonefast}"
PASS=0
FAIL=0

ok()   { echo "  ✓  $1"; ((PASS++)); return 0; }
bad()  { echo "  ✗  $1  — ${2:-}"; ((FAIL++)); return 0; }

# ── preflight ─────────────────────────────────────────────────────────────
echo "============================================================"
echo "  phonefast MCP Protocol Test (STDIO)"
echo "============================================================"

if [[ ! -f "$BINARY" ]]; then
    echo "FATAL: $BINARY not found"
    exit 1
fi

if ! adb devices 2>/dev/null | tail -n +2 | grep -q 'device$'; then
    echo "FATAL: no ADB device connected"
    exit 1
fi
DEVICE=$(adb devices 2>/dev/null | tail -n +2 | grep 'device$' | head -1 | awk '{print $1}')
echo "Device: $DEVICE"

# ── setup: named pipe for JSON-RPC I/O, log file for stderr ───────────────
FIFO_IN="/tmp/phonefast_mcp_in"
FIFO_OUT="/tmp/phonefast_mcp_out"
LOG_FILE="/tmp/phonefast_mcp.log"

rm -f "$FIFO_IN" "$FIFO_OUT" "$LOG_FILE"
mkfifo "$FIFO_IN" "$FIFO_OUT"

# Start server first (opens FIFO_IN for reading), then keep write-end open
$BINARY serve --transport stdio < "$FIFO_IN" > "$FIFO_OUT" 2>"$LOG_FILE" &
SERVER_PID=$!

# Keep write-end of FIFO_IN open so server doesn't see EOF between requests
sleep 999999 > "$FIFO_IN" &
FIFO_KEEPER=$!

cleanup() {
    kill "$FIFO_KEEPER" 2>/dev/null || true
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    kill "$CAT_PID" 2>/dev/null || true
    cp "$RPC_LOG" "$SAVED_LOG" 2>/dev/null || true
    rm -f "$FIFO_IN" "$FIFO_OUT" "$LOG_FILE" "$RPC_LOG" "$COUNTER_FILE"
}
trap cleanup EXIT

echo ""
echo "Waiting for server to start (~2s)..."
sleep 2  # 服务器立即进入 STDIO 循环，只需等 Go 进程启动

if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "FATAL: server exited. Log:"
    cat "$LOG_FILE"
    exit 1
fi
echo "Server ready."

# ── RPC helper ─────────────────────────────────────────────────────────
# Uses FIFO_IN/FIFO_OUT for I/O, polls RPC_LOG for responses

RPC_LOG="/tmp/phonefast_mcp_rpclog"
SAVED_LOG="/tmp/phonefast_mcp_saved_$(date +%H%M%S).log"
: > "$RPC_LOG"

# Background process: continuously read FIFO_OUT, append each line to RPC_LOG
cat "$FIFO_OUT" >> "$RPC_LOG" &
CAT_PID=$!

COUNTER_FILE="/tmp/phonefast_mcp_counter"
echo 0 > "$COUNTER_FILE"

call_rpc() {
    local method="$1"
    local params
    if [[ -n "${2:-}" ]]; then
        params="$2"
    else
        params="{}"
    fi
    # Use file-based counter because call_rpc runs in $(...) subshells
    local rid
    rid=$(python3 -c "import sys; f=open('$COUNTER_FILE','r+'); n=int(f.read())+1; f.seek(0); f.write(str(n)); f.truncate(); print(n)")

    # Send the request: use python with method/params passed via argv, not string interpolation
    local req_json
    req_json=$(python3 - "$method" "$params" "$rid" << 'PYEOF'
import sys, json
method = sys.argv[1]
params = sys.argv[2]
rid = int(sys.argv[3])
req = {"jsonrpc": "2.0", "id": rid, "method": method, "params": json.loads(params)}
print(json.dumps(req))
PYEOF
)
    echo "$req_json" > "$FIFO_IN"

    # Poll for response (up to 8s — phonefast can be slow)
    for i in $(seq 1 80); do
        local resp
        # Find the JSON line with matching id from RPC_LOG
        resp=$(grep '"id":'$rid'[,}]' "$RPC_LOG" 2>/dev/null | tail -1)
        if [[ -n "$resp" ]]; then
            echo "$resp"
            return 0
        fi
        sleep 0.1
    done
    echo '{"jsonrpc":"2.0","id":'$rid',"error":{"code":-1,"message":"timeout"}}'
}

tool_text() {
    local tool_name="$1"
    local tool_args
    if [[ -n "${2:-}" ]]; then
        tool_args="$2"
    else
        tool_args="{}"
    fi
    local resp
    # Build params JSON safely in Python, not bash string interpolation
    local params_json
    params_json=$(python3 - "$tool_name" "$tool_args" << 'PYEOF'
import sys, json
name = sys.argv[1]
args = sys.argv[2]
params = {"name": name, "arguments": json.loads(args)}
print(json.dumps(params))
PYEOF
)
    resp=$(call_rpc "tools/call" "$params_json")
    echo "$resp" | python3 -c "
import sys,json
r=json.loads(sys.stdin.read())
c=r.get('result',{}).get('content',[])
print(c[0].get('text','') if c else '')
" 2>/dev/null
}

# ══════════════════════════════════════════════════════════════════════════
# T1: initialize
# ══════════════════════════════════════════════════════════════════════════
echo ""
echo "── T1: MCP Initialize ──"
INIT_RESP=$(call_rpc "initialize" '{"protocolVersion":"2024-11-05","clientInfo":{"name":"test","version":"1"}}')

PROTO=$(echo "$INIT_RESP" | python3 -c "
import sys,json
print(json.loads(sys.stdin.read()).get('result',{}).get('protocolVersion','NONE'))
" 2>/dev/null)
[[ "$PROTO" == "2024-11-05" ]] && ok "protocolVersion = 2024-11-05" || bad "protocolVersion" "$PROTO"

NAME=$(echo "$INIT_RESP" | python3 -c "
import sys,json
print(json.loads(sys.stdin.read()).get('result',{}).get('serverInfo',{}).get('name','NONE'))
" 2>/dev/null)
[[ "$NAME" == "phonefast" ]] && ok "serverInfo.name = phonefast" || bad "serverInfo.name" "$NAME"

CAPS=$(echo "$INIT_RESP" | python3 -c "
import sys,json
caps=json.loads(sys.stdin.read()).get('result',{}).get('capabilities',{})
print('yes' if 'tools' in caps else 'no')
" 2>/dev/null)
[[ "$CAPS" == "yes" ]] && ok "capabilities.tools declared" || bad "capabilities.tools" "not declared"

# ══════════════════════════════════════════════════════════════════════════
# T2: tools/list
# ══════════════════════════════════════════════════════════════════════════
echo ""
echo "── T2: tools/list ──"
TOOLS_RESP=$(call_rpc "tools/list")
echo "     raw: ${TOOLS_RESP:0:200}"
TOOL_COUNT=$(echo "$TOOLS_RESP" | python3 -c "
import sys,json
tools=json.loads(sys.stdin.read()).get('result',{}).get('tools',[])
print(len(tools))
" 2>/dev/null)
TOOL_NAMES=$(echo "$TOOLS_RESP" | python3 -c "
import sys,json
tools=json.loads(sys.stdin.read()).get('result',{}).get('tools',[])
print(','.join(sorted(t['name'] for t in tools)))
" 2>/dev/null)

echo "     count: $TOOL_COUNT"
echo "     tools: $TOOL_NAMES"

EXPECTED="back,get_ui_elements,home,launch_app,list_devices,observe,press_key,screenshot,swipe,tap,tap_element,type_text,wait"
[[ "$TOOL_COUNT" -ge 13 ]] && ok "tools/list returns $TOOL_COUNT tools" || bad "tools/list" "$TOOL_COUNT tools"
[[ "$TOOL_NAMES" == "$EXPECTED" ]] && ok "all 13 expected tools" || bad "tool mismatch"

# ══════════════════════════════════════════════════════════════════════════
# T3: ping
# ══════════════════════════════════════════════════════════════════════════
echo ""
echo "── T3: ping ──"
PING_RESP=$(call_rpc "ping")
PING_OK=$(echo "$PING_RESP" | python3 -c "
import sys,json
r=json.loads(sys.stdin.read())
print('ok' if r.get('result')=={} else 'fail')
" 2>/dev/null)
[[ "$PING_OK" == "ok" ]] && ok "ping → {}" || bad "ping" "$PING_RESP"

# ══════════════════════════════════════════════════════════════════════════
# T4-T15: tools/call
# ══════════════════════════════════════════════════════════════════════════

echo ""
echo "Waiting for device connection in background (~6s)..."
sleep 6  # 设备连接在后台 goroutine 中，需等待完成

echo ""
echo "── T4: tools/call — list_devices ──"
TEXT=$(tool_text "list_devices")
DEV_COUNT=$(echo "$TEXT" | python3 -c "import sys,json; print(len(json.loads(sys.stdin.read())))" 2>/dev/null)
[[ "$DEV_COUNT" -gt 0 ]] && ok "list_devices ($DEV_COUNT device(s))" || bad "list_devices" "$TEXT"

echo ""
echo "── T5: tools/call — back ──"
TEXT=$(tool_text "back")
[[ "$TEXT" == "Back pressed" ]] && ok "back" || bad "back" "$TEXT"
sleep 0.3

echo ""
echo "── T6: tools/call — home ──"
TEXT=$(tool_text "home")
[[ "$TEXT" == "Home pressed" ]] && ok "home" || bad "home" "$TEXT"
sleep 0.3

echo ""
echo "── T7: tools/call — type_text ──"
tool_text "launch_app" '{"package":"com.android.settings"}' > /dev/null
sleep 1.5
tool_text "tap" '{"x":540,"y":200}' > /dev/null
sleep 0.5
TEXT=$(tool_text "type_text" '{"text":"hello_test"}')
[[ "$TEXT" == *"Typed"* && "$TEXT" == *"hello_test"* ]] && ok "type_text 'hello_test'" || bad "type_text" "$TEXT"

echo ""
echo "── T8: tools/call — wait ──"
T0=$(python3 -c "import time; print(time.time())")
tool_text "wait" '{"duration_ms":500}' > /dev/null
T1=$(python3 -c "import time; print(time.time())")
WAIT_MS=$(python3 -c "print(int(($T1 - $T0) * 1000))")
echo "     elapsed: ${WAIT_MS}ms"
[[ "$WAIT_MS" -ge 400 ]] && ok "wait 500ms (actual ${WAIT_MS}ms)" || bad "wait" "${WAIT_MS}ms"

echo ""
echo "── T9: tools/call — tap ──"
tool_text "home" > /dev/null
sleep 0.3
TEXT=$(tool_text "tap" '{"x":540,"y":1000}')
[[ "$TEXT" == *"Tapped"* ]] && ok "tap (540,1000)" || bad "tap" "$TEXT"

echo ""
echo "── T10: tools/call — swipe ──"
TEXT=$(tool_text "swipe" '{"start_x":540,"start_y":1200,"end_x":540,"end_y":400,"duration_ms":300}')
[[ "$TEXT" == *"Swiped"* ]] && ok "swipe up" || bad "swipe" "$TEXT"

echo ""
echo "── T11: tools/call — get_ui_elements ──"
tool_text "home" > /dev/null
sleep 0.5
TEXT=$(tool_text "get_ui_elements")
UI_COUNT=$(echo "$TEXT" | python3 -c "
import sys
lines=[l for l in sys.stdin.read().split('\n') if l.strip().startswith('[')]
print(len(lines))
" 2>/dev/null)
echo "     elements: $UI_COUNT"
[[ "$UI_COUNT" -gt 0 ]] && ok "get_ui_elements ($UI_COUNT interactive)" || bad "get_ui_elements" "0 elements"

echo ""
echo "── T12: tools/call — tap_element ──"
TEXT=$(tool_text "tap_element" '{"index":0}')
[[ "$TEXT" == *"Tapped"* ]] && ok "tap_element index=0" || bad "tap_element" "${TEXT:0:80}"

echo ""
echo "── T13: tools/call — press_key ──"
TEXT=$(tool_text "press_key" '{"keycode":4}')
[[ "$TEXT" == *"Key"* ]] && ok "press_key keycode=4" || bad "press_key" "$TEXT"

echo ""
echo "── T14: tools/call — observe ──"
tool_text "home" > /dev/null
sleep 0.5
TEXT=$(tool_text "observe")
OBS_EC=$(echo "$TEXT" | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(d.get('element_count',0))" 2>/dev/null)
OBS_B64=$(echo "$TEXT" | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(len(d.get('screenshot_base64','')))" 2>/dev/null)
echo "     elements=$OBS_EC  screenshot=${OBS_B64} chars"
[[ "$OBS_B64" -gt 1000 ]] && ok "observe (screenshot+UI)" || bad "observe" "b64=$OBS_B64"

echo ""
echo "── T15: tools/call — launch_app ──"
TEXT=$(tool_text "launch_app" '{"package":"com.android.settings"}')
[[ "$TEXT" == *"Launched"* ]] && ok "launch_app settings" || bad "launch_app" "$TEXT"

echo ""
echo "── T16: tools/call — screenshot ──"
TEXT=$(tool_text "screenshot")
SS_B64=$(echo "$TEXT" | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(len(d.get('base64','')))" 2>/dev/null)
SS_W=$(echo "$TEXT" | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(d.get('width','?'))" 2>/dev/null)
SS_H=$(echo "$TEXT" | python3 -c "import sys,json; d=json.loads(sys.stdin.read()); print(d.get('height','?'))" 2>/dev/null)
echo "     ${SS_W}x${SS_H}  base64=${SS_B64} chars"
[[ "$SS_B64" -gt 1000 ]] && ok "screenshot (${SS_W}x${SS_H})" || bad "screenshot" "$SS_B64 chars"

# ══════════════════════════════════════════════════════════════════════════
# Error handling
# ══════════════════════════════════════════════════════════════════════════
echo ""
echo "── T17: Error handling ──"

ERR1=$(call_rpc "nonexistent_method")
ERR1_CODE=$(echo "$ERR1" | python3 -c "import sys,json; print(json.loads(sys.stdin.read()).get('error',{}).get('code','NONE'))" 2>/dev/null)
[[ "$ERR1_CODE" == "-32601" ]] && ok "unknown method → -32601" || bad "unknown method" "code=$ERR1_CODE"

# mcp-go returns JSON-RPC error (-32602) for unknown tools — check raw response
UNKNOWN_RESP=$(call_rpc "tools/call" '{"name":"no_such_tool","arguments":{}}')
[[ "$UNKNOWN_RESP" == *"not found"* || "$UNKNOWN_RESP" == *"-32602"* ]] && ok "unknown tool → error" || bad "unknown tool" "${UNKNOWN_RESP:0:80}"

# mcp-go validates required params — returns validation error
MISSING_RESP=$(call_rpc "tools/call" '{"name":"tap","arguments":{"x":100}}')
[[ "$MISSING_RESP" == *"validation"* || "$MISSING_RESP" == *"-32602"* ]] && ok "missing param → error" || bad "missing param" "${MISSING_RESP:0:80}"

echo ""
echo "── T18: Invalid JSON (parse error) ──"
echo "not-valid-json" > "$FIFO_IN"
sleep 0.5
PARSE_ERR=$(tail -1 "$RPC_LOG" | python3 -c "
import sys,json
r=json.loads(sys.stdin.read())
print(r.get('error',{}).get('code','NONE'))
" 2>/dev/null)
[[ "$PARSE_ERR" == "-32700" ]] && ok "parse error → -32700" || bad "parse error" "code=$PARSE_ERR"

# ══════════════════════════════════════════════════════════════════════════
# Summary
# ══════════════════════════════════════════════════════════════════════════
echo ""
echo "============================================================"
echo "  MCP Protocol Test Results"
echo "============================================================"
TOTAL=$((PASS + FAIL))
echo "  PASS:  $PASS/$TOTAL"
echo "  FAIL:  $FAIL/$TOTAL"

if [[ -s "$LOG_FILE" ]]; then
    echo ""
    echo "  Server log (last 5 lines):"
    tail -5 "$LOG_FILE" | while IFS= read -r l; do echo "    $l"; done
fi

if [[ $FAIL -gt 0 ]]; then
    echo ""
    echo "  ⚠  $FAIL check(s) failed"
    exit 1
else
    echo ""
    echo "  ✅ All $PASS checks passed!"
    exit 0
fi
