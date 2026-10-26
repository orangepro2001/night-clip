# ClipBridge 开发计划（Windows + Android）

> 计划版本：v2 收敛版  
> 支持平台：Windows 10/11（MVP 以 Windows 11 为主）、Android 10–15  
> 明确不支持：iOS、macOS、Linux 客户端  
> 服务端可部署在 Docker/Linux/Windows Server；Linux 不是客户端目标

## 1. 计划原则

1. **先做 Gateway 和文本闭环**，不同时做文件、原生 store、FCM/WNS。
2. **Windows 先做，Android 紧随**；两端共用协议和 JSON schema。
3. **Android 不偷读剪贴板**：只做用户触发和前台服务接收。
4. **默认手动复制**：Windows 可配置自动应用，Android 默认通知。
5. **文件后置**：先浏览器接收，再做 Android 原生 store。
6. **不做跨平台“兼容层”**：不为 iOS/macOS/Linux 预留 UI 或构建产物。
7. **每个阶段有可演示验收**，不以代码量衡量进度。

## 2. 范围

### 2.1 交付物

| 交付物 | 阶段 | 说明 |
| --- | --- | --- |
| `clipbridge-gateway` | Phase 0 | Go 单二进制，SQLite，REST + WebSocket |
| Windows WPF 托盘应用 | Phase 0/1 | 剪贴板、托盘、WebSocket、通知 |
| Android App | Phase 0/1 | Compose UI、前台服务、通知、分享 |
| `cbridge.exe` | Phase 0/2 | Windows CLI，JSON 输出，文件传输 |
| Windows 命名管道 IPC | Phase 1 | 供其他 Windows 应用集成 |
| 文件事件 + croc store | Phase 2 | Windows 上传，Android 浏览器接收 |
| Android 原生 store 客户端 | Phase 3 | Kotlin 下载、校验、保存 |
| 安装器/发布包 | Phase 4 | Windows 安装器、Android APK/AAB、Gateway Docker |
| 测试与部署文档 | Phase 4 | 部署、TLS、备份、故障排查 |

### 2.2 不做

- iOS App、APNs、Bark 运行依赖。
- macOS/Linux 客户端。
- Android 后台剪贴板轮询。
- Android AccessibilityService。
- 首版 FCM/WNS。
- 首版 croc live P2P。
- 首版多租户/团队权限。
- 首版公网 SaaS。

## 3. Phase 0：Gateway + 双端骨架 + 文本闭环（1–2 周）

### 3.1 目标

完成最小可运行链路：

```text
Windows 手动发送文本
  → Gateway
  → Android 前台服务
  → Android 本地通知
  → 用户点击复制
  → Android ack
```

同时完成反向最小链路：

```text
Android 分享/发送文本
  → Gateway
  → Windows 托盘应用
  → Windows 通知/剪贴板写入
```

### 3.2 工作项

#### 协议

- [ ] 冻结 `clipbridge/v1` 事件信封。
- [ ] 冻结 shipment schema（Gateway 传输格式）。
- [ ] 冻结 WebSocket 消息类型：hello/hello_ack/event/ack/ping/pong/error/goaway。
- [ ] 定义 `event_id`、`sequence`、`content_hash`、`expires_at`。
- [ ] 定义 CLI JSON 和退出码。
- [ ] 建立跨语言测试向量（Go/Kotlin/.NET）。

#### Gateway

- [ ] Go module 和单二进制构建。
- [ ] SQLite schema：devices、channels、memberships、shipments、deliveries。
- [ ] 配对会话（一次性码，5 分钟）。
- [ ] 设备 token 认证。
- [ ] `POST /v1/events`。
- [ ] `GET /v1/events` 游标补拉。
- [ ] `GET /v1/ws` WebSocket。
- [ ] ack 和未 ack 重投。
- [ ] TTL 清理。
- [ ] 健康检查 `/healthz`。

#### Windows

- [ ] WPF 单实例托盘应用骨架。
- [ ] WebSocket 客户端和重连。
- [ ] 剪贴板读写。
- [ ] 手动发送/接收。
- [ ] 本地 SQLite 事件表。
- [ ] DPAPI 保存 token。
- [ ] 基础设置页：Gateway URL、频道、设备。

#### Android

- [ ] Compose App 骨架。
- [ ] WebSocket 客户端。
- [ ] 前台服务 + 常驻通知。
- [ ] 通知权限请求。
- [ ] 接收事件、显示通知、手动复制。
- [ ] 分享文本到 ClipBridge。
- [ ] Keystore 保存 token。

#### CLI

- [ ] `cbridge.exe` 骨架。
- [ ] `cbridge doctor`。
- [ ] `cbridge clip send --text ... --to ... --json`。
- [ ] Gateway 直连模式（不依赖 Windows App）。

#### 测试

- [ ] Windows ↔ Android 文本双向。
- [ ] 断线重连和游标补齐。
- [ ] 重复事件只应用一次。
- [ ] 目标/频道不符不应用。
- [ ] Windows 回声抑制。
- [ ] Android 锁屏/后台/通知权限拒绝。

### 3.3 退出标准

- [ ] 两端可配对。
- [ ] Gateway 落库、转发、ack 可追踪。
- [ ] Windows 发文本，Android 收到并手动复制。
- [ ] Android 发文本，Windows 收到并写入。
- [ ] Windows 不把远端写入再次广播。
- [ ] 断网 5 分钟后重连可补齐。
- [ ] 10 次重复事件只应用一次。
- [ ] 事件和 token 不出现在普通日志。

## 4. Phase 1：Windows/Android 剪贴板 MVP（2 周）

### 4.1 目标

做出可日常使用的文本剪贴板同步：

- Windows 自动捕获（可开关）。
- Android 分享/按钮/磁贴发送。
- 两端频道和目标选择。
- 默认手动应用，Windows 可选自动应用。
- 收件箱、状态、错误、基本诊断。

### 4.2 Windows 工作项

- [ ] 剪贴板监听 + debounce。
- [ ] `GetClipboardSequenceNumber` 版本检查。
- [ ] 回声抑制和 `last_remote_applied_hash`。
- [ ] 频道/目标选择。
- [ ] “捕获剪贴板”总开关。
- [ ] 自动应用开关（按频道）。
- [ ] 托盘状态：在线/离线/待同步/错误。
- [ ] Toast/托盘通知 + 复制动作。
- [ ] 开机启动。
- [ ] 本地剪贴板历史（有限条数，可清除）。
- [ ] 恢复上一个剪贴板。

### 4.3 Android 工作项

- [ ] 分享文本到频道。
- [ ] App 内“发送当前剪贴板”。
- [ ] 快捷设置磁贴（打开 Activity 后读取）。
- [ ] 通知动作：复制、打开、忽略。
- [ ] 实时模式开关（前台服务）。
- [ ] 电池优化提示。
- [ ] 频道/目标选择。
- [ ] 本地收件箱和历史。
- [ ] 通知权限/后台限制提示。
- [ ] 敏感事件不显示完整预览。

### 4.4 共用工作项

- [ ] `event_id` 去重。
- [ ] `(channel, source, sequence)` 连续性检查。
- [ ] TTL/过期。
- [ ] 敏感策略。
- [ ] 删除/清空事件。
- [ ] 诊断页面：连接、游标、最近事件、错误。
- [ ] CLI/IPC 接口稳定。

### 4.5 验收标准

| 编号 | 场景 | 预期 |
| --- | --- | --- |
| A1 | Windows 复制 → Android | Android 通知，点击后复制 |
| A2 | Android 分享 → Windows | Windows 通知/写入，不回声 |
| A3 | Windows 自动应用 → 用户刚复制 | 不覆盖新内容 |
| A4 | 频道不符 | 不应用、不展示正文 |
| A5 | 重复事件 | 只应用一次 |
| A6 | 过期事件 | 不应用 |
| A7 | Android 未开实时模式 | 打开 App 后补齐 |
| A8 | 通知权限关闭 | App 内提示，手动打开可用 |
| A9 | Windows 休眠唤醒 | 重连并补齐 |
| A10 | 连续 1000 条事件 | 无崩溃、性能可接受 |

## 5. Phase 2：文件传输（Windows 上传，Android 接收）（2 周）

### 5.1 目标

完成 croc store 文件链路：

```text
Windows 选择文件
  → cbridge file send
  → croc store 上传
  → Gateway file event
  → Android 浏览器打开/下载
  → 校验/commit
```

### 5.2 工作项

- [ ] Go `CrocStoreAdapter` 封装 `storeclient`。
- [ ] `cbridge file send`：
  - 单文件/多文件。
  - `--downloads`、`--expiration`。
  - `--store-url`。
  - `--json`。
- [ ] `cbridge file inspect`。
- [ ] `cbridge file receive`。
- [ ] `cbridge file revoke`。
- [ ] file event payload。
- [ ] Gateway 不保存文件 token 明文（payload 加密）。
- [ ] Windows App 调用 CLI 或内置 store 客户端。
- [ ] Android 打开 croc 浏览器链接。
- [ ] 文件事件 UI：大小、过期、状态。
- [ ] 错误映射：404/410/423/429/507。
- [ ] 本地收据和安全存储。

### 5.3 验收标准

- [ ] 1 MiB、100 MiB 文件可上传/接收。
- [ ] 浏览器 URL 的 key 只在 fragment。
- [ ] token 不出现在 Gateway 日志。
- [ ] 过期/撤销提示明确。
- [ ] 下载中断可重试。
- [ ] 文件事件不经过 Gateway 字节传输。
- [ ] 与 croc CLI 互操作。

## 6. Phase 3：Android 原生 store 与反向文件（2–3 周）

### 6.1 目标

- Android 在 App 内接收文件，不依赖浏览器。
- Android 可发送文件到 Windows。
- Windows 文件收件箱/下载 UI。

### 6.2 Android 原生 store

- [ ] 解析 `croc-store-v1` token/URL。
- [ ] HKDF-SHA-256。
- [ ] AES-256-GCM。
- [ ] manifest/chunk。
- [ ] claim/commit。
- [ ] SHA-256 校验。
- [ ] 保存到 Downloads（MediaStore/SAF）。
- [ ] 后台下载/断点状态。
- [ ] 磁盘空间和 1 GiB 限制提示。
- [ ] Go ↔ Kotlin 测试向量。

### 6.3 Android 上传与 Windows 接收

- [ ] Android 选择/分享文件。
- [ ] 原生 store 上传，或调用后端上传。
- [ ] 发送 file event。
- [ ] Windows 收件箱。
- [ ] `cbridge file receive` 或 App 内下载。
- [ ] 打开文件夹/文件。

### 6.4 验收标准

- [ ] Android 可完整接收并打开文件。
- [ ] 校验失败不保存、不 commit。
- [ ] 断点续传不重复消耗下载次数。
- [ ] Android 可发送文件到 Windows。
- [ ] Windows 可接收并校验。
- [ ] 大文件不一次性读入内存。

## 7. Phase 4：稳定性、打包与发布（2 周）

### 7.1 工作项

- [ ] Windows 安装器/自包含包。
- [ ] Windows 代码签名（如有证书）。
- [ ] Android APK/AAB、签名。
- [ ] Gateway Docker 镜像和配置样例。
- [ ] HTTPS/WSS 反向代理样例。
- [ ] Gateway 备份/恢复脚本。
- [ ] 日志脱敏审计。
- [ ] 性能/内存/长稳测试。
- [ ] 断网/弱网/OEM 后台测试。
- [ ] 用户文档和故障排查。
- [ ] 版本升级/数据库迁移。

### 7.2 发布检查

- [ ] `go test ./...`（Gateway/CLI）。
- [ ] Android 单元/仪器测试。
- [ ] Windows 手工验收。
- [ ] 两端端到端测试。
- [ ] 文件传输测试。
- [ ] 日志/token 扫描。
- [ ] 许可证/NOTICE。
- [ ] 隐私说明。
- [ ] 回滚方案。

## 8. 测试矩阵

### 8.1 Windows

- Windows 11 23H2/24H2 x64（主）。
- Windows 10 22H2 x64（兼容）。
- Windows 11 ARM64（可选，非阻塞）。
- 高 DPI、多显示器、远程桌面。
- 休眠/唤醒、网络切换、代理。

### 8.2 Android

- Android 10、12、13、14、15。
- Google/AOSP 基线设备。
- 至少一台主流 OEM（如 Samsung/小米/OPPO）验证后台限制。
- 锁屏、分屏、通知权限关闭、电池优化开启。
- 无 Google Play Services 设备（首版应可用）。

### 8.3 网络

- Wi-Fi/蜂窝切换。
- 高延迟/丢包。
- 代理。
- Gateway 重启。
- TLS 证书错误。

### 8.4 功能

- 文本/换行/Emoji/中文/长文本。
- 敏感事件。
- 重复/乱序/过期/目标错误。
- 文件 1 MiB/100 MiB/近 1 GiB。
- 文件过期/撤销/claim 冲突。
- 1000 条收件箱。

## 9. 依赖与前置条件

- Windows 开发机 + .NET 8 SDK。
- Android Studio + JDK + SDK。
- Go 1.27.x（croc 当前要求 1.27.1；本机可用 1.27.3）。
- 一台 Android 真机。
- 一个可部署 Gateway 的域名/主机（服务端可用 Linux/Docker）。
- croc store 测试服务或自建 `croc-web --store-dir`。
- TLS 证书。
- 代码签名证书（Windows/Android 可选但推荐）。

## 10. 资源与周期

| 角色 | Phase 0 | Phase 1 | Phase 2 | Phase 3 | Phase 4 |
| --- | --- | --- | --- | --- | --- |
| Windows/.NET | 0.5 | 1.5 | 0.5 | 1.0 | 0.5 |
| Android | 0.5 | 1.5 | 0.3 | 1.5 | 0.5 |
| Go/后端 | 0.8 | 0.5 | 1.0 | 0.5 | 0.5 |
| QA | 0.2 | 0.5 | 0.3 | 0.5 | 0.5 |

单位：人周。总计约 8–11 周，取决于 Android 原生 store 和 OEM 测试。

## 11. 风险控制

- Android 自动写入不可靠 → 默认手动，保留通知动作。
- 前台服务被 OEM 杀 → 游标补齐，不承诺绝对实时。
- Windows 剪贴板循环 → suppression + sequence/hash。
- Gateway 单点 → 备份、健康检查、Docker 重启策略。
- croc 上游变更 → 固定版本 + 适配层 + 契约测试。
- 文件 token 泄露 → 加密 payload、日志脱敏、短有效期。
- 范围反弹 → 所有新需求先回答“是否 Windows/Android 客户端必需”。

## 12. 完成定义

- 协议/接口文档更新。
- 单元/集成/真机测试通过。
- 断线/重连/重复/过期/目标错误可解释。
- 日志无敏感信息。
- 错误有稳定码和用户提示。
- Windows/Android 安装包可用。
- Gateway 可部署、可备份、可回滚。
- 不包含 iOS/Linux/macOS 客户端代码或发布物。

## 13. 第一周执行清单

1. 冻结 `clipbridge/v1` 事件和 shipment schema。
2. 建立 Gateway SQLite schema 和 WebSocket hello/event/ack。
3. Windows WPF 托盘骨架 + 剪贴板读写。
4. Android Compose 骨架 + 前台服务 + 通知动作。
5. `cbridge doctor` 和 `cbridge clip send`。
6. 完成 Windows → Android 文本闭环。
7. 完成 Android → Windows 文本闭环。
8. 记录断线、回声、通知权限问题。
9. 通过 Phase 0 退出标准后再进入 Phase 1。
