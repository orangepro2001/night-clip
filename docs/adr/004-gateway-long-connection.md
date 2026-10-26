# ADR-004：使用 Gateway 长连接替代 Bark/APNs

- 状态：Superseded by [ADR-005](005-go-core-and-tailnet-p2p.md)（Gateway 降级为 Phase 4 之后的可选中继）
- 日期：2026-10-26
- 决策者：项目组
- 相关文档：[architecture.md](../architecture.md)、[api-spec.md](../api-spec.md)

## 背景

Bark 是 iOS + APNs 方案，不能触达 Android 和 Windows。原方案中 Bark 只提供通知传输，没有可靠的跨平台 ack、游标或事件历史。

当前需求要求 Windows 和 Android 之间同步剪贴板，并传输文件引用。需要：

- 设备配对和身份。
- 频道/目标隔离。
- 实时事件。
- 离线补齐。
- ack 和去重。
- 密文存储。
- 不依赖 Apple/Google 推送服务。

## 决策

建设 ClipBridge Gateway：

- Go + SQLite（首版）。
- REST 管理设备/频道/事件。
- WebSocket 推送实时 shipment。
- 每设备/频道游标。
- ack、未 ack 重投、TTL 清理。
- 只保存 `payload_ciphertext` 和路由元数据。
- 文件不走 Gateway，继续使用 croc store。
- 首版不依赖 FCM/WNS；Windows 托盘应用和 Android 前台服务维持连接。
- FCM/WNS 仅作为未来可选“唤醒提示”适配，不作为真相来源。

## 结果

### 正面

- 跨 Windows/Android，无需 Bark/APNs。
- 有明确 ack 和离线补齐。
- 可以自建、审计和限流。
- 事件和文件彻底分离。
- 客户端重装/重连后可以由游标恢复。

### 负面

- 需要服务器、数据库、TLS 和备份。
- Android 前台服务耗电，且 OEM 可能杀进程。
- Windows 需要常驻托盘应用。
- Gateway 成为单点，需要运维保障。

### 中性

- 长连接比 FCM/WNS 更可控，但不如系统推送省电。
- 后续可以增加 FCM/WNS 作为唤醒通道，但不改变 Gateway 的事件真相。

## 备选方案

### A. 继续使用 Bark/APNs

拒绝：只支持 iOS，不能满足 Android/Windows。

### B. 仅用 FCM + WNS

暂不采用：依赖 Google/Microsoft 服务，国内可用性、配置和隐私边界复杂；仍缺少统一事件历史和游标。

### C. 纯局域网 P2P

暂不采用：跨网络不可用，NAT/防火墙复杂；可作为后续同 LAN 加速。

### D. 只用轮询

拒绝：延迟和服务器压力不可接受；长连接更适合实时文本事件。

## 验证

- 两端 WebSocket 在线时事件秒级到达。
- 断线 5 分钟后重连可补齐。
- 重复投递不重复应用。
- Gateway 重启后事件不丢。
- 只保存密文，日志无明文/token。
