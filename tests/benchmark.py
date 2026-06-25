#!/usr/bin/env python3
"""
phonefast MCP 工具 Benchmark — 延迟、吞吐、可靠性

用法:
  python3 benchmark.py                      # STDIO 模式，默认 10 轮
  python3 benchmark.py --sse --port 18019   # SSE 模式
  python3 benchmark.py --rounds 30          # 30 轮
  python3 benchmark.py --quick              # 快速模式（3 轮）
  python3 benchmark.py --output report.json # 输出 JSON 报告

测试维度:
  1. 冷启动延迟 (进程启动 → 首次工具调用成功)
  2. 各工具单次调用延迟 (p50 / p95 / p99 / avg)
  3. 吞吐 (连续调用 20 次的 QPS)
  4. 错误率
"""

import json, subprocess, socket, time, sys, os, threading, argparse, base64, statistics, math
from dataclasses import dataclass, field
from typing import Optional
from collections import defaultdict

# ── 配置 ────────────────────────────────────────────────────────────────────────
BINARY = os.path.join(os.path.dirname(__file__), "..", "dist", "phonefast")
DEFAULT_SSE_PORT = 18019
DEFAULT_ROUNDS = 10
QUICK_ROUNDS = 3

# 基准工具列表（按类别分组）
SENSOR_TOOLS = ["list_devices", "screenshot", "get_ui_elements", "observe"]
ACTION_TOOLS = ["tap", "swipe", "type_text", "back", "home", "press_key", "launch_app", "wait"]
ALL_TOOLS = SENSOR_TOOLS + ACTION_TOOLS


# ══════════════════════════════════════════════════════════════════════════════════
#  MCP 客户端基类
# ══════════════════════════════════════════════════════════════════════════════════

class StdioMCPClient:
    """通过子进程 stdin/stdout 与 MCP 服务器通信，支持逐条读取响应。"""

    def __init__(self, binary_path, startup_wait=6):
        self._next_id = 1
        self._lock = threading.Lock()
        cmd = [binary_path, "serve", "--transport", "stdio"]
        self._proc = subprocess.Popen(
            cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL, text=False,
        )
        self._started_at = time.time()
        # 等待服务器就绪（后台连接设备）
        time.sleep(startup_wait)

    def call(self, method, params=None, timeout=60) -> dict:
        with self._lock:
            rid = self._next_id
            self._next_id += 1
        req = json.dumps({
            "jsonrpc": "2.0", "id": rid, "method": method,
            "params": params or {},
        }) + "\n"
        try:
            self._proc.stdin.write(req.encode("utf-8"))
            self._proc.stdin.flush()
        except BrokenPipeError:
            return {"error": {"code": -1, "message": "broken pipe"}}
        try:
            line = self._proc.stdout.readline()
            return json.loads(line) if line else {"error": {"code": -1, "message": "no response"}}
        except json.JSONDecodeError as e:
            return {"error": {"code": -1, "message": f"JSON decode: {e}"}}

    def tool(self, name, args=None, timeout=60) -> Optional[str]:
        resp = self.call("tools/call", {"name": name, "arguments": args or {}}, timeout)
        content = resp.get("result", {}).get("content", [])
        return content[0].get("text", "") if content else str(resp.get("error", ""))

    def ready(self) -> bool:
        """快速检查设备是否就绪。"""
        text = self.tool("list_devices")
        try:
            return isinstance(json.loads(text or "[]"), list) and len(json.loads(text)) > 0
        except Exception:
            return False

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
        raw = socket.socket()
        raw.connect(("127.0.0.1", self.port))
        raw.settimeout(timeout)
        raw.send(
            b"GET /Phone/sse HTTP/1.1\r\n"
            b"Host: 127.0.0.1\r\n"
            b"Accept: text/event-stream\r\n\r\n"
        )
        buf, t0 = b"", time.time()
        ep = None
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
                    ev_type = data_val = None
                    for line in block.split("\n"):
                        line = line.strip()
                        if line.startswith("event:"):
                            ev_type = line[6:].strip()
                        elif line.startswith("data:"):
                            data_val = line[5:].strip()
                    if ev_type == "endpoint" and data_val and "/messages" in data_val:
                        ep = data_val
                        break
            if ep:
                break
        if not ep:
            raise RuntimeError(f"no endpoint event (buf={buf[:200]!r})")
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

    def call(self, method, params=None, timeout=60) -> dict:
        with self._lock:
            rid = self._next_id
            self._next_id += 1
            ev = threading.Event()
            self._pending[rid] = ev
        body = json.dumps({
            "jsonrpc": "2.0", "id": rid, "method": method,
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
        s.settimeout(5)
        s.connect(("127.0.0.1", self.port))
        s.sendall(req.encode())
        try:
            s.recv(256)
        except Exception:
            pass
        s.close()
        if not ev.wait(timeout):
            with self._lock:
                self._pending.pop(rid, None)
            return {"error": {"code": -1, "message": f"timeout waiting for {method}"}}
        with self._lock:
            result = self._pending_data.pop(rid, None)
            self._pending.pop(rid, None)
        return result or {"error": {"code": -1, "message": "no response"}}

    def tool(self, name, args=None, timeout=60) -> Optional[str]:
        resp = self.call("tools/call", {"name": name, "arguments": args or {}}, timeout)
        content = resp.get("result", {}).get("content", [])
        return content[0].get("text", "") if content else str(resp.get("error", ""))

    def close(self):
        try:
            self._sse_sock.close()
        except Exception:
            pass


# ══════════════════════════════════════════════════════════════════════════════════
#  统计工具
# ══════════════════════════════════════════════════════════════════════════════════

def percentile(data, p):
    """计算百分位数 (线性插值)。"""
    if not data:
        return 0
    s = sorted(data)
    k = (len(s) - 1) * p / 100.0
    f = int(math.floor(k))
    c = int(math.ceil(k))
    if f == c:
        return s[int(k)]
    return s[f] * (c - k) + s[c] * (k - f)


def stats(data):
    """返回 latencies 的统计摘要。"""
    if not data:
        return {"count": 0, "avg": 0, "min": 0, "max": 0, "p50": 0, "p95": 0, "p99": 0}
    return {
        "count": len(data),
        "avg": round(statistics.mean(data), 1),
        "min": round(min(data), 1),
        "max": round(max(data), 1),
        "p50": round(percentile(data, 50), 1),
        "p95": round(percentile(data, 95), 1),
        "p99": round(percentile(data, 99), 1),
        "std": round(statistics.stdev(data) if len(data) > 1 else 0, 1),
    }


def format_ms(ms):
    """格式化毫秒值。"""
    if ms < 10:
        return f"{ms:.1f}ms"
    elif ms < 100:
        return f"{ms:.0f}ms"
    else:
        return f"{ms:.0f}ms"


# ══════════════════════════════════════════════════════════════════════════════════
#  Benchmark 核心
# ══════════════════════════════════════════════════════════════════════════════════

@dataclass
class ToolResult:
    name: str
    category: str  # "sensor" | "action"
    latencies: list = field(default_factory=list)
    errors: int = 0
    data_sizes: list = field(default_factory=list)  # bytes

    @property
    def success_rate(self):
        total = len(self.latencies) + self.errors
        return (len(self.latencies) / total * 100) if total > 0 else 0


@dataclass
class BenchmarkReport:
    transport: str
    rounds: int
    cold_start_ms: float = 0
    tools: dict = field(default_factory=dict)  # name → ToolResult
    throughput: dict = field(default_factory=dict)  # name → qps
    started_at: str = ""
    finished_at: str = ""

    def summary_lines(self):
        lines = []
        lines.append("=" * 78)
        lines.append("  phonefast MCP Benchmark Report")
        lines.append("=" * 78)
        lines.append(f"  Transport:   {self.transport}")
        lines.append(f"  Rounds:      {self.rounds}")
        lines.append(f"  Cold start:  {format_ms(self.cold_start_ms)}")
        lines.append(f"  Started:     {self.started_at}")
        lines.append(f"  Finished:    {self.finished_at}")
        lines.append("")

        # 传感器类工具（读取操作）
        lines.append("  ── Sensor Tools (read-only) ──")
        lines.append(f"  {'Tool':<22} {'Avg':>7} {'p50':>7} {'p95':>7} {'p99':>7} {'Min':>7} {'Max':>7} {'Err%':>6} {'Size':>10}")
        lines.append("  " + "-" * 76)
        for name in SENSOR_TOOLS:
            if name in self.tools:
                tr = self.tools[name]
                s = stats(tr.latencies)
                err_rate = (tr.errors / (len(tr.latencies) + tr.errors) * 100) if (len(tr.latencies) + tr.errors) > 0 else 0
                avg_size = f"{statistics.mean(tr.data_sizes)/1024:.0f}KB" if tr.data_sizes else "—"
                lines.append(
                    f"  {name:<22} {format_ms(s['avg']):>7} {format_ms(s['p50']):>7} "
                    f"{format_ms(s['p95']):>7} {format_ms(s['p99']):>7} "
                    f"{format_ms(s['min']):>7} {format_ms(s['max']):>7} {err_rate:>5.0f}% {avg_size:>10}"
                )

        lines.append("")
        lines.append("  ── Action Tools (control) ──")
        lines.append(f"  {'Tool':<22} {'Avg':>7} {'p50':>7} {'p95':>7} {'p99':>7} {'Min':>7} {'Max':>7} {'Err%':>6}")
        lines.append("  " + "-" * 76)
        for name in ACTION_TOOLS:
            if name in self.tools:
                tr = self.tools[name]
                s = stats(tr.latencies)
                err_rate = (tr.errors / (len(tr.latencies) + tr.errors) * 100) if (len(tr.latencies) + tr.errors) > 0 else 0
                lines.append(
                    f"  {name:<22} {format_ms(s['avg']):>7} {format_ms(s['p50']):>7} "
                    f"{format_ms(s['p95']):>7} {format_ms(s['p99']):>7} "
                    f"{format_ms(s['min']):>7} {format_ms(s['max']):>7} {err_rate:>5.0f}%"
                )

        lines.append("")
        lines.append("  ── Throughput (QPS, 20 iterations) ──")
        lines.append(f"  {'Tool':<22} {'QPS':>8} {'Total':>8} {'Avg':>8}")
        lines.append("  " + "-" * 46)
        for name, qps in sorted(self.throughput.items(), key=lambda x: -x[1]):
            lines.append(f"  {name:<22} {qps:>7.1f}/s")

        lines.append("")
        lines.append("=" * 78)

        # 快速参考：关键指标
        critical_tools = ["observe", "screenshot", "get_ui_elements", "tap"]
        lines.append("  Key Metrics (p50):")
        for name in critical_tools:
            if name in self.tools:
                s = stats(self.tools[name].latencies)
                lines.append(f"    {name:<24} p50={format_ms(s['p50'])}  p95={format_ms(s['p95'])}")
        lines.append("=" * 78)
        return lines


class Benchmark:
    def __init__(self, client, transport="stdio", rounds=DEFAULT_ROUNDS):
        self.client = client
        self.transport = transport
        self.rounds = rounds
        self.report = BenchmarkReport(transport=transport, rounds=rounds)
        self._screen_w = 720
        self._screen_h = 1600

    def run(self):
        self.report.started_at = time.strftime("%Y-%m-%d %H:%M:%S")

        # ── 0. 冷启动检测 ──
        t0 = time.time()
        ready = False
        for i in range(30):
            if self.client.ready():
                ready = True
                break
            time.sleep(0.2)
        self.report.cold_start_ms = (time.time() - t0) * 1000
        if not ready:
            print("FATAL: client not ready after cold start wait")
            return self.report

        # 获取屏幕尺寸
        self._detect_screen_size()

        # ── 1. 逐工具多轮测试 ──
        print(f"\n{'='*78}")
        print(f"  Benchmark: {self.rounds} rounds per tool")
        print(f"{'='*78}")

        for tool_name in ALL_TOOLS:
            self._bench_tool(tool_name)
            # 工具间短暂间隔
            time.sleep(0.05)

        # ── 2. 吞吐测试（选重点工具） ──
        throughput_targets = ["observe", "screenshot", "get_ui_elements", "back", "tap"]
        for tool_name in throughput_targets:
            if tool_name in self.report.tools:
                self._bench_throughput(tool_name, 20)

        self.report.finished_at = time.strftime("%Y-%m-%d %H:%M:%S")
        return self.report

    def _detect_screen_size(self):
        """从 observe 获取真实屏幕尺寸。"""
        try:
            text = self.client.tool("observe")
            if text and "screenshot_base64" in (text or ""):
                data = json.loads(text)
                b64 = data.get("screenshot_base64", "")
                if len(b64) > 100:
                    raw = base64.b64decode(b64)
                    if raw[:4] == b"\x89PNG" and len(raw) > 24:
                        self._screen_w = int.from_bytes(raw[16:20], "big")
                        self._screen_h = int.from_bytes(raw[20:24], "big")
                        print(f"  Screen: {self._screen_w}x{self._screen_h}")
        except Exception:
            pass

    def _bench_tool(self, name):
        """对单个工具执行 N 轮测量。"""
        tr = ToolResult(
            name=name,
            category="sensor" if name in SENSOR_TOOLS else "action",
        )
        self.report.tools[name] = tr

        args = self._tool_args(name)
        label = name
        if args:
            short_args = {k: v for k, v in list(args.items())[:2]}
            label = f"{name}({json.dumps(short_args)})"

        print(f"\n  [{name}] ", end="", flush=True)

        for i in range(self.rounds):
            try:
                t0 = time.perf_counter()
                text = self.client.tool(name, args)
                elapsed = (time.perf_counter() - t0) * 1000

                # 判断是否成功
                if text and not text.startswith("error:") and not text.startswith("rpc_error:"):
                    tr.latencies.append(elapsed)
                    # 记录数据大小（用于传感器工具）
                    if name in ("screenshot", "observe", "get_ui_elements"):
                        tr.data_sizes.append(len(text.encode("utf-8")))
                else:
                    tr.errors += 1
                    # 首次错误打印详情
                    if tr.errors == 1:
                        short = (text or "")[:80]
                        print(f"\n    ⚠ [{name}] round {i}: {short}", flush=True)

                # 进度指示
                if (i + 1) % max(1, self.rounds // 5) == 0:
                    print(".", end="", flush=True)

            except Exception as e:
                tr.errors += 1
                if tr.errors == 1:
                    print(f"\n    ⚠ [{name}] round {i}: {e}", flush=True)

        s = stats(tr.latencies)
        status = "✓" if tr.success_rate >= 90 else ("⚠" if tr.success_rate > 0 else "✗")
        print(f"\n    {status} avg={format_ms(s['avg'])}  p50={format_ms(s['p50'])}  "
              f"p95={format_ms(s['p95'])}  p99={format_ms(s['p99'])}  "
              f"min/max={format_ms(s['min'])}/{format_ms(s['max'])}  "
              f"err={tr.errors}/{self.rounds}", flush=True)

    def _bench_throughput(self, name, count):
        """连续调用 count 次，测量吞吐 (QPS)。"""
        args = self._tool_args(name)
        latencies = []
        errors = 0

        print(f"\n  [throughput:{name}] {count} iterations...", flush=True)
        t_start = time.perf_counter()

        for i in range(count):
            try:
                t0 = time.perf_counter()
                text = self.client.tool(name, args)
                elapsed = (time.perf_counter() - t0) * 1000
                if text and not text.startswith("error:") and not text.startswith("rpc_error:"):
                    latencies.append(elapsed)
                else:
                    errors += 1
            except Exception:
                errors += 1

        total_sec = time.perf_counter() - t_start
        qps = (count - errors) / total_sec if total_sec > 0 else 0
        self.report.throughput[name] = qps

        s = stats(latencies)
        print(f"    QPS={qps:.1f}/s  total={total_sec:.1f}s  "
              f"avg={format_ms(s['avg'])}  p50={format_ms(s['p50'])}  "
              f"errors={errors}/{count}", flush=True)

    def _tool_args(self, name):
        """返回各工具的默认测试参数。"""
        cx = self._screen_w // 2
        cy = self._screen_h // 2
        return {
            "list_devices": {},
            "screenshot": {},
            "get_ui_elements": {},
            "observe": {},
            "tap": {"x": cx, "y": cy},
            "tap_element": {"index": 0},
            "swipe": {
                "start_x": cx, "start_y": int(self._screen_h * 0.7),
                "end_x": cx, "end_y": int(self._screen_h * 0.3),
                "duration_ms": 200,
            },
            "type_text": {"text": "a"},
            "back": {},
            "home": {},
            "press_key": {"keycode": 4},  # BACK
            "launch_app": {"package": "com.android.settings"},
            "wait": {"duration_ms": 50},
        }.get(name, {})


# ══════════════════════════════════════════════════════════════════════════════════
#  Main
# ══════════════════════════════════════════════════════════════════════════════════

def check_prereqs(binary):
    if not os.path.isfile(binary):
        print(f"FATAL: binary not found at {binary}")
        sys.exit(1)
    r = subprocess.run(["adb", "devices"], capture_output=True, text=True)
    devices = [l for l in r.stdout.strip().split("\n")[1:] if "\tdevice" in l]
    if not devices:
        print("FATAL: no ADB devices connected")
        sys.exit(1)
    print(f"Device: {devices[0].split()[0]}")


def start_sse_server(binary, port):
    proc = subprocess.Popen(
        [binary, "serve", "--transport", "sse", "--port", str(port)],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    for _ in range(60):
        time.sleep(0.2)
        try:
            s = socket.create_connection(("127.0.0.1", port), timeout=0.5)
            s.close()
            return proc
        except Exception:
            pass
    proc.terminate()
    raise RuntimeError(f"SSE server did not start on port {port}")


def main():
    parser = argparse.ArgumentParser(description="phonefast MCP Benchmark")
    parser.add_argument("--sse", action="store_true", help="Use SSE transport")
    parser.add_argument("--port", type=int, default=DEFAULT_SSE_PORT, help="SSE port")
    parser.add_argument("--binary", default=BINARY, help="Path to phonefast binary")
    parser.add_argument("--rounds", type=int, default=DEFAULT_ROUNDS, help="Rounds per tool")
    parser.add_argument("--quick", action="store_true", help="Quick mode (3 rounds)")
    parser.add_argument("--output", default=None, help="Save JSON report to file")
    args = parser.parse_args()

    binary = args.binary
    rounds = QUICK_ROUNDS if args.quick else args.rounds

    check_prereqs(binary)

    print(f"\n{'='*78}")
    print(f"  phonefast MCP Benchmark")
    print(f"  Transport: {'SSE' if args.sse else 'STDIO'}")
    print(f"  Rounds:    {rounds}")
    print(f"{'='*78}")

    sse_proc = None
    client = None

    try:
        if args.sse:
            sse_proc = start_sse_server(binary, args.port)
            print(f"\nSSE server started on :{args.port}")
            client = SSEMCPClient(args.port)
            client.connect(timeout=12)
        else:
            print("\nStarting phonefast (STDIO mode)...")
            client = StdioMCPClient(binary)

        bench = Benchmark(client, transport="sse" if args.sse else "stdio", rounds=rounds)
        report = bench.run()

        # 打印报告
        for line in report.summary_lines():
            print(line)

        # 保存 JSON
        if args.output:
            json_report = {
                "transport": report.transport,
                "rounds": report.rounds,
                "cold_start_ms": report.cold_start_ms,
                "started_at": report.started_at,
                "finished_at": report.finished_at,
                "throughput": report.throughput,
                "tools": {},
            }
            for name, tr in report.tools.items():
                s = stats(tr.latencies)
                json_report["tools"][name] = {
                    "category": tr.category,
                    "latency": s,
                    "errors": tr.errors,
                    "total_calls": len(tr.latencies) + tr.errors,
                    "success_rate": round(tr.success_rate, 1),
                }
                if tr.data_sizes:
                    json_report["tools"][name]["avg_data_bytes"] = round(statistics.mean(tr.data_sizes))
            with open(args.output, "w") as f:
                json.dump(json_report, f, indent=2, ensure_ascii=False)
            print(f"\nJSON report saved to: {args.output}")

    except Exception as e:
        print(f"\nFATAL: {e}")
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


if __name__ == "__main__":
    main()
