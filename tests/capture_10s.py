#!/usr/bin/env python3
"""10-second video + UI dump capture using phonefast scrcpy sockets."""
import socket, struct, json, subprocess, time, os, sys

SERIAL = None
for line in subprocess.run(["adb", "devices"], capture_output=True, text=True).stdout.strip().split("\n")[1:]:
    if "\tdevice" in line:
        SERIAL = line.split()[0]
        break
if not SERIAL:
    print("No device connected"); sys.exit(1)
print(f"Device: {SERIAL}")

OUT = os.path.expanduser("~/Desktop/phonefast/capture_output")
os.makedirs(OUT, exist_ok=True)

def adb(args):
    return subprocess.run(["adb", "-s", SERIAL] + args, capture_output=True, text=True)

APK = os.path.expanduser("~/Desktop/phonefast/android/scrcpy-server.jar")
VIDEO_PORT = 27183
UI_PORT = 27193

# 1. Cleanup & Deploy
print("\n=== Deploy ===")
adb(["shell", "pkill", "-f", "scrcpy"]); time.sleep(0.5)
adb(["forward", "--remove-all"]); time.sleep(0.3)
adb(["push", APK, "/data/local/tmp/pf-server.apk"])

print("Starting server...")
adb(["shell",
    "CLASSPATH=/data/local/tmp/pf-server.apk",
    "app_process", "/",
    "com.genymobile.scrcpy.Server",
    "3.3.4", "scid=3f",
    "tunnel_forward=true", "control=true", "video=true",
    "audio=false", "send_dummy_byte=true", "send_device_meta=false",
    "max_size=1080", "max_fps=15", "cleanup=false",
    ">", "/dev/null", "2>&1", "&"
])
time.sleep(3)
pid = adb(["shell", "pidof", "app_process"]).stdout.strip()
print(f"Server PID: {pid}")

# 2. Forward sockets
adb(["forward", f"tcp:{VIDEO_PORT}", "localabstract:scrcpy_0000003f"])
adb(["forward", f"tcp:{UI_PORT}",  "localabstract:scrcpy_0000003f_ui"])

# 3. Connect video+control (unblocks server)
vid = socket.socket(); vid.connect(("localhost", VIDEO_PORT)); vid.settimeout(5)
ctrl = socket.socket(); ctrl.connect(("localhost", VIDEO_PORT)); ctrl.settimeout(5)
vid.recv(1)  # dummy byte

# 4. Wait for UISocketHandler to be ready
print("Waiting for UISocketHandler...")
time.sleep(1)

# 5. Video handshake
vh = vid.recv(12)
codec = struct.unpack('>I', vh[0:4])[0]
w, h = struct.unpack('>I', vh[4:8])[0], struct.unpack('>I', vh[8:12])[0]
print(f"Video: {w}x{h}")

# 6. 10-second capture
print("\n=== 10s Capture ===\n")
t0 = time.time()
sps_pps = None
frame_count = 0
keyframes = []
ui_dumps = []

video_raw = open(f"{OUT}/video_keyframes.bin", "wb")

while time.time() - t0 < 10:
    vid.settimeout(0.1)
    try:
        fh = vid.recv(12)
        sz = struct.unpack('>I', fh[8:12])[0]
        ptsf = struct.unpack('>Q', fh[0:8])[0]
        data = vid.recv(sz)
        is_cfg = (ptsf >> 63) & 1
        is_key = (ptsf >> 62) & 1

        if is_cfg: sps_pps = data
        if is_key and not is_cfg:
            ts = time.time() - t0
            pts = ptsf & 0x3FFFFFFFFFFFFFFF
            video_raw.write(struct.pack('>dI', ts, sz))
            video_raw.write(data)
            keyframes.append({"time": round(ts, 3), "pts": pts, "size": sz})
        frame_count += 1
    except socket.timeout: pass

    # UI dump every 2 seconds with fresh connection
    if ui_dumps == [] or time.time() - t0 > len(ui_dumps) * 2:
        try:
            ui = socket.socket(); ui.connect(("localhost", UI_PORT)); ui.settimeout(2)
            ui.send(b"dump\x00")
            l = ui.recv(4)
            if len(l) == 4:
                ulen = struct.unpack('>I', l)[0]
                uid = ui.recv(ulen)
                j = json.loads(uid)
                els = j.get('elements', j)
                ts = time.time() - t0
                ui_dumps.append({"time": round(ts, 3), "elements": j, "count": len(els)})
                print(f"  UI  [{ts:5.1f}s]: {len(els)} elements")
            ui.close()
        except Exception as e:
            print(f"  UI  error: {e}")

video_raw.close()

# 7. Final screenshot
print(f"\n{frame_count} frames, {len(keyframes)} keyframes, {len(ui_dumps)} UI dumps")
print("Taking final screenshot...")

ctrl.send(bytes([17]))  # RESET_VIDEO
screenshot_bytes = 0
for _ in range(60):
    vid.settimeout(0.5)
    try:
        fh = vid.recv(12)
        sz = struct.unpack('>I', fh[8:12])[0]
        ptsf = struct.unpack('>Q', fh[0:8])[0]
        data = vid.recv(sz)
        if (ptsf>>62)&1 and not ((ptsf>>63)&1) and sps_pps:
            full = sps_pps + data
            proc = subprocess.run(
                ['ffmpeg', '-y', '-f', 'h264', '-i', 'pipe:0',
                 '-frames:v', '1', '-f', 'image2pipe', '-vcodec', 'png', 'pipe:1'],
                input=full, capture_output=True, timeout=5
            )
            open(f"{OUT}/screenshot.png", 'wb').write(proc.stdout)
            screenshot_bytes = len(proc.stdout)
            print(f"Screenshot: {screenshot_bytes} bytes → screenshot.png")
            break
    except socket.timeout: pass

# 8. Final UI dump
try:
    ui = socket.socket(); ui.connect(("localhost", UI_PORT)); ui.settimeout(2)
    ui.send(b"dump\x00")
    l = ui.recv(4)
    if len(l) == 4:
        ulen = struct.unpack('>I', l)[0]
        uid = ui.recv(ulen)
        j = json.loads(uid)
        open(f"{OUT}/ui_dump_final.json", "w").write(json.dumps(j, indent=2))
        print(f"Final UI dump: {len(j.get('elements',j))} elements")
    ui.close()
except Exception as e:
    print(f"Final UI error: {e}")

# 9. Save summary
json.dump({
    "device": SERIAL,
    "video": {"width": w, "height": h},
    "duration_s": round(time.time()-t0, 1),
    "frames": frame_count,
    "keyframes": len(keyframes),
    "ui_dumps": len(ui_dumps),
    "screenshot_bytes": screenshot_bytes,
}, open(f"{OUT}/summary.json", "w"), indent=2)

open(f"{OUT}/keyframes.json", "w").write(json.dumps(keyframes, indent=2))
open(f"{OUT}/ui_dumps.json", "w").write(json.dumps(ui_dumps, indent=2))

vid.close(); ctrl.close()
print(f"\n===== DONE =====")
print(f"Output: {OUT}/")
for f in sorted(os.listdir(OUT)):
    sz = os.path.getsize(f"{OUT}/{f}")
    print(f"  {f}: {sz:,} bytes")
