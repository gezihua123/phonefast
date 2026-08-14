# phonefast 性能演进

> 各版本性能对比与优化历程。历史压测数据见 [BENCHMARK_STRESS.md](BENCHMARK_STRESS.md)。

---

## dev 压测数据 (7/27, current)

### 1-Hour 标准压测

**Source**: `test_runs/stress_1h_20260727_162030/summary.json` · CGO=1, 1080×1920 portrait, 设备 13709314CF044927

| Metric | Value |
|---|---|
| Total Operations | 12,458 (100%) |
| Memory | 14.0→**39.5MB (Δ+25.4MB)** ✅ |

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,287 | 11.7ms | 12.8ms | 14.5ms | 12.3ms | 2,137ms ⚠️ |
| swipe | 703 | 311ms | 313ms | 315ms | 311ms | 321ms |
| **screenshot** | **351** | **35ms** | **133ms** | **139ms** | **51ms** | **141ms** |
| **observe** | **351** | **35ms** | **134ms** | **138ms** | **53ms** | **139ms** |
| **get_ui_elements** | **351** | **51ms** | **128ms** | **154ms** | **64ms** | **159ms** |
| back | 1,428 | 11.7ms | 12.6ms | 13.7ms | 11.8ms | 17ms |
| home | 1,429 | 11.8ms | 12.8ms | 14.3ms | 11.9ms | 24ms |
| press_key | 1,429 | 11.8ms | 12.9ms | 14.1ms | 11.9ms | 17ms |
| type_text | 351 | 0.7ms | 1.4ms | 3.6ms | 1.2ms | 152ms |
| launch_app | 350 | 0.7ms | 1.5ms | 2.5ms | 0.8ms | 4ms |
| status | 351 | 0.6ms | 1.1ms | 3.6ms | 0.6ms | 6ms |
| wait | 1,077 | 32ms | 32ms | 33ms | 32ms | 42ms |

> ⚠️ tap max=2.1s 是首次请求触发的 scrcpy 握手

**Memory by Phase:**

| Phase | Avg RSS | Peak | GC Trough | Wave Amp |
|---|---|---|---|---|
| Warmup (0-5min) | 44.7MB | 53.5MB | 14.0MB | 首次连接 |
| Steady (5-20min) | 51.8MB | 64.2MB | 37.5MB | 12.9MB |
| Burst A (20-25min) | 45.0MB | 50.6MB | 42.1MB | **2.6MB** ← 无截图 |
| Mixed (25-40min) | 33.1MB | 47.4MB | 19.5MB | 7.1MB |
| Burst B (40-45min) | 40.2MB | 50.6MB | 30.9MB | 13.1MB |
| Cooldown (45-60min) | 36.1MB | 49.8MB | 20.8MB | 8.0MB |

### 12-Hour 长稳压测 (partial)

| Metric | Value |
|---|---|
| Duration | ~11.3h |
| Operations (est.) | ~135,000, 100% success |
| RSS range | 32-56MB (no upward trend) |
| Daemon instances | Always 1 (flock lock) |
| Crashes / Reconnects | **0 / 0** |

> Harness kill 在 680/720min (94.5%)。唯一停止原因是外部进程 kill。

---

## 版本时间线

| Date | Version | Key Change | Duration | Ops | RSS Peak | screenshot P50 |
|---|---|---|---|---|---|---|
| 6/15 | pre-git (≈v0.x) | MCP-STDIO, scrcpy integrated | 10 rounds | — | — | 64ms |
| 7/13 | v1.0.0 | Baseline | 60min | 12,271 | 19.7MB | 121ms |
| 7/13 | v1.0.10 | LocalSocket fix + decoder threading | 60min | 12,437 | 58.3MB | 31ms |
| 7/14 | v1.0.11 | ThreadCount=1 + frame loop | 12h | 145,843 | 62.0MB | 28ms |
| 7/24 | v1.0.13 | FFmpeg 8.0 + go-astiav 0.41.0 | 60min | 12,478 | 62.7MB | 35ms |
| 7/27 | v1.0.14 (leak) | Multi-device daemon refactor | 60min | 12,436 | **86.5MB 💀** | 31ms |
| 7/27 | **dev (current)** | **Concurrency fix + streaming + lock** | 60min | 12,458 | **39.5MB ✅** | 35ms |

---

## 跨版本关键指标

| Metric | v1.0.0 | v1.0.10 | v1.0.11 | v1.0.14 | **dev** |
|---|---|---|---|---|---|
| screenshot P50 | 121ms | 31ms | 28ms | 31ms | 35ms |
| observe P50 | 138ms | 32ms | 28ms | 33ms | 35ms |
| get_ui_elements P50 | 78ms | 54ms | 46ms | 50ms | 51ms |
| tap P50 | 12ms | 12ms | 13ms | 12ms | 12ms |
| RSS peak | 19.7 | 58.3 | 62.0 | 86.5 | **39.5** |
| Memory leak | ❌ | ❌ | ❌ | **💀** | **✅** |

### 演进趋势

**Screenshot**: 121ms → 31ms (LocalSocket fix) → 28ms (T=1) → 35ms (FFmpeg 8.0)
- 3.9× 总加速 (v1.0.0 → v1.0.11)，dev 35ms 相比 v1.0.11 有 7ms 差距（FFmpeg 8.0 vs 7.1.5 固有差异）

**Memory**: 19.7MB → 58.3MB (解码器引入) → 86.5MB (v1.0.14 TOCTOU leak) → **39.5MB (dev 已修复)**
- dev post-GC 基线 20-31MB

**Lightweight ops** (tap/back/home/key): 全版本 P50 11-13ms，稳定不变

---

## 优化历程

### Phase 1: scrcpy 解码管线 (v1.0.0 → v1.0.10)

- **关键修复**: Android 14 LocalSocket 4-byte read fix (`0447ff8`), H.264 decoder threading (`7c51a06`)
- **效果**: screenshot 121→31ms (3.9×), observe 138→32ms (4.3×)
- **代价**: RSS +30-40MB (FFmpeg decoder + PNG encoder 分配)

### Phase 2: 解码器优化 (v1.0.10 → v1.0.11)

- **关键提交**: `d071608` (ThreadCount 2→1, frame loop 简化)
- **效果**: screenshot 31→28ms, P95 120→13ms (200 连续调用), 真实物理内存 ~15.8MB
- **尝试 & 回退**: SendPacket(nil) flush (+55ms), cache frame/packet (4 pitfalls), RGBA frame reuse — 全部回退

### Phase 3: FFmpeg 升级 (v1.0.11 → v1.0.13)

- **变更**: FFmpeg 7.1.5→8.0, go-astiav v0.35.0→v0.41.0
- **效果**: 稳定无退化, get_ui_elements P50=38ms (历史最快), screenshot P50 略升 7ms

### Phase 4: OCR 内存优化 (7/24)

| Metric | Before | After |
|---|---|---|
| Bytes/op | 54.7MB | **44.1MB** (−19%) |
| Allocations/op | 1,459,860 | **2,072** (−99.9%) |
| P50 latency | 173.4ms | 161.9ms (−7%) |

**关键**: `ResizeImage` 接口分发消除, `CropBox` copy, tensor 复用, `bool` slice pooling

### Phase 5: 并发 Daemon 修复 (7/27, current)

| Bug | Impact | Fix |
|---|---|---|
| writeDaemonStatus TOCTOU | device_count 不一致 | `snapshotDaemon()` 单锁 |
| getOrCreateActor map key | actor 查找失败 | `actor.serial` 做 key |
| Duplicate daemon start | 多实例抢 socket | PID file flock |
| 无流式编码 | ~24MB/screenshot 分配 | 增量 base64 编码 |

**效果**: RSS 86.5→39.5MB (−65%), GC 后基线 20-31MB, screenshot P50 35ms 无退化

---

## 注意事项

- **RSS vs 真实物理内存**: macOS `ps RSS` 含共享库 pages + MADV_FREE pages，虚高。v1.0.11 vmmap 分析: `ps RSS` 26MB → 真实物理内存 **15.8MB**
- **P95 尾部延迟**: 编解码结构性波动 (IDR frame clustering, GC pause)，P50 12ms 对应 P95 126ms (~10×)，无法在 Go 层面消除
- **操作稳定性**: Lightweight ops P50/P95 ratio 1.08-1.2×；Heavy ops (screenshot/observe/UI dump) ratio 3-4×
