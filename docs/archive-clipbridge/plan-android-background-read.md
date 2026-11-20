# 计划与实施记录：Android 后台读取剪贴板（悬浮窗取焦 + READ_LOGS）

> 状态：**壳层已实现并编译/单测通过（2026-10-26）；2026-10-27 在 Android 15 模拟器上实测，发现 READ_LOGS 方案在 Android 13+ 有结构性问题，见 §7。真机验证未完成。**
> 依据：[ADR-006](adr/006-android-background-read.md)、[plan-auto-broadcast.md](plan-auto-broadcast.md) §4.4/§5。
> 范围：只做“后台读取本机剪贴板 → 自动发送”。接收方向的后台自动写入不在本次范围内。

## 1. 结论

- Android 10+ 后台**监听器不会回调**，但系统会为每个已注册监听器写一条 `ClipboardService` 的 E 级拒绝日志；`READ_LOGS` + `logcat` 可以据此发现“用户复制了”。
- 真正读取仍需焦点：1×1 透明 `TYPE_APPLICATION_OVERLAY` 窗口取焦后读 `primaryClip`，读完立刻移除。这是 ClipCascade 的做法。
- 功能默认关闭；需要用户一次性在系统设置授予悬浮窗，并用电脑执行一次 `adb shell pm grant … READ_LOGS`。授权在更新/重启后保留，卸载重装后丢失。
- 没有权限时只回退到原来的“打开应用时自动发送”，不会假装工作。

## 2. 为什么不是只做监听器

AOSP Android 10–15 的 `ClipboardService.sendClipChangedBroadcast` 在分发前对每个监听器调用 `clipboardAccessAllowed(OP_READ_CLIPBOARD, …)`。后台应用不满足焦点/IME/系统应用条件时：

1. 回调不会到达；
2. 该函数会 `Slog.e("ClipboardService", "Denying clipboard access to <pkg>…")`。

所以**注册一个监听器（回调通常不到达，仅作少数 OEM 的兜底）**是检测手段，`logcat` 是事件源，悬浮窗只负责实际读取。只做“前台服务 + 监听器 + 悬浮窗”在 Android 10+ 不会触发。

## 3. 用户流程与授权

设置 → “后台自动发送（进阶）”：

1. 若实时模式未开，先确认一起开启（前台服务负责保活）。
2. 悬浮窗：状态“已授予/未授予”，按钮打开系统设置。
3. `READ_LOGS`：显示带真实包名的命令，可一键复制：

   ```sh
   adb shell pm grant com.nightclip.app android.permission.READ_LOGS
   adb shell am force-stop com.nightclip.app
   ```

   说明：需要电脑一次；授权后重新打开应用；卸载重装后重做。
4. 两个权限都授予且服务运行 → “运行中”。在别的应用复制文本，约 1 秒内发送到所有已配对设备。
5. 关闭开关立即停止 `logcat` 和监听器；关闭实时模式时后台自动发送暂停（开关意图保留）。

没有电脑时不要开启；原来的“打开应用时检查一次并发送”仍然有效。

## 4. 实现

| 文件 | 职责 |
| --- | --- |
| `android/app/src/main/java/com/nightclip/app/logic/BackgroundReadSetup.kt` | 纯状态机：是否可运行、缺哪一步、ADB 命令文本 |
| `.../logic/ClipboardLogTrigger.kt` | 纯日志行匹配（Android 10/11+ 措辞）与 800ms 去抖 |
| `.../logic/ClipSeen.kt` | 已见哈希 compare-and-set 的纯实现/测试基准 |
| `.../logic/LogcatBackoff.kt` | logcat 重启退避：1/2/5/10/30 秒，5 次后停止 |
| `.../platform/ClipWriteGuard.kt` | 应用自己写剪贴板后的 2 秒抑制窗 + 记录哈希 |
| `.../platform/ClipboardOverlayReader.kt` | 1×1 透明悬浮窗取焦、读取、立即移除；串行化 |
| `.../platform/LogcatClipboardWatcher.kt` | 注册监听器（回调通常不到达，仅作兜底）、过滤 `ClipboardService:E`、stderr/退避/清理 |
| `.../platform/BackgroundReadController.kt` | 状态、权限、会话计数、暂停/无对端/锁屏/自写检查、退避 |
| `.../platform/AutoSend.kt` | 第 1 层与后台层共用发送流程（claim → `clip.local_changed`） |
| `.../platform/ShellPrefs.kt` | `background_read_enabled`（默认 false）、原子 claim/rollback |
| `.../SyncForegroundService.kt` | 服务启停驱动控制器 |
| `.../MainActivity.kt` | `onResume` 重新检查权限 |
| `.../ui/SettingsScreen.kt`、`SettingsViewModel.kt`、`res/values/strings.xml` | 设置卡片、授权向导、状态/计数、测试读取 |
| `AndroidManifest.xml` | `READ_LOGS`（`tools:ignore`）、`SYSTEM_ALERT_WINDOW` |

**未改**：Go 核心、协议、数据库 schema、Windows 壳、第 1 层焦点读取的行为。

### 4.1 运行时保护

- 只在 `enabled && realtime && overlayGranted && logsGranted && serviceRunning` 时启动 watcher。
- 触发后依次检查：自写 2 秒窗 → `capture` 开 → 未暂停 → 至少有一个已配对设备 → 服务仍运行。
- 锁屏/安全窗口/游戏取焦失败：不发送、不崩溃；连续 3 次失败后退避 60 秒。
- 应用自己写剪贴板（通知“复制”、诊断复制、ADB 命令复制）：记录哈希 + 2 秒窗，避免回读再发。
- 第 1 层与后台层共用一个“已见哈希”原子占位；失败只 compare-and-set 回滚，不覆盖后来者。
- 日志/诊断只保留次数和错误码，不保留剪贴板正文或 `logcat` 原文。

### 4.2 已知限制

- 800ms 内连续复制只处理最后一条（系统剪贴板本来也只保留最后一条）。
- 首次观察只记基线，不发送（与第 1 层一致）。
- Android 12+ 每次读取显示系统“已粘贴”提示，无法关闭。
- 取焦可能短暂影响输入法/游戏；`FLAG_ALT_FOCUSABLE_IM` 只是降低概率。
- 厂商 ROM 可能改日志标签、限制悬浮窗或杀后台；失败时回退第 1 层。
- Android 8/9 不提供该功能（没有 Android 10+ 的拒绝日志信号）。
- `READ_LOGS` 与 `SYSTEM_ALERT_WINDOW` 不适合 Google Play 普通应用；当前只适用于侧载分发。

## 5. 验证

### 5.1 已通过（本机）

- `make apk`（AAR + `:app:testDebugUnitTest` + `:app:assembleDebug`）通过；Android JVM 单测共 132 个，0 失败。
- 新增 `BackgroundReadSetupTest`(6)、`ClipboardLogTriggerTest`(3)、`ClipSeenTest`(3)、`ClipWriteGuardTest`(2)、`LogcatBackoffTest`(2)，全部通过。
- `:app:lintDebug`、`:app:assembleRelease`（R8）通过；`aapt2 dump permissions` 确认 APK 含 `READ_LOGS`、`SYSTEM_ALERT_WINDOW`。
- **模拟器尝试（2026-10-26，未完成）**：在 Android 15 x86_64 模拟器安装 debug APK，`pm grant READ_LOGS`、`appops SYSTEM_ALERT_WINDOW allow` 均成功；开启实时模式后观察到本应用的 `logcat` 子进程启动（watcher 已拉起）。但该模拟器上 Go 核心的启动线程以 `SIGSYS (SYS_SECCOMP)` 崩溃，UI 无法进入设置，系统 UI/Launcher 也出现 ANR，因此**没有完成“复制→拒绝日志→悬浮窗读取→发送”的端到端验证**。这是核心/模拟器环境问题，不是本次壳层代码的结论；真机仍是放行条件。

### 5.2 真机验证清单（未完成）

| 场景 | 期望 |
| --- | --- |
| 权限齐全、实时模式开、应用后台，在别的应用复制 | 对端 ≤2 秒收到；再打开 ClipBridge 不重复 |
| 首次开启后第一次复制 | 只记基线，不发送 |
| 快速连续复制 | 只处理最后一条 |
| 暂停 / `capture=off` / 无已配对设备 | 不取焦、不读取、不发送 |
| 锁屏 / 安全窗口 / 游戏 | 不崩溃；失败退避 |
| 应用自己写剪贴板（通知复制/诊断复制） | 不回传 |
| 关闭开关 / 关闭实时模式 / 撤销权限 | `logcat` 进程与监听器清理干净 |
| Android 10/11/12/13/14/15 + 一个 OEM | 记录触发/读取/发送计数；失败机型有回退说明 |
| 键盘打开时复制 | 记录键盘是否被收起/输入是否中断 |

授权与检查：

```sh
adb shell pm grant com.nightclip.app android.permission.READ_LOGS
adb shell appops set com.nightclip.app SYSTEM_ALERT_WINDOW allow   # 也可在设置里开
adb shell am force-stop com.nightclip.app
adb logcat -s ClipboardService:E
adb logcat -s ClipBridge:I
```

## 6. 未决

- 没有 `READ_LOGS` 时的无 ADB 备选（定时轮询 / Shizuku）未做。
- 接收方向的后台自动写入未做；AOSP 允许无焦点 `setPrimaryClip`，但需要单独真机验证和自写保护。
- 真机结果出来后再调 800ms 去抖、250ms+250ms 取焦超时、60 秒退避等参数。
- **已知问题（暂不处理，只记录）：后台读取不检查“敏感”标记。** Android 13+ 的密码管理器复制时会在 `ClipDescription` 上设置 `EXTRA_IS_SENSITIVE`，`ClipboardAccess.readClip` 读取时没有检查这个标记（只排除了本应用密码库的 `VaultClipboard.MARKER_LABEL`）。因此后台自动发送会把密码管理器复制的内容当作普通文本发给所有已配对设备（核心的文本启发式只能拦住一部分）。修法方向：`readClip` 遇到 `EXTRA_IS_SENSITIVE` 时返回 null 或带标记交给核心按 `sensitive` 处理。

## 7. 实测发现（2026-10-27，Android 15 x86_64 模拟器，分支 `fix/silent-copy`）

环境：模拟器无 KVM（软件模拟，很慢），用 `tools/android-x86_64-emulator/` 构建、`tools/clipboard-probes/` 做“别的应用复制”。

### 7.1 Go 核心在模拟器上 `SIGSYS`：只与 x86_64 有关
`Fatal signal 31 (SIGSYS), code 1 (SYS_SECCOMP), syscall 6`，线程 `clipbridge-start`。x86_64 的 6 号调用是 `lstat`：`modernc.org/libc` 直接发 x86 才有的老式调用，Android 对应用的 seccomp 只放行 `*at` 系列。arm64 没有这些调用，真机不受影响（用户确认真机能运行）。模拟器测试用构建期补丁绕过，产品构建不变。

### 7.2 READ_LOGS 方案：检测前提成立，但 Android 13+ 的日志访问确认让它不可靠
- 成立：后台复制时系统确实写 `ClipboardService: Denying clipboard access to com.nightclip.app, application is not in focus nor is it a system service`；一旦应用的 `logcat` 连接获准，匹配到触发 <150 ms，之后 6 分钟内持续有效。
- 不成立的部分：Android 13+ 的 `LogcatManagerService` 对**每一次** `logcat` 连接要求用户确认。应用在前台启动 `logcat` 时弹出 “Allow ClipBridge to access all device logs?”，只有 “Allow one-time access / Don’t allow”。
- 应用在后台重连（`logcat` 进程被杀、应用重启、开机恢复）时**不弹窗，且连接被静默拒绝**：系统仍写拒绝日志，但应用收不到任何行（实测 0 次匹配）。
- 现有监视器在这种情况下仍显示“运行中”（它只看到进程在、没有 EOF），实际已经“聋”了——这是一个独立的缺陷：状态不能反映真实可用性。
- 因此该方案在 Android 13+ 只能在“用户每次把应用切到前台并点一次允许”之后工作到下次重连；无法做到“静默、持续”。

### 7.3 shell 身份的辅助进程可以做到（已用探针验证）
以 shell 用户（`app_process`）运行的进程，在后台、无焦点时：
- 能写剪贴板（`SetClip`）、能读剪贴板（`GetClip` 读到了刚复制的文本）；
- 轮询（`WatchClip`，300 ms）每次后台复制都能发现并读到内容；
- 不需要 `READ_LOGS`、不需要悬浮窗、不抢焦点、不弹日志访问确认。
没有验证的：监听回调（`addPrimaryClipChangedListener`）版本的探针在模拟器里被系统杀掉，原因没查明，所以目前只验证了轮询；Android 12+ 对 shell 读取是否显示“已粘贴”提示未检查；真机和其它版本未验证。

要让 shell 进程长期运行，需要用户授权一次（USB/无线 adb，或 Android 11+ 设备上的“无线调试”自配对，或 Shizuku）；重启后需要重新拉起。这比 READ_LOGS 更直接，但需要决定实现方式（见分支讨论）。
