#!/usr/bin/env python3
"""
phonefast Tap timing comparison test — real device, raw scrcpy socket.

验证 Tap() 的 DOWN→UP 间隔对 Play 商店安装按钮的影响：
  - 10ms 间隔 → 安装按钮应静默拒绝（反自动化校验）
  - 50ms 间隔 → 安装按钮应正常触发

使用方法:
  python3 tests/test_tap_compare.py
"""

import subprocess
import socket
import struct
import time
import sys
import re
import os

ADB_SERIAL = None


def adb(args, timeout=15):
    cmd = ["adb"]
    if ADB_SERIAL:
        cmd += ["-s", ADB_SERIAL]
    cmd += args
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    return r.stdout.strip(), r.stderr.strip(), r.returncode


def get_device():
    global ADB_SERIAL
    out, _, _ = adb(["devices"])
    for line in out.split('\n')[1:]:
        if '\tdevice' in line:
            ADB_SERIAL = line.split('\t')[0]
            return ADB_SERIAL
    return None


def get_display_size():
    out, _, _ = adb(["shell", "wm", "size"])
    m = re.search(r'(\d+)x(\d+)', out)
    return (int(m[1]), int(m[2])) if m else (None, None)


def get_scrcpy_port():
    """Find scrcpy ADB forward port (video+control socket)."""
    out, _, _ = adb(["forward", "--list"])
    for line in out.split('\n'):
        m = re.search(r'scrcpy_[0-9a-f]+.*tcp:(\d+)', line)
        if m:
            return int(m[1])
    return None


def encode_touch(action, x, y, screen_w, screen_h):
    """Encode scrcpy TypeInjectTouchEvent message."""
    return struct.pack(
        '>BBqIIHHHII',
        2,          # TypeInjectTouchEvent
        action,     # 0=DOWN, 1=UP
        -1,         # pointerId (virtual finger)
        x, y,       # position
        screen_w, screen_h,
        0xffff,     # pressure (1.0)
        0, 0,       # actionBtn, buttons
    )


def tap_with_delay(sock, x, y, dev_w, dev_h, delay_ms):
    """Send DOWN → delay_ms → UP on control socket."""
    down = encode_touch(0, x, y, dev_w, dev_h)
    up = encode_touch(1, x, y, dev_w, dev_h)

    sock.sendall(down)
    t0 = time.time()
    time.sleep(delay_ms / 1000.0)
    sock.sendall(up)
    actual_ms = (time.time() - t0) * 1000
    return actual_ms


def get_ui_xml():
    """Get UI hierarchy XML."""
    adb(["shell", "uiautomator", "dump", "/sdcard/ui_test.xml"], timeout=20)
    out, _, rc = adb(["shell", "cat", "/sdcard/ui_test.xml"])
    return out if rc == 0 else ""


def find_button(xml, text):
    """Find button center coordinates in UI XML."""
    pattern = re.compile(
        rf'<node[^>]*text="{text}"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"[^>]*/>'
    )
    m = pattern.search(xml)
    if m:
        x1, y1, x2, y2 = map(int, m.groups())
        return (x1 + x2) // 2, (y1 + y2) // 2
    return None


def has_install_dialog(xml):
    """Check for install permission/progress indicators."""
    markers = ["取消", "就绪后自动打开", "安装应用后立即使用",
               "permission", "Permissions"]
    return any(m in xml for m in markers)


def wait_for_page_change(prev_xml, timeout=5):
    """Wait until UI changes from prev_xml."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        time.sleep(0.5)
        cur = get_ui_xml()
        if cur and cur != prev_xml:
            return cur
    return get_ui_xml()


def ensure_play_store_app_page():
    """Ensure we're on a Play Store app page with an install button."""
    for attempt in range(3):
        xml = get_ui_xml()
        btn = find_button(xml, "安装")
        if btn:
            print(f"  ✓ 找到安装按钮: {btn}")
            return btn
        # Try tapping "应用" tab
        print(f"  未找到安装按钮 (attempt {attempt+1}/3), 导航中...")
        app_btn = find_button(xml, "应用")
        if app_btn:
            tap_via_adb(*app_btn)
            time.sleep(1)
    return None


def tap_via_adb(x, y):
    """Tap via 'adb shell input tap' (absolute device coordinates)."""
    adb(["shell", "input", "tap", str(x), str(y)])


def test_delay(label, delay_ms, install_btn, dev_w, dev_h):
    """Test one delay value: connect, tap, check result."""
    print(f"\n{'=' * 60}")
    print(f"测试: {label} (delay={delay_ms}ms)")
    print(f"{'=' * 60}")

    # 1. Ensure we have a scrcpy session running
    port = get_scrcpy_port()
    if not port:
        print("  无 scrcpy 会话，启动 direct 模式...")
        # Start phonefast as background session
        subprocess.Popen(
            ["go", "run", "./cmd/phonefast", "tap", str(dev_w//2), str(dev_h//2)],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            cwd=os.path.join(os.path.dirname(__file__), "..")
        )
        time.sleep(5)
        port = get_scrcpy_port()
        if not port:
            print("  ❌ 无法建立 scrcpy 会话")
            return None

    print(f"  scrcpy port: {port}")

    # 2. Connect control socket
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(5)
    try:
        sock.connect(("127.0.0.1", port))
    except Exception as e:
        print(f"  ❌ 连接失败: {e}")
        return None

    # 3. Before UI
    xml_before = get_ui_xml()
    btn_before = find_button(xml_before, "安装")
    if not btn_before:
        print("  ❌ 页面无安装按钮")
        sock.close()
        return None
    print(f"  安装按钮: {btn_before}")

    # 4. Send tap
    actual_ms = tap_with_delay(sock, *btn_before, dev_w, dev_h, delay_ms)
    print(f"  实际间隔: {actual_ms:.1f}ms")
    sock.close()

    # 5. Wait and check
    time.sleep(2)
    xml_after = get_ui_xml()
    dialog = has_install_dialog(xml_after)

    if dialog:
        print(f"  ✅ 结果: 安装弹窗出现!")
        # Cancel the install
        cancel_btn = find_button(xml_after, "取消")
        if cancel_btn:
            print(f"  取消安装...")
            tap_via_adb(*cancel_btn)
            time.sleep(1)
    else:
        print(f"  ❌ 结果: 安装弹窗未出现 (静默拒绝)")

    return dialog


def main():
    print("=" * 60)
    print("phonefast Tap Timing: 10ms vs 50ms Comparison")
    print("=" * 60)

    # Setup
    if not get_device():
        print("❌ 没有连接的设备")
        sys.exit(1)
    print(f"设备: {ADB_SERIAL}")

    dev_w, dev_h = get_display_size()
    print(f"分辨率: {dev_w}x{dev_h}")

    # Ensure Play Store is open on an app page
    print("\n[准备] 确保 Play 商店在应用页面...")
    install_btn = ensure_play_store_app_page()
    if not install_btn:
        print("❌ 找不到安装按钮，请在 Play 商店打开一个未安装的应用页面")
        sys.exit(1)

    # Kill any existing scrcpy sessions first
    print("\n[清理] 停止现有 scrcpy 会话...")
    adb(["shell", "pkill", "-f", "scrcpy-server"], timeout=5)
    for fwd_out, _, _ in [adb(["forward", "--list"])]:
        for line in fwd_out.split("\n") if fwd_out else []:
            m = re.search(r'tcp:(\d+).*scrcpy', line)
            if m:
                adb(["forward", "--remove", f"tcp:{m[1]}"])
    time.sleep(1)

    # Test 1: 10ms
    result_10ms = test_delay("10ms (修复前)", 10, install_btn, dev_w, dev_h)

    # Cleanup between tests
    time.sleep(2)

    # Test 2: 50ms
    result_50ms = test_delay("50ms (修复后)", 50, install_btn, dev_w, dev_h)

    # Summary
    print(f"\n{'=' * 60}")
    print("结果汇总")
    print(f"{'=' * 60}")
    print(f"  10ms delay: {'✅ 触发' if result_10ms else '❌ 未触发'} (预期: 未触发)")
    print(f"  50ms delay: {'✅ 触发' if result_50ms else '❌ 未触发'} (预期: 触发)")

    if result_10ms is False and result_50ms is True:
        print(f"\n  ✅ 测试通过: 10ms 被静默拒绝, 50ms 成功触发安装")
        print("     符合 phonefast-tap-fix.md 分析结论")
        return 0
    elif result_10ms is True and result_50ms is True:
        print(f"\n  ⚠️ 10ms 和 50ms 都触发了安装 (设备可能未启用反自动化校验)")
        return 1
    elif result_10ms is False and result_50ms is False:
        print(f"\n  ⚠️ 两者都未触发 (可能是坐标/页面问题)")
        return 1
    else:
        print(f"\n  ⚠️ 异常结果")
        return 1


if __name__ == "__main__":
    sys.exit(main())
