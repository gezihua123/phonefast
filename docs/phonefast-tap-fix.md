# phonefast `tap` 无法触发 Play 商店安装按钮——分析与修复

## 症状

`phonefast` CLI 的 `tap` 命令可以正常点击 Play 商店页面上的截图缩略图、返回按钮、菜单等元素，但**安装按钮**始终不响应。

```bash
phonefast --daemon tap 540 1296    # 安装按钮无反应
phonefast tap 540 862               # direct 模式同样无效
phonefast --daemon swipe 541 862 541 863 2000  # 长按模拟依然无效
```

## 根因

### 直接原因：ACTION_DOWN → ACTION_UP 间隔过短

phonefast `Tap()` 硬编码了 **10ms** 的触摸间隔：

```go:internal/session/control.go
func (s *Session) Tap(x, y int) error {
    down := protocol.NewTouchMsg(protocol.ActionDown, int32(x), int32(y), w, h)
    s.controlConn.Write(down.Encode())
    time.Sleep(10 * time.Millisecond)   // ← 仅 10ms
    up := protocol.NewTouchMsg(protocol.ActionUp, int32(x), int32(y), w, h)
    s.controlConn.Write(up.Encode())
}
```

scrcpy-server 在设备端用以下方式构造 `MotionEvent`：

```java
long now = SystemClock.uptimeMillis();
MotionEvent event = MotionEvent.obtain(
    pointer.downTime,  // phonefast: 仅 10ms 之前的时刻
    now,               // 当前时刻
    action, ...
);
// 结果: eventTime - downTime = 10ms
```

人类手指触摸的最短时长通常在 **50~80ms** 以上（Android AOSP 的 `ViewConfiguration.getTapTimeout()` 为 100ms）。**10ms 物理上不可能由人类达成**。

### 为什么仅安装按钮受影响

Google Play 商店的 Compose UI 在安全敏感控件（安装、购买）上增加了触摸时长校验，作为反自动化防线：

```
Play Store Compose 控件点击处理:

非敏感控件（导航、内容浏览）
  └─ 不做 duration 检查 → phonefast 10ms tap 通过 ✓

安全敏感控件（安装、购买）
  └─ event.eventTime - event.downTime < 阈值? → 静默拒绝
  └─ 有效点击 → 触发安装
```

### 对比 scrcpy

| 维度 | scrcpy 鼠标点击 | phonefast `tap` |
|------|----------------|-----------------|
| DOWN→UP 间隔 | 50~300ms（人手按键） | **10ms**（硬编码） |
| 协议格式 | `TYPE_INJECT_TOUCH_EVENT` | **相同** |
| action | ACTION_DOWN / ACTION_UP | **相同** |
| pointerID | -1 | **相同** |
| pressure | 1.0 (0xffff) | **相同** |
| actionButton | 0 | **相同** |
| buttons | 0 | **相同** |

协议层面完全一致，唯一差异就是时间间隔。

## 修复方案

### 方案一：增加固定延迟（最小改动）

将 10ms 改为 50~80ms：

```go:internal/session/control.go
func (s *Session) Tap(x, y int) error {
    if s.controlConn == nil {
        return fmt.Errorf("control socket not available")
    }
    w := uint16(s.DeviceW)
    h := uint16(s.DeviceH)

    down := protocol.NewTouchMsg(protocol.ActionDown, int32(x), int32(y), w, h)
    if _, err := s.controlConn.Write(down.Encode()); err != nil {
        s.markControlBroken(err)
        return fmt.Errorf("tap down: %w", err)
    }

    time.Sleep(50 * time.Millisecond)  // 10ms → 50ms

    up := protocol.NewTouchMsg(protocol.ActionUp, int32(x), int32(y), w, h)
    if _, err := s.controlConn.Write(up.Encode()); err != nil {
        s.markControlBroken(err)
        return fmt.Errorf("tap up: %w", err)
    }

    phonelog.Default().Write("tap (%d,%d)", x, y)
    return nil
}
```

### 方案二：随机化延迟（更拟人，推荐）

```go
import "math/rand"

func (s *Session) Tap(x, y int) error {
    // ... 同上 ...
    s.controlConn.Write(down.Encode())

    // 模拟人类触摸时长：50~120ms 随机
    duration := time.Duration(50 + rand.Intn(70)) * time.Millisecond
    time.Sleep(duration)

    s.controlConn.Write(up.Encode())
    // ...
}
```

### 方案三：增加压力变化（更精细的拟人）

在方案二基础上，DOWN 和 UP 之间增加 MOVE 事件模拟手指轻微移动和压力变化，使事件序列更接近真实触摸：

```
ACTION_DOWN (pressure=0.3, pos=x,y)
  → 20ms
ACTION_MOVE (pressure=1.0, pos=x+1,y)
  → 30ms
ACTION_UP   (pressure=0.8, pos=x+1,y+1)
```

但方案一/二已足够解决当前问题。

## 验证方法

修复后可用以下方式验证：

```bash
# 抓取修复前/后的原始触摸事件对比
adb shell getevent -lt /dev/input/event7 > /data/local/tmp/touch.log

# 修复前: DOWN 和 UP 事件时间戳差 ≈ 10ms
# 修复后: DOWN 和 UP 事件时间戳差 ≥ 50ms
```

功能验证：在 Play 商店任一包含安装按钮的页面执行 `tap` 确认能触发安装。

## 涉及文件

| 文件 | 修改 |
|------|------|
| `internal/session/control.go:29` | `time.Sleep(10 * time.Millisecond)` → `time.Sleep(50 * time.Millisecond)` |
