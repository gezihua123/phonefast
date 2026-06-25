# phonefast 测试脚本集合

## 目录

```
tests/
├── README.md               本文件
├── gate.py                 准出测试 (发布/PR 前运行，组合以下脚本)
├── benchmark.py            性能基准测试 (Python, 支持 STDIO / SSE)
├── benchmark.sh            性能对比脚本 (Bash, phone-mcp vs phonefast)
├── benchmark_report.json   性能基准测试 JSON 报告
├── test_e2e.py             端到端 socket 连接测试
├── test_release.py         发布包冒烟测试 (15 项检查)
├── test_continuous.py      5 分钟持续真机测试 (数据写入 test_runs/)
├── test_device_5min.py     5 分钟设备测试 (MCP SSE 模式, 6 阶段)
├── test_mcp.py             MCP 协议测试 (Python, 全面 RPC 校验)
├── test_mcp.sh             MCP 协议测试 (Bash, STDIO + 命名管道)
├── stress_test_cli.py          长时间压测 (稳定性、内存泄漏、延迟分布)
├── stress_test_rpc.py       1 小时真机压测 (MCP + daemon 双模式、爆发测试、内存趋势)
├── test_install_cases.py   逻辑正确性验证 (18 个用例: daemon/observe/tap/swipe/key/wait/错误处理)
├── test_tap_install.py     Tap DOWN→UP 间隔对 Play 商店安装按钮的影响验证
├── diag_ui_latency.py      诊断 get_ui_elements 延迟 (100 次采样、逐次计时)
├── test_dynamic_name.py    验证二进制名动态派生 (非硬编码)
├── capture_10s.py          10 秒视频 + UI dump 抓取
└── mcp.json                Claude Desktop MCP 配置示例
```

## 测试分类

### 准出测试 (Gate)

发布或 PR 合并前运行一站式验证，自动执行：

| 阶段 | 脚本 | 覆盖 |
|------|------|------|
| 环境检查 | gate.sh 内联 | 二进制存在、jar/version 文件、ffmpeg、adb、设备连接 |
| Go 单元测试 | `go test ./...` | 所有 Go 包 |
| 构建验证 | `go build` | 编译成功 |
| 功能冒烟 | `test_release.py` | 15 项：help/devices/screenshot/ui/back/home/tap/swipe/wait/observe/MCP SSE |
| MCP 协议 | `test_mcp.py` | 初始化握手、tools/list、tools/call 全部工具、错误处理、响应格式 |
| Daemon 健康 | gate.sh 内联 | daemon 启动、back 控制、screenshot |
| 性能基准 | `benchmark.py --quick` | MCP 工具延迟 p50/p95/p99、吞吐、错误率 |
| 延迟阈值 | gate.sh 内联 | daemon back < 100ms |

```bash
bash tests/gate.sh              # 全部测试 (~90s)
bash tests/gate.sh --quick      # 快速模式，跳过性能基准 (~60s)
bash tests/gate.sh --perf       # 仅性能基准
```

### 单元测试 (Go)

```bash
go test ./...              # 运行所有 Go 单元测试
go test -v ./pkg/protocol/ # 带详细输出
go test -run TestEncode ./pkg/protocol/  # 运行特定测试
```

### 性能基准测试

| 脚本 | 语言 | 传输 | 测试内容 |
|------|------|------|----------|
| `benchmark.py` | Python | STDIO / SSE | MCP 工具延迟 (p50/p95/p99)、吞吐、错误率 |
| `benchmark.sh` | Bash | CLI | phone-mcp vs phonefast 全方位延迟对比 (10 项) |

```bash
# 快速基准测试
python3 tests/benchmark.py --quick

# SSE 模式，30 轮
python3 tests/benchmark.py --sse --rounds 30

# Bash 对比测试
bash tests/benchmark.sh
```

### 协议测试

| 脚本 | 传输 | 测试范围 |
|------|------|----------|
| `test_mcp.py` | STDIO / SSE | MCP initialize 握手、tools/list、tools/call、错误处理、响应校验 |
| `test_mcp.sh` | STDIO | JSON-RPC 2.0 合规性、工具列表、错误码 |

```bash
# Python MCP 测试
python3 tests/test_mcp.py

# Bash MCP 测试 (STDIO)
bash tests/test_mcp.sh

# SSE 模式
python3 tests/test_mcp.py --sse --port 18019
```

### 端到端测试

| 脚本 | 说明 |
|------|------|
| `test_e2e.py` | 原始 scrcpy socket 连接测试 (video/control/ui 握手) |
| `test_release.py` | 发布包冒烟测试: help / devices / screenshot / ui / tap / swipe / back / home / MCP SSE |
| `test_continuous.py` | 5 分钟持续测试，数据输出到 `test_runs/<timestamp>/` |
| `test_device_5min.py` | 6 阶段真机测试，覆盖导航/输入/UI 检测/性能/压力 |
| `capture_10s.py` | 10 秒视频帧 + UI dump 抓取，输出到 `capture_output/` |
| `stress_test_cli.py` | ⭐ 长时间压测 — 稳定性、内存泄漏、延迟分布 |

```bash
# 发布包完整检查 (~30s)
python3 tests/test_release.py

# 端到端 socket 测试
python3 tests/test_e2e.py

# 5 分钟持续测试
python3 tests/test_continuous.py

# 5 分钟设备测试
python3 tests/test_device_5min.py
```

### 长时间压测 (`stress_test_cli.py`)

测试稳定性与内存问题，输出延迟分布 CSV + 内存趋势 CSV + JSON 汇总。

```bash
# 30 分钟 daemon 模式（默认）
python3 tests/stress_test_cli.py

# 60 分钟，两种模式都压
python3 tests/stress_test_cli.py --duration 60 --mode all

# direct 模式（反复启停 session）
python3 tests/stress_test_cli.py --mode direct

# 启用 Go pprof 内存采样（需要运行时暴露 /debug/pprof）
python3 tests/stress_test_cli.py --monitor-mem
```

**输出** (`test_runs/stress_<mode>_<timestamp>/`):
- `timing.csv` — 每个操作的 P50/P95/P99/avg/min/max 延迟
- `memory.csv` — 时间序列 RSS + Go 堆内存
- `errors.log` — 所有失败详情
- `summary.json` — 结构化汇总（含内存泄漏检测）

### 1 小时真机压测 (`stress_test_rpc.py`) ⭐

直连 daemon Unix socket 发送 JSON-RPC，无 CLI 子进程开销，精确测量每操作延迟。

```bash
# 完整 1 小时压测
python3 tests/stress_test_rpc.py

# 5 分钟快速冒烟
python3 tests/stress_test_rpc.py --quick

# 自定义时长（分钟）
python3 tests/stress_test_rpc.py -d 120
```

**6 阶段设计:**

| 阶段 | 时长 | 间隔 | 操作池 | 目的 |
|------|------|------|--------|------|
| Warmup | 5min | 1.0s | 全部 14 项 | 建立基线 |
| Steady | 15min | 0.5s | 全部 14 项 | 常规压力 |
| Burst A | 5min | 0.08s | 轻量 7 项 | 高频吞吐（~12/s） |
| Mixed | 15min | 0.4s | 全部 14 项 | 导航+截图混合 |
| Burst B | 5min | 0.06s | 轻量 7 项 | 极限吞吐（~16/s） |
| Cooldown | 15min | 1.0s | 全部 14 项 | 验证长期稳定性 |

**检测项目:**
- 延迟分布: P50 / P95 / P99
- 成功率统计 (≥99% EXCELLENT, ≥95% GOOD, ≥90% FAIR, <90% POOR)
- 内存趋势: 每 30s 采样 RSS，线性回归检测泄漏
- Daemon 异常断连自动重连
- 爆发阶段极限 QPS 压力

**输出** (`test_runs/stress_1h_<timestamp>/`):
- `timing.csv` — 每操作 P50/P95/P99/avg/min/max + 错误数
- `memory.csv` — RSS 时间序列
- `errors.log` — 所有失败详情
- `reconnects.log` — daemon 重连记录
- `summary.json` — 结构化汇总
- `report.txt` — 人类可读报告

## 运行前提

1. **编译 phonefast 二进制**:
   ```bash
   go build -o dist/phonefast ./cmd/phonefast/
   cp android/scrcpy-server.jar dist/
   echo "3.3.4" > dist/scrcpy-server.version
   ```

2. **Android 设备已连接** (USB 或 TCP):
   ```bash
   adb devices    # 确认设备列表有 "device" 状态
   ```

3. **Python 3 可用** (所有 .py 测试脚本)

4. **ffmpeg 已安装** (screenshot 需要):
   ```bash
   brew install ffmpeg    # macOS
   ```

5. **[可选] phone-mcp 已安装** (benchmark.sh 对比测试需要)

## 测试数据输出

- `test_continuous.py` → `../test_runs/<timestamp>/` (screenshots/, ui_dumps/, timing.csv, events.log, summary.json)
- `capture_10s.py` → `~/Desktop/phonefast/capture_output/`
- `benchmark.py` 带 `--output` → 指定 JSON 文件

## 快速开始

```bash
# 1. 先跑冒烟测试确认基础功能正常
python3 tests/test_release.py

# 2. 跑 MCP 协议测试
python3 tests/test_mcp.py

# 3. 跑性能基准
python3 tests/benchmark.py --quick
```
