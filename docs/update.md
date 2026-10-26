# 自动更新与本机分发

> 范围：Windows 与 Android 两个壳都能自动检查、下载、校验更新；安装由用户确认。更新包由**本机**（开发机）构建并分发，不依赖任何第三方服务。
> GitHub 上的 `nightly` 发布是功能全部完成之后的事，见 §9。

## 1. 总览

```text
 开发机                                              设备（同一 tailnet）
 ┌──────────────────────────┐   tailscale serve     ┌──────────────────────────┐
 │ scripts/release-local.sh │   https://…/clipbridge │ Go 核心 update.Manager    │
 │  构建 → cbdist publish   │──────────────────────▶│  每 6 h 检查 → 校验签名    │
 │  Ed25519 签名清单         │  ┌────────────────┐   │  下载（Range 续传）        │
 │ cbdist serve 127.0.0.1   │◀─┤ GET latest /   │   │  SHA-256 校验 → ready     │
 └──────────────────────────┘  │ files/<name>   │   └────────────┬─────────────┘
                               └────────────────┘                │ update.ready
                                                       壳：Android 系统安装器
                                                           Windows 暂存→替换→重启
```

- **逻辑在 Go 核心**：检查、签名校验、下载、续传、SHA-256 校验、重试、状态机。两个壳只做「显示状态」和「调起安装」。
- 分发服务器是只读静态服务，**只监听 127.0.0.1**，经 `tailscale serve` 暴露在 tailnet 内（见 §6）。

## 2. 信任模型

| 威胁 | 防护 |
| --- | --- |
| 服务器或传输被篡改，推送恶意包 | 清单带 **Ed25519 签名**，公钥编译进核心（`core/update/keys.go`）；包的大小与 SHA-256 在签名之内 |
| 传输被降级/中间人 | 只接受 `https://`（`http://` 仅限回环地址，用于开发）；重定向不得跨主机，也不得改变协议（https→http 被拒绝） |
| 回放旧清单 | 只接受 `versionCode` 高于当前安装的清单；`minVersionCode` 在签名内；核心还记住服务器**见过的最高** `versionCode`（设置项 `update.high`），之后服务器给出更低的清单一律当作错误（换更新服务器时清零） |
| Android 上被换成别人签名的 APK | 系统安装器强制要求与已安装应用**同一签名密钥**；发布脚本钉住签名证书 SHA-256 |
| 下载损坏/被截断 | 写入 `.part`，校验大小与 SHA-256 通过后才改名；校验失败的文件被删除 |
| 压缩包路径穿越/炸弹（Windows） | 解压时拒绝逃出目录的条目，限制条目数与总大小；失败时回滚 |

不在防护范围：发布私钥泄露（私钥只在开发机，`0600`，请备份）；设备本身被攻破。

**两把密钥，别混淆：**

| 密钥 | 用途 | 位置（本机，不在仓库） |
| --- | --- | --- |
| Ed25519 发布密钥 | 签清单，两个平台共用 | `~/.config/clipbridge/release-ed25519.key` |
| Android 签名密钥（JKS） | 签 APK，Android 系统用它判断「能否覆盖安装」 | `~/.config/clipbridge/clipbridge-release.jks` + `android-signing.properties` |

丢失 Android 密钥后，已安装的 App 只能卸载重装；丢失发布密钥则需要发一个新信任密钥的版本（见 `keys.go` 的轮换说明）。

**备份**：`scripts/backup-keys.sh` 把三个文件打包到 `~/backups/clipbridge-keys/`（目录 700、文件 600、写入后逐字节校验、附 `SHA256SUMS` 与 `RESTORE.txt`）。这是同一块盘上的明文备份，只防误删；请另存一份到本机之外（密码管理器附件、加密 U 盘等）。更换或新增密钥后重新运行。

## 3. 清单与端点

`GET {update_url}/v1/{platform}/latest`（`platform` = `android` | `windows`）：

```json
{
  "platform": "android",
  "versionCode": 12,
  "versionName": "0.1.12",
  "file": "clipbridge-android-0.1.12-12.apk",
  "size": 15800000,
  "sha256": "<64 位小写十六进制>",
  "minVersionCode": 0,
  "notes": "…",
  "publishedAt": "2026-10-26T10:00:00Z",
  "signature": "<base64 Ed25519>"
}
```

- 404 = 该平台尚未发布。
- `GET {update_url}/v1/{platform}/files/{file}`：包本体，支持 `Range`；响应带 `X-Content-SHA256`。文件名内含版本号，内容永不改变。
- `GET {update_url}/v1/{platform}/download`：当前版本的固定地址（给人用，响应带 `Content-Disposition`）。
- `GET {update_url}/v1/{platform}/watch?code=N&wait=S`：**实时通知**。客户端已知版本为 N 时，一旦服务器上出现更高的 `versionCode` 就立刻返回清单（发布后约 1 秒内）；`wait` 秒（最长 50）内没有新版本则返回 `204`，客户端立即再问。每次回答后连接即结束，不依赖长连接，能穿过 `tailscale serve`。
- `GET {update_url}/healthz`、`GET {update_url}/`（给人看的下载页）。
- 签名内容（逐行 `key=value`，固定顺序）：`clipbridge-release-v1`、`platform`、`versionCode`、`versionName`、`file`、`size`、`sha256`、`minVersionCode`、`publishedAt`、`notesSha256`（notes 的 SHA-256）。
- 强制更新：发布时加 `--force`，`minVersionCode` 提升为该版本；低于它的客户端状态里 `mandatory=true`，UI 以醒目方式提示（核心不阻断剪贴板功能）。

## 4. 核心行为

- `update.Manager` 在 `Init` 之后启动：首次完整检查延迟约 20 s，之后**挂在 `watch` 上**，服务器发布新版本约 1 秒内就会收到 `update.available`（并按设置自动下载）。服务器不支持 `watch`（旧版 cbdist）时退回每 6 h 轮询。失败时指数退避（5 s 起，翻倍，最长 5 min）。后台检查失败**不**发事件（离线时不打扰用户），只记入状态。
- 实时通知只在**核心进程运行时**有效：Windows 是程序开着的时候；Android 是前台服务（Start connected mode）或界面开着的时候。
- 状态：`idle → checking → up_to_date | available | error`；`available → downloading → ready | error`。`ready` 表示磁盘上有一个**已校验**的安装包。
- 自动下载（默认开）：发现新版本后立即下载；关闭后停在 `available`，等 `update.download`。
- 已存在且校验通过的包不会重复下载；`up_to_date` 或装好新版本后，旧包与 `.part` 会被清理。
- 下载目录：`<data_dir>/updates/`；卡住 60 s 无数据则中止，保留 `.part` 供续传。
- 事件与方法见 [api-spec.md](api-spec.md) §7.3、§7.4。

## 5. 壳

**Android**（`UpdateState.kt`、`ApkInstaller.kt`、`MainActivity.kt`、`SyncForegroundService.kt`）
- 主界面「Updates」卡片：状态、进度、按钮（检查 / 下载 / 安装）、自动下载开关。界面轮询 `update.status`，不读事件队列（事件队列只属于前台服务的事件线程）。
- 前台服务收到 `update.ready` 时发通知，点击回到应用。
- 安装：`ACTION_VIEW` + `FileProvider`（只共享 `files/clipbridge/updates/`）。首次需要用户在系统设置里允许「安装未知应用」，应用会跳转到对应页面。

**Windows**（`Update/*.cs`、`MainWindow.xaml(.cs)`、`App.xaml.cs`）
- 窗口里的更新栏与 Android 相同。
- 安装流程：`ready` → 解压到 `%LOCALAPPDATA%\ClipBridge\staging\<guid>\` → 启动**暂存目录里的新 `ClipBridge.exe --apply-update --pid N --target <安装目录>`** → 旧进程退出 → 新进程等待旧进程结束，把文件逐个移到 `.old` 后复制过去，**任何一步失败都回滚**，成功后删除 `.old` 并启动安装目录里的 exe。
- 只覆盖更新包里有的文件，不删除其它文件。安装目录必须当前用户可写。
- 更新包是 `dotnet publish`（framework-dependent，win-x64）的输出外加 `clipbridge_core.dll`，扁平 zip。

## 6. 发布流程（开发机）

一次性准备：

```sh
# 1) 发布签名密钥（已完成时跳过）；公钥写进 core/update/keys.go
core/dist/host/cbdist keygen -key ~/.config/clipbridge/release-ed25519.key

# 2) Android 签名密钥与 ~/.config/clipbridge/android-signing.properties（storeFile/storePassword/keyAlias/keyPassword）

# 3) 分发地址（不进仓库，因为它包含 tailnet 名）
cp release.local.env.example release.local.env   # 编辑 CB_UPDATE_URL

# 4) 分发服务（只监听 127.0.0.1:18780）
scripts/install-dist-service.sh
```

每次发布：

```sh
git commit …                                   # 版本号 = 提交数，所以先提交
scripts/release-local.sh --notes "更新说明"      # 构建两个平台并发布；--platform android|windows，--force 强制更新
```

版本号：`versionCode` = `git rev-list --count HEAD`（单调递增），`versionName` = `VERSION_BASE`.`versionCode`。工作区不干净时脚本拒绝发布（`--allow-dirty` 可覆盖）。同一个 `versionCode` 不能重复发布。

### 发布到 tailnet（需要显式执行，脚本不会做）

本机的 `tailscale serve` 已有若干路径，更新分发要**新增一条**，不改动已有的：

```sh
tailscale serve --bg --set-path /clipbridge http://127.0.0.1:18780
```

`/clipbridge` 前缀由 tailscale 去掉后再转发（已在本机验证，`cbdist serve` 不需要 `-prefix`）。验证：`curl https://<本机>.ts.net/clipbridge/healthz`。

给人用的下载入口：`/clipbridge/`（页面）、`/clipbridge/v1/android/download`、`/clipbridge/v1/windows/download`（固定地址，永远指向当前版本）。

### 每个阶段性功能都发版

约定：一个阶段性功能完成后依次 `git commit` → `git push` → `scripts/release-local.sh --notes "…"`。版本号是提交数，设备上已安装的应用会在约 1 秒内收到新版本并自动下载，Android 通知栏 / Windows 任务栏闪烁提示，点击即可安装体验。

## 7. 开发构建

不带 `CB_VERSION_CODE` 的构建（`make apk`、IDE、`dotnet build`）是**开发构建**：`app_version_code = 0`，自更新关闭，界面显示 “Development build: updates are off”。Debug APK 用 debug 密钥签名，本来也无法覆盖 release 签名的安装。

## 8. 测试

| 层 | 覆盖 |
| --- | --- |
| `core/update`（55 个） | 清单签名/篡改/形状校验、发布规则、服务器路由与 Range 与路径穿越、续传、服务器忽略 Range、过长/错误内容、停滞与恢复、跨主机重定向、管理器状态机、后台循环、`Close` 取消下载 |
| `core/facade`（`update_test.go`） | 经门面的检查→下载→事件、手动模式、设置持久化、URL 校验、不受信任的签名 |
| Windows（`UpdateApplierTests`、`UpdateModelTests`） | 解压（路径穿越、缺 exe、坏 zip）、替换与回滚、等待旧进程、清理、参数解析、状态→界面 |
| Android（`UpdateStateTest`） | 状态解析与按钮/文案 |

**未在真机验证**：Android 系统安装器与「安装未知应用」跳转、Windows 上真实的文件占用与杀毒软件干扰、`tailscale serve` 路径前缀行为（见 §6）。

## 9. 之后：GitHub `nightly` 发布

全部功能完成后，CI 在每次推送到 `main` 时更新一个滚动的 `nightly` 预发布（GitHub Release），附带 APK、Windows zip 和对应清单。注意：
- GitHub 上的包需要 CI 用**同一把** Android 密钥签名（作为仓库密钥提供），否则无法覆盖本机分发的安装；Ed25519 发布密钥同理。
- 版本号继续用提交数，CI 需 `fetch-depth: 0`。
- 应用里的更新源仍是本机分发地址；GitHub 只是对外下载渠道，是否让应用也能从 GitHub 更新是单独的决定。
