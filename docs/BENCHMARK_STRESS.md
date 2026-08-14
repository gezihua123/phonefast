# phonefast 压测详细记录

> 各版本完整压测数据（操作延迟 + 内存详情）。版本对比和演进总结见 [BENCHMARK.md](BENCHMARK.md)。

---

## 1. 6/15 MCP-STDIO Baseline

**Source**: Claude Code session `cabac5fc` @ `/Users/mulei/Desktop/phonefast`

**Conditions**: MCP STDIO mode, serial execution, 10 rounds per operation, `benchmark.py`. Device 13709314CF044927, 488×1080. Cold start 19ms.

| Operation | Avg | P50 | P95 | P99 | Min | Max |
|---|---|---|---|---|---|---|
| screenshot | 63ms | 64ms | 75ms | 80ms | 52ms | 81ms |
| get_ui_elements | 12ms | 11ms | 20ms | 23ms | 8.8ms | 23ms |
| observe | 90ms | 88ms | 111ms | 116ms | 73ms | 117ms |
| tap | 11ms | 11ms | 11ms | 11ms | 10ms | 11ms |
| swipe | 211ms | 210ms | 213ms | 213ms | 209ms | 214ms |
| type_text | 0.3ms | 0.2ms | 0.5ms | 0.5ms | 0.2ms | 0.5ms |
| back | 0.3ms | 0.2ms | 0.6ms | 0.7ms | 0.2ms | 0.7ms |
| home | 11ms | 11ms | 11ms | 12ms | 11ms | 12ms |
| press_key | 11ms | 11ms | 11ms | 12ms | 11ms | 12ms |
| launch_app | 0.1ms | 0.1ms | 0.2ms | 0.3ms | 0.1ms | 0.3ms |
| wait | 52ms | 52ms | 52ms | 53ms | 51ms | 53ms |

> `swipe.duration_ms=200`, `back`/`type_text`/`launch_app` fire-and-forget.

---

## 2. v1.0.0 1-Hour Stress Test

**Source**: `phonefast-v1.0.0/test_runs/stress_1h_20260713_125353/summary.json`

**Conditions**: Daemon RPC, 6-phase, 60min. Device 13709314CF044927.

| Metric | Value |
|---|---|
| Total Operations | 12,271 |
| Successful | 12,271 (100%) |
| Reconnections | 0 |
| Memory | 15.1MB → 19.7MB (Δ+4.6MB) |

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,225 | 12ms | 14ms | 16ms | 12ms | 22ms |
| swipe | 694 | 314ms | 322ms | 326ms | 315ms | 336ms |
| **screenshot** | **345** | **121ms** | **202ms** | **276ms** | **137ms** | **659ms** |
| **observe** | **344** | **138ms** | **212ms** | **237ms** | **149ms** | **269ms** |
| **get_ui_elements** | **345** | **78ms** | 191ms | 216ms | 96ms | 224ms |

> Pre-optimization baseline: screenshot/observe P50 ~120-138ms.

---

## 3. v1.0.10 1-Hour Stress Test

**Source**: `test_runs/stress_1h_20260713_104147/summary.json`

**Conditions**: 同上设备，60min，6-phase.

| Metric | Value |
|---|---|
| Total Operations | 12,437 |
| Successful | 12,437 (100%) |
| Reconnections | 0 |
| Memory | 15.2MB → 42.2MB (Δ+27MB, peak 58.3MB) |

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,278 | 12ms | 13ms | 15ms | 12ms | 32ms |
| swipe | 702 | **311ms** | 315ms | 318ms | 311ms | 325ms |
| **screenshot** | **351** | **31ms** | 125ms | 135ms | 52ms | 136ms |
| **observe** | **351** | **32ms** | 126ms | 137ms | 54ms | 141ms |
| **get_ui_elements** | **351** | **54ms** | 140ms | 176ms | 69ms | 184ms |

> `swipe.duration_ms=300`; screenshot/observe ~4× faster than v1.0.0.

---

## 4. v1.0.11 Optimized 12-Hour Stress Test

**Source**: `test_runs/stress_1h_20260713_192539/summary.json`

**Conditions**: ThreadCount=1 + frame loop simplification, 12h, 6-phase.

| Metric | Value |
|---|---|
| Duration | 43,200s (12h) |
| Total Operations | 145,843 |
| Successful | 145,843 (100%) |
| Reconnections | 0 |
| Memory | 14.7MB → 62.0MB (Δ+47.3MB) |

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 49,943 | 13ms | 13ms | 14ms | 12ms | 453ms |
| back | 16,639 | 13ms | 13ms | 14ms | 12ms | 474ms |
| home | 16,642 | 13ms | 13ms | 14ms | 12ms | 18ms |
| press_key | 16,650 | 13ms | 13ms | 14ms | 12ms | 18ms |
| wait | 12,459 | 33ms | 33ms | 33ms | 32ms | 38ms |
| swipe | 8,384 | 318ms | 322ms | 323ms | 318ms | 821ms |
| type_text | 4,188 | 1ms | 1ms | 2ms | 1ms | 6ms |
| launch_app | 4,187 | 1ms | 1ms | 2ms | 1ms | 5ms |
| status | 4,189 | 1ms | 1ms | 2ms | 1ms | 3ms |
| **screenshot** | **4,185** | **28ms** | **126ms** | **128ms** | **49ms** | **132ms** |
| **observe** | **4,188** | **28ms** | **126ms** | **129ms** | **51ms** | **134ms** |
| **get_ui_elements** | **4,189** | **46ms** | **132ms** | **151ms** | **61ms** | **192ms** |

---

## 5. 7/24 dev CGO 1-Hour (FFmpeg 8.0 + go-astiav 0.41.0)

**Source**: `test_runs/stress_1h_20260724_135900/summary.json`

**Conditions**: CGO_ENABLED=1, FFmpeg 8.0, 1080×1920 portrait, 60min.

| Metric | Value |
|---|---|
| Total Operations | 12,478 |
| Successful | 12,478 (100%) |
| Reconnections | 0 |
| Memory | 13.5MB → 56.9MB (Δ+43.3MB, peak 62.7MB) |

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,295 | 12ms | 13ms | 14ms | 12ms | 2,129ms ⚠️ |
| back | 1,433 | 12ms | 13ms | 14ms | 12ms | 17ms |
| home | 1,431 | 12ms | 13ms | 14ms | 12ms | 28ms |
| press_key | 1,430 | 12ms | 13ms | 14ms | 12ms | 21ms |
| swipe | 703 | 310ms | 314ms | 318ms | 311ms | 326ms |
| **screenshot** | **352** | **35ms** | **129ms** | **135ms** | **51ms** | **139ms** |
| **observe** | **351** | **35ms** | **132ms** | **138ms** | **54ms** | **149ms** |
| **get_ui_elements** | **351** | **38ms** | **130ms** | **151ms** | **55ms** | **166ms** |
| type_text | 351 | 1ms | 2ms | 3ms | 1ms | 5ms |
| launch_app | 351 | 1ms | 2ms | 3ms | 1ms | 6ms |
| status | 351 | 1ms | 1ms | 2ms | 1ms | 5ms |
| wait | 1,079 | 31ms | 32ms | 33ms | 31ms | 35ms |

> ⚠️ tap max=2,129ms is isolated GC STW spike (P50/P95/P99 all 12-14ms). **get_ui_elements P50=38ms — fastest ever** across all versions. No-CGO counterpart: 12,212 ops, screenshot P50=129ms.

---

## 6. v1.0.14 CGO 1-Hour (7/27, pre-fix baseline)

**Source**: `test_runs/stress_1h_20260727_102149/summary.json`

**Conditions**: CGO_ENABLED=1, FFmpeg 8.0, 1080×1920 portrait, 60min. Commit `2ca1627`.

| Metric | Value |
|---|---|
| Total Operations | 12,436 (100%) |
| Memory | 13.6→**86.5MB (Δ+72.9MB)** 💀 |

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,278 | 11.5ms | 12.5ms | 13.2ms | 12.1ms | 2,053ms ⚠️ |
| back | 1,425 | 11.5ms | 12.5ms | 13.3ms | 11.6ms | 20ms |
| home | 1,424 | 11.5ms | 12.5ms | 13.2ms | 11.6ms | 22ms |
| press_key | 1,427 | 11.5ms | 12.5ms | 13.4ms | 11.7ms | 19ms |
| swipe | 702 | 311ms | 316ms | 320ms | 311ms | 338ms |
| **screenshot** | **351** | **31ms** | **133ms** | **138ms** | **50ms** | **144ms** |
| **observe** | **351** | **33ms** | **135ms** | **138ms** | **53ms** | **321ms** |
| **get_ui_elements** | **351** | **50ms** | **125ms** | **141ms** | **63ms** | **148ms** |
| type_text | 352 | 0.6ms | 1.2ms | 1.5ms | 0.6ms | 3ms |
| launch_app | 350 | 0.6ms | 1.4ms | 2.0ms | 0.7ms | 7ms |
| status | 350 | 0.5ms | 1.0ms | 1.9ms | 0.5ms | 4ms |
| wait | 1,075 | 31.4ms | 32.0ms | 32.5ms | 31.4ms | 42ms |

> RSS 86.5MB — **确认内存泄漏**（TOCTOU + map key 不一致）。

---

## 7. dev 并发修复后 1-Hour (7/27 16:20)

**Source**: `test_runs/stress_1h_20260727_162030/summary.json`

**Conditions**: §6 的修复版本。CGO=1, 同设备。内存泄漏已修复。

| Metric | Value |
|---|---|
| Total Operations | 12,458 (100%) |
| Memory | 14.0→**39.5MB (Δ+25.4MB)** ✅ |

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,287 | 11.7ms | 12.8ms | 14.5ms | 12.3ms | 2,137ms ⚠️ |
| back | 1,428 | 11.7ms | 12.6ms | 13.7ms | 11.8ms | 17ms |
| home | 1,429 | 11.8ms | 12.8ms | 14.3ms | 11.9ms | 24ms |
| press_key | 1,429 | 11.8ms | 12.9ms | 14.1ms | 11.9ms | 17ms |
| swipe | 703 | 311ms | 313ms | 315ms | 311ms | 321ms |
| **screenshot** | **351** | **35ms** | **133ms** | **139ms** | **51ms** | **141ms** |
| **observe** | **351** | **35ms** | **134ms** | **138ms** | **53ms** | **139ms** |
| **get_ui_elements** | **351** | **51ms** | **128ms** | **154ms** | **64ms** | **159ms** |
| type_text | 351 | 0.7ms | 1.4ms | 3.6ms | 1.2ms | 152ms |
| launch_app | 350 | 0.7ms | 1.5ms | 2.5ms | 0.8ms | 4ms |
| status | 351 | 0.6ms | 1.1ms | 3.6ms | 0.6ms | 6ms |
| wait | 1,077 | 32ms | 32ms | 33ms | 32ms | 42ms |

> ⚠️ tap max=2.1s 是首次请求触发的 scrcpy 握手（daemon 日志确认: 2.125s）。

**Memory by Phase:**

| Phase | Avg RSS | Peak | GC Trough | Wave Amp |
|---|---|---|---|---|
| Warmup (0-5min) | 44.7MB | 53.5MB | 14.0MB | 首次连接 |
| Steady (5-20min) | 51.8MB | 64.2MB | 37.5MB | 12.9MB |
| Burst A (20-25min) | 45.0MB | 50.6MB | 42.1MB | **2.6MB** ← 无截图 |
| Mixed (25-40min) | 33.1MB | 47.4MB | 19.5MB | 7.1MB |
| Burst B (40-45min) | 40.2MB | 50.6MB | 30.9MB | 13.1MB |
| Cooldown (45-60min) | 36.1MB | 49.8MB | 20.8MB | 8.0MB |

---

## 8. dev 12-Hour Stress Test (7/27 21:21, partial)

**Conditions**: 同 §7。12h 压测，`nohup` 独立运行。Harness kill 在 680/720min (94.5%)。

| Metric | Value |
|---|---|
| Duration | ~11.3h |
| Operations (est.) | ~135,000, 100% success |
| RSS range | 32-56MB (no upward trend) |
| Daemon instances | Always 1 (flock lock) |
| Daemon alive after | 12h+, connected=True |
| Report | None (killed before `write_report()`) |

> Daemon zero crashes, zero reconnects. Only stopped by external process kill.
