#!/usr/bin/env python3
"""Isolate which op class drives daemon RSS growth (macOS).

Phases: baseline -> screenshot-only -> ui-only -> observe-only -> tap-only,
sampling daemon RSS every 2s. Ends with vmmap/footprint anatomy of the
steady-state daemon. Read-only w.r.t. the device (no state changes beyond
what stress_test does).
"""
import subprocess, threading, time, os, sys, csv

BIN = os.path.join(os.path.dirname(__file__), "..", "dist", "dev", "phonefast-darwin-arm64")
SERIAL = "13709314CF044927"
SAMPLE_INTERVAL = 2.0
SETTLE_S = 20

samples = []          # (elapsed, phase, rss_mb)
stop_flag = threading.Event()


def daemon_pid():
    r = subprocess.run(["pgrep", "-f", "phonefast.*daemon_worker"],
                       capture_output=True, text=True)
    pids = r.stdout.strip().split("\n")
    return int(pids[0]) if pids and pids[0] else None


def rss_mb(pid):
    r = subprocess.run(["ps", "-o", "rss=", "-p", str(pid)],
                       capture_output=True, text=True)
    return int(r.stdout.strip()) / 1024.0 if r.stdout.strip() else None


def sampler(pid):
    t0 = time.time()
    while not stop_flag.is_set():
        m = rss_mb(pid)
        if m is not None:
            samples.append((time.time() - t0, current_phase[0], m))
        time.sleep(SAMPLE_INTERVAL)


current_phase = ["init"]


def run_ops(phase, cmd_args, count, note=""):
    current_phase[0] = phase
    pid = daemon_pid()
    before = rss_mb(pid)
    t0 = time.time()
    fails = 0
    for i in range(count):
        r = subprocess.run([BIN, "-s", SERIAL] + cmd_args,
                           capture_output=True, timeout=15)
        if r.returncode != 0:
            fails += 1
    dur = time.time() - t0
    time.sleep(SETTLE_S)  # settle: let GC/scavenger run
    after = rss_mb(daemon_pid())
    print(f"[{phase:11s}] {count:4d} ops in {dur:5.1f}s  fails={fails}  "
          f"RSS {before:.1f} -> {after:.1f} MB (Δ{after - before:+.1f}) {note}",
          flush=True)


def main():
    subprocess.run([BIN, "daemon", "--stop"], capture_output=True, timeout=5)
    time.sleep(1)
    subprocess.run([BIN, "-s", SERIAL, "daemon"], capture_output=True, timeout=10)
    for _ in range(30):
        if daemon_pid():
            break
        time.sleep(1)
    pid = daemon_pid()
    if not pid:
        print("FATAL: daemon did not start")
        return 1
    print(f"daemon pid={pid}", flush=True)

    th = threading.Thread(target=sampler, args=(pid,), daemon=True)
    th.start()

    time.sleep(10)  # warm-up: scrcpy connect, first frames
    print(f"[baseline  ] RSS {rss_mb(pid):.1f} MB", flush=True)

    # One warm call per op type (JIT caches, first-frame buffers)
    for args in (["screenshot"], ["ui"], ["observe"], ["tap", "540", "960"]):
        subprocess.run([BIN, "-s", SERIAL] + args, capture_output=True, timeout=15)
    time.sleep(5)
    print(f"[warmed    ] RSS {rss_mb(pid):.1f} MB", flush=True)

    run_ops("screenshot", ["screenshot"], 200)
    run_ops("ui", ["ui"], 200)
    run_ops("observe", ["observe"], 200)
    run_ops("tap", ["tap", "540", "960"], 500)

    current_phase[0] = "idle"
    time.sleep(30)  # long settle: macOS scavenger window
    pid = daemon_pid()
    print(f"[idle-30s  ] RSS {rss_mb(pid):.1f} MB", flush=True)

    out = os.path.join(os.path.dirname(__file__), "..", "test_runs",
                       f"mem_isolation_{time.strftime('%Y%m%d_%H%M%S')}")
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, "rss_timeline.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["elapsed_s", "phase", "rss_mb"])
        w.writerows(samples)

    for tool, args in (("footprint", [str(pid)]),
                       ("vmmap", ["-summary", str(pid)])):
        r = subprocess.run([tool] + args, capture_output=True, text=True, timeout=30)
        with open(os.path.join(out, f"{tool}.txt"), "w") as f:
            f.write(r.stdout + r.stderr)
    print(f"output: {out}/", flush=True)

    stop_flag.set()
    subprocess.run([BIN, "daemon", "--stop"], capture_output=True, timeout=5)
    return 0


if __name__ == "__main__":
    sys.exit(main())
