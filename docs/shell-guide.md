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
5. **发送入口**：(a) 主界面“发送剪贴板”按钮（前台读取剪贴板）；(b) 分享目标：`ACTION_SEND text/plain` → `clip.send`；(c) 常驻通知上的“发送剪贴板”动作与快捷设置磁贴（`TileService` → 透明 Activity 获得焦点后读取剪贴板，读完立即 `finish`）；(d) 手动输入框发送。读取剪贴板只发生在用户可见的前台时刻（Android 10+ 限制）。
6. **接收**：通知渠道“收到的剪贴板”；通知正文预览（敏感则“敏感内容”）；动作“复制”（经一个透明 Activity 在前台写剪贴板，写完 `clip.mark_applied` 并 `finish`）和“忽略”（`clip.dismiss`）。`decision == auto`：App 在前台时直接写入并 `mark_applied`；在后台时降级为通知。
7. **实时模式**：设置里的开关，**持久化**；开启时启动前台服务（`specialUse`，常驻通知“已连接 N 台设备”，带“断开”动作）。开机自启：`RECEIVE_BOOT_COMPLETED` 接收器，仅当实时模式开着时启动服务（Android 15 对从 BOOT 启动的前台服务类型有限制，`specialUse` 允许；如遇限制则退化为通知提示用户打开 App）。服务只负责保活，**不再自己消费事件**。
8. **设备页 / 收件箱页 / 设置页**：对应 Windows 的同名页面（底部导航：设备、收件箱、设置）。设置含：实时模式、默认策略、有效期、历史条数、更新、关于（版本、设备 ID、指纹前 8 位）、诊断（`diagnostics.get` 可复制）。
9. **文件**：发送——分享目标 `ACTION_SEND` / `ACTION_SEND_MULTIPLE` 的 `*/*`：对每个 `Uri` 用 `ContentResolver.openFileDescriptor(uri,"r")` → `detachFd()` → `file.offer {to, fd, name, size}`（名字与大小用 `OpenableColumns` 查询）；让用户选择目标设备（只有一台时直接发）。接收——offer 通知带“接收 / 拒绝”；进度通知（可取消）；完成后用 `MediaStore.Downloads`（API 29+；API 26–28 用 `getExternalStoragePublicDirectory`）保存，重名自动加 `(1)`，成功后 `file.release`。
10. 保留并接上现有更新逻辑（`UpdateState`、`ApkInstaller`）；更新通知保持。
11. 代码混淆（R8）下 `go.**` 与 `com.nightclip.core.**` 必须保留（已有规则，保持）。

建议结构：`core/`（`CoreHost`、`CoreEvents`、`CoreApi` 类型安全封装）、`ui/`（Compose 页面 + 各自的 `ViewModel`）、`platform/`（`KeystoreKey`、`ClipboardAccess`、`Notifications`、`MediaStoreSaver`）。把“事件 → 动作”的决策（`ReceiveDecider`）、JSON → 模型的解析、文件名/大小处理、配对 link 校验写成纯 Kotlin 并用 JUnit 测试（`org.json` 在单元测试里已有真实实现依赖）。

## 5. 验收

- `make check` 通过（含新增的 C# / Kotlin 单元测试）。
- 报告里单独列出：**无法在这台机器上验证的行为**（真实剪贴板、托盘、通知、相机、MediaStore、前台服务）和你对其正确性的依据（文档/API 的哪一条）。
- 不要宣称“已验证”未被测试或编译覆盖的东西。
