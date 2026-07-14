# phonefast 项目文档

> **项目目标：Phone as a Native Device for AI Agents**
>
> phonefast 的使命是让 Android 手机成为 AI Agent 的原生设备——如同键盘、鼠标、显示器是人类操作电脑的原生外设一样。通过 <10ms 命令延迟、持久长连接、原子态观察（截图 + UI 树一次返回）和 MCP 原生集成，使 AI 能够像操作本地设备一样实时、可靠地控制手机。

---

> **⚠️ 本文档仅保留项目目标、团队角色和文档导航。构建命令、架构细节等操作信息请写入对应的 `docs/` 文档，勿添加到此文件。**

---

## 📋 文档索引

### 根目录文档

| 文档 | 作用 |
|---|---|
| [README.md](README.md) | 项目主 README（英文），包含安装、构建、核心功能说明和快速上手 |
| [README_zh.md](README_zh.md) | 项目主 README（中文版） |
| [CHANGELOG.md](CHANGELOG.md) | 版本发布历史，记录每个版本的特性、优化、修复和文档变更 |
| [SKILL.md](SKILL.md) | Claude Code skill 定义文件，供 AI Agent 自动检测和调用 phonefast 功能 |
| [CLAUDE.md](CLAUDE.md) | **本文档** — 项目目标、团队角色与文档索引 |

### docs/ 目录文档

| 文档 | 作用 |
|---|---|
| [docs/CLI.md](docs/CLI.md) | **命令行工具完整使用手册** — 安装与构建、命令参考（tap/swipe/type/key/launch/screenshot/ui/observe 等）、Daemon 管理与 MCP 服务器配置、使用场景与最佳实践、日志与故障恢复 |
| [docs/DEV.md](docs/DEV.md) | **开发笔记** — LocalSocket 4字节读取限制（Android 14）的排查与修复过程、H.264 截图解码架构设计（astiav CGO + ffmpeg CLI 双路径）、构建与发布流程（zig 交叉编译 / CI 原生 runner）、交叉编译踩坑记录（asm 判定、静态 FFmpeg 编译关键坑） |
| [docs/benchmark.md](docs/benchmark.md) | **Benchmark 历史记录** — 从 v1.0.0 到 v1.0.11 的全版本性能测试数据，包括 MCP-STDIO 基线、Daemon 中间测试、1h/12h 长稳压测、v1.0.0 vs v1.0.10 同条件对比、优化版（ThreadCount=1 + 帧循环简化）验证数据、真实内存分析（vmmap） |
| [docs/install.md](docs/install.md) | **安装下载链接** — 各平台二进制下载 URL（GitHub Release 产物） |
| [docs/phonefast.md](docs/phonefast.md) | **产品横向对比** — phonefast vs agent-device vs adb kill 的全面对比：架构设计（Go+scrcpy vs TypeScript+ADB vs Python+ADB）、操作延迟数据、功能矩阵、稳定性对比（12小时长稳压测结果）、适用场景推荐 |

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
- **关键原则**：关键修改后须运行 benchmark 验证性能；跨平台构建前本地先 `bash scripts/build.sh` 验证；所有 panic 路径须 recover 并日志记录

### [QA] 测试工程师
- **核心职责**：benchmark 数据采集与分析、长稳压测（12h+）、版本回归验证、性能退化检测、内存分析
- **输入**：待测二进制、压测脚本（`tests/stress_test_rpc.py`）
- **输出**：benchmark 报告、性能对比表、版本退化告警
- **关键原则**：每个发布版本须完成 1h 标准压测 + 与上一版本关键指标对比；退化 >10% 须阻断发布；数据记录到 docs/benchmark.md

### [DOC] 文档工程师
- **核心职责**：README 维护、CLI 使用手册编写、架构设计文档化、CHANGELOG 记录、中英文双语文档同步
- **输入**：架构变更、新增功能、修复记录
- **输出**：README.md、README_zh.md、docs/CLI.md、CHANGELOG.md 更新
- **关键原则**：中文文档为主，英文文档同步；命令格式变更须同步更新 CLI.md；性能变更须更新 benchmark.md 时间线

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
[ARCH] 设计确认 -> [RD] 实现 -> [RD] 本地构建验证 -> [QA] Benchmark 验证 -> [DOC] 文档更新
```

### 发布流

```
[RD] 功能冻结 -> [QA] 全量压测(1h+) -> [QA] 性能对比 -> [PM] 发布评审 -> [RD] 打 tag 发布
```

### BUG 修复流

```
发现 BUG -> [RD] 定位修复 -> [RD] 构建验证 -> [QA] 回归验证 -> [DOC] 更新 CHANGELOG
```
