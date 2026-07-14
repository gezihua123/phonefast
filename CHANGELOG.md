# Changelog

## v1.0.11 (2026-07-14)

### 🚀 性能优化
- **H.264 解码**: ThreadCount 2→1，消除多线程切片同步开销，DPB 内存减半
- **帧循环简化**: 移除旧版 AllocFrame 探测循环，每次解码省 2 次 CGO 分配/释放
- **内存优化**: 帧分配和 GC 压力减半，真实物理内存 ~16MB（单次截图后）
- **截图速度**: 长稳 P50=28ms（v1.0.0 相比 4.3× 提升），纯截图 RPC 达 12ms

### 📝 文档
- `docs/DEV.md`: 新增 H.264 截图解码架构文档（astiav CGO + ffmpeg CLI 降级双路径）
- `docs/benchmark.md`: 更新基准测试时间线，标注 v1.0.11 发布
- 网站 `_tabs/phonefast.md`: 更新速度对比数据、内存行、架构设计、长稳压测

### 🛠️ 构建
- `scripts/install_pkg.sh`: 默认安装目录改为 `~/.local/bin`，移除 `--local`/`--global` 模式

---

## v1.0.10 (2026-07-11)

### 🛠️ 构建
- 移除 darwin-amd64 (macOS Intel) 支持，仅保留 darwin-arm64
- GitHub Actions CI 确认 macOS runner 已全量 arm64

---

## v1.0.9 (2026-07-11)

### 🐛 修复
- CI release pipeline: 跳过 Windows 交叉编译测试（已知 CGO 限制）

---

## v1.0.8 (2026-07-11)

### 🛠️ 构建
- GitHub Actions CI: 正式启用 5 平台原生 runner 自动构建+发布
- 方案 3 (CI 原生 runner) 作为正式 release 唯一路径

---

## v1.0.7 (2026-07-08)

### 🐛 修复
- **Android 14 LocalSocket 4 字节限制**: 重写 UISocketHandler 读取逻辑，批量读前 4 字节避免 `readByte()` 超过 4 次触发静默重置
- 下载 URL 路径增加 `v` 前缀以匹配 tag 格式

### 🔧 改进
- `scripts/build-server.sh`: 自动构建 scrcpy-server.jar
- `scripts/release.sh`: 构建前清理 `dist/`，先编译 jar 后编译 Go 二进制

---

## v1.0.6 (2026-07-08)

### 🛠️ 构建
- release.sh 构建前清理 dist/ 目录

---

## v1.0.5 (2026-07-08)

### 🛠️ 构建
- release.sh 先构建 scrcpy-server.jar 再编译 Go 二进制

---

## v1.0.4 (2026-07-08)

### 🐛 修复
- **Android 14 兼容性**: UISocketHandler 读取限制修复，解决 get_ui_elements 在 Android 14 设备上 socket 静默断开的问题
- 使用 batch read 替代逐字节 readByte() 绕开 Android 14 LocalSocket 底层 bug

---

## v1.0.3 (2026-07-08)

### 🔧 改进
- CLI 帮助文档完善，支持 `--help`/`-h` 参数
- SKILL.md 命令示例修正

---

## v1.0.2 (2026-07-08)

### 🛠️ 构建
- 版本号自增流程自动化

---

## v1.0.1 (2026-07-08)

### 🔧 改进
- `scripts/install_pkg.sh`: 自动检测系统架构，下载对应预编译包
- 安装脚本支持 `--local`/`--global` 模式

---

## v1.0.0 (2026-07-08)

### 🎉 初始发布

- **phonefast CLI**: tap/swipe/type/screenshot/observe/launch 等完整命令集
- **Daemon 模式**: Unix Socket JSON-RPC 常驻进程，<1ms 通信延迟
- **MCP 服务**: STDIO/SSE 双协议，原生 ImageContent 输出
- **scrcpy 集成**: H.264 关键帧截图 + UISocketHandler UI 解析
- **三级保活**: TCP keepalive + healthLoop + 写失败自动恢复
- **全平台静态编译**: macOS/Linux/Windows，FFmpeg 静态链接进二进制
