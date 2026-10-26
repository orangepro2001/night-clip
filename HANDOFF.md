# night-clip 交接文档

> 写于 2026-10-26。读完这一份就能接着干活，不需要翻聊天记录。  
> **2026-10-26 增量**：`store` 数据层已完成并**已接进门面**（`device_id` 重启不变），细节、验证结果见 [HANDOFF-STORE.md](HANDOFF-STORE.md)。仓库已推到 `git@github.com:orangepro2001/night-clip.git`（`main`）。  
> 设计文档在 [`../docs`](../docs)（`README.md` → `adr/005-go-core-and-tailnet-p2p.md` → `architecture.md` → `api-spec.md` → `development-plan.md`）。  
> 本文只讲：**现在是什么状态、怎么验证、为什么这么做、下一步做什么、哪些事要等用户拍板。**

## 0. 一页摘要

- **产品**：Windows ↔ Android 剪贴板同步 + 文件传输。所有设备在同一个 Tailscale tailnet，设备间直连（无服务端）。工作名 ClipBridge，仓库目录 `night-clip`。
- **架构**：一个 Go 核心库承载全部业务；Windows 用 C# WPF 壳 + Go 编译的 DLL；Android 用 Kotlin 壳 + gomobile AAR。两个壳只通过 `Call(method, json)` / `NextEvent(timeoutMs)` 两个入口访问核心。
- **现状**：**脚手架 + 验证体系已完成**。协议类型（信封、规范 JSON、content_hash、校验）和设备身份（Ed25519、自签证书、device_id、指纹）已实现并有测试；门面、DLL、AAR、两个壳的最小骨架都能编译并跑通冒烟测试。`make check` 全绿。
- **没有的**：网络、配对、剪贴板逻辑、文件传输、托盘、通知、命名管道——全部未开始。持久化已完成：`facade.Init` 现在要求 `data_dir`（绝对路径）+ `db_key_b64`（32 字节），身份落盘，重启后 `device_id` 不变；见 [HANDOFF-STORE.md](HANDOFF-STORE.md)。`peer/ pairing/ clip/ xfer/` 目前只有 `doc.go`。
- **下一步**：补 `api-spec.md` 的三个规范空缺（§5 第 10 条），然后 `peer`（WP2）与双实例测试框架（WP5）→ `pairing` → `clip` → `xfer`（详见 §7；WP1 已完成）。
- **环境**：全部在这台 Linux（Ubuntu 26.04，无 KVM，无 Windows）上验证。真机项目（WPF 渲染、剪贴板、Android 真实 VPN）见 §6。
- **Git**：本地仓库在 `night-clip/`，远端 `origin = git@github.com:orangepro2001/night-clip.git`。用户已授权：每个阶段性新增功能完成后提交并推送，无需再询问；push 之外的破坏性操作（amend/reset/force）仍需明确授权。`docs/` 在仓库之外（`../docs`），**没有**被提交。

## 1. 接手：5 分钟上手

```sh
cd /home/tony/dev/Bark/night-clip
make check        # 约 1–3 分钟（首次更久），应全部通过
```

`make check` 依次执行：`vet`（go vet + gofmt）→ `test`（Go 单测 -race）→ `dll-smoke`（Linux .so + C 测试）→ `dll-wine`（Windows DLL 在 Wine 下）→ `test-interop`（C# 互操作 xUnit，对 Linux .so）→ `build-windows-app`（WPF 在 Linux 上编译）→ `cli` → `aar`（gomobile）→ `apk`（Gradle debug APK）。

### 工具链位置（本机，非仓库内容）

| 工具 | 位置 | 备注 |
| --- | --- | --- |
| Go 1.27.3 | `/usr/local/bin/go` | `core/go.mod` 声明 `go 1.27` |
| mingw-w64 gcc | `/usr/bin/x86_64-w64-mingw32-gcc` | 交叉编译 DLL |
| Wine 10 | `/usr/bin/wine64`，前缀 `~/.wine-clipbridge` | 仅用于 `make dll-wine` |
| .NET 10 SDK | `~/.dotnet/dotnet`（**不在 PATH**） | Makefile 的 `DOTNET` 变量会自动找到 |
| JDK 17 | `/usr/lib/jvm/java-17-openjdk-amd64` | Makefile 默认 `JAVA_HOME` |
| Android SDK | `~/Android/Sdk`（platform 35，build-tools 34/35） | |
| Android NDK | `~/Android/Sdk/ndk/28.2.13676358` | 本轮安装 |
| gomobile / gobind | `~/go/bin` | Makefile 已把它加进 PATH |
| Gradle 8.11.1 | `~/opt/gradle-8.11.1` | 仅用于生成 wrapper；日常用 `android/gradlew` |

本轮由我（Claude）在用户授权下安装的：JDK 17、Wine（apt，sudo）、NDK 28.2（sdkmanager，并替用户接受了 SDK 许可）、gomobile、Gradle。.NET SDK 原本就在 `~/.dotnet`。

换机器时：用环境变量覆盖 `ANDROID_HOME`、`ANDROID_NDK_HOME`、`JAVA_HOME`、`DOTNET`、`MINGW_CC`（见 `Makefile` 开头）。

## 2. 已做出的决策（及原因）

| 日期 | 决策 | 原因 / 来源 |
| --- | --- | --- |
| 2026-10-26 | 范围仅 Windows + Android；放弃 iOS/macOS/Linux 客户端 | ADR-003，用户要求 |
| 2026-10-26 | 放弃 Bark/APNs；v2 的 Gateway 方案归档到 `docs/archive-v2/` | 用户要求核心 Go/C、尽量 P2P；Gateway 方案要在三种语言重复实现协议 |
| 2026-10-26 | **所有设备在同一 tailnet 是产品前提** | 用户确认。tailnet 已解决 NAT 穿透和链路加密，所以不要 Gateway、STUN/TURN、WebRTC、libp2p、tsnet、局域网发现 |
| 2026-10-26 | Go 核心 + WPF（C#）+ Go c-shared DLL；Android Kotlin + gomobile AAR | 用户裁决；曾评估 C/C++ 核心，结论是工作量多 50–100%、安全风险更高，用户选择保持 Go |
| 2026-10-26 | 数据搬运总由**拨号方**发起；Windows 监听（只绑 tailnet 地址），Android 只出站拨号 | 避免 Android 监听端口和网卡枚举问题（targetSdk≥30 受限） |
| 2026-10-26 | 核心不嵌入 tsnet / libtailscale，只依赖系统 Tailscale 客户端 | Android 上 tsnet 受限；croc 的 `tailcattransport` 构建标签也不含 Android |
| 2026-10-26 | 链路认证用 mTLS + 配对时固定证书指纹；不做 payload 二次加密 | 没有不可信中间方。若将来引入中继，必须补端到端加密（新 ADR） |
| 2026-10-26 | `sequence` 按**每条链路**（source→target）单独递增；缺口用 `skip` 控制帧消除 | 修复 v2 遗留问题：过期事件会卡住后续事件 |
| 2026-10-26 | 事件不转发（B 收到 A 的事件不再发给 C） | 避免多设备回路 |
| 2026-10-26 | Windows 壳目标框架 `net10.0-windows`（原计划 .NET 8） | 我记得 .NET 8 在 2026-11 结束支持（**未联网核实**）；本机 SDK 是 10 |
| 2026-10-26 | Android 前台服务类型用 `specialUse`，不用 `dataSync` | 我记得 Android 15 对 `dataSync` 有每日约 6 小时限制（**未核实**），会打断长连接 |
| 2026-10-26 | 验证策略：Linux 上做构建/测试；Windows 用 Wine + 已写好的 GitHub Actions；Android 只编译 + JVM 测试，真机留到验收 | 用户裁决（本机无 Windows、无 KVM） |
| 2026-10-26 | 不 `git init`、不提交、不推送 | 用户未授权 |

## 3. 已完成清单

### 3.1 文档（`/home/tony/dev/Bark/docs`）

v3 全套已定稿：`README.md`、`feasibility-analysis.md`、`architecture.md`、`api-spec.md`、`development-plan.md`、`schemas/event.schema.json`、`adr/005-...md`（ADR-004 标为 Superseded，ADR-003 加了更新说明）。v2 全套在 `archive-v2/`（注意：归档文件里的相对链接可能失效，仅供查阅）。

### 3.2 Go 核心（`core/`，模块 `nightclip/core`，仅标准库 + `golang.org/x/mobile`（构建期））

| 包 | 内容 | 测试 |
| --- | --- | --- |
| `proto` | `Canonical`（规范 JSON：键排序、去空白、仅整数、`SetEscapeHTML(false)`）；`ContentHash`；`Envelope` + `Validate(now)`（版本、event_id UUIDv4、kind、device_id 格式、source≠target、sequence≥1、过期、hash 格式与一致性、剪贴板 mime/size/上限 64 KiB）；`Code` 常量与 `*Error` | 5 个测试函数，含 9 个表驱动用例 |
| `identity` | `Generate(name)`（Ed25519 + 10 年自签 X.509，ExtKeyUsage 同时含 server/client auth）；`FromParts`（校验证书与私钥匹配）；`PrivateKeyPKCS8`/`ParsePrivateKeyPKCS8`；`DeviceID`（`dev_` + base32(sha256(SPKI))[:26] 小写）；`Fingerprint`（base64url(sha256(cert DER))，43 字符）；`TLSCertificate()` | 4 个 |
| `facade` | `Core{Init, Call, NextEvent, Shutdown}`；方法 `version`、`status`、`identity.get`；事件 `core.started`；事件队列 256 条，满则丢弃并计数（`events_dropped`）；`Call`/`Init` 统一 `recover` 转 `INTERNAL`；panic 栈写 stderr。`Init` 校验 `data_dir`（绝对路径）与 `db_key_b64`（标准 base64，带或不带填充，解码后 32 字节），打开 `store` 并 `LoadOrCreateIdentity`；任何一步失败都关库、不发 `core.started`，且可重试。`Shutdown` 关库（幂等），之后所有方法返回 `NOT_INITIALIZED`，再 `Init` 返回 `INVALID` | 15 个顶层（含子用例 25） |
| `cmd/cbcore-dll` | `-buildmode=c-shared`，导出 `cb_init/cb_call/cb_next_event/cb_shutdown/cb_free`；每个导出都 `defer recover` | `test/abi/smoke.c`（Linux .so）、`test/abi/smoke_win.c`（Wine） |
| `mobile` | gomobile 表面：`Core`（`NewCore`→Java 构造器）+ `Init/Call/NextEvent/Shutdown` | AAR 构建 + `javap` 核对签名 |
| `cmd/cbridge` | 仅 `version`；其它子命令返回退出码 3（命名管道未实现） | — |
| `store` | `Open`/`Close`、schema v1（10 表 + 4 索引）、幂等迁移、AES-256-GCM 字段封装、身份落盘与数据层 CRUD；**已由 `facade.Init` 使用**。其余 API（peers/links/outbox/inbox/settings/clip_state/revoked_fps）已有单测，等 `peer`/`clip` 调用（见 [HANDOFF-STORE.md](HANDOFF-STORE.md)） | 40 个 |
| `peer/ pairing/ clip/ xfer/` | 仅 `doc.go`，指向设计章节 | — |
| `tools.go`（build tag `tools`） | 固定 `golang.org/x/mobile/bind`，防止 `go mod tidy` 删掉它（已验证 tidy 后 `make aar` 仍成功） | — |

### 3.3 Windows（`windows/`）

- `ClipBridge.Windows/`：WPF 应用（`net10.0-windows`，x64）。`Interop/CoreInterop.cs` 含 `Native`（P/Invoke，`LPUTF8Str`，`cb_free` 释放）、`CoreHost`（`Start`、`Call`→结果或 `CoreException`、专用事件泵线程循环 `cb_next_event(1000)`、`Dispose` 先置停止标志再 `cb_shutdown` 再 `Join(3s)`）。`MainWindow` 启动核心、显示 `status` 与事件。无托盘、无单实例、无剪贴板监听。
- `ClipBridge.Windows.Tests/`：xUnit，把 `CoreInterop.cs` 以链接方式编进来，Linux 上加载 `libclipbridge_core.so`，Windows 上加载 DLL。一个生命周期测试（核心是进程级单例，所以不拆多个测试）。

### 3.4 Android（`android/`）

Gradle wrapper 8.11.1 已生成；AGP 8.7.3、Kotlin 2.1.0（compose 插件）、compileSdk/targetSdk 35、minSdk 26、Compose BOM 2024.12.01 —— **按这些版本 debug 和 release(R8) APK 均构建成功**。

- `CoreHost.kt`：进程级单例，`start`（幂等、`listen=false`）、`call`、`nextEvent`、`shutdown`。
- `SyncForegroundService.kt`：`specialUse` 前台服务；常驻通知；工作线程循环 `nextEvent`，目前只打日志。
- `MainActivity.kt`：Compose 状态页（`status` + 刷新 + 启动前台服务）。
- Manifest 权限：INTERNET、ACCESS_NETWORK_STATE、POST_NOTIFICATIONS、FOREGROUND_SERVICE、FOREGROUND_SERVICE_SPECIAL_USE。
- ProGuard 保留 `go.**` 与 `com.nightclip.core.**`。

### 3.5 构建与 CI

- `Makefile` 目标：`check test vet dll dll-smoke dll-wine test-interop build-windows-app cli aar apk clean`。
- `.github/workflows/ci.yml`：linux / android / windows 三个任务，**已写好，从未运行**（无 git 远端；Action 版本、runner 镜像、`setup-go` 对 Go 1.27 的支持、windows-latest 自带 MinGW 的假设均未验证）。

## 4. 验证矩阵

| 项 | 状态 | 证据 / 方法 |
| --- | --- | --- |
| Go 单测（含 `-race`） | ✅ 已验证 | `make test`：facade 15、store 40、identity 4、proto 5 个顶层测试 |
| gofmt / go vet | ✅ | `make vet` |
| C ABI 逻辑 | ✅ | `make dll-smoke` |
| Windows DLL 可加载、Go 运行时启动、UTF-8、`shutdown` 唤醒、SQLite 在 Windows 用户态读写 | ✅（在 Wine 下） | `make dll-wine`（含临时 `data_dir`、被拒配置不影响后续 init、关闭后 `NOT_INITIALIZED`） |
| C# P/Invoke 编组、UTF-8 往返、事件泵、`Dispose` <2s | ✅（对 Linux .so） | `make test-interop` |
| WPF 应用编译（含 XAML） | ✅ | `make build-windows-app`（`EnableWindowsTargeting`） |
| AAR（arm64-v8a、x86_64）、debug/release APK、lint 运行 | ✅ | `make apk` 只构建 debug；release 另跑 `./gradlew :app:assembleRelease`（需 `ANDROID_HOME`/`JAVA_HOME`）。接入 SQLite 后：AAR 7.6 → 15.6 MB，release APK 15 → 28.9 MB，debug APK 50.3 MB |
| gomobile 生成的 Java API 与 Kotlin 调用一致 | ✅ | `javap` 核对 `Core()`、`call`、`init`、`nextEvent(long)`、`shutdown` |
| WPF 窗口实际渲染 / 托盘 / 剪贴板 / DPAPI / 防火墙 | ❌ 未验证 | 需要真实 Windows 或 Windows CI |
| C# 互操作在真实 Windows 上加载真实 DLL | ❌ 未验证 | Windows CI 任务会做，但未运行 |
| Android 上核心实际运行、经 Tailscale VPN 拨号（P-A） | ❌ 未验证 | 需要真机 |
| 前台服务存活、Doze、OEM（P-C） | ❌ 未验证 | 需要真机 |
| 任何两台设备之间的通信 | ❌ 不存在 | 网络层未写 |

## 5. 已知限制与代码层面的坑

1. **壳里的 key 是临时方案**：Windows `Interop/DevKey.cs` 与 Android `DevKey.kt` 把随机 32 字节 key 以明文 base64 放在数据目录的 `devkey.b64`，只靠用户配置文件 ACL / 应用沙箱保护。`TODO(WP9: DPAPI)`、`TODO(WP10: Keystore)`。key 文件损坏时壳会报错而不是重建（重建会让已有加密库永远打不开）。
2. **核心不能重启**：`facade.Core.Init` 只能调用一次；DLL 里是进程级全局单例，`cb_shutdown` 之后同进程不能再 `cb_init`。Android 的 `CoreHost.shutdown()` 会置空并可新建 `Core()`（gomobile 对象是新实例），但服务和 Activity 共用同一个 `CoreHost`：服务 `onDestroy` 会关掉 Activity 也在用的核心。接入真实逻辑前要重新设计生命周期（建议核心由 Application 级持有，服务只负责保活）。
3. **`NextEvent` 单消费者**：事件通道只有一个，多个消费者会互相抢事件。当前每个壳只有一个事件泵线程，符合设计。
4. **`emit` 在 `Shutdown` 之后仍会入队**，没有被阻止；无害但不整洁。
5. **`Canonical` 拒绝非整数数字**（包括 `1.5`、`1e2`）。这是有意的（保证跨实现确定性），将来 payload 如果需要浮点必须改规范。
6. **`Envelope.Validate` 不检查对端关系**：不核对 TLS 指纹与 `source_device_id` 是否一致，不核对 `target_device_id == 本机`。这些归 `peer` 层。
7. **Android 壳不处理 `Core.init` 异常**：`CoreHost.start` 失败（key 文件损坏、`KEY_INVALID`、`DB_UNAVAILABLE`）会直接抛出到 `MainActivity`/`SyncForegroundService`，没有用户可见的错误页。接 WP10 时补。
8. **panic 日志**直接写 `os.Stderr`，尚无结构化日志；Windows GUI 进程没有控制台，stderr 会丢失。应改成写文件，并确保不含 payload/密钥（`facade.go` 里留有 TODO）。
9. **gomobile 的 `go.mod` 依赖**：`golang.org/x/mobile` 现在是直接依赖（通过 `tools.go`）；`go mod tidy` 会下载 `go-cmp` 等；这不影响运行时二进制。
10. **规范文档与代码的空缺**（实现 `pairing` 前必须先补进 `api-spec.md`）：
    - SAS 公式 `sha256(fpA || fpB || nonce)` 没有规定 fpA/fpB 的顺序。建议按指纹字典序排序后拼接，使两端得到相同结果。
    - 配对 `proof = HMAC(psk, device_id || cert_fp || nonce)` 的拼接没有长度前缀，存在歧义拼接风险。建议改成带长度前缀或分隔符的固定编码。
    - `/v1/pair` 对未配对指纹开放，需要按来源 IP 限流（规范只写了会话级限流）。
11. **`.gitignore` 已写**，但因为没有 git 仓库，目前没有实际效果。

## 6. 需要真实设备才能证明的事（验收清单）

对应 `development-plan.md` §3.3。**Windows CI 任务（已写未跑）能覆盖其中 P-B 的一部分。**

| 编号 | 内容 | 怎么做 | 通过标准 |
| --- | --- | --- | --- |
| P-A | Android 核心经 Tailscale VPN 用 IP 直拨 Windows 监听端口 | 前置：`peer` 的 TLS 拨号/监听已实现。把 debug APK 装到真机，手机和 Windows 都登录同一 tailnet；Windows 上跑监听；手机点连接 | 连接成功；日志显示握手完成；全程不调用 `net.Interfaces()`；不依赖 DNS |
| P-B | C# P/Invoke 事件泵 24h 稳定 | 在 Windows 上长跑 `ClipBridge.Windows.Tests` 的加长版或主程序，持续产生事件 | 无死锁、无句柄/内存持续增长、退出干净 |
| P-C | Android 前台服务在目标 OEM 上锁屏 30 分钟后仍连接，或断线可补齐 | 真机，目标 OEM（Samsung/小米/OPPO 任选）；记录 `SyncForegroundService` 存活与重连日志 | 存活，或被杀后打开 App 能按游标补齐 |
| P-D | 直连与 DERP 路径下 100 MiB 吞吐 | 两台设备分别在能直连和被迫走 DERP（`tailscale ping`/`tailscale status` 查看路径）的网络上传文件 | 记录数字，判断是否可接受 |
| P-E | Windows 休眠/唤醒、Tailscale 重启后自动重绑并重连 | 在 Windows 上做休眠、`tailscale down/up`、重启 Tailscale 服务 | 核心重新绑定 tailnet 地址，对端自动重连 |

另外必须真机验证：WPF 窗口渲染与托盘、`WM_CLIPBOARDUPDATE` 与回声抑制、DPAPI、Windows 防火墙入站规则（限定 `100.64.0.0/10`）、Android 剪贴板读取的前台焦点限制、通知动作、MediaStore 保存。

当前 tailnet 里有 Windows 节点 `afvi13/14/2/3`，状态均为 offline（最近一次在线 1–11 天前）。我没有连接过它们。

## 7. 下一步（详细）

顺序按依赖排列。估算沿用 `development-plan.md`，**未验证**。每个工作包都以"测试先行、Linux 上能跑完"为目标。

### WP1 `store`：持久化 + 身份落盘 —— ✅ 已完成（2026-10-26）；下面的原始规格保留作参考

**目标**：重启后 device_id 不变；敏感字段落盘加密。

- 新增依赖：`modernc.org/sqlite`（纯 Go，Android/Windows 交叉编译无需 cgo）。**这是新增第三方依赖，开始前提一句即可，设计上已认可。**
- 文件：`store/db.go`（`Open(dir string, key []byte) (*DB, error)`，WAL，`PRAGMA foreign_keys=ON`）、`store/migrate.go`（`schema_version` 表，幂等迁移）、`store/seal.go`（AES-256-GCM `Seal(plain, aad)`/`Open`，nonce 随机 96 位前置；AAD = 表名 + 列名 + 主键，防密文搬运）、`store/identity.go`（`LoadOrCreateIdentity(name)`：私钥 PKCS8 经 `Seal` 存储，证书 DER 明文）。
- 表结构按 `architecture.md` §5：`settings, identity, peers, links, outbox, inbox, transfers, clip_state, revoked_fps`。`*_enc` 列用 `Seal`。
- `facade.Init`：解析 `db_key_b64`（必须 32 字节，缺失或长度不对返回 `INVALID`）；`store.Open(data_dir/clipbridge.db)`；用 `LoadOrCreateIdentity` 替换 `identity.Generate`。测试可用 `db_key_b64` 随机值 + `t.TempDir()`。
- 测试：重开库 device_id 不变；错误密钥打不开（返回明确错误码，不 panic）；迁移重复执行无副作用；`Seal` 的 AAD 被篡改则失败；outbox/inbox/peers 的基本 CRUD。
- 验收：`make test` 通过；`make dll-smoke`/`dll-wine`/`test-interop` 里把配置补上 `db_key_b64` 与临时 `data_dir` 后仍通过；`facade.Init` 的错误路径有测试。

### WP2 `peer`：mTLS 链路、发件箱、游标（约 1–1.5 周，最大的一块）

**目标**：两个核心实例之间能可靠地交换事件，行为符合 `architecture.md` §3.3、§4 与 `api-spec.md` §4。

- 新增依赖：`github.com/coder/websocket`（croc 的 go.mod 已用 v1.8.15，纯 Go）。
- `peer/tlsconf.go`：TLS 1.3 only；`ClientAuth: tls.RequireAnyClientCert`（保证总能拿到对端证书指纹）；不依赖系统根；服务端和客户端都在握手后/handler 里用 `Fingerprint(cert.Raw)` 查 `peers` 表。客户端拨号时固定期望指纹（`VerifyPeerCertificate` 比较）。
- `peer/netguard.go`：`InTailnet(netip.Addr)`（`100.64.0.0/10` 与 `fd7a:115c:a1e0::/48`）；`TailnetAddrs()` 枚举本机接口（**仅 Windows/监听方调用，Android 不得调用**）；测试用的 `AllowLoopback` 开关只能由内部测试构造器设置，不得出现在 `Config` JSON 里。
- `peer/listener.go`：只绑 tailnet 地址；轮询（建议 5 s）发现地址变化并重绑；找不到地址时不监听并发 `core.warning NO_TAILNET`。
- HTTP 路由：`/v1/ws`（需已配对指纹）、`/v1/pair*`（接受未配对指纹，WP3）、`/healthz`。
- `peer/link.go`：每个对端一个链路 goroutine。拨号方指数退避 1/2/4…60 s 加抖动；认证失败（`NOT_PAIRED`）不重试；同一对端同一时刻只保留一条 WebSocket（新连接取代旧连接）；Win↔Win 按 `device_id` 字典序决定谁拨号。
- 握手：`hello{device_id,name,tailnet_addrs,recv_cursor}` / `hello_ack{...,recv_cursor,max_frame_bytes}`；`SetReadLimit(64 KiB)`；25–30 s ping，60 s 无帧判断断线。
- 发送：发件箱按链路分配 `sequence`（`links.next_seq_out`，事务内递增）；连接建立后从 `sequence > 对方 recv_cursor` 且未过期处补发；缺口（事件已过期被清）先发 `skip{through}`；ack 到达后标记完成。过期清扫器定时运行。
- 接收（严格按 `architecture.md` §4.3 顺序）：指纹→来源设备一致 → `target_device_id == 本机` → 未过期 → `event_id` 去重（重复仍回 ack）→ `content_hash` 校验（`Envelope.Validate`）→ sequence 处理（连续则推进游标；乱序缓存最多 64 条；超出发 `resync`；收到 `skip` 推进游标）→ 写 inbox → 回 `ack(received)` → 发 `clip.received`/`file.offered` 事件给壳。
- 事件**不转发**。
- 测试（见 WP5 的双实例框架）：握手成功；指纹不符被拒；未配对指纹访问 `/v1/ws` 得 `NOT_PAIRED`；非 tailnet 来源被拒；断线 5 分钟后补发（用可注入时钟/缩短参数）；重复事件只应用一次；过期事件不应用；`skip` 后游标前进、后续事件不被卡住；乱序缓存与 `resync`；同一对端双连接只留一条；退避参数；`SetReadLimit` 生效。
- 验收：以上测试全部通过且 `-race` 干净。

### WP3 `pairing`：配对流程（约 3–4 天）

**前置**：先把 §5 第 10 条的三个规范空缺补进 `api-spec.md`（SAS 排序、proof 编码、按 IP 限流）。

- `pairing/session.go`：内存会话（128 位随机 psk、5 分钟、3 次失败作废）；`pairing/qr.go`：构造/解析 `clipbridge://pair?v=1&host=&port=&fp=&psk=&exp=`（严格校验各字段，拒绝缺失/过期）。
- `pairing/sas.go`：`SAS(fpA, fpB, nonce)` → 6 位十进制，两端结果必须相同（用测试保证）。
- HTTP：`POST /v1/pair`（校验 HMAC proof，生成 `pair_id`，返回 `202 + sas`）；`GET /v1/pair/{id}` 轮询（≤120 s）；Windows 端用户确认后写 `peers` 表，状态 `accepted`；拒绝则 `rejected`。成功后 psk 立即作废。
- 门面方法：`pair.begin / pair.join / pair.confirm`；事件 `pair.request`、`pair.result`。
- 手动输入路径（无 `fp`）：走 TOFU + SAS 比对，UI 必须强制显示 SAS。
- 测试：正常配对（两端 SAS 相同、双方 peers 表都写入）；错误 psk；psk 过期；连续 3 次失败作废；psk 单次有效；QR 中 `fp` 与实际服务端证书不符时客户端中止（模拟中间人）；按 IP 限流。
- 验收：测试全绿；配对完成后的 mTLS 连接（WP2）可建立。

### WP4 `clip`：策略与回声（约 3 天）

- `clip/policy.go`：`Decide(peerApplyMode, sensitive, platform) → notify | manual | auto`（表见 `architecture.md` §6.3）；敏感内容永不 `auto`。
- `clip/echo.go`：`last_applied_hash`、`last_local_sent_hash`、`suppress_until`（建议默认窗口 2–3 s，做成设置项）；`OnLocalChanged(text)` 返回是否发送；`OnApplied(eventID, hash)` 记录抑制。
- 门面方法：`clip.local_changed`、`clip.send`、`clip.mark_applied`、`clip.dismiss`、`inbox.list/get/clear`、`settings.get/set`、`peers.list/update/remove`。
- 测试：回声（远端写入后的本地变化不外发）、同内容重复复制、抑制窗口过期后恢复发送、敏感不自动应用、各 `apply_mode` 的决策表、空文本不发送、>64 KiB 报 `TOO_LARGE`。

### WP5 双实例集成测试框架（与 WP2 同步建设，约 2 天）

- `core/internal/itest`：`NewPair(t)` 启动两个 `facade.Core`（一个 `platform=windows, listen=true`，一个 `platform=android, listen=false`），各用 `t.TempDir()`、随机 db key、回环地址与随机端口；提供 `Pair(t)`（走完整配对）、`WaitEvent(core, type, timeout)`、可注入时钟、断网模拟（关闭/重开链路）。
- 用它覆盖 `development-plan.md` 里 A1–A12 中与核心相关的全部场景，以及 Phase 0 退出标准里的"断网 5 分钟补齐、10 次重复只应用一次、事件和 token 不进日志"（日志检查：捕获日志输出并断言不含 payload、psk、私钥）。

### WP6 门面接线 + 事件（约 2 天）

把 `peer/pairing/clip/store` 接进 `facade`，实现 `api-spec.md` §7.3 的方法与 §7.4 的事件；补齐 `status`（对端列表、连接状态、tailnet 地址、发件箱长度、游标）和 `diagnostics.get`。保证 DLL/AAR/（未来的）管道三处方法集一致。

### WP7 `xfer`：文件传输（约 2 周，Phase 2）

按 `architecture.md` §7 与 `api-spec.md` §5：`file.offer / accept / reject / cancel`、`transfers.list`；pull 模式（`GET` + `Range`）和 push 模式（`file_accept{offset}` 后 `PUT` + `Content-Range`）；发送前整文件 SHA-256；临时文件 + 校验后落盘；`HEAD` 恢复；路径清洗（`..`、盘符、保留名、路径分隔符）；磁盘空间预检；Android 用 `fd` 传入（`os.NewFile`），完成后由 Kotlin 写 MediaStore。大文件（≥2 GiB）流式，不整体入内存。测试含中断续传、校验失败不落盘、哈希不变才允许续传、恶意文件名。

### WP8 命名管道 + `cbridge` CLI（约 3 天，Phase 1）

核心（仅 Windows 构建标签）提供 `\\.\pipe\clipbridge.v1`，ACL 仅当前用户，JSON Lines，方法集与门面一致（`shutdown`、`pair.*` 除外）。为了能在 Linux 上测试，开发期用 Unix socket 做同一协议的替代传输（构建标签区分）。`cbridge` 实现 `api-spec.md` §9 的命令与退出码。管道库可用 `github.com/Microsoft/go-winio`（新依赖）或直接 `syscall`。

### WP9 Windows 壳（约 2 周，Phase 0–1，**需要 Windows 才能完整验证**）

单实例互斥；托盘（`System.Windows.Forms.NotifyIcon` 或第三方库）；`AddClipboardFormatListener` + `GetClipboardSequenceNumber`，读取失败重试 1–2 次，空内容不发；写剪贴板前比较序号（用户刚复制则不覆盖）；DPAPI（`ProtectedData`，`CurrentUser`）保护数据库主密钥并传入 `db_key_b64`；配对界面（QR 生成，如 `QRCoder`）与 SAS 确认；设置页；开机启动；Toast；恢复上一个剪贴板；安装器与限定远端地址的防火墙规则（Phase 3）。验证路径：Windows CI 任务 + 真机。

### WP10 Android 壳（约 2 周，Phase 0–1，**需要真机才能完整验证**）

扫码配对（ZXing，无 Play 服务时也可用）；Keystore 包装的主密钥传入 `db_key_b64`；通知渠道与动作（复制/打开/忽略）；分享目标（`text/plain`、`*/*`）；快捷设置磁贴（透明 Activity 获得焦点后读取剪贴板）；实时模式开关与电池优化引导；Tailscale 未连接提示（`NetworkCapabilities.TRANSPORT_VPN` 仅作提示）；核心生命周期改造（见 §5 第 2 条）；MediaStore 保存。

### WP11 真机 PoC（P-A…P-E）与发布（Phase 3）

见 §6。发布：Windows 安装器 + 防火墙规则 + 签名（如有证书）；Android 签名 APK/AAB；日志脱敏审计；长稳；用户文档与 tailnet ACL 示例。

### 建议的执行顺序与里程碑

1. WP1 → WP2/WP5（一起）→ WP3 → WP6：**里程碑 M1 = 双实例在 Linux 上完成"配对 → 双向文本 → 断线补发 → 去重/过期/skip → 指纹拒绝"，全部自动化测试通过。**
2. WP4 → M2 = 剪贴板策略与回声在核心层闭环。
3. 并行或随后：WP9/WP10 的壳功能；真机跑 P-A、P-B（需要 WP2 完成）。
4. WP7 → WP8 → WP11。

## 8. 需要用户裁决 / 授权的事项

| 事项 | 为什么需要问 | 我的建议 |
| --- | --- | --- |
| ~~是否 `git init` 并提交；是否建远端并推送~~ | 已决定（2026-10-26）：远端 `orangepro2001/night-clip`，阶段性功能完成后直接提交推送 | 已执行 |
| 新增第三方依赖：`modernc.org/sqlite`、`github.com/coder/websocket`，以及后续的 `go-winio` | 设计已认可，但属于新增依赖 | 同意 |
| 命名：目录 `night-clip` vs 协议/产品 `ClipBridge` | 目前混用（线上名 `clipbridge/v1`、`clipbridge_core.dll`、`\\.\pipe\clipbridge.v1`；仓库名 `night-clip`） | 保持现状，README 已说明；若要统一，现在改成本最低 |
| 把 `docs/` 移进 `night-clip/` 使仓库自洽 | 目前在 `night-clip/` 之外（`../docs`） | 建议移入 |
| 用哪台 Windows 做最终验收 | tailnet 里的 `afvi*` 当前都离线 | 需要时告知开机 |
| Android 分发方式（侧载 vs Play） | `specialUse` 前台服务在 Play 上需要说明理由 | 先侧载 |
| tailnet ACL | 建议只放行两台设备之间的 47821 | 文档里给示例，由用户决定是否收紧 |

## 9. 我没有核实的断言（别当事实）

- .NET 8 将在 2026-11 结束支持（凭记忆）。
- Android 15 对 `dataSync` 前台服务有每日约 6 小时限制（凭记忆）；`specialUse` 在 Play 上的审核要求同理。
- `.github/workflows/ci.yml` 里的 Action 版本、`windows-latest` 自带 MinGW gcc、`setup-go` 支持 Go 1.27。
- WPF 在 Linux 上"编译通过"只代表 XAML 与代码能编译，不代表运行正确。
- 开源调研的结论（ClipCascade、Clipbird、KDE Connect、LocalSend、pion、libp2p、tsnet 的特性与限制）来自搜索摘要，没有深入读源码。
- `development-plan.md` 里的周期与人周估算。

## 10. 文件清单（`night-clip/`）

```text
.editorconfig  .gitignore  .github/workflows/ci.yml  Makefile  README.md  HANDOFF.md
core/go.mod  core/go.sum  core/tools.go
core/proto/{canonical.go,envelope.go,errors.go,proto_test.go}
core/identity/{identity.go,identity_test.go}
core/facade/{facade.go,facade_test.go}
core/cmd/cbcore-dll/main.go          core/cmd/cbridge/main.go
core/mobile/mobile.go
core/store/{db.go,migrate.go,identity.go,peers.go,links.go,outbox.go,inbox.go,settings.go,clipstate.go,revoked.go,store_test.go,identity_test.go,crud_test.go}
core/{peer,pairing,clip,xfer}/doc.go
core/test/abi/{smoke.c,smoke_win.c}
windows/ClipBridge.Windows/{ClipBridge.Windows.csproj,App.xaml,App.xaml.cs,MainWindow.xaml,MainWindow.xaml.cs,Interop/CoreInterop.cs,Interop/DevKey.cs}
windows/ClipBridge.Windows.Tests/{ClipBridge.Windows.Tests.csproj,CoreInteropTests.cs}
android/{settings.gradle.kts,build.gradle.kts,gradle.properties,gradlew,gradlew.bat,gradle/wrapper/*}
android/app/{build.gradle.kts,proguard-rules.pro,libs/.gitignore}
android/app/src/main/{AndroidManifest.xml,java/com/nightclip/app/{CoreHost,DevKey,MainActivity,SyncForegroundService}.kt}
```

构建产物（均被 `.gitignore` 忽略）：`core/dist/`（各平台 DLL/.so/.h/exe/smoke）、`android/app/libs/*.aar|*.jar`、`android/**/build/`、`windows/**/bin|obj`。

## 11. 与 `development-plan.md` Phase 0 清单的对照

| Phase 0 项 | 状态 |
| --- | --- |
| 仓库与模块结构、`go.mod`、CI | 结构 ✅；CI 文件已写未跑 |
| `identity` | ✅（已持久化） |
| `proto`（信封、规范 JSON、hash、Schema 校验测试） | ✅ 信封与 hash；❌ 尚未对照 `event.schema.json` 做自动化校验测试（建议补：用 JSON Schema 库或手写用例保证二者一致） |
| `store` | ✅ schema/迁移/加密/身份落盘/CRUD 全部有测试，身份已接入门面 |
| 发件箱 / sequence / 游标 / skip / 去重 / TTL | ❌ |
| `pairing` | ❌ |
| WebSocket 链路 | ❌ |
| `facade` | 部分（3 个方法 + 1 个事件；已接 store） |
| `cbcore-dll` 与 `mobile` | ✅ |
| 双进程集成测试 | ❌ |
| Windows：托盘骨架、`CoreInterop`、DPAPI、配对 UI、手动发送/接收 | 仅 `CoreInterop` ✅ 与最小窗口 |
| Android：Compose 骨架、AAR 接入、Keystore、扫码、前台服务、通知、分享 | 骨架 + AAR + 前台服务外壳 ✅；其余 ❌ |
| P-A…P-E | ❌ |
