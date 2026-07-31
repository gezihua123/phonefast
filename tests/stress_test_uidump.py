#!/usr/bin/env python3
"""
phonefast UI Dump 专项压测脚本

针对 get_ui_elements (dump + summary) 和 observe (full) 进行循环压测。
记录每次调用耗时，统计 P50/P95/P99/max，检测错误。

用法:
  python3 tests/stress_test_uidump.py --serial 13709314CF044927
  python3 tests/stress_test_uidump.py --serial 13709314CF044927 --duration 10
  python3 tests/stress_test_uidump.py --serial 13709314CF044927 --mode summary
  python3 tests/stress_test_uidump.py --serial 13709314CF044927 --interval 0.2
"""

import socket, json, time, sys, os, argparse, statistics

BINARY = os.path.join(os.path.dirname(__file__), "..", "dist", "dev", "phonefast-darwin-arm64")

def call(sock_path, device, method, params=None, timeout=10):
    if params is None:
        params = {}
    params["device"] = device
    req = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}) + "\n"
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(timeout)
    s.connect(sock_path)
    t0 = time.time()
    s.sendall(req.encode())
    buf = b""
    while b"\n" not in buf:
        chunk = s.recv(65536)
        if not chunk:
            break
        buf += chunk
    dt = (time.time() - t0) * 1000
    s.close()
    resp = json.loads(buf.split(b"\n")[0])
    err = resp.get("error", {}).get("message", "") if resp.get("error") else ""
    count = resp.get("result", {}).get("count", len(resp.get("result", {}).get("elements", [])))
    return dt, count, err


def fmt_stats(times):
    if not times:
        return "no data"
    s = sorted(times)
    n = len(s)
    p50 = s[n // 2]
    p95 = s[int(n * 0.95)] if n > 1 else s[0]
    p99 = s[int(n * 0.99)] if n > 1 else s[0]
    avg = sum(s) / n
    return f"min={s[0]:.0f} p50={p50:.0f} p95={p95:.0f} p99={p99:.0f} avg={avg:.0f} max={s[-1]:.0f}ms (n={n})"


def main():
    parser = argparse.ArgumentParser(description="phonefast UI Dump stress test")
    parser.add_argument("--serial", required=True, help="Device serial")
    parser.add_argument("--duration", "-d", type=int, default=10, help="Duration in minutes (default 10)")
    parser.add_argument("--interval", type=float, default=0.3, help="Interval between calls in seconds (default 0.3)")
    parser.add_argument("--mode", choices=["all", "summary", "dump", "full"], default="all",
                        help="Test mode: all (alternate), summary, dump, full (default all)")
    args = parser.parse_args()

    # Stop existing daemon
    subprocess_run = __import__("subprocess").run
    subprocess_run([BINARY, "daemon", "--stop"], capture_output=True, timeout=8)
    time.sleep(1)

    # Start daemon
    subprocess_run([BINARY, "daemon", "--serial", args.serial], capture_output=True, timeout=30)
    time.sleep(5)

    sock_path = f"/tmp/phonefast-{os.getuid()}.sock"

    # Verify connection
    try:
        dt, _, _ = call(sock_path, args.serial, "status", timeout=5)
        print(f"Daemon ready ({dt:.0f}ms)")
    except Exception as e:
        print(f"FATAL: Cannot connect: {e}")
        return 1

    # Define test cases
    modes_map = {
        "summary": ("get_ui_elements", {"summary": True}),
        "dump": ("get_ui_elements", {"summary": False}),
        "full": ("observe", {"format": "flatref"}),
    }
    if args.mode == "all":
        modes = ["summary", "dump", "full"]
    else:
        modes = [args.mode]

    # Results storage
    times_by_mode = {m: [] for m in modes}
    errors_by_mode = {m: [] for m in modes}
    total_calls = 0
    total_errors = 0

    duration_s = args.duration * 60
    start = time.time()

    print(f"\n{'='*60}")
    print(f"  UI Dump Stress Test")
    print(f"  Device: {args.serial}")
    print(f"  Duration: {args.duration} min")
    print(f"  Interval: {args.interval}s")
    print(f"  Modes: {', '.join(modes)}")
    print(f"{'='*60}\n")

    mode_idx = 0
    try:
        while time.time() - start < duration_s:
            mode = modes[mode_idx % len(modes)]
            method, params = modes_map[mode]
            mode_idx += 1

            t0 = time.time()
            dt, count, err = call(sock_path, args.serial, method, params, timeout=10)
            actual_interval = time.time() - t0

            total_calls += 1
            if err:
                total_errors += 1
                errors_by_mode[mode].append((time.time() - start, err))
                times_by_mode[mode].append(dt)
                print(f"\r  [{total_calls:4d}] {mode:8s} ERROR: {err[:60]}")
            else:
                times_by_mode[mode].append(dt)
                elapsed = time.time() - start
                pct = elapsed / duration_s * 100
                print(f"\r  [{total_calls:4d}] {mode:8s} {dt:6.0f}ms  {count:4d} elems  "
                      f"({pct:4.0f}%  {int(elapsed//60)}:{int(elapsed%60):02d}/{args.duration}:00)"
                      f"  err={total_errors}", end="", flush=True)

            sleep_time = max(0.01, args.interval - actual_interval * 0.3)
            time.sleep(sleep_time)

    except KeyboardInterrupt:
        print("\n\n  Interrupted, generating report...")

    # Generate report
    print(f"\n\n{'='*60}")
    print(f"  UI Dump Stress Test Report")
    print(f"{'='*60}")
    print(f"  Total calls: {total_calls}")
    print(f"  Total errors: {total_errors}")
    print(f"  Success rate: {(total_calls - total_errors) / max(total_calls, 1) * 100:.1f}%")
    print()
    for mode in modes:
        times = times_by_mode[mode]
        errors = errors_by_mode[mode]
        n = len(times)
        ok = n - len(errors)
        print(f"  {mode:8s}: {n:4d} calls, {ok:4d} ok, {len(errors):3d} errors  "
              f"({ok/max(n,1)*100:5.1f}%)  {fmt_stats(times)}")
        if errors:
            for t, e in errors[:3]:
                print(f"           [{t:.0f}s] {e[:80]}")
            if len(errors) > 3:
                print(f"           ... ({len(errors) - 3} more)")
    print(f"\n{'='*60}")

    # Cleanup
    subprocess_run([BINARY, "daemon", "--stop"], capture_output=True, timeout=5)
    return 0 if total_errors == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
