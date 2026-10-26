# ClipBridge 开发计划 v3（Go 核心 + Tailnet 直连）

> 平台：Windows 10/11 x64（C# WPF + Go DLL）、Android 10–15（Kotlin + Go AAR）  
> 前提：所有设备在同一 tailnet 并运行 Tailscale  
> 依据：[ADR-005](adr/005-go-core-and-tailnet-p2p.md)、[architecture.md](architecture.md)、[api-spec.md](api-spec.md)  
> 周期为估计值，未经验证；以 Phase 0 的 Go/No-Go 结果为准修正。

## 1. 计划原则

1. **先打通三层栈**：Go 核心 → DLL/P-Invoke → WPF，和 Go 核心 → AAR → Kotlin，再谈功能。
2. **核心先行、测试先行**：协议、发件箱、游标、去重、传输都在 Go 里用单测和双进程集成测试覆盖，壳只做薄层。
3. **先文本，再文件**；先手动应用，再评估自动应用。
4. **Android 默认不偷读剪贴板**：只做用户触发和前台服务接收；可选的后台读取必须由用户显式开启并完成一次性 ADB 授权（[ADR-006](adr/006-android-background-read.md)）。
5. **无服务端**：不再有 Gateway/croc store 工作项；不新增任何 `tailscale serve` 路径。
6. **范围守门**：新需求先回答"Windows/Android 客户端是否必需、是否依赖 tailnet 前提"。
7. 每个阶段有可演示验收，不以代码量衡量进度。

## 2. 交付物

| 交付物 | 阶段 | 说明 |
| --- | --- | --- |
| `clipbridge-core`（Go module） | P0– | 协议、身份、配对、对等连接、发件箱、传输、门面 |
| `clipbridge_core.dll` | P0 | `c-shared`，C ABI（5 个函数） |
| `clipbridge-core.aar` | P0 | gomobile 绑定 |
| Windows WPF 托盘应用 | P0/P1 | 剪贴板、托盘、配对 UI、通知 |
| Android App | P0/P1 | Compose、前台服务、分享、通知 |
| `cbridge.exe` + 命名管道 | P1 | 本地集成 |
| 文件传输（双向） | P2 | Range 断点、SHA-256、MediaStore |
| 安装器 / APK | P3 | 含防火墙规则 |
| 可选：离线中继 peer | P4+ | 需新 ADR，默认不做 |

## 3. Phase 0：三层栈打通 + 文本闭环（2–3 周）

### 3.1 目标

```text
Windows 手动发文本 → Go 核心(mTLS/WS) → Android Go 核心 → Android 通知 → 用户点复制 → ack
Android 分享文本   → Go 核心 → Windows Go 核心 → WPF 写剪贴板（或通知）→ ack
```

### 3.2 工作项

#### Go 核心

- [ ] 仓库与模块结构、`go.mod` 固定版本、CI（`go test ./...`、`go vet`、race）。
- [ ] `identity`：Ed25519 + 自签证书 + `device_id` 派生。
- [ ] `peer`：mTLS 服务端/客户端，指纹固定；tailnet 地址发现与来源校验；监听只绑 tailnet 地址。
- [ ] `proto`：信封、规范 JSON、`content_hash`；对照 [event.schema.json](schemas/event.schema.json) 的校验测试。
- [x] `store`：SQLite（modernc）、迁移、字段加密（身份落盘已接入门面；peers/links/outbox/inbox CRUD 已测，待 `peer` 调用）。
- [ ] 发件箱 / 每链路 sequence / `recv_cursor` / `skip` / 去重 / TTL。
- [ ] `pairing`：QR + SAS + psk 限流。
- [ ] WebSocket 链路：hello/hello_ack/event/ack/ping/pong/goaway，指数退避。
- [ ] `facade`：`Call`/`NextEvent`，panic recover，错误码。
- [ ] `cmd/cbcore-dll`（导出 5 个函数）与 `mobile`（gomobile）。
- [ ] 双进程集成测试：配对、收发、断线重连补发、重复、过期、缺口 skip、指纹不符、非 tailnet 来源。

#### Windows

- [ ] WPF 单实例托盘骨架。
- [ ] `CoreInterop`：P/Invoke、UTF-8 marshaling、`cb_free`、事件泵线程、关闭流程。
- [ ] DPAPI 保护数据库主密钥。
- [ ] 配对 UI：显示 QR、SAS 确认。
- [ ] 手动发送文本、收到文本通知 + 手动写剪贴板。

#### Android

- [ ] Compose 骨架；AAR 接入；Keystore 保护主密钥。
- [ ] 扫码配对（CameraX/ML Kit 或 ZXing；无 Play 服务时用 ZXing）。
- [ ] 前台服务 + 常驻通知 + 通知权限。
- [ ] 收到事件显示通知，"复制"动作写剪贴板。
- [ ] 分享文本到 ClipBridge。

#### 测试

- [ ] Windows ↔ Android 文本双向（真机 + 真实 tailnet）。
- [ ] 断网 5 分钟重连补齐；10 次重复事件只应用一次；指纹不符被拒。
- [ ] 日志扫描：无 psk、私钥、正文。

### 3.3 Go/No-Go（PoC 项，Phase 0 第一周完成）

- [ ] **P-A**：AAR 在真机上，Tailscale VPN 开启时，用 IP 直拨 Windows DLL 的 mTLS 端口成功；核心不调用 `net.Interfaces()`。
- [ ] **P-B**：C# P/Invoke 事件泵 24 小时不死锁、不泄漏；关闭无崩溃。
- [ ] **P-C**：Android 前台服务在目标 OEM 上锁屏 30 分钟后仍保持连接，或断线后能补齐。
- [ ] **P-D**：tailnet 直连与 DERP 中继两种路径下，100 MiB 文件吞吐可接受（记录数字）。
- [ ] **P-E**：Windows 休眠/唤醒、Tailscale 重启后，核心自动重新绑定并重连。

任一 P-A/P-B 失败即停止并重审方案（回退到 Windows 端 Go CLI 子进程 + stdio，或 Android 端改 Kotlin 薄实现）。

### 3.4 退出标准

- [ ] 双向文本闭环可演示。
- [ ] P-A…P-E 有书面结果。
- [ ] 核心单测与集成测试在 CI 通过。
- [ ] 事件和密钥不出现在日志。

## 4. Phase 1：剪贴板 MVP（2 周）

### 4.1 Windows

- [ ] 剪贴板监听 + debounce + 读取重试；`GetClipboardSequenceNumber` 检查。
- [ ] 回声抑制（核心）与"用户刚复制则不覆盖"（壳）。
- [ ] 捕获总开关（首次由用户选择）、按对端的 `apply_mode`、敏感事件策略。
- [ ] 托盘状态：已连接 / 连接中 / 离线 / `NO_TAILNET` / 错误。
- [ ] Toast + 复制动作；开机启动；本地历史 + 恢复上一个剪贴板。
- [ ] 命名管道 + `cbridge.exe`。

### 4.2 Android

- [ ] App 内"发送当前剪贴板"；快捷设置磁贴（透明 Activity）。
- [ ] 通知动作：复制 / 打开 / 忽略。
- [ ] 实时模式开关；电池优化引导；Tailscale 未连接提示（`TRANSPORT_VPN` 检查仅作提示）。
- [ ] 收件箱、历史、敏感内容不显示预览。

### 4.3 共用

- [ ] 诊断页（连接、路径、游标、最近错误）。
- [ ] 设置：TTL、对端策略、删除/清空。

### 4.4 验收

| 编号 | 场景 | 预期 |
| --- | --- | --- |
| A1 | Windows 复制 → Android | 通知，点击后复制 |
| A2 | Android 分享 → Windows | 通知/写入，不回声 |
| A3 | Windows 自动应用时用户刚复制 | 不覆盖新内容 |
| A4 | 未配对设备尝试连接 | 拒绝，无内容泄露 |
| A5 | 重复事件 | 只应用一次 |
| A6 | 过期事件 | 不应用；发送方 `skip` 后游标前进，后续事件不被卡住 |
| A7 | Android 未开实时模式 | 打开 App 后补齐 |
| A8 | 通知权限关闭 | App 内提示，手动可用 |
| A9 | Windows 休眠唤醒 | 重连并补齐 |
| A10 | 连续 1000 条事件 | 无崩溃，性能可接受 |
| A11 | Tailscale 关闭 | 明确提示，不重连风暴 |
| A12 | 撤销对端 | 立即断开且不可重连 |

## 5. Phase 2：文件传输（2 周）

- [ ] `xfer`：offer/accept/reject/cancel、`pull` 与 `push` 两种模式。
- [ ] Range GET / Content-Range PUT / HEAD 恢复；SHA-256；临时文件 + 校验后落盘。
- [ ] 磁盘空间检查、单文件上限设置、路径清洗。
- [ ] Windows：文件选择/拖拽发送、接收确认、进度、打开所在文件夹、CLI `file send`。
- [ ] Android：分享文件发送（`fd` 传入核心）；MediaStore 保存到 Downloads；进度通知；后台继续（前台服务）。
- [ ] 错误映射：`NO_SPACE`、`TOO_LARGE`、`HASH_MISMATCH`、`EXPIRED`、`PEER_OFFLINE`。

验收：

- [ ] 1 MiB、100 MiB、2 GiB 文件双向传输；大文件不整体读入内存。
- [ ] 传输中断后从 offset 续传，最终 SHA-256 一致。
- [ ] 校验失败不保存。
- [ ] DERP 与直连路径均可完成。
- [ ] 文件名中的 `..`、盘符、保留名被清洗。

## 6. Phase 3：稳定性、打包与发布（1.5–2 周）

- [ ] Windows 安装器：文件、开机启动、**限定远端地址的防火墙入站规则**、卸载清理；代码签名（如有证书）。
- [ ] Android：APK/AAB 签名、首次运行引导（Tailscale、通知、电池）、混淆与 Go 库保留规则。
- [ ] 长稳（24–72 小时）、内存、弱网、tailnet 切换、Tailscale 重启。
- [ ] OEM 后台测试（至少一台 Samsung/小米/OPPO）。
- [ ] 日志脱敏审计；数据库迁移与升级测试；备份/重置说明。
- [ ] 用户文档：前提（Tailscale）、配对、故障排查、tailnet ACL 示例。
- [ ] 许可证/NOTICE、隐私说明。

## 7. Phase 4+（可选，需新 ADR）

- 离线中继 peer（always-on 节点存转发，端到端加密，因中继不再被信任）。
- FCM 唤醒提示（仅唤醒，不作真相来源）。
- HTML/图片剪贴板、多文件打包。

## 8. 测试矩阵

### 8.1 Go 核心（CI）

- 单元：规范 JSON、hash、序号/游标、去重、TTL、策略、路径清洗。
- 集成：双进程（模拟 Windows 监听 + Android 拨号）覆盖重连、skip、乱序、指纹不符、非 tailnet 来源。
- 模糊：信封解析、WebSocket 帧、Range 头。
- `go test -race ./...`。

### 8.2 Windows

- Windows 11 23H2/24H2 x64（主）、Windows 10 22H2（兼容）。
- 高 DPI、远程桌面、休眠/唤醒、Tailscale 服务重启、多网卡。

### 8.3 Android

- Android 10、12、13、14、15；Google/AOSP 基线 + 至少一台主流 OEM。
- 锁屏、分屏、通知权限关闭、电池优化、无 Google Play 服务设备。
- 与其它 VPN 冲突时的提示。

### 8.4 网络

- tailnet 直连 / DERP；Wi-Fi ↔ 蜂窝切换；高延迟丢包；对端休眠；ACL 阻断。

## 9. 依赖与前置条件

- Go 1.27.x、`mingw-w64`（交叉编译 DLL）、`gomobile` + Android NDK。
- .NET 10 SDK（WPF；.NET 8 LTS 将于 2026-11 结束支持）；Android Studio + JDK + SDK。
- 一台 Android 真机；Windows 机器；同一 tailnet 的账号，两端已安装 Tailscale。
- 代码签名证书（可选）。
- 无需域名、TLS 证书、Docker、服务器。

## 10. 资源与周期（估计）

| 角色 | P0 | P1 | P2 | P3 |
| --- | --- | --- | --- | --- |
| Go 核心 | 1.5 | 0.5 | 1.0 | 0.3 |
| Windows / C# | 0.8 | 1.2 | 0.7 | 0.7 |
| Android / Kotlin | 0.8 | 1.2 | 0.7 | 0.7 |
| QA | 0.3 | 0.5 | 0.4 | 0.5 |

单位：人周。总计约 7.5–9 周（v2 为 8–11 周）；减少了 Gateway、croc 适配、Kotlin 加密实现，增加了 DLL/AAR 绑定与 PoC。

## 11. 风险与控制

| 风险 | 缓解 |
| --- | --- |
| 前提强：必须有 Tailscale | 首次运行检测并引导；文档明确；不承诺无 tailnet 可用 |
| Android 同时只能一个 VPN | 提示冲突；Phase 4 评估中继 |
| 电脑休眠导致 Android 无法同步 | UI 明示"电脑离线"；TTL 内排队；可选中继（P4+） |
| Android 前台服务被 OEM 杀 | 游标补齐；引导电池设置；不承诺绝对实时 |
| Android 15 对 `dataSync` 类前台服务有每日约 6 小时的时长限制 | 脚手架已改用 `specialUse` 类型并声明用途；Play 上架需要说明理由，侧载不受影响；P-C 验证 |
| Go 在 Android 上的网络限制（网卡枚举、DNS） | 只用 IP 拨号；不依赖会枚举网卡的库；P-A 验证 |
| Go DLL panic / 无法卸载 | 门面统一 recover；退出时先 `cb_shutdown`；事件泵线程 |
| gomobile 类型与生命周期限制 | 门面只用字符串/整型/fd；不跨边界传复杂对象 |
| 证书/配对被滥用（tailnet 内其他设备） | 指纹固定 + SAS 确认 + psk 限流；防火墙与 ACL 纵深 |
| 密钥丢失/重装 | 重新配对；文档说明；不做云备份 |
| 剪贴板回声 / 覆盖用户内容 | hash + 抑制窗口 + sequence 检查；默认手动 |
| 文件走 DERP 慢 | 显示路径与速率；不阻塞文本 |
| 范围反弹 | ADR-003/005 守门 |

## 12. 完成定义

- 协议/接口文档与实现一致，Schema 校验在核心中有测试。
- 核心单测/集成测试通过；双端真机测试通过。
- 断线/重连/重复/过期/缺口/目标错误/指纹不符可解释。
- 日志与崩溃报告无敏感信息。
- 错误有稳定码和用户提示。
- Windows 安装包与 Android APK 可用；防火墙规则限定 tailnet 网段。
- 不包含 iOS/Linux/macOS 客户端代码或发布物，不包含 Gateway。

## 13. 第一周执行清单

1. 建立 `clipbridge-core` 仓库、CI、Go 版本；写 `proto` 与规范 JSON + Schema 测试。
2. `identity` + `peer` mTLS，用两个本地进程完成指纹固定与拒绝测试。
3. 最小 `facade` + `cb_*` 导出，交叉编译出 DLL，写 C# 最小 P/Invoke 控制台验证 P-B。
4. 最小 `mobile` 绑定，AAR 接入空白 Android 工程，真机经 Tailscale 拨通 Windows 监听端口（P-A）。
5. 记录 `net.Interfaces()`、DNS、VPN 相关的问题与结论。
6. 通过 P-A/P-B 后再开发配对与事件链路；否则停下重审方案。
