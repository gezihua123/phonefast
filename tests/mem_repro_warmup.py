#!/usr/bin/env python3
"""Replay the stress test's Warmup profile (1 op/s, full op mix, per-call conn)
against a pprof-enabled daemon, sampling RSS every 2s. ~6 minutes.

Usage: python3 tests/mem_repro_warmup.py [duration_s]
"""
import socket, json, time, sys, os, subprocess, threading, csv

SERIAL = "13709314CF044927"
SOCK = "/tmp/phonefast-501.sock"
DURATION = int(sys.argv[1]) if len(sys.argv) > 1 else 360
INTERVAL = 1.0

OPS = [
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
    ("wait",          {"duration_ms": 30}),
]

samples = []
stop = threading.Event()


def daemon_pid():
    r = subprocess.run(["pgrep", "-f", "phonefast.*daemon_worker"], capture_output=True, text=True)
    p = r.stdout.strip().split("\n")
    return int(p[0]) if p and p[0] else None


def sampler():
    t0 = time.time()
    while not stop.is_set():
        pid = daemon_pid()
        if pid:
            r = subprocess.run(["ps", "-o", "rss=", "-p", str(pid)], capture_output=True, text=True)
            if r.stdout.strip():
                samples.append((time.time() - t0, int(r.stdout.strip()) / 1024.0))
        time.sleep(2)


def call(method, params, timeout=30):
    params = dict(params)
    params.setdefault("device", SERIAL)
    req = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(timeout)
    sock.connect(SOCK)
    sock.sendall((req + "\n").encode())
    buf = b""
    while b"\n" not in buf:
        chunk = sock.recv(1 << 20)
        if not chunk:
            sock.close()
            return "connection closed"
        buf += chunk
    sock.close()
    try:
        resp = json.loads(buf.decode(errors="replace").split("\n")[0])
        return resp.get("error")
    except Exception as e:
        return f"parse: {e}"


def main():
    th = threading.Thread(target=sampler, daemon=True)
    th.start()
    t0 = time.time()
    i = 0
    errs = 0
    while time.time() - t0 < DURATION:
        name, params = OPS[i % len(OPS)]
        err = call(name, params)
        if err:
            errs += 1
            print(f"  [ERR] {name}: {str(err)[:120]}", flush=True)
        i += 1
        dt = time.time() - t0
        if dt < i * INTERVAL:
            time.sleep(i * INTERVAL - dt)
    stop.set()
    out_dir = os.path.join(os.path.dirname(__file__), "..", "test_runs", "mem_repro")
    os.makedirs(out_dir, exist_ok=True)
    path = os.path.join(out_dir, "rss_repro.csv")
    with open(path, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["elapsed_s", "rss_mb"])
        w.writerows(samples)
    print(f"done: {i} ops, {errs} errors; rss samples -> {path}", flush=True)
    if samples:
        print(f"RSS {samples[0][1]:.1f} -> {samples[-1][1]:.1f} MB", flush=True)


if __name__ == "__main__":
    main()
