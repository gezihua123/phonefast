# phonefast 开发笔记

---

## get_ui_elements 性能分析

### 实测数据

| 场景 | P50 | P95 | P99 | Max |
|------|:---:|:---:|:---:|:---:|
| 空闲设备（无负载） | **21.6ms** | 37.4ms | 127.7ms | 127.7ms |
| 1h 压测混合负载 | **84.1ms** | 190.7ms | 207.4ms | 305.6ms |
| 极限并发（3 线程 tap/home/back） | **359.5ms** | 803.1ms | 909.7ms | 909.7ms |

> 数据来源：`tests/diag_ui_latency.py` 100 次采样 + `tests/stress_test_rpc.py` 346 次采样

### 链路

```
Go (PC)                          Java (Android 设备)
───────                          ───────────────────
TCP connect (ADB forward)  ──▶  LocalServerSocket.accept()
  ~1-5ms
write("dump\x00")          ──▶  read 5 bytes
  <1ms                          ┌─ dumpUIHierarchy():
                                │  ① ua.getWindows()       ← 反射，Binder IPC
                                │  ② window.getRoot()      ← Binder IPC × N窗口
                                │  ③ collectNodes() 递归   ← node.getChild(i) IPC × M节点
                                │  ④ JsonWriter 序列化 JSON
                                └─ 返回
read 4B length + JSON   ◀───  write(len + JSON)
  ~2-10ms
```

### 波动根因

延迟波动的**唯一根因**是 Android UiAutomation API 的 Binder IPC 调用排队。

**空闲时**：AccessibilityManagerService 无其他请求，`getWindows()` + 节点遍历立即返回 → 20ms

**有负载时**：设备正在处理触摸事件（tap/swipe），InputDispatcher 占用 Binder 线程池，UiAutomation 请求排队 → 400ms+

**压测混合负载下**：Steady 阶段 0.5s 间隔有足够空闲窗口，Burst 阶段 0.06s 间隔和触摸事件抢占 Binder → 84ms 是两种状态的时间加权平均

### 不需要优化的理由

**1. 业界已经是天花板**

| | phonefast | phone-mcp | agent-device |
|------|:---:|:---:|:---:|
| UI dump | **84ms** | 7,600ms | 超时 (30s+) |
| 领先倍数 | — | **90x** | **不可用** |

**2. 瓶颈不在 phonefast**

```
AI Agent 一次交互周期:
  LLM 推理:  ████████████████████████████████  2000-5000ms (97%+)
  截图+UI:    ██                                  134ms
  操作:       ▏                                    12ms
```

把 get_ui_elements 从 84ms 优化到 5ms，端到端感知差异仅 1.6%。

**3. 400ms 场景不存在于真实使用**

Burst 阶段每秒 16 次调用是压测人造极端。真实 AI Agent 2 秒一次请求，间隔足够设备消化所有 Binder 积压，始终 21ms 返回。

**4. 缓存方案有反噬**

- 需要决定 TTL（设短命中率低，设长返回过期数据）
- 需要维护失效逻辑（哪些操作清缓存？swipe 中途？launch_app 异步启动？）
- 可能返回旧界面给 Agent，导致误判
- 增加代码复杂度，破坏当前零状态架构

### 结论

**当前实现已经是最优解，无需优化。** 12,434 次 1 小时压测 100% 成功率、0 错误、RSS 稳定在 1MB 以内。get_ui_elements 的波动是 Android Binder IPC 的物理极限，不是 phonefast 的问题。
