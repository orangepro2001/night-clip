# 自动更新与本机分发

> 范围：Windows、Android、macOS 三个壳都能自动检查、下载、校验更新；安装由用户确认。更新包由开发机（`AI`，tony-ai-pc-1）构建，交给 **release-publish** 服务（`relhub`）签名和托管。
> **2026-11-20 起服务是 moon 上公开的 `https://release.moonl.cloud`**（Caddy 反代，没有路径前缀，签名公钥与原来相同），见 §7b。在此之前是 `AI` 上只在 tailnet 内可用的 `https://tony-ai-pc-1.tail9f7f1f.ts.net/release`，那台服务已经停用；下文里写作 `https://<本机>.ts.net/release` 的地方是当时的历史记录。
> 2026-10-28 起使用 release-publish，取代项目自带的 `cbdist`（`/moonpassword`）。迁移过程与已装设备如何过渡见 §7；把这次接入当作可复用方法看，见 [release-publish-integration.md](release-publish-integration.md)。
> GitHub 上的 `nightly` 发布是功能全部完成之后的事，见 §10。

## 1. 总览

```text
 开发机                                                    设备（同一 tailnet）
 ┌───────────────────────────┐   tailscale serve /release   ┌──────────────────────────┐
 │ scripts/release-local.sh  │   https://…/release           │ Go 核心 update.Manager    │
 │  预检 → 构建 → relhub      │──────────────────────────────▶│  watch 长轮询（秒级通知）  │
 │  publish（服务端签名）      │   ┌──────────────────────┐   │  验签 → 下载（Range 续传） │
 │ release-publish 127.0.0.1 │◀──┤ check / watch /      │   │  SHA-256 校验 → ready     │
 │  :18790（多项目共用）       │   │ releases/<n>/download│   └────────────┬─────────────┘
 └───────────────────────────┘   └──────────────────────┘                │ update.ready
                                                                壳：Android 系统安装器
                                                                    Windows 暂存→替换→重启
```

- **逻辑在 Go 核心**：检查、签名校验、下载、续传、SHA-256 校验、重试、状态机。两个壳只做「显示状态」和「调起安装」。
- **服务端是共用的**：release-publish 同时托管别的应用（例如 `go-nx`，以后还会有），MoonPassword 在其中是应用 `moonpassword`，平台 `android` / `windows`，渠道 `stable`。它只监听 `127.0.0.1:18790`，经 `tailscale serve --set-path /release` 暴露——这条路径属于 release-publish，**本项目不再新增任何 serve 路径**。
- **核心里不再有分发服务端与发布器**：签名、渠道、统计、撤回、下载页全部由 release-publish 提供。核心只带它的 Go SDK（`core/third_party/relhub`，原样复制，见该目录的 `README.md`）。

## 2. 信任模型

| 威胁 | 防护 |
| --- | --- |
| 服务器或传输被篡改，推送恶意包 | 清单带 **Ed25519 签名**，公钥编译进核心（`core/update/keys.go`）；应用 id、平台、渠道、包的大小与 SHA-256 都在签名之内，客户端逐项核对「这是我要的那份清单」 |
| 构建里没有可信公钥 | 客户端直接拒绝所有更新（`UPDATE_REJECTED`）。SDK 自己在「没有公钥」时只检查格式不验签，所以这一条是本项目额外加的 |
| 传输被降级/中间人 | 只接受 `https://`（`http://` 仅限回环地址，用于开发）；重定向不得跨主机，也不得改变协议（https→http 被拒绝） |
| 回放旧清单 | 只接受 `versionCode` 高于当前安装的清单；`minVersionCode` 在签名内；核心还记住服务器**见过的最高** `versionCode`（设置项 `update.high`），之后服务器给出更低的清单一律当作错误（换更新服务器时清零） |
| Android 上被换成别人签名的 APK | 系统安装器强制要求与已安装应用**同一签名密钥**；发布脚本钉住签名证书 SHA-256 |
| 下载损坏/被截断 | 写入 `.part`，校验大小与 SHA-256 通过后才改名；校验失败的文件被删除 |
| 服务器卡住或不停地发数据 | 60 s 没有数据就中止（保留 `.part`，SDK 自动续传）；响应体最多读到 `size+1` 字节，多出来的当作校验失败，不会写满磁盘 |
| 压缩包路径穿越/炸弹（Windows） | 解压时拒绝逃出目录的条目，限制条目数与总大小；失败时回滚 |

不在防护范围：release-publish 的签名私钥泄露（只在开发机，`0600`）；设备本身被攻破；tailnet 内任何设备都能读取（读取接口不需要 token，边界是 tailnet）。

**密钥，别混淆：**

| 密钥 | 用途 | 位置（本机，不在仓库） | 备份 |
| --- | --- | --- | --- |
| release-publish 签名密钥（Ed25519） | 给 `moonpassword` 以及其它应用的清单签名；公钥 `k4thZR+OVYSGAT/9j5F39csgGqTL0t3X6GYdNCmuRI8=`（key id `f5faa9f9858b789f`）编译进每个构建 | `~/.local/share/release-publish/keys/signing.ed25519` | `~/backups/release-publish-keys/`（release-publish 的 `docs/USAGE.md` §8） |
| Android 签名密钥（JKS） | 签 APK，Android 系统用它判断「能否覆盖安装」 | `~/.config/moonpassword/moonpassword-release.jks` + `android-signing.properties` | `scripts/backup-keys.sh` |
| 旧 `cbdist` 的 Ed25519 密钥 | 只用于 §7 的桥接版（给还信任它的旧安装推最后一次更新） | `~/.config/moonpassword/release-ed25519.key` | `scripts/backup-keys.sh`；旧源退役后可删 |

丢失 Android 密钥后，已安装的 App 只能卸载重装。丢失 release-publish 的签名密钥则无法再签出被现有安装接受的更新——所以它的备份要另存一份到本机之外。轮换签名密钥必须先发一个**同时信任新旧公钥**的版本（`trustedKeysB64` 是个列表，就是为此），详细步骤在 release-publish 的 `docs/USAGE.md` §8，实测过。

`scripts/backup-keys.sh` 把 JKS、`android-signing.properties` 和旧 Ed25519 密钥打包到 `~/backups/moonpassword-keys/`（目录 700、文件 600、写入后逐字节校验、附 `SHA256SUMS` 与 `RESTORE.txt`）。这是同一块盘上的明文备份，只防误删，请另存一份到本机之外。

## 3. 协议（release-publish）

完整协议在 release-publish 的 `docs/API.md`。更新用到三个调用，`{update_url}` 是构建期写入的基地址（`https://<本机>.ts.net/release`）：

| 调用 | 说明 |
| --- | --- |
| `GET {update_url}/v1/apps/moonpassword/check?platform=&channel=stable&versionCode=<已装版本>` | 返回渠道当前提供的**已签名清单**。服务自己回答的 404 `not_found`（带 JSON 错误信封）= 应用未注册或该平台还没发布；没有信封的 404（代理没挂 `/release`、地址写错）是错误，界面上会显示出来，不当作「已是最新」。客户端用清单本地重新判断是否更新，不信响应里没签名的 `updateAvailable`/`mandatory`。服务端按「已装版本」统计每次检查（`relhub stats`） |
| `GET {update_url}/v1/apps/moonpassword/watch?platform=&channel=&versionCode=<已知最高>&wait=S` | **实时通知**。一旦渠道出现更高的 `versionCode` 就立刻返回同样的结果（发布后约 1 秒内）；`wait` 秒（最长 50）内没有则 `204`，客户端立即再问。每次回答后连接即结束，不依赖长连接，能穿过 `tailscale serve`。应用未注册返回 404，客户端退回轮询 |
| `GET {update_url}/v1/apps/moonpassword/<platform>/releases/<versionCode>/download` | 版本固定的包本体：支持 `Range` / `If-Range`（带清单里的 SHA-256 作 ETag，续传中服务端换了文件就从头来，不会拼出两个版本的混合体），响应带 `X-Content-SHA256`；永不改变，所以可缓存 |

给人用的入口：下载页 `https://<本机>.ts.net/release/`，固定地址 `…/release/v1/apps/moonpassword/<platform>/latest/download`（永远指向当前版本）。

清单（节选；`keyId`/`signature` 之外的字段都被签名覆盖，`notes` 经其 SHA-256）：

```json
{ "app": "moonpassword", "platform": "android", "channel": "stable",
  "versionCode": 81, "versionName": "0.1.81",
  "file": "moonpassword-0.1.81-android.apk", "size": 49468241, "sha256": "<64 位小写十六进制>",
  "minVersionCode": 0, "notes": "…", "publishedAt": "2026-10-28T03:00:00Z",
  "keyId": "f5faa9f9858b789f", "signature": "<base64 Ed25519>" }
```

- 强制更新：发布时加 `--force`，渠道的 `minVersionCode` 提升为该版本；低于它的客户端状态里 `mandatory=true`，UI 以醒目方式提示（核心不阻断剪贴板功能）。
- 包文件名由 release-publish 生成（`moonpassword-<版本名>-<平台><扩展名>`）。两个壳只依赖扩展名（Android 要求 `.apk`）和核心给出的 `ready_path`，不依赖文件名格式。

## 4. 核心行为

- `update.Manager` 在 `Init` 之后启动：首次完整检查延迟约 20 s，之后**挂在 `watch` 上**，服务器发布新版本约 1 秒内就会收到 `update.available`（并按设置自动下载）。应用未注册（`watch` 返回 404）时退回每 6 h 轮询。失败时指数退避（5 s 起，翻倍，最长 5 min）。后台检查失败**不**发事件（离线时不打扰用户），只记入状态。
- 检查时把**已装的 versionCode** 发给服务，所以 `relhub stats -app moonpassword` 能看到每个版本还有多少设备在用——这是判断「旧源/旧版本能不能下线」的依据。请求的 `User-Agent` 是 `moonpassword/<版本名>`，出现在 release-publish 的访问日志里。
- 实时通知只在**核心进程运行时**有效：Windows 是程序开着的时候；Android 是前台服务（Start connected mode）或界面开着的时候。
- 状态：`idle → checking → up_to_date | available | error`；`available → downloading → ready | error`。`ready` 表示磁盘上有一个**已校验**的安装包。
- 自动下载（默认开）：发现新版本后立即下载；关闭后停在 `available`，等 `update.download`。
- 已存在且校验通过的包不会重复下载；`up_to_date` 或装好新版本后，旧包与 `.part` 会被清理。
- 下载目录：`<data_dir>/updates/`。下载中途网络错误或服务端 5xx 时，SDK 从 `.part` 断点自动重试最多 3 次（1 s、2 s、4 s）；仍失败则进入 `error`，用户可再点下载。
- 事件与方法见 [api-spec.md](api-spec.md) §7.3、§7.4。

## 5. 壳

**Android**（`UpdateState.kt`、`ApkInstaller.kt`、`MainActivity.kt`、`SyncForegroundService.kt`）
- 主界面「Updates」卡片：状态、进度、按钮（检查 / 下载 / 安装）、自动下载开关。界面轮询 `update.status`，不读事件队列（事件队列只属于前台服务的事件线程）。
- 前台服务收到 `update.ready` 时发通知，点击回到应用。
- 安装：`ACTION_VIEW` + `FileProvider`（只共享 `files/moonpassword/updates/`）。首次需要用户在系统设置里允许「安装未知应用」，应用会跳转到对应页面。

**Windows**（`Update/*.cs`、`MainWindow.xaml(.cs)`、`App.xaml.cs`）
- 窗口里的更新栏与 Android 相同。
- 安装流程：`ready` → 解压到 `%LOCALAPPDATA%\MoonPassword\staging\<guid>\` → 启动**暂存目录里的新 `MoonPassword.exe --apply-update --pid N --target <安装目录>`** → 旧进程退出 → 新进程等待旧进程结束，把文件逐个移到 `.old` 后复制过去，**任何一步失败都回滚**，成功后删除 `.old` 并启动安装目录里的 exe。
- 只覆盖更新包里有的文件，不删除其它文件。安装目录必须当前用户可写。
- 更新包是 `dotnet publish`（framework-dependent，win-x64）的输出，包含 `moonpassword_core.dll` 和 `moonpassword-ssh-agent.exe`，打成扁平 zip。
- Windows 的 SSH Agent 以独立进程运行。安装更新前确认交接，安装后从托盘重新连接。更新期间仍可通过原管道使用已加载的钥匙。首次升级到独立 Agent 版本后需要解锁一次。

壳不关心更新服务是谁：它们只把构建期的 `CB_UPDATE_URL` 当作 `update_url` 交给核心。

## 6. 发布流程（开发机）

逐步操作说明（含桥接版、真机验证、常见问题）见 [release-runbook.md](release-runbook.md)；下面是机制与检查项。

一次性准备（已在本机完成，记录在这里方便换机器）：

```sh
# 1) release-publish 在跑，且 /release 已在 tailscale serve 上（属于 release-publish 项目，见它的 docs/USAGE.md §1）
podman ps --filter name=release-publish      # 2026-10-28 起是容器；以 release-publish 项目的文档为准
curl -fsS https://<本机>.ts.net/release/healthz

# 2) 应用注册 + 旧版本导入（2026-10-28 完成；重复运行是安全的）
relhub app create -id moonpassword -name MoonPassword
relhub import moonpassword -from ~/.local/share/moonpassword/releases

# 3) 服务的签名公钥写进 core/update/keys.go 的 trustedKeysB64（relhub keys 可查）

# 4) Android 签名密钥与 ~/.config/moonpassword/android-signing.properties（storeFile/storePassword/keyAlias/keyPassword）

# 5) 分发地址：2026-11-20 起是公开地址，Makefile 默认就是 https://release.moonl.cloud；
#    release.local.env 里只放上传用的 RELHUB_URL 和只能发布 moonpassword 的 RELHUB_TOKEN（见 §7b）
cp release.local.env.example release.local.env   # 填 RELHUB_TOKEN，chmod 600
```

每次发布：

```sh
git commit …                                        # 版本号 = 提交数，所以先提交
scripts/release-local.sh --check                    # 只做预检（可随时跑）
scripts/release-local.sh --notes "更新说明"           # 预检 → 构建两个平台 → relhub publish → 按 App 的方式验证
                                                    # --platform android|windows，--force 强制更新
```

版本号：`versionCode` = `git rev-list --count HEAD`（单调递增），`versionName` = `VERSION_BASE`.`versionCode`。工作区不干净时脚本拒绝发布（`--allow-dirty` 可覆盖）。release-publish 对每个平台要求 `versionCode` 严格递增，**发布不可修改，号码一旦用掉就不会回来**（撤回 `relhub yank` 也不释放号码）。

脚本在**构建之前**先把不可逆的错误挡掉，在**发布之后**按装好的 App 的视角核对：

| 阶段 | 检查 | 失败的后果（为什么要挡） |
| --- | --- | --- |
| 预检 | 能连上 `CB_UPDATE_URL/v1/info` | 构建会把一个连不上的地址烤进所有设备 |
| 预检 | 服务**正在用于签名的公钥**在 `core/update/keys.go` 的受信任列表里 | 否则这个构建拒绝之后所有更新，只能手动重装——最重的一种事故 |
| 预检 | 每个要发布的平台，服务里没有 ≥ 当前 `versionCode` 的版本（含已撤回的） | 省得构建 5 分钟后才在上传时发现号码被占 |
| Android | 签名证书 SHA-256 与上次钉住的一致（`~/.config/moonpassword/android-signer.sha256`） | 换了签名的 APK 装不上 |
| Android | `aapt2 dump badging` 里的 `versionCode` 等于本次发布的号码 | release-publish 要求二者一致；不一致会提供一个系统安装器永远拒绝的更新 |
| 发布后 | 通过**公开地址**、用**编译进 App 的公钥**验签并完整下载（`relhub download -keys`），与本地构建产物逐字节比较 | 这就是装好的 App 将要走的路径 |

某个平台发布了、另一个失败（例如 Windows 打包出错）：修好后 `scripts/release-local.sh --platform windows --no-build`（已发布的平台不会再被检查）。

撤回与回滚（release-publish 提供）：`relhub yank`（先把渠道 `promote` 到别的版本）、`relhub channel promote`（可指回旧版本）。已升级的设备不会被降级；要让它们换到修复版，必须发一个更高 `versionCode` 的新版本，必要时加 `--force`。

### 每个阶段性功能都发版

约定：一个阶段性功能完成后依次 `git commit` → `git push` → `scripts/release-local.sh --notes "…"`。版本号是提交数，设备上已安装的应用会在约 1 秒内收到新版本并自动下载，Android 通知栏 / Windows 任务栏闪烁提示，点击即可安装。

## 7c. 2026-11：改名 MoonPassword

- 应用 ID 从 `clipbridge` 改为 `moonpassword`（`core/update/manifest.go` 的 `AppID`），release-publish 上是另一个应用。0.1.113 及以前的版本以 ClipBridge（应用 ID `clipbridge`）发布，它们不会自动升级到 MoonPassword：安卓包名也变了（`cloud.moonl.password`），各设备需要手动安装一次新应用，旧数据用迁移向导导入。
- 版本号从 `VERSION_BASE` = `1.0` 起（versionCode 仍是提交数，单调递增）。
- 本机签名材料的目录改为 `~/.config/moonpassword/`；改名前配好的机器继续用 `~/.config/clipbridge/`（构建脚本两处都找，签名密钥不变）。
- 首次发布前要在 moon 上用管理员身份建应用并签发只能发布它的令牌（命令见 `release.local.env.example`），令牌只放进本机的 `release.local.env`。

## 7b. 2026-11-20：迁到 release.moonl.cloud

- **服务**：moon（srv2178025）上的 `release-publish.service`，Caddy 在 `https://release.moonl.cloud` 根路径反代，数据在 `/var/lib/release-publish`，配置 `/etc/release-publish/env`。签名公钥与 `AI` 上的旧服务相同（key id `f5faa9f9858b789f`），所以 `core/update/keys.go` 不用改。这台服务上的应用和版本是它自己的，不与别处镜像。
- **注册与令牌**（在 moon 上做一次，已于 2026-11-20 完成）：

  ```sh
  relhub app create -id moonpassword -name MoonPassword -desc "…" -data /var/lib/release-publish
  relhub token create -name moonpassword-release -app moonpassword -data /var/lib/release-publish   # 只能发布 moonpassword
  ```

  令牌只写进构建机（`AI`）上 git 忽略的 `release.local.env`（`RELHUB_URL=https://release.moonl.cloud`、`RELHUB_TOKEN=…`，权限 600），不进仓库、不放 CI。
- **发布**：仍在 `AI` 上 `scripts/release-local.sh --notes "…"`。脚本把 `RELHUB_URL` 默认设成更新地址，远端服务要求有 `RELHUB_TOKEN`，并用编译进 App 的公钥（`RELHUB_KEYS`）核对读回的清单；最后照旧用真实核心客户端从公开地址下载比对。
- **已装设备**：≤ 0.1.110 的安装把 `AI` 的 tailnet 地址编译在里面，而那台服务和 `AI` 上的 tailscale 都已停用，所以它们收不到任何更新，也**不做桥接**（用户 2026-11-20 的决定）。每台设备手动装一次新版即可：

  | 平台 | 下载 |
  | --- | --- |
  | Windows | `https://release.moonl.cloud/v1/apps/moonpassword/windows/latest/download`，解压覆盖原安装目录（或解压到新目录后删掉旧的） |
  | Android | `https://release.moonl.cloud/v1/apps/moonpassword/android/latest/download`，覆盖安装（签名不变，数据保留） |
  | macOS | `https://release.moonl.cloud/v1/apps/moonpassword/macos/latest/download` |

  装好之后它们只问 `release.moonl.cloud`，以后自动更新。下载页：`https://release.moonl.cloud/`。
- **在 Windows 开发机上构建和发布**（0.1.113 起；不再用 `AI` 构建）。一次性准备：
  - `scoop install make mingw-winlibs zig`；`go install golang.org/x/mobile/cmd/gomobile@<go.mod 里的版本>`（gobind 同）。
  - Android SDK 里装 NDK `28.2.13676358`（gomobile）和 `27.0.12077973`（AGP 8.7.3 只用它剥离 `.so` 的调试符号，否则 APK 大约 68 MB）；`android/local.properties` 写 `sdk.dir=C:/Users/<你>/AppData/Local/Android/Sdk`。
  - `rcodesign` 0.29.0 的 Windows 版放进 PATH；`relhub.exe` 从 release-publish 源码 `go build ./cmd/relhub`。
  - `%USERPROFILE%\.config\moonpassword\` 放 Android 签名密钥、`android-signing.properties`（`storeFile` 用本机路径）和 `android-signer.sha256`；仓库根目录的 `release.local.env` 加 `RELHUB_BIN=…/relhub.exe`、`ANDROID_HOME=…`。两处都只允许本人读取。
  - 构建：`make dist-windows dist-android dist-macos MINGW_CC=gcc DOTNET=dotnet`，`JAVA_HOME` 指向 JDK，`ANDROID_NDK_HOME` 指向 28.2；然后 `scripts/release-local.sh --no-build --notes "…"`。
  - Windows 特有的坑（脚本已处理）：Go 交叉链接 macOS 时改写 UUID 的重命名在 Windows 上失败，`macos/build-app.sh` 改用 `-B none` 加一个去掉 `-no_uuid` 的 zig 包装；Windows 没有执行位，没有 `zip` 时由 `scripts/zipapp.py` 给 Mach-O 文件写 0755；`apksigner.bat`/`aapt2.exe`、Go 不认 `/c/...` 路径也都已兼容。
- **egg**（`https://release.521985.xyz`）是另一台独立的公开分发服务器，上面停在 0.1.110；不要把它当作这个项目的后备地址。

## 7. 从旧分发服务（cbdist）迁移

**问题**：已装的 App（≤ 0.1.79）把旧公钥和旧地址 `…/moonpassword` 编译在里面，永远不会去问 `…/release`，也不认 release-publish 的签名。新服务建好之后，这些设备**不会自己过去**。

**做法：桥接版。** 发一个新版本，它的更新源已经是 release-publish；再把**同一个安装包**用旧工具、旧密钥推到旧源，让旧安装照老路径更新到它。之后设备上只剩新机制。

1. 提交代码，`scripts/release-local.sh --notes "…"` 发布到 release-publish（此时没有任何设备在读它，可以用 §6 的公开地址验证）。
2. 把同一份包推到旧源（旧 `cbdist` 二进制仍装在 `~/.local/bin/cbdist`，旧服务 `moonpassword-dist` 还在跑；仓库里已经没有它的源码，最后一个带它的提交是 `29326f3`）：

   ```sh
   cbdist=~/.local/bin/cbdist; feed=~/.local/share/moonpassword/releases; key=~/.config/moonpassword/release-ed25519.key
   # 包取 release-publish 里的那份，保证两边字节相同
   relhub download -app moonpassword -platform android -out /tmp/bridge.apk
   $cbdist publish -dir $feed -key $key -platform android -file /tmp/bridge.apk -code <versionCode> -name <versionName> -notes "<说明>"
   # windows 同理
   ```
3. 旧设备按老路径发现、下载、验签（旧密钥）、安装；新版本此后只问 `…/release`。
4. 确认迁移完成：`relhub stats -app moonpassword` 里出现来自新版本的检查（`checks from stable/<新版本号>`；统计按版本号计数，不区分设备）。**旧的 `cbdist` 不记访问日志**，看不到还有谁在用它，所以只能反过来：每台设备（本项目就是自己的 Windows 和 Android）在「关于」里确认版本已经是桥接版或更新的版本，再退役。

**风险与防线**：桥接版如果更新链路有错（公钥、地址、应用 id、平台名、下载），装了它的设备就再也收不到更新，只能手动重装。所以在把桥接版推到旧源之前，必须已经验证过——这次做了：核心单测（假服务端）、对真实 `relhub` 二进制的集成测试（`TestAgainstRealRelhub`）、用真实核心代码 + 编译进去的公钥通过公开 HTTPS 地址对生产服务做的只读探测（检查并完整下载），以及发布脚本里的公钥/号码预检。

**退役旧源**（所有设备都确认已经是新版本之后；每一步都影响线上，需要明确决定后手动执行。摘除 serve 路径的写法依据 `tailscale serve --set-path … off`，**在本机还没执行过**，先看 `tailscale serve --help`；不带路径的 `off` 和 `reset` 会清掉别的服务，不要用）：

```sh
systemctl --user disable --now moonpassword-dist.service
tailscale serve --https=443 --set-path /moonpassword off     # 只摘这一条；先 tailscale serve status，之后再核对其余路径都在
rm ~/.config/systemd/user/moonpassword-dist.service ~/.local/bin/cbdist
# 之后才可删除 ~/.local/share/moonpassword/releases 和旧 Ed25519 密钥（先确认 release-publish 里已有全部历史版本）
```

## 8. 开发构建

不带 `CB_VERSION_CODE` 的构建（`make apk`、IDE、`dotnet build`）是**开发构建**：`app_version_code = 0`，自更新关闭，界面显示 “Development build: updates are off”。Debug APK 用 debug 密钥签名，本来也无法覆盖 release 签名的安装。

## 9. 测试

| 层 | 覆盖 |
| --- | --- |
| `core/update`（客户端、管理器） | 对**假 release-publish**（`core/update/updatetest`）：检查（含向服务报告已装版本、User-Agent）、未注册应用、签名被篡改/不受信任/没有公钥、别的应用或平台的合法清单、超大包、非 JSON/5xx/不可达；下载：续传（`Range`+`If-Range`）、服务器忽略 Range、416、`.part` 过大、内容被换、过长/无尽的响应体、截断、停滞后恢复、撤回的包、跨主机与降级重定向；`watch`：空、已知版本、挂起时收到发布、未注册；管理器状态机、后台循环、退避、`Close` 取消下载、`update.high` 回放防护 |
| `core/update`（`TestAgainstRealRelhub`） | 对**真实的 `relhub` 二进制**（随机回环端口、临时数据目录，不碰线上服务）：注册应用、CLI 发布、检查/续传下载/验签、挂起的 watch 被发布唤醒、`-force` 强制更新、管理器自动下载、`relhub stats` 统计到已装版本。没有 `relhub` 时（CI）自动跳过；`RELHUB_BIN` 可指定二进制 |
| `core/facade`（`update_test.go`） | 经门面的检查→下载→事件、手动模式、设置持久化、URL 校验、不受信任的签名 |
| Windows（`UpdateApplierTests`、`UpdateModelTests`） | 解压（路径穿越、缺 exe、坏 zip）、替换与回滚、等待旧进程、清理、参数解析、状态→界面 |
| Android（`UpdateStateTest`） | 状态解析与按钮/文案 |

**未在真机验证**：Android 系统安装器与「安装未知应用」跳转、Windows 上真实的文件占用与杀毒软件干扰；桥接版在真实旧设备上的一次完整更新（见 §7 的状态与 handoff 文档）。

## 10. 之后：GitHub `nightly` 发布

全部功能完成后，CI 在每次推送到 `main` 时更新一个滚动的 `nightly` 预发布（GitHub Release），附带 APK 和 Windows zip。注意：
- GitHub 上的 APK 需要 CI 用**同一把** Android 密钥签名（作为仓库密钥提供），否则无法覆盖本机分发的安装。
- 清单现在由 release-publish 的**服务端私钥**签名，这把私钥不能给 CI。所以 GitHub 上的包只是**对外下载渠道**，应用内更新仍然只走本机 release-publish；让应用也能从 GitHub 更新需要另一条信任链，是单独的决定。
- 版本号继续用提交数，CI 需 `fetch-depth: 0`。
