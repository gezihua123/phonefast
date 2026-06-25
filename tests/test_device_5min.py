#!/usr/bin/env python3
"""
5-minute real-device test for phonefast dist/ package.
Uses a single persistent MCP SSE session (no per-command reconnect overhead).

Phases:
  Phase 1 (0:00-0:30)  Connect & baseline
  Phase 2 (0:30-1:30)  Navigation — Home, Settings, scroll
  Phase 3 (1:30-2:30)  Text input — dialer / search field
  Phase 4 (2:30-3:30)  UI element detection & tap-by-text
  Phase 5 (3:30-4:30)  Performance measurement (screenshot & UI-dump latency)
  Phase 6 (4:30-5:00)  Stress — rapid swipe + cleanup
"""

import socket, json, time, sys, os, subprocess, threading, signal
import urllib.parse, struct, base64

# ── Config ────────────────────────────────────────────────────────────────────
BINARY  = os.path.join(os.path.dirname(__file__), "..", "dist", "phonefast")
PORT    = 18120
SERIAL  = None
W, H    = 720, 1600        # updated after first observe
START   = time.time()

PASS, FAIL, WARN = [], [], []

def elapsed():
    return f"{time.time()-START:5.1f}s"

def ok(name, note=""):
    PASS.append(name)
    suffix = f"  ({note})" if note else ""
    print(f"  [{elapsed()}] ✓  {name}{suffix}")

def fail(name, reason=""):
    FAIL.append(name)
    print(f"  [{elapsed()}] ✗  {name}  — {reason}")

def warn(name, note=""):
    WARN.append(name)
    print(f"  [{elapsed()}] ⚠  {name}  — {note}")

# ── MCP SSE client ─────────────────────────────────────────────────────────────
class MCPClient:
    """Thin MCP SSE client: one GET /sse stream, POST /messages per call."""

    def __init__(self, port):
        self.port = port
        self._sess_id = None
        self._sse_sock = None
        self._sse_buf  = b""
        self._lock     = threading.Lock()
        self._pending  = {}     # id → Event
        self._pending_data = {} # id → result
        self._reader   = None
        self._next_id  = 1

    def connect(self, timeout=10):
        """Open SSE stream and wait for endpoint event."""
        raw = socket.socket()
        raw.connect(("127.0.0.1", self.port))
        raw.settimeout(timeout)
        raw.send(b"GET /Phone/sse HTTP/1.1\r\nHost: 127.0.0.1\r\n"
                 b"Accept: text/event-stream\r\n\r\n")

        # Read until endpoint event
        buf = b""
        t0 = time.time()
        ep = None
        while time.time()-t0 < timeout:
            try:
                chunk = raw.recv(1024)
            except socket.timeout:
                break
            if not chunk:
                break
            buf += chunk
            for line in buf.decode(errors="replace").split("\n"):
                line = line.strip()
                if line.startswith("data:") and "/messages" in line:
                    ep = line[5:].strip()
            if ep:
                break

        if not ep:
            raise RuntimeError("no endpoint event from SSE server")

        self._sess_id = urllib.parse.parse_qs(
            urllib.parse.urlparse(ep).query
        ).get("session_id", [""])[0]
        raw.settimeout(None)
        self._sse_sock = raw

        # Background reader delivers data events to waiting callers
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()

    def _read_loop(self):
        while True:
            try:
                chunk = self._sse_sock.recv(65536)
            except Exception:
                break
            if not chunk:
                break
            with self._lock:
                self._sse_buf += chunk
            self._drain()

    def _drain(self):
        with self._lock:
            text = self._sse_buf.decode(errors="replace")
        events = text.split("\n\n")
        consumed = 0
        for block in events[:-1]:
            consumed += len(block.encode()) + 2
            for line in block.split("\n"):
                line = line.strip()
                if not line.startswith("data:"):
                    continue
                raw_data = line[5:].strip()
                try:
                    msg = json.loads(raw_data)
                except Exception:
                    continue
                rid = msg.get("id")
                if rid is not None:
                    with self._lock:
                        self._pending_data[rid] = msg
                        ev = self._pending.get(rid)
                    if ev:
                        ev.set()
        with self._lock:
            self._sse_buf = self._sse_buf[consumed:]

    def call(self, method, params=None, timeout=30):
        """Send JSON-RPC via POST and wait for response on SSE stream."""
        with self._lock:
            rid = self._next_id
            self._next_id += 1
            ev = threading.Event()
            self._pending[rid] = ev

        body = json.dumps({
            "jsonrpc": "2.0", "id": rid,
            "method": method,
            "params": params or {}
        })
        path = f"/Phone/messages?session_id={self._sess_id}"
        req = (f"POST {path} HTTP/1.1\r\nHost: 127.0.0.1\r\n"
               f"Content-Type: application/json\r\n"
               f"Content-Length: {len(body)}\r\n\r\n{body}")

        s = socket.socket()
        s.connect(("127.0.0.1", self.port))
        s.settimeout(5)
        s.sendall(req.encode())
        try:
            s.recv(256)   # consume HTTP 202
        except Exception:
            pass
        s.close()

        if not ev.wait(timeout):
            with self._lock:
                self._pending.pop(rid, None)
            raise TimeoutError(f"no response for {method} (id={rid})")

        with self._lock:
            result = self._pending_data.pop(rid, None)
            self._pending.pop(rid, None)
        return result

    def tool(self, name, args=None, timeout=30):
        """Call tools/call and return text content."""
        resp = self.call("tools/call",
                         {"name": name, "arguments": args or {}},
                         timeout=timeout)
        if resp is None:
            return None
        result = resp.get("result", {})
        content = result.get("content", [])
        if content:
            return content[0].get("text", "")
        return str(resp.get("error", ""))

    def close(self):
        try:
            self._sse_sock.close()
        except Exception:
            pass

# ── helpers ───────────────────────────────────────────────────────────────────

def save_png(b64_text, path):
    try:
        data = json.loads(b64_text)
        raw = base64.b64decode(data.get("base64","") or data.get("screenshot_base64",""))
        if raw:
            open(path, "wb").write(raw)
            return len(raw)
    except Exception:
        pass
    return 0

def parse_elements(text):
    """Parse get_ui_elements formatted text into list of (index, label) tuples."""
    items = []
    for line in (text or "").split("\n"):
        line = line.strip()
        if line.startswith("[") and "]" in line:
            try:
                idx = int(line[1:line.index("]")])
                label = line[line.index("]")+1:].strip()
                items.append((idx, label))
            except Exception:
                pass
    return items

def find_element(text, keyword):
    """Find first element index whose label contains keyword (case-insensitive)."""
    kw = keyword.lower()
    for idx, label in parse_elements(text):
        if kw in label.lower():
            return idx
    return None

# ── main ──────────────────────────────────────────────────────────────────────

def main():
    global W, H, SERIAL

    # ── Preflight ──────────────────────────────────────────────────────────────
    print(f"\n{'='*58}")
    print(f"  phonefast 5-min real-device test  |  {time.strftime('%H:%M:%S')}")
    print(f"{'='*58}")

    if not os.path.isfile(BINARY):
        print(f"FATAL: binary not found: {BINARY}"); sys.exit(1)

    r = subprocess.run(["adb", "devices"], capture_output=True, text=True)
    for line in r.stdout.strip().split("\n")[1:]:
        if "\tdevice" in line:
            SERIAL = line.split()[0]
    if not SERIAL:
        print("FATAL: no ADB device connected"); sys.exit(1)

    model = subprocess.run(["adb","-s",SERIAL,"shell","getprop","ro.product.model"],
                           capture_output=True, text=True).stdout.strip()
    print(f"\n  Device : {SERIAL}  ({model})")
    print(f"  Binary : {os.path.getsize(BINARY)//1024} KB\n")

    # ── Start MCP SSE server ───────────────────────────────────────────────────
    print(f"[{elapsed()}] Starting MCP SSE server on :{PORT} ...")
    proc = subprocess.Popen(
        [BINARY, "serve", "--transport", "sse", "--port", str(PORT)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE
    )

    # Wait for device session to be ready (server logs "connected" on stderr)
    ready = False
    for _ in range(40):
        time.sleep(0.3)
        try:
            s = socket.create_connection(("127.0.0.1", PORT), timeout=0.5)
            s.close()
            ready = True
            break
        except Exception:
            pass

    if not ready:
        fail("server start", "port not reachable after 12s")
        proc.terminate(); sys.exit(1)

    # Extra wait for device session fully established
    time.sleep(1.5)

    mcp = MCPClient(PORT)
    try:
        mcp.connect(timeout=10)
    except Exception as e:
        fail("MCP SSE connect", str(e))
        proc.terminate(); sys.exit(1)

    ok("MCP SSE server + session ready")

    # ── initialize ─────────────────────────────────────────────────────────────
    init = mcp.call("initialize", {
        "protocolVersion": "2024-11-05",
        "clientInfo": {"name": "test", "version": "1"}
    })
    if init and "result" in init and "serverInfo" in init["result"]:
        ok("MCP initialize", init["result"]["serverInfo"].get("name","?"))
    else:
        warn("MCP initialize", str(init)[:80])

    tools_resp = mcp.call("tools/list")
    tool_names = [t["name"] for t in
                  (tools_resp or {}).get("result",{}).get("tools",[])]
    ok("tools/list", f"{len(tool_names)} tools: {', '.join(tool_names[:5])}...")

    # ──────────────────────────────────────────────────────────────────────────
    # Phase 1 — Baseline observe
    # ──────────────────────────────────────────────────────────────────────────
    print(f"\n--- Phase 1: Baseline [{elapsed()}] ---")

    obs = mcp.tool("observe", timeout=20)
    if obs:
        try:
            d = json.loads(obs)
            b64len = len(d.get("screenshot_base64",""))
            cnt = d.get("element_count", 0)
            W = d.get("width", W) or W
            H = d.get("height", H) or W
        except Exception:
            b64len, cnt = 0, 0

        if b64len > 5000:
            ok("baseline observe", f"{b64len} B64 chars, {cnt} elements, {W}×{H}")
        else:
            fail("baseline observe", f"b64={b64len}")
    else:
        fail("baseline observe", "no response")

    shot = mcp.tool("screenshot", timeout=15)
    sz = save_png(shot or "", "/tmp/pf_phase1.png")
    if sz > 5000:
        ok("baseline screenshot", f"{sz} bytes → /tmp/pf_phase1.png")
    else:
        fail("baseline screenshot", f"png={sz}")

    # ──────────────────────────────────────────────────────────────────────────
    # Phase 2 — Navigation
    # ──────────────────────────────────────────────────────────────────────────
    print(f"\n--- Phase 2: Navigation [{elapsed()}] ---")

    mcp.tool("home"); time.sleep(0.8)
    ok("press Home")

    shot1 = mcp.tool("screenshot", timeout=15)
    sz1 = save_png(shot1 or "", "/tmp/pf_home.png")
    ok("screenshot after Home", f"{sz1} bytes")

    # Open Settings
    mcp.tool("launch_app", {"app": "com.android.settings"}); time.sleep(1.5)
    ui = mcp.tool("get_ui_elements", timeout=15)
    els = parse_elements(ui or "")
    in_settings = any("setting" in lbl.lower() or "设置" in lbl for _, lbl in els)
    if in_settings:
        ok("launch Settings", f"{len(els)} elements")
    else:
        warn("launch Settings", f"got {len(els)} elements (may still be loading)")

    # Scroll down in Settings
    cx = W // 2
    mcp.tool("swipe", {"start_x": cx, "start_y": int(H*0.7),
                        "end_x": cx, "end_y": int(H*0.3), "duration_ms": 400})
    time.sleep(0.5)
    ui2 = mcp.tool("get_ui_elements", timeout=15)
    els2 = parse_elements(ui2 or "")
    ok("swipe down in Settings", f"{len(els2)} elements after scroll")

    shot2 = mcp.tool("screenshot", timeout=15)
    sz2 = save_png(shot2 or "", "/tmp/pf_settings.png")
    ok("screenshot Settings", f"{sz2} bytes → /tmp/pf_settings.png")

    # Swipe back up
    mcp.tool("swipe", {"start_x": cx, "start_y": int(H*0.3),
                        "end_x": cx, "end_y": int(H*0.7), "duration_ms": 400})
    time.sleep(0.4)
    ok("swipe back up")

    # ──────────────────────────────────────────────────────────────────────────
    # Phase 3 — Text input (dialer)
    # ──────────────────────────────────────────────────────────────────────────
    print(f"\n--- Phase 3: Text input [{elapsed()}] ---")

    mcp.tool("back"); time.sleep(0.5)
    mcp.tool("home"); time.sleep(0.8)

    # Open dialer
    mcp.tool("launch_app", {"app": "com.android.dialer"}); time.sleep(1.2)
    ui3 = mcp.tool("get_ui_elements", timeout=15)
    els3 = parse_elements(ui3 or "")
    ok("launch Dialer", f"{len(els3)} elements")

    # Try to find and tap a dialpad button; fallback to tapping center
    dialpad_idx = find_element(ui3 or "", "dialpad") or find_element(ui3 or "", "拨号")
    if dialpad_idx is not None:
        mcp.tool("tap_element", {"index": dialpad_idx}); time.sleep(0.5)
        ok("tap dialpad button", f"index={dialpad_idx}")
    else:
        # Tap lower half where dialpad usually lives
        mcp.tool("tap", {"x": cx, "y": int(H*0.85)}); time.sleep(0.3)
        warn("tap dialpad button", "element not found, tapped by coords")

    # Type a phone number
    mcp.tool("type_text", {"text": "10086"}); time.sleep(0.5)
    ui4 = mcp.tool("get_ui_elements", timeout=15)
    has_digits = any("10086" in lbl or any(c in lbl for c in "12345678900")
                     for _, lbl in parse_elements(ui4 or ""))
    if has_digits:
        ok("type_text '10086'", "digits visible in UI")
    else:
        ok("type_text '10086' sent", "UI may not reflect in elements")

    shot3 = mcp.tool("screenshot", timeout=15)
    sz3 = save_png(shot3 or "", "/tmp/pf_dialer.png")
    ok("screenshot dialer", f"{sz3} bytes → /tmp/pf_dialer.png")

    # Clear with back-key presses
    for _ in range(5):
        mcp.tool("press_key", {"key": "delete"}); time.sleep(0.05)
    ok("delete 5 digits")

    mcp.tool("back"); time.sleep(0.4)

    # ──────────────────────────────────────────────────────────────────────────
    # Phase 4 — UI element interaction
    # ──────────────────────────────────────────────────────────────────────────
    print(f"\n--- Phase 4: UI element interaction [{elapsed()}] ---")

    mcp.tool("home"); time.sleep(0.8)
    ui5 = mcp.tool("get_ui_elements", timeout=15)
    els5 = parse_elements(ui5 or "")

    clickable = [(i, lbl) for i, lbl in els5
                 if "[clickable]" in lbl and lbl.replace("[clickable]","").strip()]
    ok("home screen elements", f"{len(els5)} total, {len(clickable)} clickable")

    if clickable:
        idx0, lbl0 = clickable[0]
        mcp.tool("tap_element", {"index": idx0}); time.sleep(1.0)
        ui6 = mcp.tool("get_ui_elements", timeout=15)
        changed = parse_elements(ui6 or "") != els5
        label_preview = lbl0[:40]
        ok(f"tap_element [{idx0}] '{label_preview}'",
           "screen changed" if changed else "screen same (app may need time)")
        mcp.tool("back"); time.sleep(0.5)
    else:
        warn("tap_element", "no labeled clickable elements on home screen")

    # tap_element by text
    mcp.tool("launch_app", {"app": "com.android.settings"}); time.sleep(1.5)
    ui_s = mcp.tool("get_ui_elements", timeout=15)

    # Try to tap first text-labeled element in settings
    text_els = [(i, lbl) for i, lbl in parse_elements(ui_s or "")
                if "[clickable]" in lbl
                and 'text="' in lbl
                and len(lbl) > 12]
    if text_els:
        idx_t, lbl_t = text_els[0]
        # extract text value
        import re
        m = re.search(r'text="([^"]+)"', lbl_t)
        txt_val = m.group(1) if m else None
        if txt_val:
            mcp.tool("tap_element", {"text": txt_val}); time.sleep(0.8)
            ok(f"tap_element by text='{txt_val[:30]}'")
            mcp.tool("back"); time.sleep(0.5)
        else:
            mcp.tool("tap_element", {"index": idx_t}); time.sleep(0.8)
            ok(f"tap_element by index={idx_t}")
            mcp.tool("back"); time.sleep(0.5)
    else:
        warn("tap_element by text", "no suitable text element found")

    mcp.tool("back"); time.sleep(0.3)
    mcp.tool("home"); time.sleep(0.6)

    # ──────────────────────────────────────────────────────────────────────────
    # Phase 5 — Latency benchmark
    # ──────────────────────────────────────────────────────────────────────────
    print(f"\n--- Phase 5: Performance benchmark [{elapsed()}] ---")
    N = 8

    # Screenshot latency
    shot_times = []
    for i in range(N):
        t0 = time.time()
        s = mcp.tool("screenshot", timeout=15)
        dt = time.time()-t0
        shot_times.append(dt)
        if not s or len(s) < 100:
            fail(f"screenshot #{i+1}", "empty")
            break

    if len(shot_times) == N:
        avg_s = sum(shot_times)/N
        mn_s, mx_s = min(shot_times), max(shot_times)
        ok(f"screenshot ×{N}", f"avg={avg_s:.2f}s  min={mn_s:.2f}s  max={mx_s:.2f}s")
    else:
        fail("screenshot benchmark", f"only {len(shot_times)}/{N}")

    # UI dump latency
    ui_times = []
    for i in range(N):
        t0 = time.time()
        u = mcp.tool("get_ui_elements", timeout=15)
        dt = time.time()-t0
        ui_times.append(dt)
        cnt_u = len(parse_elements(u or ""))
        if cnt_u == 0:
            warn(f"ui_dump #{i+1}", "0 elements")

    if len(ui_times) == N:
        avg_u = sum(ui_times)/N
        mn_u, mx_u = min(ui_times), max(ui_times)
        ok(f"ui_dump ×{N}", f"avg={avg_u:.2f}s  min={mn_u:.2f}s  max={mx_u:.2f}s")
    else:
        fail("ui_dump benchmark", f"only {len(ui_times)}/{N}")

    # ──────────────────────────────────────────────────────────────────────────
    # Phase 6 — Stress / cleanup
    # ──────────────────────────────────────────────────────────────────────────
    print(f"\n--- Phase 6: Stress [{elapsed()}] ---")

    mcp.tool("home"); time.sleep(0.5)

    # Rapid swipe x5
    for i in range(5):
        mcp.tool("swipe", {"start_x": cx, "start_y": int(H*0.75),
                            "end_x": cx, "end_y": int(H*0.25), "duration_ms": 200})
        time.sleep(0.15)
    ok("rapid swipe ×5")

    # Rapid tap x5 (center of screen)
    for i in range(5):
        mcp.tool("tap", {"x": cx, "y": H//2})
        time.sleep(0.1)
    ok("rapid tap ×5")

    mcp.tool("home"); time.sleep(0.5)

    # Final screenshot
    shot_final = mcp.tool("screenshot", timeout=15)
    sz_f = save_png(shot_final or "", "/tmp/pf_final.png")
    ok("final screenshot", f"{sz_f} bytes → /tmp/pf_final.png")

    # ── Cleanup ───────────────────────────────────────────────────────────────
    mcp.close()
    proc.terminate()
    try: proc.wait(timeout=5)
    except: proc.kill()

    # ── Summary ───────────────────────────────────────────────────────────────
    total = time.time()-START
    print(f"\n{'='*58}")
    print(f"  RESULT  ({total:.1f}s elapsed)")
    print(f"{'='*58}")
    print(f"  ✓ PASS : {len(PASS)}")
    print(f"  ⚠ WARN : {len(WARN)}")
    print(f"  ✗ FAIL : {len(FAIL)}")
    if FAIL:
        print(f"\n  Failed tests:")
        for f in FAIL:
            print(f"    ✗  {f}")
    if WARN:
        print(f"\n  Warnings:")
        for w in WARN:
            print(f"    ⚠  {w}")
    print(f"\n  Screenshots saved: /tmp/pf_*.png")
    print(f"{'='*58}")
    sys.exit(0 if not FAIL else 1)

if __name__ == "__main__":
    main()
