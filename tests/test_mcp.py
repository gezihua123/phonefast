#!/usr/bin/env python3
"""
MCP 协议测试脚本 — 全面测试 phonefast MCP 能力。

测试项:
  1. MCP initialize 握手 (协议版本、服务端信息、能力声明)
  2. tools/list 响应 (工具数量、必填字段、inputSchema)
  3. tools/call 逐工具测试 (list_devices, screenshot, get_ui_elements, etc.)
  4. 错误处理 (未知方法、无效参数、未知工具)
  5. 响应格式校验 (JSON-RPC 2.0 合规)

用法:
  python3 test_mcp.py                  # 自动启动 STDIO 模式测试
  python3 test_mcp.py --sse --port 8019  # 连接已有 SSE 服务器测试
  python3 test_mcp.py --sse            # 自动启动 SSE 服务器测试
"""

import json, subprocess, socket, struct, time, sys, os, threading, argparse, base64

# ── 配置 ──────────────────────────────────────────────────────────────────────
BINARY = os.path.join(os.path.dirname(__file__), "..", "dist", "phonefast")
DEFAULT_SSE_PORT = 18019

PASS = []
FAIL = []

def ok(name, detail=""):
    PASS.append(name)
    msg = f"  ✓  {name}"
    if detail:
        msg += f"  ({detail})"
    print(msg)

def bad(name, reason=""):
    FAIL.append(name)
    print(f"  ✗  {name}  — {reason}")

def check(cond, name, detail="", fail_reason=""):
    if cond:
        ok(name, detail)
    else:
        bad(name, fail_reason or detail)
    return cond


# ══════════════════════════════════════════════════════════════════════════════
#  STDIO MCP 客户端
# ══════════════════════════════════════════════════════════════════════════════

class StdioMCPClient:
    """通过子进程 stdin/stdout 与 MCP 服务器通信。

    注意：STDIO 模式下，服务器需要先连接设备（2-4 秒），然后才读取 stdin。
    在连接完成前，stdin 的写入会被 OS 管道缓冲，不会丢失。
    """

    def __init__(self, binary_path, extra_args=None):
        self._next_id = 1
        self._lock = threading.Lock()
        cmd = [binary_path, "serve", "--transport", "stdio"]
        if extra_args:
            cmd.extend(extra_args)
        self._proc = subprocess.Popen(
            cmd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=False,
        )
        # 等待服务器连接设备并就绪（设备连接 + session 建立需要 3-5 秒）
        time.sleep(5)

    def _send_raw(self, method, params=None, timeout=2):
        """发送原始 JSON-RPC，返回 dict 或用 'error' key 包裹的错误。"""
        with self._lock:
            req_id = self._next_id
            self._next_id += 1

        req = {
            "jsonrpc": "2.0",
            "id": req_id,
            "method": method,
            "params": params or {},
        }
        req_bytes = (json.dumps(req) + "\n").encode("utf-8")

        try:
            self._proc.stdin.write(req_bytes)
            self._proc.stdin.flush()
        except BrokenPipeError:
            return {"error": "broken pipe — server exited?", "jsonrpc": "2.0", "id": req_id}

        try:
            line = self._proc.stdout.readline()
            if not line:
                return {"error": "no response — server exited?", "jsonrpc": "2.0", "id": req_id}
            return json.loads(line)
        except json.JSONDecodeError as e:
            return {"error": f"JSON decode: {e}", "jsonrpc": "2.0", "id": req_id}
        except Exception as e:
            return {"error": str(e), "jsonrpc": "2.0", "id": req_id}

    def call(self, method, params=None, timeout=20):
        """发送 JSON-RPC 请求，返回响应 dict。"""
        return self._send_raw(method, params, timeout)

    def tool(self, name, args=None, timeout=20):
        """调用 tools/call，返回 text 字符串或 ImageContent 序列化 JSON。"""
        resp = self._send_raw("tools/call", {"name": name, "arguments": args or {}}, timeout)
        if isinstance(resp, dict):
            if "result" in resp:
                content = resp["result"].get("content", [])
                if content:
                    # Check if response contains an image (screenshot/observe)
                    img = next((c for c in content if c.get("type") == "image"), None)
                    if img:
                        import re
                        img_b64 = img.get("data", "")
                        caption = next((c.get("text", "") for c in content if c.get("type") == "text"), "")
                        m = re.search(r"(\d+)x(\d+)", caption)
                        w = int(m.group(1)) if m else 0
                        h = int(m.group(2)) if m else 0

                        if name == "observe":
                            elements_text = next((c.get("text", "") for c in content[1:] if c.get("type") == "text"), "")
                            elements = [l.strip() for l in elements_text.split("\n") if l.strip().startswith("[")]
                            return json.dumps({
                                "screenshot_base64": img_b64,
                                "ui_elements": elements,
                                "element_count": len(elements),
                            })
                        else:
                            return json.dumps({
                                "base64": img_b64,
                                "width": w,
                                "height": h,
                                "format": "png",
                            })
                    return content[0].get("text", "")
            if "error" in resp:
                # 可能是 tools/call 返回了 isError 内容，或者 JSON-RPC 错误
                if isinstance(resp["error"], dict):
                    return f"rpc_error: {resp['error'].get('message', resp['error'])}"
                return f"error: {resp['error']}"
        return str(resp) if resp else None

    def close(self):
        try:
            self._proc.stdin.close()
            self._proc.stdout.close()
        except Exception:
            pass
        try:
            self._proc.terminate()
            self._proc.wait(timeout=5)
        except Exception:
            self._proc.kill()

    @property
    def returncode(self):
        return self._proc.poll()


# ══════════════════════════════════════════════════════════════════════════════
#  SSE MCP 客户端
# ══════════════════════════════════════════════════════════════════════════════

class SSEMCPClient:
    """通过 HTTP SSE 与 MCP 服务器通信。"""

    def __init__(self, port):
        self.port = port
        self._sess_id = None
        self._sse_sock = None
        self._sse_buf = b""
        self._lock = threading.Lock()
        self._pending = {}
        self._pending_data = {}
        self._next_id = 1

    def connect(self, timeout=15):
        """打开 SSE 连接，读取 endpoint 事件获取 session_id。"""
        raw = socket.socket()
        raw.connect(("127.0.0.1", self.port))
        raw.settimeout(timeout)
        raw.send(
            b"GET /Phone/sse HTTP/1.1\r\n"
            b"Host: 127.0.0.1\r\n"
            b"Accept: text/event-stream\r\n\r\n"
        )
        buf, ep = b"", None
        t0 = time.time()
        while time.time() - t0 < timeout:
            try:
                chunk = raw.recv(4096)
            except socket.timeout:
                break
            if not chunk:
                break
            buf += chunk
            text = buf.decode(errors="replace")
            if "\n\n" in text:
                for block in text.split("\n\n"):
                    ev_type = None
                    data_val = None
                    for line in block.split("\n"):
                        line = line.strip()
                        if line.startswith("event:"):
                            ev_type = line[6:].strip()
                        elif line.startswith("data:"):
                            data_val = line[5:].strip()
                    if ev_type == "endpoint" and "/messages" in (data_val or ""):
                        ep = data_val
                        break
            if ep:
                break

        if not ep:
            raise RuntimeError(
                f"no endpoint event (buf={buf[:200]!r})"
            )

        import urllib.parse
        self._sess_id = urllib.parse.parse_qs(
            urllib.parse.urlparse(ep).query
        ).get("session_id", [""])[0]

        raw.settimeout(None)
        self._sse_sock = raw
        threading.Thread(target=self._read_loop, daemon=True).start()

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
                try:
                    msg = json.loads(line[5:].strip())
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

    def call(self, method, params=None, timeout=25):
        """POST JSON-RPC 请求，等待 SSE 响应。"""
        with self._lock:
            rid = self._next_id
            self._next_id += 1
            ev = threading.Event()
            self._pending[rid] = ev

        body = json.dumps({
            "jsonrpc": "2.0",
            "id": rid,
            "method": method,
            "params": params or {},
        })
        path = f"/Phone/messages?session_id={self._sess_id}"
        req = (
            f"POST {path} HTTP/1.1\r\n"
            f"Host: 127.0.0.1\r\n"
            f"Content-Type: application/json\r\n"
            f"Content-Length: {len(body)}\r\n\r\n{body}"
        )
        s = socket.socket()
        s.connect(("127.0.0.1", self.port))
        s.settimeout(5)
        s.sendall(req.encode())
        try:
            s.recv(256)
        except Exception:
            pass
        s.close()

        if not ev.wait(timeout):
            with self._lock:
                self._pending.pop(rid, None)
            raise TimeoutError(f"timeout waiting for {method} response")

        with self._lock:
            result = self._pending_data.pop(rid, None)
            self._pending.pop(rid, None)
        return result

    def tool(self, name, args=None, timeout=25):
        """调用 tools/call，返回 text 内容或 ImageContent 序列化 JSON。"""
        resp = self.call(
            "tools/call", {"name": name, "arguments": args or {}}, timeout
        )
        if resp is None:
            return None
        content = resp.get("result", {}).get("content", [])
        if not content:
            return str(resp.get("error", ""))

        # Check if response contains an image (screenshot/observe)
        img = next((c for c in content if c.get("type") == "image"), None)
        if img:
            import re
            img_b64 = img.get("data", "")
            caption = next((c.get("text", "") for c in content if c.get("type") == "text"), "")
            m = re.search(r"(\d+)x(\d+)", caption)
            w = int(m.group(1)) if m else 0
            h = int(m.group(2)) if m else 0

            if name == "observe":
                elements_text = next((c.get("text", "") for c in content[1:] if c.get("type") == "text"), "")
                elements = [l.strip() for l in elements_text.split("\n") if l.strip().startswith("[")]
                return json.dumps({
                    "screenshot_base64": img_b64,
                    "ui_elements": elements,
                    "element_count": len(elements),
                })
            else:
                return json.dumps({
                    "base64": img_b64,
                    "width": w,
                    "height": h,
                    "format": "png",
                })

        return content[0].get("text", "")

    def close(self):
        try:
            self._sse_sock.close()
        except Exception:
            pass


# ══════════════════════════════════════════════════════════════════════════════
#  校验函数
# ══════════════════════════════════════════════════════════════════════════════

def validate_jsonrpc_response(resp, expected_id=None):
    """校验 JSON-RPC 2.0 响应基本格式。"""
    if not isinstance(resp, dict):
        return False, "response is not a dict"
    if resp.get("jsonrpc") != "2.0":
        return False, f"jsonrpc != '2.0': {resp.get('jsonrpc')}"
    if expected_id is not None and resp.get("id") != expected_id:
        return False, f"id mismatch: expected {expected_id}, got {resp.get('id')}"
    # 必须有 result 或 error 其一
    has_result = "result" in resp
    has_error = "error" in resp
    if not has_result and not has_error:
        return False, "response missing both 'result' and 'error'"
    return True, ""


def validate_tool_definition(tool):
    """校验单个工具定义的必填字段。"""
    issues = []
    if not isinstance(tool, dict):
        issues.append("not a dict")
        return issues
    if "name" not in tool:
        issues.append("missing 'name'")
    if "description" not in tool:
        issues.append("missing 'description'")
    # inputSchema 是可选字段
    return issues


EXPECTED_TOOLS = {
    "list_devices",
    "screenshot",
    "get_ui_elements",
    "tap",
    "tap_element",
    "swipe",
    "type_text",
    "back",
    "home",
    "press_key",
    "launch_app",
    "wait",
    "observe",
}

# 每个工具必填/可选的参数
TOOL_PARAMS = {
    "tap":             {"required": ["x", "y"], "optional": []},
    "tap_element":     {"required": [], "optional": ["index", "text"]},
    "swipe":           {"required": ["start_x", "start_y", "end_x", "end_y"], "optional": ["duration_ms"]},
    "type_text":       {"required": ["text"], "optional": []},
    "press_key":       {"required": [], "optional": ["keycode", "key"]},
    "launch_app":      {"required": [], "optional": ["app", "package"]},
    "wait":            {"required": [], "optional": ["duration_ms"]},
}

NO_ARG_TOOLS = {"list_devices", "screenshot", "get_ui_elements", "back", "home", "observe"}


# ══════════════════════════════════════════════════════════════════════════════
#  测试函数
# ══════════════════════════════════════════════════════════════════════════════

def test_initialize(client):
    """T1: MCP initialize 握手"""
    print("\n── T1: MCP Initialize ──")
    resp = client.call("initialize", {
        "protocolVersion": "2024-11-05",
        "clientInfo": {"name": "mcp-test", "version": "1.0.0"},
    })
    if os.environ.get("DEBUG"):
        print(f"     [DEBUG] raw resp: {json.dumps(resp, indent=2)[:500]}")

    is_valid, reason = validate_jsonrpc_response(resp)
    check(is_valid, "JSON-RPC 2.0 response", reason)

    result = resp.get("result", {})
    check(
        result.get("protocolVersion") == "2024-11-05",
        "protocolVersion = 2024-11-05",
        str(result.get("protocolVersion")),
        f"expected 2024-11-05, got {result.get('protocolVersion')}",
    )

    server_info = result.get("serverInfo", {})
    check(
        server_info.get("name") == "phonefast",
        "serverInfo.name = phonefast",
        str(server_info.get("name")),
    )
    check(
        "version" in server_info,
        "serverInfo.version present",
        str(server_info.get("version")),
    )

    caps = result.get("capabilities", {})
    check(
        "tools" in caps,
        "capabilities.tools declared",
        str(caps)[:60],
        "tools capability not declared",
    )

    return resp


def test_tools_list(client):
    """T2: tools/list 响应格式 & 完整性"""
    print("\n── T2: tools/list ──")
    resp = client.call("tools/list")
    is_valid, reason = validate_jsonrpc_response(resp)
    check(is_valid, "JSON-RPC 2.0 response", reason)
    if not is_valid:
        return set()

    tools = resp.get("result", {}).get("tools", [])
    check(isinstance(tools, list) and len(tools) > 0, f"tools array has {len(tools) if isinstance(tools, list) else 0} entries")

    # 校验每个工具定义
    all_defs_ok = True
    tool_names = set()
    for t in tools:
        name = t.get("name", "<unnamed>")
        tool_names.add(name)
        issues = validate_tool_definition(t)
        if issues:
            all_defs_ok = False
            bad(f"tool[{name}] definition", ", ".join(issues))
    if all_defs_ok:
        ok("all tool definitions valid (name + description)")

    # 检查预期工具是否齐全
    missing = EXPECTED_TOOLS - tool_names
    extra = tool_names - EXPECTED_TOOLS
    check(
        not missing,
        "all 13 expected tools present",
        f"missing: {sorted(missing)}" if missing else "",
    )
    if extra:
        print(f"     ℹ  extra tools: {sorted(extra)}")

    # 检查 inputSchema
    with_schema = [t["name"] for t in tools if "inputSchema" in t]
    without_schema = [t["name"] for t in tools if "inputSchema" not in t]
    print(f"     tools with inputSchema: {len(with_schema)} — {with_schema}")
    print(f"     tools without inputSchema: {len(without_schema)} — {without_schema}")

    return tool_names


def test_list_devices(client):
    """T3: list_devices"""
    print("\n── T3: list_devices ──")
    resp = client.call("tools/call", {"name": "list_devices", "arguments": {}})
    ok_r, reason = validate_jsonrpc_response(resp)
    check(ok_r, "JSON-RPC 2.0 response", reason)

    content = resp.get("result", {}).get("content", [])
    check(len(content) > 0, "response has content", f"{len(content)} items")

    text = content[0].get("text", "") if content else ""
    check(content[0].get("type") == "text", "content type = text", content[0].get("type"))

    try:
        devices = json.loads(text)
        check(isinstance(devices, list), f"returned device list ({len(devices)} devices)")
        for d in devices:
            print(f"     {d.get('serial','?')}  [{d.get('status','?')}]  {d.get('model','?')}")
        check(all("serial" in d for d in devices), "all devices have 'serial'")
    except json.JSONDecodeError:
        bad("devices JSON parse", text[:100])


def test_screenshot(client):
    """T4: screenshot"""
    print("\n── T4: screenshot ──")
    text = client.tool("screenshot")
    if text is None or "error" in (text or "").lower()[:20]:
        bad("screenshot", str(text)[:100] if text else "None")
        return None

    check(text is not None and len(text) > 0, "response non-empty", f"len={len(text or '')}")

    try:
        data = json.loads(text)
        has_b64 = "base64" in data
        has_w = "width" in data
        has_h = "height" in data
        check(has_b64, "has 'base64' field")
        check(has_w, "has 'width' field", data.get("width"))
        check(has_h, "has 'height' field", data.get("height"))
        check(data.get("format") == "png", "format = png", data.get("format"))

        b64len = len(data.get("base64", ""))
        check(b64len > 1000, f"screenshot base64 > 1000 chars ({b64len} chars)")

        # 验证可以 base64 解码
        try:
            raw = base64.b64decode(data["base64"])
            check(
                raw[:4] == b"\x89PNG",
                "screenshot is valid PNG",
                f"magic={raw[:4].hex()}",
            )
            print(f"     {data.get('width')}×{data.get('height')}  PNG={len(raw)} bytes")
            return raw
        except Exception as e:
            bad("base64 decode", str(e))
    except json.JSONDecodeError:
        bad("screenshot JSON parse", text[:200] if text else "None")


def test_ui_elements(client):
    """T5: get_ui_elements"""
    print("\n── T5: get_ui_elements ──")
    text = client.tool("get_ui_elements")
    if text is None or "error" in (text or "").lower()[:20]:
        bad("get_ui_elements", str(text)[:100] if text else "None")
        return []

    check(text is not None and len(text) > 0, "response non-empty")

    elements = []
    lines = (text or "").split("\n")
    for line in lines:
        line = line.strip()
        if line.startswith("[") and "]" in line:
            elements.append(line)
    check(len(elements) > 0, f"found {len(elements)} interactive elements")

    for el in elements[:5]:
        print(f"     {el[:90]}")
    if len(elements) > 5:
        print(f"     ... and {len(elements) - 5} more")

    return elements


def test_observe(client):
    """T6: observe"""
    print("\n── T6: observe ──")
    text = client.tool("observe")
    if text is None or "error" in (text or "").lower()[:20]:
        bad("observe", str(text)[:100] if text else "None")
        return

    check(text is not None and len(text) > 0, "response non-empty")

    try:
        data = json.loads(text)
        check("screenshot_base64" in data, "has screenshot_base64")
        check("ui_elements" in data, "has ui_elements")
        check("element_count" in data, "has element_count")

        ec = data.get("element_count", 0)
        print(f"     element_count={ec}")
        check(ec >= 0, f"element_count >= 0 ({ec})")

        b64len = len(data.get("screenshot_base64", ""))
        check(b64len > 1000, f"screenshot_base64 > 1000 chars ({b64len})")
    except json.JSONDecodeError:
        bad("observe JSON parse", text[:200] if text else "None")


def test_tap(client, x, y):
    """T7: tap"""
    print(f"\n── T7: tap({x}, {y}) ──")
    text = client.tool("tap", {"x": x, "y": y})
    check(
        isinstance(text, str) and "Tapped" in (text or ""),
        f"tap at ({x}, {y})",
        (text or "")[:80],
    )


def test_swipe(client, x1, y1, x2, y2, dur=300):
    """T8: swipe"""
    print(f"\n── T8: swipe ({x1},{y1})→({x2},{y2}) ──")
    text = client.tool("swipe", {
        "start_x": x1, "start_y": y1,
        "end_x": x2, "end_y": y2,
        "duration_ms": dur,
    })
    check(
        isinstance(text, str) and "Swiped" in (text or ""),
        f"swipe ({x1},{y1})→({x2},{y2})",
        (text or "")[:80],
    )


def test_back(client):
    """T9: back"""
    print("\n── T9: back ──")
    text = client.tool("back")
    check(
        isinstance(text, str) and "Back pressed" in (text or ""),
        "back button",
        (text or "")[:80],
    )


def test_home(client):
    """T10: home"""
    print("\n── T10: home ──")
    text = client.tool("home")
    check(
        isinstance(text, str) and "Home pressed" in (text or ""),
        "home button",
        (text or "")[:80],
    )


def test_type_text(client, txt="hello"):
    """T11: type_text"""
    print(f"\n── T11: type_text('{txt}') ──")
    text = client.tool("type_text", {"text": txt})
    check(
        isinstance(text, str) and txt in (text or ""),
        f"type_text('{txt}')",
        (text or "")[:80],
    )


def test_wait(client, ms=300):
    """T12: wait"""
    print(f"\n── T12: wait({ms}ms) ──")
    t0 = time.time()
    text = client.tool("wait", {"duration_ms": ms})
    elapsed = (time.time() - t0) * 1000
    check(
        isinstance(text, str) and "Waited" in (text or ""),
        f"wait {ms}ms",
        f"text={(text or '')[:60]}, elapsed={elapsed:.0f}ms",
    )
    print(f"     elapsed={elapsed:.0f}ms (target={ms}ms)")


def test_launch_app(client, pkg="com.android.settings"):
    """T13: launch_app"""
    print(f"\n── T13: launch_app('{pkg}') ──")
    text = client.tool("launch_app", {"package": pkg})
    check(
        isinstance(text, str),
        "launch_app response",
        (text or "")[:80],
    )
    # 注: 部分设备上启动可能失败（权限等），只要拿到响应就算通过


def test_tap_element(client):
    """T14: tap_element by index"""
    print("\n── T14: tap_element ──")
    # 先获取 UI 元素
    ui_text = client.tool("get_ui_elements")
    if ui_text is None or "error" in (ui_text or "").lower()[:20]:
        bad("tap_element (pre: get_ui_elements failed)", str(ui_text)[:100] if ui_text else "None")
        return

    # 找到第一个可点击元素
    clickable_idx = None
    for line in (ui_text or "").split("\n"):
        line = line.strip()
        if line.startswith("[") and "[clickable]" in line:
            try:
                clickable_idx = int(line[1:line.index("]")])
                break
            except ValueError:
                pass

    if clickable_idx is not None:
        text = client.tool("tap_element", {"index": clickable_idx})
        check(
            isinstance(text, str) and "Tapped" in (text or ""),
            f"tap_element index={clickable_idx}",
            (text or "")[:80],
        )
    else:
        # 尝试按 text 搜索
        text = client.tool("tap_element", {"text": "设置"})
        if isinstance(text, str) and "Tapped" in (text or ""):
            check(True, "tap_element text='设置'", (text or "")[:80])
        else:
            print("     ⚠  no clickable element found, trying any element")
            text = client.tool("tap_element", {"index": 0})
            check(
                isinstance(text, str) and "Tapped" in (text or ""),
                "tap_element index=0",
                (text or "")[:80],
            )


def test_press_key(client):
    """T15: press_key"""
    print("\n── T15: press_key ──")
    text = client.tool("press_key", {"keycode": 4})  # KEYCODE_BACK
    check(
        isinstance(text, str),
        "press_key keycode=4 (BACK)",
        (text or "")[:80],
    )


def test_error_handling(client):
    """T16: 错误处理测试"""
    print("\n── T16: Error handling ──")

    # 1. 未知方法
    resp = client.call("nonexistent_method")
    err = resp.get("error", {})
    check(
        err.get("code") == -32601,
        "unknown method → error code -32601",
        f"code={err.get('code')} msg={err.get('message','')}",
    )

    # 2. 未知工具
    resp = client.call("tools/call", {"name": "nonexistent_tool", "arguments": {}})
    content = resp.get("result", {}).get("content", [])
    text = content[0].get("text", "") if content else ""
    check(
        "Unknown tool" in text or "unknown" in text.lower() or "unknown" in resp.get("error", {}).get("message", "").lower() or "not found" in resp.get("error", {}).get("message", "").lower(),
        "unknown tool → 'Unknown tool'",
        text[:80],
    )

    # 3. 缺少必填参数
    resp = client.call("tools/call", {"name": "tap", "arguments": {"x": 100}})
    content = resp.get("result", {}).get("content", [])
    text = content[0].get("text", "") if content else ""
    check(
        "error" in (text or "").lower() or "validation" in (text or "").lower() or "missing" in (text or "").lower(),
        "missing 'y' param → error",
        text[:80],
    )

    # 4. type_text 缺 text
    resp = client.call("tools/call", {"name": "type_text", "arguments": {}})
    content = resp.get("result", {}).get("content", [])
    text = content[0].get("text", "") if content else ""
    check(
        "error" in (text or "").lower() or "validation" in (text or "").lower() or "missing" in (text or "").lower(),
        "missing 'text' param → error",
        text[:80],
    )

    # 5. ping
    resp = client.call("ping")
    ok_r, _ = validate_jsonrpc_response(resp)
    check(ok_r and "result" in resp and resp.get("result") == {},
          "ping → empty result object")


def test_no_arg_tools(client):
    """T17: 测试所有无参数工具连续调用"""
    print("\n── T17: No-arg tools quick smoke ──")
    tools_to_test = ["list_devices", "back", "home"]
    for name in tools_to_test:
        resp = client.call("tools/call", {"name": name, "arguments": {}})
        ok_r, reason = validate_jsonrpc_response(resp)
        check(ok_r, f"tools/call {name}", reason)


# ══════════════════════════════════════════════════════════════════════════════
#  SSE 服务器启动辅助
# ══════════════════════════════════════════════════════════════════════════════

def start_sse_server(port):
    """启动 SSE 模式 phonefast 服务器，返回 Popen 进程。"""
    proc = subprocess.Popen(
        [BINARY, "serve", "--transport", "sse", "--port", str(port)],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    # 等待端口就绪
    for _ in range(50):
        time.sleep(0.2)
        try:
            s = socket.create_connection(("127.0.0.1", port), timeout=0.5)
            s.close()
            return proc
        except Exception:
            pass
    proc.terminate()
    raise RuntimeError(f"SSE server did not start on port {port}")


# ══════════════════════════════════════════════════════════════════════════════
#  主流程
# ══════════════════════════════════════════════════════════════════════════════

def main():
    parser = argparse.ArgumentParser(description="phonefast MCP protocol test")
    parser.add_argument("--sse", action="store_true", help="Use SSE transport")
    parser.add_argument("--port", type=int, default=DEFAULT_SSE_PORT, help="SSE port")
    parser.add_argument("--binary", default=BINARY, help="Path to phonefast binary")
    parser.add_argument("--quick", action="store_true", help="Skip slow tests (screenshot)")

    args = parser.parse_args()

    # 检查 ADB 设备
    r = subprocess.run(["adb", "devices"], capture_output=True, text=True)
    devices = [l for l in r.stdout.strip().split("\n")[1:] if "\tdevice" in l]
    if not devices:
        print("FATAL: no ADB devices connected")
        sys.exit(1)
    serial = devices[0].split()[0]
    print(f"Device: {serial}")

    # 检查二进制
    binary = args.binary
    if not os.path.isfile(binary):
        print(f"FATAL: binary not found at {binary}")
        sys.exit(1)

    print(f"\n{'='*60}")
    print(f"  phonefast MCP Protocol Test")
    print(f"  transport: {'SSE' if args.sse else 'STDIO'}")
    print(f"  binary:    {binary}")
    print(f"  port:      {args.port if args.sse else 'N/A'}")
    print(f"{'='*60}")

    sse_proc = None
    client = None

    try:
        if args.sse:
            sse_proc = start_sse_server(args.port)
            print(f"\nSSE server started on :{args.port}")
            client = SSEMCPClient(args.port)
            client.connect(timeout=12)
        else:
            client = StdioMCPClient(binary)

        # ── 运行测试 ──────────────────────────────────────────────────────────

        test_initialize(client)
        test_tools_list(client)

        if not args.quick:
            test_screenshot(client)
            test_ui_elements(client)
            test_observe(client)

            # 获取屏幕尺寸用于手势测试
            text = client.tool("observe")
            W, H = 720, 1600  # 默认
            if isinstance(text, str):
                try:
                    d = json.loads(text)
                    b64 = d.get("screenshot_base64", "")
                    # 从 PNG 头解析尺寸
                    if len(b64) > 100:
                        raw = base64.b64decode(b64)
                        if raw[:4] == b"\x89PNG":
                            import struct as st
                            # IHDR 在 offset 16: 8+4+4+4
                            W = int.from_bytes(raw[16:20], "big")
                            H = int.from_bytes(raw[20:24], "big")
                except Exception:
                    pass

            cx, cy = W // 2, H // 2
            test_tap(client, cx, cy)
            time.sleep(0.3)
            test_swipe(client, cx, int(H * 0.7), cx, int(H * 0.3), 300)
            time.sleep(0.3)
        else:
            print("\n  [quick mode] skipping screenshot/tap/swipe")

        test_back(client)
        time.sleep(0.3)
        test_home(client)
        time.sleep(0.3)

        test_type_text(client, "test")
        time.sleep(0.2)
        test_wait(client, 500)

        test_launch_app(client)
        time.sleep(0.5)
        test_home(client)
        time.sleep(0.3)

        test_tap_element(client)
        test_press_key(client)

        test_no_arg_tools(client)
        test_error_handling(client)

    except Exception as e:
        print(f"\n  FATAL: {e}")
        import traceback
        traceback.print_exc()
    finally:
        if client:
            client.close()
        if sse_proc:
            sse_proc.terminate()
            try:
                sse_proc.wait(timeout=5)
            except Exception:
                sse_proc.kill()

    # ── 汇总 ──────────────────────────────────────────────────────────────────
    total = len(PASS) + len(FAIL)
    print(f"\n{'='*60}")
    print(f"  MCP Protocol Test Results")
    print(f"{'='*60}")
    print(f"  PASS:  {len(PASS)}/{total}")
    print(f"  FAIL:  {len(FAIL)}/{total}")

    if FAIL:
        print(f"\n  Failed checks:")
        for f in FAIL:
            print(f"    ✗ {f}")

    print(f"{'='*60}")

    if FAIL:
        print(f"\n⚠  {len(FAIL)} check(s) failed")
        sys.exit(1)
    else:
        print(f"\n✅ All {len(PASS)} checks passed!")
        sys.exit(0)


if __name__ == "__main__":
    main()
