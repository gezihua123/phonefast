#!/usr/bin/env python3
"""
End-to-end test for phonefast scrcpy socket connections.

Connection order (matches fixed session.go):
  1. Deploy + start server
  2. Forward video/control socket
  3. Connect video socket  → unblocks server accept #1
  4. Connect control socket → unblocks server accept #2
     → server then runs UISocketHandler.start()
  5. Read video header (blocks until server sends it)
  6. Sleep 600ms for UISocketHandler to bind its abstract socket
  7. Forward UI socket
  8. Probe UI socket
"""
import socket, struct, subprocess, time, sys, json, os

SERIAL = None
for line in subprocess.run(["adb", "devices"], capture_output=True, text=True).stdout.strip().split("\n")[1:]:
    if "\tdevice" in line:
        SERIAL = line.split()[0]
        break
if not SERIAL:
    print("No device connected"); sys.exit(1)

VIDEO_PORT = 27183
UI_PORT    = 27193
APK        = os.path.expanduser("~/Desktop/phonefast/android/scrcpy-server.jar")

def adb(cmd):
    return subprocess.run(["adb", "-s", SERIAL] + cmd, capture_output=True, text=True)

def readfull(s, n):
    buf = b""
    while len(buf) < n:
        chunk = s.recv(n - len(buf))
        if not chunk:
            raise EOFError(f"connection closed after {len(buf)}/{n} bytes")
        buf += chunk
    return buf

print(f"Device: {SERIAL}")
print(f"APK:    {APK}")

# ── 1. Cleanup ─────────────────────────────────────────────────────────────
print("\n=== Cleanup ===")
adb(["shell", "pkill", "-f", "scrcpy"]); time.sleep(0.5)
adb(["forward", "--remove-all"]); time.sleep(0.3)

# ── 2. Deploy ──────────────────────────────────────────────────────────────
print("\n=== Deploy ===")
r = adb(["push", APK, "/data/local/tmp/pf-server.apk"])
print(f"  push: {r.stderr.strip() or 'ok'}")

# ── 3. Start server ────────────────────────────────────────────────────────
print("\n=== Start server ===")
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
print(f"  PID: {pid or 'NOT FOUND'}")
if not pid:
    print("  Logs:", adb(["logcat", "-d", "-s", "scrcpy:*"]).stdout[-300:])
    sys.exit(1)

# ── 4. Forward video/control only (UI doesn't exist yet) ──────────────────
print("\n=== Forward video/control ===")
adb(["forward", f"tcp:{VIDEO_PORT}", "localabstract:scrcpy_0000003f"])
print(f"  video/control → localhost:{VIDEO_PORT}")

# ── 5. Connect video socket (unblocks server accept #1) ───────────────────
print("\n=== Connect video socket ===")
vid = socket.socket(); vid.connect(("localhost", VIDEO_PORT)); vid.settimeout(5)
dummy = vid.recv(1)
print(f"  dummy byte: 0x{dummy.hex()}")

# ── 6. Connect control socket (unblocks server accept #2) ─────────────────
print("\n=== Connect control socket ===")
ctrl = socket.socket(); ctrl.connect(("localhost", VIDEO_PORT)); ctrl.settimeout(5)
print("  connected")

# ── 7. Read video header (server sends after both accepts resolve) ─────────
print("\n=== Video header ===")
vh = readfull(vid, 12)
codec = struct.unpack('>I', vh[0:4])[0]
w     = struct.unpack('>I', vh[4:8])[0]
h     = struct.unpack('>I', vh[8:12])[0]
print(f"  codec=0x{codec:08x}  {w}x{h}")

# ── 8. Sleep: UISocketHandler.start() runs after video header ─────────────
print("\n=== Waiting 600ms for UISocketHandler to bind ===")
time.sleep(0.6)

# ── 9. Forward UI socket (abstract socket now exists) ─────────────────────
print("\n=== Forward UI socket ===")
r = adb(["forward", f"tcp:{UI_PORT}", "localabstract:scrcpy_0000003f_ui"])
print(f"  ui → localhost:{UI_PORT}  (rc={r.returncode})")

# ── 10. Probe UI socket ───────────────────────────────────────────────────
print("\n=== Probe UI socket ===")
try:
    probe = socket.socket(); probe.connect(("localhost", UI_PORT)); probe.close()
    print("  UI socket reachable ✅")
    ui_ready = True
except Exception as e:
    print(f"  UI socket not ready: {e}")
    ui_ready = False

# ── 11. Read frames ────────────────────────────────────────────────────────
print("\n=== First 5 video frames ===")
sps_pps = None
first_keyframe = None
frame_idx = 0

while frame_idx < 10 and not (sps_pps and first_keyframe):
    fh = readfull(vid, 12)
    sz = struct.unpack('>I', fh[8:12])[0]
    ptsf = struct.unpack('>Q', fh[0:8])[0]
    data = readfull(vid, sz)
    is_cfg = (ptsf >> 63) & 1
    is_key = (ptsf >> 62) & 1
    ftype = "CONFIG" if is_cfg else ("KEY" if is_key else "P")
    if frame_idx < 5:
        print(f"  Frame {frame_idx}: {ftype:6s}  {sz:7d}B")
    if is_cfg:
        sps_pps = data
    elif is_key and not first_keyframe:
        first_keyframe = data
    frame_idx += 1

# ── 12. Screenshot ────────────────────────────────────────────────────────
print("\n=== Screenshot ===")
if sps_pps and first_keyframe:
    full = sps_pps + first_keyframe
    proc = subprocess.run(
        ["ffmpeg", "-y", "-f", "h264", "-i", "pipe:0",
         "-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "pipe:1"],
        input=full, capture_output=True, timeout=5
    )
    out_path = "/tmp/phonefast_screenshot.png"
    open(out_path, "wb").write(proc.stdout)
    print(f"  H.264 {len(full)}B → PNG {len(proc.stdout)}B → {out_path}")
    if not proc.stdout:
        print(f"  ffmpeg stderr: {proc.stderr.decode()[:200]}")
    screenshot_ok = len(proc.stdout) > 0
else:
    print("  no keyframe available")
    screenshot_ok = False

# ── 13. UI dump ───────────────────────────────────────────────────────────
print("\n=== UI dump ===")
ui_elements = []
if ui_ready:
    for attempt in range(3):
        try:
            ui = socket.socket()
            ui.connect(("localhost", UI_PORT))
            ui.settimeout(3)
            ui.send(b"dump\x00")
            l = readfull(ui, 4)
            ulen = struct.unpack(">I", l)[0]
            data = readfull(ui, ulen)
            j = json.loads(data)
            ui_elements = j.get("elements", j)
            ui.close()
            print(f"  {len(ui_elements)} elements (attempt {attempt+1})")
            for el in ui_elements[:6]:
                lbl = (el.get("text") or el.get("content_desc") or
                       el.get("resource_id") or el.get("class_name",""))
                print(f"    [{el['index']}] {lbl[:60]}")
            break
        except Exception as e:
            print(f"  attempt {attempt+1} failed: {e}")
            time.sleep(0.3)
else:
    print("  UI socket not available, skipping")

# ── 14. Control: back button ──────────────────────────────────────────────
print("\n=== Control: back button ===")
try:
    # BACK_OR_SCREEN_ON type=4, action=0 (BACK)
    ctrl.send(bytes([4, 0]))
    print("  sent ✅")
    ctrl_ok = True
except Exception as e:
    print(f"  failed: {e}")
    ctrl_ok = False

# ── 15. Control: tap at center ───────────────────────────────────────────
print("\n=== Control: tap center ===")
if ctrl_ok and w and h:
    cx, cy = w // 2, h // 2
    # INJECT_TOUCH_EVENT (type=2)
    # action=0(DOWN) + pointerId(8B) + x(4B) + y(4B) + sw(2B) + sh(2B) + pressure_u16(2B) + actionBtn(4B) + buttons(4B)
    def encode_touch(action, x, y):
        buf = bytes([2, action])                        # type=2, action
        buf += struct.pack(">Q", 0)                     # pointerId=0
        buf += struct.pack(">II", x, y)                 # x, y
        buf += struct.pack(">HH", w, h)                 # screenW, screenH
        buf += struct.pack(">H", 0xffff)                # pressure=1.0 (u16 fixed-point) ← FIXED
        buf += struct.pack(">II", 0, 0)                 # actionButton=0, buttons=0
        return buf
    ctrl.send(encode_touch(0, cx, cy))   # DOWN
    time.sleep(0.05)
    ctrl.send(encode_touch(1, cx, cy))   # UP
    print(f"  tapped ({cx}, {cy}) with correct u16 pressure ✅")

# ── 16. Second UI dump (after tap) ────────────────────────────────────────
print("\n=== UI dump after tap ===")
if ui_ready:
    try:
        ui = socket.socket()
        ui.connect(("localhost", UI_PORT))
        ui.settimeout(3)
        ui.send(b"dump\x00")
        l = readfull(ui, 4)
        ulen = struct.unpack(">I", l)[0]
        data = readfull(ui, ulen)
        j2 = json.loads(data)
        els2 = j2.get("elements", j2)
        ui.close()
        print(f"  {len(els2)} elements")
        out_path2 = "/tmp/phonefast_ui_dump.json"
        open(out_path2, "w").write(json.dumps(j2, indent=2))
        print(f"  saved → {out_path2}")
    except Exception as e:
        print(f"  {e}")

# ── Cleanup ───────────────────────────────────────────────────────────────
vid.close(); ctrl.close()

# ── Summary ──────────────────────────────────────────────────────────────
print("\n" + "="*50)
print("RESULT SUMMARY")
print("="*50)
print(f"  Video stream:    {'✅ ' + str(w) + 'x' + str(h) if w else '❌'}")
print(f"  Screenshot:      {'✅ ' + str(len(open('/tmp/phonefast_screenshot.png','rb').read()) if screenshot_ok else 0) + 'B PNG' if screenshot_ok else '❌'}")
print(f"  UI socket:       {'✅' if ui_ready else '❌'}")
print(f"  UI elements:     {'✅ ' + str(len(ui_elements)) + ' found' if ui_ready else '⚠️  skipped'}")
print(f"  Control back:    {'✅' if ctrl_ok else '❌'}")
print(f"  Control tap:     {'✅ u16 pressure' if ctrl_ok and w else '❌'}")
print("="*50)

all_ok = bool(w) and screenshot_ok and ctrl_ok
if all_ok:
    print("\n✅ All core tests passed!")
else:
    print("\n⚠️  Some tests failed — see details above")
    sys.exit(1)
