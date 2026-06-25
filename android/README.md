# Android Server

Custom scrcpy server with phonefast extensions for fast UI hierarchy dumps.

## File layout

```
android/
├── scrcpy-server.jar       Pre-built server binary
├── patches/                 phonefast patches for scrcpy upstream
│   └── 0001-phonefast-uisocket.patch
├── phonefast-agent/         phonefast additions (source reference)
│   ├── UISocketHandler.java   New: UI dump via abstract socket
│   └── README.md
├── README.md
└── server.patch            (legacy, use patches/ instead)
```

## Building scrcpy-server.jar

```bash
# One-command build: clone scrcpy v3.3.4 → patch → build → copy jar
bash scripts/build-server.sh

# Or use existing scrcpy clone
bash scripts/build-server.sh ~/Desktop/code/scrcpy
```

### Manual build

```bash
# 1. Clone scrcpy v3.3.4
git clone --depth 1 --branch v3.3.4 https://github.com/Genymobile/scrcpy.git

# 2. Apply phonefast patch
cd scrcpy
git apply /path/to/android/patches/0001-phonefast-uisocket.patch

# 3. Build
cd server
./gradlew assembleRelease

# 4. Copy result
cp build/outputs/apk/release/server-release-unsigned.apk \
   /path/to/android/scrcpy-server.jar
```

## What the patch adds

| File | Change | Lines |
|------|--------|-------|
| `server/.../control/UISocketHandler.java` | **New** — UI dump socket handler | +266 |
| `server/.../Server.java` | Import + init + start + stop | +14 |

The patch is minimal and non-invasive: it adds a new file and 3 small blocks to `Server.java`.
All original scrcpy functionality is preserved unchanged.
