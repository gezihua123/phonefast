# phonefast 项目文档

> **项目目标：Phone as a Native Device for AI Agents**
>
> phonefast 的使命是让 Android 手机成为 AI Agent 的原生设备——如同键盘、鼠标、显示器是人类操作电脑的原生外设一样。通过 <10ms 命令延迟、持久长连接、原子态观察（截图 + UI 树一次返回）和 MCP 原生集成，使 AI 能够像操作本地设备一样实时、可靠地控制手机。

---

> **⚠️ 本文档仅保留项目目标、团队角色和文档导航。构建命令、架构细节等操作信息请写入对应的 `docs/` 文档，勿添加到此文件。**

---

## 📋 文档索引

### 根目录文档

| 文档 | 语言 | 作用 |
|---|---|---|
| [README.md](README.md) | 🇬🇧 | 项目主 README（英文），包含安装、构建、核心功能说明和快速上手 |
| [README_zh.md](README_zh.md) | 🇨🇳 | 项目主 README（中文版） |
| [CHANGELOG.md](CHANGELOG.md) | 🇬🇧 | 版本发布历史，记录每个版本的特性、优化、修复和文档变更 |
| [SKILL.md](SKILL.md) | 🇬🇧 | Claude Code skill 定义文件，供 AI Agent 自动检测和调用 phonefast 功能 |
| [CLAUDE.md](CLAUDE.md) | 🇨🇳 | **本文档** — 项目目标、团队角色与文档索引 |

### docs/ 目录文档

| 文档 | 语言 | 作用 |
|---|---|---|
| [docs/CLI.md](docs/CLI.md) | 🇬🇧 | **命令行工具完整使用手册**（英文），含中文版 [docs/CLI_zh.md](docs/CLI_zh.md) |
| [docs/DEV.md](docs/DEV.md) | 🇨🇳 | **开发笔记** — LocalSocket 4字节读取限制（Android 14）的排查与修复过程、H.264 截图解码架构设计（astiav CGO + ffmpeg CLI 双路径）、交叉编译踩坑记录（仅中文，内部开发用） |
| [docs/BENCHMARK.md](docs/BENCHMARK.md) | 🇬🇧 | **Benchmark 历史记录**（英文），含中文版 [docs/BENCHMARK_zh.md](docs/BENCHMARK_zh.md) |
| [docs/PHONEFAST.md](docs/PHONEFAST.md) | 🇬🇧 | **产品横向对比**（英文），含中文版 [docs/PHONEFAST_zh.md](docs/PHONEFAST_zh.md) |

---

## 团队角色

### [ARCH] 架构师
- **核心职责**：系统架构设计、协议设计（scrcpy/MCP）、H.264 解码管线决策、性能策略制定、关键技术选型
- **输入**：产品需求、性能目标、兼容性要求
- **输出**：架构设计文档、协议规范、性能优化方案
- **关键原则**：保持核心路径简洁；CGO vs CLI 降级路径需有明确触发条件；所有架构决策须写入 docs/DEV.md

### [RD] 核心开发者
- **核心职责**：Go 代码实现、daemon/session 生命周期管理、scrcpy 协议集成、ASTIAV 解码优化、构建系统维护
- **输入**：架构设计、优化目标、Issue/BUG 报告
- **输出**：可运行二进制、Bug 修复、性能改进代码
- **关键原则**：关键修改后须运行 benchmark 验证性能；全平台构建验证（见下方"构建验证"章节）；所有 panic 路径须 recover 并日志记录

### [QA] 测试工程师
- **核心职责**：benchmark 数据采集与分析、长稳压测（12h+）、版本回归验证、性能退化检测、内存分析
- **输入**：待测二进制、压测脚本（`tests/stress_test_rpc.py`）
- **输出**：benchmark 报告、性能对比表、版本退化告警
- **关键原则**：每个发布版本须完成 1h 标准压测 + 与上一版本关键指标对比；退化 >10% 须阻断发布；数据记录到 docs/BENCHMARK.md

### [DOC] 文档工程师
- **核心职责**：README 维护、CLI 使用手册编写、架构设计文档化、CHANGELOG 记录、中英文双语文档同步
- **输入**：架构变更、新增功能、修复记录
- **输出**：README.md、README_zh.md、docs/CLI.md、CHANGELOG.md 更新
- **关键原则**：**英文文档为主（默认无后缀），中文文档为辅（`_zh.md` 后缀）**；命令格式变更须同步更新 CLI.md；性能变更须更新 BENCHMARK.md 时间线；英文文档为推广主力，DEV.md 可仅中文

### [PM] 产品经理
- **核心职责**：产品方向定义、feature 优先级决策、MCP 生态集成策略、社区反馈收集、版本规划
- **输入**：用户反馈、AI Agent 生态趋势、竞品分析
- **输出**：版本规划、feature 需求、发布决策
- **关键原则**：所有对外暴露的接口（CLI 命令、MCP 工具、JSON-RPC 协议）变更须经 PM 确认；保持与 MCP 协议规范的兼容性

---

## 工作流

### 架构设计流

```
[PM] 需求提出 -> [ARCH] 方案设计 -> [RD] 可行性验证 -> [ARCH] 定稿 -> docs/DEV.md 记录
```

### 开发流

```
[ARCH] 设计确认 -> [RD] 实现 -> [RD] 全平台交叉验证 -> [RD] 本地构建 -> [QA] Benchmark 验证 -> [DOC] 文档更新
```

### 构建验证

**Step 1 — 全平台交叉验证**（CGO_ENABLED=0，秒级快速验证 5 平台纯 Go 路径无编译错误）：

```bash
export CGO_ENABLED=0
GOOS=linux   GOARCH=amd64 go build ./cmd/phonefast/ || exit 1
GOOS=linux   GOARCH=arm64 go build ./cmd/phonefast/ || exit 1
GOOS=darwin  GOARCH=amd64 go build ./cmd/phonefast/ || exit 1
GOOS=darwin  GOARCH=arm64 go build ./cmd/phonefast/ || exit 1
GOOS=windows GOARCH=amd64 go build ./cmd/phonefast/ || exit 1
```

**Step 2 — 本地构建**（必须通过，验证当前平台完整 CGO 编译链路）：

```bash
bash scripts/build.sh
```

产物落于 `dist/dev/`，编译错误或链接失败须阻断合并。

> **说明**：Step 1 快速验证 5 平台纯 Go 语法/类型/导入无差异（跳过 CGO）。Step 2 验证当前平台完整 CGO 编译（含 astiav + FFmpeg 链接）。CI 中的完整 CGO 交叉编译（各平台原生 FFmpeg 链接）由 `bash scripts/build.sh --all` 执行。

**Step 3 — 测试**（跑全仓库单测，含 CGO 包）：

```bash
bash scripts/test.sh
```

> **为何用 wrapper 而非裸 `go test`**：裸 `go test ./...` 默认用系统 FFmpeg（macOS homebrew 8.0），该版本移除了 `AVFMT_FLAG_SHORTEST` 宏，而 go-astiav v0.35.0 仍引用它，导致 `pkg/avcodec` → `internal/session` → `internal/daemon` → `internal/mcp` → `cmd/phonefast` 链式构建失败。`test.sh` 自动设 `PKG_CONFIG_PATH` 指向 `build/cross-ffmpeg/` 自编译 FFmpeg 7.x（与 `build.sh` 同源），让测试环境与生产构建一致。自编译 FFmpeg 不存在时自动降级 `CGO_ENABLED=0`（跳过 avcodec CGO 测试）。支持参数转发：`bash scripts/test.sh ./pkg/h264/ -race`。

### 发布流

```
[RD] 功能冻结 -> [QA] 全量压测(1h+) -> [QA] 性能对比 -> [PM] 发布评审 -> [RD] 打 tag 发布
```

### BUG 修复流

```
发现 BUG -> [RD] 定位修复 -> [RD] 构建验证 -> [QA] 回归验证 -> [DOC] 更新 CHANGELOG
```
