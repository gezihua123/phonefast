#!/usr/bin/env python3
"""
phonefast 1-Hour Daemon Stress Test

直接通过 Unix socket 发送 JSON-RPC 到 daemon，无 CLI 子进程开销。
每个操作独立计时（毫秒精度）。

测试阶段 (共 60 分钟):
  Warmup    0-5min   interval=1.0s   低频，建立基线
  Steady    5-20min  interval=0.5s   常规循环，覆盖所有操作
  Burst A   20-25min interval=0.08s  高频轻量操作 (tap/back/home/key)
  Mixed     25-40min interval=0.4s   导航 + 截图 + UI dump
  Burst B   40-45min interval=0.06s  极限吞吐压测
  Cooldown  45-60min interval=1.0s   低频收尾，验证长期稳定性

用法:
  python3 tests/stress_test_rpc.py              # 完整 60 分钟
  python3 tests/stress_test_rpc.py --quick      # 5 分钟冒烟
  python3 tests/stress_test_rpc.py -d 120       # 自定义时长
"""

import socket, json, time, sys, os, argparse, subprocess
import statistics, csv, textwrap, threading
from datetime import datetime
from collections import defaultdict

# ── 配置 ────────────────────────────────────────────────────────────────────────
_BINARY = os.path.join(os.path.dirname(__file__), "..", "dist", "dev", "phonefast-darwin-arm64")
MEM_INTERVAL = 30  # RSS 采样间隔（秒）

# 测试阶段: (分钟, 名称, 间隔秒, 操作池)
PHASES_FULL = [
    (5,  "Warmup",    1.0,  "full"),
    (15, "Steady",    0.5,  "full"),
    (5,  "Burst A",   0.08, "light"),
    (15, "Mixed",     0.4,  "full"),
    (5,  "Burst B",   0.06, "light"),
    (15, "Cooldown",  1.0,  "full"),
]

PHASES_QUICK = [
    (1, "Warmup",   0.8,  "full"),
    (2, "Steady",   0.3,  "full"),
    (1, "Burst",    0.08, "light"),
    (1, "Cooldown", 1.0,  "full"),
]

OPS_LIGHT = [
    ("tap",       {"x": 540, "y": 960}),
    ("tap",       {"x": 300, "y": 400}),
    ("tap",       {"x": 800, "y": 1200}),
    ("back",      {}),
    ("home",      {}),
    ("press_key", {"key": "enter"}),
    ("wait",      {"duration_ms": 30}),
]

OPS_FULL = [
    ("tap",           {"x": 540, "y": 960}),
    ("tap",           {"x": 200, "y": 800}),
    ("tap",           {"x": 900, "y": 600}),
    ("swipe",         {"start_x": 540, "start_y": 1200, "end_x": 540, "end_y": 400, "duration_ms": 300}),
    ("swipe",         {"start_x": 540, "start_y": 400, "end_x": 540, "end_y": 1200, "duration_ms": 300}),
    ("back",          {}),
    ("home",          {}),
    ("screenshot",    {}),
    ("get_ui_elements", {}),
    ("observe",       {}),
    ("press_key",     {"key": "enter"}),
    ("type_text",     {"text": "test"}),
    ("launch_app",    {"app": "com.android.settings"}),
    ("status",        {}),
]


# ── Daemon JSON-RPC 客户端 ──────────────────────────────────────────────────────
class DaemonRPC:
    """通过 Unix socket 与 phonefast daemon 通信。"""

    def __init__(self, binary, serial):
        self._binary = binary
        self._serial = serial
        self._socket_path = None
        self._lock = threading.Lock()
        self._next_id = 0

    @property
    def socket_path(self):
        if self._socket_path is None:
            uid = os.getuid()
            self._socket_path = f"/tmp/phonefast-{uid}-{self._serial}.sock"
        return self._socket_path

    def connect(self, timeout=15):
        """验证 socket 可连通。"""
        if not os.path.exists(self.socket_path):
            raise RuntimeError(
                f"Socket not found at {self.socket_path}. Is daemon running?")
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(timeout)
        sock.connect(self.socket_path)
        sock.close()
        return self.socket_path

    def call(self, method, params=None, timeout=30):
        """发送 JSON-RPC 请求。返回 (elapsed_ms, result, error_string)。"""
        if params is None:
            params = {}

        with self._lock:
            self._next_id += 1
            req_id = self._next_id

        req = json.dumps({
            "jsonrpc": "2.0",
            "id": req_id,
            "method": method,
            "params": params,
        })
        payload = (req + "\n").encode()

        t0 = time.time()
        sock = None
        try:
            sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            sock.settimeout(timeout)
            sock.connect(self.socket_path)
            sock.sendall(payload)

            buf = b""
            while b"\n" not in buf:
                chunk = sock.recv(4096)
                if not chunk:
                    dt = (time.time() - t0) * 1000
                    return dt, None, "connection closed"
                buf += chunk

            dt = (time.time() - t0) * 1000
            line = buf.split(b"\n")[0]
            resp = json.loads(line)

            if "error" in resp and resp["error"]:
                msg = resp["error"].get("message", str(resp["error"]))
                return dt, None, msg
            return dt, resp.get("result"), None

        except socket.timeout:
            return (timeout * 1000), None, "timeout"
        except Exception as e:
            return (time.time() - t0) * 1000, None, str(e)
        finally:
            if sock:
                try:
                    sock.close()
                except Exception:
                    pass

    def close(self):
        pass


# ── 压测引擎 ────────────────────────────────────────────────────────────────────
class StressTest:
    def __init__(self, binary, serial, duration_min=60, quick=False):
        self.binary = binary
        self.serial = serial
        self.quick = quick
        self.duration = 300 if quick else duration_min * 60
        self.start_time = None
        self.run_dir = None

        self.times = defaultdict(list)
        self.errors = []
        self.mem_samples = []
        self.reconnects = []
        self.ops_ok = 0
        self.ops_fail = 0

        self.rpc = DaemonRPC(binary, serial)
        self.stop_event = threading.Event()

    @property
    def ops_total(self):
        return self.ops_ok + self.ops_fail

    def sample_mem(self):
        try:
            # Match ONLY the long-lived daemon worker process, whose
            # command line is "...phonefast... daemon_worker --serial".
            # A bare `pgrep -f phonefast` also matches the short-lived CLI
            # child processes forked per RPC call (e.g. "--daemon tap"), whose
            # RSS is ~0.5-1MB before they exit — sampling those produced bogus
            # low RSS readings in earlier runs (e.g. 6/18 reported 3.1→0.9MB).
            r = subprocess.run(
                ["pgrep", "-f", "phonefast.*daemon_worker"],
                capture_output=True, text=True, timeout=3)
            pids = r.stdout.strip().split()
            if not pids:
                return
            r = subprocess.run(
                ["ps", "-o", "rss=", "-p", pids[0]],
                capture_output=True, text=True, timeout=3)
            rss = int(r.stdout.strip()) if r.stdout.strip() else 0
            # Sanity guard: a real daemon holds scrcpy TCP conns + an H.264
            # decoder, so RSS is always >= ~5MB. Sub-5MB means we matched a
            # transient CLI child — discard rather than pollute the series.
            if rss < 5 * 1024:
                return
            self.mem_samples.append((time.time() - self.start_time, rss))
        except Exception:
            pass

    def progress_loop(self):
        while not self.stop_event.is_set():
            elapsed = time.time() - self.start_time
            if elapsed <= 0:
                time.sleep(0.5)
                continue
            remaining = max(0, self.duration - elapsed)
            pct = min(100, elapsed / self.duration * 100)
            rate = self.ops_total / elapsed if elapsed > 0 else 0
            bar_w = 30
            filled = int(pct / 100 * bar_w)
            bar = "█" * filled + "░" * (bar_w - filled)

            mem = ""
            if self.mem_samples:
                mem = f"  RSS={self.mem_samples[-1][1] // 1024}MB"

            print(f"\r  [{bar}] {pct:5.1f}%  "
                  f"{int(elapsed // 60):02d}:{int(elapsed % 60):02d}/"
                  f"{int(self.duration // 60):02d}:00  "
                  f"ops={self.ops_total}  ok={self.ops_ok}  "
                  f"err={self.ops_fail}  rec={len(self.reconnects)}  "
                  f"rate={rate:.1f}/s{mem}   ",
                  end="", flush=True)
            time.sleep(2)

    def write_report(self):
        total_elapsed = time.time() - self.start_time
        os.makedirs(self.run_dir, exist_ok=True)

        with open(os.path.join(self.run_dir, "timing.csv"), "w", newline="") as f:
            w = csv.writer(f)
            w.writerow(["op", "count", "p50_ms", "p95_ms", "p99_ms",
                        "avg_ms", "min_ms", "max_ms", "errors"])
            for op_name in sorted(self.times):
                vals = self.times[op_name]
                if not vals:
                    continue
                s = sorted(vals)
                n = len(s)
                errs = sum(1 for e in self.errors if e[1] == op_name)
                w.writerow([op_name, n,
                           round(s[n // 2], 1),
                           round(s[int(n * 0.95)], 1),
                           round(s[int(n * 0.99)], 1) if n > 1 else round(s[0], 1),
                           round(sum(s) / n, 1),
                           round(s[0], 1),
                           round(s[-1], 1),
                           errs])

        if self.mem_samples:
            with open(os.path.join(self.run_dir, "memory.csv"), "w", newline="") as f:
                w = csv.writer(f)
                w.writerow(["elapsed_s", "rss_kb", "rss_mb"])
                for elapsed, rss in self.mem_samples:
                    w.writerow([round(elapsed, 1), rss, round(rss / 1024, 1)])

        if self.errors:
            with open(os.path.join(self.run_dir, "errors.log"), "w") as f:
                f.write(f"Total errors: {len(self.errors)}\n")
                f.write("=" * 70 + "\n")
                for elapsed, op, err in self.errors:
                    f.write(f"[{elapsed:8.1f}s] {op:20s} | {err}\n")

        if self.reconnects:
            with open(os.path.join(self.run_dir, "reconnects.log"), "w") as f:
                for elapsed, reason in self.reconnects:
                    f.write(f"[{elapsed:8.1f}s] {reason}\n")

        mem_trend = "no_data"
        mem_growth_mb = 0
        leak_suspected = False
        if len(self.mem_samples) >= 3:
            rss_vals = [s[1] for s in self.mem_samples]
            first, last = rss_vals[0], rss_vals[-1]
            mem_growth_mb = (last - first) / 1024

        summary = {
            "test_start": datetime.now().isoformat(),
            "test_duration_s": round(total_elapsed, 1),
            "test_duration_min": round(total_elapsed / 60, 1),
            "ops_total": self.ops_total,
            "ops_ok": self.ops_ok,
            "ops_fail": self.ops_fail,
            "success_rate_pct": round(self.ops_ok / max(self.ops_total, 1) * 100, 2),
            "reconnects": len(self.reconnects),
            "device": self.serial,
            "memory": {
                "samples": len(self.mem_samples),
                "trend": mem_trend,
                "leak_suspected": leak_suspected,
            },
            "operations": {},
        }

        if self.mem_samples:
            rss_all = [s[1] for s in self.mem_samples]
            summary["memory"].update({
                "rss_start_mb": round(rss_all[0] / 1024, 1),
                "rss_end_mb": round(rss_all[-1] / 1024, 1),
                "rss_growth_mb": round(mem_growth_mb, 1),
                "rss_min_mb": round(min(rss_all) / 1024, 1),
                "rss_max_mb": round(max(rss_all) / 1024, 1),
                "rss_avg_mb": round(sum(rss_all) / len(rss_all) / 1024, 1),
            })

        for op_name in sorted(self.times):
            vals = self.times[op_name]
            if not vals:
                continue
            s = sorted(vals)
            n = len(s)
            errs = sum(1 for e in self.errors if e[1] == op_name)
            summary["operations"][op_name] = {
                "count": n,
                "errors": errs,
                "success_rate_pct": round((n - errs) / n * 100, 2) if n else 0,
                "p50_ms": round(s[n // 2], 1),
                "p95_ms": round(s[int(n * 0.95)], 1),
                "p99_ms": round(s[int(n * 0.99)], 1) if n > 1 else round(s[0], 1),
                "avg_ms": round(sum(s) / n, 1),
                "min_ms": round(s[0], 1),
                "max_ms": round(s[-1], 1),
            }

        with open(os.path.join(self.run_dir, "summary.json"), "w") as f:
            json.dump(summary, f, indent=2, ensure_ascii=False)

        lines = [
            "=" * 70,
            "  phonefast 1-Hour Daemon Stress Test Report",
            "=" * 70,
            f"  Device:    {self.serial}",
            f"  Duration:  {summary['test_duration_min']:.0f} min ({summary['test_duration_s']:.0f}s)",
            f"  Total ops: {self.ops_total} ({self.ops_ok} ok, {self.ops_fail} failed)",
            f"  Success:   {summary['success_rate_pct']}%",
            f"  Reconnects: {len(self.reconnects)}",
        ]
        if self.mem_samples:
            mm = summary["memory"]
            lines.append(f"  Memory:    {mm['rss_start_mb']}MB -> {mm['rss_end_mb']}MB "
                        f"(Δ{mm['rss_growth_mb']:+.1f}MB, {mm['trend']})")

        lines += [
            "",
            f"  {'Operation':<20} {'Count':>6} {'Err':>4} {'Rate':>6}  "
            f"{'P50':>7} {'P95':>7} {'P99':>7} {'Avg':>7} {'Max':>7}",
            "  " + "-" * 68,
        ]
        for op_name, d in summary["operations"].items():
            lines.append(
                f"  {op_name:<20} {d['count']:>6} {d['errors']:>4} "
                f"{d['success_rate_pct']:>5.1f}%  "
                f"{d['p50_ms']:>6.0f}ms {d['p95_ms']:>6.0f}ms "
                f"{d['p99_ms']:>6.0f}ms {d['avg_ms']:>6.0f}ms "
                f"{d['max_ms']:>6.0f}ms")
        lines += ["", f"  Output: {self.run_dir}/", "=" * 70]

        report = "\n".join(lines)
        with open(os.path.join(self.run_dir, "report.txt"), "w") as f:
            f.write(report + "\n")
        return summary, report

    def run(self):
        ts = datetime.now().strftime("%Y%m%d_%H%M%S")
        self.run_dir = os.path.join(os.path.dirname(__file__), "..",
                                    "test_runs", f"stress_1h_{ts}")

        print("=" * 70)
        print("  phonefast 1-Hour Daemon Stress Test")
        print("=" * 70)
        print(f"  Binary:    {self.binary}")
        print(f"  Device:    {self.serial}")
        print(f"  Duration:  {self.duration // 60} min")
        print(f"  Output:    {self.run_dir}/")
        print("=" * 70)

        # ── 前置检查 ──
        if not os.path.isfile(self.binary):
            print(f"FATAL: {self.binary} not found.")
            print("  Build: bash scripts/build.sh")
            return 1

        # 检查设备（用 phonefast 自身）
        r = subprocess.run([self.binary, "devices"], capture_output=True, text=True, timeout=10)
        if "device" not in r.stdout.lower():
            print("FATAL: No device connected")
            return 1
        print(f"  Devices:\n{r.stdout.strip()}")

        # ── 停止现有 daemon（强制清理旧版和当前版本）──
        print("\n  Stopping existing daemon...")
        # 先停 serial-specific daemon
        subprocess.run([self.binary, "daemon", "--stop", "--serial", self.serial],
                       capture_output=True, timeout=8)
        time.sleep(1)
        # 再清理遗留的 UID-only daemon
        subprocess.run([self.binary, "daemon", "--stop"],
                       capture_output=True, timeout=5)
        time.sleep(1)
        # Kill any lingering phonefast processes
        subprocess.run(["pkill", "-f", "phonefast-darwin"],
                       capture_output=True, timeout=3)
        time.sleep(0.5)
        # Kill scrcpy server on device
        subprocess.run([self.binary, "disconnect"],
                       capture_output=True, timeout=5)
        time.sleep(0.5)

        # ── 启动 daemon ──
        print("  Starting daemon...")
        r = subprocess.run([self.binary, "daemon", "--serial", self.serial],
                           capture_output=True, text=True, timeout=30)
        time.sleep(3)

        # 验证 daemon 运行
        r = subprocess.run([self.binary, "daemon", "--status", "--serial", self.serial],
                           capture_output=True, text=True, timeout=10)
        if "running" not in r.stdout.lower():
            print(f"FATAL: Daemon failed to start\n  stdout: {r.stdout[:300]}\n  stderr: {r.stderr[:300]}")
            return 1

        print(f"  Daemon ready: {r.stdout.strip()}")

        # ── 连接 ──
        try:
            sock_path = self.rpc.connect(timeout=10)
            print(f"  Socket:    {sock_path}")
        except Exception as e:
            print(f"FATAL: Cannot connect: {e}")
            return 1

        dt, result, err = self.rpc.call("status", timeout=5)
        if err:
            print(f"FATAL: status failed: {err}")
            return 1
        w, h = 1080, 1920
        if result:
            w = int(result.get("device_width", w))
            h = int(result.get("device_height", h))
            print(f"  Screen:    {w}x{h}")

        # ── 开始测试 ──
        print(f"\n  Press Ctrl+C to stop gracefully\n")
        self.start_time = time.time()

        prog = threading.Thread(target=self.progress_loop, daemon=True)
        prog.start()

        def mem_loop():
            while not self.stop_event.is_set():
                self.sample_mem()
                time.sleep(MEM_INTERVAL)
        mem_t = threading.Thread(target=mem_loop, daemon=True)
        mem_t.start()

        # ── 阶段执行 ──
        phases = PHASES_QUICK if self.quick else PHASES_FULL
        op_idx = 0
        cycle = 0

        try:
            while time.time() - self.start_time < self.duration:
                cycle += 1

                for phase_min, phase_name, interval, pool_key in phases:
                    phase_dur = phase_min * 60
                    elapsed = time.time() - self.start_time
                    phase_dur = min(phase_dur, max(1, self.duration - elapsed))
                    phase_start = time.time()
                    pool = OPS_LIGHT if pool_key == "light" else OPS_FULL
                    ops_per_sec = 1.0 / interval if interval > 0 else 99

                    print(f"\n  ── {phase_name} ({phase_dur // 60}min{phase_dur % 60}s, "
                          f"{ops_per_sec:.0f} ops/s, {len(pool)} actions) ──")

                    while time.time() - phase_start < phase_dur:
                        if self.stop_event.is_set():
                            break

                        op_name, params = pool[op_idx % len(pool)]
                        op_idx += 1

                        dt, result, err = self.rpc.call(op_name, params, timeout=15)

                        if err:
                            self.ops_fail += 1
                            self.errors.append((time.time() - self.start_time, op_name, err))
                            self.times[op_name].append(dt)

                            if any(kw in err.lower() for kw in
                                  ["connection", "closed", "broken pipe", "refused"]):
                                self.reconnects.append(
                                    (time.time() - self.start_time, f"reconnect: {err}"))
                                print(f"\n  ⚠️  Connection lost, reconnecting for {self.serial}...")
                                try:
                                    self.rpc.close()
                                except Exception:
                                    pass
                                time.sleep(1)
                                subprocess.run([self.binary, "daemon", "--stop", "--serial", self.serial],
                                               capture_output=True, timeout=5)
                                time.sleep(0.5)
                                subprocess.run([self.binary, "daemon", "--serial", self.serial],
                                               capture_output=True, timeout=30)
                                time.sleep(3)
                                try:
                                    self.rpc = DaemonRPC(self.binary, self.serial)
                                    self.rpc.connect(timeout=15)
                                    _, _, stat_err = self.rpc.call("status", timeout=5)
                                    print(f"  {'✅ Reconnected' if not stat_err else '❌ Reconnect failed: ' + str(stat_err)}")
                                except Exception as e2:
                                    print(f"  ❌ Reconnect error: {e2}")
                        else:
                            self.ops_ok += 1
                            self.times[op_name].append(dt)

                        sleep_time = max(0.01, interval - (dt / 1000) * 0.5)
                        time.sleep(sleep_time)

                    if time.time() - self.start_time >= self.duration:
                        break

        except KeyboardInterrupt:
            print("\n\n  ⚠️  Interrupted by user, writing report...")

        # ── 收尾（不放在 finally，避免覆盖异常）──
        exit_code = 1
        self.stop_event.set()
        print()

        self.rpc.close()
        summary, report = self.write_report()
        print(report)

        rate = summary["success_rate_pct"]
        verdict = ("✅ EXCELLENT" if rate >= 99 else
                   "✅ GOOD" if rate >= 95 else
                   "⚠️  FAIR" if rate >= 90 else "❌ POOR")
        print(f"\n  Verdict: {verdict}")
        print(f"  Report:  {self.run_dir}/")

        subprocess.run([self.binary, "daemon", "--stop", "--serial", self.serial],
                       capture_output=True, timeout=5)

        exit_code = 0 if rate >= 95 else 1
        return exit_code


# ── 入口 ──
def main():
    parser = argparse.ArgumentParser(
        description="phonefast 1-Hour Daemon Stress Test",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=textwrap.dedent("""\
        Examples:
          %(prog)s                     Full 60-minute stress test
          %(prog)s --quick             5-minute smoke test
          %(prog)s --duration 120      2-hour extended test
        """))

    parser.add_argument("--duration", "-d", type=int, default=60,
                       help="Test duration in minutes (default: 60)")
    parser.add_argument("--quick", "-q", action="store_true",
                       help="5-minute quick smoke test")
    parser.add_argument("--binary", default=_BINARY,
                       help=f"Path to phonefast binary (default: {_BINARY})")
    parser.add_argument("--serial", default=None,
                       help="Device serial (auto-detect if not set)")

    args = parser.parse_args()

    # Auto-detect serial
    serial = args.serial
    if not serial:
        r = subprocess.run([args.binary, "devices"], capture_output=True, text=True, timeout=10)
        for line in r.stdout.strip().split("\n"):
            if "device" in line and line.split()[0] != "Connected":
                serial = line.split()[0]
                break
        if not serial:
            print("FATAL: No device detected")
            return 1

    runner = StressTest(
        binary=args.binary,
        serial=serial,
        duration_min=args.duration,
        quick=args.quick,
    )
    sys.exit(runner.run())


if __name__ == "__main__":
    main()
