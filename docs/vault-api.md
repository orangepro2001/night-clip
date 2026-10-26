# 密码库：门面接口与壳层规则

> 对应实现：`core/vault`（KDBX 4 存储）与 `core/facade/vault.go`（`vault.*` 方法）。设计依据：[plan-vault.md](plan-vault.md)、[vault-v0-poc-report.md](vault-v0-poc-report.md)。
> 所有方法都通过现有的 `Call(method, paramsJSON)`；**一律不开放给命名管道和命令行**（`core/pipe` 有测试固定这一点）。
> 数据文件：`<data_dir>/vault/vault.kdbx`（标准 KDBX 4，KeePassXC 可直接打开）、`vault.kdbx.prev`（上一代）、`backups/`（快照）。

## 1. 状态与锁

- 核心同一时刻最多持有一个已解锁的库。**未解锁时除下列方法外一律返回 `VAULT_LOCKED`**：`vault.status`、`vault.create`、`vault.unlock`、`vault.lock`、`vault.verify_master`（需要已解锁）、`vault.generate`、`vault.strength`、`vault.backup_list`、`vault.backup_verify`。
- **空闲自动锁**：任何 `vault.*`（除 `status`）调用都会重置计时；超过 `vault_autolock_s`（默认 300 秒，范围 30–86400）核心自行锁定并发出事件 `{"type":"vault.locked","reason":"idle"}`。壳在系统锁屏/睡眠/应用进入后台时应调用 `vault.lock`（`reason:"user"`）。
- **猜测限速**：同一进程内连续 3 次错误凭据后，每次再试等待 1、2、4…秒（上限 5 分钟），期间 `vault.unlock`/`verify_master`/`change_master`/`export_*`/`backup_verify` 返回 `RATE_LIMITED`（`retryable:true`，`details.retry_after_s`）。成功一次清零。这只能挡住对运行中应用的脚本猜测；偷走文件的离线猜测靠 Argon2 成本。
- 关闭核心（`shutdown`）会先锁库。

## 2. 错误码

| 码 | 含义 | 壳层该怎么显示 |
| --- | --- | --- |
| `VAULT_LOCKED` | 库未解锁 | 跳到解锁界面 |
| `VAULT_WRONG_CREDENTIALS` | 主密码/密钥文件错误**或文件损坏**（故意不区分） | “主密码或密钥文件不对，或文件已损坏。可以从备份恢复。” |
| `VAULT_NOT_FOUND` | 条目/分组/备份不存在 | |
| `VAULT_EXISTS` | 库已存在 / 导出目标文件已存在 | |
| `VAULT_UNSUPPORTED` | 文件的密钥派生参数不被接受（过大或不支持的 KDF） | “这个库的加密参数不受支持或不安全。” |
| `VAULT_BROKEN` | 内存状态不一致 | 提示锁定后重新解锁 |
| `RATE_LIMITED` | 见上 | 显示倒计时 |
| `TOO_LARGE`、`INVALID` | 同其它方法 | |

## 3. 方法

字段名均为 snake_case JSON。`credParams` = `{"password": "...", "key_file_b64": "<可选，密钥文件内容的 base64，≤1 MiB>"}`。

### 3.1 生命周期

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `vault.status` | — | `{exists, unlocked, autolock_s, clip_clear_s, retry_after_s, entries?, kdf?, backups?, last_manual_backup_at?}` |
| `vault.create` | `credParams` + `kdf?`（`low`/`standard`/`high`，默认 `standard`），`name?` | `{unlocked:true, create_ms}`；创建后已解锁。`exists` 时 `VAULT_EXISTS` |
| `vault.unlock` | `credParams` | `{unlocked:true, unlock_ms}` |
| `vault.lock` | — | `{was_unlocked}` |
| `vault.verify_master` | `credParams` | `{ok:true}` 或 `VAULT_WRONG_CREDENTIALS`；用于“再次确认主密码”类操作 |
| `vault.change_master` | `credParams`（当前）+ `new: {password, key_file_b64?}` + `kdf?` | `{}`；整个库用新的随机值重新加密；**旧快照仍需要旧密码** |

KDF 预设：`low` = Argon2d 32 MiB×3，`standard` = 64 MiB×3，`high` = 128 MiB×4。壳可以用 `unlock_ms` 判断设备是否太慢（> 3 秒时建议降级）。

### 3.2 条目

条目 JSON（`vault.add`/`vault.update` 的 `entry`）：

```json
{"kind":"login|apikey|secret|note","title":"…","username":"","password":"","url":"","notes":"",
 "tags":["a","b"],"fields":[{"name":"Key ID","value":"…","protected":false}],
 "totp":"otpauth://totp/… 或 base32","expires_at":0,"group_id":""}
```

- `kind` 只影响界面的呈现：`login`（站点账号）、`apikey`（API 密钥/令牌：密钥放 `password`，端点放 `url`，Key ID/区域/环境等放 `fields`）、`secret`（单个机密：许可证、PIN、恢复码，放 `password`）、`note`（安全笔记，只用 `notes`）。KeePassXC 里它们都是普通条目。
- `fields[].name` 不能是保留名（Title/UserName/Password/URL/Notes/otp，大小写不敏感），不能重复，≤128 字节；单个文本字段 ≤256 KiB；最多 100 个字段/标签。

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `vault.list` | `query?`、`group_id?`、`kind?`、`tag?`、`trash?`（只看回收站）、`include_trash?` | `{entries:[{id, group_id, kind, title, username, url, tags, has_totp, has_attachments, modified_at, expires_at, in_trash}]}`，按标题排序。**不含任何机密，也不含备注**。`query` 匹配标题、用户名、URL、备注、标签、自定义字段名和**非保护**字段值，**永远不匹配保护值** |
| `vault.get` | `id` | 详情：同上加 `notes`、`fields[{name,value,protected,has_value}]`（保护字段的 `value` 为空）、`has_password`、`has_totp`、`attachments[{name,size}]`、`history`（版本数）、`created_at`、`password_strength{bits,label}` |
| `vault.reveal` | `id`, `field` | `{value}`。`field`：`password`、`username`、`url`、`notes`、`totp`（当前验证码）、`totp_secret`、`field:<名字>`。**只用于在屏幕上显示**（用完立刻丢弃）；要复制到剪贴板请用 `vault.copy` |
| `vault.copy` | 同上 | `{value, clear_after_s}`。除返回值外，核心把该文本登记为“来自密码库”：随后的剪贴板变化**不会被自动广播**（`clip.local_changed` 返回 `suppressed: "vault_origin"`）。壳必须按 §4 写入剪贴板并在 `clear_after_s` 秒后清除（0 = 不清除） |
| `vault.totp` | `id` | `{code, seconds_left}`（RFC 6238；SHA1/256/512，6–8 位，周期 1–3600 秒） |
| `vault.add` | `entry` | `{id}` |
| `vault.update` | `id`, `entry` | `{}`；内容有变化才写入历史并更新修改时间；附件、所在分组、历史保持 |
| `vault.delete` | `id` | `{permanent}`；第一次移入回收站，回收站里再删一次才永久删除 |
| `vault.restore_entry` | `id` | `{}`：从回收站放回原分组 |
| `vault.empty_trash` | — | `{removed}` |
| `vault.move` | `id`, `group_id` | `{}`（`group_id` 空 = 顶层） |
| `vault.history` | `id` | `{versions:[{index, modified_at, title, username, url}]}`（旧→新，无机密） |
| `vault.history_restore` | `id`, `index` | `{}`：把旧版本恢复为当前（当前版本进入历史） |

### 3.3 分组与附件

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `vault.groups` | — | `{groups:[{id, parent_id, name, path, entries, is_trash}]}`（第一个是顶层；回收站 `is_trash:true`） |
| `vault.group_add` / `group_rename` / `group_delete` | `{parent_id?, name}` / `{id, name}` / `{id}` | `{id}` / `{}` / `{}`；只能删除空分组，先把条目删掉（进回收站） |
| `vault.attach_add` | `id`, `name`, 以及 `data_b64` **或** `path`（核心读取本地文件，≤5 MiB） | `{}` |
| `vault.attach_get` | `id`, `name`, `path?` | 不给 `path`：`{data_b64, size}`；给 `path`：核心把文件写到那里（**绝不覆盖已有文件**，权限 0600）并返回 `{size}` |
| `vault.attach_remove` | `id`, `name` | `{}` |

### 3.4 工具

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `vault.generate` | `type:"password"`（`length`(4–256，默认 20)、`lower/upper/digits/symbols`（都不选=全选）、`exclude_ambiguous`、`exclude`）或 `type:"passphrase"`（`words`(3–20，默认 6)、`separator`、`capitalize`、`number`） | `{value, strength}`；用系统安全随机源；口令短语来自 EFF 长词表（7776 词，每词约 12.9 位） |
| `vault.strength` | `password` | `{bits, label}`；`label`：`very_weak`/`weak`/`fair`/`good`/`strong`（启发式估计，仅用于强度条） |
| `vault.health` | — | `{weak[], reused[], old[], expired[], without_totp, total}`，每项 `{id,title,info}`；纯本地计算，**不联网、没有泄露库查询**；回收站和笔记不计 |

### 3.5 备份与恢复

快照是“修改之前”的加密文件副本：任何保存、删除、导入、恢复、改主密码之前都会拍一张（普通保存之间至少隔 10 秒）。保留策略：最新 20 张 + 近 7 天每天最新一张 + 近 4 周每周最新一张。**快照用拍摄时的主密码加密**：改过主密码之后，旧快照需要旧密码（`credParams` 可以给）。

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `vault.backup_list` | — | `{backups:[{name, time, size, reason}]}`，新→旧；`reason`：`save`/`delete`/`import`/`restore`/`rekey`/`merge`/`create`/`upgrade`/`manual`/`previous`（`vault.kdbx.prev`）。库未解锁时也可用 |
| `vault.backup_now` | — | 一条备份信息；“立即备份”按钮 |
| `vault.backup_verify` | `name` + `credParams` | `{entries, groups, in_trash, modified_at}`：真的用主密码把它打开，“确认可以恢复” |
| `vault.restore_preview` | `name` + `credParams?`（省略 = 当前凭据） | `{only_in_backup[], only_in_current[], changed[], same}`，项为 `{id,title,modified_at}`（无机密） |
| `vault.restore` | `name`, `mode`, `ids?`, `credParams?` | `{stats}`。`mode`：`entries`（把列出的条目恢复成备份中的样子，当前版本进历史，已不存在的重建）、`merge`（按合并规则并入）、`replace`（整库替换为备份内容，只在当前库里的条目会丢，但仍在恢复前的快照里）。**恢复前一定先拍快照，所以恢复本身可以撤销** |
| `vault.import_file` | `path` + 该文件的 `credParams` | `{stats}`：把另一个 KDBX 文件（KeePassXC、旧版 KDBX 3.x 都行）并入当前库 |
| `vault.import_csv` | `path` | `{added, skipped}`；能识别 Chrome/Edge、Bitwarden、1Password、LastPass、KeePassXC 等常见表头；分组列里的 `A/B` 会建成嵌套分组 |
| `vault.export_file` | `path`, `confirm_password`（+`confirm_key_file_b64?`，当前主凭据），导出文件的 `password`/`key_file_b64`，`kdf?` | `{}`：用**另外的**凭据加密的完整副本；**不覆盖**已存在的文件（`VAULT_EXISTS`） |
| `vault.export_csv` | `path`, `confirm_password` | `{}`：**明文**；壳必须先强警告并要求用户确认，文件权限 0600 |

`stats` = `{entries_added, entries_updated, entries_removed, entries_moved, groups_added, groups_removed}`。

### 3.6 设置（`settings.get`/`settings.set`）

| 键 | 值 | 默认 |
| --- | --- | --- |
| `vault_autolock_s` | 30–86400 | 300 |
| `vault_clip_clear_s` | `0` 或 5–300 | 20 |

## 4. 壳层必须遵守的规则

1. **剪贴板：** 复制机密一律用 `vault.copy`（不是 `reveal`），然后：
   - Windows：写入时同时设置 `ExcludeClipboardContentFromMonitorProcessing`、`CanIncludeInClipboardHistory = 0`、`CanUploadToCloudClipboard = 0`；
   - Android 13+：`ClipDescription.EXTRA_IS_SENSITIVE = true`（`ClipboardAccess.writeText(..., sensitive = true)` 已支持）；
   - 在 `clear_after_s` 秒后清除，**仅当剪贴板内容仍是刚才写入的那份**（Windows 用 `GetClipboardSequenceNumber` 比对；Android 比对内容或描述）；
   - 不要把这段文本写进日志、通知、收件箱、最近使用列表。
2. **屏幕：** 库的界面不能被截屏/录屏/出现在最近任务缩略图里：Android 用 `FLAG_SECURE`（至少在库页面可见期间）；Windows 用 `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)`（可在设置里关掉，默认开）。
3. **显示：** 机密默认遮盖，点“显示”才展开，展开后在若干秒（建议 15 秒）或离开页面时自动再遮盖；`vault.reveal` 的结果用完立即丢弃引用。
4. **锁定：** 系统锁屏/睡眠/切换用户、应用进入后台超过设定秒数 → `vault.lock`。锁定后清空列表、详情、搜索框、剪贴板中仍是我们写入的那份。
5. **危险操作再确认：** 导出（KDBX/CSV）、改主密码、清空回收站、永久删除、整库恢复，都要先弹确认；导出还要重新输入主密码（核心也会校验）。
6. **快速解锁（可选）：** 壳可以把主密码用设备密钥保护后保存（Android：Keystore + `BiometricPrompt`，`setUserAuthenticationRequired(true)`，生物识别变化即失效；Windows：暂不提供），解锁时取出并调用 `vault.unlock`。**每 72 小时、重启后或连续 5 次失败后必须重新输入主密码。** 核心不知道也不保存这个包装。
7. **不要**缓存条目详情或机密到磁盘，不要把库内容放进系统备份（Android `allowBackup="false"` 已满足）。
8. **文案**中不得出现“忘记主密码可以找回”之类的话：忘记主密码就是永久无法打开，请在创建时明确告知，并引导用户做一份 `vault.export_file` 备份和（可选）密钥文件。

## 5. 与剪贴板同步的关系

- 密码库复制的内容不会被自动广播（`vault_origin`，登记有效 1 小时）；用户显式用 `clip.send` 发送仍然可以。
- 收到的剪贴板文本若被识别为敏感，依然永远不会自动写入（`plan-auto-broadcast.md`）。
- 密码库本身**目前只在本机**；多设备同步（V2）见 [plan-vault.md](plan-vault.md) §5，合并规则已在 `core/vault/merge.go` 实现并有收敛性测试，传输层尚未接入。
