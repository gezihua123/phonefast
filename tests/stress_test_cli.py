#!/usr/bin/env python3
"""
phonefast 长时间压测脚本 — 稳定性 + 内存/性能监控

用法:
  python3 stress_test_cli.py                    # 默认 30 分钟，daemon 模式
  python3 stress_test_cli.py --duration 60      # 60 分钟
  python3 stress_test_cli.py --mode direct      # direct 模式压测
  python3 stress_test_cli.py --mode all         # 两种模式都压
  python3 stress_test_cli.py --monitor-mem      # 启用 Go pprof 内存采样

测试维度:
  1. daemon 模式持久 session 稳定性（长时间不崩）
  2. direct 模式反复启停 session（每次 ~2.5s）
  3. 内存泄漏检测（RSS 增长趋势）
  4. 操作成功率统计
  5. P50/P95/P99 延迟分布
"""

import subprocess, socket, json, time, sys, os, signal, argparse
import statistics, threading, csv, re, tempfile
from datetime import datetime
from collections import defaultdict

# ── 配置 ──────────────────────────────────────────────────────────────────────
_DEFAULT_BINARY = os.path.join(os.path.dirname(__file__), "..", "dist", "phonefast")
DURATION   = 30          # 分钟
MEM_INTERVAL = 10        # 内存采样间隔（秒）
OPS_INTERVAL = 2         # 操作间隔（秒，daemon 模式）

# 压测操作列表（daemon 模式）
DAEMON_OPS = [
    ("back",      ["--daemon", "back"]),
    ("home",      ["--daemon", "home"]),
    ("tap",       ["--daemon", "tap", "244", "540"]),
    ("swipe",     ["--daemon", "swipe", "540", "960", "540", "400", "300"]),
    ("screenshot","--daemon", "screenshot", "/tmp/pf_stress_screenshot.png"),
    ("ui",        ["--daemon", "ui"]),
    ("observe",   ["--daemon", "observe"]),
    ("type",      ["--daemon", "type", "test"]),
    ("launch",    ["--daemon", "launch", "com.android.settings"]),
    ("status",    ["--daemon", "status"]),
]

# 压测操作列表（direct 模式，间隔更长）
DIRECT_OPS = [
    ("back",      ["back"]),
    ("home",      ["home"]),
    ("tap",       ["tap", "600", "800"]),
    ("screenshot","screenshot", "/tmp/pf_stress_screenshot.png"),
    ("ui",        ["ui"]),
]

# ── 工具函数 ──────────────────────────────────────────────────────────────────
class StressRunner:
    def __init__(self, mode="daemon", duration_min=30, monitor_mem=False, binary=""):
        self.mode = mode
        self.duration = duration_min * 60
        self.monitor_mem = monitor_mem
        self.binary = binary
        self.start_time = None
        self.results = defaultdict(list)  # op_name -> [duration_ms, ...]
        self.errors = []                  # (timestamp, op_name, error)
        self.mem_samples = []             # (timestamp, rss_kb, go_alloc_mb)
        self.ops_total = 0
        self.ops_ok = 0
        self.ops_fail = 0
        self.process = None
        self.run_dir = None

    def ok(self, op, elapsed_ms):
        self.ops_total += 1
        self.ops_ok += 1
        self.results[op].append(elapsed_ms)

    def fail(self, op, err):
        self.ops_total += 1
        self.ops_fail += 1
        self.errors.append((datetime.now().isoformat(), op, str(err)[:200]))

    def run_cmd(self, args, timeout=10):
        """Run phonefast CLI command, return (success, elapsed_ms, output)."""
        t0 = time.time()
        try:
            r = subprocess.run([self.binary] + args, capture_output=True, text=True,
                             timeout=timeout)
            dt = (time.time() - t0) * 1000
            ok = r.returncode == 0
            return ok, dt, (r.stdout + r.stderr)[:500]
        except subprocess.TimeoutExpired:
            dt = timeout * 1000
            return False, dt, "timeout"
        except Exception as e:
            dt = (time.time() - t0) * 1000
            return False, dt, str(e)

    def sample_memory(self):
        """Sample process memory (RSS via ps, Go alloc via pprof if enabled)."""
        try:
            # Match only the long-lived daemon worker process,
            # not the short-lived CLI child processes forked per RPC call,
            # whose tiny RSS would otherwise corrupt the series.
            r = subprocess.run(["pgrep", "-f", "phonefast.*daemon_worker"],
                               capture_output=True, text=True)
            pids = r.stdout.strip().split()
            if not pids:
                return

            for pid in pids[:1]:  # take first pid
                # RSS via ps
                r2 = subprocess.run(["ps", "-o", "rss=", "-p", pid],
                                  capture_output=True, text=True)
                rss = int(r2.stdout.strip()) if r2.stdout.strip() else 0
                # Discard sub-5MB readings: real daemon RSS is always higher;
                # a low value means we matched a transient CLI child.
                if rss < 5 * 1024:
                    return

                # Goroutine count via runtime debug endpoint (if exposed)
                go_alloc = 0
                if self.monitor_mem:
                    try:
                        gr = subprocess.run(
                            ["curl", "-s", "http://localhost:6161/debug/pprof/heap?debug=1"],
                            capture_output=True, text=True, timeout=3)
                        # Extract heap in use
                        for line in gr.stdout.split("\n"):
                            if "HeapAlloc" in line:
                                parts = line.strip().split()
                                go_alloc = int(parts[-1]) // (1024 * 1024)
                    except:
                        pass

                self.mem_samples.append((
                    time.time() - self.start_time,
                    rss,
                    go_alloc
                ))
        except:
            pass

    def save_checkpoint(self):
        """Save intermediate report without stopping the test."""
        os.makedirs(self.run_dir, exist_ok=True)
        elapsed = time.time() - self.start_time
        hour = int(elapsed // 3600)

        chkpt = {
            "elapsed_s": round(elapsed, 1),
            "elapsed_h": round(elapsed / 3600, 2),
            "ops_total": self.ops_total,
            "ops_ok": self.ops_ok,
            "ops_fail": self.ops_fail,
            "errors": len(self.errors),
        }
        with open(os.path.join(self.run_dir, f"checkpoint_h{hour:02d}.json"), "w") as f:
            json.dump(chkpt, f, indent=2)
        return hour

    def save_report(self):
        """Write summary report to run_dir."""
        os.makedirs(self.run_dir, exist_ok=True)

        # Timing CSV
        with open(os.path.join(self.run_dir, "timing.csv"), "w") as f:
            w = csv.writer(f)
            w.writerow(["op", "p50_ms", "p95_ms", "p99_ms", "avg_ms", "min_ms", "max_ms", "count", "errors"])
            for op, times in sorted(self.results.items()):
                if not times:
                    continue
                s = sorted(times)
                err_count = sum(1 for e in self.errors if e[1] == op)
                w.writerow([
                    op,
                    round(s[len(s)//2], 1),
                    round(s[int(len(s)*0.95)], 1),
                    round(s[int(len(s)*0.99)], 1) if len(s) > 1 else round(s[0], 1),
                    round(statistics.mean(s), 1),
                    round(min(s), 1),
                    round(max(s), 1),
                    len(s),
                    err_count,
                ])

        # Memory CSV
        if self.mem_samples:
            with open(os.path.join(self.run_dir, "memory.csv"), "w") as f:
                w = csv.writer(f)
                w.writerow(["elapsed_s", "rss_kb", "go_heap_mb"])
                for s in self.mem_samples:
                    w.writerow(s)

        # Error log
        if self.errors:
            with open(os.path.join(self.run_dir, "errors.log"), "w") as f:
                f.write(f"Total errors: {len(self.errors)}\n")
                f.write("=" * 80 + "\n")
                for ts, op, err in self.errors:
                    f.write(f"[{ts}] {op}: {err}\n")

        # Summary JSON
        total_elapsed = time.time() - self.start_time
        summary = {
            "test_duration_s": round(total_elapsed, 1),
            "mode": self.mode,
            "ops_total": self.ops_total,
            "ops_ok": self.ops_ok,
            "ops_fail": self.ops_fail,
            "success_rate": round(self.ops_ok / max(self.ops_total, 1) * 100, 1),
            "total_errors": len(self.errors),
            "operations": {},
            "memory": {},
        }

        for op, times in sorted(self.results.items()):
            if not times:
                continue
            s = sorted(times)
            summary["operations"][op] = {
                "count": len(s),
                "p50_ms": round(s[len(s)//2], 1),
                "p95_ms": round(s[int(len(s)*0.95)], 1),
                "avg_ms": round(statistics.mean(s), 1),
                "min_ms": round(min(s), 1),
                "max_ms": round(max(s), 1),
            }

        if self.mem_samples:
            rss_vals = [s[1] for s in self.mem_samples]
            rss_first = rss_vals[0] if rss_vals else 0
            rss_last = rss_vals[-1] if rss_vals else 0
            rss_growth = rss_last - rss_first if rss_first > 0 else 0
            summary["memory"] = {
                "samples": len(self.mem_samples),
                "rss_start_kb": rss_first,
                "rss_end_kb": rss_last,
                "rss_growth_kb": rss_growth,
                "rss_avg_kb": round(statistics.mean(rss_vals), 0) if rss_vals else 0,
                "rss_trend": "INCREASING" if rss_growth > rss_first * 0.3 else
                             "DECREASING" if rss_growth < -rss_first * 0.1 else "STABLE",
                "leak_suspected": rss_growth > rss_first * 0.5 and total_elapsed > 300,
            }

        with open(os.path.join(self.run_dir, "summary.json"), "w") as f:
            json.dump(summary, f, indent=2)

        return summary

    def print_progress(self, summary=None):
        """Print live progress to stdout."""
        elapsed = time.time() - self.start_time
        remaining = self.duration - elapsed
        rate = self.ops_total / max(elapsed, 1)
        success_rate = self.ops_ok / max(self.ops_total, 1) * 100

        print(f"\n{'─'*60}")
        print(f"[{int(elapsed//60):02d}:{int(elapsed%60):02d} / "
              f"{self.duration//60:02d}:00]  "
              f"ops={self.ops_total}  ok={self.ops_ok}  fail={self.ops_fail}  "
              f"rate={rate:.1f}/s  success={success_rate:.1f}%"
              f"  remaining={int(remaining//60)}:{int(remaining%60):02d}")

        if self.mem_samples:
            last = self.mem_samples[-1]
            print(f"  mem: RSS={last[1]//1024}MB  heap~{last[2]}MB  "
                  f"(samples={len(self.mem_samples)})")

        if summary:
            print(f"\n{'='*60}")
            print(f"  STRESS TEST COMPLETE ({self.mode} mode)")
            print(f"{'='*60}")
            print(f"  Duration:     {summary['test_duration_s']:.0f}s")
            print(f"  Operations:   {summary['ops_total']} "
                  f"({summary['ops_ok']} ok, {summary['ops_fail']} fail)")
            print(f"  Success rate: {summary['success_rate']}%")
            print(f"  Errors:       {summary['total_errors']}")
            mem = summary.get("memory", {})
            if mem:
                print(f"  RSS:          {mem.get('rss_start_kb',0)//1024}MB → "
                      f"{mem.get('rss_end_kb',0)//1024}MB "
                      f"(Δ{mem.get('rss_growth_kb',0):+d}KB, {mem.get('rss_trend','?')})")
                if mem.get("leak_suspected"):
                    print(f"  ⚠️  MEMORY LEAK SUSPECTED — RSS grew by >50%")
            print(f"  Report:       {self.run_dir}/")

    # ── Test modes ────────────────────────────────────────────────────────────

    def run_daemon(self):
        """Daemon mode: persistent scrcpy session, fast ops loop."""
        print(f"\n>>> Daemon mode — starting daemon...")
        self.run_dir = os.path.join(os.path.dirname(__file__), "..", "test_runs",
                                    f"stress_daemon_{datetime.now().strftime('%Y%m%d_%H%M%S')}")

        # Start daemon
        subprocess.run([self.binary, "daemon"], capture_output=True, timeout=10)
        time.sleep(2)

        # Verify daemon is running
        r = subprocess.run([self.binary, "daemon", "--status"],capture_output=True, text=True)
        if "running" not in r.stdout.lower():
            print("FATAL: Daemon failed to start")
            return False

        print(f"  Daemon running. Duration: {self.duration//60}min. Output: {self.run_dir}/")
        self.start_time = time.time()
        next_mem = self.start_time + MEM_INTERVAL
        next_checkpoint_hour = 1  # Save checkpoint at hour 1, 2, 3, ...
        last_checkpoint_hour = 0
        op_idx = 0

        try:
            while time.time() - self.start_time < self.duration:
                op_name, *args = DAEMON_OPS[op_idx % len(DAEMON_OPS)]
                # Flatten args (screenshot has a single tuple)
                flat_args = []
                for a in args:
                    if isinstance(a, list):
                        flat_args.extend(a)
                    else:
                        flat_args.append(a)

                ok, dt, out = self.run_cmd(flat_args)
                if ok:
                    self.ok(op_name, dt)
                else:
                    self.fail(op_name, out)

                op_idx += 1

                # Memory sampling
                if time.time() >= next_mem:
                    self.sample_memory()
                    next_mem = time.time() + MEM_INTERVAL

                # Progress every 60s
                if op_idx % 30 == 0:
                    self.print_progress()

                # Hourly checkpoint
                elapsed = time.time() - self.start_time
                current_hour = int(elapsed // 3600)
                if current_hour >= next_checkpoint_hour and current_hour > last_checkpoint_hour:
                    h = self.save_checkpoint()
                    print(f"\n  📊 Hour {h} checkpoint saved → {self.run_dir}/checkpoint_h{h:02d}.json")
                    last_checkpoint_hour = current_hour
                    next_checkpoint_hour = current_hour + 1

                time.sleep(OPS_INTERVAL - min(dt / 1000, OPS_INTERVAL * 0.8))

        except KeyboardInterrupt:
            print("\n>>> Interrupted by user")

        return True

    def run_direct(self):
        """Direct mode: new scrcpy session every few calls."""
        print(f"\n>>> Direct mode — each op starts a fresh session (~2.5s)...")
        self.run_dir = os.path.join(os.path.dirname(__file__), "..", "test_runs",
                                    f"stress_direct_{datetime.now().strftime('%Y%m%d_%H%M%S')}")

        print(f"  Duration: {self.duration//60}min. Output: {self.run_dir}/")
        self.start_time = time.time()
        next_mem = self.start_time + MEM_INTERVAL
        op_idx = 0

        try:
            while time.time() - self.start_time < self.duration:
                op_name, *args = DIRECT_OPS[op_idx % len(DIRECT_OPS)]
                flat_args = []
                for a in args:
                    if isinstance(a, list):
                        flat_args.extend(a)
                    else:
                        flat_args.append(a)

                ok, dt, out = self.run_cmd(flat_args, timeout=20)
                if ok:
                    self.ok(op_name, dt)
                else:
                    self.fail(op_name, out)

                op_idx += 1

                if time.time() >= next_mem:
                    self.sample_memory()
                    next_mem = time.time() + MEM_INTERVAL

                if op_idx % 10 == 0:
                    self.print_progress()

                # Direct mode: no extra sleep (session init takes ~2.5s)

        except KeyboardInterrupt:
            print("\n>>> Interrupted by user")

        return True


# ── Main ──────────────────────────────────────────────────────────────────────
def main():
    parser = argparse.ArgumentParser(description="phonefast stress test")
    parser.add_argument("--duration", type=int, default=30,
                       help="Test duration in minutes (default: 30)")
    parser.add_argument("--mode", choices=["daemon", "direct", "all"],
                       default="daemon", help="Test mode (default: daemon)")
    parser.add_argument("--monitor-mem", action="store_true",
                       help="Enable Go pprof memory monitoring")
    parser.add_argument("--binary", default=_DEFAULT_BINARY,
                       help="Path to phonefast binary")
    args = parser.parse_args()

    binpath = args.binary

    if not os.path.isfile(binpath):
        print(f"FATAL: {binpath} not found. Build first: go build -o dist/phonefast ./cmd/phonefast/")
        sys.exit(1)

    # Check device
    r = subprocess.run(["adb", "devices"], capture_output=True, text=True)
    if "\tdevice" not in r.stdout:
        print("FATAL: No Android device connected")
        sys.exit(1)

    print("=" * 60)
    print("  phonefast Stress Test")
    print("=" * 60)
    print(f"  Binary:    {binpath}")
    print(f"  Mode:      {args.mode}")
    print(f"  Duration:  {args.duration} min")
    print(f"  Mem mon:   {'on' if args.monitor_mem else 'off'}")
    print("=" * 60)

    modes = ["daemon", "direct"] if args.mode == "all" else [args.mode]
    summaries = []

    for mode in modes:
        runner = StressRunner(mode=mode, duration_min=args.duration,
                            monitor_mem=args.monitor_mem, binary=binpath)

        if mode == "daemon":
            runner.run_daemon()
        else:
            runner.run_direct()

        summary = runner.save_report()
        runner.print_progress(summary)
        summaries.append((mode, summary))

    # Overall summary
    if len(summaries) > 1:
        print(f"\n{'='*60}")
        print(f"  OVERALL")
        print(f"{'='*60}")
        for mode, s in summaries:
            print(f"  {mode:8s}  {s['ops_total']:4d} ops  "
                  f"{s['success_rate']:5.1f}% success  "
                  f"{s['total_errors']:3d} errors")

    # Exit code based on success rate
    all_ok = all(s["success_rate"] >= 95 for _, s in summaries)
    sys.exit(0 if all_ok else 1)


if __name__ == "__main__":
    main()
