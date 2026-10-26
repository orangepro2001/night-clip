# ADR-003：客户端范围收敛为 Windows + Android

- 状态：Accepted
- 日期：2026-10-26
- 决策者：项目组
- 相关文档：[README.md](../README.md)、[feasibility-analysis.md](../feasibility-analysis.md)、[development-plan.md](../development-plan.md)

> 更新（2026-10-26）：本 ADR 中关于 Gateway / croc store 的表述已被 [ADR-005](005-go-core-and-tailnet-p2p.md) 取代；客户端范围（仅 Windows + Android）决策仍然有效。

## 背景

前一版方案以 Bark/iOS 推送链路为核心，并计划支持 iOS App、CLI 和 croc 文件传输。该方案带来：

- iOS 通知扩展和剪贴板限制。
- APNs 无法服务 Android/Windows。
- Bark key 与跨平台身份模型不匹配。
- iOS/macOS/Linux/Windows 多端 UI 和发布矩阵过大。
- 需要同时维护 App Store、TestFlight、Xcode 和多个平台构建。

用户明确要求只关注 Windows 与 Android 的通信和交互，Linux 也可以放弃。

## 决策

1. 客户端只支持 **Windows** 和 **Android**。
2. 放弃 iOS、iPadOS、macOS 客户端。
3. 放弃 Linux 客户端；Linux 仅可作为 Gateway/croc store 的服务端部署载体。
4. 删除 Bark/APNs/iOS 扩展、App Intent、App Store 相关计划。
5. Windows CLI 只发布 `cbridge.exe`，不发布 Linux/macOS 二进制支持承诺。
6. 服务端 Gateway 使用 Go，可部署在 Docker/Linux 或 Windows Server；这是运维选择，不是客户端范围扩张。
7. 所有新需求必须先判断是否对 Windows/Android 客户端必要，否则进入 backlog 或拒绝。

## 结果

### 正面

- 开发、测试和发布矩阵从 4+ 平台收缩到 2 个客户端平台。
- 可以移除 iOS 通知扩展、APNs、Bark key 等复杂约束。
- Windows 和 Android 都能使用 Gateway 长连接与统一事件协议。
- Windows 托盘应用可可靠监听剪贴板；Android 可用分享/通知动作。
- 文件仍可通过 croc store 跨平台传输。

### 负面

- 放弃 iOS 用户和 Bark 生态。
- 需要自建 Gateway，增加服务端运维。
- Android 不能后台自动读剪贴板，体验受限。
- Windows 需要托盘常驻，用户可能反感开机启动。

### 中性

- Linux 作为服务端仍可能存在，但不在支持矩阵中承诺客户端兼容。
- 后续若重新支持 Linux 客户端，需要新增 ADR，不默认扩展。

## 备选方案

### A. 保留 Bark/iOS

拒绝：APNs 不服务 Android/Windows；继续保留会主导架构。

### B. 支持所有桌面平台

拒绝：范围过大，Windows 已覆盖主要桌面场景；Linux/macOS 后置。

### C. Windows + Android + Web

暂不采用：Web 会增加浏览器权限、剪贴板和文件系统差异；先做原生双端。

## 验证

- 两个平台都能通过同一 Gateway 配对。
- Windows 剪贴板捕获和 Android 前台服务接收可用。
- 不需要任何 iOS/Bark 代码即可完成端到端闭环。
- 发布物只有 Windows 安装包、Android APK/AAB、Gateway 镜像。
