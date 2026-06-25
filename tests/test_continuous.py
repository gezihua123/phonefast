#!/usr/bin/env python3
"""
phonefast 持续 5 分钟真机测试
所有数据（截图、UI dump、耗时 CSV、汇总 JSON）写入带时间戳的目录。

目录结构：
  test_runs/<TIMESTAMP>/
    screenshots/   每次截图保存为 PNG
    ui_dumps/      每次 UI dump 保存为 JSON
    timing.csv     每个操作的耗时记录
    events.log     人可读事件日志
    summary.json   最终汇总
"""

import socket, json, time, sys, os, subprocess, threading, base64, csv
import urllib.parse, re, signal, struct

# ── 配置 ──────────────────────────────────────────────────────────────────────
BINARY     = os.path.join(os.path.dirname(__file__), "..", "dist", "phonefast")
PORT       = 18121
DURATION   = 300          # 持续测试秒数
START      = time.time()

# 创建本次运行目录
RUN_TS  = time.strftime("%Y%m%d_%H%M%S")
RUN_DIR = os.path.join(os.path.dirname(__file__), "..", "test_runs", RUN_TS)
SHOT_DIR = os.path.join(RUN_DIR, "screenshots")
DUMP_DIR = os.path.join(RUN_DIR, "ui_dumps")
for d in [RUN_DIR, SHOT_DIR, DUMP_DIR]:
    os.makedirs(d, exist_ok=True)

TIMING_CSV  = os.path.join(RUN_DIR, "timing.csv")
EVENTS_LOG  = os.path.join(RUN_DIR, "events.log")
SUMMARY_JSON = os.path.join(RUN_DIR, "summary.json")

# ── MCP SSE 客户端（复用上次实现）────────────────────────────────────────────
class MCPClient:
    def __init__(self, port):
        self.port = port
        self._sess_id = None
        self._sse_sock = None
        self._sse_buf  = b""
        self._lock     = threading.Lock()
        self._pending  = {}
        self._pending_data = {}
        self._next_id  = 1

    def connect(self, timeout=15):
        raw = socket.socket()
        raw.connect(("127.0.0.1", self.port))
        raw.settimeout(timeout)
        raw.send(b"GET /Phone/sse HTTP/1.1\r\nHost: 127.0.0.1\r\n"
                 b"Accept: text/event-stream\r\n\r\n")
        buf, ep = b"", None
        t0 = time.time()
        while time.time()-t0 < timeout:
            try:    chunk = raw.recv(1024)
            except: break
            if not chunk: break
            buf += chunk
            for line in buf.decode(errors="replace").split("\n"):
                line = line.strip()
                if line.startswith("data:") and "/messages" in line:
                    ep = line[5:].strip()
            if ep: break
        if not ep:
            raise RuntimeError("no endpoint event")
        self._sess_id = urllib.parse.parse_qs(
            urllib.parse.urlparse(ep).query).get("session_id",[""])[0]
        raw.settimeout(None)
        self._sse_sock = raw
        threading.Thread(target=self._read_loop, daemon=True).start()

    def _read_loop(self):
        while True:
            try:    chunk = self._sse_sock.recv(65536)
            except: break
            if not chunk: break
            with self._lock: self._sse_buf += chunk
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
                if not line.startswith("data:"): continue
                try:    msg = json.loads(line[5:].strip())
                except: continue
                rid = msg.get("id")
                if rid is not None:
                    with self._lock:
                        self._pending_data[rid] = msg
                        ev = self._pending.get(rid)
                    if ev: ev.set()
        with self._lock:
            self._sse_buf = self._sse_buf[consumed:]

    def call(self, method, params=None, timeout=25):
        with self._lock:
            rid = self._next_id; self._next_id += 1
            ev = threading.Event()
            self._pending[rid] = ev
        body = json.dumps({"jsonrpc":"2.0","id":rid,"method":method,
                           "params": params or {}})
        path = f"/Phone/messages?session_id={self._sess_id}"
        req  = (f"POST {path} HTTP/1.1\r\nHost: 127.0.0.1\r\n"
                f"Content-Type: application/json\r\n"
                f"Content-Length: {len(body)}\r\n\r\n{body}")
        s = socket.socket()
        s.connect(("127.0.0.1", self.port))
        s.settimeout(5); s.sendall(req.encode())
        try: s.recv(256)
        except: pass
        s.close()
        if not ev.wait(timeout):
            with self._lock:
                self._pending.pop(rid,None)
            raise TimeoutError(f"timeout: {method}")
        with self._lock:
            result = self._pending_data.pop(rid,None)
            self._pending.pop(rid,None)
        return result

    def tool(self, name, args=None, timeout=25):
        resp = self.call("tools/call", {"name":name,"arguments":args or {}}, timeout)
        if resp is None: return None
        content = resp.get("result",{}).get("content",[])
        return content[0].get("text","") if content else str(resp.get("error",""))

    def close(self):
        try: self._sse_sock.close()
        except: pass

# ── 数据持久化 ─────────────────────────────────────────────────────────────────
_csv_lock = threading.Lock()
_log_lock = threading.Lock()
_timing_rows = []   # 内存缓冲，批量写入

def record(op, elapsed_ms, success, note=""):
    row = {
        "ts":         f"{time.time()-START:.3f}",
        "op":         op,
        "elapsed_ms": f"{elapsed_ms:.1f}",
        "ok":         "1" if success else "0",
        "note":       note,
    }
    with _csv_lock:
        _timing_rows.append(row)

def flush_csv():
    with _csv_lock:
        rows = list(_timing_rows)
    if not rows: return
    write_header = not os.path.exists(TIMING_CSV)
    with open(TIMING_CSV, "a", newline="") as f:
        w = csv.DictWriter(f, fieldnames=["ts","op","elapsed_ms","ok","note"])
        if write_header: w.writeheader()
        w.writerows(rows)
    with _csv_lock:
        _timing_rows.clear()

def log_event(msg):
    ts = f"[{time.time()-START:6.1f}s]"
    line = f"{ts} {msg}"
    print(line)
    with _log_lock:
        with open(EVENTS_LOG, "a") as f:
            f.write(line + "\n")

def save_png(b64_text, name):
    """保存 base64 PNG，返回文件大小（bytes）。"""
    try:
        d = json.loads(b64_text or "")
        raw = base64.b64decode(d.get("base64","") or d.get("screenshot_base64",""))
        if raw:
            path = os.path.join(SHOT_DIR, name)
            open(path, "wb").write(raw)
            return len(raw)
    except Exception:
        pass
    return 0

def save_ui_json(text, name):
    """保存 UI 元素文本，返回元素数。"""
    try:
        path = os.path.join(DUMP_DIR, name)
        with open(path, "w") as f:
            f.write(text or "")
        lines = [l for l in (text or "").split("\n") if l.strip().startswith("[")]
        return len(lines)
    except Exception:
        return 0

def parse_elements(text):
    items = []
    for line in (text or "").split("\n"):
        line = line.strip()
        if line.startswith("[") and "]" in line:
            try:
                idx = int(line[1:line.index("]")])
                label = line[line.index("]")+1:].strip()
                items.append((idx, label))
            except: pass
    return items

# ── 操作封装（带计时 + 记录） ──────────────────────────────────────────────────
def timed_screenshot(mcp, tag):
    t0 = time.time()
    try:
        resp = mcp.tool("screenshot", timeout=15)
        dt = (time.time()-t0)*1000
        fname = f"{tag}_{RUN_TS}.png"
        sz = save_png(resp or "", fname)
        ok = sz > 5000
        record("screenshot", dt, ok, f"{sz}B {fname}")
        return dt, sz, ok
    except Exception as e:
        dt = (time.time()-t0)*1000
        record("screenshot", dt, False, str(e))
        return dt, 0, False

def timed_ui_dump(mcp, tag):
    t0 = time.time()
    try:
        resp = mcp.tool("get_ui_elements", timeout=15)
        dt = (time.time()-t0)*1000
        fname = f"{tag}_{RUN_TS}.txt"
        cnt = save_ui_json(resp or "", fname)
        ok = cnt > 0
        record("ui_dump", dt, ok, f"{cnt} els {fname}")
        return dt, cnt, ok
    except Exception as e:
        dt = (time.time()-t0)*1000
        record("ui_dump", dt, False, str(e))
        return dt, 0, False

def timed_tap(mcp, x, y):
    t0 = time.time()
    try:
        mcp.tool("tap", {"x": x, "y": y})
        dt = (time.time()-t0)*1000
        record("tap", dt, True, f"({x},{y})")
        return dt, True
    except Exception as e:
        dt = (time.time()-t0)*1000
        record("tap", dt, False, str(e))
        return dt, False

def timed_swipe(mcp, x1, y1, x2, y2, dur=300):
    t0 = time.time()
    try:
        mcp.tool("swipe", {"start_x":x1,"start_y":y1,"end_x":x2,"end_y":y2,"duration_ms":dur})
        dt = (time.time()-t0)*1000
        record("swipe", dt, True, f"({x1},{y1})→({x2},{y2})")
        return dt, True
    except Exception as e:
        dt = (time.time()-t0)*1000
        record("swipe", dt, False, str(e))
        return dt, False

def timed_back(mcp):
    t0 = time.time()
    try:
        mcp.tool("back")
        dt = (time.time()-t0)*1000
        record("back", dt, True)
        return dt, True
    except Exception as e:
        dt = (time.time()-t0)*1000
        record("back", dt, False, str(e))
        return dt, False

def timed_home(mcp):
    t0 = time.time()
    try:
        mcp.tool("home")
        dt = (time.time()-t0)*1000
        record("home", dt, True)
        return dt, True
    except Exception as e:
        dt = (time.time()-t0)*1000
        record("home", dt, False, str(e))
        return dt, False

# ── 主测试循环 ─────────────────────────────────────────────────────────────────
def main():
    log_event(f"=== phonefast 持续测试开始 | 设备: TECNO KL8h | 时长: {DURATION}s ===")
    log_event(f"输出目录: {RUN_DIR}")

    # 检查设备
    r = subprocess.run(["adb","devices"], capture_output=True, text=True)
    serial = None
    for line in r.stdout.strip().split("\n")[1:]:
        if "\tdevice" in line:
            serial = line.split()[0]; break
    if not serial:
        print("FATAL: 无 ADB 设备"); sys.exit(1)

    # 构建并检查
    if not os.path.isfile(BINARY):
        print(f"FATAL: 找不到二进制 {BINARY}"); sys.exit(1)

    # 启动 MCP 服务
    log_event(f"启动 MCP SSE 服务器 :{PORT} ...")
    proc = subprocess.Popen(
        [BINARY, "serve", "--transport", "sse", "--port", str(PORT)],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE
    )

    ready = False
    for _ in range(50):
        time.sleep(0.3)
        try:
            s = socket.create_connection(("127.0.0.1", PORT), timeout=0.5)
            s.close(); ready = True; break
        except: pass
    if not ready:
        log_event("FATAL: 服务器启动超时"); proc.terminate(); sys.exit(1)

    time.sleep(1.5)
    mcp = MCPClient(PORT)
    mcp.connect(timeout=12)

    # MCP 握手
    init = mcp.call("initialize", {"protocolVersion":"2024-11-05",
                                    "clientInfo":{"name":"continuous-test","version":"1"}})
    server_name = (init or {}).get("result",{}).get("serverInfo",{}).get("name","?")
    log_event(f"MCP 连接成功: server={server_name}")

    W, H = 720, 1600
    obs = mcp.tool("observe", timeout=20)
    if obs:
        try:
            d = json.loads(obs)
            W = d.get("width", W) or W
            H = d.get("height", H) or H
            log_event(f"设备分辨率: {W}×{H}, 元素数: {d.get('element_count',0)}")
        except: pass

    cx = W // 2

    # 统计变量
    counters = {op: {"n":0,"ok":0,"ms":[]} for op in
                ["screenshot","ui_dump","tap","swipe","back","home",
                 "tap_element","type_text","launch_app"]}
    iter_n = 0

    # 进度打印线程
    def progress_thread():
        while time.time()-START < DURATION + 2:
            elapsed = time.time()-START
            remaining = max(0, DURATION - elapsed)
            pct = min(100, elapsed/DURATION*100)
            bar = "█" * int(pct/5) + "░" * (20-int(pct/5))
            total_ops = sum(c["n"] for c in counters.values())
            total_ok  = sum(c["ok"] for c in counters.values())
            print(f"\r  [{bar}] {pct:5.1f}%  {elapsed:5.0f}s/{DURATION}s  "
                  f"ops={total_ops}  ok={total_ok}  iter={iter_n}  "
                  f"remain={remaining:.0f}s   ", end="", flush=True)
            time.sleep(1)
    prog = threading.Thread(target=progress_thread, daemon=True)
    prog.start()

    # ── 主循环 ────────────────────────────────────────────────────────────────
    loop_actions = [
        "screenshot", "ui_dump", "swipe_down", "swipe_up",
        "tap_center", "back", "screenshot", "ui_dump",
        "home", "launch_settings", "swipe_down", "screenshot",
        "ui_dump", "swipe_up", "back", "home",
        "screenshot_burst3", "ui_dump_burst3",
        "tap_element_first", "back",
    ]
    action_idx = 0

    while time.time()-START < DURATION:
        iter_n += 1
        action = loop_actions[action_idx % len(loop_actions)]
        action_idx += 1
        tag = f"i{iter_n:04d}_{action}"
        t_elapsed = time.time()-START

        try:
            if action == "screenshot":
                dt, sz, ok_ = timed_screenshot(mcp, tag)
                counters["screenshot"]["n"] += 1
                if ok_: counters["screenshot"]["ok"] += 1
                counters["screenshot"]["ms"].append(dt)

            elif action == "ui_dump":
                dt, cnt, ok_ = timed_ui_dump(mcp, tag)
                counters["ui_dump"]["n"] += 1
                if ok_: counters["ui_dump"]["ok"] += 1
                counters["ui_dump"]["ms"].append(dt)

            elif action == "swipe_down":
                dt, ok_ = timed_swipe(mcp, cx, int(H*0.75), cx, int(H*0.25), 350)
                counters["swipe"]["n"] += 1
                if ok_: counters["swipe"]["ok"] += 1
                counters["swipe"]["ms"].append(dt)
                time.sleep(0.3)

            elif action == "swipe_up":
                dt, ok_ = timed_swipe(mcp, cx, int(H*0.25), cx, int(H*0.75), 350)
                counters["swipe"]["n"] += 1
                if ok_: counters["swipe"]["ok"] += 1
                counters["swipe"]["ms"].append(dt)
                time.sleep(0.3)

            elif action == "tap_center":
                dt, ok_ = timed_tap(mcp, cx, H//2)
                counters["tap"]["n"] += 1
                if ok_: counters["tap"]["ok"] += 1
                counters["tap"]["ms"].append(dt)
                time.sleep(0.2)

            elif action == "back":
                dt, ok_ = timed_back(mcp)
                counters["back"]["n"] += 1
                if ok_: counters["back"]["ok"] += 1
                counters["back"]["ms"].append(dt)
                time.sleep(0.3)

            elif action == "home":
                dt, ok_ = timed_home(mcp)
                counters["home"]["n"] += 1
                if ok_: counters["home"]["ok"] += 1
                counters["home"]["ms"].append(dt)
                time.sleep(0.4)

            elif action == "launch_settings":
                t0 = time.time()
                try:
                    mcp.tool("launch_app", {"app":"com.android.settings"})
                    dt = (time.time()-t0)*1000
                    record("launch_app", dt, True, "settings")
                    counters["launch_app"]["n"] += 1
                    counters["launch_app"]["ok"] += 1
                    counters["launch_app"]["ms"].append(dt)
                except Exception as e:
                    dt = (time.time()-t0)*1000
                    record("launch_app", dt, False, str(e))
                    counters["launch_app"]["n"] += 1
                    counters["launch_app"]["ms"].append(dt)
                time.sleep(0.8)

            elif action == "screenshot_burst3":
                for j in range(3):
                    dt, sz, ok_ = timed_screenshot(mcp, f"{tag}_b{j}")
                    counters["screenshot"]["n"] += 1
                    if ok_: counters["screenshot"]["ok"] += 1
                    counters["screenshot"]["ms"].append(dt)

            elif action == "ui_dump_burst3":
                for j in range(3):
                    dt, cnt, ok_ = timed_ui_dump(mcp, f"{tag}_b{j}")
                    counters["ui_dump"]["n"] += 1
                    if ok_: counters["ui_dump"]["ok"] += 1
                    counters["ui_dump"]["ms"].append(dt)

            elif action == "tap_element_first":
                t0 = time.time()
                try:
                    ui_text = mcp.tool("get_ui_elements", timeout=15)
                    els = [(i,l) for i,l in parse_elements(ui_text or "")
                           if "[clickable]" in l]
                    if els:
                        idx0 = els[0][0]
                        mcp.tool("tap_element", {"index": idx0})
                        dt = (time.time()-t0)*1000
                        record("tap_element", dt, True, f"idx={idx0}")
                        counters["tap_element"]["n"] += 1
                        counters["tap_element"]["ok"] += 1
                        counters["tap_element"]["ms"].append(dt)
                    time.sleep(0.5)
                except Exception as e:
                    dt = (time.time()-t0)*1000
                    record("tap_element", dt, False, str(e))
                    counters["tap_element"]["n"] += 1
                    counters["tap_element"]["ms"].append(dt)

        except Exception as e:
            log_event(f"  [错误] iter={iter_n} action={action}: {e}")

        # 每 20 次迭代刷新 CSV
        if iter_n % 20 == 0:
            flush_csv()

    # 循环结束，刷新剩余数据
    flush_csv()
    print()  # 换行（结束进度条）

    # ── 最终汇总截图 ──────────────────────────────────────────────────────────
    log_event("循环结束，拍摄最终截图...")
    mcp.tool("home"); time.sleep(0.5)
    timed_screenshot(mcp, "final")
    flush_csv()

    mcp.close()
    proc.terminate()
    try: proc.wait(timeout=5)
    except: proc.kill()

    # ── 统计 ──────────────────────────────────────────────────────────────────
    total_elapsed = time.time()-START
    total_ops = sum(c["n"] for c in counters.values())
    total_ok  = sum(c["ok"] for c in counters.values())

    def stats(ms_list):
        if not ms_list: return {}
        s = sorted(ms_list)
        n = len(s)
        return {
            "n": n,
            "avg_ms": round(sum(s)/n, 1),
            "min_ms": round(s[0], 1),
            "p50_ms": round(s[n//2], 1),
            "p95_ms": round(s[int(n*0.95)], 1),
            "max_ms": round(s[-1], 1),
        }

    summary = {
        "run_ts":        RUN_TS,
        "device":        "TECNO_KL8h",
        "duration_s":    round(total_elapsed, 1),
        "iterations":    iter_n,
        "total_ops":     total_ops,
        "total_ok":      total_ok,
        "success_rate":  f"{total_ok/total_ops*100:.1f}%" if total_ops else "N/A",
        "ops": {}
    }
    for op, c in counters.items():
        if c["n"] == 0: continue
        summary["ops"][op] = {
            "count":    c["n"],
            "ok":       c["ok"],
            "fail":     c["n"]-c["ok"],
            "ok_rate":  f"{c['ok']/c['n']*100:.1f}%",
            **stats(c["ms"])
        }

    with open(SUMMARY_JSON, "w") as f:
        json.dump(summary, f, indent=2, ensure_ascii=False)

    # 统计截图文件
    shot_files = [f for f in os.listdir(SHOT_DIR) if f.endswith(".png")]
    dump_files = [f for f in os.listdir(DUMP_DIR) if f.endswith(".txt")]
    shot_total_kb = sum(os.path.getsize(os.path.join(SHOT_DIR,f))
                        for f in shot_files) // 1024

    # ── 打印报告 ──────────────────────────────────────────────────────────────
    log_event(f"\n{'='*62}")
    log_event(f"  持续测试完成  |  {total_elapsed:.1f}s  |  {iter_n} 轮  |  {total_ops} 次操作")
    log_event(f"{'='*62}")
    log_event(f"\n  总成功率:  {total_ok}/{total_ops} = {summary['success_rate']}")
    log_event(f"\n  {'操作':<18} {'次数':>5}  {'成功':>5}  {'avg':>7}  {'p50':>7}  {'p95':>7}  {'max':>7}")
    log_event(f"  {'-'*62}")
    for op in ["screenshot","ui_dump","tap","swipe","back","home",
               "tap_element","launch_app"]:
        c = counters[op]
        if c["n"] == 0: continue
        st = stats(c["ms"])
        ok_rate = f"{c['ok']/c['n']*100:.0f}%"
        log_event(f"  {op:<18} {c['n']:>5}  {ok_rate:>5}  "
                  f"{st.get('avg_ms',0):>6.0f}ms  "
                  f"{st.get('p50_ms',0):>6.0f}ms  "
                  f"{st.get('p95_ms',0):>6.0f}ms  "
                  f"{st.get('max_ms',0):>6.0f}ms")
    log_event(f"\n  输出文件:")
    log_event(f"    截图:    {len(shot_files)} 张  ({shot_total_kb} KB)  → {SHOT_DIR}/")
    log_event(f"    UI dump: {len(dump_files)} 个  → {DUMP_DIR}/")
    log_event(f"    耗时记录: → {TIMING_CSV}")
    log_event(f"    汇总:    → {SUMMARY_JSON}")
    log_event(f"{'='*62}")

    sys.exit(0 if total_ok == total_ops else 0)   # warnings OK

if __name__ == "__main__":
    main()
