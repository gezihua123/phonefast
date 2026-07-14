# phonefast 项目文档

## 📋 文档索引

### 根目录文档

| 文档 | 作用 |
|---|---|
| [README.md](README.md) | 项目主 README（英文），包含安装、构建、核心功能说明和快速上手 |
| [README_zh.md](README_zh.md) | 项目主 README（中文版） |
| [CHANGELOG.md](CHANGELOG.md) | 版本发布历史，记录每个版本的特性、优化、修复和文档变更 |
| [SKILL.md](SKILL.md) | Claude Code skill 定义文件，供 AI Agent 自动检测和调用 phonefast 功能 |
| [CLAUDE.md](CLAUDE.md) | **本文档** — 项目文档索引与开发指引 |

### docs/ 目录文档

| 文档 | 作用 |
|---|---|
| [docs/CLI.md](docs/CLI.md) | **命令行工具完整使用手册** — 安装与构建、命令参考（tap/swipe/type/key/launch/screenshot/ui/observe 等）、Daemon 管理与 MCP 服务器配置、使用场景与最佳实践、日志与故障恢复 |
| [docs/DEV.md](docs/DEV.md) | **开发笔记** — LocalSocket 4字节读取限制（Android 14）的排查与修复过程、H.264 截图解码架构设计（astiav CGO + ffmpeg CLI 双路径）、构建与发布流程（zig 交叉编译 / CI 原生 runner）、交叉编译踩坑记录（asm 判定、静态 FFmpeg 编译关键坑） |
| [docs/BENCHMARK_HISTORY.md](docs/BENCHMARK_HISTORY.md) | **Benchmark 历史记录** — 从 v1.0.0 到 v1.0.11 的全版本性能测试数据，包括 MCP-STDIO 基线、Daemon 中间测试、1h/12h 长稳压测、v1.0.0 vs v1.0.10 同条件对比、优化版（ThreadCount=1 + 帧循环简化）验证数据、真实内存分析（vmmap） |
| [docs/install.md](docs/install.md) | **安装下载链接** — 各平台二进制下载 URL（GitHub Release 产物） |
| [docs/phonefast.md](docs/phonefast.md) | **产品横向对比** — phonefast vs agent-device vs adb kill 的全面对比：架构设计（Go+scrcpy vs TypeScript+ADB vs Python+ADB）、操作延迟数据、功能矩阵、稳定性对比（12小时长稳压测结果）、适用场景推荐 |
