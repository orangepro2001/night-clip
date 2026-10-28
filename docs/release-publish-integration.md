# 接入本机 release-publish 服务：可复用的经验与方法

> 来源：2026-10-28，night-clip（ClipBridge）把自己写的更新分发服务 `cbdist` 换成本机共用的 **release-publish**（`relhub`）。这份文档把那次迁移里的**决定、代价、坑和检查清单**抽出来，写给下一个要接入的应用（或下一次换更新服务的自己）。ClipBridge 的落地细节在 [update.md](update.md)；这次任务的交接与现状在 [handoff-2026-10-28-release-publish.md](handoff-2026-10-28-release-publish.md)。
> release-publish 自己的文档（权威来源，不在这里重复）：`~/dev/release-publish/docs/` 下的 `USAGE.md`（运维）、`SDK.md`（Go SDK）、`CSHARP.md`（.NET SDK）、`API.md`（协议与签名字节）。

## 0. 一页纸结论

1. **接入 = 注册应用 + 客户端换传输层 + 发布脚本换成 `relhub publish`。** 服务端不用写，签名、渠道、统计、撤回、下载页都是现成的。核心里自己维护的生产代码因此净减少约 690 行（分发服务器、发布器、清单签名、`cbdist` 命令），SDK 的约 1,500 行以 `third_party` 的形式原样复制进来、不手改。
2. **SDK 是传输层，不是安全策略。** 它在「没有公钥」时只验格式不验签，下载不检测卡死、不限制响应体长度，对任意 http(s) 地址和重定向不设防。这些是应用自己的责任，补法见 §3，**每一条都有测试**。
3. **最重的一课：已装的设备不会自己跟着换服务。** 旧版本把旧公钥和旧地址编译在里面。必须发一个「桥接版」——更新源已经是新服务，同时用旧工具、旧密钥推到旧源——设备才会过来。桥接版的更新链路有任何错误，设备就永远失联。所以要**分层验证后再桥接**（§5、§6）。
4. **发布是不可逆的**（号码用掉就不回来），所以脚本把不可逆的错误放在**构建之前**挡掉，并在发布之后用**装好的 App 的视角**验证（§4）。
5. **测试分三层**：假服务端（快、能注入故障）→ 真实 `relhub` 二进制（协议有没有跑偏）→ 对线上服务的只读探测（密钥、地址、应用 id 真的对得上）。缺一层就会漏一类事故（§6）。
6. 服务是**多项目共用**的：只动自己的应用，不新增 `tailscale serve` 路径，不碰别人的数据（§1）。

## 1. 先弄清楚：服务、你的应用、谁拥有什么

本机上的服务（以 `~/dev/release-publish/docs/USAGE.md` §1 为准，这里只列接入需要的）：

| 项目 | 值 |
| --- | --- |
| 对外地址（仅 tailnet） | `https://tony-ai-pc-1.tail9f7f1f.ts.net/release`；后端 `127.0.0.1:18790`；tailscale 会剥掉 `/release` 前缀，服务同时兼容有无前缀 |
| 运行 | systemd 用户服务 `release-publish.service`（linger）；程序 `~/.local/bin/relhub` |
| 签名公钥（要编译进应用） | `k4thZR+OVYSGAT/9j5F39csgGqTL0t3X6GYdNCmuRI8=`（`relhub keys` 查看） |
| 读 / 写 | 读取（检查、下载）不需要 token，边界是 tailnet；写入需要 Bearer token。本机 CLI 自动读管理员 token（`~/.local/share/release-publish/admin.token`）；CI 或别的机器用**按应用限定**的 token：`relhub token create -name ci-<app> -app <app>` |
| 共用 | 同一个服务托管多个应用。接入时 `relhub list` 已经能看到别的项目（例如 `go-nx`）。**只创建、导入、发布、撤回你自己的应用**；服务的 `serve` 路径属于 release-publish 项目，你的应用不新增路径 |

接入前你要为自己的应用定下五个标识（ClipBridge 的选择和理由放在右边，新应用照此思路定）：

| 标识 | 规则 | ClipBridge 的选择与理由 |
| --- | --- | --- |
| app id | `^[a-z][a-z0-9-]{1,31}$`，一旦发布就不改 | `clipbridge`（与工作名一致） |
| platform | 自由命名 `^[a-z0-9][a-z0-9_-]{0,23}$`；服务文档约定用 `android`、`win-x64`…；**每个平台一条独立的版本序列** | `android`、`windows`。没有跟约定用 `win-x64`，因为 `windows` 已经写在核心的 `platform` 设置、测试、旧发布目录和导入工具里，改名只会带来一次没有收益的迁移。**没有历史包袱的新应用请直接用约定名**，这样 `relhub list` 里各应用一致 |
| versionCode | 整数，同一平台内严格递增，发布后号码永久占用（撤回也不释放）；**Android 必须等于 APK 自己的 `versionCode`** | 沿用 `git rev-list --count HEAD`（提交数）：单调、不用手工改、新旧服务共用同一序列，桥接版在两边是同一个号码（§5） |
| channel | 渠道是「客户端能看到哪个版本」的指针，默认 `stable` | 只用 `stable`。灰度要用 `beta` 时，客户端得新增一个渠道设置，现在没有 |
| update_url | 服务**基地址，含挂载路径** | `https://<本机>.ts.net/release`，构建期写入（`release.local.env` 的 `CB_UPDATE_URL`，不进仓库，因为含 tailnet 名） |

## 2. 决策记录（为什么这样做）

| # | 决定 | 备选 | 理由 |
| --- | --- | --- | --- |
| 1 | **把 SDK 复制进 `core/third_party/relhub`**，保留模块名 `relhub`，`go.mod` 里 `require relhub v0.0.0` + `replace relhub => ./third_party/relhub` | A：`replace relhub => /home/tony/dev/release-publish`（SDK.md 推荐的最省事方式）；B：`go mod vendor`；C：复制并改写导入路径 | 仓库是**公开**的，GitHub CI 没有 `~/dev/release-publish`，A 会让 CI 直接红。B 要把整套依赖提交进来；C 要改 SDK 的导入路径，升级时易错。复制 + 保留模块名 + 相对路径 `replace` 与已有的 `third_party/gokeepasslib` 做法一致；gomobile（AAR）、Wine 下的 DLL、Windows 与 Android 构建已由 `make check` 验证，GitHub CI 在推送后确认。**不手改**，用 `scripts/sync-relhub.sh` 同步并写 `REVISION` |
| 2 | 只复制 `*.go` 源码，**不复制 SDK 的测试** | 全部复制 | SDK 的测试要启动服务端的 `internal/` 包，在别处编译不了 |
| 3 | **保留自己的 `Manager` 状态机，只替换传输层**（`Client`） | 全部改用 SDK 的 `WatchLoop` | 状态机里是应用语义：最高版本回放防护、退避、自动下载开关、清理旧包、事件。它们与分发服务无关，原样保留，原来的几十个状态机测试几乎不用改 |
| 4 | `type Manifest = relproto.Manifest`（类型别名） | 自己再定义一份结构并转换 | 字段、`Mandatory()` 都对得上，少一层转换就少一类 bug |
| 5 | 受信任公钥只留 release-publish 的这一把，不再信任旧的 `cbdist` 密钥 | 新旧并存 | 旧密钥再没有东西用它签名；多一个信任根就多一个攻击面。轮换时才需要列表里放两把 |
| 6 | 旧设备走**桥接版**过渡 | 客户端双栈（同时会说新旧两种协议）| 双栈是永久多出来的代码和测试；桥接是一次发布 |
| 7 | 发布脚本做**预检**和**按 App 视角的验证** | 只调用 `relhub publish` | 发布不可逆，且最重的事故（构建不信任服务的密钥）在发布成功后才会暴露——到那时设备已经装上了 |
| 8 | 导入旧版本历史（`relhub import clipbridge`） | 只发新的 | 保留回滚目标和下载页的历史，且可用 SHA-256 与旧源逐个核对 |

## 3. 客户端接入（Go）

### 3.1 步骤

1. `scripts/sync-relhub.sh`：复制 SDK，写 `REVISION`；`core/go.mod` 加 `require` 和 `replace`；`go mod edit -fmt`。
2. 在更新包里写一个薄的 `Client`，内部每次调用现建一个 `relclient.Client`（很便宜），把应用 id、平台、公钥、HTTP 客户端配进去。`Manifest` 用别名。
3. `Check(ctx, installedVersionCode)`、`Watch(ctx, known, wait)`、`Download(ctx, manifest, dir, progress)` 三个方法，原样替换旧客户端的同名方法，`Manager` 只改一行（`Latest(ctx)` → `Check(ctx, 已装版本)`）。
4. 把受信任公钥列表（`trustedKeysB64`）换成服务的公钥。
5. 门面层（facade）传入 `User-Agent: clipbridge/<版本名>`。
6. 删掉不再需要的：自带的清单签名/验签、分发服务器、发布器、`cbdist` 命令、安装 `cbdist` 服务的脚本和 unit。
7. 测试（§6）。

### 3.2 SDK 没有、必须自己补的（每一条都有测试，改传输层时别丢）

| SDK 的实际行为 | 风险 | ClipBridge 的补法 | 对应测试 |
| --- | --- | --- | --- |
| `WithTrustedKeys` 为空时，**只验清单格式，不验签** | 一次编译/配置失误（公钥解析失败得到空列表）就让所有更新变成「无签名也接受」 | `Client.rel()`：没有公钥直接返回 `UPDATE_REJECTED`，不构造 SDK 客户端 | `TestClientCheckErrors`（无公钥） |
| `relclient.New` 接受任意 `http(s)://` | 明文传输、凭据混在 URL 里 | `ValidateBaseURL`：只允许 https，http 仅限回环；拒绝带用户名、query、fragment 的 URL | `TestValidateBaseURL` |
| `http.Client` 默认跟随重定向 | 服务上的开放重定向或被攻破的代理把请求带到别的主机，或 https 降级成 http | 自带 `CheckRedirect`：最多 3 跳、不得换主机、不得换协议 | `TestClientRefusesCrossHostRedirect`、`TestRedirectToAnotherSchemeIsRefused` |
| 下载**没有卡死检测**（只靠 `ctx`） | 手机走出 Wi-Fi 后连接不断也不来数据，下载挂到系统超时 | 下载专用的 `RoundTripper`：响应体 60 s 没有数据就取消请求；错误实现 `Timeout()`，SDK 把它当网络错误从 `.part` 续传 | `TestClientStallThenResume` |
| 下载**不限制响应体长度**，来多少写多少 | 失控或被替换的服务把磁盘写满，之后才在校验时发现 | 同一个 `RoundTripper` 把响应体截到 `size+1` 字节，多出的一字节让 SDK 报校验失败 | `TestClientCapsAnEndlessBody` |
| 清单允许的包大小上限是 16 GiB | 签名清单里的笔误让手机去下载几个 GB | `MaxPackageBytes`（512 MiB）在检查和下载前都核对 | `TestClientCheckErrors`（超大包） |
| 错误是 SDK 自己的类型 | 壳按稳定错误码给用户看文案 | `mapError`：验签/校验失败 → `UPDATE_REJECTED`；包与清单不符 → `HASH_MISMATCH`；其余（网络、5xx）→ `UPDATE_FAILED`（可重试） | 多处 |
| `not_found` 既可能是「应用没注册」也可能是「该平台还没发布」；**代理没挂 `/release` 时同样是 404，只是没有服务的 JSON 错误信封**（SDK 把它的消息写成 `404 Not Found: …`） | 前两种对用户没区别：都是「没有更新」。但把**代理的 404** 也当成「没有更新」，会让设备在 `tailscale serve` 被重置后永远显示「已是最新」，更新悄悄停摆 | 只有**服务自己**回答的 `not_found` 才算：`Check` → `ErrNoRelease`（已是最新），`Watch` → `ErrWatchUnsupported`（退回每 6 h 轮询）；没有信封的 404 一律是 `UPDATE_FAILED`，界面上看得见 | `TestClientCheckErrors`（proxy 404）、`TestClientWatch`、`TestManagerBrokenFeedIsAnErrorNotUpToDate`、`TestManagerFallsBackToPollingWithoutWatch` |
| `Check` 的第三个参数是**已装版本** | 传 0 或写死会让服务端的统计失真（`relhub stats` 靠它判断旧版本还有没有人用） | `Manager` 传 `CurrentCode`；`User-Agent` 带应用版本 | `TestClientCheckAndDownload`、facade 测试 |
| `Download` 内建 3 次重试（1 s、2 s、4 s） | 失败路径的测试被拖慢到 7 秒以上 | `Client.RetryWait` 字段，测试里设 1 ms；产品里用默认 | 所有失败下载测试 |
| `Watch` 对已注册应用的空平台**不报错而是挂起**；对未注册应用返回 404 | 以为 404 就是「服务不支持 watch」 | 如上，404 一律当「退回轮询」 | `TestAgainstRealRelhub`（未注册应用） |

SDK 做得好、不用重复造的：按清单**本地重新判断**是否更新（不信响应里没签名的 `updateAvailable`/`mandatory`）、核对清单的 app/平台/渠道与请求一致（拿别的应用的合法签名冒充过不了）、`Range`+`If-Range`（带 SHA-256 的 ETag）续传、下载到 `.part` 校验后才改名。

### 3.3 `Manager` 里需要知道的两点

- 检查流程仍是「`Check` → 比较 → `available` → 下载」；`watch` 只是让下一次 `Check` 在发布后 1 秒内发生。`Watch` 的 `known` 取「已装版本」与「已见到的最高版本」中大的，避免重复通知。
- `update.high`（见过的最高 versionCode）是**每台设备持久化**的，换更新服务时 `SetClient` 会清零。因为 ClipBridge 在新旧两个服务上用同一条号码序列，桥接不会触发「回放」误报。如果新服务的号码序列比旧的**小**，设备会因为这条防护拒绝更新——换服务前先确认号码单调。

## 4. 发布侧（`scripts/release-local.sh`）

流程：**预检 → 构建 → 发布 → 按 App 的视角验证**。设计原则：不可逆的错误放在构建之前；验证用「装好的 App 将会走的那条路」，而不是服务端自己说「成功」。

| 阶段 | 检查 | 挡住的事故 |
| --- | --- | --- |
| 预检 | `CB_UPDATE_URL/v1/info` 可达 | 把连不上的地址烤进设备 |
| 预检 | 服务**正在用来签名的公钥**在 `core/update/keys.go` 的列表里（`sed` 取出编译进去的、`curl` 取 `/v1/info`、`grep -x` 比较） | 构建拒绝之后所有更新，只能手动重装。实测：换一个密钥不同的临时服务，脚本拒绝 |
| 预检 | `relhub list -all -app … -platform …` 里没有 ≥ 当前 versionCode 的版本 | 构建 5 分钟之后才在上传时发现号码被占 |
| Android | 签名证书 SHA-256 与上次钉住的一致 | 换签名的 APK 装不上 |
| Android | `aapt2 dump badging` 的 `versionCode` = 本次号码 | 服务要求二者一致，不一致就是系统安装器永远拒绝的更新 |
| 发布后 | `scripts/probe-update.sh`：真实核心代码 + 编译进去的公钥 + 公开 HTTPS 地址，假装已装的是上一个版本，完整下载，与本地构建产物比 SHA-256 | 公钥、地址、应用 id、平台名、签名、下载链路中任何一个不对 |

细节与经验：

- `relhub publish -app <id> -platform <p> -file <f> -version <name> -code <n> -notes <t> [-force]`。表单里 `file` 必须是最后一个字段（用 curl 发布时），CLI 和 SDK 已处理。
- 本机用 CLI 不需要配置（默认连 `127.0.0.1:18790` 并读管理员 token）。**公开读取接口不需要 token**，所以做「像 App 一样的验证」时不要带上管理员 token：脚本里设 `RELHUB_DATA=/nonexistent`（探测脚本用的是 Go 客户端，本来就不带 token）。
- **本地环境文件会覆盖命令行里的环境变量**：脚本先 `source release.local.env`，所以从命令行传 `CB_UPDATE_URL=…` 无效——这次预检第一次就因此拿旧地址跑，反而验证了「旧地址会被拒绝」。改地址要改文件。
- 某个平台发布了、另一个失败：修好后 `--platform <失败的> --no-build` 重跑；预检只检查被选中的平台。
- 要强制更新：`--force`（渠道的 `minVersionCode` = 本次号码）。
- 其它机器或 CI 发布：`RELHUB_URL=https://…/release RELHUB_TOKEN=<应用限定 token> relhub publish …`，不要把管理员 token 放进 CI。

## 5. 迁移已装设备（桥接）——这次最重要的经验

### 5.1 为什么旧设备不会自己过来

已装的 App 里编译进去两样东西：**信任根**（旧公钥）和**地址**（旧 `…/clipbridge`）。它们永远只问旧地址，也不认新服务的签名。新服务建得再好，旧设备都看不见。

### 5.2 桥接版

1. 新代码提交，用新流程发布到新服务（此时没有任何设备在读它）。
2. 把**同一个安装包**用旧工具、旧密钥推到旧源，版本号相同。旧设备按老路径发现 → 验签（旧密钥）→ 下载 → 安装。
3. 新版本此后只问新服务。旧源最后保留一个版本，等确认全部迁移后退役。

选择桥接而不是「客户端同时支持两种协议」的原因见 §2 的 #6。

### 5.3 失败模型与防线

桥接版如果新的更新链路有错（公钥、地址、应用 id、平台名、下载任何一个），装了它的设备**永远收不到更新**，只能手动重装——而且没有补救办法，因为设备不再问旧源。所以**桥接之前必须逐层证明链路是通的**：

| 层 | 证明什么 | 怎么做 |
| --- | --- | --- |
| 单元 | 客户端逻辑、故障路径 | 对假服务端的测试（§6.1） |
| 协议 | 假服务端与真实服务没有偏差 | `TestAgainstRealRelhub`（§6.2） |
| 线上 | 编译进去的公钥、地址、应用 id、平台名对得上生产服务 | `scripts/probe-update.sh`（§6.3）。这一步**不需要发布任何东西**：先 `relhub import` 把旧版本放进新服务，再假装已装版本更低去检查和下载——这次就是这样在桥接版存在之前就验证了生产链路 |
| 发布 | 刚发布的包按 App 的视角验证 | 发布脚本的最后一步 |

### 5.4 数据迁移

`relhub import clipbridge -from <旧发布目录> [-dry-run]`：把旧源的发行包复制进新服务（旧源不被改动，可重复运行）。要点：
- 「正在提供」的版本进 `stable`，更旧的进历史（不上线）；平台名取自旧目录名，**原样保留**。
- SHA-256 保持不变，可与旧源 `latest.json` 逐个核对（这次 0.1.79 的哈希一致）。
- 导入后的 `publishedAt` 是**导入时间**，不是当初发布的时间；包文件名由服务重新生成（`clipbridge-0.1.79-android.apk` 而不是 `clipbridge-android-0.1.79-79.apk`）——先确认客户端和壳不依赖文件名格式（ClipBridge 只看扩展名和核心给出的路径）。
- 号码序列必须连续：新服务里已有 79，下一次发布 ≥ 80。

### 5.5 容易漏的几处

- **持久化的覆盖设置**：如果设备把「更新地址」存进了本地设置（用户或测试时设置过），它会盖过编译进去的地址，桥接后仍指向旧源。迁移前检查有没有这类入口（ClipBridge 的 `update.url` 只能通过 `update.config` 调用写入，两个壳都没有界面入口，所以设备上不会有）。
- **签名密钥的备份责任变了**：清单签名私钥现在在 release-publish 里，不再在项目里；项目只剩 Android 的 JKS。更新备份清单和 `scripts/backup-keys.sh`。
- **「之后」的计划被牵连**：把清单签名交给服务之后，CI 不能用同一把私钥给 GitHub 上的包签名（私钥不能给 CI）。GitHub `nightly` 因此只能是对外下载渠道，应用内更新仍走本机服务（见 update.md §10）。
- **旧源没有访问日志**（`cbdist` 不记请求），所以**无法证明还有没有设备在用它**。只能反过来：新服务的 `relhub stats -app <id>` 会出现「来自新版本的检查」；设备数很少时，以设备上显示的版本号为准再退役。教训：分发服务一开始就要记请求日志（release-publish 每个请求一行，`journalctl --user -u release-publish`）。
- **退役旧源的命令都影响线上**（停服务、摘 `tailscale serve` 路径、删目录），逐条明确决定后手动执行；摘路径时只用 `--set-path /旧路径 off`，不要用不带路径的 `off` 或 `reset`。

## 6. 测试策略（三层）与隔离

### 6.1 假服务端：快，能注入故障

`core/update/updatetest`（普通包，不是 `_test.go`，所以 `facade` 的测试也能用；它**不得导入被测的 `update` 包**，否则 `update` 自己的测试会循环引用，所以应用 id 在两边各写一次并用测试保持相等）。它实现更新客户端用到的那一小部分协议：`check`、`watch`（挂起，发布时唤醒）、带 `Range`/`If-Range` 的版本固定下载；用一次性密钥签名；**记录收到的请求**（已装版本、`User-Agent`、`Range`、`If-Range`），测试用它断言客户端「告诉了服务它该知道的」。故障注入的办法是在它前面包一层 `http.HandlerFunc`：返回 503、错误内容、过长/无尽的响应体、半截后卡住、重定向到别的主机……

### 6.2 真实二进制：协议有没有跑偏

`TestAgainstRealRelhub`：`relhub serve -addr 127.0.0.1:0`（随机回环端口）+ 临时数据目录，从日志里 `msg=listening addr=…` 取端口；CLI 发布；客户端检查、续传下载、`watch` 被发布唤醒、`-force`、`relhub stats`。要点：
- **隔离**：子进程环境先剔除所有 `RELHUB_*`（开发者 shell 里可能指向线上数据目录），再显式设 `RELHUB_DATA`（临时目录）和 `RELHUB_URL`（临时服务）。永远不要让测试碰线上服务。
- 找不到 `relhub`（`RELHUB_BIN` 或 `PATH`）就 `t.Skip`，所以 GitHub CI 不受影响；本机每次 `make test` 都在检验「假服务端」没有和真服务端脱节。
- 它在第一次运行就通过了，这本身说明 SDK 文档与实现是一致的；当服务升级后它先红，就是协议变了的警报。

### 6.3 线上只读探测：密钥、地址、应用 id 真的对得上

`scripts/probe-update.sh [-u URL] [-c 已装版本] [-p 平台] [-o 目录]`：生成一个临时 Go 模块，用 `replace` 引入仓库的 `core`，调用**真实的** `update.Client`（编译进去的公钥、应用 id、平台名），检查并完整下载，打印 `平台 版本号 版本名 大小 sha256 路径`。只读，不发送 token。用法：
- 发版后（发布脚本已经自动做了）；
- 换服务/换密钥之前后；
- 怀疑「设备收不到更新」时，先在这里复现。
正面（正确地址）和反面（旧地址 → `no release published`）都要看过；公钥不符的反面由单元测试和发布脚本的预检覆盖。

### 6.4 其它验证手段

- 预检的反面用例：起一个密钥不同的临时 `relhub`，把 `release.local.env` 暂时指向它，脚本必须拒绝（做完要恢复文件）。
- 原生构建链：`make check` 覆盖 gomobile AAR、Wine 下的 DLL、C# 与 Android 测试——确认 `replace relhub` 在每条构建链里都能解析。

## 7. 踩坑清单

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 在命令行设了 `CB_UPDATE_URL=…` 却被忽略 | 脚本在读完环境之后 `source release.local.env` | 改文件，不要指望命令行覆盖 |
| `grep --include=*.go` 报 `no matches found`（zsh） | 本机 zsh 对未加引号的通配符报错 | `--include='*.go'` 加引号 |
| `echo ======` 报 `===== not found`（zsh） | zsh 把以 `=` 开头的词当命令展开 | 避免以 `=` 开头的字符串 |
| `relhub list` 多出自己不认识的应用 | 服务多项目共用，别的项目正在接入 | 只操作自己的 app id；不要凭「列表里不是我的」去清理 |
| 导入后版本的发布时间是现在 | `publishedAt` = 导入时间 | 文档里写明；需要原始时间看旧源的 `latest.json` |
| 下载的文件名变了 | 服务按 `<app>-<版本>-<平台><扩展名>` 命名 | 先确认壳只依赖扩展名和核心给出的路径 |
| `relhub check/download -url <公开地址>` 带上了管理员 token | CLI 默认读数据目录里的 `admin.token` | 做只读验证时 `RELHUB_DATA=/nonexistent` |
| 失败路径测试很慢 | SDK 下载默认重试 3 次，等待 1+2+4 s | `Client.RetryWait` 设成毫秒 |
| 想在测试里跑服务二进制却连到了线上 | 环境里有 `RELHUB_*` | 子进程环境先剔除，再显式设置 |
| `replace relhub => …` 不生效 | `replace` 只对**主模块**有效 | 谁是主模块谁写；探测脚本的临时模块要重复写 `core` 的 `replace`（包括 `gokeepasslib`） |
| 担心模块名 `relhub` 没有域名 | Go 允许，配合 `replace` 即可 | 已在 Linux/Windows/Android 交叉编译和 `make check` 验证 |
| 同步 SDK 后构建失败 | 服务的协议或 SDK 接口变了 | 先跑 `TestAgainstRealRelhub`，再看 `REVISION` 对应的提交差异 |
| 不确定服务到底有没有收到检查 | — | `relhub stats -app <id>`；请求日志 `journalctl --user -u release-publish` |

## 8. 下一个应用的接入清单

**服务侧**
- [ ] `systemctl --user status release-publish`；`curl https://<本机>.ts.net/release/healthz`
- [ ] 定好 app id / platform / versionCode 规则（§1）；`relhub app create -id … -name …`
- [ ] 有旧版本要保留：`relhub import … -dry-run`，核对后正式导入，核对 SHA-256
- [ ] CI 或别机器发布：`relhub token create -name ci-<app> -app <app>`（只显示一次，存进密钥库）

**客户端**
- [ ] `relhub keys` 取公钥，写进应用，**列表形式**（以后轮换要放两把）
- [ ] 引入 SDK（公开仓库 + CI → 复制进 `third_party` 并 `replace`，§2 #1）
- [ ] 补上 §3.2 表里的每一条：无公钥拒绝、URL 校验、重定向限制、卡死检测、响应体上限、包大小上限、错误码映射
- [ ] 检查时传已装版本；设置 `User-Agent`
- [ ] 本地持久化设置里没有会盖过更新地址的项（§5.5）
- [ ] 三层测试（§6）

**发布侧**
- [ ] 发布脚本含预检（可达、公钥受信任、号码空闲）和 App 视角的验证
- [ ] Android：签名证书钉住、APK 的 versionCode 与发布号一致
- [ ] 更新密钥备份清单（谁保管清单签名私钥？）

**迁移（如果有已装设备）**
- [ ] 先对线上服务做只读探测，通过后才桥接（§5.3）
- [ ] 桥接版：同一份包、同一个号码，同时在新旧两边
- [ ] 确认设备都已升级，再退役旧源（§5.5）

## 9. ClipBridge 中对应的文件

| 文件 | 作用 |
| --- | --- |
| `core/third_party/relhub/` | 复制进来的 SDK（`REVISION` 记录来源提交，`README.md` 说明规则） |
| `scripts/sync-relhub.sh` | 同步 SDK |
| `core/update/client.go` | 薄客户端：SDK + §3.2 的所有补强 |
| `core/update/manifest.go`、`keys.go` | 应用 id、平台名、`Manifest` 别名、受信任公钥 |
| `core/update/manager.go` | 状态机（与分发服务无关） |
| `core/update/updatetest/` | 假 release-publish |
| `core/update/{client,manager}_test.go`、`relhub_integration_test.go` | 三层测试中的前两层 |
| `core/facade/update.go` | 构建客户端（`User-Agent`），设置持久化 |
| `scripts/release-local.sh` | 预检 → 构建 → 发布 → 验证 |
| `scripts/probe-update.sh` | 线上只读探测 |
| `release.local.env.example` | 本地配置模板（`CB_UPDATE_URL`） |
| [update.md](update.md) | ClipBridge 的更新机制、发布流程、迁移步骤、测试矩阵 |
