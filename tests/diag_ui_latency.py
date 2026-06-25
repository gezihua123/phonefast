#!/usr/bin/env python3
"""Diagnose get_ui_elements latency — 100 samples, raw per-call timing."""
import socket, json, time, os, subprocess, sys

BINARY = os.path.join(os.path.dirname(__file__), "..", "dist", "phonefast")
UID = os.getuid()
SOCK = f"/tmp/phonefast-{UID}.sock"

def ensure_daemon():
    r = subprocess.run([BINARY, "daemon", "--status"],
                       capture_output=True, text=True, timeout=5)
    if "running" not in r.stdout.lower():
        print("Starting daemon...")
        subprocess.run([BINARY, "daemon", "--stop"],
                      capture_output=True, timeout=5)
        time.sleep(0.5)
        subprocess.run([BINARY, "daemon"], capture_output=True, timeout=20)
        time.sleep(3)
    print(f"Daemon ready, socket: {SOCK}")

def call_ui():
    """Single get_ui_elements call, return (elapsed_ms, element_count, json_bytes)."""
    req = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "get_ui_elements", "params": {}})
    payload = (req + "\n").encode()

    t0 = time.time()
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(15)
    sock.connect(SOCK)
    sock.sendall(payload)
    buf = b""
    while b"\n" not in buf:
        chunk = sock.recv(65536)
        if not chunk:
            break
        buf += chunk
    dt = (time.time() - t0) * 1000
    sock.close()

    line = buf.split(b"\n")[0]
    resp = json.loads(line)
    result = resp.get("result", {})
    elements = result.get("elements", [])
    return dt, len(elements), len(buf)

# ── Main ──
ensure_daemon()

print(f"\n{'='*60}")
print(f"  get_ui_elements 诊断 — 100 次采样")
print(f"{'='*60}\n")

samples = []
for i in range(100):
    try:
        dt, cnt, sz = call_ui()
        samples.append(dt)
        bar = "█" * (i // 5) + "░" * (20 - i // 5)
        print(f"\r  [{bar}] {i+1:3d}/100  {dt:6.1f}ms  {cnt:4d} elements  {sz:6d}B  ", end="", flush=True)
    except Exception as e:
        print(f"\n  [{i+1}] ERROR: {e}")
    time.sleep(0.3)
print("\n")

# ── Analysis ──
s = sorted(samples)
n = len(s)
print(f"  Samples: {n}")
print(f"  Min:    {s[0]:6.1f}ms")
print(f"  P10:    {s[int(n*0.10)]:6.1f}ms")
print(f"  P25:    {s[int(n*0.25)]:6.1f}ms")
print(f"  P50:    {s[n//2]:6.1f}ms")
print(f"  P75:    {s[int(n*0.75)]:6.1f}ms")
print(f"  P90:    {s[int(n*0.90)]:6.1f}ms")
print(f"  P95:    {s[int(n*0.95)]:6.1f}ms")
print(f"  P99:    {s[int(n*0.99)]:6.1f}ms")
print(f"  Max:    {s[-1]:6.1f}ms")
print(f"  Mean:   {sum(s)/n:6.1f}ms")
print(f"  StdDev: {(sum((x-sum(s)/n)**2 for x in s)/n)**0.5:6.1f}ms")

# Histogram
buckets = {}
for v in s:
    key = int(v // 20) * 20
    buckets[key] = buckets.get(key, 0) + 1

print(f"\n  直方图 (每 20ms 桶):")
max_count = max(buckets.values())
for k in sorted(buckets):
    bar = "█" * (buckets[k] * 50 // max_count)
    print(f"  {k:4d}-{k+19:4d}ms: {buckets[k]:3d}  {bar}")
