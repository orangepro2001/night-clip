# 计划：复制即自动广播 + Syncthing 评估

> 状态：**默认层已实施**（2026-10-26）：核心（阶段 1）、Windows 壳（阶段 2）、Android 第 1 层（阶段 3）；见 §11。Android 后台层（第 2/3 层）也已实现，见 §12，但**真机 PoC（§5）尚未完成**，不能当成已可用。
> 范围：评估并规划"检测到复制 → 自动向所有已配对设备推送"，以及评估 Syncthing 是否值得用于本应用。  
> 前提不变：所有设备都在同一个 tailnet，服务仅在 tailnet 内可达（见 [ADR-005](adr/005-go-core-and-tailnet-p2p.md)）。评价标准：**易用性第一**，安全只在不明显损害易用性的前提下加码。  
> 标注约定：**[已查证]** = 读了本仓库代码或官方页面；**[待验证]** = 凭记忆或推断，必须在真机 PoC 里确认，不能当事实。

## 1. 结论

1. **Windows → 所有设备：基本已经做完，缺的只是"默认值"。** 核心的 `clip.local_changed` 已经向**所有**已配对设备各排一个事件（`core/clip/service.go` `LocalChanged` → `sendTo`）。现在不是"自动广播"的原因有三个，全是默认值与保护逻辑，不是架构：
   - 同步开关默认 **关**，首次启动弹窗让用户选（`SettingCapture` 未设置即 `off`）；
   - 接收端默认策略是 **notify**（只通知，不写剪贴板）（`SettingApplyMode` 默认 `notify`）；
   - Android 端 **没有任何自动读取剪贴板的路径**，只有四个手动入口。
2. **Android → 其它设备：这是唯一真正难的部分。** Android 10+ 只允许"当前有焦点的应用"或默认输入法读取剪贴板 [已查证：[Android 10 隐私变更](https://developer.android.com/about/versions/10/privacy/changes#clipboard-data)]。没有"无需用户配置、后台、可靠"的读取方式。本计划采用**分层方案**：零配置层（前台/获得焦点时自动读取并广播）作为默认，后台层作为用户显式开启的进阶选项，由 PoC 决定做不做（§5）。
3. **接收端自动写入**是可行的，但必须加三道保护才能把默认值改成 `auto`：新鲜度（过期/补发的事件不自动写）、无时限回声抑制（防多设备乒乓）、可撤销（§4）。
4. **Syncthing：不建议嵌入或依赖。** 它是"文件夹最终一致同步"系统，不是"事件推送"系统，与剪贴板广播和"提供→接受→保存"的文件传输语义都不匹配，且在 Android 上有额外的维护风险（§7）。需要文件夹同步的用户可以让 Syncthing 与本应用**并行独立运行**，无需任何代码。

## 2. 现状（均为 [已查证]，来自代码）

| 环节 | 现状 | 位置 |
| --- | --- | --- |
| Windows 监听复制 | `WM_CLIPBOARDUPDATE` → `ClipboardMonitor` → `clip.local_changed`；开关 `Settings.Capture` | `windows/.../Services/ClipboardMonitor.cs`、`ShellController.cs` |
| 核心广播 | 对每个 `state=paired` 的对端各自 `Send`，每链路独立 `event_id`；对端离线则进发件箱，TTL 默认 15 分钟 | `core/clip/service.go` `sendTo` |
| 已有的发送侧过滤 | 空文本、>64 KiB、回声（3 秒窗口）、重复（3 秒窗口）、无对端、敏感且被禁止 | `LocalChanged` |
| 接收策略 | 每对端 `apply_mode`：`notify`/`manual`/`auto`；敏感内容永不 `auto` | `Decide`、`ClipboardDecider.cs`、`ReceiveDecider.kt` |
| Windows 自动写入保护 | 写入前比对剪贴板序号，用户在期间又复制过则降级为通知 | `ClipboardDecider.cs` |
| Android 自动写入 | 仅当应用在前台才 `WRITE_NOW`，否则降级为通知 | `ReceiveDecider.kt`、`ForegroundTracker.kt` |
| Android 读取 | 只在 `SendClipboardActivity.onWindowFocusChanged(true)` 里读（磁贴/通知动作/按钮） | `SendClipboardActivity.kt`、`ClipboardAccess.kt` |
| 密码管理器 | Windows 端忽略带"排除监控"标记的复制 | 说明书 §5 |

## 3. 目标与验收（"易用性第一"的具体含义）

目标体验：**配对完成后什么都不用设置**——在任一设备复制，几秒内所有在线的已配对设备的剪贴板里就是同样的内容；对端离线则回来后在有效期内补上（补上的内容不抢占剪贴板，见 §4.2）。

验收（全部要有自动化测试或真机记录）：

| # | 场景 | 期望 |
| --- | --- | --- |
| A1 | 全新安装，两台设备完成配对 | 无任何额外开关操作，Windows 复制 → 对端 ≤3 秒剪贴板更新（对端在线、自动应用） |
| A2 | 三台设备 A/B/C 全部互相配对，A 复制 | B、C 各收到一次；B、C 之间不再互相转发；3 秒后任何设备都不再发出该内容 |
| A3 | A 复制 X；B 离线 10 分钟后上线 | B 收到通知但**不覆盖**剪贴板；通知可一键应用 |
| A4 | 对端自动写入 X 后，该对端的剪贴板监听在 >3 秒后才触发（慢设备） | 不回传 X（无时限回声抑制） |
| A5 | 复制密码管理器内容 / 疑似口令 | 默认不广播或广播但永不自动写入（沿用现有规则） |
| A6 | 用户在对端刚复制了别的内容，此时 X 到达 | 不覆盖，降级为通知（Windows 已有，Android 补齐） |
| A7 | 用户点"暂停同步 30 分钟" | 期间本机既不发送也不自动应用；到时自动恢复 |
| A8 | 自动写入后用户点"撤销" | 恢复到写入前的内容（仅内存，不落盘） |
| A9 | Android 前台或获得焦点时复制 | 自动广播，无需点任何按钮 |

## 4. 设计

### 4.1 默认值与开关（核心 + 两个壳）

- `capture` 默认值改为 **on，但只在存在至少一个已配对设备后生效**（无对端时本来就是 `no_peers`）。已有的显式选择（`on`/`off`）一律保留，不覆盖。
- Windows 首次启动的阻塞弹窗改为：配对成功后在托盘气泡里**告知**"已开启自动同步，可在托盘菜单关闭"，不再要求选择。
- `default_apply_mode` 默认值改为 `auto`，只影响**之后新配对**的设备；**不改动已存在对端的 `apply_mode`**。已有设备在升级后首次打开设备页时给一条一次性提示"让它也自动应用？"（不静默改变用户已配置的信任策略）。
- 新增每对端发送开关 `send_enabled`（默认 true），用于"某台设备只收不发"（例如公用电脑）。需要 schema v4 迁移（当前 v3，见 `core/store/migrate.go`，迁移只追加不改旧条目）。
- 新增全局"暂停至…"：`paused_until`（unix 秒，设置项）。核心在 `LocalChanged` 与接收决策里都检查，两端因此行为一致，壳只负责显示倒计时。

### 4.2 接收端保护（把 `auto` 变成默认的前提）

1. **新鲜度**：自动应用只对"实时到达"的事件生效。核心在 `OnEvent` 给 `clip.received` 增加字段 `live`（bool）：事件在链路建立后的补发阶段到达、或 `now - created_at > 20s` 的，`live=false`，并且 `decision` 在核心里直接降级为 `manual`。壳不需要新逻辑，只读 `decision`。20 秒是起点，由真机数据调整；时钟偏差大时事件会被误判为过期，其结果只是降级为通知，是安全的失败方向。
2. **无时限回声抑制**：现有 `LastAppliedHash` + 3 秒窗口，会在慢设备（Android 剪贴板通知延迟、休眠唤醒）上失效，3 台以上设备时可能形成 A→B→A 的循环。改为：本机每次**应用**或**收到**的内容哈希，保存在最近 N（例如 32）条的环形表里，只要本机"读到的新复制内容"哈希在表内，就当作回声丢弃，不设时间上限；表项随用户真正复制了不同内容而自然被顶掉。这样 A2、A4 都由核心单测覆盖，不依赖壳的时序。
3. **不转发**：只有"本机用户读取到的本地复制"才会广播；`clip.received` 永远不会被核心再次广播（现状已是如此，写进测试固定住）。
4. **可撤销**：写入前壳在内存里保留上一条剪贴板文本（仅一条，进程退出即丢，**不落盘**，敏感标记的不保留），托盘/通知提供"撤销"。Android 不保证能读到写入前的内容（后台读不到），所以 Android 只在前台应用时提供撤销。
5. **沿用的保护**：敏感内容永不自动写入；Windows 写入前的序号检查；单条 64 KiB 上限。

### 4.3 广播语义

- 仍是"每对端一个事件"，不新增中继。三台设备时各自直连，没有单点转发（设备数通常很小，直接发送比做路由简单可靠）。
- 同一次复制在同一台设备上只发一次（现有 3 秒去重 + 壳端合并器 `Coalescer`）。
- 快速连续复制多次：每次都发；接收端最后一条生效，通知合并（Windows 合并器已有，Android 需要对同一来源 5 秒内的通知做合并）。

### 4.4 Android 读取分层

Android 不能用一种办法解决，按"用户配置成本"分三层，**默认只启用第 1 层**：

| 层 | 机制 | 配置成本 | 覆盖 | 状态 |
| --- | --- | --- | --- | --- |
| 1（默认） | 应用到前台 / 窗口获得焦点时读一次剪贴板，与上次已处理哈希比较，变了就广播；通知栏动作、磁贴、分享菜单保留 | 无 | 用户切到本应用时才发 | 立即可做 |
| 2（进阶） | 前台服务内监听剪贴板变化 + 悬浮窗（`SYSTEM_ALERT_WINDOW`）短暂获取焦点后读取 | 一次性授权"显示在其它应用上层" | 后台，若监听能触发 | **单独不可行**（已查 AOSP：Android 10+ 后台监听器不派发；见 §12） |
| 3（进阶，已实现） | 一次性 ADB 授权 `READ_LOGS`，通过日志检测"被拒绝的剪贴板访问"，再用悬浮窗取焦读取（开源项目 [ClipCascade](https://github.com/Sathvik-Rao/ClipCascade) 的做法） | 电脑上执行 2 条 adb 命令，重装后需重做 | 后台 | **已实现（2026-10-26，待真机）**；依赖系统日志格式，厂商差异待验证 |

不采用：

- **无障碍服务**：需要用户到系统设置里手动开启，旁载应用在 Android 13+ 还要先解除"受限设置"；权限过大，且与现有隐私叙事冲突。[待验证] 更新的 Android 版本可能进一步收紧。若 PoC 证明第 2/3 层都不可行，再单独写 ADR 重新评估，不在本计划范围内。
- **自带输入法**：默认输入法可读剪贴板，但要求用户换键盘，易用性反而最差。
- **root / Shizuku 授予 `READ_CLIPBOARD_IN_BACKGROUND`**：AOSP 的 CTS 说明该权限只给特权系统应用 [已查证：[CTS 测试](https://android.googlesource.com/platform/cts/+/680b05b92af%5E%21/)]，普通应用拿不到，不作为方案。

**第 2/3 层的实现说明（2026-10-26）：** 第 2 层单独不可行——AOSP 10–15 的 `ClipboardService.sendClipChangedBroadcast` 在分发前检查焦点，后台应用根本收不到回调；但系统会为该监听器写一条 `ClipboardService` 拒绝日志。因此最终实现是"注册空监听器产生日志 + `READ_LOGS` 读日志 + 悬浮窗取焦读取"，代码与验证见 [plan-android-background-read.md](plan-android-background-read.md) 与 [ADR-006](adr/006-android-background-read.md)。

Android 12+ 每次读取其它应用写入的剪贴板都会显示"已粘贴"提示 [已查证：[Secure Clipboard Handling](https://developer.android.com/privacy-and-security/risks/secure-clipboard-handling)]。这是系统行为，无法关闭；第 1 层每次焦点切换最多读一次（哈希未变不重复读），把提示频率压到最低。

**Android 接收端后台自动写入**：目前代码假设后台不能写剪贴板（`ReceiveDecider` 在后台一律降级为通知，写入走透明的 `CopyActivity`）。我记得"后台写入通常可行，受限的是读取"，但**没有查证**，也不同厂商可能不同——PoC-F1 验证。如果可行，实时模式开启时 Android 也能真正做到"对方复制，我这边剪贴板立刻更新"，这是用户体验上收益最大的一项。

## 5. PoC（真机，先做，决定后面的范围）

| 编号 | 问题 | 通过标准 | 失败后的处理 |
| --- | --- | --- | --- |
| F1 | 前台服务（实时模式）里能否 `setPrimaryClip` | 锁屏/其它应用在前台时写入成功，另一个应用能粘贴出新内容；在至少 2 个品牌/版本上试 | 沿用现状（通知 + 点"复制"）；Android 接收仍是半自动 |
| F2 | 后台时 `OnPrimaryClipChangedListener` 是否被回调 | 其它应用复制文本时，前台服务收到回调 | 第 2 层不可行，只剩第 1 层和第 3 层 |
| F3 | 回调后用悬浮窗取焦能否读到内容 | 读到的文本与复制内容一致，成功率 ≥95%（连续 50 次），无明显闪屏 | 第 2 层放弃 |
| F4 | 第 3 层 `READ_LOGS` 检测的可用性 | 同上；记录每个机型的日志关键字 | 不做第 3 层 |
| F5 | 3 台设备的乒乓与回声 | A2、A4 在真机（含慢设备）上成立 | 调整窗口/表大小 |
| F6 | 休眠唤醒、`tailscale down/up` 之后的补发是否被正确降级（A3） | 不覆盖剪贴板 | 修新鲜度逻辑 |

PoC 不需要写产品代码：F1–F4 用一个独立的最小 APK 即可；F5、F6 在第 1 阶段完成后直接在真机上跑。

## 6. 实施阶段

| 阶段 | 内容 | 产出 | 估计 |
| --- | --- | --- | --- |
| 0 | PoC F1–F4；写 ADR-006（"默认自动同步与自动应用"），并在 ADR-002 里标注"默认手动"部分被 ADR-006 取代 | 范围决定 | 2–3 天（需要真机） |
| 1 | 核心：`capture`/`default_apply_mode` 默认值、`live` 字段与降级、回声哈希表、`paused_until`、`send_enabled`（schema v4）。核心测试用 `internal/testnet` 夹具：三设备全互联的 A1–A4、A7；schema 迁移测试；Wine 下重跑 | 核心测试全绿 | 3–4 天 |
| 2 | Windows 壳：去掉首次阻塞弹窗改为气泡、托盘"暂停同步"、撤销、设备页每设备"发送/接收"开关与升级一次性提示；对应 xUnit | 壳测试全绿 | 2–3 天 |
| 3 | Android 壳：第 1 层（焦点自动读取 + 哈希去重 + 通知合并）、设备页开关与暂停、按 F1 结果放开后台自动写入；对应 JUnit | 前台全自动 | 3–4 天 |
| 4 | 第 2/3 层（设置页"后台自动发送（进阶）"，带向导和失败回退） | **已实现（2026-10-26）**；真机验证待做 | 3–6 天，实际已完成 |
| 5 | 文档（`user-guide.md` §5 重写、`api-spec.md` 新字段与设置项、`HANDOFF.md`）、`make check`、真机验证 A1–A9、`scripts/release-local.sh` 发布 | 发布 | 1–2 天 |

合计约 2–3 周（含真机），第 4 阶段另计。这是估计，不是承诺。

协议影响：`clip.received` 事件新增 `live` 字段，设置项新增 `paused_until`，对端新增 `send_enabled`。**线上协议（信封、链路帧）不变**，旧版本对端互通不受影响（`live` 是本机计算的，不上线路）。

## 7. Syncthing 评估

对象：[syncthing/syncthing](https://github.com/syncthing/syncthing)。

### 7.1 事实

| 项 | 内容 | 来源 |
| --- | --- | --- |
| 许可 | MPL-2.0（文件级弱 copyleft，修改过的 MPL 文件需公开，链接使用不传染） | [已查证] 仓库首页 |
| 语言 | Go，`go 1.26.2`；仓库 HEAD 为 2026-10-26 | [已查证] 本机浅克隆 |
| 稳定版 | 2.1.5，2026-09-08（二手来源，维基百科条目） | 未直接核对发布页 |
| 协议 | BEP v1：TLS 1.3+ 之上的 Cluster Config / Index / Index Update / Request / Response / Download Progress / Ping / Close；设备身份 = 证书 SHA-256 指纹；"为文件夹索引同步设计，不是通用的临时推送" | [已查证] [BEP 规范](https://docs.syncthing.net/specs/bep-v1.html) |
| 代码量 | `lib/` + `cmd/` 非测试 Go 约 6.9 万行；依赖含 `quic-go`、`mattn/go-sqlite3`（cgo）与 `modernc.org/sqlite`（纯 Go） | [已查证] 本机统计 + `go.mod` |
| 可嵌入性 | `lib/syncthing` 是整个应用的服务封装，内部引用 `internal/db`；等于要整体嵌入配置、数据库、REST/GUI、发现、扫描、版本管理，没有"只取协议"的稳定小接口 | [已查证] 读了 `lib/syncthing/syncthing.go` 的导入；"官方不承诺库 API 稳定"为 [待验证] |
| Android | 官方 Android 应用在 2024-12 之后停止发布（因 Google Play 政策与维护人力）；目前靠社区分叉（如 [Syncthing-Fork](https://github.com/Catfriend1/syncthing-android)），2026 年初有人提议合并重启，**没有确认的官方客户端** | [已查证] 多个新闻/论坛来源 |
| 体积/内存 | 未测量 | [待验证] |

### 7.2 与我们需求的匹配

| 需求 | Syncthing 能做到吗 | 判断 |
| --- | --- | --- |
| 复制即推送文本 | 只能把文本写进共享文件夹的文件里，靠文件监听 + 索引交换 + 块请求完成。延迟通常秒级到更长，且每条剪贴板都落成磁盘文件（含口令类内容的明文历史），需要自己清理；没有 TTL、没有逐事件回执、没有"应用策略"、没有敏感标记 | **不匹配**，并且比现状更差 |
| 文件传输"提供→接受→保存"，只在对方同意后传输 | 文件夹同步默认是持续、自动、双向（或单向）的，没有逐文件的接受/拒绝提示；"发到对方收件箱"需要靠约定好的发送/接收文件夹模拟 | **语义不同**。我们已有可续传 + SHA-256 + 接受/拒绝的 `xfer` |
| 配对 | 设备 ID 交换并互相接受；与我们的"二维码 + 6 位 SAS + 指纹绑定"是第二套配对体系，用户要配对两次，除非自己用 REST 驱动 Syncthing 配置（再多一层胶水） | **增加摩擦**，与"易用性第一"冲突 |
| 仅 tailnet | 需要关闭全局发现、本地发现、中继、NAT 穿透、使用统计与升级检查，设置静态地址 `tcp://100.x.y.z:22000`，监听地址限定在 tailnet；GUI/API 只绑回环并设置密钥 | 可以做到，但是一长串配置，漏一项就会对外联网 |
| Android | 要在进程内跑一整个 Syncthing（没有 gomobile 绑定的官方先例，官方应用是把它作为独立原生进程运行），与我们的核心争用 SQLite 与电量；官方客户端已停更 | **风险高** |
| 与现有核心的关系 | 我们的核心已是 Go + mTLS 固定证书 + 发件箱 + 游标 + 续传；Syncthing 能提供的"块级传输"已有等价物（HTTP Range + SHA-256） | **没有可复用的缺口** |

### 7.3 结论与建议

- **不集成。** 三个原因：语义不匹配（它做的是文件夹一致性，不是事件与交互式传输）、用户体验会变差（两套配对、剪贴板落盘）、Android 端维护风险高。
- **可借鉴而无需依赖的点**（均已在我们的设计里）：证书指纹作为设备 ID、断点续传、块/整体哈希校验。
- **并行使用**：如果用户另有"整个目录自动同步"的需求，建议用户另装 Syncthing（可同样经 tailnet 运行），与本应用互不干扰；我们不做胶水代码。
- **重新评估的触发条件**：出现"多个 GB 的目录要持续双向同步"的明确需求，并且官方 Android 客户端恢复发布。届时先做体积、内存、电量的实测（本次**没有**构建或运行它），再决定是外挂（推荐）还是嵌入。

## 8. 风险

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| 已配对设备可以直接改写你的剪贴板（默认 `auto`） | 如果某台已配对设备被攻陷，可以投放恶意内容让你粘贴（如命令行） | 信任边界仍是"配对 + 证书固定"；多行/含换行的文本、含明显命令行特征的文本可降级为通知（作为可选加固，默认关，列入阶段 2 评估）；撤销；暂停；`send_enabled` 与逐设备 `apply_mode` |
| 多设备循环 | 剪贴板在设备间来回弹 | 无时限回声哈希表 + 只广播本地复制（§4.2），有 3 设备测试 |
| 补发内容覆盖用户刚复制的内容 | 数据损失 | `live` 判定 + Windows 序号检查 + Android 前台判定 |
| 敏感内容外泄到其它设备 | 隐私 | 沿用现有敏感识别，默认可广播但永不自动写入；设置里仍可完全禁止发送 |
| Android 后台方案不稳定 | 用户期望与现实不符 | 默认只承诺第 1 层；第 2/3 层标注"进阶/实验"，失败时回退到第 1 层并在界面里说明原因 |
| 电量 | 后台前台服务 + 悬浮窗 | 只在用户开启"实时模式"和进阶选项时运行；PoC 里记录耗电 |
| 文档与代码漂移 | 用户说明书与实际默认值不一致 | 阶段 5 重写 `user-guide.md` §5 |

## 9. 需要用户决定的事

1. 是否接受把默认策略改成"配对后自动同步 + 自动应用"（§4.1）？这改变了 ADR-002 的"默认手动"原则；我的建议是接受，并附带暂停/撤销/逐设备开关。
2. Android 后台读取（第 2/3 层）是否值得做：取决于 PoC，且需要用户做一次性授权。可以先只做第 1 层上线，再看 PoC。
3. 是否需要"疑似命令行/多行文本不自动写入"这种额外加固（默认关还是默认开）。
4. PoC 需要真机：请说明可用的 Android 机型和系统版本。

## 10. 参考

- [Android 10 隐私变更：剪贴板](https://developer.android.com/about/versions/10/privacy/changes#clipboard-data)
- [Secure Clipboard Handling（Android 12+ 提示、`EXTRA_IS_SENSITIVE`）](https://developer.android.com/privacy-and-security/risks/secure-clipboard-handling)
- [AOSP CTS：`READ_CLIPBOARD_IN_BACKGROUND`](https://android.googlesource.com/platform/cts/+/680b05b92af%5E%21/)
- [ClipCascade（READ_LOGS + 悬浮窗方案）](https://github.com/Sathvik-Rao/ClipCascade)
- [Syncthing 仓库](https://github.com/syncthing/syncthing)、[BEP v1](https://docs.syncthing.net/specs/bep-v1.html)
- [Syncthing 官方 Android 应用停更报道](https://itsfoss.com/news/syncthing-android-app-no-more/)、[Syncthing-Fork](https://github.com/Catfriend1/syncthing-android)
- 本仓库：[ADR-002](adr/002-triggered-clipboard-sync.md)、[ADR-005](adr/005-go-core-and-tailnet-p2p.md)、[api-spec.md](api-spec.md)、[user-guide.md](user-guide.md)、[../HANDOFF.md](../HANDOFF.md)

## 11. 实施记录（2026-10-26）

已实现并有自动化测试：

| 项 | 实现 | 测试 |
| --- | --- | --- |
| `capture` 默认 `on`、`default_apply_mode` 默认 `auto`（仅新配对设备；已有设备不变；监听方与加入方两条配对路径都读该默认值） | `core/clip/service.go`、`core/store/settings.go`（`NewPeerApplyMode`）、`core/pairing/manager.go`、`core/facade/network.go` | clip、facade |
| 新鲜度：`clip.received.live`；超过 20 秒或暂停中，`auto` 降级为 `manual` | `OnEvent` | `TestOnlyFreshEventsAreAppliedAutomatically`、`TestPauseStopsSendingAndAutomaticApply` |
| 无时限回声记忆（32 项，被真正的本地复制清空）；三设备全互联不弹跳 | `echoRing` | `TestAppliedTextIsNotSentBackAsAnEcho`、`TestNothingBouncesBetweenThreeDevices` |
| `paused_until` 设置（`suppressed: paused`） | `Paused()` | 同上 + `ValidateSetting` |
| 每设备 `send_enabled`（schema v4，`send_disabled` 列，默认 0，旧设备继续接收） | `store`、`peers.update/list` | `TestMigrationV4KeepsExistingPeersReceivingBroadcasts`、`TestSendDisabledPeersAreLeftOutOfBroadcasts` |
| Windows：首次启动不再弹窗改为一次性气泡；托盘“暂停同步/恢复”；“撤销上次自动写入”（内存、2 分钟）；设备页“自动发送”开关 | `ShellController`/`ShellActions`/`TrayService`/`DevicesPage` | `PeerModel`、`SettingsValues`、`clip.received.live` 解析（xUnit） |
| Android 第 1 层：窗口获得焦点时读一次剪贴板，哈希去重、5 分钟新鲜度、首次只记基线；设置页“自动发送我复制的内容”与“暂停同步”；设备菜单“自动发送到此设备” | `ClipAutoSend`、`FocusAutoSend`、`MainActivity` | `ClipAutoSendTest`、`ModelParsersTest`、`FormattersTest` |

另外顺手修了一个已有的不稳定测试：收件箱同一秒内到达的多条按随机 `event_id` 排序，改为按插入顺序（`ORDER BY received_at DESC, rowid DESC`）。

**未做 / 与计划的差异（如实）：**

- 真机 PoC F1–F6（§5）没有做：Android 后台写剪贴板、后台监听、悬浮窗取焦、`READ_LOGS` 都**未验证**；Android 后台自动写入仍沿用“前台才写、否则通知”。第 2/3 层（后台读取/发送）已实现，但只能算“待真机验证”，见 §12。
- Android 的“撤销”未做（后台读不到写入前内容，计划里本来就只在前台提供）；通知合并（5 秒内同一来源）未做。
- 后台自动发送（第 2/3 层）已实现：见 §12；真机 PoC F2–F4 仍未跑，不能当成已可用。
- Windows/Android 的界面只编译、未在真机上运行（与整个项目现状一致）。
- 疑似命令行/多行文本降级为通知（§8）未做。

## 12. 后台读取（2026-10-26，已实现，待真机）

- 实现：`LogcatClipboardWatcher`（注册空 `OnPrimaryClipChangedListener` + `logcat -T 1 ClipboardService:E *:S`）+ `ClipboardOverlayReader`（1×1 `TYPE_APPLICATION_OVERLAY` 取焦读取）+ `BackgroundReadController` + `AutoSend`。
- 授权：`SYSTEM_ALERT_WINDOW`（设置里开）+ `adb shell pm grant com.nightclip.app android.permission.READ_LOGS`（电脑一次）。默认关闭；没有权限时回退第 1 层。
- 已验证：Android JVM 单测与 debug APK 构建；AOSP 源码核对（监听器不派发、拒绝日志、`READ_LOGS` 是 development 权限）。
- **未验证**：真机后台触发、悬浮窗取焦、键盘影响、各 OEM。详见 [plan-android-background-read.md](plan-android-background-read.md) §5。
