# 壳层开发指南（Windows / Android）

> 读者：写 Windows 壳（C# WPF）和 Android 壳（Kotlin Compose）的人。
> 唯一的接口依据：[api-spec.md](api-spec.md) §7（方法与事件）+ 本文 §2（文件传输契约）。
> 架构边界见 [architecture.md](architecture.md) §2.2/§2.3：**壳不做协议、重试、去重、加密、网络、序号**，这些都在 Go 核心里。壳只做三件事：把平台能力（剪贴板、文件、通知、托盘、权限）接到核心，把核心的状态显示出来，把用户的操作变成方法调用。

## 1. 共同要求

- **界面语言：简体中文**。所有面向用户的文字放进集中的资源（Windows `Strings.cs` 或资源字典；Android `strings.xml`），代码里不散落字面量，以后可以加英文。
- **调用核心**：只通过 `Call(method, paramsJSON)` 和 `NextEvent(timeoutMs)`。`NextEvent` **只能有一个消费者**：每个进程一个事件泵线程，把事件分发给界面（Windows：`CoreHost.EventReceived` 已是如此；Android：Application 级一个线程 + `SharedFlow`）。界面**不得**自己轮询事件。
- **线程**：`Call` 可能阻塞（例如 `update.check` 最长 30 s、`pair.join` 立即返回但 `peers.remove` 会等对端确认至多 1.5 s）。**不要在 UI 线程里调用**。
- **错误**：核心错误只依赖 `error.code`（api-spec §1.4）。每个 code 要有一条人话提示（如 `NO_TAILNET` →“没有检测到 Tailscale 网络，请确认 Tailscale 已登录并连接”）。未知 code 显示通用提示 + code。
- **敏感内容**：`clip.received.sensitive == true` 时，通知和列表默认不显示正文（显示“敏感内容（已隐藏）”，点击后才展开）；永不自动写入剪贴板（核心已保证 `decision != auto`，壳也要再检查一次）。
- **日志**：不得记录剪贴板文本、psk、数据库密钥、私钥。事件日志只记 `type` 和 `event_id`。
- **不新增不必要的依赖**。Windows 不引入 NuGet 包（用 `UseWindowsForms` 的 `NotifyIcon`、P/Invoke 实现 DPAPI 与剪贴板）。Android 允许的新增依赖：`com.journeyapps:zxing-android-embedded`（扫码）、AndroidX 官方库；其余先问。
- **测试**：纯逻辑（解析、状态映射、策略判断、文本处理、文件名清洗）写单元测试并放进 `make check`；UI 本身无法在这台机器上运行，所以把逻辑从界面里分出来，界面只做绑定。
- **不要**修改 `core/`、`docs/`（本文件除外）、`Makefile` 里与你无关的部分。不要 `git commit` / `git push`。

### 剪贴板文本约定

- 核心已经把换行规整为 `\n`。**Windows 写入剪贴板前**把 `\n` 转成 `\r\n`；**读取后**把 `\r\n` 转回 `\n` 再交给 `clip.local_changed`。
- 上限 64 KiB（UTF-8 字节）；更大的文本核心返回 `suppressed: "too_large"`，壳提示“太大，请作为文件发送”。

### 配对流程（两端 UI 要点）

1. 电脑（监听方）：`pair.begin` → 显示二维码（用返回的 `qr.rows` 自己画，每个模块至少 6 px，保留静区）、`link` 文本（带“复制”按钮）、倒计时（`expires_at`）。离开界面时 `pair.cancel`。
2. 手机（加入方）：扫码得到 `link` → `pair.join {link}`；或粘贴 link；或手动输入 `host`/`port`/`psk`（以及可选 `fp`）。收到 `pair.sas` 后显示**大号 6 位数字**“请核对电脑上显示的数字是否一致”。
3. 电脑收到 `pair.request` → 弹出确认窗口：设备名、平台、**同样的 6 位数字**、“允许 / 拒绝” → `pair.confirm`。
4. 双方收到 `pair.result`：`accepted` → 回到设备列表；`failed`/`expired` → 显示 `code` 对应的提示。
5. Win↔Win：两台都能 `pair.begin`，也都能 `pair.join`。

### 设备状态文案

`peers.list[].state`：`connected` 已连接 / `connecting` 正在连接 / `offline` 离线（排队 `outbox_pending` 条）/ `unpaired` 对方已不认识本机，需要重新配对 / `revoked` 已解除配对。

## 2. 文件传输契约（核心实现中，壳按此编写）

方法（均经 `Call`）：

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `file.offer` | `to`（device_id），以及 **Windows**：`path`；**Android**：`fd`（`ParcelFileDescriptor.detachFd()` 得到的整数，所有权转给核心）、`name`、`size` | `{transfer_id}`。核心先计算 SHA-256（大文件需要时间，期间状态 `hashing`），再向对端发出 offer |
| `file.accept` | `transfer_id`, `save_dir?` | 接收方同意。Windows 默认保存到 `settings` 的 `download_dir`（默认“下载\ClipBridge”）；Android 不传 `save_dir`，核心写到应用私有目录 `…/received/`，完成后由壳用 MediaStore 存到“下载”，再调用 `file.release` |
| `file.reject` | `transfer_id` | 接收方拒绝 |
| `file.cancel` | `transfer_id` | 任一方取消进行中的传输 |
| `file.release` | `transfer_id` | Android：壳已把文件搬进 MediaStore，核心可以删除私有副本并把该传输标记为已完成 |
| `transfers.list` | `limit?` | `{transfers:[{transfer_id, peer_id, peer_name, direction("in"/"out"), name, size, sha256, state, offset, rate_bps, error?, path?, created_at, expires_at}]}`，新的在前 |

传输 `state`：`hashing` → `offered` → `accepted` → `transferring` → `done`；终态还有 `rejected`、`cancelled`、`failed`（带 `error`：`HASH_MISMATCH`、`NO_SPACE`、`TOO_LARGE`、`EXPIRED`、`PEER_OFFLINE`…）、`expired`。

事件：

```json
{"type":"file.offered","transfer_id":"tr_…","from":"dev_…","from_name":"Pixel","name":"a.pdf","size":123456,"sha256":"…","mime_type":"application/pdf"}
{"type":"file.state","transfer_id":"tr_…","state":"transferring"}
{"type":"file.progress","transfer_id":"tr_…","offset":1048576,"size":123456789,"rate_bps":52428800}
{"type":"file.done","transfer_id":"tr_…","direction":"in","path":"C:\\…\\a.pdf","name":"a.pdf","size":123456,"sha256":"…"}
{"type":"file.failed","transfer_id":"tr_…","code":"HASH_MISMATCH","message":"…"}
```

行为要点（壳要体现）：

- 收到 `file.offered`：**不自动接收**。显示通知/对话框（文件名、大小、来自谁）和“接收 / 拒绝”。
- 进度条用 `file.progress`（节流约 4 次/秒）；显示速度与剩余时间；可取消。
- 断线会自动续传，壳不用做什么；`transfers.list` 里 `state` 回到 `transferring` 即可。
- 完成后：Windows“打开文件 / 打开所在文件夹”；Android 保存到“下载”并提示。
- 校验失败：核心已删除临时文件，壳提示“文件校验失败，请让对方重发”。

核心设置键（加到 `settings.*`）：`download_dir`（Windows 默认路径，字符串）、`max_file_bytes`（默认 4294967296）。

## 3. Windows 壳（`windows/ClipBridge.Windows/`，`net10.0-windows`，WPF + WinForms NotifyIcon）

必须实现：

1. **单实例**：命名 Mutex；第二次启动把第一个实例带到前台（命名事件或 `PostMessage`），然后退出。
2. **数据库密钥用 DPAPI**：`CryptProtectData`（P/Invoke，`CRYPTPROTECT_UI_FORBIDDEN`，当前用户范围）保护 32 字节随机密钥，存为 `%LOCALAPPDATA%\ClipBridge\devkey.dpapi`。首次启动若发现旧的明文 `devkey.b64`：读出 → 用 DPAPI 保护写入新文件 → 校验能解回同一密钥 → 删除明文（顺序不能错，失败要保留旧文件并继续用）。密钥文件损坏时**不得**自动重建（会使数据库永久不可读），而是报错。保持 `DevKey.cs` 对外行为兼容（测试在用）。
3. **托盘**（`NotifyIcon`）：图标随状态变化（正常 / 无连接 / 警告）；左键打开主窗口；右键菜单：打开、配对新设备…、“同步我复制的内容”（勾选，对应 `capture`）、立即发送当前剪贴板、开机自启（勾选，写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，参数 `--minimized`）、检查更新、退出。关闭主窗口只是隐藏到托盘。`--minimized` 启动时不显示窗口。
4. **剪贴板监听**：隐藏消息窗口 + `AddClipboardFormatListener`；`WM_CLIPBOARDUPDATE` 后防抖约 50 ms；读 `CF_UNICODETEXT`，`OpenClipboard` 失败重试至多 3 次（间隔 30 ms）；空内容不发；若剪贴板含格式 `ExcludeClipboardContentFromMonitorProcessing`（密码管理器使用）则忽略；读到的文本转 `\n` 后调用 `clip.local_changed`。
5. **接收**：按 `clip.received.decision`：`notify` → 托盘气泡（正文预览 ≤ 80 字符；敏感则不显示）；`manual` → 气泡，点击气泡或在收件箱点“复制”才写剪贴板；`auto` → 先比较 `GetClipboardSequenceNumber()` 与事件到达时记录的值，**不同**（用户又复制了别的）则降级为通知，**相同**才写入。写入成功后**同步地**立刻调用 `clip.mark_applied`（此时窗口消息还没处理，核心的回声抑制窗口先于 `WM_CLIPBOARDUPDATE` 生效）；用户忽略调 `clip.dismiss`。
6. **配对界面**：见 §1“配对流程”。二维码用 `WriteableBitmap` + 最近邻缩放绘制，不引入库。
7. **设备页**：列表（名称、平台、状态、排队数、最后在线）；操作：重命名、策略（只通知 / 手动 / 自动）、重新连接（`unpaired` 时提示重新配对）、解除配对（确认对话框，`peers.remove`）。
8. **收件箱页**：最近的剪贴板条目（时间、来自、预览、状态），按钮“复制”“忽略”；敏感条目折叠；“清空”（`inbox.clear`，确认）。
9. **设置页**：同步开关（首次启动弹窗让用户选择，选择前保持关闭）、默认策略、有效期（`clip_ttl_s`）、历史条数、是否允许发送敏感内容、开机自启、下载目录、**更新**（现有更新栏移入这里 + 关于页）。
10. **文件**：主窗口“发送文件…”（选择设备 + 文件选择器，可多选逐个 offer）、拖放文件到设备卡片；收到 offer 的对话框（接收/拒绝，可改保存位置）；传输列表（进度、速度、取消、打开）。
11. **诊断页**：`diagnostics.get`（状态、监听地址与端口、各对端游标）、`NO_TAILNET` 横幅（“未找到 Tailscale 地址”）、“复制诊断信息”、打开日志/数据目录。
12. **防火墙提示**：首次监听时 Windows 可能弹出防火墙授权，设置页说明“请允许专用网络”。
13. 保留并接上现有的更新逻辑（`Update/` 目录），更新到达时托盘气泡 + 任务栏闪烁。

建议结构：`ViewModels/`（纯 C#，可测试）、`Views/`、`Services/`（`ClipboardMonitor`、`TrayService`、`DpapiKeyStore`、`CoreEvents`）、`Strings.cs`。把“事件 → 动作”的决策（例如 `ClipboardDecider`：给定事件、序号快照、设置，返回 `Notify/WriteNow/Downgrade`）写成无 UI 依赖的类并充分测试。

## 4. Android 壳（`android/app/`，Kotlin + Compose，minSdk 26）

必须实现：

1. **进程级核心**：`Application` 子类启动 `CoreHost`（单例）和**唯一的事件泵线程**，事件进 `SharedFlow`；界面与服务都订阅它。去掉界面里对 `update.status` 的轮询，改为订阅 `update.*` 事件并在进入界面时读一次。`CoreHost.start` 的失败（`KEY_INVALID`/`DB_UNAVAILABLE`）要有可见的错误页（原因 + “重置本机数据”按钮，二次确认）。
2. **数据库密钥用 Android Keystore**：在 Keystore 生成不可导出的 AES-GCM 密钥，包裹随机 32 字节数据库密钥，包裹结果存在 `filesDir/clipbridge/devkey.wrapped`。首次发现旧明文 `devkey.b64`：读出 → 包裹写入 → 校验可解回同一密钥 → 删除明文（失败则保留旧文件继续用）。包裹文件损坏**不得**自动重建。
3. **首次引导**：说明需要 Tailscale 并已连接；请求通知权限（Android 13+）；提示电池优化（`ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` 说明页，不强制）。
4. **配对**：扫码（`zxing-android-embedded` 的 `ScanContract`，需要相机权限，拒绝时退回手动输入）、粘贴 link、手动输入 host/port/密钥/指纹。等待期间显示大号 SAS（见 §1）。可见 Tailscale 状态提示（`ConnectivityManager` 的 VPN 传输是否存在，仅作提示）。
5. **发送入口**：(a) 主界面“发送剪贴板”按钮（前台读取剪贴板）；(b) 分享目标：`ACTION_SEND text/plain` → `clip.send`；(c) 常驻通知上的“发送剪贴板”动作与快捷设置磁贴（`TileService` → 透明 Activity 获得焦点后读取剪贴板，读完立即 `finish`）；(d) 手动输入框发送。默认读取只发生在用户可见的前台时刻（Android 10+ 限制）。(e) **可选进阶**：设置里开启“后台自动发送”后，`LogcatClipboardWatcher`（`READ_LOGS` + `ClipboardService:E` 拒绝日志）触发 `ClipboardOverlayReader`（1×1 `TYPE_APPLICATION_OVERLAY` 取焦）读取，再走与 (a) 相同的 `AutoSend`。默认关闭；没有两个权限时回退前台方案。见 `docs/adr/006-android-background-read.md`。
6. **接收**：通知渠道“收到的剪贴板”；通知正文预览（敏感则“敏感内容”）；动作“复制”（经一个透明 Activity 在前台写剪贴板，写完 `clip.mark_applied` 并 `finish`）和“忽略”（`clip.dismiss`）。`decision == auto`：App 在前台时直接写入并 `mark_applied`；在后台时降级为通知。
7. **实时模式**：设置里的开关，**持久化**；开启时启动前台服务（`specialUse`，常驻通知“已连接 N 台设备”，带“断开”动作）。开机自启：`RECEIVE_BOOT_COMPLETED` 接收器，仅当实时模式开着时启动服务（Android 15 对从 BOOT 启动的前台服务类型有限制，`specialUse` 允许；如遇限制则退化为通知提示用户打开 App）。服务负责保活，**不再自己消费事件**；后台自动发送开启时，服务同时驱动 `BackgroundReadController`（启动/停止 `/system/bin/logcat` 与监听器）。
8. **设备页 / 收件箱页 / 设置页**：对应 Windows 的同名页面（底部导航：设备、收件箱、设置）。设置含：实时模式、默认策略、有效期、历史条数、更新、关于（版本、设备 ID、指纹前 8 位）、诊断（`diagnostics.get` 可复制）。
9. **文件**：发送——分享目标 `ACTION_SEND` / `ACTION_SEND_MULTIPLE` 的 `*/*`：对每个 `Uri` 用 `ContentResolver.openFileDescriptor(uri,"r")` → `detachFd()` → `file.offer {to, fd, name, size}`（名字与大小用 `OpenableColumns` 查询）；让用户选择目标设备（只有一台时直接发）。接收——offer 通知带“接收 / 拒绝”；进度通知（可取消）；完成后用 `MediaStore.Downloads`（API 29+；API 26–28 用 `getExternalStoragePublicDirectory`）保存，重名自动加 `(1)`，成功后 `file.release`。
10. 保留并接上现有更新逻辑（`UpdateState`、`ApkInstaller`）；更新通知保持。
11. 代码混淆（R8）下 `go.**` 与 `com.nightclip.core.**` 必须保留（已有规则，保持）。

建议结构：`core/`（`CoreHost`、`CoreEvents`、`CoreApi` 类型安全封装）、`ui/`（Compose 页面 + 各自的 `ViewModel`）、`platform/`（`KeystoreKey`、`ClipboardAccess`、`Notifications`、`MediaStoreSaver`）。把“事件 → 动作”的决策（`ReceiveDecider`）、JSON → 模型的解析、文件名/大小处理、配对 link 校验写成纯 Kotlin 并用 JUnit 测试（`org.json` 在单元测试里已有真实实现依赖）。

### Vault screens (Android)

密码库页（底部导航第四个标签“密码库”，接口依据 [vault-api.md](vault-api.md)，规则 §4 全部适用）。**只编译、跑过 JVM 单测，没有在真机上运行过。** 自动填充见 §6；通行密钥不在这一阶段。

- **结构**：`core/VaultApi.kt`（`vault.*` 的类型化封装，一律挂起、不在主线程）与 `core/VaultModels.kt`（`VaultParsers`：所有结果形状的解析）；`logic/` 里是无 Android 依赖的纯逻辑（列表过滤与排序、TOTP 倒计时、强度标签、条目校验与模板、备份原因与差异分组、剪贴板清除决策、快速解锁策略、自动锁策略、错误码文案），每个都有 JUnit 测试；`platform/` 里是 `VaultSession`（后台宽限期、熄屏、`vault.locked` 事件）、`VaultClipboard`、`VaultFiles`（SAF ⇄ 缓存临时文件）、`QuickUnlock`（Keystore + `BiometricPrompt`）、`VaultPrefs`；`ui/vault/` 是 Compose 页面与三个 ViewModel（解锁与导航、浏览与编辑、备份与导入导出）。用户可见文字在 `res/values/strings_vault.xml`（页面）和 `logic/VaultTexts.kt`（逻辑层）。
- **FLAG_SECURE（默认开，可关闭）**：`SecureWindowEffect` 观察 `ShellPrefs.vaultPreventCapture`（默认 true）；开启时在“密码库”标签可见的整个期间给窗口加 `FLAG_SECURE`，离开时清除；关闭时（主设置页“密码库页面防止截屏和录屏”）不加，方便开发/测试截图。`AutofillAuthActivity`/`AutofillSaveActivity` 启动时也读这个偏好。每个 Activity 实例只管自己的窗口，旋转重建后由新的组合重新应用。
- **复制**：一律 `vault.copy` → `VaultClipboard.copy`：`ClipData` 标签固定为 `ClipBridge vault`（`ClipClearPolicy.MARKER_LABEL`），Android 13+ 带 `EXTRA_IS_SENSITIVE`；只记住文本的 SHA-256；定时器挂在 Application 作用域，界面关闭也会触发；到点（或密码库锁定时立刻）读剪贴板，`ClipClearPolicy.decide` 决定：能读且仍是那一份 → 清除；能读但已是别的内容 → 保留；读不到（无焦点时 Android 10+ 连描述也不给）→ 描述是我们的标签则清除，什么都读不到则直接清除（代价：用户在这 20 秒内在别处复制的东西也可能被清掉）。`ClipboardAccess.readClip` 对带这个标签的剪贴板返回 null，所以焦点自动发送和手动“发送剪贴板”都不会读到密码库复制的内容（核心还会以 `vault_origin` 兜底）。
- **自动锁**：应用在后台超过 30 秒（`AutoLockPolicy.BACKGROUND_GRACE_MS`；刚拉起文件选择器或生物识别提示时放宽到 5 分钟，因为那时本应用也处于“停止”状态）、熄屏（`ACTION_SCREEN_OFF`，仅在已解锁期间注册）、核心空闲超时（`vault.locked` 事件）都会锁定。锁定后三个 ViewModel 的列表、详情、草稿、搜索词、导航栈、待导出的密码全部清空，页面回到解锁界面。
- **快速解锁**（默认关，首次主密码解锁后询问，也可在“密码库设置”里开）：Keystore 的 AES-256-GCM 密钥，`setUserAuthenticationRequired(true)`、`setInvalidatedByBiometricEnrollment(true)`、仅 `BIOMETRIC_STRONG`、每次使用都要认证（API 30+ `setUserAuthenticationParameters(0, …)`，更低版本 `ValidityDuration=-1`），包装主密码（和密钥文件字节），密文放在应用私有 SharedPreferences。只有“已启用 + 距上次主密码解锁 < 72 小时 + 连续失败 < 5 次 + 开机计数未变”时才提供；主密码输错、更换主密码、关闭开关、过期、失败过多、重启都会删除密钥和密文。开机计数读 `Settings.Global.BOOT_COUNT`，读不到时跳过“重启”这一条（只剩 72 小时限制）。
- **文件**：导入/导出/附件都经应用缓存里的随机名临时文件（`vault.import_file` / `export_file` / `attach_add` / `attach_get` 的 `path`），用完立即清零并删除，锁定和启动时再清扫一遍；导出失败会删掉选择器已创建的空文件。
- **设备同步**（`ui/vault/VaultSyncScreen.kt`，入口：列表右上角“更多”、密码库设置、解锁页和新建页的“设备同步”按钮；库不存在或已锁定时也能进，`vault.sync_status` / `sync_enable` / `sync_now` 不需要解锁）：每台已配对设备一张卡片；开关 = `vault.sync_enable`，**默认开**（schema v6 会把已配对设备置开；单台关闭是高级退出），显示连接状态、上次同步时间和该状态的中文说明（`idle` / `pending_unlock` / `needs_password` / `review` / `peer_off` / `error`，文案在 `logic/VaultSyncLogic.kt`，核心的英文 `detail` 只用来区分 `review` 的原因：不是同一个库 / 一次删除 5 个以上条目）。`needs_password` 弹窗向用户要**对方设备**当前的主密码（可带密钥文件），走 `vault.sync_resolve`，密码错误时弹窗保持打开并显示错误，`RATE_LIMITED` 显示倒计时；`review` 弹窗给出原因，“批准并合并”= `sync_resolve{approve:true}`，“忽略”什么都不做（请求保留）。处理这两种状态需要库已解锁，锁定时只提示“先解锁”。事件由 `VaultSession` 统一接收：`vault.sync` 更新“需要处理”的小红点（更多菜单、设置页、同步入口）并让同步页刷新；`vault.changed`（仅在已解锁时）让列表、详情和状态重新读取；`vault.joined` 弹出提示和 Toast“已加入 <设备> 的密码库，请用那个库的主密码解锁”。同步页可见时每 8 秒再读一次状态（连接与最近同步时间不产生事件）。
- **解锁页与密钥文件**：`vault.status.key_file` 为 `true` 时强调“这个库需要密钥文件”；缺省（经同步加入、尚未在本机解锁过）时作为可选项显示；为 `false` 时折叠在“我的主密码还搭配了密钥文件…”后面（`KeyFileHint`）。
- **Activity 基类**：`MainActivity` 改为继承 `FragmentActivity`（`BiometricPrompt` 的要求，`ComponentActivity` 的子类，其余不变）。新增依赖 `androidx.biometric:biometric:1.1.0`（官方 AndroidX 库）。

## 5. 验收

- `make check` 通过（含新增的 C# / Kotlin 单元测试）；后台读取的纯逻辑（日志匹配、去抖、状态机、claim、退避）必须有 JVM 单测。
- 报告里单独列出：**无法在这台机器上验证的行为**（真实剪贴板、托盘、通知、相机、MediaStore、前台服务）和你对其正确性的依据（文档/API 的哪一条）。
- 不要宣称“已验证”未被测试或编译覆盖的东西。

## 6. Android：自动填充（Autofill）

密码库的 Android 自动填充服务（`AutofillService`，API 26+）。**只编译、跑过 JVM 单测，没有在真机上运行过。** 通行密钥（Credential Manager）不在这一阶段。依据：[vault-api.md](vault-api.md) §3.2b（`vault.match`）、§4（壳层规则）、[plan-vault.md](plan-vault.md) §7.1。

代码分布（全部新增文件，没有改 `core/` 和 `core/CoreApi.kt`）：

| 位置 | 内容 |
| --- | --- |
| `logic/autofill/` | 所有决定，纯 Kotlin、无 `android.*`：`FieldClassifier`（字段分类）、`FormAnalyzer`（分组成登录表单）、`TargetResolver`/`SchemePolicy`（网页域名或应用包名、协议规则）、`DatasetPlanner`/`AuthPolicy`（候选数据集与验证策略）、`SaveDecider`/`SaveExtractor`/`PendingSaveStore`（保存决定与内存中的待保存值）、`QuickUnlockProvider`（生物识别钩子，默认空实现）、`AutofillSetupLogic`、`VaultModels` |
| `autofill/` | Android 胶水：`VaultAutofillService`、`AssistParser`（`AssistStructure` → `NodeInfo`）、`ResponseFactory`、`FillPipeline`、`FillValues`、`AutofillVault`（`vault.*` 调用）、`AutofillAuthActivity`（解锁/确认）、`AutofillSaveActivity`、`AutofillSettingsSection`（设置卡片）、`AutofillSettingsActivity` |
| `platform/` | `AutofillPrefs`（自己的偏好文件 `clipbridge_autofill`，只有 `autofill_require_auth`）、`AutofillSetup`（`isDefaultService` / `intentToEnable` / `open`） |

**请求流程。** `onFillRequest` → 解析结构 → 分类并取焦点所在的表单 → 解析目标 → `vault.status`：没有库则什么都不给；库已锁定则返回带 `setAuthentication` 的响应（“解锁以填充”，进 `AutofillAuthActivity`）；已解锁则 `vault.match` + `SchemePolicy` 过滤，最多 5 个数据集，每个数据集的密码在构建那一刻用 `vault.reveal` 取（只取页面要的字段、只取匹配到的条目）；没有匹配时只给一行“打开 ClipBridge 密码库”。登记页（新密码字段）从不填旧密码，只声明 `SaveInfo`。

**安全规则（代码里都有对应测试）：**

- 网页只用平台报告的 `webDomain`/`webScheme`，从不读页面文字里的网址；原样传给 `vault.match`（相似域名的区分由核心负责，壳层有测试保证没有改写）。
- 原生应用用 `activityComponent` 的包名（系统给的，不是窗口标题或视图内容）。同一结构里不同的域名/协议/包名**不会合并成一个表单**，只用焦点字段自己的上下文。
- 浏览器包名且没有 `webDomain`：什么都不给（浏览器没有按包名匹配）。包名里含 `browser` 的也按浏览器处理。本应用自己的界面永远忽略。
- 协议：`https` 页面可用任意匹配条目；`http` 页面只对 `localhost`/IP 字面量，或条目自己的 URL 就是 `http://` 时填充；平台没给协议按 `http` 处理（**严格，真机上 Chrome/Firefox 是否报告 `webScheme` 要验证**）。
- 非浏览器应用里的 WebView 页面（`webDomain` 可被应用用 `loadDataWithBaseURL` 声称）：**每次**要求输入主密码。
- `autofill_require_auth`（“每次填充前验证主密码”，默认关）：开启后数据集不带值，点选后进 `AutofillAuthActivity` 验证主密码（`vault.verify_master`）再返回值。
- TOTP：数据集为空，点选时才 `vault.totp`（快过期时等下一个周期）。
- 两个界面都 `FLAG_SECURE`、`filterTouchesWhenObscured`、对自己关闭自动填充；不碰剪贴板、不用无障碍服务；日志里没有任何结构内容或值。
- 保存：`onSaveRequest` 把表单值放进 `PendingSaveStore`（只在内存、一次性、2 分钟过期），API 28+ 通过 `SaveCallback.onSuccess(IntentSender)` 打开 `AutofillSaveActivity`；界面里先解锁，再对比 `vault.match`：相同账号相同密码→忽略；相同账号不同密码→“更新现有条目”（`vault.update` 要整条回写，所以先 `vault.get` + 逐个 `reveal` 受保护字段和 TOTP 密钥，缺任何一个就拒绝，避免抹掉数据）；否则新建 `login`，网址为域名或 `androidapp://<包名>`（核心按此约定匹配应用）。

**给密码库界面的接入点：**

1. 设置页放 `AutofillSettingsSection()`（`autofill/AutofillSettingsSection.kt`，自包含：状态、“启用自动填充”按钮、验证开关），或自己调用 `AutofillSetup.isDefaultService(context)` / `AutofillSetup.open(context)`。
2. `AutofillAuthActivity.EXTRA_TAB_VAULT`（`"vault"`）作为 `Actions.EXTRA_TAB` 传给 `MainActivity`，用来从“打开 ClipBridge 密码库”回到密码库标签。
3. 生物识别快速解锁：实现 `QuickUnlockProvider`（`logic/autofill/QuickUnlock.kt`）并赋给 `QuickUnlockRegistry.provider`；`BiometricPrompt` 需要 `FragmentActivity`，届时把 `AutofillAuthActivity` 的基类改成 `FragmentActivity`。策略见 `AutofillQuickUnlockPolicy`（72 小时、重启、5 次失败）。
4. 后台自动锁：用户在自动填充里解锁后应用并不在前台，注意别让“进入后台即锁”的逻辑把刚解锁的库立刻锁掉（遵守 vault-api.md §4 第 4 条的宽限时间）。

不做：内联建议（API 30+ 键盘栏，只用系统下拉）、无障碍填充、兼容模式（见用户文档）、通行密钥。

## 7. Windows：密码库页面（`vault.*`，接口见 [vault-api.md](vault-api.md)）

文件：`windows/ClipBridge.Windows/` 下的 `Views/Vault*.xaml(.cs)`、`Views/EntryDetailPane.cs`、`Views/EntryEditorWindow.*`、`Views/PasswordGeneratorWindow.*`、`Views/VaultDialogs.cs`、`Views/VaultBackupDialogs.cs`、`Views/VaultControls.cs`（`SecretBox`、`CredsBox`、`StrengthBar`、`TotpRing`）、`Services/ShellController.Vault.cs`、`Services/DisplayAffinity.cs`；纯逻辑（无 WPF 依赖，有单元测试）在 `Logic/Vault*.cs`、`Logic/EntryDraft.cs`。

结构与规则：

- **所有决定放在 `Logic/`**：`VaultApi`（带类型的 `vault.*` 封装，`ICoreCaller` 可替换成假的）、`EntryDraft` / `EntryValidator`（和核心 `validate()` 一致的客户端校验：标题必填、单项 256 KiB（按 UTF-8 字节）、字段名保留字/重复/128 字节、最多 100 个字段和标签）、`KindTemplate` / `FormVisibility`（四种类型的表单）、`QuickFilters` / `GroupTree`（筛选与分组，分组在客户端过滤并包含子分组）、`DetailRows` / `RevealState`（遮盖与 15 秒自动再遮盖）、`TotpCountdown`（倒计时与圆环）、`ClipboardClearPolicy` / `ClipboardClearGuard`（“序号没变才清除”）、`LockPolicy`、`RateLimitCountdown`、`RestoreSelection`、`HealthView`、`ErrorMessages.ForVault`。视图只做绑定。
- **线程**：所有核心调用经 `ShellController.VaultCallAsync`（`Task.Run`，不阻塞 UI）。结果是 `VaultResult<T>`：成功值，或错误码 + 友好文本 + `RATE_LIMITED` 的等待秒数（`CoreException.RetryAfterSeconds` 取自 `error.details.retry_after_s`）。日志只记错误码，**从不**记参数或值。
- **剪贴板（§4 规则 1）**：`CopyVaultSecretAsync` = `vault.copy` → 在剪贴板工作线程里用 `ClipboardAccess.WriteText(owner, text, sensitive: true)`：同一次打开剪贴板的会话里写入文本和三个格式 `ExcludeClipboardContentFromMonitorProcessing`、`CanIncludeInClipboardHistory=0`、`CanUploadToCloudClipboard=0`，任何一个失败就清空并报错（机密不会无保护地留在剪贴板）→ 写完立刻记下 `GetClipboardSequenceNumber` 既作为 `_ownWriteSeq`（`ClipboardMonitor` 不会把它当成用户复制），也交给 `ClipboardClearGuard`。`clear_after_s` 秒后在剪贴板线程里比较序号：没变才 `EmptyClipboard`；变了、未知（0）或设置为 0 都不动。锁定时（`vault.locked` 事件、托盘、会话事件、退出）同样按“序号没变才清”处理。
- **屏幕（规则 2）**：`DisplayAffinity.Set` 用 `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE=0x11)`，失败回退 `WDA_MONITOR`；只在密码库页显示期间设置（`MainWindow` 切页时调用 `VaultPage.OnShown/OnHidden`）；开关 `ShellPrefs.PreventCapture`（`shell.json` 的 `prevent_capture`，默认开）；所有密码库对话框用 `ProtectDialog`。
- **锁定（规则 4）**：`SystemEvents.SessionSwitch`（锁屏、注销、切换用户、远程断开）和 `PowerModeChanged`（挂起）→ 在事件线程里**同步**调用 `vault.lock`（不能等 UI 线程，挂起不会等）；核心发出 `vault.locked` 事件（`CoreEventParser` → `VaultLockedEvent`）→ `ShellController` 触发 `VaultLocked` → `VaultPage` 关闭所有已登记的密码库对话框（`VaultUi.Track`/`CloseAll`）、清空列表/详情/搜索框/密码框、显示解锁页。
- **空闲计时的两个细节**：核心的空闲计时只被 `vault.*` 调用（`status` 除外）重置。① 详情里的 TOTP 每个周期调用一次 `vault.totp`，所以用户一段时间没有操作后（`TotpActivityPolicy`）就暂停刷新，否则永远不会自动锁定；② 反过来，用户在编辑器里打很长的字时核心看不到“操作”，所以 `IdleKeepAlive` 在有新的键盘/鼠标输入时每隔 autolock/3 秒（10–120 秒）调用一次 `vault.groups` 作为心跳；没有新输入就不发。
- **没有做**：Windows 快速解锁（规则 6，按文档“暂不提供”）。
- **测试**：`Vault*Tests.cs`（逻辑）+ `VaultInteropTests.cs`（用真实核心走完创建→增删改→锁定→限速→备份→恢复→导出导入→改主密码，并在有 `keepassxc-cli` 时交叉检查导出文件）。注意：原生核心在一个进程里只能启动一次，所有用到它的测试共享 `NativeCoreFixture`，测试串行，并关闭并发 GC（在 Linux 上并发 GC 线程和 Go 核心的信号处理会让测试宿主偶尔崩溃）。
- **无法在这台机器上验证**：真实的剪贴板标志与清除、`SetWindowDisplayAffinity` 的实际效果、`SystemEvents` 的触发、托盘菜单、所有窗口的布局与交互。
