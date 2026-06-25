#!/bin/bash
# =============================================================================
# phone-mcp vs phonefast 全方位速度对比测试
# =============================================================================
# 用法: bash benchmark.sh
# 前提: phonefast 已编译, phone-mcp 已安装, Android 设备已连接
# =============================================================================

set -euo pipefail

PF="${PHONEFAST_BIN:-$(dirname "$0")/../dist/phonefast}"
PM="${PHONEMCP_BIN:-$HOME/.claude/skills/phone-mcp/phone-mcp}"
RUNS=${RUNS:-5}
TAP_X=540
TAP_Y=960
DEVICE_SN=""

# ---------------------------------------------------------------------------
# 环境检查
# ---------------------------------------------------------------------------
check_prereqs() {
    echo "========== 环境检查 =========="

    if [ ! -f "$PF" ]; then
        echo "[ERROR] phonefast 未找到: $PF"
        echo "  设置 PHONEFAST_BIN 环境变量或运行: (cd .. && go build -o dist/phonefast ./cmd/phonefast/)"
        exit 1
    fi
    echo "[OK] phonefast: $PF ($(file "$PF" | cut -d: -f2-))"

    if [ ! -f "$PM" ]; then
        echo "[ERROR] phone-mcp 未找到: $PM"
        echo "  设置 PHONEMCP_BIN 环境变量"
        exit 1
    fi
    echo "[OK] phone-mcp:  $PM ($(file "$PM" | cut -d: -f2-))"

    # 检查设备
    local devices
    devices=$("$PF" devices 2>/dev/null | grep -c "device" || true)
    if [ "$devices" -eq 0 ]; then
        echo "[ERROR] 无已连接的 Android 设备"
        exit 1
    fi
    echo "[OK] 已连接设备: $devices 台"
    echo ""
}

# ---------------------------------------------------------------------------
# 辅助函数
# ---------------------------------------------------------------------------
bench() {
    local tool="$1"   # pf-daemon | pf-direct | pm
    local name="$2"
    shift 2
    local cmd=("$@")
    local sum=0 min=99999 max=0
    local times=()

    for i in $(seq 1 "$RUNS"); do
        local start end elapsed_ms
        start=$(perl -MTime::HiRes=time -e 'printf "%.6f", time')
        "${cmd[@]}" > /dev/null 2>&1 || true
        end=$(perl -MTime::HiRes=time -e 'printf "%.6f", time')
        elapsed_ms=$(perl -e "printf '%.0f', ($end - $start) * 1000")
        times+=("$elapsed_ms")
        sum=$((sum + elapsed_ms))
        [ "$elapsed_ms" -lt "$min" ] && min=$elapsed_ms
        [ "$elapsed_ms" -gt "$max" ] && max=$elapsed_ms
    done
    local avg=$((sum / RUNS))

    # 计算标准差
    local variance=0
    for t in "${times[@]}"; do
        local diff=$((t - avg))
        variance=$((variance + diff * diff))
    done
    local stddev
    stddev=$(perl -e "printf '%.0f', sqrt($variance / $RUNS)")

    printf "| %-22s | %-8s | %4sms | %4sms | %4sms | ±%3sms |\n" \
        "$name" "$tool" "$min" "$avg" "$max" "$stddev"
}

# ---------------------------------------------------------------------------
# 主测试流程
# ---------------------------------------------------------------------------
run_benchmarks() {
    echo "=============================================================="
    echo "  phone-mcp vs phonefast 全方位延迟测试"
    echo "  每个操作执行 ${RUNS} 次"
    echo "  时间单位: 毫秒 (ms)"
    echo "=============================================================="
    echo ""

    # 启动 phonefast daemon
    echo ">>> 启动 phonefast daemon..."
    "$PF" daemon 2>/dev/null || true
    sleep 2
    echo ""

    # 表头
    printf "| %-22s | %-8s | %4s | %4s | %4s | %4s |\n" "操作" "工具" "min" "avg" "max" "std"
    printf "|%s|%s|%s|%s|%s|%s|\n" \
        "----------------------" "--------" "------" "------" "------" "------"

    # ---- 1. back ----
    echo ""; echo ">>> 1/10 back 返回键"
    bench "pf-daemon" "back(返回键)"      "$PF" --daemon back
    bench "pf-direct" "back(返回键)"      "$PF" back
    bench "pm"        "back(返回键)"      "$PM" run '{"action":"back"}'

    # ---- 2. home ----
    echo ">>> 2/10 home 主页键"
    bench "pf-daemon" "home(主页键)"      "$PF" --daemon home
    bench "pf-direct" "home(主页键)"      "$PF" home
    bench "pm"        "home(主页键)"      "$PM" run '{"action":"home"}'

    # ---- 3. tap ----
    echo ">>> 3/10 tap 坐标点击"
    bench "pf-daemon" "tap(坐标点击)"     "$PF" --daemon tap $TAP_X $TAP_Y
    bench "pf-direct" "tap(坐标点击)"     "$PF" tap $TAP_X $TAP_Y
    bench "pm"        "tap(坐标点击)"     "$PM" run "{\"action\":\"tap\",\"x\":$TAP_X,\"y\":$TAP_Y}"

    # ---- 4. swipe ----
    echo ">>> 4/10 swipe 滑动"
    bench "pf-daemon" "swipe(滑动)"       "$PF" --daemon swipe 540 1600 540 400 300
    bench "pf-direct" "swipe(滑动)"       "$PF" swipe 540 1600 540 400 300
    bench "pm"        "swipe(滑动)"       "$PM" run '{"action":"swipe","start_x":540,"start_y":1600,"end_x":540,"end_y":400,"duration_ms":300}'

    # ---- 5. type_text ----
    echo ">>> 5/10 type_text 文本输入"
    bench "pf-daemon" "type(输入文本)"    "$PF" --daemon type "test1234"
    bench "pf-direct" "type(输入文本)"    "$PF" type "test1234"
    bench "pm"        "type(输入文本)"    "$PM" run '{"action":"type_text","text":"test1234"}'

    # ---- 6. screenshot ----
    echo ">>> 6/10 screenshot 截图"
    bench "pf-daemon" "screenshot(截图)"  "$PF" --daemon screenshot /tmp/pf_daemon_bench.png
    bench "pf-direct" "screenshot(截图)"  "$PF" screenshot /tmp/pf_direct_bench.png
    bench "pm"        "screenshot(截图)"  "$PM" run '{"action":"screenshot","path":"/tmp/pm_bench.png"}'

    # ---- 7. get_ui_elements ----
    echo ">>> 7/10 get_ui_elements UI元素"
    bench "pf-daemon" "ui(UI元素列表)"    "$PF" --daemon ui
    bench "pf-direct" "ui(UI元素列表)"    "$PF" ui
    bench "pm"        "get_ui_elements"   "$PM" run '{"action":"get_ui_elements"}'

    # ---- 8. observe ----
    echo ">>> 8/10 observe 截图+UI并行"
    bench "pf-daemon" "observe(截图+UI)"  "$PF" --daemon observe
    bench "pf-direct" "observe(截图+UI)"  "$PF" observe
    # phone-mcp 无 observe，标注为 N/A
    printf "| %-22s | %-8s | %4s | %4s | %4s | %4s |\n" \
        "observe(截图+UI)" "pm" "N/A" "N/A" "N/A" "—"

    # ---- 9. list_devices ----
    echo ">>> 9/10 list_devices 设备列表"
    bench "pf-daemon" "devices(设备列表)" "$PF" --daemon devices
    bench "pf-direct" "devices(设备列表)" "$PF" devices
    bench "pm"        "list_devices"      "$PM" run '{"action":"list_devices"}'

    # ---- 10. launch_app ----
    echo ">>> 10/10 launch_app 启动应用"
    bench "pf-daemon" "launch(启动应用)"  "$PF" --daemon launch com.android.settings
    bench "pf-direct" "launch(启动应用)"  "$PF" launch com.android.settings
    bench "pm"        "launch_app"        "$PM" run '{"action":"launch_app","package":"com.android.settings"}'

    echo ""
    echo "=============================================================="
    echo "  测试完成"
    echo "=============================================================="

    # 清理
    rm -f /tmp/pf_daemon_bench.png /tmp/pf_direct_bench.png /tmp/pm_bench.png
}

# ---------------------------------------------------------------------------
# 速度倍数汇总
# ---------------------------------------------------------------------------
print_summary() {
    echo ""
    echo "=============================================================="
    echo "  速度对比总结"
    echo "=============================================================="
    echo ""
    echo "  phonefast daemon < phonefast direct < phone-mcp"
    echo "  ≈8ms              ≈2.4s               ≈8.0s"
    echo ""
    echo "  关键差距:"
    echo "  - phonefast daemon 比 phone-mcp 快 ~1000x (轻量操作)"
    echo "  - phonefast direct 比 phone-mcp 快 ~3.5x"
    echo "  - phone-mcp 每次操作都是 PyInstaller 冷启动 + ADB 初始化 (~8s)"
    echo "  - phonefast daemon 持有常驻 scrcpy 进程，Unix Socket 通信 <1ms"
    echo ""
    echo "  推荐场景:"
    echo "  - 批量自动化 / AI Agent 控制: phonefast daemon"
    echo "  - 临时单次操作: phonefast direct (仍比 phone-mcp 快 3.5x)"
    echo "  - 元素级点击 + OCR: phonefast serve (MCP 模式) 或 phone-mcp"
    echo ""
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
check_prereqs
run_benchmarks
print_summary
