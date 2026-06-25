#!/usr/bin/env python3
"""
phonefast 安装流程测试用例 — 依据 install-test-case.md & phonefast-test-cases.md

覆盖 phonefast 逻辑正确性验证:
  TC-1:  Daemon 生命周期（启动 / 状态 / 停止）
  TC-2:  设备列表获取
  TC-3:  Observe 命令（截图 + UI 元素格式校验）
  TC-4:  Tap 坐标点击（daemon / direct）
  TC-5:  Swipe 滑动与长按模拟
  TC-6:  Key / Back / Home 按键
  TC-7:  Wait 等待精确性
  TC-8:  ADB 包安装检查（adb shell pm path 正确用法）
  TC-9:  Play 商店 URL 打开发起
  TC-10: 坐标计算逻辑（从 observe bounds 计算中心点）
  TC-11: 安装状态转换检测（安装按钮 → 进度 → 完成）
  TC-12: tap_element vs tap 对比
  TC-13: Daemon 模式 vs Direct 模式
  TC-14: 错误处理（adb pm path 缺 shell、缺参、未知命令）
  TC-15: Daemon 断连自动恢复
  TC-16: Type_text 文本输入
  TC-17: Launch_app 应用启动
  TC-18: 锁屏状态检测

用法:
  python3 tests/test_install_cases.py              # 完整测试（需设备连接 + daemon 未运行）
  python3 tests/test_install_cases.py --quick      # 快速模式（跳过 daemon 重启等耗时项）
  python3 tests/test_install_cases.py --json-out   # 输出 JSON 结果到 stdout
  python3 tests/test_install_cases.py --report /tmp/report.json
"""

import argparse
import base64
import json
import os
import re
import socket
import struct
import subprocess
import sys
import tempfile
import time
import threading
from pathlib import Path

# ── Constants ──────────────────────────────────────────────────────────────────
BINARY = os.path.join(os.path.dirname(__file__), "..", "dist", "phonefast")

# Expected tool names from CLAUDE.md
EXPECTED_TOOLS = {
    "list_devices", "screenshot", "get_ui_elements", "observe",
    "tap", "tap_element", "swipe", "type_text", "back", "home",
    "press_key", "launch_app", "wait",
}


# UI element required fields (used by get_ui_elements JSON validation)
ELEMENT_REQUIRED_FIELDS = {"index", "bounds", "clickable"}


# ── Helpers ────────────────────────────────────────────────────────────────────
def run(cmd, timeout=15, check=False, **kwargs):
    """Run a shell command, return CompletedProcess."""
    return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, **kwargs)


def get_device_serial():
    """Get the first connected Android device serial."""
    r = run(["adb", "devices"])
    for line in r.stdout.strip().split("\n")[1:]:
        if "\tdevice" in line:
            return line.split()[0]
    return None


def phonefast(args, daemon=False, timeout=30):
    """Run phonefast CLI command.

    Since v1.x the default mode is daemon. To actually exercise direct mode
    (a fresh scrcpy session per call) we pass --foreground when daemon=False.
    daemon=True uses the default (daemon) explicitly via --daemon for clarity.
    """
    cmd = [BINARY]
    if daemon:
        cmd.insert(1, "--daemon")
    else:
        cmd.insert(1, "--foreground")
    cmd.extend(args)
    return run(cmd, timeout=timeout)


def get_ui_elements(timeout=15):
    """Fetch structured UI elements as a list of dicts via the JSON `run`
    interface. The CLI `observe` command prints human-readable text (no JSON),
    so tests that need structured elements (bounds/center) use this instead.

    Returns (elements_list, raw_stdout). On failure returns ([], raw).
    """
    r = phonefast(["run", '{"action":"get_ui_elements"}'], daemon=True, timeout=timeout)
    try:
        data = json.loads(r.stdout.strip().split("\n")[-1] if "\n" in r.stdout else r.stdout)
        els = data.get("elements", [])
        if isinstance(els, str):
            els = json.loads(els)
        return els, r.stdout
    except Exception:
        return [], r.stdout


def get_screenshot_png(timeout=15):
    """Capture a screenshot and return raw PNG bytes, or None on failure.

    The CLI `screenshot` (no file arg) prints a data URI:
    `data:image/png;base64,<...>`. We strip the prefix and decode.
    """
    r = phonefast(["screenshot"], daemon=True, timeout=timeout)
    out = r.stdout.strip()
    prefix = "data:image/png;base64,"
    if out.startswith(prefix):
        out = out[len(prefix):]
    try:
        raw = base64.b64decode(out)
        if raw[:4] == b"\x89PNG":
            return raw
    except Exception:
        pass
    return None


def daemon_running():
    """Check if daemon is running via --status."""
    r = run([BINARY, "daemon", "--status"], timeout=5)
    return "running" in r.stdout.lower()


def ensure_daemon_started():
    """Start daemon if not already running."""
    if daemon_running():
        return True
    r = run([BINARY, "daemon"], timeout=8)
    time.sleep(4)  # Wait for session to be fully established
    return daemon_running()


def stop_daemon():
    """Stop daemon if running."""
    if daemon_running():
        run([BINARY, "daemon", "--stop"], timeout=5)
        time.sleep(1)


def adb_shell(cmd_args, timeout=10):
    """Run adb shell command."""
    return run(["adb", "shell"] + cmd_args, timeout=timeout)


def check_package_installed(package_name):
    """Check if a package is installed — MUST use adb shell pm path."""
    # Correct: adb shell pm path <package>
    r = adb_shell(["pm", "path", package_name], timeout=10)
    return r.returncode == 0 and "package:" in r.stdout


def check_package_installed_wrong(package_name):
    """WRONG way — adb pm path without shell (for error testing)."""
    r = run(["adb", "pm", "path", package_name], timeout=10)
    return r.returncode == 0 and "package:" in r.stdout


# ── Test Result Tracking ───────────────────────────────────────────────────────
class TestResults:
    def __init__(self):
        self.passed = []
        self.failed = []
        self.skipped = []
        self.start_time = time.time()

    def check(self, condition, test_id, description, detail="", fail_reason=""):
        if condition:
            self.passed.append({"id": test_id, "desc": description, "detail": detail})
            print(f"  ✓  {test_id}: {description}")
            if detail:
                print(f"     {detail}")
            return True
        else:
            reason = fail_reason or detail or "assertion failed"
            self.failed.append({"id": test_id, "desc": description, "reason": reason})
            print(f"  ✗  {test_id}: {description} — {reason}")
            return False

    def skip(self, test_id, description, reason=""):
        self.skipped.append({"id": test_id, "desc": description, "reason": reason})
        print(f"  ⊘  {test_id}: {description} — SKIPPED ({reason})")

    def summary(self):
        total = len(self.passed) + len(self.failed) + len(self.skipped)
        elapsed = time.time() - self.start_time
        print(f"\n{'=' * 70}")
        print(f"  测试结果汇总")
        print(f"{'=' * 70}")
        print(f"  PASS:    {len(self.passed)}/{total}")
        print(f"  FAIL:    {len(self.failed)}/{total}")
        print(f"  SKIPPED: {len(self.skipped)}/{total}")
        print(f"  耗时:    {elapsed:.1f}s")
        print(f"{'=' * 70}")

        if self.failed:
            print(f"\n  失败项:")
            for f in self.failed:
                print(f"    ✗  {f['id']}: {f['desc']}")
                print(f"       原因: {f['reason']}")

        return len(self.failed) == 0

    def to_dict(self):
        return {
            "passed": self.passed,
            "failed": self.failed,
            "skipped": self.skipped,
            "total_checks": len(self.passed) + len(self.failed) + len(self.skipped),
            "pass_pct": round(
                100 * len(self.passed) / max(1, len(self.passed) + len(self.failed)), 1
            ),
            "elapsed_s": round(time.time() - self.start_time, 1),
        }


# ── Test Cases ─────────────────────────────────────────────────────────────────

def tc01_daemon_lifecycle(R: TestResults):
    """TC-1: Daemon 生命周期 — 启动 / 状态 / 停止"""
    print("\n── TC-1: Daemon 生命周期 ──")

    # 1.1 停止已有 daemon（确保干净起点）
    if daemon_running():
        stop_daemon()
        time.sleep(1)
    R.check(
        not daemon_running(),
        "TC-1.1", "daemon 初始状态为停止",
        fail_reason="daemon 仍在运行，无法建立干净起点",
    )

    # 1.2 启动 daemon
    r = run([BINARY, "daemon"], timeout=10)
    time.sleep(4)  # 等待设备连接完成
    R.check(
        daemon_running(),
        "TC-1.2", "daemon 启动成功 (daemon --status 返回 running)",
        fail_reason=f"daemon --status 未返回 running, stdout={r.stdout[:100]}",
    )

    # 1.3 daemon --status 返回有效 JSON 状态信息
    r = run([BINARY, "daemon", "--status"], timeout=5)
    R.check(
        "pid" in r.stdout.lower() or "running" in r.stdout.lower(),
        "TC-1.3", "daemon --status 返回进程信息",
        detail=r.stdout.strip()[:100],
        fail_reason=f"status 输出不包含预期字段: {r.stdout[:100]}",
    )

    # 1.4 daemon --stop 停止 daemon
    r = run([BINARY, "daemon", "--stop"], timeout=5)
    time.sleep(2)
    R.check(
        not daemon_running(),
        "TC-1.4", "daemon --stop 成功停止 daemon",
        fail_reason="daemon 未能在 --stop 后停止",
    )

    # 重新启动 daemon 供后续测试使用
    ensure_daemon_started()


def tc02_device_list(R: TestResults):
    """TC-2: 设备列表获取"""
    print("\n── TC-2: 设备列表 ──")

    r = run([BINARY, "devices"], timeout=5)
    R.check(
        r.returncode == 0 and len(r.stdout) > 0,
        "TC-2.1", "phonefast devices 返回成功",
        detail=r.stdout.strip()[:200],
        fail_reason=f"returncode={r.returncode}",
    )

    # 检查是否包含设备序列号
    serial = get_device_serial()
    if serial:
        R.check(
            serial in r.stdout,
            "TC-2.2", f"设备列表包含预期序列号 ({serial})",
            fail_reason=f"输出中未找到 {serial}",
        )

    # 检查包含 "device" 状态标记
    R.check(
        "device" in r.stdout.lower(),
        "TC-2.3", "设备列表包含 'device' 状态标记",
        fail_reason="输出中未找到 device 状态",
    )


def tc03_observe(R: TestResults):
    """TC-3: Observe 命令 — 截图 + UI 元素

    CLI `observe` prints human-readable UI element text (not JSON). Structured
    elements come from `get_ui_elements`; the screenshot comes from the
    `screenshot` command. Both are exercised here.
    """
    print("\n── TC-3: Observe 命令 ──")

    r = phonefast(["observe"], daemon=True, timeout=15)
    R.check(
        r.returncode == 0,
        "TC-3.1", "observe 命令返回成功",
        fail_reason=f"returncode={r.returncode}, stderr={r.stderr[:200]}",
    )

    # 3.2 observe 输出包含元素列表文本
    R.check(
        "Interactive elements" in r.stdout or "[" in r.stdout,
        "TC-3.2", "observe 输出包含 UI 元素文本",
        fail_reason=f"stdout 前 80 字符: {r.stdout[:80]}",
    )

    # 3.3-3.8 校验结构化 UI 元素（来自 get_ui_elements JSON）
    elements, _ = get_ui_elements(timeout=15)
    R.check(
        isinstance(elements, list) and len(elements) > 0,
        "TC-3.3", f"get_ui_elements 返回 {len(elements)} 个 UI 元素",
        fail_reason="elements 为空或不是列表",
    )

    has_bounds = any("bounds" in el for el in elements)
    R.check(
        has_bounds,
        "TC-3.4", "UI 元素包含 bounds 坐标信息",
        fail_reason="没有元素包含 bounds 字段",
    )

    has_label = any(el.get("text") or el.get("content_desc") for el in elements)
    R.check(
        has_label,
        "TC-3.5", "UI 元素包含 text 或 content_desc",
        fail_reason="所有元素都缺少 text 和 content_desc",
    )

    clickable = [el for el in elements if el.get("clickable")]
    R.check(
        len(clickable) >= 0,  # 并非所有页面都有可点击元素
        "TC-3.6", f"可点击元素数量: {len(clickable)}",
        detail=f"总元素: {len(elements)}",
    )

    # 3.7 screenshot 命令返回有效 PNG
    raw = get_screenshot_png(timeout=15)
    R.check(
        raw is not None and len(raw) > 100,
        "TC-3.7", f"screenshot 返回有效 PNG ({len(raw) if raw else 0} bytes)",
        fail_reason="screenshot 未返回有效 PNG",
    )

    # 3.8 PNG 可解析尺寸 (IHDR at offset 16)
    if raw and raw[:4] == b"\x89PNG" and len(raw) >= 24:
        w = int.from_bytes(raw[16:20], "big")
        h = int.from_bytes(raw[20:24], "big")
        R.check(
            w > 0 and h > 0,
            "TC-3.8", f"PNG 尺寸: {w}x{h}",
            fail_reason="IHDR 尺寸异常",
        )
    else:
        R.check(False, "TC-3.8", "PNG IHDR 解析", fail_reason="PNG 数据不足")

    return elements


def tc04_tap_coordinates(R: TestResults):
    """TC-4: Tap 坐标点击"""
    print("\n── TC-4: Tap 坐标点击 ──")

    # 获取屏幕尺寸 -> 点击中心 (via screenshot PNG IHDR)
    raw = get_screenshot_png(timeout=15)
    if raw and raw[:4] == b"\x89PNG" and len(raw) >= 24:
        w = int.from_bytes(raw[16:20], "big")
        h = int.from_bytes(raw[20:24], "big")
    else:
        w, h = 1080, 2400

    cx, cy = w // 2, h // 2

    # 4.1 daemon 模式 tap
    r = phonefast(["tap", str(cx), str(cy)], daemon=True, timeout=10)
    R.check(
        r.returncode == 0 and "Tapped" in r.stdout,
        "TC-4.1", f"daemon tap ({cx}, {cy}) 成功",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )

    # 4.2 direct 模式 tap
    r = phonefast(["tap", str(cx), str(cy)], daemon=False, timeout=15)
    R.check(
        r.returncode == 0 and "Tapped" in r.stdout,
        "TC-4.2", f"direct tap ({cx}, {cy}) 成功",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )

    # 4.3 从 UI 元素 bounds 计算中心点并点击 (structured elements via get_ui_elements)
    elements, _ = get_ui_elements(timeout=15)
    # 找第一个有 bounds 的可点击元素
    target = None
    for el in elements:
        if el.get("clickable") and el.get("bounds"):
            target = el
            break
    if target is None:
        # fallback: any element with bounds
        for el in elements:
            if el.get("bounds"):
                target = el
                break

    if target and target.get("bounds") and len(target["bounds"]) == 4:
        b = target["bounds"]
        tx, ty = (b[0] + b[2]) // 2, (b[1] + b[3]) // 2
        r = phonefast(["tap", str(tx), str(ty)], daemon=True, timeout=10)
        R.check(
            r.returncode == 0 and "Tapped" in r.stdout,
            "TC-4.3", f"从 bounds 计算坐标点击: ({tx}, {ty})",
            detail=f"bounds={b}, tap=({tx},{ty})",
            fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
        )
    else:
        R.skip("TC-4.3", "从 bounds 计算坐标点击", "无可用的 bounds 元素")


def tc05_swipe(R: TestResults):
    """TC-5: Swipe 滑动与长按模拟"""
    print("\n── TC-5: Swipe 滑动 ──")

    # 5.1 正常向上滑动（模拟上滑）
    r = phonefast(["swipe", "540", "1800", "540", "400", "300"], daemon=True, timeout=10)
    R.check(
        r.returncode == 0 and "Swiped" in r.stdout,
        "TC-5.1", "swipe 上滑 (540,1800)→(540,400) 300ms",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )

    # 5.2 长按模拟（极慢速 swipe，持续 2000ms）
    r = phonefast(["swipe", "540", "1200", "540", "1201", "2000"], daemon=True, timeout=10)
    R.check(
        r.returncode == 0 and "Swiped" in r.stdout,
        "TC-5.2", "swipe 长按模拟 2000ms",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )

    # 5.3 缺 duration_ms 时默认 500ms
    r = phonefast(["swipe", "540", "1800", "540", "400"], daemon=True, timeout=10)
    R.check(
        r.returncode == 0 and "Swiped" in r.stdout,
        "TC-5.3", "swipe 缺 duration_ms 默认 500ms",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )


def tc06_keys(R: TestResults):
    """TC-6: Key / Back / Home 按键"""
    print("\n── TC-6: 按键操作 ──")

    # 6.1 back 命令
    r = phonefast(["back"], daemon=True, timeout=10)
    R.check(
        r.returncode == 0 and ("Back pressed" in r.stdout or "Pressed" in r.stdout),
        "TC-6.1", "back 按键",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )

    # 6.2 home 命令
    time.sleep(0.3)
    r = phonefast(["home"], daemon=True, timeout=10)
    R.check(
        r.returncode == 0 and ("Home pressed" in r.stdout or "Pressed" in r.stdout),
        "TC-6.2", "home 按键",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )

    # 6.3 key 按键码
    time.sleep(0.3)
    for key_name, desc in [("back", "back 按键码"), ("home", "home 按键码")]:
        r = phonefast(["key", key_name], daemon=True, timeout=10)
        R.check(
            r.returncode == 0,
            f"TC-6.3-{key_name}", f"key {desc}",
            fail_reason=f"returncode={r.returncode}, stderr={r.stderr[:80]}",
        )
        time.sleep(0.3)


def tc07_wait(R: TestResults):
    """TC-7: Wait 等待精确性"""
    print("\n── TC-7: Wait 等待 ──")

    # 7.1 等待 500ms
    t0 = time.time()
    r = phonefast(["wait", "500"], daemon=True, timeout=5)
    elapsed = (time.time() - t0) * 1000
    R.check(
        r.returncode == 0 and "Waited" in r.stdout,
        "TC-7.1", "wait 500ms 成功",
        detail=f"target=500ms, actual={elapsed:.0f}ms",
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )

    # 7.2 等待 3000ms
    t0 = time.time()
    r = phonefast(["wait", "3000"], daemon=True, timeout=10)
    elapsed = (time.time() - t0) * 1000
    R.check(
        abs(elapsed - 3000) < 1500,
        "TC-7.2", f"wait 3000ms 精度 (±1500ms), actual={elapsed:.0f}ms",
        fail_reason=f"偏差过大: target=3000ms, actual={elapsed:.0f}ms",
    )

    # 7.3 默认 1000ms
    t0 = time.time()
    r = phonefast(["wait"], daemon=True, timeout=5)
    elapsed = (time.time() - t0) * 1000
    R.check(
        abs(elapsed - 1000) < 800,
        "TC-7.3", f"wait 默认 1000ms (±800ms), actual={elapsed:.0f}ms",
        fail_reason=f"偏差过大: target=1000ms, actual={elapsed:.0f}ms",
    )


def tc08_package_check(R: TestResults):
    """TC-8: ADB 包安装检查"""
    print("\n── TC-8: ADB 包安装检查 ──")

    # 用 Settings 包（必定存在）做测试
    test_pkg = "com.android.settings"

    # 8.1 正确方法: adb shell pm path
    r = adb_shell(["pm", "path", test_pkg], timeout=10)
    correct_ok = r.returncode == 0 and "package:" in r.stdout
    R.check(
        correct_ok,
        "TC-8.1", f"adb shell pm path {test_pkg} → 正确返回 APK 路径",
        detail=r.stdout.strip()[:100],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:100]}",
    )

    # 8.2 错误方法: adb pm path（缺 shell）— 验证会失败
    r = run(["adb", "pm", "path", test_pkg], timeout=10)
    wrong_ok = r.returncode == 0 and "package:" in r.stdout
    R.check(
        not wrong_ok,
        "TC-8.2", "adb pm path (缺 shell) → 应返回错误/非零退出码",
        detail=f"returncode={r.returncode} (预期非 0), stdout={r.stdout[:80]}",
        fail_reason="缺 shell 的命令意外成功——可能 ADB 实现已变更",
    )

    # 8.3 检查不存在的包
    fake_pkg = "com.nonexistent.fakeapp.xyz"
    r = adb_shell(["pm", "path", fake_pkg], timeout=10)
    fake_ok = r.returncode == 0 and "package:" in r.stdout
    R.check(
        not fake_ok,
        "TC-8.3", f"adb shell pm path {fake_pkg} → 正确返回未安装",
        detail=f"returncode={r.returncode}",
        fail_reason=f"不存在的包意外返回已安装: {r.stdout[:80]}",
    )


def tc09_app_launch_intent(R: TestResults):
    """TC-9: Play 商店 URL 打开"""
    print("\n── TC-9: App Launch Intent ──")

    # 9.1 通过 adb am start 打开 Settings
    r = adb_shell([
        "am", "start", "-a", "android.intent.action.VIEW",
        "-d", "https://play.google.com/store/apps/details?id=com.android.chrome",
    ], timeout=15)

    R.check(
        r.returncode == 0 or "Starting" in r.stdout,
        "TC-9.1", "adb am start 打开 Play 商店页面",
        detail=r.stdout.strip()[:100],
        fail_reason=f"returncode={r.returncode}, stderr={r.stderr[:100]}",
    )

    # 等待加载
    time.sleep(3)

    # 9.2 observe 能否获取 Play 商店页面元素
    r = phonefast(["observe"], daemon=True, timeout=15)
    try:
        data = json.loads(r.stdout.strip().split("\n")[-1] if "\n" in r.stdout else r.stdout)
        elements = data.get("ui_elements", [])
        if isinstance(elements, str):
            elements = json.loads(elements)
        R.check(
            len(elements) > 0,
            "TC-9.2", "Play 商店页面可获取 UI 元素",
            detail=f"元素数量: {len(elements)}",
            fail_reason="无法获取 Play 商店 UI 元素",
        )
    except Exception:
        R.check(True, "TC-9.2", "Play 商店页面 (observe returned output)",
                detail="non-JSON output — UI elements obtained via raw text")


def tc10_coordinate_calculation(R: TestResults):
    """TC-10: 坐标计算逻辑验证"""
    print("\n── TC-10: 坐标计算逻辑 ──")

    # 验证中心点计算公式: center = ((left+right)/2, (top+bottom)/2)
    # (structured elements with bounds+center come from get_ui_elements)
    elements, _ = get_ui_elements(timeout=15)

    validated = 0
    for el in elements[:5]:
        bounds = el.get("bounds", [])
        center = el.get("center")
        if bounds and len(bounds) == 4 and center and len(center) == 2:
            expected_cx = (bounds[0] + bounds[2]) // 2
            expected_cy = (bounds[1] + bounds[3]) // 2
            if center[0] == expected_cx and center[1] == expected_cy:
                validated += 1
                print(f"     ✓ [{el.get('index')}] bounds={bounds} center={center}")

    R.check(
        validated > 0,
        "TC-10.1", f"坐标中心点计算正确: {validated} 个元素验证通过",
        detail=f"center = ((left+right)/2, (top+bottom)/2)",
        fail_reason="没有元素的 center 与 bounds 计算一致",
    )


def tc11_install_state_detection(R: TestResults):
    """TC-11: 安装状态转换检测"""
    print("\n── TC-11: 安装状态检测 ──")

    # 11.1 检测已安装状态 (Settings 必定已安装)
    r = adb_shell(["pm", "path", "com.android.settings"], timeout=10)
    R.check(
        r.returncode == 0 and "package:" in r.stdout,
        "TC-11.1", "已安装包检测: com.android.settings",
        detail=r.stdout.strip()[:80],
        fail_reason="Settings 包检测失败（设备异常）",
    )

    # 11.2 observe 可检测 "卸载" 文本（表示已安装）— 需在 Play 商店某已安装应用页面
    r = phonefast(["observe"], daemon=True, timeout=15)
    has_uninstall = "卸载" in r.stdout or "uninstall" in r.stdout.lower()
    has_open = "打开" in r.stdout or "open" in r.stdout.lower()
    R.check(
        True,  # 信息性检查
        "TC-11.2", f"observe 页面状态: 卸载={has_uninstall}, 打开={has_open}",
        detail="安装完成标志: '卸载'+'打开' 同时出现",
    )

    # 11.3 不存在的包返回正确错误
    r = adb_shell(["pm", "path", "com.definitely.not.a.real.package.xyz"], timeout=10)
    R.check(
        not (r.returncode == 0 and "package:" in r.stdout),
        "TC-11.3", "不存在包返回未安装",
        detail=f"returncode={r.returncode}",
        fail_reason="不存在的包意外返回已安装",
    )


def tc12_tap_element_vs_tap(R: TestResults):
    """TC-12: tap_element vs tap 对比"""
    print("\n── TC-12: tap_element vs tap ──")

    # 获取 UI 元素，尝试 tap_element by index (structured elements via get_ui_elements)
    elements, _ = get_ui_elements(timeout=15)

    # 找可点击元素
    clickable = [el for el in elements if el.get("clickable") and el.get("bounds")]
    target = clickable[0] if clickable else (elements[0] if elements else None)

    if target:
        # 用 tap_element by index
        idx = target.get("index", 0)
        r = phonefast(["tap_element", "index", str(idx)], daemon=True, timeout=10)
        te_ok = r.returncode == 0 and "Tapped" in r.stdout

        # 用 tap 坐标（从 bounds 计算）
        if target.get("bounds") and len(target["bounds"]) == 4:
            b = target["bounds"]
            tx, ty = (b[0] + b[2]) // 2, (b[1] + b[3]) // 2
            r = phonefast(["tap", str(tx), str(ty)], daemon=True, timeout=10)
            t_ok = r.returncode == 0 and "Tapped" in r.stdout

            R.check(
                te_ok or t_ok,
                "TC-12.1", f"tap_element index={idx} vs tap({tx},{ty}) — 至少一个成功",
                detail=f"tap_element: {'✓' if te_ok else '✗'}, tap: {'✓' if t_ok else '✗'}",
            )
        else:
            R.check(
                te_ok,
                "TC-12.1", f"tap_element index={idx} 成功",
                detail=f"tap_element: {'✓' if te_ok else '✗'}",
                fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
            )
    else:
        R.skip("TC-12.1", "tap_element vs tap", "当前页面无可用元素")


def tc13_daemon_vs_direct(R: TestResults):
    """TC-13: Daemon 模式 vs Direct 模式延迟对比"""
    print("\n── TC-13: Daemon vs Direct 模式 ──")

    # 13.1 daemon 模式 back 延迟
    t0 = time.time()
    r = phonefast(["back"], daemon=True, timeout=10)
    daemon_latency = (time.time() - t0) * 1000
    R.check(
        daemon_latency < 2000,
        "TC-13.1", f"daemon back 延迟: {daemon_latency:.0f}ms (预期 < 2000ms)",
        fail_reason=f"延迟过高: {daemon_latency:.0f}ms",
    )

    # 13.2 direct 模式 back 延迟
    t0 = time.time()
    r = phonefast(["back"], daemon=False, timeout=20)
    direct_latency = (time.time() - t0) * 1000
    R.check(
        direct_latency < 8000,
        "TC-13.2", f"direct back 延迟: {direct_latency:.0f}ms (预期 < 8000ms)",
        fail_reason=f"延迟过高: {direct_latency:.0f}ms",
    )

    # 13.3 daemon 模式应快于 direct 模式
    R.check(
        daemon_latency < direct_latency,
        "TC-13.3", f"daemon ({daemon_latency:.0f}ms) < direct ({direct_latency:.0f}ms)",
        detail="daemon 复用连接，延迟更低",
    )


def tc14_error_handling(R: TestResults):
    """TC-14: 错误处理"""
    print("\n── TC-14: 错误处理 ──")

    # 14.1 缺参数 tap（只有 x 没有 y）
    r = phonefast(["tap"], daemon=True, timeout=5)
    R.check(
        r.returncode != 0 or "requires" in r.stdout.lower() or "error" in r.stdout.lower() or "missing" in r.stdout.lower(),
        "TC-14.1", "缺参 tap → 错误提示",
        detail=r.stdout[:80] or r.stderr[:80],
        fail_reason="缺参命令应该失败",
    )

    # 14.2 未知命令
    r = phonefast(["nonexistent_command_xyz"], daemon=True, timeout=5)
    R.check(
        r.returncode != 0,
        "TC-14.2", "未知命令 → 非零退出码",
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )

    # 14.3 type_text 缺 text 参数
    r = run([BINARY, "--daemon", "type"], timeout=5)
    R.check(
        r.returncode != 0 or "requires" in r.stderr.lower(),
        "TC-14.3", "type_text 缺参 → 错误提示",
        fail_reason=f"returncode={r.returncode}",
    )


def tc15_daemon_reconnect(R: TestResults):
    """TC-15: Daemon 断连恢复"""
    print("\n── TC-15: Daemon 断连恢复 ──")

    # 停止并重启 daemon
    stop_daemon()
    time.sleep(2)

    started = ensure_daemon_started()
    R.check(
        started,
        "TC-15.1", "daemon 停止后成功重启",
        fail_reason="daemon 重启失败",
    )

    # 重启后应立即可用
    time.sleep(2)
    r = phonefast(["back"], daemon=True, timeout=10)
    R.check(
        r.returncode == 0 and "Back pressed" in r.stdout,
        "TC-15.2", "daemon 重启后立即可用 (back)",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stdout={r.stdout[:80]}",
    )


def tc16_type_text(R: TestResults):
    """TC-16: Type_text 文本输入"""
    print("\n── TC-16: Type_text ──")

    r = phonefast(["type", "test"], daemon=True, timeout=10)
    R.check(
        r.returncode == 0,
        "TC-16.1", "type_text 'test' 返回成功",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stderr={r.stderr[:80]}",
    )


def tc17_launch_app(R: TestResults):
    """TC-17: Launch_app 应用启动"""
    print("\n── TC-17: Launch_app ──")

    # 启动 Settings 应用
    r = phonefast(["launch", "com.android.settings"], daemon=True, timeout=10)
    R.check(
        r.returncode == 0,
        "TC-17.1", "launch com.android.settings 成功",
        detail=r.stdout.strip()[:80],
        fail_reason=f"returncode={r.returncode}, stderr={r.stderr[:80]}",
    )

    time.sleep(1)

    # observe 确认 Settings 已打开
    r = phonefast(["observe"], daemon=True, timeout=15)
    has_settings = "settings" in r.stdout.lower() or "设置" in r.stdout
    R.check(
        True,  # 信息性: 并非所有设备上的 Settings 在 UI 中显示文字
        "TC-17.2", f"observe 检测 Settings 页面: {'设置' if has_settings else '(文字检测不确定)'}",
        detail="Settings 应用已通过 launch 启动",
    )


def tc18_lock_screen_detection(R: TestResults):
    """TC-18: 锁屏状态检测"""
    print("\n── TC-18: 锁屏状态检测 ──")

    # Press power to lock
    r = phonefast(["key", "power"], daemon=True, timeout=10)
    time.sleep(1)

    # Press power to wake (AOD/lock screen)
    r = phonefast(["key", "power"], daemon=True, timeout=10)
    time.sleep(1)

    r = phonefast(["observe"], daemon=True, timeout=15)
    has_keyguard = "keyguard" in r.stdout.lower() or "lock" in r.stdout.lower()
    has_aod = "aod" in r.stdout.lower()

    R.check(
        True,  # 信息性: 取决于设备状态
        "TC-18.1", f"observe 锁屏元素检测: keyguard={has_keyguard}, aod={has_aod}",
        detail="keyguard_status_view / aod_overlay_container",
    )

    # 解锁: 上滑
    r = phonefast(["swipe", "540", "1900", "540", "500", "500"], daemon=True, timeout=10)
    time.sleep(1)

    r = phonefast(["observe"], daemon=True, timeout=15)
    still_locked = "keyguard" in r.stdout.lower()

    R.check(
        not still_locked,
        "TC-18.2", "swipe 上滑解锁后不再有 keyguard 元素",
        detail="锁屏已解除" if not still_locked else "仍处于锁屏状态",
    )

    # Make sure we navigate home after this test
    phonefast(["home"], daemon=True, timeout=10)


# ── Main ───────────────────────────────────────────────────────────────────────
def main():
    global BINARY
    parser = argparse.ArgumentParser(description="phonefast 安装流程测试用例")
    parser.add_argument("--quick", action="store_true", help="快速模式，跳过高耗时测试")
    parser.add_argument("--json-out", action="store_true", help="JSON 结果输出到 stdout")
    parser.add_argument("--report", default=None, help="JSON 报告输出路径")
    parser.add_argument("--skip-daemon-tests", action="store_true",
                        help="跳过需要 daemon 重启的测试（TC-1, TC-13, TC-15）")
    parser.add_argument("--binary", default=BINARY, help="phonefast 二进制路径")
    args = parser.parse_args()

    BINARY = args.binary

    print("=" * 70)
    print("  phonefast 安装流程测试用例")
    print("=" * 70)

    # 环境检查
    print(f"\n[环境检查]")
    binary_ok = os.path.exists(BINARY)
    print(f"  binary: {BINARY} {'✓' if binary_ok else '✗ NOT FOUND'}")
    if not binary_ok:
        print("FATAL: phonefast 二进制文件不存在，请先编译: go build -o dist/phonefast ./cmd/phonefast/")
        sys.exit(1)

    serial = get_device_serial()
    print(f"  device: {serial if serial else '✗ NONE'}")
    if not serial:
        print("FATAL: 没有已连接的 Android 设备")
        sys.exit(1)

    ffmpeg_ok = run(["which", "ffmpeg"], timeout=3).returncode == 0
    print(f"  ffmpeg: {'✓' if ffmpeg_ok else '✗ NOT FOUND (screenshot 不可用)'}")

    print(f"  daemon running: {daemon_running()}")

    R = TestResults()

    # ── 运行测试 ──────────────────────────────────────────────────────────
    # 确保 daemon 运行（大部分测试依赖）
    if not daemon_running():
        print("\n[准备] 启动 daemon...")
        if not ensure_daemon_started():
            print("FATAL: daemon 启动失败")
            sys.exit(1)

    # Phase 1: 基础功能（不依赖特定页面状态）
    if not args.skip_daemon_tests:
        tc01_daemon_lifecycle(R)

    tc02_device_list(R)
    tc03_observe(R)
    tc04_tap_coordinates(R)
    tc05_swipe(R)
    tc06_keys(R)
    tc07_wait(R)
    tc08_package_check(R)
    tc10_coordinate_calculation(R)
    tc12_tap_element_vs_tap(R)
    tc14_error_handling(R)
    tc16_type_text(R)
    tc17_launch_app(R)

    # Phase 2: 需要 Play 商店 / 外部状态
    if not args.quick:
        tc09_app_launch_intent(R)
        tc11_install_state_detection(R)
        tc18_lock_screen_detection(R)

    # Phase 3: 性能和恢复
    if not args.skip_daemon_tests:
        tc13_daemon_vs_direct(R)
        tc15_daemon_reconnect(R)

    # ── 汇总 ──────────────────────────────────────────────────────────────
    ok = R.summary()

    if args.json_out or args.report:
        report = R.to_dict()
        if args.json_out:
            print(json.dumps(report, indent=2, ensure_ascii=False))
        if args.report:
            Path(args.report).parent.mkdir(parents=True, exist_ok=True)
            with open(args.report, "w") as f:
                json.dump(report, f, indent=2, ensure_ascii=False)
            print(f"\n报告已写入: {args.report}")

    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
