# ADR-006：Android 后台剪贴板读取采用“READ_LOGS + 悬浮窗取焦”（可选进阶）

- 状态：**Accepted（2026-10-26，壳层已实现；真机验证待完成）**
- 日期：2026-10-26
- 决策者：项目组（用户明确要求按 ClipCascade 的做法实现）
- 相关文档：[plan-android-background-read.md](../plan-android-background-read.md)、[plan-auto-broadcast.md](../plan-auto-broadcast.md)、[user-guide.md](../user-guide.md)、[shell-guide.md](../shell-guide.md)
- 取代：ADR-002 中“Android 不提供后台读取”的绝对表述；ADR-002 的“用户触发、默认不覆盖”原则仍然有效

## 背景

Android 10 起，只有当前有焦点的应用或默认输入法能读取剪贴板。应用在后台时：

- `ClipboardManager.getPrimaryClip` 被拒绝；
- `OnPrimaryClipChangedListener` **不会**收到回调（`ClipboardService.sendClipChangedBroadcast` 在分发前做焦点检查）。

因此仅靠前台服务 + 监听器无法实现“在别的应用复制后自动发送”。用户要求实现 ClipCascade 的进阶方案：一次性授权 `READ_LOGS`，从系统日志发现复制事件，再用悬浮窗短暂取得焦点读取内容。

## 决策

提供一个**默认关闭、显式开启**的“后台自动发送（进阶）”功能：

1. **检测**：在实时模式的前台服务里注册一个 `OnPrimaryClipChangedListener`（回调通常不到达，仅作少数 OEM 的兜底触发）。Android 10–15 的 `ClipboardService` 在每次 `setPrimaryClip` 时会遍历监听器并调用 `clipboardAccessAllowed(OP_READ_CLIPBOARD, …)`；后台应用不满足焦点条件时，系统会写一条 E 级日志：
   `ClipboardService: Denying clipboard access to <pkg>, application is not in focus nor is it a system service for user 0`
   （Android 10 是 “neither”）。监听器回调本身不会到达，**注册它就是为了让系统写这条日志**。
2. **日志读取**：前台服务启动 `logcat` 子进程，参数固定为 `-b main,system -T 1 ClipboardService:E *:S`，只匹配包含本包名和 `Denying clipboard access` 的行。需要用户用电脑执行一次：
   `adb shell pm grant com.nightclip.app android.permission.READ_LOGS`
   `READ_LOGS` 的 protectionLevel 是 `signature|privileged|development`，AOSP 的 `PermissionManagerServiceImpl` 允许 `isDevelopment()` 的权限通过 `pm grant` 授予。授权在更新/重启后保留，卸载重装后丢失。
3. **读取**：检测到复制后，添加一个 1×1 透明、`TYPE_APPLICATION_OVERLAY`、可聚焦的窗口；窗口取得输入焦点后调用 `getPrimaryClip`，随后立刻移除，把焦点还给用户原来的应用。这是 ClipCascade 的 `ClipboardFloatingActivity` 做法。
4. **发送**：读取到的文本走现有 `ClipAutoSend.decide` + `clip.local_changed`；与“打开应用时自动发送”的第 1 层共用原子“已见哈希”，两条路径不会重复发送。核心继续负责 `capture`、暂停、回声、去重、大小和敏感内容规则。
5. **开关条件**：只有“用户开启 + 实时模式开 + 悬浮窗已授权 + READ_LOGS 已授权 + 前台服务运行”时才启动监听；否则显示明确的状态和缺失步骤，不静默降级。

## 替代方案

| 方案 | 结论 | 原因 |
| --- | --- | --- |
| 定时轮询 + 悬浮窗 | 不采用（本次） | 每次轮询都要抢焦点，打断输入法/游戏；后续可作为无 ADB 的备选 |
| Shizuku | 不采用（本次） | 需要另装并保持 Shizuku 运行；可作为后续高级选项 |
| 无障碍服务 | 不采用 | 权限过大、旁载应用在 Android 13+ 还需要解除受限设置，与现有隐私叙事冲突 |
| 自带输入法 | 不采用 | 要求用户换键盘，易用性最差 |
| root / `READ_CLIPBOARD_IN_BACKGROUND` | 不采用 | 该权限只给特权系统应用；普通应用拿不到 |
| 后台直接写剪贴板 | 本次不做 | AOSP 允许 `OP_WRITE_CLIPBOARD` 无焦点写入，但属于接收方向的独立改动；见下文“未决” |

## 后果

**正面**

- 真正实现“在别的应用复制，几秒内同步到对端”，不需要打开本应用。
- 事件驱动、无轮询；只有复制发生时才会短暂出现 1×1 窗口。
- 失败方向安全：没有权限/取焦失败时只回退到第 1 层，不伪造成功。
- 所有策略仍在核心里，协议、数据库 schema、Windows 壳不变。

**负面/成本**

- `READ_LOGS` 必须用电脑执行一次 ADB；没有电脑就无法使用。卸载重装后需要重做。
- 悬浮窗取焦会短暂打断输入焦点；键盘/游戏可能受影响。Android 12+ 还会显示系统“已粘贴”提示。
- 不同厂商 ROM 可能限制悬浮窗、后台服务或日志；无法保证所有机型可用。
- `READ_LOGS` 和 `SYSTEM_ALERT_WINDOW` 都不适合 Google Play 普通应用；本功能只适用于当前侧载分发。若上架 Play，必须移除或做独立变体。
- 恶意应用理论上可以伪造日志行触发一次取焦；用去抖、退避和“不读取日志正文”降低影响。

## 验证

- 单元测试：日志行匹配（Android 10/11+ 措辞）、去抖、状态机、已见哈希的 compare-and-set、自写抑制、退避。
- 构建：`make apk` + JVM 单测通过；APK 包含两条权限。
- 真机（待完成）：至少一台 Android 11+，覆盖 Android 10/12/14/15 与一个 OEM；验证后台复制 → 对端收到、无重复、取焦时长/键盘影响、权限撤销/重启、`logcat` 进程清理。
- 真机结果记录在 [plan-android-background-read.md](../plan-android-background-read.md) 的验证表里；未验证前不得对外宣称已可用。

## 未决

- 是否增加无 ADB 的定时轮询/Shizuku 备选。
- 是否单独实现“接收方向后台自动写入”（AOSP 已允许无焦点写，需真机验证与自写保护）。
