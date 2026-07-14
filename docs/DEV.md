# phonefast Development Notes

> Investigation records, architectural decisions, and hard-earned lessons from the development process.

## Table of Contents

- [LocalSocket 4-byte Read Limit (Android 14)](#localsocket-4-byte-read-limit-android-14)
- [Build and Release](#build-and-release)

---

## LocalSocket 4-byte Read Limit (Android 14)

### Problem

In `UISocketHandler.handleClient`, the code uses `DataInputStream.readByte()` to read requests byte-by-byte from a `LocalSocket`. When a request exceeds **4 characters** (i.e., `readByte()` is called more than 4 times), Android 14 devices **silently reset the connection**:

```
dump\0    (4 readByte calls + \0) → ✓ OK
dump.\0   (5 readByte calls + \0) → ✗ Connection reset
dump:5\0  (6 readByte calls + \0) → ✗ Connection reset
dumpp\0   (5 readByte calls + \0) → ✗ Connection reset
```

### Investigation Process

1. **Initial observation**: Users reported `get ui elements` returning `exit status 137` (ADB uiautomator dump killed by OOM). But daemon status showed `ui: true`, indicating the fast socket channel was already established.

2. **Fallback identified**: Code analysis revealed that `handleGetUIElements` first attempts the fast socket, then falls back to ADB uiautomator dump on failure. The error message only showed the ADB failure; the fast socket's original error was silently swallowed.

3. **Confirmed socket works**: Using Python to directly connect to the ADB-forwarded port (`localhost:27246`), `dump\0` successfully returned 10KB+ of UI data.

4. **Narrowing down**: Comparative testing showed:
   - `dump\0` → OK
   - `dump:5\0` → Connection closed immediately
   - `dump:500\0` → Connection closed immediately
   - Even `dump.\0` (5 characters) → Connection closed immediately

5. **Key finding**: **Any request exceeding 4 bytes would fail**. Confirmed it was a problem with the number of `readByte()` calls, not the content.

6. **Root cause**: A compatibility bug in Android 14's `LocalSocket` + `DataInputStream.readByte()`. After more than 4 consecutive `readByte()` calls, the underlying native `read()` call causes the socket connection to be silently reset. This differs from `read(byte[], int, int)` bulk reads, which use a different native code path.

### Fix

**Core approach**: Use `InputStream.read(byte[], int, int)` to read the first 4 bytes in batch (1 native call), then use `read()` byte-by-byte for the remaining portion.

```java
// Before (problematic byte-by-byte reading):
byte[] req = readUntilNull(in); // Internally loops calling readByte()

// After (bulk read first 4 bytes + byte-by-byte for the rest):
byte[] prefix = new byte[4];
in.read(prefix, 0, 4);        // Bulk read, 1 native call
int sep = in.read();           // Separator (':' or '\0')
if (sep == ':') {
    // Parse number byte-by-byte
    int b = in.read();
    ...
}
```

**Protocol remains compatible**: `dump:N\0` format unchanged; Go side still sends `:N` parameters as before. Java side parses using the new read method.

**Files involved**:
- `android/phonefast-agent/UISocketHandler.java` — Java-side fix
- `pkg/protocol/ui.go` — Go-side request writing (format unchanged)
- `internal/session/ui.go` — Client-side defensive truncation
- `scripts/build-server.sh` — Auto-overwrites with latest source during build

### Verification

| Request | Expected | Result |
|---|---|---|
| `dump\0` | Default 500 elements | ✓ |
| `dump:5\0` | 5 elements | ✓ |
| `dump:500\0` | 500 elements | ✓ |
| `dump:10000\0` | Parse 10000 → cap at 500 | ✓ |
| `dump:-5\0` | Invalid character → default 500 | ✓ |
| `dump:5a\0` | Partial parse → default 500 | ✓ |
| `sum\0` | 50 elements (summary) | ✓ |
| `sum:3\0` | 3 elements (summary) | ✓ |

### Lessons Learned

- **Never assume `DataInputStream.readByte()` behaves consistently across all devices**. Android fragmentation is severe; the underlying `SocketInputStream` implementation varies by vendor customization.
- **The fast socket error was swallowed by the fallback path**, causing users to only see `exit 137`. Original errors should be logged when falling back.
- **Using Python/nc to directly connect to the socket is an excellent debugging technique** — it bypasses the Go code's fallback logic and lets you observe the Java server's raw behavior directly.

---

## Build and Release

### H.264 Screenshot Decoding Architecture (v1.0.11)

phonefast's screenshot pipeline extracts IDR keyframes from the Android device's scrcpy H.264 video stream and decodes them into PNG images.
There are two paths, selected at compile time:

#### Primary Path: astiav CGO In-Process Decoding (default)

`pkg/avcodec/decode_astiav.go` — Uses the go-astiav library to directly call the libavcodec/libswscale C API.

```go
import "github.com/asticode/go-astiav"

decoder, _ := astiav.NewDecoder(codecID)
ctx := astiav.AllocCodecContext(codec)
ctx.SetThreadCount(1)  // Single-frame IDR doesn't need multi-threading
ctx.Open(decoder, nil)

// Each screenshot:
pkt := astiav.AllocPacket()
pkt.SetData(keyframe)  // H.264 AnnexB bytes
ctx.SendPacket(pkt)

frame := astiav.AllocFrame()
ctx.ReceiveFrame(frame)

// sws_scale → PNG
sws := astiav.AllocSwsContext(...)
sws.Scale(frame, rgbaFrame)
astiav.EncodeImage(rgbaFrame, pngBytes, astiav.FormatPNG)
```

**Key optimizations**:
- ThreadCount=1: A single 488×1080 frame has minimal decoding workload; multi-thread slice synchronization overhead exceeds the decoding itself
- Persistent CodecContext: Not reusing it would cost an extra +55ms each time (SPS/PPS re-parsing + DPB reconstruction)
- Simplified frame loop: IDR outputs exactly 1 frame, no need for the older AllocFrame probe loop

#### Fallback Path: ffmpeg CLI Subprocess (CGO_ENABLED=0)

`internal/session/video.go` — Launches an ffmpeg subprocess via `exec.CommandContext`, passing data through stdin/stdout pipes.

```go
cmd := exec.CommandContext(ctx, "ffmpeg",
    "-f", "h264",
    "-i", "pipe:0",
    "-vcodec", "png",
    "-f", "image2pipe",
    "pipe:1",
)
stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()
stdin.Write(keyframe)
stdin.Close()
pngBytes, _ := io.ReadAll(stdout)
cmd.Wait()
```

**Overhead**: fork+exec ~50-80ms + SPS/PPS re-parsing + pipe memcpy ≈ 100-200ms/request

#### Code Structure

```
pkg/avcodec/
├── avcodec.go         — Package docs + public types (ImageFormat, DecodeError, ErrNotAvailable)
├── decode.go          — Decoder interface definition
├── decode_astiav.go   — Primary path: CGO decoder (build tag: cgo)
├── decode_nocgo.go    — Fallback stub: returns ErrNotAvailable (build tag: !cgo)
├── decode_test.go     — Tests (go test + fuzz)
└── testdata/          — Test fixtures

internal/session/
└── video.go           — keyframeToPNG + decodeViaFFmpeg (fallback path)
```

Build-time selection via build tag:

```bash
go build -tags cgo ./cmd/phonefast/    # Primary path (default, CGO_ENABLED=1)
CGO_ENABLED=0 go build ./cmd/phonefast/ # Fallback path (requires ffmpeg on system)
```

### Building the Server Jar

```bash
bash scripts/build-server.sh
```

Process:
1. Clone scrcpy v3.3.4
2. Apply `android/patches/0001-phonefast-uisocket.patch`
3. Overwrite with `android/phonefast-agent/UISocketHandler.java` (keeping latest version)
4. Gradle builds the server APK
5. Copy to `android/scrcpy-server.jar` and `assets/scrcpy-server.jar`

**Note**: The patches in `android/patches/` are baseline versions; the latest code lives in `android/phonefast-agent/`. The build script auto-overwrites during the process.

### Cross-Platform Build

phonefast statically links FFmpeg into the Go CGO binary, enabling single-file distribution (jar + FFmpeg all embedded).
There are two build paths:

#### Option 2: Local Zig Cross-Compilation (daily development)

Build all 4 platform binaries with a single command on macOS. The native darwin-arm64 target uses native clang;
other targets use zig cc for cross-compilation (asm fully enabled, verified). `build_local.sh` provides a one-click wrapper:

```bash
# One-click all platforms (auto: env check → build FFmpeg libs → build Go binaries)
bash scripts/build_local.sh            # All 4 targets
bash scripts/build_local.sh --macos    # darwin only
bash scripts/build_local.sh --linux    # linux only
bash scripts/build_local.sh --windows  # windows only
bash scripts/build_local.sh --clean    # Clean dist/ before building
```

Underlying steps (automated by build_local.sh, but can also be run manually):

```bash
# 1. Check/install build environment (nasm/zig/clang/go)
bash scripts/build_env.sh check        # Check
bash scripts/build_env.sh install      # Auto-install missing deps (brew)

# 2. Build static FFmpeg libraries (one per target, cached in build/cross-ffmpeg/<target>/)
bash scripts/cross-build-ffmpeg.sh aarch64-darwin    # mac arm64
bash scripts/cross-build-ffmpeg.sh x86_64-linux-gnu  # linux amd64
bash scripts/cross-build-ffmpeg.sh aarch64-linux-gnu # linux arm64
bash scripts/cross-build-ffmpeg.sh x86_64-windows-gnu # windows amd64

# 3. Build
bash scripts/build.sh            # Native darwin-arm64 only (default)
bash scripts/build.sh --all      # All 4 targets
bash scripts/build.sh --linux    # linux only
```

Artifacts go to `dist/dev/`: `phonefast-<os>-<arch>[.exe]` + `.tar.gz`.

#### Option 3: CI Native Runners (official releases)

Each platform uses a native GitHub Actions runner for its architecture — zero emulation, full asm support, maximum reliability.
Pushing a `v*` tag triggers automatically: `.github/workflows/release.yml`.

```bash
# Push a tag locally to trigger CI full-platform build + release
git tag v1.0.8 && git push origin v1.0.8
# Or manually run workflow from Actions tab (enter version number)
```

CI matrix (native runner per platform):

| Target | runner | FFmpeg Toolchain |
|---|---|---|
| darwin-arm64 | macos-14 | Native clang + nasm |
| darwin-arm64 | macos-14 | Native clang |
| linux-amd64 | ubuntu-latest | Native gcc + nasm |
| linux-arm64 | ubuntu-24.04-arm | Native gcc (NEON) |
| windows-amd64 | windows-latest | Native mingw + nasm |

> Public repository CI is fully free (including macOS runners and arm64 linux runners).

### Build Environment and ASM Detection

`scripts/build_env.sh` is the unified environment detection/installation entry point:

```bash
bash scripts/build_env.sh           # Report
bash scripts/build_env.sh check     # Check, returns non-zero if deps missing
bash scripts/build_env.sh install   # Auto-install missing deps via brew (nasm/zig/go)
```

**ASM detection logic** (`cross-build-ffmpeg.sh`):
- `x86_64` targets: require nasm (SSE/AVX/AVX2/AVX-512). If available, enable; otherwise fall back to `--disable-asm` (pure C, 2-4× slower).
- `aarch64` targets: NEON uses assembler (zig built-in / clang gas), no nasm required, always enabled.
- Install nasm for full-platform asm-on: `bash scripts/build_env.sh install`.

### Key Pitfalls in Static FFmpeg Compilation

Pitfalls encountered and fixed in `cross-build-ffmpeg.sh` (for future reference):

1. **zig + nasm with asm enabled**: Early versions hardcoded `--disable-asm` because nasm wasn't installed. After installing nasm, zig cc auto-detects it, resulting in full-platform asm-on. Runtime verification: asm-off logs `No accelerated colorspace conversion found`; after asm-on, the warning disappears for amd64 targets.

2. **Darwin forced to use Apple native ar/ranlib**: If GNU binutils (`brew install binutils`) is on PATH, its ar produces GNU-format `.a` archives (symbol table member named `/`), which Apple's ld rejects with `archive member '/' not a mach-o file`. The darwin branch forces using `/usr/bin/ar`, `/usr/bin/ranlib`, and `/usr/bin/nm` to produce BSD-format `.a` archives, eliminating the need for libtool re-wrapping.

3. **Cannot use libtool to re-wrap darwin .a**: Earlier, `ar -x` + `libtool -static *.o` was used to re-wrap archives to fix SYMDEF. However, `ar -x` causes `.o` files with the same name in aarch64 and the root directory (e.g., `aarch64/swscale.o` vs `swscale.o`) to overwrite each other, losing the NEON init symbol → `symbol(s) not found for architecture arm64`. Since Apple's native ar already generates valid SYMDEF, the re-wrapping step has been removed.

4. **MinGW C99 math conflict** (windows target): Under zig-mingw, all configure math function probes fail (`HAVE_TRUNC/ROUND/CBRT/...=0`). FFmpeg's `libm.h` redefines these with static inline, conflicting with mingw math.h's extern declarations → `static declaration follows non-static declaration`. The `mingw_math` patch flips those `HAVE_*` values to 1 (using mingw system versions) and comments out the conflicting `#define getenv(x) NULL`.

5. **GCC + PIC + x86 inline asm** (Linux-host branch): `--enable-pic` + GCC triggers `impossible constraint in 'asm'` (mathops.h NEG_USR32). Linux static libraries linked into Go do not need PIC (the Go linker handles relocation itself), so the Linux-host branch does not enable PIC. The zig branch is unaffected by this (PIC works fine, verified).

### Why Not Use Docker

Building amd64 on an arm64 Mac with Docker requires QEMU emulation. When GCC compiles FFmpeg's SIMD/asm under emulation, it triggers `internal compiler error: Segmentation fault` (QEMU+GCC is a known unstable combination with no reliable workaround). Industry consensus: if cross-compilation is available, avoid emulation. Therefore, linux/windows targets use zig cross-compilation (Option 2) or CI native runners (Option 3); the Docker approach has been removed.

### GitHub Release

`release.sh` is only responsible for triggering CI — it does not build locally or directly create a Release.
After pushing a `v*` tag, GitHub Actions (Option 3) compiles natively on all platforms and publishes the release.

```bash
# Dry-run preview (no tag, no CI trigger)
bash scripts/release.sh --dry-run

# Auto-version-bump + trigger CI release
bash scripts/release.sh

# Specify a version
bash scripts/release.sh 1.0.8
```

Prerequisites:
- Git (required, for pushing tags)
- gh (optional, for checking CI/Release status afterward)

Release flow:
1. Check working directory is clean
2. Auto-patch version number increment + commit
3. Create Git tag `v${VERSION}`
4. Push tag → **triggers CI** → CI builds natively on 4 platforms → publishes GitHub Release

Final artifacts: GitHub Release Assets
`https://github.com/gezihua123/phonefast/releases/tag/vX.Y.Z`

> Use `bash scripts/build_local.sh` (Option 2) for local manual packaging (without publishing).
> CI artifacts and local artifacts can be cross-verified.
