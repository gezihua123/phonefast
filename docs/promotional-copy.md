phonefast 精准破解 Harness Coding 在移动端验证环节的四大死穴：慢、不准、烧 token🔥、不稳。

慢 —— daemon 常驻进程 + Unix Socket JSON-RPC，每次触控延迟不到 10 毫秒。对比 adb shell 方案 3-5 秒一次，快 100 倍。"截图→分析→操作→验证"循环从 24 秒压到 0.2 秒。

不准 ——  截图走 H.264 关键帧管道，ffmpeg 直出无损 PNG。UI 解析用自定义 UISocketHandler，比 uiautomator dump 快 40%。observe 原子操作一次拿到画面 + 控件树，杜绝"截图完界面已变"的竞态。

烧 token🔥 ——  phonefast MCP模式 原生 ImageContent，图片以 image/png 类型返回，LLM 多模态引擎直接像素级识别，不再把几十 KB 的 base64 塞进 JSON text。phonefast CLI 模式 observe 合并截图+UI 一步到位，往返砍半，token 预算解套。

稳 —— 12 小时持续压测，14W+次操作，100% 成功，零失败，零断连，零内存泄漏。daemon actor 模型自带 panic 自愈 + reconnect 节流，进程崩了自动拉起来，10 秒内恢复。RSS 内存稳定在 ~24MB，一小时进入稳态后 11 小时零增长，零内存泄漏。三级保活（TCP keepalive + 10s 心跳 + 写失败检测），断了能自愈，挂了能重启。

总结

    phonefast 把手机变成了 AI Agent 原生外设。