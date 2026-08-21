# phonefast 性能演进

> 各版本性能对比与优化历程。历史压测数据见 [BENCHMARK_STRESS.md](BENCHMARK_STRESS.md)。

---

## dev 压测数据 (7/27)

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

---

## dev 压测数据 (8/14, 优化前基线)

### 1-Hour 标准压测(桥接形态 cgo1,优化前基线)

**Source**: `test_runs/stress_1h_20260814_135756/summary.json` · CGO=1 cgo1 bridge（OCR 模型磁盘加载，无内嵌）· 1080×1920 portrait · 设备 13709314CF044927

| Metric | Value |
|---|---|
| Total Operations | 12,182 (100%) |
| Reconnects | 0 |
| Memory | 12.2→**11.5MB (Δ-0.6MB)** ✅ 模型懒加载+无内嵌,较 7/27 dev 的 39.5MB 大幅下降 |

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,220 | 12ms | 15ms | 19ms | 14ms | 5,372ms ⚠️ |
| swipe | 668 | 313ms | 322ms | 330ms | 320ms | 4,535ms ⚠️ |
| screenshot | 334 | **286ms** | 372ms | 391ms | 303ms | 3,032ms ⚠️ |
| observe | 334 | **370ms** | 410ms | 426ms | 370ms | 444ms |
| get_ui_elements | 334 | 94ms | 200ms | 246ms | 113ms | 3,098ms ⚠️ |
| back / home / press_key | 1,405×3 | 12ms | ~15ms | ~21ms | 12ms | 43ms |
| type_text | 334 | 121ms | 140ms | 146ms | 124ms | 656ms |
| launch_app | 335 | 2ms | 4ms | 7ms | 2ms | 11ms |
| status | 335 | 1ms | 2ms | 5ms | 1ms | 8ms |
| wait | 1,071 | 32ms | 34ms | 37ms | 32ms | 44ms |

> ⚠️ max 尖峰为压测最初几秒的冷启动（device actor 连接 + scrcpy 部署）。P99 3.0s 级为 scrcpy 3.3.4 RESET_VIDEO 触发 VDS 重建的设备侧行为（见 DEV.md）。
>
> **screenshot 286ms vs 7/27 的 35ms**:daemon 重构将截图从"直接返回缓存 IDR"改为"强制新关键帧"（修复 Home/Settings 同 md5 的 stale bug）——正确性换延迟,286ms = 设备编码管线重建成本(实测拆分:等待 283-365ms + 解码 32ms)。
>
> **type_text 0.7→121ms**:ASCII 快速通道(INJECT_TEXT)因软键盘丢字问题被移除,统一走 PFIME 广播(正确性换延迟)。

## 版本时间线

| Date | Version | Key Change | Duration | Ops | RSS Peak | screenshot P50 |
|---|---|---|---|---|---|---|
| 6/15 | pre-git (≈v0.x) | MCP-STDIO, scrcpy integrated | 10 rounds | — | — | 64ms |
| 7/13 | v1.0.0 | Baseline | 60min | 12,271 | 19.7MB | 121ms |
| 7/13 | v1.0.10 | LocalSocket fix + decoder threading | 60min | 12,437 | 58.3MB | 31ms |
| 7/14 | v1.0.11 | ThreadCount=1 + frame loop | 12h | 145,843 | 62.0MB | 28ms |
| 7/24 | v1.0.13 | FFmpeg 8.0 + go-astiav 0.41.0 | 60min | 12,478 | 62.7MB | 35ms |
| 7/27 | v1.0.14 (leak) | Multi-device daemon refactor | 60min | 12,436 | **86.5MB 💀** | 31ms |
| 7/27 | dev | Concurrency fix + streaming + lock | 60min | 12,458 | **39.5MB ✅** | 35ms |
| 8/14 | dev | **OCR 桥接形态(模型磁盘加载)+ daemon 重构复测** | 60min | 12,182 | **11.5MB ✅** | 286ms ⚠️ |
| 8/21 | **dev (current)** | **持续流式解码管线(StreamDecoder)替代 RESET_VIDEO 冷路径 + 127.0.0.1 启动修复 + 零拷贝编码** | 60min | 12,314 | **26.3MB ✅** | **26ms** 🚀 |

---

## 跨版本关键指标

| Metric | v1.0.0 | v1.0.10 | v1.0.11 | v1.0.14 | **dev** |
|---|---|---|---|---|---|
| screenshot P50 | 121ms | 31ms | 28ms | 31ms | **26ms** |
| observe P50 | 138ms | 32ms | 28ms | 33ms | **38ms** |
| get_ui_elements P50 | 78ms | 54ms | 46ms | 50ms | 47ms |
| tap P50 | 12ms | 12ms | 13ms | 12ms | 12ms |
| RSS peak | 19.7 | 58.3 | 62.0 | 86.5 | **26.3** |
| Memory leak | ❌ | ❌ | ❌ | **💀** | **✅** |

### 演进趋势

**Screenshot**: 121ms → 31ms (LocalSocket fix) → 28ms (T=1) → 35ms (FFmpeg 8.0) → 286ms (daemon 重构强制新 keyframe，正确性换延迟) → **26ms (8/21 持续流式解码，恢复并超越历史最优)**
- 4.7× 总加速 (v1.0.0 → dev)；流式解码后 screenshot/observe 均快于任何历史版本

**Memory**: 19.7MB → 58.3MB (解码器引入) → 86.5MB (v1.0.14 TOCTOU leak) → **26.3MB (dev 流式解码 + 零拷贝编码，无泄漏)**
- dev post-GC 基线 20-31MB；流式解码稳态 ~26-32MB（含持续解码 DPB）

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

### Phase 5: 并发 Daemon 修复 (7/27)

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

---

## 12-Hour Full 变体压测 (8/20-8/21)

**Source**: `test_runs/stress_1h_20260820_194125/` · `--variant full` (CGO=1, OCR 内嵌), 1080×1920 portrait, 设备 13709314CF044927

### 总览

| Metric | Value |
|---|---|
| Duration | 720 min (12h 整) |
| Total Operations | **141,918 (100% 成功, 0 失败)** ✅ |
| Reconnects | 0 |
| Memory | 14.1→29.0MB (Δ+14.8MB, max 31MB) ✅ 无泄漏 |

### 分操作延迟

| Operation | Count | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 48,822 | 13.4ms | 14.4ms | 15.7ms | 13.3ms | 2,459ms ⚠️ |
| back | 16,269 | 13.3ms | 14.2ms | 15.7ms | 13.1ms | 39ms |
| home | 16,272 | 13.3ms | 14.3ms | 15.5ms | 13.2ms | 35ms |
| press_key | 16,281 | 13.3ms | 14.3ms | 15.7ms | 13.1ms | 28ms |
| launch_app | 3,998 | 1.6ms | 2.6ms | 4.1ms | 1.7ms | 10ms |
| status | 3,998 | 1.1ms | 1.6ms | 3.0ms | 1.1ms | 10ms |
| wait | 12,276 | 32.8ms | 33.5ms | 34.7ms | 32.6ms | 51ms |
| type_text | 4,001 | 129ms | 140ms | 146ms | 129ms | 595ms |
| **screenshot** | **3,999** | **269ms** | **363ms** | **386ms** | **276ms** | **422ms** |
| **get_ui_elements** | **4,000** | **64ms** | **138ms** | **154ms** | **72ms** | **183ms** |
| **observe** | **4,001** | **396ms** | **435ms** | **453ms** | **395ms** | **482ms** |
| swipe | 8,001 | 320ms | 324ms | 327ms | 320ms | 349ms |

### 瓶颈分析

1. **screenshot 是头号瓶颈（P50 269ms）**：比 6/15 MCP-STDIO baseline（avg 63ms）慢 **4.3 倍**，比 7/27 dev 版（avg 51ms）慢 **5.4 倍**。observe（P50 396ms）≈ screenshot + get_ui_elements + ~60ms 序列化开销，其慢几乎全部来自截图。**根因已定位**（2026-08-21）：94% 截图走 RESET_VIDEO 冷路径（设备编码管线整体重建 ~350ms/次），解码仅占 ~14%——已由持续流式解码管线修复（见下文「截图冷路径优化 (8/21)」）。
2. **get_ui_elements P50 64ms / P95 138ms**：比 6/15 baseline（avg 12ms）慢 5 倍。UI dump 的 XML 解析/传输在 daemon 形态下明显退化，P95 长尾说明存在偶发的锁竞争或序列化阻塞。
3. **observe 成为 agent 感知循环的绝对主力**：一次 observe ≈ 396ms，而 tap/back/home 仅 13ms。agent 若每步都 observe，吞吐上限约 2.5 step/s，快不起来。
4. **tap 偶发 2.4s 尖刺**（P99 仍 15.7ms）：与 7/27 的 2.1s 尖刺同模式——非操作本身慢，疑似 RPC 队列排在截图/observe 后面的排队延迟或 GC。不影响 P50/P95。
5. **swipe 固定 320ms**：320ms 与 swipe duration 参数强相关（≈300ms 动画时长 + 20ms 开销），属设计行为而非回归。
6. **内存健康**：12h 仅 +14.8MB（平均 28.8MB），无泄漏迹象，full 内嵌 OCR 未带来内存压力。
7. **type_text 129ms（baseline 0.3ms 的 400 倍）**：baseline 是 fire-and-forget 语义，daemon 同步等待 IME 提交——语义差异，非 bug，但 agent 连续打字时是隐藏成本。

**行动建议**：observe 是 agent 感知循环的上限——截图已由流式解码修复（screenshot P50 269→26ms），get_ui_elements P95 长尾（138→141ms）仍待排查。

---

## 截图冷路径优化 (8/21)

**背景**: 12h 压测发现 screenshot P50 269ms 的根因不是解码(仅 ~50ms), 而是 **94% 的截图走冷路径** — RESET_VIDEO 触发设备端 scrcpy 编码管线整体重建(~350ms) + 等待新 keyframe。旧架构只缓存 IDR、丢弃 P 帧, 而屏幕变化正编码在 P 帧里。

**最终方案** (初版"无动作复用快路径"经评审废弃): **持续流式解码管线** — drainFrames 把每一帧(config/IDR/P)喂给持久 astiav 解码器(`pkg/avcodec.StreamDecoder`), 缓存最新 YUV420P 帧; 截图 = 锁内直接编码缓存帧(零拷贝), **不再发 RESET_VIDEO**。新鲜度按"帧晚于最近一次设备动作 + 年龄 <60s"判定(设备静止 ~1s 后编码器停发重复帧, 纯年龄判定不可行; 年龄上限兜底外部操作深休眠洞)。配套修复: 回环拨号改 127.0.0.1(消除启动期 resolver 抖动故障类)、uiautomator 重试沉降。

**60min 标准压测对比** (`test_runs/stress_1h_20260821_150453/`, 同设备, apple 变体):

| Operation | 12h 基线 P50 | After P50 | 提升 | After P95 |
|---|---|---|---:|---:|
| screenshot | 269ms | **26ms** | **10.3×** | **43ms** |
| observe | 396ms | **38ms** | **10.4×** | **53ms** |
| get_ui_elements | 64ms | 47ms | 1.4× | 141ms |
| tap/back/home/key | 13ms | 12ms | 不变 | — |
| 内存 | 29.0MB | 26.3MB | 无泄漏 ✅ | — |

**12,314 ops / 0 失败 / 100% / 0 重连**。
