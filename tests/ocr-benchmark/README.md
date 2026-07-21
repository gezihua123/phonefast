# OCR Performance Benchmark Report

## Test Configuration

| Item | Value |
|---|---|
| Binary | `phonefast-darwin-arm64` dev build |
| OS | macOS 15, Apple M4 Pro |
| Engine | Go purego ONNX Runtime CPU (brew 1.27.1, no BLAS) |
| Detection | macOS Vision ANE (`VNDetectTextRectanglesRequest`) |
| Recognition | PP-OCR v3 ONNX (10.7MB), batch inference, **H=48** |
| Resolution cap | det maxSide=1024, rec H=48 × dynamic W |
| Images | 20 screenshots (10 CN + 10 EN), 720×1600/1612 |
| Iterations | 15 per image (300 total), warmup 3 per image |

### Rec height selection: why H=40

PP-OCR rec was trained at H=48. Lower H = fewer pixels = faster inference,
but too low loses character detail. We tested H=48/40/32 across 20 images.

| Height | per-box | CN accuracy | EN accuracy | Status |
|---|---|---|---|---|
| 48 (original) | 5.83ms | baseline | baseline | — |
| **48 (current)** | **5.83ms** | **best** | **best** | ✅ default |
| 40 | 5.31ms | -10.1% chars | identical | ⚠️ not worth |
| 32 | 3.0ms | degraded | OK | ❌ not adopted |

H=40 loses 10.1% of recognized Chinese characters vs H=48 (905 vs 814 chars across 10 images).
Speed gain of 9% does not justify the quality loss. H=48 remains the default.

## Running the Benchmark

```bash
go test -v -run TestOCRBenchmark -count 1 ./tests/ocr-benchmark/
```

## Results

### English device (10 images, 720×1600)

| Image | Boxes | Avg | Per-Box | Notes |
|---|---|---|---|---|
| 08_alarm.png | 9 | 51ms | 5.67ms | Clock app, sparse text |
| 04_notif.png | 10 | 58ms | 5.80ms | Notification panel |
| 07_dialer.png | 17 | 76ms | 4.47ms | Dialer |
| 02_settings.png | 17 | 80ms | 4.71ms | Settings search |
| 10_recent.png | 17 | 82ms | 4.82ms | Recent apps |
| 09_files.png | 18 | 80ms | 4.44ms | Wallpaper picker |
| 03_drawer.png | 16 | 96ms | 6.00ms | App drawer |
| 01_home.png | 16 | 97ms | 6.06ms | Home screen |
| 06_web.png | 20 | 118ms | 5.36ms | Web browser |
| 05_fixed.png | 34 | 188ms | 5.53ms | Dense settings |

### Chinese device (10 images, 720×1612)

| Image | Boxes | Avg | Per-Box | Notes |
|---|---|---|---|---|
| zh_01_settings | 11 | 62ms | 5.64ms | Settings (中文) |
| zh_02_wifi | 12 | 60ms | 5.00ms | WLAN |
| zh_03_about | 12 | 59ms | 4.92ms | About phone |
| zh_04_display | 9 | 51ms | 5.67ms | Display |
| zh_05_apps | 13 | 59ms | 4.54ms | Applications |
| zh_06_storage | 12 | 60ms | 5.00ms | Storage |
| zh_07_bluetooth | 10 | 53ms | 5.30ms | Bluetooth |
| zh_08_sound | 9 | 51ms | 5.67ms | Sound |
| zh_09_home | 6 | 60ms | 10.00ms | Home screen |
| zh_10_notif | 13 | 61ms | 4.69ms | Notification panel |

### Combined Average

```
20 images × 15 rounds = 300 inferences
Avg: 81ms / 14 boxes / 5.73ms per-box (ORT 1.27.1, Vision ANE detect)
中英质量差异: 无
```

### Time Composition (34-box image, ORT 1.27.1)

```
PNG decode:        6ms    ( 5%)
Vision ANE det:    8ms    ( 7%)
Rec preprocess:    8ms    ( 7%)
Rec ONNX infer:   ~80ms    (68%) ← dominant (was 140ms on ORT 1.23.0)
CTC decode:       11ms    ( 9%)
Other:             5ms    ( 4%)
─────────────────────────
Total engine:    ~118ms
+ Screenshot:     20ms
+ Daemon IPC:     15ms
─────────────────────────
End-to-end:     ~153ms
```

### Optimization Status

| Optimization | Status | Effect |
|---|---|---|
| maxSide=1024 resolution cap | ✅ | 1.6× |
| Vision ANE text detection | ✅ | 3.0× (combined) |
| Rec height H=48 (kept, H=40 loses 10% CN chars) | ✅ | default |
| Resize fast path + direct Pix | ✅ | stable |
| Dilate mask (replaces O(n²) box merge) | ✅ | stable |
| Batch rec inference | ✅ | stable |
| CTC strings.Builder decode | ✅ | <1ms |
| ORT 1.27.1 (MLAS ARM64 NEON optimization) | ✅ | 2× vs 1.23.0 |
| ocr_embed dual-product (plain 24MB / -full 42MB) | ✅ | stable |
| **Rec ONNX with BLAS (Accelerate)** | ❌ | Expected 2–3× |

### Image Sources

| Device | Serial | Images |
|---|---|---|
| TECNO (EN) | 13709314CF044927 | 01–10 |
| Xiaomi (CN) | ZD222MYQLJ | zh_01–zh_10 |
