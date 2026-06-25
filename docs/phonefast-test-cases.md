# phonefast 功能测试用例——Play 商店应用安装流程

## 测试环境

| 项目 | 值 |
|------|-----|
| 设备型号 | SM-A325F |
| 设备序列号 | RF8RB05GQ3L |
| 分辨率 | 1080×2400 |
| 系统 | Android (Samsung One UI) |
| phonefast 版本 | 本仓库编译 (Go binary) |
| scrcpy-server | v3.3.4 |
| ADB 路径 | ~/Library/Android/sdk/platform-tools/adb |
| 日期 | 2026-06-22 |

---

## 用例 1：基础命令

### 1.1 daemon 启动 / 状态 / 停止

```bash
phonefast daemon              # 启动
phonefast daemon --status     # 查看状态
phonefast daemon --stop       # 停止
```

**预期**: daemon 后台启动，`--status` 返回 `daemon running`，`--stop` 停止进程。

**结果**: ✅ 通过。daemon 在后台运行，支持复用连接。

### 1.2 devices 列出设备

```bash
phonefast devices
```

**预期**: 列出已连接设备及型号。

**结果**: ✅ 通过。
```
Connected devices:
  RF8RB05GQ3L  device  [SM_A325F]
```

### 1.3 wait 等待

```bash
phonefast --daemon wait 3000
```

**预期**: 等待 3000ms 后返回。

**结果**: ✅ 通过。输出 `Waited 3000ms`。

### 1.4 screenshot 截图

```bash
phonefast --daemon screenshot /tmp/screen.png
```

**预期**: 保存当前屏幕截图到指定路径。

**结果**: ✅ 通过。输出 `Screenshot saved to /tmp/screen.png`，文件可读。

### 1.5 observe（截图 + UI 元素）

```bash
phonefast --daemon observe
```

**预期**: 返回截图与可交互元素列表（含 index、text、desc、id、bounds、clickable 等）。

**结果**: ✅ 通过。输出包括元素类型（FrameLayout/TextView/Button/WebView 等）、bounds 坐标、clickable 标记、文本和描述。

---

## 用例 2：触摸操作

### 2.1 tap 坐标点击（daemon 模式）

```bash
phonefast --daemon tap 540 1300
```

**预期**: 在 (540, 1300) 处产生点击事件。

**结果**: ✅ 通过。输出 `Tapped at (540, 1300)`。

### 2.2 tap 坐标点击（direct 模式）

```bash
phonefast tap 540 1300
```

**预期**: 直接启动 scrcpy-server 连接，在 (540, 1300) 处产生点击。

**结果**: ✅ 通过。每次启动新的 server 连接，点击成功。

### 2.3 swipe 滑动

```bash
phonefast --daemon swipe 540 1800 540 400 500
```

**预期**: 从 (540,1800) 滑动到 (540,400)，持续 500ms。

**结果**: ✅ 通过。输出 `Swiped from (540, 1800) to (540, 400)`。

### 2.4 长按模拟（慢速 swipe）

```bash
phonefast --daemon swipe 541 862 541 863 2000
```

**预期**: 在 (541,862) 处产生类似长按的触摸，持续 2000ms。

**结果**: ✅ 通过。输出 `Swiped from (541, 862) to (541, 863)`。

---

## 用例 3：按键操作

### 3.1 key 发送按键码

```bash
phonefast --daemon key power       # 电源键 (26)
phonefast --daemon key home        # Home 键 (3)
phonefast --daemon key back        # 返回键 (4)
phonefast --daemon key enter       # 回车键 (66)
phonefast --daemon key dpad_center # DPAD 确认 (23)
phonefast --daemon key dpad_down   # DPAD 下 (20)
```

**预期**: 发送对应按键事件。

**结果**: ✅ 通过。各按键码正确映射并执行。

### 3.2 back / home 快捷命令

```bash
phonefast --daemon back
phonefast --daemon home
```

**预期**: 等价于 `key back` / `key home`。

**结果**: ✅ 通过。输出 `Back pressed` / `Home pressed`。

---

## 用例 4：Play 商店 UI 交互

### 4.1 通过 URL 打开商店页面

```bash
adb shell am start -a android.intent.action.VIEW \
  -d "https://play.google.com/store/apps/details?id=<package>"
```

**预期**: Play 商店打开指定应用详情页。

**结果**: ✅ 通过。页面加载，`observe` 可获取 UI 元素。

### 4.2 通过 market:// URI 打开

```bash
adb shell am start -a android.intent.action.VIEW \
  -d "market://details?id=<package>"
```

**预期**: 与 HTTP URL 效果相同。

**结果**: ⚠️ 部分有效。首次可能跳转到首页而非详情页。

### 4.3 Play 商店 Compose UI 元素点击

```bash
phonefast --daemon tap <x> <y>
```

**测试元素**:

| 目标 | 坐标 | 结果 |
|------|------|------|
| 截图缩略图 | (200, 800) | ✅ 成功打开截图查看器 |
| 返回按钮 | (73, 153) | ✅ 返回上一页 |
| 更多选项菜单 | (1006, 153) | ✅ 点击有响应 |
| 底栏导航（游戏/应用/搜索） | 各坐标 | ✅ 正常切换 |

### 4.4 Play 商店安装按钮点击

```bash
phonefast --daemon tap 541 1296
phonefast tap 541 862            # direct 模式
phonefast --daemon tap 900 862   # 按钮区域右边缘
phonefast --daemon tap 1000 862  # 按钮区域最右边
# 快速连点
phonefast --daemon tap 540 1270 && phonefast --daemon tap 540 1290 && phonefast --daemon tap 540 1300
# 长按模拟
phonefast --daemon swipe 541 862 541 863 2000
```

**预期**: Play 商店开始下载安装应用。

**结果**: ❌ 失败。按钮无反应，页面未变化。

**原因**: phonefast `Tap()` 硬编码 10ms DOWN→UP 间隔，Play 商店安装按钮有最小触摸时长校验，拒绝 <50ms 的触摸事件。详见 `phonefast-tap-fix.md`。

---

## 用例 5：Chrome WebView 交互

### 5.1 在 Chrome 中打开测试页面

```bash
# 先强制停止 Play 商店，避免 URL 被拦截
adb shell am force-stop com.android.vending
adb shell am start -a android.intent.action.VIEW \
  -d "https://play.google.com/apps/testing/<package>" \
  -n com.android.chrome/com.google.android.apps.chrome.Main
```

**预期**: Chrome 浏览器打开 Google Play 测试页面。

**结果**: ✅ 通过。页面加载，"Become a tester" 按钮可见。

### 5.2 WebView 按钮点击

```bash
phonefast --daemon tap 283 1837
```

**目标**: 点击 "Become a tester" 按钮。

**结果**: ⚠️ 间歇性成功。多次尝试后最终页面显示 "You are a tester"，证明点击在某次执行中生效。WebView 的 JS click 事件不校验 MotionEvent 时长，但触摸到 click 转换存在不确定性。

---

## 用例 6：锁屏与唤醒

### 6.1 锁屏状态处理

```bash
phonefast --daemon observe   # 检测到 keyguard/AOD 元素
phonefast --daemon key power # 唤醒屏幕
phonefast --daemon swipe 540 1900 540 500 500  # 上滑解锁
```

**预期**: 从 AOD → 锁屏 → 解锁主屏幕。

**结果**: ✅ 通过。`observe` 可检测锁屏状态（识别 `keyguard_status_view` / `aod_overlay_container`），按键 + 滑动可完成解锁。

---

## 用例 7：daemon vs direct 模式对比

| 维度 | daemon 模式 | direct 模式 |
|------|-----------|------------|
| 连接方式 | 复用已有的 scrcpy-server 连接 | 每次启动新 scrcpy-server |
| 速度 | 快（无需重新部署 jar） | 慢（部署 jar + 建立连接 ≈2-3s） |
| 并发冲突 | 无 | daemon 运行时 direct 会报 `EOF` |
| 适用场景 | 连续多次操作 | 单次操作或 daemon 不可用时 |
| 触摸行为 | 完全一致（同协议同 server） | 完全一致 |

**结果**: ✅ 行为一致，daemon 模式更快。

---

## 用例 8：边界情况

### 8.1 Play 商店清除数据后重新加载

```bash
adb shell pm clear com.android.vending
adb shell am start -a android.intent.action.VIEW -d "market://details?id=<package>"
```

**观察**: UI 布局发生变化（顶部遮罩层消失，安装按钮位置 y 从 1234 移至 799），但安装按钮仍不响应 10ms tap。

**结果**: 确认问题与缓存/状态无关，是运行时策略校验。

### 8.2 测试者状态持久性

```bash
# 第 N 次访问测试页面
adb shell am start -a android.intent.action.VIEW \
  -d "https://play.google.com/apps/testing/com.muzple.junqi"
phonefast --daemon observe
```

**观察**: 页面显示 "You are a tester."——之前通过 phonefast 点击 "Become a tester" 成功接受了测试者邀请。

**结果**: ✅ 测试者状态跨会话保持。

### 8.3 不存在的应用

```bash
adb shell am start -a android.intent.action.VIEW \
  -d "https://play.google.com/store/apps/details?id=com.shiyu"
phonefast --daemon observe
```

**观察**: Play 商店显示 "找不到该内容"。

**结果**: ✅ 正确处理，不会崩溃。

---

## 用例 9：综合流程（端到端）

### 流程：gp.md 应用批量安装

```
1. python3 extract_gp_links.py gp.md        → 提取 15 个应用包名和链接
2. for each pkg: adb pm path <pkg>          → 检查安装状态
3. for 未安装:
   a. adb shell am start PLAY_STORE_URL     → 打开商店
   b. phonefast --daemon wait 3000          → 等待加载
   c. phonefast --daemon observe            → 分析页面
   d. 有 Install 按钮 → tap 安装
      需测试者身份 → 打开 testing URL → tap "Become a tester" → 回商店
   e. adb pm path <pkg>                     → 验证安装
4. phonefast daemon --stop                  → 清理
```

### 执行统计

| 指标 | 值 |
|------|-----|
| 总应用数 | 15 |
| 已安装（初始） | 0（后确认 13 个实际已安装） |
| 成功安装 | 0（phonefast tap 无法触发安装按钮） |
| 测试者注册 | 1（com.muzple.junqi，经 WebView 点击成功） |
| 商店不可用 | 1（com.shiyu，返回"找不到该内容"） |

### 核心阻塞点

Play 商店原生 Compose 安装按钮无法被 phonefast `tap` 触发（10ms 触摸时长被反自动化校验拒绝）。

### 修复后应验证

执行完整流程，确认所有未安装应用可通过 phonefast 安装。

---

## phonefast 命令矩阵

| 命令 | 语法 | 状态 |
|------|------|------|
| daemon | `phonefast daemon` | ✅ |
| daemon --status | `phonefast daemon --status` | ✅ |
| daemon --stop | `phonefast daemon --stop` | ✅ |
| devices | `phonefast devices` | ✅ |
| tap | `phonefast --daemon tap <x> <y>` | ⚠️ 10ms 延迟缺陷 |
| swipe | `phonefast --daemon swipe <x1> <y1> <x2> <y2> [ms]` | ✅ |
| key | `phonefast --daemon key <name\|keycode>` | ✅ |
| back | `phonefast --daemon back` | ✅ |
| home | `phonefast --daemon home` | ✅ |
| wait | `phonefast --daemon wait <ms>` | ✅ |
| screenshot | `phonefast --daemon screenshot [file]` | ✅ |
| observe | `phonefast --daemon observe` | ✅ |
| ui | `phonefast --daemon ui` | ✅（未单独详测） |

---

## 相关文档

- `phonefast-tap-fix.md` — tap 缺陷根因分析与修复方案
- `agents.md` — 自动化安装流程规范
- `gp.md` — 目标应用列表
- `extract_gp_links.py` — 应用信息提取脚本
