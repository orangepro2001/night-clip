# 计划：集成密码/凭证管理（Vault）

> 状态：**计划，未实施**（2026-10-26）  
> 目标：在 ClipBridge 内集成一个类似 KeePassXC 的本地加密密码库，覆盖安全、备份/恢复、多设备同步和自动填充，不另开应用。  
> 前提不变：Windows + Android，同一 tailnet，无服务端（[ADR-005](adr/005-go-core-and-tailnet-p2p.md)）。  
> 标注约定：**[已查证]** = 读了本仓库代码、官方页面或实测；**[待验证]** = 凭记忆或推断，必须在 PoC 或评审里确认。  
> 与 [plan-auto-broadcast.md](plan-auto-broadcast.md) 的关系：两者有一个必须同时处理的交叉点（§6.4：密码库复制出来的内容绝不能被剪贴板自动广播）。

## 1. 结论

1. **可行，但这是一个比现有全部功能都大的工程，也是风险最高的一块。** 现有基础设施能复用不少（配对、mTLS 链路、文件传输、更新、两个壳），但密码库对正确性和安全的要求远高于剪贴板同步。粗估完整形态 **15–20 周**（估计，不是承诺），必须分阶段、每阶段有放行条件。
2. **推荐把 KDBX 4（KeePass 数据库格式）作为唯一的数据格式。** 不自创格式和加密方案。好处：库文件可以直接用 KeePassXC / KeePassDX 等打开，"退出成本为零"本身就是最强的备份与可信度；社区审查过的格式；备份就是复制文件。Go 里有 MIT 许可的库 [gokeepasslib](https://github.com/tobischo/gokeepasslib)（v3.7.1，2026-10-23 发布，仍在维护）[已查证]。
3. **推荐集成，但作为边界清晰的独立模块**：独立的包 `core/vault`、独立的数据文件、独立的门面命名空间 `vault.*`、默认关闭、独立的界面入口，且**密码库永不经过命名管道和命令行**。集成的理由是：配对与同步通道现成、剪贴板敏感内容联动、一个应用一套更新。集成的代价是：攻击面和保证等级要求都提高了，见 §9。
4. **备选"方案 0"：不写代码**，直接用 KeePassXC（Windows）+ KeePassDX / Keepass2Android（Android），用本应用的文件传输把 `.kdbx` 在设备间发来发去。这能立刻得到一套成熟、完整的方案，缺点是没有自动合并（两边都改会产生冲突副本）、没有统一配对和统一自动填充体验。**本计划的价值在于把这些缺点补上，如果你更看重稳妥，方案 0 是合理的底线。**
5. **建议的放行顺序**：V0（PoC + ADR）→ V1（本地密码库 + 备份/恢复）→ V2（多设备同步）→ V3（Android 自动填充）→ V4（Windows 自动输入 + 浏览器）→ V5（通行密钥，可能不做）。**V1 不含同步**，因为 V1 单独就已经有完整价值，也让我们先在真机上验证安全基础，再引入"一台设备被攻破等于全部泄露"的同步风险。

## 2. 现状（均为 [已查证]，来自代码）

| 项 | 现状 | 对密码库的含义 |
| --- | --- | --- |
| 数据库加密 | `core/store`：SQLite（`modernc.org/sqlite`，纯 Go），敏感列 AES-256-GCM，密钥由壳提供（Windows DPAPI / Android Keystore）。DPAPI 使用**固定**应用熵，只防误用，不防同用户其它进程（`DpapiProtector.cs`、HANDOFF §5.7） | **不能**拿这把"设备密钥"保护密码库：同用户恶意进程能解开它。密码库必须有独立的主密钥（用户输入的主密码，可加密钥文件），设备级保护只用来做"快速解锁"的包装 |
| Android Keystore | 现有密钥为普通 AES-GCM 密钥，**没有**绑定生物识别（`KeystoreCipher.kt`） | 快速解锁需要新建一把"需要用户认证"的密钥 |
| 生物识别 / 防截屏 | 代码里**没有** `BiometricPrompt`、`FLAG_SECURE` | 都要新增 |
| 剪贴板敏感标记 | Android 写入已带 `EXTRA_IS_SENSITIVE`；Windows 已注册 `ExcludeClipboardContentFromMonitorProcessing`（读取侧忽略），但**没有**用于写入的排除/不进历史/不上云标记 | 密码库复制需要补齐写入侧标记 + 自动清除 |
| 事件通道 | 事件种类只有 `clipboard`/`file`/`control`；事件有 TTL（最长 7 天），发件箱按链路序号补发；帧是 WebSocket JSON（`core/peer/frames.go`） | 密码库同步是"状态一致性"问题，不是"一次性事件"：不能走带 TTL 的发件箱，需要新的同步通道（§5） |
| 文件传输 | `xfer`：HTTP Range 续传、SHA-256、监听方服务字节 | 可复用来搬运整库密文 |
| 命名管道 | 白名单 `Allowed`，测试里列出绝不能开放的方法 | 新增测试：所有 `vault.*` 永不在白名单里 |
| 更新 | 自托管签名清单；发布密钥与 Android 签名密钥在开发机 `~/.config/clipbridge/`（HANDOFF §8） | 最大的系统性风险，见 §9 R1 |
| 云备份 | Android `allowBackup="false"` | 保持；密码库明确排除在系统云备份之外 |
| 真机验证 | **两端壳都还没在真机上跑过**（HANDOFF §4） | 密码库不能先于真机验证放行，见 §10 |
| 仓库许可 | 仓库里**没有** LICENSE 文件 | KeePassXC 是 GPL-2/3：**不得**拷贝或移植其代码，只参考公开的格式规范和协议文档；开放仓库前需要选定许可 |

## 3. KeePassXC 对标与取舍

[已查证] KeePassXC：GPL-2/3，Windows/macOS/Linux；功能含 TOTP、浏览器集成（含通行密钥）、Auto-Type、SSH Agent、YubiKey/OnlyKey 挑战应答、Secret Service、KDBX3/KDBX4、密码健康/HIBP 报告。

| 功能 | 本计划 | 阶段 | 说明 |
| --- | --- | --- | --- |
| 条目：标题、用户名、密码、URL、备注、标签、自定义字段、有效期、图标 | 做 | V1 | KDBX 原生字段 |
| 分组、收藏、搜索、回收站 | 做 | V1 | |
| 条目历史与恢复 | 做 | V1 | KDBX 原生 `History` |
| 附件 | 做（有大小上限，如 5 MiB/个） | V1 | 防止库文件膨胀影响同步 |
| TOTP（RFC 6238） | 做 | V1 | `crypto/hmac` + SHA-1/256/512 即可，兼容 KeePassXC 的 `otp` 字段 |
| 密码生成器（随机/口令短语） | 做 | V1 | `crypto/rand`；口令词表用 EFF 长词表（许可 CC-BY，需署名）[待核对] |
| 强度提示、重复/弱/过期密码报告 | 做（本地计算） | V1/V2 | |
| 泄露检查（HIBP） | **默认关**，需要显式开启 | V2+ | 需要联网，与"仅 tailnet"相冲突；只用 k-匿名前缀接口，不发送完整哈希 |
| 密钥文件 | 做 | V1 | KDBX 复合密钥 |
| 导入：KDBX、KeePass XML、浏览器/常见管理器 CSV；导出：KDBX、CSV（强警告） | 做 | V1 | |
| 同步多设备 | 做 | V2 | §5 |
| Android 自动填充 | 做 | V3 | §7.1 |
| Windows Auto-Type（全局热键输入） | 做 | V4 | §7.2 |
| 浏览器扩展 | 评估 | V4 | §7.2 |
| 通行密钥（Passkeys） | 评估，可能不做 | V5 | §7.3 |
| SSH Agent、YubiKey 挑战应答、Secret Service | **不做** | — | 超出范围 |
| 紧急访问、共享库、团队权限 | **不做** | — | 单用户产品 |

## 4. 数据格式与加密

### 4.1 为什么选 KDBX 4 而不是自创格式

| 方案 | 优点 | 缺点 | 判断 |
| --- | --- | --- | --- |
| **A. KDBX 4 文件即库**（推荐） | 现成格式、社区审查过；用 KeePassXC/KeePassDX 可直接打开（退出成本为零）；备份 = 复制文件；Go 有 MIT 库 | 每次保存重写整个文件；整库解密常驻内存；同步是整库合并而非逐条 | **采用** |
| B. 自有逐条加密记录（SQLite 行 + 随机库密钥 VK，主密码只包装 VK） | 逐条同步、改主密码不用重新加密、各设备主密码可不同 | 要自己设计并长期维护加密格式；需要自己写 KDBX 导入导出；任何设计错误都是密码库级别的事故 | 不采用；若 V2 实测整库同步体验差再重评 |

KDBX 4 要点 [已查证：[KeePass KDBX 4 说明](https://keepass.info/help/kb/kdbx_4.html)]：密钥派生 AES-KDF 或 Argon2d（页面只写了 Argon2d）；ChaCha20 可选；头部先 SHA-256 再 HMAC-SHA-256 认证；负载按 1 MiB 分块，每块 HMAC（先加密后认证）；条目和分组有 `CustomData`。

参数建议：Argon2d，目标单次解锁约 1 秒，内存默认 64 MiB（Android 低端机上限 32–64 MiB，**[待验证]** 真机实测后定）；加密算法 ChaCha20 或 AES-256（跟随 KeePassXC 默认，保证互通）。Argon2id 是否被 KeePassXC/KeePassDX 读取为 **[待验证]**，在 V0 里用真实文件交叉测试后再决定是否开放。

### 4.2 库选型风险与 V0 验证

[已查证] gokeepasslib README：写入默认 KDBX 3.1，可选 4.0/4.1；使用前后需调用 `UnlockProtectedEntries`/`LockProtectedEntries`；依赖 `tobischo/argon2`、`x/crypto`。README **没有**说明：是否支持 Argon2id、密钥文件、ChaCha20 加密、合并。

V0 必须用真实文件验证（把 KeePassXC 生成的库、含历史/附件/自定义字段/TOTP/密钥文件的库，读入再写出，再让 KeePassXC 打开，字节语义无损）：

- 读写 KDBX 4.0/4.1，Argon2d 参数、ChaCha20、AES-256、密钥文件、复合密钥；
- 往返不丢字段（`History`、`CustomData`、`DeletedObjects`、`otp`、附件、图标、有效期）；
- 对解析器做 Go 模糊测试（恶意库文件不能崩溃、不能无界分配内存）；
- Argon2 的纯 Go 实现性能和内存（Android 上 64 MiB 是否可接受）；是否依赖 cgo（我们的核心是纯 Go，不能引入 cgo）**[待验证]**。

放行条件：全部通过则用该库；任一项不通过，则备选是**自己实现最小 KDBX 4 读写**（格式是公开的，估计 1–2 周，但这是安全关键代码，需要独立评审）。

**V0 验证结果（2026-10-26，见 [vault-v0-poc-report.md](vault-v0-poc-report.md)）：** 结论是"使用该库 + 包装层 + 少量补丁"。往返无损、损坏输入全部报错、纯 Go、MIT/BSD、性能可接受；但库**不支持 Argon2id（且静默退化为无拉伸）**、**解析前没有任何资源上限**（几百字节的文件头可造成 4 GiB 分配或挂死）、有 1 处 panic、重新保存时**重用 MasterSeed/IV**（ChaCha20 密钥流重用）、写入端有两个可能被严格读取方拒绝的怪癖。**KeePassXC 互操作仍未验证**，是进入 V1 的闸门。

### 4.3 主密钥与解锁

- 复合密钥：主密码 + 可选密钥文件（KDBX 原生）。**没有主密码就无法解锁**，不存在"忘记密码"后门。
- **快速解锁**（可选，默认开）：
  - Android：新建一把 Keystore 密钥，`setUserAuthenticationRequired(true)`，并在生物识别录入变化时失效；用它包装"复合密钥的派生结果"，通过 `BiometricPrompt` 解锁。**[待验证]** 具体参数与各厂商兼容性。
  - Windows：用 Windows Hello（`KeyCredentialManager`）或 DPAPI + 重新确认的方式包装；DPAPI 单独不能阻止同用户恶意进程，所以必须叠加 Hello 或仅在会话内内存保存。**[待验证]**
  - 快速解锁有硬性时限：**每 72 小时（可配）或重启后必须输入主密码**；错误 5 次后退回主密码。
- 自动锁定：空闲 N 分钟（默认 5 分钟）、Windows 会话锁定/睡眠、Android 熄屏或应用进入后台 N 秒后锁定。锁定即丢弃内存里的解密库与派生密钥。
- 主密码强度：创建时给强度提示和长度建议，但不强制拒绝；提示"忘记主密码无法找回"，并引导生成恢复密钥文件（§8.4）。

### 4.4 内存与进程

- Go 无法保证清零（GC 会移动对象、字符串不可变）。做法：密钥与密码尽量放在 `[]byte`，用完立即覆写；尽力使用 `VirtualLock`/`mlock`；**在文档中明确写出**：已解锁状态下，同用户权限的恶意软件可以读取进程内存——这是所有密码管理器共有的限制，不是本实现的特例。
- 核心 DLL 在 WPF 进程内，Go panic 会拖垮宿主（ADR-005 已记）。密码库方法必须在门面处统一 recover，且 panic 时不能把请求参数写进日志。
- 日志永不包含条目内容、密码、主密码、密钥（现有约定，延伸到全部 `vault.*`）。

## 5. 多设备同步（V2）

### 5.1 语义

- 密码库是**最终一致的状态**，不是事件流。不走带 TTL 的发件箱（事件寿命上限 7 天，过期即丢弃；密码库一次丢失更新就是数据损坏）。
- 每台参与同步的设备保存一份完整的 `.kdbx` 副本，同一个 `vault_id`（随机 UUID，存在库的 `CustomData` 里）。
- **每个已配对设备单独选择是否参与密码库同步，默认关闭**（`vault_sync` 能力位，配对后和设备页里开启）。剪贴板信任和密码库信任是两件事。

### 5.2 传输与协议

- 复用现有 mTLS 链路与证书固定，不新增端口、不新增监听。链路上新增三种帧：`vault_head`（`vault_id`、版本摘要 = 库密文 SHA-256、修改时间）、`vault_want`、`vault_have`（带 Range 的整库密文传输，复用 `xfer` 的续传与校验）。
- 拨号方驱动，沿用"Windows 监听、Android 只出站"。链路建立和每次保存后交换 `vault_head`，摘要不同才传输。
- **只传输密文**（`.kdbx` 本身），对端没有主密码时也不能读取；对端解锁后再合并。
- 对端收到后：
  1. 摘要相同 → 无事；
  2. 对端副本是本机副本的祖先（本机包含其全部条目与历史）→ 无合并，直接采用本机；
  3. 否则 → 进入合并（§5.3）。库已锁定时，把收到的密文暂存为 `incoming`，下次解锁后合并。

### 5.3 合并规则（必须写成可测试的性质）

沿用 KeePass 的合并思路：按条目 UUID 匹配；`LastModificationTime` 较新者为当前版本，另一版本进入 `History`；`DeletedObjects` 记录墓碑，删除与较新的修改冲突时**保留修改**（宁可多留不要误删）；分组移动按较新的 `LocationChanged`。

必须有的性质（属性测试 + 模糊测试覆盖）：

- **交换律**：`merge(A,B) = merge(B,A)`；
- **结合律与幂等**：`merge(merge(A,B),B) = merge(A,B)`；多设备以任意顺序同步最终收敛；
- **不丢数据**：任何一方存在的条目/历史/附件，合并后要么存在，要么在墓碑里有明确记录；
- **合并前自动备份**当前库（§8），合并出错可回滚。

### 5.4 主密码变更

采用整库方案的已知代价：某台设备改了主密码后，其它设备解不开收到的新库。处理：收到解不开的新库时，界面提示"另一台设备已更换主密码，请输入新的主密码"，用新密码解开并合并，成功后本机副本改用新密码重新加密。旧密码被撤销的时间点与旧副本的残留风险写入说明书。**[待验证]** V2 实测体验，不好就重评方案 B。

### 5.5 撤销设备

解除配对只能阻止之后的同步，**无法收回该设备已经见过的秘密**。界面在"解除密码库同步"时明确提示：这台设备可能保留了旧副本，建议更换主密码并轮换重要账户的密码；提供"更换主密码并重新加密"（新的主种子和 KDF 盐）的一键流程。

## 6. 与剪贴板功能的联动（硬要求）

1. **密码库复制必须标记为敏感并自动清除。**
   - Windows：写入时同时设置 `ExcludeClipboardContentFromMonitorProcessing`、`CanIncludeInClipboardHistory=0`、`CanUploadToCloudClipboard=0`；
   - Android 13+：`EXTRA_IS_SENSITIVE`（已有）；
   - 默认 20 秒后清除，**仅当剪贴板内容仍是我们写入的那一份**才清除（比较内容哈希，避免清掉用户后来复制的东西）。
2. **绝不广播**：从密码库复制的内容，核心把其哈希登记为"库来源"（`vault_origin`），`clip.local_changed` 遇到该哈希直接 `suppressed: vault_origin`，不发送给任何设备。
3. **绝不自动应用**：来自对端的内容一旦被识别为敏感，保持现有规则（永不 `auto`）；与自动广播计划的 `auto` 默认值叠加时，这条是安全兜底，必须保留测试。
4. 密码库界面必须启用防截屏（Android `FLAG_SECURE`；Windows `SetWindowDisplayAffinity` 的排除捕获模式，可在设置里关闭）。**[待验证]** Windows 各版本行为。
5. 命名管道与命令行：**所有 `vault.*` 不进入白名单**，并新增测试固定这一点；V1 不提供任何命令行取密码的入口。

## 7. 自动填充（V3–V5）

### 7.1 Android（V3）

- `AutofillService`（API 26 引入，与我们的 `minSdk=26` 一致）[已查证 minSdk]：用户在系统设置里把本应用设为首选自动填充服务，之后所有应用可用；Chrome 自 2024 年起原生支持第三方自动填充服务，不再需要兼容模式 [已查证：[Android 开发者博客](https://android-developers.googleblog.com/2024/10/chrome-3p-autofill-services.html)]。
- 条目与应用/网站的匹配：优先 Web 域名，其次应用包名（约定自定义字段 `AndroidApp=<包名>` 与 Keepass2Android 一致 **[待验证]**）；域名匹配用公共后缀列表规范化，**不做子串匹配**（防钓鱼）。
- 每次填充都要解锁（生物识别）；库锁定时只显示"解锁以填充"，不缓存数据集；填充前展示将填入的条目，让用户确认。
- 不支持的应用/场景：自绘视图、禁用自动填充的应用——不尝试用无障碍服务绕过（与 plan-auto-broadcast 同一立场：权限过大）。
- 第二步：Credential Manager 提供者（`CredentialProviderService`，API 34+，用于通行密钥和统一选择界面）[已查证：[文档](https://developer.android.com/identity/sign-in/credential-provider)]，放 V5。

### 7.2 Windows（V4）

Windows 没有等价于 Android 的系统级密码自动填充框架。选项：

| 选项 | 做法 | 优点 | 缺点 | 判断 |
| --- | --- | --- | --- | --- |
| Auto-Type | 全局热键，按窗口标题/进程匹配条目，用 `SendInput` 逐键输入 | 通用、无需浏览器扩展、KeePass 用户熟悉 | 对管理员权限窗口无效（UIPI）；键盘输入可被钩子记录；要防"输入到错误窗口" | 做；默认需要用户确认窗口标题 |
| 浏览器扩展 + Native Messaging | 扩展与本机应用通过原生消息通信 | 体验最好，可按真实 URL 匹配 | 要自己维护 Chrome/Edge/Firefox 三套扩展与上架/侧载；协议要自己设计和审计；这是一个独立的产品线 | 评估：先不做；若做，独立立项 |
| 兼容 KeePassXC-Browser 协议 | 让现成的 KeePassXC-Browser 扩展与本应用对话 | 不用自己写扩展 | 受上游协议演化约束；协议实现要独立审计；KeePassXC 代码是 GPL，**只能按公开协议文档自行实现，不能拷贝代码** | 评估，放 V4 末尾，PoC 后决定 |
| 方案 0 | 用户在 Windows 上同时安装 KeePassXC 直接打开同一个 `.kdbx`，浏览器集成由它负责 | 零开发，功能完整 | 不是"一个应用"；两个程序同时写同一文件要避免冲突（我们的合并可兜底） | 作为 V4 之前的过渡建议写进说明书 |

### 7.3 Windows 通行密钥插件（V5，评估）

[已查证] Windows 11 已有第三方通行密钥插件机制，2025 年 11 月起随安全更新正式提供，Bitwarden、1Password 是首批合作方 [来源：[Petri](https://petri.com/windows-11-insider-preview-third-party-passkey-support/)、[BleepingComputer](https://www.bleepingcomputer.com/news/security/microsoft-testing-windows-11-support-for-third-party-passkeys/amp/)]。最低系统版本、清单与签名要求我**没有查到官方细节**，且示例要求 WinUI 3 / 打包应用 **[待验证]**。而我们目前是解压即用的 zip（未打包），很可能需要先做 MSIX 打包（HANDOFF §7 已列为待决）。结论：V5 在 V1–V4 稳定后再评估，不作为承诺。

## 8. 备份与恢复（完整性的核心）

目标：任何单点故障（误删、同步传播了错误、文件损坏、设备丢失、忘记主密码）都有明确的恢复路径，且**不依赖本应用本身**。

### 8.1 多层备份

| 层 | 机制 | 默认 |
| --- | --- | --- |
| L1 原子写入 | 临时文件写完 → `fsync` → 原子改名；保留上一代 `vault.kdbx.prev`；打开时校验 HMAC，失败自动提示使用上一代 | 总是 |
| L2 自动快照 | 每次**会改变库的操作之前**（保存、合并、导入、恢复）生成加密快照：保留最近 20 份 + 每日 7 份 + 每周 4 份；快照就是 `.kdbx` 密文，与原库同一主密钥 | 开 |
| L3 外部备份 | 定期把最新快照复制到用户指定位置（Windows：任意文件夹；Android：SAF 选定目录），可指向同步盘/U 盘；不联网上传，我们不碰云 | 提示设置，30 天未做外部备份时提醒 |
| L4 手动导出 | 导出 `.kdbx`（可选不同的主密码/密钥文件）；导出 CSV 需二次确认并强警告明文 | — |
| L5 其它设备副本 | 同步产生的副本天然是冗余，但**同步也会传播错误和删除**，所以不能替代 L2/L3 | 随同步 |

### 8.2 恢复

- 选择快照 → **预览差异**（新增/删除/修改的条目数与列表，不显示密码）→ 选择"整库恢复"或"只恢复选中的条目"（走合并逻辑，重复条目保留历史）。
- 恢复前**先自动做一份当前库的快照**，所以恢复本身可撤销。
- "验证备份"按钮：用主密码实际打开该快照并校验 HMAC 与条目数，结果以"可恢复/不可恢复"显示。
- 误删恢复：回收站 + 条目历史 + 快照三层。
- 同步冲突导致的"另一版本"以历史记录形式保留，不静默丢弃。

### 8.3 灾难场景表

| 场景 | 恢复路径 |
| --- | --- |
| 误删一个条目 | 回收站；或历史；或快照里"只恢复选中条目" |
| 同步把错误改动传到所有设备 | L2 快照（时间点恢复） |
| 库文件损坏 | L1 上一代；L2 快照；其它设备副本 |
| 手机丢失 | 用电脑上的副本；解除该设备配对；建议更换主密码并轮换重要账户 |
| 所有设备丢失 | L3/L4 外部备份 + 主密码（+ 密钥文件）；备份是标准 `.kdbx`，装 KeePassXC 就能打开 |
| 忘记主密码 | **无法恢复**。唯一出路是恢复密钥（§8.4）或其它设备上已解锁的会话（立即导出） |
| 应用本身出 bug / 项目废弃 | 标准格式，任意 KeePass 兼容软件可读，这就是选 KDBX 的核心理由 |

### 8.4 恢复密钥（可选，建议）

创建库时提供"生成恢复密钥文件"：随机 256 位，作为 KDBX **密钥文件**加入复合密钥，要求用户打印或存放到安全位置，界面明确说明"丢失主密码且无恢复密钥文件 = 永久无法打开"。注意：使用密钥文件意味着**日常解锁也需要该文件**（KDBX 复合密钥的特性），因此更实际的做法是 V1 把它做成"导出一份用独立恢复口令加密的备份副本"（口令由系统生成并要求用户抄写），日常解锁不受影响。具体选哪个在 V0 的 ADR-006 里定。

### 8.5 Android 系统备份

保持 `allowBackup="false"`（[已查证]），密码库明确排除在系统云备份和设备迁移之外；用户自行用 L3/L4。需要在说明书里写明"换手机要先导出或先同步"。

## 9. 威胁模型与风险

| # | 风险 | 影响 | 对策 |
| --- | --- | --- | --- |
| R1 | **自更新机制 = 对所有设备的代码执行权**。发布密钥和 Android 签名密钥在开发机，更新服务器也在同一台机器（HANDOFF §8） | 开发机或密钥被攻破 → 推送恶意版本 → 窃取所有已解锁的库 | 启用密码库的设备默认**关闭静默自动安装**，改为"下载后须用户确认"，并显示版本与哈希；发布密钥离线/硬件存放（需用户决定）；发布前复核构建（可复现构建作为后续项）。**这是引入密码库后必须由用户正视的最大系统性风险** |
| R2 | 已配对设备被攻破 | 同步的库整体泄露（有主密码时） | 密码库同步单独逐设备开启；每台设备必须自己输入主密码解锁；设备页显示"此设备持有库副本"；撤销流程（§5.5） |
| R3 | 同用户恶意进程读内存/键盘 | 已解锁时可窃取 | 自动锁定；快速解锁时限；说明书如实声明限制；管道与命令行不暴露 |
| R4 | 剪贴板泄露 | 密码被自动广播或被剪贴板历史记录 | §6 的四条硬要求 + 测试 |
| R5 | 同步 bug 导致数据丢失 | 不可逆 | 合并前自动快照；属性测试；V2 先在测试库上灰度 |
| R6 | 解析器漏洞（恶意库文件） | 崩溃/内存耗尽/代码执行 | 模糊测试；限制块数/条目数/附件大小；解析在沙盒式的受限函数里并 recover |
| R7 | 钓鱼式匹配 | 把密码填到仿冒网站 | 严格的域名匹配（公共后缀规范化），不做子串/模糊匹配；Android 校验 `webDomain`；填充前展示目标 |
| R8 | 实现未经真机验证 | 锁定/防截屏/生物识别在真机上不如预期 | §10 放行条件；V1 的真机清单作为放行项 |
| R9 | 许可证冲突 | 误拷贝 GPL 代码污染仓库 | 只用 MIT 库（gokeepasslib）和公开规范；仓库目前无 LICENSE，公开前必须选定；评审时检查依赖许可证 |
| R10 | 范围蔓延 | 项目拖垮、质量下降 | 阶段化 + 放行条件；"不做"清单（§3）冻结 |
| R11 | 用户期望"像 1Password/Bitwarden" | 失望 | 说明书写清：无云账户、无紧急访问、无共享库；退出成本为零是替代 |

## 10. 阶段与放行条件

| 阶段 | 内容 | 放行条件（全部满足才进入下一阶段） | 估计 |
| --- | --- | --- | --- |
| **V0** | ADR-006（格式、威胁模型、同步方案）；KDBX 库选型 PoC（§4.2）；真机 PoC：Android 生物识别快速解锁、`FLAG_SECURE`、Windows 防截屏/剪贴板标记；决定恢复密钥形态（§8.4）；**先完成现有功能的真机验证（HANDOFF §6）** | 库往返无损并被 KeePassXC 打开；模糊测试通过；真机 PoC 通过；用户确认 R1 的处理 | 1–2 周 + 真机 |
| **V1** | 本地库：`core/vault`（创建/解锁/锁定、条目与分组 CRUD、历史、回收站、搜索、TOTP、生成器、导入导出、L1/L2 备份、恢复与差异预览、自动锁定、剪贴板联动 §6）；Windows 与 Android 界面；管道禁用测试；`make check`；独立对抗式安全评审（沿用上次评审方式） | 评审的高危问题清零；真机清单通过：锁定/解锁/生物识别/自动清除剪贴板/备份恢复演练（含"备份在另一台机器用 KeePassXC 打开"） | 4–6 周 |
| **V2** | 多设备同步：能力位、`vault_*` 帧、整库传输、合并与属性测试、主密码变更流程、撤销流程、设备页 UI、更新确认策略（R1） | 合并性质测试通过；三设备乱序/断线/并发编辑演练；无数据丢失 | 2–3 周 |
| **V3** | Android 自动填充 | 在 Chrome 与至少 5 个常用应用上填充正确；钓鱼域名用例全部拒绝 | 2–3 周 |
| **V4** | Windows Auto-Type；评估浏览器扩展/KeePassXC-Browser 协议 | Auto-Type 对窗口匹配的安全用例通过；浏览器方案单独立项决定 | 3–4 周 |
| **V5** | 通行密钥评估 | 另立计划 | 不估计 |

合计约 12–16 周（V0–V4）+ V5，加上真机往返和评审修复，保守按 **15–20 周**。这是估计。

## 11. 门面接口草案（V1）

均走现有的 `Call(method, json)`，仅示意，实现时进 `api-spec.md`：

| 方法 | 说明 |
| --- | --- |
| `vault.status` | `{exists, locked, entries, last_backup_at, quick_unlock, sync_peers[]}` |
| `vault.create` / `vault.unlock` / `vault.lock` / `vault.change_master` | 主密码以字节传入，调用后核心立即覆写；快速解锁由壳持有包装后的密钥并通过 `vault.unlock{wrapped}` 交回 |
| `vault.list` / `vault.search` / `vault.get` | `get` 默认**不含**密码字段；`vault.reveal{id,field}` 单独返回一次明文，壳用完立即覆写 |
| `vault.entry.create/update/delete/restore`、`vault.group.*` | |
| `vault.totp{id}` | 返回当前码与剩余秒数 |
| `vault.generate` | 密码/口令短语生成 |
| `vault.backup.create/list/verify/preview/restore`、`vault.export`、`vault.import` | §8 |
| `vault.sync.enable{device_id}` / `vault.sync.status` | V2 |
| 事件 | `vault.locked`、`vault.changed`、`vault.sync.state` |

约束：所有 `vault.*` 不进命名管道白名单；错误信息不包含条目内容；`vault.reveal` 与 `vault.export` 要求库处于解锁且最近一次主密码确认在 N 分钟内（防止解锁后被长期滥用）。

数据位置：`<data_dir>/vault/vault.kdbx`、`vault.kdbx.prev`、`backups/`、`vault.meta.json`（非机密：`vault_id`、同步摘要、设置）。

## 12. 测试策略

- **格式互通**：CI 里用 KeePassXC 命令行（`keepassxc-cli`，本机目前**未安装**）生成/读取库，双向互测；固定一组黄金文件放进仓库。
- **属性测试**：合并的交换律、结合律、幂等、不丢数据（随机操作序列生成）。
- **模糊测试**：KDBX 解析、导入器（CSV/XML）、`vault_*` 帧。
- **安全回归**：管道禁用、日志不泄露、剪贴板联动（`vault_origin` 绝不广播）、自动清除只清自己写入的内容、锁定后内存里没有残留（尽力检查）。
- **备份演练**：自动化的"备份 → 恢复到全新设备 → 校验"；真机的灾难场景表（§8.3）逐项演练。
- 沿用现有矩阵：核心 `-race`、Wine 下重跑、C# xUnit、JUnit、`make check`。

## 13. 需要用户决定的事

1. **方案 0 还是自研集成？** 方案 0 零开发立即可用；集成是 15–20 周的大工程。我的建议：先用方案 0 过渡，同时启动 V0（只花 1–2 周，用 PoC 数据再决定是否继续 V1）。
2. **R1：** 启用密码库的设备是否接受"更新必须手动确认"；发布密钥是否迁到离线/硬件保存。
3. **格式取舍：** 接受 KDBX 4 整库同步（推荐），还是为将来的逐条同步预留自有格式（B 方案，成本显著更高）。
4. **恢复形态：** 恢复密钥文件（日常解锁也要）还是独立恢复口令备份（日常不受影响，推荐）。
5. **范围冻结：** §3 的"不做"清单（SSH Agent、YubiKey、共享库、紧急访问）是否同意。
6. **仓库许可证：** 公开仓库目前没有 LICENSE；需要在引入第三方依赖与对外发布前确定。
7. **真机：** V0 的 PoC 与全部放行条件都依赖真机；请说明可用的 Windows 与 Android 设备。

## 14. 我没有核实的内容（别当事实）

- gokeepasslib 对 Argon2id、密钥文件、ChaCha20、合并的支持，以及其 Argon2 实现是否带 cgo；
- KeePassXC/KeePassDX 对 Argon2id 的读取；
- Android 低端机上 64 MiB Argon2 的耗时与内存；
- Android Keystore 生物识别密钥在各厂商的行为；Windows Hello 密钥包装的具体 API 与限制；
- Windows 防截屏接口在各版本上的效果；
- Windows 通行密钥插件的系统版本、打包与签名要求；
- `AndroidApp=` 自定义字段与 Keepass2Android 的约定；
- EFF 词表的许可证文字；
- 工作量与周期估计。

## 15. 参考

- [KeePass：KDBX 4 说明](https://keepass.info/help/kb/kdbx_4.html)
- [gokeepasslib（MIT，Go）](https://github.com/tobischo/gokeepasslib)
- [KeePassXC（GPL-2/3，仅参考其公开文档，不拷贝代码）](https://github.com/keepassxreboot/keepassxc)
- [Android：Credential Manager 提供者](https://developer.android.com/identity/sign-in/credential-provider)
- [Android 开发者博客：Chrome 原生支持第三方自动填充](https://android-developers.googleblog.com/2024/10/chrome-3p-autofill-services.html)
- [Petri：Windows 11 第三方通行密钥](https://petri.com/windows-11-insider-preview-third-party-passkey-support/)、[BleepingComputer](https://www.bleepingcomputer.com/news/security/microsoft-testing-windows-11-support-for-third-party-passkeys/amp/)
- 本仓库：[plan-auto-broadcast.md](plan-auto-broadcast.md)、[ADR-005](adr/005-go-core-and-tailnet-p2p.md)、[architecture.md](architecture.md)、[api-spec.md](api-spec.md)、[update.md](update.md)、[../HANDOFF.md](../HANDOFF.md)
