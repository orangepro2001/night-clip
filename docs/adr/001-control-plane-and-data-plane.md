# ADR-001：采用推送控制面 + croc store 数据面

- 状态：**Superseded**（2026-10-26 被 ADR-004 取代）
- 历史说明：本 ADR 描述 Bark/APNs 控制面，已不适用于当前 Windows + Android 范围。保留仅用于追溯。
- 日期：2026-10-26
- 决策者：项目组
- 相关文档：[architecture.md](../architecture.md)、[feasibility-analysis.md](../feasibility-analysis.md)

## 背景

产品需要同时满足：

1. 剪贴板事件必须定向、可去重、不能互相污染。
2. 文件不能通过 APNs 传输，需要可恢复的加密文件通道。
3. 需要 CLI 集成和 iOS App。
4. 首版应尽量复用 Bark 和 croc，而不是重写推送或文件协议。

Bark 已提供 APNs 推送、加密载荷、通知扩展和自动复制；croc 已提供 PAKE、relay 回退和 `croc-store-v1` 加密存储。直接把两者拼成一个“聊天群”会产生频道隔离、可靠性和大文件问题。

## 决策

采用两层架构：

- **控制面**：Bark/APNs（MVP）或 BarkSync Gateway + Bark/APNs（增强）。承载加密事件、目标、频道、事件 ID、TTL 和投递状态。
- **数据面**：croc store（首版）承载文件字节；live croc 仅作为后续桌面/局域网能力。

同时执行以下约束：

1. 事件使用独立 `barkx/v1` 信封，不以 Bark `group` 作为隔离。
2. 剪贴板内联内容有大小上限；大内容转文件事件或 Gateway 引用。
3. 首版 iOS 不内嵌 croc live。
4. APNs 只作为通知/控制，不作为唯一事件存储。
5. 直连 Bark 模式不承诺跨设备送达确认；需要 ack 时使用 Gateway。
6. 文件事件只携带 croc store 引用和最小元数据。

## 结果

### 正面

- 最大限度复用成熟能力，缩短 MVP。
- 剪贴板事件有明确的频道/目标/幂等语义。
- 文件不占用 APNs 载荷，天然支持断点和浏览器接收。
- 可先不建服务端，再按需引入 Gateway。
- 服务端/APNs 不接触明文。

### 负面

- 存在两种运行模式（direct/gateway），需要明确文档和测试。
- 直连模式无法可靠接收 CLI 事件，也缺少历史补齐。
- croc store API 是上游实现边界，需要固定版本和适配层。
- iOS 后台剪贴板限制仍然存在，不能靠架构消除。

### 中性

- 剪贴板默认手动复制会影响“无感同步”体验，但这是安全和平台约束的合理取舍。
- Gateway 会引入服务端运维和密钥管理成本。

## 备选方案

### A. 全部用 Bark 推送

拒绝：APNs 载荷有限、无可靠 ack、没有文件通道；大内容无法承载。

### B. 全部用 croc live

拒绝：要求两个 peer 同时在线，移动端后台和网络行为复杂；剪贴板短消息使用 live 传输成本过高。

### C. 自建聊天/消息服务替代 Bark

暂不采用：需要重做 APNs、通知扩展和设备注册；只有在 Bark 的 key/路由模型成为硬瓶颈时才考虑 fork/扩展 bark-server。

### D. 用 Bark `group` 做隔离

拒绝：`group` 只是通知展示分组，不是权限、租户或频道边界。

## 验证

Phase 0 必须验证：

- 加密事件可通过 Bark 推送并在扩展中解析。
- 目标/频道/过期/重复检查可阻止错误应用。
- croc store 1 MiB/100 MiB 文件可被 CLI 接收、校验、commit。
- 不加 Gateway 时产品仍能完成最小闭环。
