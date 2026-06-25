# Changelog

## [Unreleased]

### Fixed
- `Back()` 缺少 ACTION_UP 事件 — 设备只收到 DOWN，大多数 app 不响应，回退无效
- `Tap()` 忽略 `s.TapDelay` 字段，硬编码 10ms
- `status` PID 字段永远为 0，应为 `os.Getpid()`
- `status` `connected` 字段只检查 session 对象是否存在，改为 `IsAlive()` 真实检测

## [1.1.0] - 2026-06-25

### Added
- `daemon_worker` 内部子进程命令，替代外露的 `daemon --foreground`
- `assets/assets.go` 使用 `go:embed` 嵌入 scrcpy-server.jar，实现单文件分发
- `scripts/build-server.sh` — 一键拉取/patch/编译 scrcpy server
- `scripts/build.sh` — 全平台构建脚本，自动同步 assets/
- `scripts/clean.sh` — 构建产物与运行时残留清理
- `internal/daemon/` — Actor 模型重构，Unix socket JSON-RPC 服务端
- `internal/log/` — 异步文件日志（goroutine-based）
- `tests/gate.sh` — 准出测试，含 Device Cleanup 阶段
- `tests/stress_test_rpc.py` — Unix socket 纯 RPC 压测（原 stress_test_1h.py）
- `tests/stress_test_cli.py` — CLI 子进程压测（原 stress_test.py）
- `tests/test_mcp.py` / `test_release.py` — MCP 协议和发布冒烟测试
- `docs/BENCHMARK.md` — 多版本性能对比数据（6/17 → 6/24）

### Changed
- `ensureDaemon()` 等待循环 60→40 次，"up-but-not-connected" 状态快速失败
- `stress_test.py` → `stress_test_cli.py`，`stress_test_1h.py` → `stress_test_rpc.py`（区分测量方式）
- `internal/adb/deploy.go` 新增嵌入 jar 提取路径（fallback）
- `internal/session/session.go` 新增 `TapDelay`、`NativeW/NativeH` 字段

### Fixed
- `daemon_worker` 排除在 `ensureDaemon()` 检测范围外，防止自循环 spawn
- MCP `screenshot`/`observe` 返回 `ImageContent`，测试断言兼容
- `gate.sh` 移除对外置 jar/version 文件的前置依赖

---

## [1.0.0] - 2026-06-22

- 初始发布：scrcpy 视频流 + MCP server + daemon 常驻模式
- 支持 tap / swipe / back / home / screenshot / UI dump / observe
- SSE 和 STDIO 双传输协议
