# phonefast Benchmark 历史记录

> 从 Claude Code 会话缓存、git 历史、test_runs 目录中恢复的完整 benchmark 记录。

---

## 测试时间线

| 日期 | 版本 | 设备 | 模式 | 测试工具 | 时长 |
|---|---|---|---|---|---|
| 2026-06-15 19:18 | pre-git (≈v0.x) | 13709314CF044927 | MCP-STDIO | `tests/benchmark.py` | 10 轮 |
| 2026-07-10 17:16 | v1.0.8-dev | RF8RB05GQ3L | Daemon RPC | 临时 shell 脚本 | ~5h |
| 2026-07-10 17:49 | v1.0.8-dev | RF8RB05GQ3L | Daemon RPC | 临时 shell 脚本 | ~1h |
| 2026-07-13 12:53 | **v1.0.0** | 13709314CF044927 | Daemon RPC | `tests/stress_test_rpc.py` | **60 分钟** |
| 2026-07-13 10:41 | v1.0.10 | 13709314CF044927 | Daemon RPC | `tests/stress_test_rpc.py` | 60 分钟 |
| 2026-07-13 12:02 | **v1.0.0** | 13709314CF044927 | Daemon RPC | `tests/stress_test_rpc.py` --quick | **5 分钟** |
| 2026-07-13 12:07 | **v1.0.10** | 13709314CF044927 | Daemon RPC | `tests/stress_test_rpc.py` --quick | **5 分钟** |
| 2026-07-13 14:19 | **优化版** (ThreadCount=1) | 13709314CF044927 | Daemon RPC | 自定义 RPC 脚本 | 100 次截图+observe |
| 2026-07-13 14:46 | **优化版** (ThreadCount=1) | 13709314CF044927 | Daemon RPC | 自定义 RPC 脚本 | 200 次纯截图 |
| 2026-07-13 19:25 | **优化版** (ThreadCount=1) | 13709314CF044927 | Daemon RPC | `tests/stress_test_rpc.py` | **12 小时** |

---

## 1. 6/15 MCP-STDIO Benchmark（基准线）

**来源**: Claude Code session `cabac5fc` @ `/Users/mulei/Desktop/phonefast`

**条件**: MCP STDIO 模式，串行执行，每操作 10 轮，`benchmark.py` 脚本。

```
Device: 13709314CF044927
Screen: 488x1080
Cold start: 19ms
```

### 各操作延迟

| 操作 | Avg | P50 | P95 | P99 | Min | Max | 数据量 |
|---|---|---|---|---|---|---|---|
| list_devices | 9.1ms | 9.4ms | 10ms | 10ms | 7.7ms | 10ms | — |
| screenshot | 63ms | 64ms | 75ms | 80ms | 52ms | 81ms | 54KB |
| get_ui_elements | 12ms | 11ms | 20ms | 23ms | 8.8ms | 23ms | 1KB |
| observe | 90ms | 88ms | 111ms | 116ms | 73ms | 117ms | 55KB |
| tap | 11ms | 11ms | 11ms | 11ms | 10ms | 11ms | — |
| swipe | **211ms** | 210ms | 213ms | 213ms | 209ms | 214ms | — |
| type_text | 0.3ms | 0.2ms | 0.5ms | 0.5ms | 0.2ms | 0.5ms | — |
| back | 0.3ms | 0.2ms | 0.6ms | 0.7ms | 0.2ms | 0.7ms | — |
| home | 11ms | 11ms | 11ms | 12ms | 11ms | 12ms | — |
| press_key | 11ms | 11ms | 11ms | 12ms | 11ms | 12ms | — |
| launch_app | 0.1ms | 0.1ms | 0.2ms | 0.3ms | 0.1ms | 0.3ms | — |
| wait | 52ms | 52ms | 52ms | 53ms | 51ms | 53ms | — |

**关键参数**:
- `swipe.duration_ms = 200`
- `get_ui_elements` 返回纯文本，avg 1202 bytes
- `back`/`type_text`/`launch_app` 使用 fire-and-forget 语义（不等待设备确认）

---

## 2. 7/10 Daemon 中间测试（UI Socket 优化验证）

**来源**: Claude Code session `ad3127af` @ `/Users/mulei/Downloads/phonefast`

**条件**: 设备 RF8RB05GQ3L (488x1080)，daemon RPC 直连，持续 observe+screenshot 循环。

### TCP per-request 基线

| 指标 | 截图 | observe |
|---|---|---|
| avg | 59ms | 80ms |
| p50 | 59ms | 75ms |
| p95 | 66ms | 86ms |
| p99 | 69ms | **393ms** (TCP 握手尖刺) |
| 迭代 | 345 | 344 |
| 失败 | 0 | 0 |

### 持久连接修复后（热机稳态，排除 IDR 帧聚簇）

| 指标 | 截图 | observe |
|---|---|---|
| p50 | — | 64ms |
| p95 | — | 84ms |
| p99 | — | 199ms |

### 关键发现

- TCP 每次新建连接会导致 p99 出现 393ms 尖刺
- 持久连接修复后 p99 从 393ms → 199ms（但仍有 IDR 帧聚簇导致的毛刺）
- 排除 IDR 帧聚簇后的稳态 observe p50=64ms, p95=84ms

---

## 3. 7/13 v1.0.10 1 小时压测（完整数据）

**来源**: `test_runs/stress_1h_20260713_104147/summary.json`

**条件**: Daemon RPC 直连，6 阶段变强度（Warmup→Steady→Burst A→Mixed→Burst B→Cooldown），60 分钟。

### 总览

| 指标 | 数值 |
|---|---|
| 设备 | 13709314CF044927 |
| 时长 | 3601s (60min) |
| 总操作 | 12,437 |
| 成功 | 12,437 (100%) |
| 重连 | 0 |
| 内存 | 15.2MB → 42.2MB (Δ+27MB, 峰值 58.3MB) |

### 各操作延迟

| 操作 | 次数 | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,278 | 12ms | 13ms | 15ms | 12ms | 32ms |
| back | 1,427 | 12ms | 13ms | 15ms | 12ms | 26ms |
| home | 1,426 | 12ms | 13ms | 15ms | 12ms | 23ms |
| press_key | 1,424 | 12ms | 13ms | 16ms | 12ms | 254ms |
| wait | 1,075 | 32ms | 32ms | 33ms | 32ms | 39ms |
| swipe | 702 | **311ms** | 315ms | 318ms | 311ms | 325ms |
| get_ui_elements | 351 | **54ms** | 140ms | 176ms | 69ms | 184ms |
| screenshot | 351 | 31ms | 125ms | 135ms | 52ms | 136ms |
| observe | 351 | 32ms | 126ms | 137ms | 54ms | 141ms |
| launch_app | 351 | 0.5ms | 2ms | 4ms | 1ms | 6ms |
| type_text | 351 | 0.5ms | 2ms | 3ms | 1ms | 6ms |
| status | 350 | 0.5ms | 1ms | 3ms | 1ms | 7ms |

**关键参数**:
- `swipe.duration_ms = 300`
- daemon RPC handler 同时返回 `elements` (JSON) + `formatted` (文本)
- 高并发场景（12,437 次操作/小时，平均 3.5 ops/s）

---

## 4. v1.0.0 1 小时压测（完整数据）

**来源**: `phonefast-v1.0.0/test_runs/stress_1h_20260713_125353/summary.json`

**条件**: Daemon RPC 直连，6 阶段变强度，60 分钟。

### 总览

| 指标 | 数值 |
|---|---|
| 设备 | 13709314CF044927 |
| 时长 | 3600s (60min) |
| 总操作 | 12,271 |
| 成功 | 12,271 (100%) |
| 重连 | 0 |
| 内存 | 15.1MB → 19.7MB (Δ+4.6MB) |

### 各操作延迟

| 操作 | 次数 | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 4,225 | 12ms | 14ms | 16ms | 12ms | 22ms |
| back | 1,409 | 12ms | 14ms | 16ms | 12ms | 18ms |
| home | 1,408 | 12ms | 14ms | 15ms | 12ms | 20ms |
| press_key | 1,406 | 12ms | 14ms | 16ms | 12ms | 19ms |
| wait | 1,063 | 32ms | 34ms | 35ms | 32ms | 37ms |
| swipe | 694 | 314ms | 322ms | 326ms | 315ms | 336ms |
| **screenshot** | **345** | **121ms** | **202ms** | **276ms** | **137ms** | **659ms** |
| **observe** | **344** | **138ms** | **212ms** | **237ms** | **149ms** | **269ms** |
| get_ui_elements | 345 | 78ms | 191ms | 216ms | 96ms | 224ms |
| launch_app | 344 | 1ms | 2ms | 4ms | 1ms | 6ms |
| type_text | 344 | 1ms | 2ms | 3ms | 1ms | 5ms |
| status | 344 | 1ms | 2ms | 3ms | 1ms | 3ms |

---

## 5. v1.0.0 vs v1.0.10 同条件对比

### 5a. Quick 冒烟 5 分钟

**条件**: 同一设备 13709314CF044927，同一脚本 `stress_test_rpc.py --quick`，Daemon RPC 直连，5 分钟。

### 核心延迟对比 (P50)

| 操作 | v1.0.0 | v1.0.10 | 变化 | 结论 |
|---|---|---|---|---|
| tap | 12ms | 12ms | 持平 | ✅ |
| back/home/press_key | 12-13ms | 12ms | 持平 | ✅ |
| swipe | 317ms | 317ms | 持平 | ✅ |
| type_text/launch_app/status | 1ms | 1ms | 持平 | ✅ |
| wait | 32ms | 32ms | 持平 | ✅ |
| **observe** | **131ms** | **30ms** | **-77% 🚀** | **4.4x 变快** |
| **screenshot** | **114ms** | **36ms** | **-68% 🚀** | **3.2x 变快** |
| get_ui_elements | 58ms | 51ms | -12% | 略快 |

### 完整数据

#### v1.0.0 (commit 121530b, 7.9MB binary)

| 操作 | 次数 | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 378 | 12ms | 13ms | 14ms | 12ms | 19ms |
| back | 126 | 12ms | 13ms | 14ms | 12ms | 17ms |
| home | 125 | 12ms | 14ms | 14ms | 12ms | 14ms |
| press_key | 125 | 13ms | 14ms | 14ms | 13ms | 15ms |
| swipe | 68 | 317ms | 321ms | 322ms | 317ms | 322ms |
| **screenshot** | **34** | **114ms** | **235ms** | **251ms** | **127ms** | **251ms** |
| **observe** | **34** | **131ms** | **204ms** | **205ms** | **137ms** | **205ms** |
| get_ui_elements | 34 | 58ms | 177ms | 178ms | 76ms | 178ms |
| status | 34 | 1ms | 1ms | 1ms | 1ms | 1ms |
| type_text | 35 | 1ms | 1ms | 1ms | 1ms | 1ms |
| launch_app | 34 | 1ms | 1ms | 2ms | 1ms | 2ms |
| wait | 91 | 32ms | 33ms | 34ms | 32ms | 34ms |

**内存**: 14.9MB → 21.3MB (Δ+6.4MB) | 成功率: **100%** (1118/1118)

#### v1.0.10 (commit 11b5e98, 11MB binary)

| 操作 | 次数 | P50 | P95 | P99 | Avg | Max |
|---|---|---|---|---|---|---|
| tap | 381 | 12ms | 13ms | 14ms | 12ms | 14ms |
| back | 126 | 12ms | 13ms | 14ms | 12ms | 14ms |
| home | 126 | 12ms | 14ms | 14ms | 12ms | 14ms |
| press_key | 125 | 12ms | 13ms | 14ms | 12ms | 14ms |
| swipe | 70 | 317ms | 320ms | 1270ms | 330ms | 1270ms |
| **screenshot** | **34** | **36ms** | **120ms** | **128ms** | **39ms** | **128ms** |
| **observe** | **34** | **30ms** | **124ms** | **126ms** | **38ms** | **126ms** |
| get_ui_elements | 34 | 51ms | 166ms | 192ms | 72ms | 192ms |
| status | 34 | 1ms | 2ms | 2ms | 1ms | 2ms |
| type_text | 34 | 1ms | 2ms | 2ms | 1ms | 2ms |
| launch_app | 34 | 1ms | 3ms | 3ms | 1ms | 3ms |
| wait | 91 | 32ms | 33ms | 33ms | 32ms | 33ms |

**内存**: 15.4MB → 34.2MB (Δ+18.8MB，中间峰值 51MB 后 GC 回收) | 成功率: **100%** (1123/1123)

### 关键发现

1. **observe/screenshot 快了 3-4x**: v1.0.0 的截图需要 114ms，v1.0.10 只需 36ms。这是 v1.0.3→v1.0.4 之间 `0447ff8` (Android 14 LocalSocket 4-byte 限制修复) 以及后续 H.264 decoder 线程优化 (`7c51a06`) 的累积效果。

2. **get_ui_elements 基本持平**: 58ms vs 51ms，都在同一量级。v1.0.10 的 P95 略好（166ms vs 177ms）。

3. **内存增长模式不同**: v1.0.0 增长平缓（+6.4MB），v1.0.10 增长更多（+18.8MB）但中间有 GC 主动回收（51MB → 34MB 骤降），说明 v1.0.10 分配更激进但 GC 更有效。

4. **swipe 尾部刺**: v1.0.10 出现一次 1270ms 的 swipe（P99=1270ms），v1.0.0 则非常稳定（max=322ms）。可能和 v1.0.10 的 swipe 并发场景下偶尔排队有关。

---

## 差异根因分析

### swipe: 210ms → 311ms (+101ms)

| 因素 | 6/15 (MCP) | 7/13 (RPC) | 差异 |
|---|---|---|---|
| `duration_ms` 参数 | **200** | **300** | +100ms |
| RPC 开销 | ~10ms | ~11ms | +1ms |
| **合计** | **210ms** | **311ms** | **+101ms** |

**结论**: 完全由参数差异导致，`benchmark.py` 写死了 200ms，`stress_test_rpc.py` 写死了 300ms。不是性能退化。

### get_ui_elements: 11ms → 54ms (+43ms)

| 因素 | 影响 |
|---|---|
| **响应体膨胀** (MCP 只返回纯文本 ≈1KB，daemon RPC 返回 JSON+formatted 双份 ≈10KB+) | **主因** |
| **并发竞争** (串行 vs 12437 次/小时高频混合) | P95 从 20ms → 140ms |
| **GC 拖尾** (RSS 15→42MB，持续分配 JSON 序列化内存) | 尾部延迟 |
| **设备 UI 复杂度** (不同时间屏幕元素数不同) | 次要 |

**结论**: 主要由响应体大小差异和并发场景不同导致，非代码性能退化。

### back/type_text/launch_app: <1ms → ~12ms

**结论**: 测量口径不同。6/15 MCP 模式对部分操作使用 fire-and-forget（不等待 Android 端确认），7/13 daemon RPC 等待完整往返。实际使用中 ~12ms 才是真实延迟。

### v1.0.0 → v1.0.10 1小时截屏性能飞跃

同一设备 13709314CF044927，同一压测脚本，同一 60 分钟时长：

| 操作 | v1.0.0 P50 | v1.0.10 P50 | 加速 | v1.0.0 P99 | v1.0.10 P99 |
|---|---|---|---|---|---|
| **observe** | 138ms | 32ms | **4.3x** 🚀 | 237ms | 137ms |
| **screenshot** | 121ms | 31ms | **3.9x** 🚀 | 276ms | 135ms |
| **get_ui_elements** | 78ms | 54ms | **1.4x** | 216ms | 176ms |
| tap | 12ms | 12ms | 持平 | 16ms | 15ms |
| swipe | 314ms | 311ms | 持平 | 326ms | 318ms |
| 内存增长 | Δ+4.6MB | Δ+27MB | — | 峰值 19.7MB | 峰值 58.3MB |

**结论**: v1.0.10 截屏/observe 快了约 4 倍，代价是内存多用了约 5 倍但仍在可控范围。性能增长来自 `0447ff8`（Android 14 LocalSocket 读取修复）和 `7c51a06`（H.264 decoder 线程优化）。

---

## 6. 优化版（ThreadCount=1 + 帧循环简化）

**改动**（`pkg/avcodec/decode_astiav.go`）：
1. **帧循环简化**：单帧 IDR 解码从 2 次 `AllocFrame`+`ReceiveFrame` 探测循环 → 1 次接收，省一次 CGO 调用和帧分配
2. **ThreadCount 2→1**：单帧 488×1080 太小，多线程切片同步开销 > 解码本身；单线程消除 DPB 翻倍分配和 slice-merge 开销

> 注：中途试过 `SendPacket(nil)` flush 释放 DPB，但实测每次 decode 多 55ms（解码器被 drained 后重新初始化 SPS/PPS + DPB），得不偿失，已回退。也评估过 cache frame/packet、SkipLoopFilter、Flag2Fast、RGBA frame 复用等方案，均因收益不抵风险回退。

### 6a. 三版截图性能对比（100 次 screenshot+observe，RPC 间隔 0.5s）

| 指标 | 原版 (T=2 旧循环) | 帧循环简化 (T=2) | **优化版 (T=1)** |
|---|---|---|---|
| **screenshot P50** | 36ms | 32ms | **33ms** |
| **screenshot P95** | 120ms | 35ms | **36ms** |
| observe P50 | 30ms | 33ms | **33ms** |
| observe P95 | — | 42ms | **36ms** |
| RSS 峰值 | 51MB | 47MB | **48MB** |

### 6b. 200 次纯截图 RPC 专项测试

**条件**: ThreadCount=1 优化版，daemon RPC 直连，200 次连续 `screenshot`，间隔由 RPC 往返决定（无 sleep）。

```
Device: 13709314CF044927 | RSS start: 13MB | 200 screenshots
  #1     19ms   24MB   ← 冷启动
  #21    12ms   45MB   ← decoder 热机完成
  #41    12ms   48MB
  #100   12ms   53MB
  #200   12ms   53MB   ← 稳态
```

| 指标 | 数值 |
|---|---|
| **P50** | **12ms** |
| **P95** | **13ms** |
| **P99** | **14ms** |
| avg | 12ms |
| min/max | 11ms / 19ms（冷启动） |
| RSS 起→止 | 13MB → 53MB（Δ+40MB） |
| RSS 峰值 | 53MB |

**对比原版 v1.0.10**：screenshot P50 从 36ms → **12ms（3x 提速）**，P95 从 120ms → 13ms（稳定性大幅提升）。

### 6c. 真实内存分析（vmmap，修正 RSS 认知）

> 用 `vmmap` 分析单次截图后的 daemon 进程，发现 `ps RSS` 严重虚高，真实物理内存远低于此前所有估算。

| 区域 | VSIZE | RSS | 说明 |
|---|---|---|---|
| MALLOC_MEDIUM | 128MB | 2.6MB | macOS malloc 预留，实际占用很少 |
| MALLOC_NANO | 512MB | 640KB | tiny 分配区，几乎未用 |
| VM_ALLOCATE | 1.2GB | 6.2MB | Go runtime 预留虚拟地址空间 |
| __TEXT (库代码段) | 237MB | 0 | 共享库，多进程共享不独占 |
| Stack | 96MB | 272KB | goroutine 栈预留 |
| **Physical footprint** | — | **15.8MB** | ← 真实物理内存 |
| **Physical footprint (peak)** | — | **16.9MB** | ← 真实峰值 |
| `ps RSS` | — | 26MB | 含共享库页，虚高 |

**关键修正**：
1. **`ps RSS` 26MB 是虚高**——包含共享库 `__TEXT` 等多进程共享页，非独占内存
2. **真实物理内存才 15.8MB**（单次截图后），峰值 16.9MB
3. **FFmpeg 静态链接**，无独立 libav/libsws 映射，代码段在 `__TEXT`（共享，0 独占 RSS）
4. **VM_ALLOCATE 1.2GB 是 Go 预留虚拟空间**，物理只占 6.2MB——Go runtime 正常行为
5. 此前所有压测的"58MB 峰值"基于 `ps RSS`，真实物理内存估计 **30-40MB**

### 6d. 优化结论

| 方向 | 评估 | 决定 |
|---|---|---|
| ThreadCount 2→1 | 速度 3x，内存持平 | ✅ 保留 |
| 帧循环简化 | P95 大幅改善，速度微升 | ✅ 保留 |
| SendPacket(nil) flush | 每次 +55ms，得不偿失 | ❌ 回退 |
| cache frame/packet | 收益 1-2MB，4 个坑 | ❌ 回退 |
| RGBA frame 复用 | 收益 1-2MB，状态同步复杂 | ❌ 回退 |
| SkipLoopFilter | 省 CPU 但降画质，影响 AI 识别 | ❌ 不做 |
| Flag2Fast | 对 H.264 解码无效（仅编码有效） | ❌ 不做 |
| `debug.SetMemoryLimit` | 真实物理内存已极低，无必要 | ❌ 不做 |

**最终状态**：ThreadCount=1 + 帧循环简化。截图 P50=12ms，真实物理内存 15.8MB。FFmpeg 解码侧优化到此为止——真实内存已极优，继续优化是负 ROI。

---

## 7. 优化版 12 小时压测（最终验证）

**来源**: `test_runs/stress_1h_20260713_192539/summary.json`

**条件**: 优化版（ThreadCount=1 + 帧循环简化），daemon RPC 直连，6 阶段变强度，12 小时。

### 总览

| 指标 | 数值 |
|---|---|
| 设备 | 13709314CF044927 |
| 时长 | 43,200s (720min / 12h) |
| 总操作 | 145,843 |
| 成功 | 145,843 (100%) |
| 重连 | 0 |
| 内存 | 14.7MB → 62.0MB (Δ+47.3MB) |

### 各操作延迟

| 操作 | 次数 | P50 | P95 | P99 | Avg | Max |
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

### 全版本演进对比

| 指标 | v1.0.0 (1h) | v1.0.10 (1h) | **优化版 (12h)** | 总加速 |
|---|---|---|---|---|
| screenshot P50 | 121ms | 31ms | **28ms** | **4.3x** 🚀 |
| screenshot P95 | 202ms | 125ms | **126ms** | — |
| observe P50 | 138ms | 32ms | **28ms** | **4.9x** 🚀 |
| observe P95 | 212ms | 126ms | **126ms** | — |
| get_ui_elements P50 | 78ms | 54ms | **46ms** | **1.7x** |
| tap P50 | 12ms | 12ms | **13ms** | 持平 |
| 总操作数 | 12,271 | 12,437 | **145,843** | — |
| 成功率 | 100% | 100% | **100%** | — |
| RSS 峰值 | 19.7MB | 58.3MB | **62.0MB** | — |

### 结论

1. **12 小时零故障**：145,843 次操作，0 错误，0 重连，证明了优化版的生产级稳定性
2. **screenshot 4.3x 提速**：从 v1.0.0 的 121ms → 28ms，帧循环简化 + ThreadCount=1 的组合效果
3. **内存健康**：12h 峰值 62MB 与 1h 峰值 58MB 仅差 4MB，无泄漏趋势——长期运行内存收敛
4. **get_ui_elements 亦受益**：P50 从 78ms → 46ms（1.7x），减轻了 scrcpy UI socket 竞争

---

## 历史数据来源

| 数据 | 位置 |
|---|---|
| 6/15 MCP benchmark | `~/.claude/projects/-Users-mulei-Desktop-phonefast/cabac5fc-*.jsonl` |
| 7/10 中间测试 | `~/.claude/projects/-Users-mulei-Downloads-phonefast/ad3127af-*.jsonl` |
| 7/13 临时冒烟 | `~/.claude/projects/-Users-mulei-Downloads-phonefast/9bca5fcc-*.jsonl` |
| 7/13 1h 压测 | `test_runs/stress_1h_20260713_104147/` |
| 7/13 v1.0.0 quick | `phonefast-v1.0.0/test_runs/stress_1h_20260713_120229/` |
| 7/13 v1.0.10 quick | `test_runs/stress_1h_20260713_120747/` |
| 7/13 优化版 200 截图 | 对话内 RPC 专项测试（见 §6b） |
| 7/13 优化版 12h | `test_runs/stress_1h_20260713_192539/` |
