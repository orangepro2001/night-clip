# night-clip 交接文档

> 更新于 2026-10-26（功能开发完成、待真机验证阶段）。读完这一份就能接着干活。
> 用户文档：[docs/user-guide.md](docs/user-guide.md)。设计：[docs/README.md](docs/README.md) → `architecture.md` → `api-spec.md`。更新/发布：[docs/update.md](docs/update.md)。壳层开发指南：[docs/shell-guide.md](docs/shell-guide.md)。

## 0. 一页摘要

- **产品**：Windows ↔ Android 剪贴板（文本）同步 + 文件传输。设备都在同一个 Tailscale tailnet 里直连，无服务端。仓库 `git@github.com:orangepro2001/night-clip.git`（公开，`main`）。
- **架构**：一个 Go 核心承载全部业务；Windows 用 C# WPF 壳 + Go 编译的 DLL；Android 用 Kotlin Compose 壳 + gomobile AAR。壳只通过 `Call(method, json)` / `NextEvent(timeoutMs)` 访问核心。
- **现状：功能完整**，已按 [docs/plan-auto-broadcast.md](docs/plan-auto-broadcast.md) 把“复制即自动广播”做成默认（见该文 §11）。配对、文本同步、文件传输、离线补发、撤销、命名管道与命令行、自动更新与本机分发、两端完整界面都已实现，核心全部有测试。**两端壳的系统相关行为（托盘、剪贴板监听、通知、相机、前台服务、MediaStore…）只编译、没在真机上跑过**——这是现在最大的未知，见 §6。
- **验证**：`make check` 全绿；GitHub CI（linux / windows / android）全绿；核心所有包的测试同时以 Windows 程序在 Wine 下通过。
- **本机分发**：`clipbridge-dist` 用户服务（`127.0.0.1:18780`，`Linger=yes`）经 `tailscale serve --set-path /clipbridge` 暴露；下载页 `https://tony-ai-pc-1.tail9f7f1f.ts.net/clipbridge/`。每个阶段性功能：提交 → 推送 → `scripts/release-local.sh`，设备约 1 秒内收到更新。
- **下一步**：真机验证清单（§6）→ 修问题 → 全部满意后配置 GitHub `nightly` 滚动发布（§7）。

## 1. 接手：5 分钟上手

```sh
cd /home/tony/dev/Bark/night-clip
export GOCACHE=$PWD/.gocache        # 沙箱里默认的 Go 构建缓存只读，必须设
make check                          # 约 1 分钟：vet、Go 测试(-race)、C ABI、Wine 下 DLL、C# 测试、WPF 编译、CLI、AAR、APK
```

`make check` 依次：`vet` → `test`（核心，-race）→ `dll-smoke`（Linux .so + C）→ `dll-wine` → `test-interop`（C# xUnit，含 273+ 个壳逻辑测试）→ `build-windows-app` → `cli` → `aar` → `apk`（含 JVM 单测）。

### 工具链（本机，非仓库内容）

| 工具 | 位置 |
| --- | --- |
| Go 1.27 | `/usr/local/bin/go`（模块缓存 `~/go/pkg/mod` 可写） |
| mingw-w64、Wine | apt（`wine64`/`wine`，Makefile 自动选） |
| .NET 10 SDK | `~/.dotnet/dotnet`（不在 PATH，Makefile 自动找） |
| JDK 17 / Android SDK 35 / NDK 28.2 / gomobile / Gradle | 见 Makefile 头部变量；`android/gradlew` |
| `gh` | apt 安装，已登录 `orangepro2001`（token 在 `~/.config/gh/hosts.yml`） |

注意：交互 shell 里 `ls`/`cat` 是 `eza`/`bat` 别名，`ls` 偶尔会卡住——脚本里用 `/usr/bin/ls`、`/usr/bin/cat`；zsh 下含 `?` 的 URL 要加引号。

### 在 Wine 里跑 Windows 版测试

```sh
cd core && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o /tmp/p.test.exe ./peer
cd peer && WINEDEBUG=-all WINEPREFIX=$HOME/.wine-clipbridge WINEDLLOVERRIDES="mscoree,mshtml=" wine64 /tmp/p.test.exe
```
它抓到过 Linux 上看不到的 Windows 问题（打开中的文件不能删除、Unix 路径当绝对路径）。Wine 不实现 `FILE_FLAG_FIRST_PIPE_INSTANCE`，相关两条断言在 Wine 下跳过、真 Windows 上保留。

## 2. 已做出的决策（及原因）

| 日期 | 决策 | 原因 |
| --- | --- | --- |
| 10-26 | 范围仅 Windows + Android；所有设备在同一 tailnet 是前提 | 用户确认；tailnet 已解决 NAT 与链路加密，所以没有 Gateway/STUN/WebRTC |
| 10-26 | Go 核心 + WPF（C#）+ c-shared DLL；Android Kotlin + gomobile AAR | 用户裁决 |
| 10-26 | 数据搬运总由拨号方发起；Windows 监听（只绑 tailnet 地址），Android 只出站 | 避免 Android 监听端口与网卡枚举限制 |
| 10-26 | mTLS + 配对时固定证书指纹；不做 payload 二次加密 | 没有不可信中间方 |
| 10-26 | `sequence` 按链路递增；缺口用 `skip` **链路帧**（非事件）消除；帧上限 512 KiB | 过期事件不卡住后续；JSON 转义会膨胀 64 KiB 文本 |
| 10-26 | 配对 proof 把 TLS 指纹绑进 HMAC（长度前缀编码）；SAS 按指纹排序 | 即使手动输入（无 QR 指纹）中间人也过不了 |
| 10-26 | 刚配对后 2 分钟内的 `NOT_PAIRED` 视为暂时性并重试 | Win↔Win 时监听方可能比加入方先保存对端（测试发现的真实竞态） |
| 10-26 | 数据库密钥：Windows DPAPI / Android Keystore；旧明文文件一次性迁移 | 用户接受测试 key 作为过渡，现已替换 |
| 10-26 | 自更新：Ed25519 签名清单 + SHA-256 + 可续传下载；`watch` 长轮询实时通知 | Windows 无 OS 级同签名校验；见 update.md |
| 10-26 | 版本号 = 提交数；工作区不干净拒绝发布 | 单调、无需手工 bump |
| 10-26 | 命名管道用白名单（`pair.*`、`peers.update/remove`、`update.*`、`settings.set` 不开放） | 同用户其它进程不得改变信任关系 |
| 10-26 | 文件：监听方服务字节（pull=GET+Range，push=file_accept{offset}+PUT）；`.part` 校验后原子改名 | 见 api-spec §5.4 |
| 10-26 | 对抗式安全审查后的修复：每对端独立 event_id；监听方向加入方出示 `ServerProof`；被移除设备可再配对；pull 传输由接收方发 `file_done` 才算送达；PUT 可被取消/过期/解除配对中止并可用 `bytes */N` 补完；事件载荷/寿命/收件箱/并发连接上限；管道调用方不能传 `fd`/`save_dir`、读不到敏感正文；更新清单“最高见过版本”防回滚、拒绝 https→http 重定向 | 审查报告 14 项，见 core 各包 `security_test.go` 与 api-spec |
| 10-26 | 两个壳由子代理并行施工，核心与壳通过 `docs/shell-guide.md` 契约对接 | 并行；逻辑与界面分离以便单测 |

## 3. 已完成清单

### 3.1 Go 核心（`core/`，模块 `nightclip/core`；依赖：`modernc.org/sqlite`、`coder/websocket`、`skip2/go-qrcode`、gomobile）

| 包 | 内容 | 测试（顶层） |
| --- | --- | --- |
| `proto` | 规范 JSON、content_hash、信封校验、错误码 | 5 |
| `identity` | Ed25519 身份、自签证书、device_id、指纹 | 4 |
| `store` | SQLite（schema v3）、AES-GCM 字段加密、身份落盘、对端/链路/发件箱/收件箱/传输 CRUD | 51 |
| `peer` | mTLS 监听（只绑 tailnet、随接口变化重绑）、WebSocket 链路、发件箱补发、游标、skip、重排缓冲、去重 ack、替换连接、退避重连、撤销 | 32 |
| `pairing` | 链接解析、proof、SAS、监听方管理器、加入方客户端、限流 | 23 |
| `clip` | 文本规范化、回声抑制、敏感识别、策略、历史 | 17 |
| `xfer` | 文件传输（见 api-spec §5.4） | 26 |
| `pipe` + `cli` + `cmd/cbridge` | 本地控制通道与命令行 | 9 + 9 |
| `update` + `cmd/cbdist` | 签名清单、下载器、管理器、分发服务器、发布器 | 37 |
| `facade` | 全部方法与事件；事件顺序保证 `core.started` 在先 | 32 |
| `internal/testnet` | 测试用的“多台真实设备”夹具（仅 _test 使用） | — |
| `cmd/cbcore-dll`、`mobile` | C ABI / gomobile 入口 | ABI 冒烟 + Wine |

### 3.2 Windows 壳（`windows/`）
WPF + WinForms NotifyIcon。托盘、单实例、DPAPI 密钥与迁移、剪贴板监听与写入决策（序号守卫）、配对窗口（二维码用核心给的矩阵绘制）、设备/收件箱/传输/设置/诊断页面、文件收发、更新栏。逻辑在无 WPF 依赖的类里，273 个 xUnit 测试（总 299）。

### 3.3 Android 壳（`android/`）
Compose。Application 级唯一事件泵 + SharedFlow、Keystore 包装密钥与迁移、首次引导、扫码/粘贴/手动配对、四种发送入口、接收通知（复制/忽略）、实时模式前台服务与开机恢复、设备/收件箱/设置页、文件分享发送（fd 交接）与 MediaStore 保存。102 个新 JUnit 测试（总 108）。

### 3.4 构建、CI、发布
`Makefile`（`check`、`dist-windows`、`dist-android`、`cbdist`…）；`scripts/release-local.sh`、`scripts/backup-keys.sh`、`scripts/install-dist-service.sh`；`.github/workflows/ci.yml` 三个任务全绿。

## 4. 验证矩阵

| 项 | 状态 | 证据 |
| --- | --- | --- |
| 核心全部测试（-race，重复运行无抖动） | ✅ | `make test`；xfer/peer/pairing 跑过 ≥15 次 |
| 核心测试作为 Windows 程序 | ✅ | Wine（见 §1） |
| 真 Windows 上的核心测试 | ✅ | GitHub CI windows 任务（曾抓到 `.part` 删除问题，已修） |
| 命名管道真实 Windows 代码 | ✅（Wine） | `FIRST_PIPE_INSTANCE` 抢占检测仅真 Windows 验证 |
| C ABI / Windows DLL / C# 互操作 | ✅ | `dll-smoke`、`dll-wine`、`test-interop` |
| WPF 编译 / APK / R8 release | ✅ | `build-windows-app`、`apk`、`assembleRelease` |
| 双实例协议（配对→同步→断线→续传→撤销） | ✅ | peer/pairing/clip/xfer/facade 的集成测试 |
| 更新链路（真实密钥、实时通知、tailnet https） | ✅ | 0.1.16 实测 |
| **WPF 界面、托盘、剪贴板监听、DPAPI 真调用** | ❌ 未运行 | 需要真 Windows |
| **Android 界面、通知、扫码、前台服务、Keystore、MediaStore、fd 交接** | ❌ 未运行 | 需要真机 |
| Tailscale 真网络下的拨号/重绑（P-A、P-E） | ❌ | 需要真设备 |
| Android 前台服务在 OEM 上存活（P-C）、100 MiB 吞吐（P-D） | ❌ | 需要真机 |

## 5. 已知限制与注意

1. **事件队列**：门面事件队列 1024 条，满则丢弃并计数（`events_dropped`）。壳在启动时重新读取状态以自愈。
2. **回执可能丢**：`applied` ack 只在线时发送，不持久化；发送方的发件箱可能一直停在 `sent`。不影响内容送达。
3. **同一 data_dir 只能一个核心**：没有跨进程锁。
4. **Android 不能在后台读剪贴板**：由用户触发（按钮/分享/通知动作/磁贴）。
5. **fd 来源无法跨重启续传**（`SOURCE_LOST`）；不可 seek 的 fd 会先落盘一份。
6. **Android 6 位 SAS** 对抗能在配对窗口内预计算 10⁶ 证书的攻击者强度有限（QR 路径有指纹固定，不受影响）；手动输入路径靠 psk 的 proof 绑定保护。
7. **Windows DPAPI 的应用熵是固定字符串**，不能防同用户进程解密（只防误用）。
8. `api-spec §10` 提到的外部 Intent `com.clipbridge.action.SEND_TEXT` 未实现。
9. **体积**：AAR 15.6 MB、release APK ≈ 37 MB（`.so` 未压缩打包，双 ABI）；发布只打 arm64 可省约一半。
10. **管道上读不到敏感条目正文**（`inbox show` 只提示去界面看）；`file accept` 不能指定目录。这是有意的，不是缺陷。
11. 代码里一处遗留：`UpdateState.text/buttonText`（Android）已不使用，只被旧单测引用。

## 6. 真机验证清单（现在最重要的事）

对应设计里的 P-A…P-E 与各壳的系统相关行为。请按 [docs/user-guide.md](docs/user-guide.md) §12 走一遍：

- **Windows**：首次运行询问同步；托盘菜单与气泡；复制一次只发一次；密码管理器复制被忽略；DPAPI 迁移（旧 `devkey.b64` 被删）；防火墙弹窗；开机自启 `--minimized`；休眠唤醒/`tailscale down/up` 后自动恢复（P-E）；更新后重启。
- **Android**：扫码配对（含无相机权限时退回）；通知“复制/忽略”；分享与磁贴；实时模式锁屏 30 分钟后仍连接或打开 App 后补齐（P-C）；接收文件保存到“下载”；Android 15 开机恢复前台服务。
- **两端**：配对→双向同步→断网恢复→大文件（含 DERP 路径吞吐 P-D）→撤销。
- 发现的问题请带上 `diagnostics.get` 输出（不含密钥）。

## 7. 下一步

1. 真机验证与修复（§6）。
2. **GitHub `nightly` 滚动发布**（用户要求：全部功能完成后）：CI 在推送到 `main` 时构建 APK 与 Windows zip，更新名为 `nightly` 的预发布。需要把 **同一把** Android 签名密钥与 Ed25519 发布密钥作为仓库密钥提供（否则无法覆盖本机分发的安装、更新清单也无法验证）；版本号用提交数（`fetch-depth: 0`）。应用内更新源仍是本机分发地址。详见 update.md §9。**此项需要用户决定如何提供密钥。**
3. Windows 安装程序（目前是解压即用的 zip；自包含 .NET 还是框架依赖待定）。
4. 可选：Android 的 `SEND_TEXT` 外部 Intent；发布体积优化；日志落盘与脱敏的统一实现。

## 8. 运维备忘与待用户决定

- 密钥（只在本机，**已备份到 `~/backups/clipbridge-keys/`，请另存一份到本机之外**）：`~/.config/clipbridge/` 下的 Ed25519 发布密钥、Android JKS 与 `android-signing.properties`。丢失 JKS = 已安装的 Android 应用只能卸载重装。
- `release.local.env`（git 忽略）里是更新地址；仓库公开，tailnet 名不入库。
- 发布：`git commit` → `git push` → `scripts/release-local.sh --notes "…"`（工作区必须干净）。
- `tailscale serve` 只新增过 `/clipbridge`，其余路径未动。
- 权限策略多次拦截“未在当次请求点名”的暴露服务/常驻服务/对公开仓库的推送；这类动作需用户当次明确说出。

## 9. 我没有核实的断言（别当事实）

- .NET 8 将在 2026-11 结束支持（凭记忆）；Android 15 对 `dataSync` 前台服务有每日时长限制（凭记忆）。
- `FILE_FLAG_FIRST_PIPE_INSTANCE` 在真 Windows 上拒绝第二个实例的行为：依据微软文档，**未在真机验证**（Wine 不实现）。
- 各厂商系统对后台服务、剪贴板访问、通知的具体限制。
- `development-plan.md` 里的周期估算。

## 10. 目录

```text
night-clip/
├── core/        Go 核心（proto identity store peer pairing clip xfer pipe cli update facade）、cmd/{cbcore-dll,cbridge,cbdist}、mobile、test/abi、internal/testnet
├── windows/     ClipBridge.Windows（WPF）与 ClipBridge.Windows.Tests（xUnit）
├── android/     Compose 应用（app/）
├── docs/        设计、规范、说明书、更新与发布、壳层指南、ADR、archive-v2
├── scripts/     version.sh release-local.sh backup-keys.sh install-dist-service.sh zipdir.py
├── deploy/      clipbridge-dist.service
├── .github/workflows/ci.yml
└── Makefile  VERSION_BASE  release.local.env.example
```
