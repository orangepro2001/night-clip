# 交接与上下文恢复：更新分发改用本机 release-publish（2026-10-28）

> 读完这一份就能接着干活。可复用的方法与坑在 [release-publish-integration.md](release-publish-integration.md)；ClipBridge 的更新机制、发布步骤、桥接与退役命令在 [update.md](update.md)。

## 0. 一句话状态

**0.1.83（versionCode 83）已在 2026-10-28 发布到 release-publish，并已桥接到旧源**：两边是同一份包（android sha256 `862c3418…`、windows `75c17380…`），release-publish 一侧用编译进 App 的公钥验过，旧源一侧用 `cbdist verify` 验过签名、并通过旧的公开地址下载比对了字节。已装的 ≤ 0.1.79 设备会从旧源收到它；装上之后它们只问 release-publish。**真机验证：** 用户在 2026-10-28 13:30 确认 Android 手机已经收到 0.1.83 的更新推送（经旧源，桥接起作用）；Windows 电脑还没确认。**还没验证的是新通道本身**：装了 0.1.83 的设备现在问的是 release-publish，要再发一个只发到 release-publish 的版本（不再推旧源），看设备能否秒级收到，才算证明（§3 第 3 条）。

经过：用户批准后第一次构建（0.1.82）在 R8 阶段因系统内存不足被中止（当时有一台约 10 GB 的虚拟机在跑），没有发布任何东西；用户关掉虚拟机后重跑，因提交了文档所以号码是 0.1.83。

## 1. 做了什么

| 类别 | 内容 |
| --- | --- |
| 核心（`core/update`） | `Client` 改为封装 release-publish 的 Go SDK；补上 SDK 没有的保护（无公钥拒绝、https/回环、重定向限制、60 s 卡死中止并续传、响应体上限 `size+1`、包大小上限、错误码映射、上报已装版本与 `User-Agent`、代理 404 不当作「没有更新」）。`Manager` 状态机不变，只把 `Latest(ctx)` 改成 `Check(ctx, 已装版本)`。受信任公钥换成 release-publish 的签名公钥 |
| 删除 | 自带的清单签名/验签、分发服务器、发布器、`core/cmd/cbdist`、`scripts/install-dist-service.sh`、`deploy/clipbridge-dist.service`、`make cbdist`（净减约 690 行生产代码） |
| 第三方 | `core/third_party/relhub`：SDK 原样复制（`REVISION` = release-publish `9f2a1cf`），`scripts/sync-relhub.sh` 同步；`core/go.mod` 加 `require relhub v0.0.0` + `replace relhub => ./third_party/relhub` |
| 测试 | 共享假服务端 `core/update/updatetest`；`client_test.go`、`manager_test.go`；对真实 `relhub` 二进制的 `TestAgainstRealRelhub`（没有二进制时跳过）；facade 测试移植。`core/update` 30 个测试 |
| 脚本 | `scripts/release-local.sh` 重写（预检 → 构建 → `relhub publish` → 验证；`--check` 只预检）；`scripts/probe-update.sh`（用真实核心客户端对线上服务做只读探测）；`scripts/sync-relhub.sh` |
| 本地配置（git 忽略） | `release.local.env`：`CB_UPDATE_URL=https://tony-ai-pc-1.tail9f7f1f.ts.net/release`；`release.local.env.example` 同步 |
| 生产 release-publish | 2026-10-28：`relhub app create -id clipbridge`；`relhub import clipbridge -from ~/.local/share/clipbridge/releases`（6 个历史版本；Android/Windows 的 0.1.79 进 `stable`，SHA-256 与旧 `latest.json` 一致）。**没有发布新版本** |
| 文档 | 新增 `release-publish-integration.md`（可复用经验）、本文；重写 `update.md`；改 `api-spec.md`、`HANDOFF.md`、`README.md` 里过时的描述 |

## 2. 现状一览

| 项目 | 状态 | 证据 / 怎么看 |
| --- | --- | --- |
| 仓库 `main` | 代码提交 `31bc6b1`（提交数 81）；本文随后一个提交 | `git log --oneline -3` |
| 下一次发布的号码 | = 提交数（`git rev-list --count HEAD`）。0.1.83 是从第 83 个提交 `714dd83` 发布的；之后每多一个提交 +1（下一个提交之后就是 0.1.84）。发布脚本要求工作区干净 | `scripts/version.sh` |
| 生产 release-publish | `clipbridge`：`android/stable=0.1.83`、`windows/stable=0.1.83`（2026-10-28 13:07 发布）；服务本身 2026-10-28 13:05 起由另一个会话改成 podman 容器运行（仍是 `127.0.0.1:18790` 和 tailnet `/release`，签名公钥不变），本项目只依赖 URL、公钥和 `relhub` 命令行；另有别的项目的应用 `go-nx`（**不是我们的，别动**） | `relhub list` |
| 旧分发 `clipbridge-dist` | 仍在运行，`/clipbridge` 仍在 `tailscale serve` 上，最新是 **0.1.83（桥接版）**；不记访问日志 | `systemctl --user status clipbridge-dist`、`tailscale serve status` |
| 设备 | ≤ 0.1.79 的编译着旧公钥和旧地址，**只会问旧源**，现在能从旧源收到 0.1.83；是否已更新**尚未确认** | 各设备「关于」里的版本号；`relhub stats -app clipbridge` 里出现来自 0.1.83 的检查 |
| GitHub CI | 推送后再看 | `gh run list --limit 3`（`gh` 已登录 `orangepro2001`） |

## 3. 待办（按顺序）与需要用户决定的

1. ~~发布新版本到 release-publish~~ **已完成（0.1.83）。**（下面是当时的命令，之后每次发版照用，号码以提交数为准。）**发布新版本到 release-publish。** 不影响任何设备（没有设备读它），可以先做，用来在生产上跑通整条脚本。命令（工作区必须干净、已提交）：
   ```sh
   scripts/release-local.sh --check                       # 预检，只读
   scripts/release-local.sh --notes "更新分发改用本机 release-publish（实时通知、统计、撤回）"
   ```
   约 5 分钟（Gradle + dotnet 构建）。脚本最后用真实核心客户端从公开地址验证。号码一旦占用就不会回来。
2. ~~桥接到旧源~~ **已完成（0.1.83，用户批准）。**原说明：**桥接到旧源（需要用户明确同意——这一步会让真机收到更新）。** 同一份包、同一个号码，用旧工具推到旧源，命令见 [update.md §7](update.md#7-从旧分发服务cbdist迁移)。推之前确认第 1 步的验证输出全绿；桥接版链路有错的话，装了它的设备再也收不到更新。
3. **真机上完整走一遍桥接更新**（Android：通知栏 → 允许安装未知应用 → 安装；Windows：任务栏闪烁 → 点击更新），装好后确认 `relhub stats -app clipbridge` 出现 `checks from stable/<桥接版的号码>`，并再发一个小的下一版验证新通道真的把更新推给了设备（秒级通知）。
4. 所有设备确认升级后**退役旧源**（update.md §7，几条命令都影响线上，需明确决定）。之后 `~/.config/clipbridge/release-ed25519.key` 和 `scripts/backup-keys.sh` 里的这一项可以去掉。
5. 把 `~/backups/release-publish-keys/` 另存一份到本机之外（清单签名私钥现在只在 release-publish 里；丢失意味着无法再给已装设备推可验证的更新）。
6. 推送后看 GitHub CI（`replace relhub` 的相对路径在 CI 里应当可用——本机 `make check` 已验证 gomobile/Wine/C#/Android 构建，但 CI 本身还没跑过）。
7. 更后面的 GitHub `nightly`：清单私钥不能给 CI，所以 GitHub 只是下载渠道（update.md §10）。**需要用户决定如何提供 Android 签名密钥。**

## 4. 恢复上下文：接手时怎么读、怎么做

阅读顺序：本文 → [update.md](update.md)（§6 发布、§7 迁移）→ [release-publish-integration.md](release-publish-integration.md)（§3.2 补强表、§5 迁移、§7 踩坑）→ release-publish 自己的 `~/dev/release-publish/docs/USAGE.md`。

常用命令：

```sh
curl -fsS https://<本机>.ts.net/release/healthz    # 服务在不在（它的运行方式由 release-publish 项目决定，目前是容器）
relhub list                                        # 看所有应用（注意：里面有别人的）
relhub list -all -app clipbridge -platform android # 我们的版本，含已撤回
relhub stats -app clipbridge                       # 各已装版本的检查次数
scripts/probe-update.sh -c 78                      # 像装好的 App 一样检查并下载，只读
scripts/release-local.sh --check                   # 发布前预检
scripts/sync-relhub.sh                             # release-publish 的 SDK 更新后重新同步
cd core && go test -race -count=1 ./update/ ./facade/   # 有 relhub 二进制时会连真实服务端的测试一起跑
make check                                         # 全量：Go、DLL(Wine)、C#、AAR、APK（约 4 分钟）
```

**别做的事：**
- 不要手改 `core/third_party/relhub`（用同步脚本）。
- 不要对线上数据目录运行 `relhub keys rotate`；轮换流程见 release-publish 的 `docs/USAGE.md` §8，必须先发一个同时信任新旧公钥的版本。
- 不要动 `go-nx` 或别的应用；不要新增 `tailscale serve` 路径；摘路径只用 `--set-path … off`，不要用不带路径的 `off` / `reset`。
- 不要在测试里连线上服务：集成测试已经剔除了 `RELHUB_*` 环境变量并使用临时数据目录，新写的也照做。
- 命令行里的 `CB_UPDATE_URL=…` 会被 `release.local.env` 覆盖（改文件）。
- `ls`/`cat` 在这台机器的交互 shell 里是 `eza`/`bat` 的别名，脚本里用 `/usr/bin/ls`；zsh 里含 `?`、`*` 的参数要加引号。

## 5. 过程与思路（为什么是这样）

1. **先摸清服务**。`release-publish` 是 `~/dev/release-publish`（Go，仅标准库）：REST + JSON-RPC、Ed25519 签名清单、渠道、`watch` 长轮询、撤回、统计、`import clipbridge`。它在本机已经以 systemd 用户服务运行，`/release` 已在 `tailscale serve`，开机就有。起初 `relhub list` 为空，做到一半多了别的项目的应用 `go-nx`——服务是多项目共用的。
2. **新旧协议非常接近**（检查/监视/可续传下载/签名清单），所以保留 `Manager`，只换传输层。最大的变化是签名私钥从项目移到服务。
3. **SDK 引入方式**：官方推荐 `replace => /home/tony/dev/release-publish`，但仓库公开、CI 没有这个路径，所以改为复制进 `third_party`（与 `gokeepasslib` 同做法）。
4. **写 `Client` 时逐行读了 SDK 源码**，发现它「不是安全策略」：没有公钥时只验格式、下载没有卡死检测和长度上限、不限制 URL 与重定向。旧客户端有的保护一条条补回去（并各有测试）。写发布脚本和文档时才又想到「代理 404 ≠ 没有更新」，提交前补上。
5. **迁移的核心难题**是已装设备不会自己过来。选桥接版而不是双栈。因为桥接版出错就无法补救，所以验证做成了四层：假服务端 → 真实 `relhub` 二进制 → 对生产服务的只读探测（先 `import` 历史版本，再假装已装 78 去检查并下载 79，**没有发布任何东西就验证了生产链路**）→ 发布脚本的预检与发布后验证。
6. **发布脚本的彩排**：在临时 worktree 里把 `keys.go` 的公钥换成临时 `relhub` 的，指向它，只发 Windows、不构建，把整条脚本跑了一遍（预检 → 发布 → 探测验证），再跑一次确认「号码已被占」会被拒绝。另用一个密钥不同的临时服务验证「服务密钥不被信任」会被拒绝。彩排用的 worktree 与临时服务已清理，生产没有被碰。
7. **弯路与教训**：预检第一次跑拿的是旧地址（`release.local.env` 覆盖了命令行变量）——顺带验证了旧地址会被拒绝；zsh 对通配符和 `=` 开头的字符串的处理；旧 `cbdist` 不记访问日志，导致无法证明「没人再用旧源」，所以退役条件改成以设备版本为准；`relhub import` 后 `publishedAt` 是导入时间、文件名被重新生成，要先确认壳不依赖旧文件名（只依赖 `.apk` 扩展名与核心给的路径）。

## 6. 验证记录（2026-10-28）

| 验证 | 结果 |
| --- | --- |
| `go vet ./...`、`gofmt` | 干净 |
| `go test -race -count=1 ./...`（core 全部包） | 通过（含 `core/update` 30 个、`core/facade` 更新相关 7 个） |
| `TestAgainstRealRelhub`（真实二进制、临时数据目录） | 通过：发布、检查、续传下载、验签、挂起的 watch 被发布唤醒、`-force`、管理器自动下载、`relhub stats`、未注册应用 |
| `make check` | exit 0：Go `-race`、Linux `.so` + C 烟雾测试、Wine 下 DLL、C# 935 个测试、WPF 编译、CLI、gomobile AAR、Android 单测 + APK 构建 |
| `scripts/probe-update.sh -c 78`（生产，公开 HTTPS 地址） | 两个平台都检查到 0.1.79、完整下载并验签（Android 49,468,241 字节 `8d71f2a9…`；Windows 11,648,262 字节 `1a5be41c…`）；旧地址 `/clipbridge` 明确失败 |
| 发布脚本预检 | 生产：通过；服务密钥不同的临时服务：拒绝；号码已被占：拒绝；旧地址：拒绝 |
| 发布脚本整条彩排（临时服务） | 预检 → 发布 → 探测验证通过；重复运行被拒绝 |
| `aapt2 dump badging` 取 versionCode | 在 0.1.79 的 release APK 上得到 79 |

## 7. 没有验证的

- 桥接版在真机上的一次完整更新（Android 系统安装器、Windows 替换重启走新通道）。
- 发布脚本里的**构建**步骤在新脚本下没有重跑（彩排用了 `--no-build`）；构建命令本身与之前相同，Android 签名证书钉住与 `aapt2` 版本号检查是新增/搬动的，会在第一次真实发布时首次执行。
- GitHub CI。
- 退役旧源的命令（`tailscale serve … off` 等）在本机从未执行过。

## 8. 本机全局配置（不在仓库里）

2026-10-28 还调整了本机 Claude Code 的全局 Git 规则和权限分类器配置（commit/push 不再逐次授权）。这属于个人机器配置，细节不放进公开仓库，记录在本机的 Claude 记忆里（`claude-code-global-git-automode`）。
