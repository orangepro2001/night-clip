# 密码库：门面接口与壳层规则

> 对应实现：`core/vault`（KDBX 4 存储）与 `core/facade/vault.go`（`vault.*` 方法）。设计依据：[plan-vault.md](plan-vault.md)、[vault-v0-poc-report.md](vault-v0-poc-report.md)。
> 所有方法都通过现有的 `Call(method, paramsJSON)`；**一律不开放给命名管道和命令行**（`core/pipe` 有测试固定这一点）。SSH 密钥的 `ssh.*` 方法同样如此；SSH Agent 用自己的管道，只提供签名（[ADR-009](adr/009-ssh-agent.md)、§3.9）。
> 数据文件：`<data_dir>/vault/vault.kdbx`（标准 KDBX 4，KeePassXC 可直接打开）、`vault.kdbx.prev`（上一代）、`backups/`（快照）。

## 1. 状态与锁

- 核心同一时刻最多持有一个已解锁的库。**未解锁时除下列方法外一律返回 `VAULT_LOCKED`**：`vault.status`、`vault.create`、`vault.unlock`、`vault.lock`、`vault.verify_master`（需要已解锁）、`vault.generate`、`vault.strength`、`vault.backup_list`、`vault.backup_verify`。
- **空闲自动锁**：任何 `vault.*`（除 `status` 和 `vault.totp`）调用都会重置计时（轮询一次性验证码**不算**活动，否则无人值守的库永远不会锁）；用户正在输入而没有调用核心时，壳发 `vault.touch`（`{}`）表示“还在”；超过 `vault_autolock_s`（默认 300 秒；范围 0 或 30–86400，0 = 永不自动锁定）核心自行锁定并发出事件 `{"type":"vault.locked","reason":"idle"}`。壳在系统锁屏、注销、切换用户时调用 `vault.lock {reason:"session"}`，睡眠时 `{reason:"suspend"}`，用户点“锁定”时 `{reason:"user"}`（默认）；事件里的 `reason` 与之相同。这三种原因还会立即清除 SSH Agent 里的钥匙，**即使密码库本来就锁着**（空闲锁定后钥匙可能还在宽限期里），所以壳在这些系统事件上无论库是否解锁都要调用。
- **猜测限速**：所有接受凭据的方法（`unlock`、`verify_master`、`change_master`/`export_*` 的确认、`backup_verify`、`restore`、`restore_preview`、`import_file`、带密码的 `sync_resolve`）**一次只处理一个**，并共用同一个计数；只有“打开库/确认主密码”的成功才会清零计数（用旧密码验证备份成功不会清零）。同一进程内连续 3 次错误凭据后，每次再试等待 1、2、4…秒（上限 5 分钟），期间 `vault.unlock`/`verify_master`/`change_master`/`export_*`/`backup_verify` 返回 `RATE_LIMITED`（`retryable:true`，`details.retry_after_s`）。成功一次清零。这只能挡住对运行中应用的脚本猜测；偷走文件的离线猜测靠 Argon2 成本。
- 关闭核心（`shutdown`）会先锁库。

## 2. 错误码

| 码 | 含义 | 壳层该怎么显示 |
| --- | --- | --- |
| `VAULT_LOCKED` | 库未解锁 | 跳到解锁界面 |
| `VAULT_WRONG_CREDENTIALS` | 主密码/密钥文件错误**或文件损坏**（故意不区分） | “主密码或密钥文件不对，或文件已损坏。可以从备份恢复。” |
| `VAULT_NOT_FOUND` | 条目/分组/备份不存在 | |
| `VAULT_NOT_EMPTY` | 删除的分组还有条目或子分组 | 提示先删条目 |
| `VAULT_REVIEW` | 需要用户批准（同步：不是同一个库、一批会删除/覆盖很多条目；批准时没有给出或给错了要批准的文件的 `sha256`） | 重新读取 `vault.sync_status` 后让用户确认 |
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
| `vault.status` | — | `{exists, unlocked, autolock_s, clip_clear_s, retry_after_s, key_file?, entries?, kdf?{memory_mib,iterations,parallelism}, backups?, last_manual_backup_at?}`；`key_file` 表示这台设备上次使用的凭据是否含密钥文件（KDBX 文件本身不说明；通过同步加入的库在首次解锁前没有这个字段） |
| `vault.create` | `credParams`（主密码**至少 8 个字符**，否则 `INVALID`）+ `kdf?`（`low`/`standard`/`high`，默认 `standard`），`name?` | `{unlocked:true, create_ms}`；创建后已解锁。`exists` 时 `VAULT_EXISTS` |
| `vault.unlock` | `credParams` | `{unlocked:true, unlock_ms}` |
| `vault.lock` | `reason?`（`user` 默认 / `session` / `suspend`） | `{was_unlocked, ssh_keys_cleared}` |
| `vault.verify_master` | `credParams` | `{ok:true}` 或 `VAULT_WRONG_CREDENTIALS`；用于“再次确认主密码”类操作 |
| `vault.change_master` | `credParams`（当前）+ `new: {password(≥8 字符), key_file_b64?}` + `kdf?` + `purge_backups?` | `{purged}`；整个库用新的随机值重新加密；**旧快照仍需要旧密码**，怀疑泄露时传 `purge_backups:true` 删除全部快照和上一代文件（不可撤销） |

KDF 预设：`low` = Argon2d 32 MiB×3，`standard` = 64 MiB×3，`high` = 128 MiB×4。壳可以用 `unlock_ms` 判断设备是否太慢（> 3 秒时建议降级）。

### 3.2 条目

条目 JSON（`vault.add`/`vault.update` 的 `entry`）：

```json
{"kind":"login|apikey|secret|note|ssh","title":"…","username":"","password":"","url":"","notes":"",
 "tags":["a","b"],"fields":[{"name":"Key ID","value":"…","protected":false}],
 "totp":"otpauth://totp/… 或 base32","expires_at":0,"group_id":""}
```

- `kind` 只影响界面的呈现：`login`（站点账号）、`apikey`（API 密钥/令牌：密钥放 `password`，端点放 `url`，Key ID/区域/环境等放 `fields`）、`secret`（单个机密：许可证、PIN、恢复码，放 `password`）、`note`（安全笔记，只用 `notes`）、`ssh`（SSH 私钥，见 §3.9；由 `ssh.import`/`ssh.generate` 创建，不要用 `vault.add` 手写）。KeePassXC 里它们都是普通条目。
- 没有 `MoonPassword.Kind`（或它是 `login`）但带 `KeeAgent.settings` 附件的条目读作 `ssh`，因此从 KeePassXC 导入的 SSH 条目会被识别；`vault.update` 对这样的条目收到 `kind:"login"` 时保持 `ssh`。
- `fields[].name` 不能是保留名（Title/UserName/Password/URL/Notes/otp，大小写不敏感），不能重复，≤128 字节；单个文本字段 ≤256 KiB；最多 100 个字段/标签。

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `vault.list` | `query?`、`group_id?`（**只含该分组本身的条目，不含子分组**）、`kind?`、`tag?`、`trash?`（只看回收站）、`include_trash?` | `{entries:[{id, group_id, kind, title, username, url, tags, has_password, has_totp, has_attachments, modified_at, expires_at, in_trash}]}`，按标题排序。**不含任何机密，也不含备注**。`query` 匹配标题、用户名、URL、备注、标签、自定义字段名和**非保护**字段值，**永远不匹配保护值** |
| `vault.get` | `id` | 详情：同上加 `notes`、`fields[{name,value,protected,has_value}]`（保护字段的 `value` 为空）、`has_password`、`has_totp`、`attachments[{name,size}]`、`history`（版本数）、`created_at`、`password_strength{bits,label}` |
| `vault.reveal` | `id`, `field` | `{value}`。`field`：`password`、`username`、`url`、`notes`、`totp`（当前验证码）、`totp_secret`、`field:<名字>`。**只用于在屏幕上显示**（用完立刻丢弃）；要复制到剪贴板请用 `vault.copy` |
| `vault.copy` | 同上 | `{value, clear_after_s}`。除返回值外，核心把该文本登记为“来自密码库”：随后的剪贴板变化**不会被自动广播**（`clip.local_changed` 返回 `suppressed: "vault_origin"`）。壳必须按 §4 写入剪贴板并在 `clear_after_s` 秒后清除（0 = 不清除） |
| `vault.touch` | — | `{}`：只重置空闲计时 |
| `vault.totp` | `id` | `{code, seconds_left}`（RFC 6238；SHA1/256/512，6–8 位，周期 1–3600 秒） |
| `vault.add` | `entry` | `{id}` |
| `vault.update` | `id`, `entry` | `{}`；内容有变化才写入历史并更新修改时间；附件、所在分组、历史保持 |
| `vault.patch` | `id` + 要改的字段（`title/username/password/url/notes/totp/kind/tags/fields/expires_at`，没给的保持不变；给了 `fields` 则整体替换自定义字段） | `{}`；改一个密码不必读出所有保护字段 |
| `vault.delete` | `id` | `{permanent}`；第一次移入回收站，回收站里再删一次才永久删除 |
| `vault.restore_entry` | `id` | `{}`：从回收站放回原分组 |
| `vault.empty_trash` | — | `{removed}` |
| `vault.move` | `id`, `group_id` | `{}`（`group_id` 空 = 顶层） |
| `vault.history` | `id` | `{versions:[{index, modified_at, title, username, url}]}`（旧→新，无机密） |
| `vault.history_restore` | `id`, `index` | `{}`：把旧版本恢复为当前（当前版本进入历史） |

### 3.2b 自动填充匹配

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `vault.browser_logins` | `origin`（HTTPS 或回环地址 HTTP）, `id?` | `{matches:[{id,title,username}]}`。仅匹配同源的登录条目，涵盖备用网址。只有壳在用户确认后传入选中的 `id`，才返回该条目的 `password`，核心再次校验网址。 |
| `vault.match` | `url?`（网页地址或主机名）, `package?`（Android 应用包名） | `{matches:[{…列表摘要字段, score}]}`，最好的在前。**不含机密**；要填入时再对选中的条目调用 `vault.reveal`（用户确认/生物识别之后） |

匹配**严格**：先看协议和端口——只接受 http/https；https 页面可以用保存为 http 或 https 的条目，http 页面只能用保存为 http 的条目（本机/内网地址除外）；条目网址里写明的非默认端口必须与页面一致；然后只在“可注册域名”（公共后缀 + 一个标签）相同时才命中（`score` 3 = 主机完全相同，2 = 同一可注册域名），绝不因为一个名字包含另一个就命中（`example.com.evil.net`、`evil-example.com` 都不会匹配 `example.com`）；IP 与单标签主机名要完全相同。应用匹配（`score` 4）：条目的自定义字段 `AndroidApp` 等于包名，或条目 URL 为 `androidapp://<包名>`（Keepass2Android/KeePassDroid 的约定）。回收站与笔记不参与。条目可以用以 `URL`/`KP2A_URL` 开头的自定义字段登记更多网址。

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
| `vault.otp_import` | `payloads: string[]`, `group_id?` | 导入 `otpauth://totp` 或 Google Authenticator 的 `otpauth-migration://offline` 链接。返回 `{added, duplicates, unsupported}`。多张迁移二维码必须一起导入，缺少分片返回 `OTP_INCOMPLETE`。异步扫码可先在壳中收集链接。 |
| `vault.otp_export` | `password`, `key_file_b64?`, `id?` | 重新认证后返回 `{entries:[{id,title,username,link}], skipped}`。省略 `id` 导出所有可用条目。有密钥的链接仅供当前导出流程使用，勿写入日志。 |
| `vault.import_bitwarden_json` | `path` | 导入未加密的 Bitwarden JSON，返回 `{added, skipped, passkeys_skipped}`。主网址保存在 `url`，其他可匹配的网址保存在 `URL n` 字段。自定义字段及其保护状态随条目保存。银行卡、身份资料和 SSH 密钥保存为安全笔记。加密导出返回 `BITWARDEN_ENCRYPTED`。 |
| `vault.export_file` | `path`, `confirm_password`（+`confirm_key_file_b64?`，当前主凭据），导出文件的 `password`/`key_file_b64`，`kdf?` | `{}`：用**另外的**凭据加密的完整副本；**不覆盖**已存在的文件（`VAULT_EXISTS`） |
| `vault.export_csv` | `path`, `confirm_password` | `{}`：**明文**；壳必须先强警告并要求用户确认，文件权限 0600 |

`stats` = `{entries_added, entries_updated, entries_removed, entries_moved, groups_added, groups_removed}`。

### 3.6 多设备同步（V2）

设备之间传的**永远是加密的 `.kdbx` 文件**，不是明文条目：接收方先把对方的文件放在一边（`vault/incoming/<设备ID>.kdbx`），库**已解锁**时按 KeePass 规则合并（核心 `core/vault/merge.go`，有交换律/收敛性测试），合并后再把自己的最新文件传回去。传输复用现有的互相固定证书的 TLS 连接：监听方提供 `GET /v1/vault/head`、`GET /v1/vault/file`、`PUT /v1/vault/file`（仅限已配对设备），拨号方每 15 秒比较一次哈希，本机保存后立即推送。

**默认每台已配对设备都是完整副本**：配对（mTLS 证书固定 + SAS）即信任决定；schema v6 会把已配对设备的 `vault_sync` 置为开，新配对设备也默认开。没有中心节点，也没有主/从节点：本机始终用自己的完整文件提供密码库；一台设备离线不影响其它设备打开本地库，重新连接后自动合并。`vault.sync_enable{enabled:false}` 仍保留为高级的单台退出开关；关闭时对端看到 `peer_off`。

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `vault.sync_status` | — | `{vault_exists, peers:[{device_id, name, enabled, connected, state, detail, last_ok_at, pending_incoming, incoming_sha256?}]}` |
| `vault.sync_enable` | `device_id`, `enabled` | `{}`；高级单台退出开关，默认开；关闭时丢弃已收到但未合并的文件 |
| `vault.sync_resolve` | `device_id`, `approve?`, `sha256?`, `credParams?` | `{}`：为 `needs_password` 输入对方库的主密码，或为 `review` 点“批准”。**`approve` 必须同时给出 `sha256`**（取自 `vault.sync_status` 的 `incoming_sha256`，即用户看到的那个文件），文件在此期间变了就 `VAULT_REVIEW`。库必须已解锁，否则 `VAULT_LOCKED` |
| `vault.sync_now` | — | `{}` |

`state`：`idle`（正常）、`pending_unlock`（对方的修改已收到，等你解锁后合并）、`needs_password`（对方改了主密码，需要输入新密码才能合并）、`review`（需要批准：对方的文件**不是同一个库**，或这一批会**删除 5 个以上条目**）、`peer_off`（对方仍关着单台开关，通常是旧版本客户端）、`error`。

- **加入：** 一台还没有库的设备，配对并连接后会自动收到对方的库文件（事件 `vault.joined`），库显示为“已存在、已锁定”，用**那个库的主密码**解锁即可。
- **事件：** `vault.sync {device_id, name, state, detail}`（状态变化）、`vault.changed {reason:"sync", device_id, stats}`（同步合并改动了本机库，壳应刷新列表）、`vault.joined {device_id, name}`。
- **保护：** 单个文件 ≤64 MiB；同一时刻只处理一个上传（其余 429），每个对端每 2 秒最多上传一次；文件头的密钥派生成本超过“来自对端的文件”上限（Argon2 内存 > 128 MiB、迭代 > 16 等）直接拒绝，不会进行任何派生；收到的文件必须是结构正确的 KDBX，否则 400；一次同步将**删除超过 5 个或覆盖超过 50 个**条目，或 24 小时内累计删除超过 15 个，都需要批准（`review`）；比较“谁更新”时，对端晚于当前时间 10 分钟以上的时间戳按“现在 + 10 分钟”处理；同一个已经失败的文件不会重复尝试（直到新文件、输入密码或批准）；同步产生的快照至少间隔 60 秒，用户主动做的（手动/改主密码/升级/恢复/导入）快照单独保留最多 50 份，不会被例行快照挤掉。

### 3.7 设置（`settings.get`/`settings.set`）

| 键 | 值 | 默认 |
| --- | --- | --- |
| `vault_autolock_s` | `0` 或 30–86400 | 300 |
| `vault_clip_clear_s` | `0` 或 5–300 | 20 |
| `ssh_agent` | `on`/`off`：启用 SSH Agent（Android 上没有效果） | `off` |
| `ssh_agent_pipe` | Agent 的地址：Windows 为 `\\.\pipe\名字`，其它平台为绝对路径；空 = 默认 | Windows `\\.\pipe\openssh-ssh-agent`，其它 `<data_dir>/ssh-agent.sock` |
| `ssh_key_ttl_s` | `0` 或 300–86400：密码库**空闲**锁定后钥匙在 Agent 里再留多久（从本应锁定的时刻算起）；0 = 一起清除 | 14400 |
| `ssh_allow_forwarding` | `on`/`off`：是否服务经 `ssh -A` 转发来的请求 | `off` |

改 `ssh_agent` 或 `ssh_agent_pipe` 会立刻停止并重新启动监听。

### 3.9 SSH 密钥与 SSH Agent（[ADR-009](adr/009-ssh-agent.md)）

**存放：** kind `ssh` 的条目。私钥以**不加密**的 OpenSSH 格式作为附件（名字沿用原文件名，生成的叫 `id_<算法>`），另有 `KeeAgent.settings` 附件（KeePassXC 的格式；`AllowUseOfSshKey` 与 `AddAtDatabaseOpen` 都为真时提供给 Agent）。KeePassXC 习惯把加密私钥的口令放在条目的 Password 里，核心读取时会用它试着解密；`ssh.normalize` 可以把这类条目转成不加密的 OpenSSH 附件。

**`vault.get` 的 `ssh` 块**（只有 SSH 条目有）：`{entry_id, title, group_id, key_attachment, algo, bits, fingerprint:"SHA256:…", comment, public_key, agent, loaded, source_format, encrypted, problem, reason?, last_used_at?, in_trash, expires_at, hosts:[…]}`。`problem` 为空表示可用；其它值：`needs_passphrase`（附件仍加密且没有可用口令）、`external_file`（KeePassXC 指向磁盘文件，不支持）、`missing_key`、`too_large`、`unsupported`（`reason` 同下表）、`corrupt`、`missing_settings`（可用，但没有 `KeeAgent.settings`，不会进 Agent）。

| 方法 | 参数 | 结果 |
| --- | --- | --- |
| `ssh.inspect` | `data_b64` 或 `path`（绝对路径，≤1 MiB），`passphrase?` | `{format, encrypted, cipher, kdf, needs_passphrase, wrong_passphrase, algo?, bits?, fingerprint?, comment?, public_key?, in_vault?:[{entry_id,title}], warnings:[]}`。**库锁着也能用**（此时没有 `in_vault`）。需要口令时不报错，`needs_passphrase:true`；OpenSSH 与 PPK 不需要口令也能给出公钥 |
| `ssh.import` | `data_b64`/`path`、`passphrase?`、`title?`、`group_id?`、`agent?`（默认 true）、`attachment_name?`、`allow_duplicate?` | `{id, fingerprint, public_key, algo, bits, attachment_name}`；按指纹查重，已存在时 `SSH_KEY_DUPLICATE`（`details.entry_id`） |
| `ssh.generate` | `type`（`ed25519` 默认 / `rsa` 3072·4096 / `ecdsa` 256·384·521）、`bits?`、`title`、`comment?`、`group_id?`、`agent?` | 同上 |
| `ssh.public_key` | `id` 或 `fingerprint` | `{public_key, fingerprint, algo, title}`；库锁着时从 Agent 已加载的钥匙里回答 |
| `ssh.export` | `credParams`（主密码，再确认）、`id`、`format`（`openssh` 默认 / `pkcs8` / `pem`，pem 只限 RSA/ECDSA 且不加密）、`passphrase?`、`path`（绝对路径） | `{size}`；以独占方式新建文件（权限 0600），已存在则 `INVALID`。**私钥离开核心的唯一途径** |
| `ssh.set_agent` | `id`、`enabled` | `{agent}`；改写（或新建）`KeeAgent.settings`，保留其中不认识的元素 |
| `ssh.normalize` | `id`、`passphrase?`（缺省用条目的 Password） | `{fingerprint}`；附件改成不加密的 OpenSSH 格式 |
| `ssh.set_hosts` | `id`、`hosts:[…]`（最多 32 个；空 = 任何主机） | `{hosts}`（去掉首尾空白和重复后实际保存的）。每项是主机名（可用 `*`、`?`）、`[主机]:端口`，或服务器主机钥匙的指纹 `SHA256:…`；格式不对时 `INVALID`（`details.host`）。保存在条目的 CustomData `MoonPassword.SSH.Hosts`（KeePassXC 会保留但不执行），不产生历史版本 |
| `ssh.scan` | `dir?`（默认 `~/.ssh`） | `{dir, items:[{path, format, encrypted, algo, bits, fingerprint, comment, pub_path, pub_state, config_hosts, referenced_by:[{file,line}], problem, in_vault}], skipped, vault_unlocked, config_hosts:[{aliases,host_name?,user?,port?,identity_files}], config_partial}`。在目录内按内容识别私钥，每个候选文件最多读 64 KiB，响应只含元数据。`pub_state` 为 `ok`/`missing`/`mismatch`/`unverified`。密钥条目的 `config_hosts` 来自声明该 `IdentityFile` 的配置段，`referenced_by` 来自同目录脚本。 |
| `ssh.config_targets` | `path?`（默认 `~/.ssh/config`） | `{targets:[{alias,host_name?,port?,known_host?}],partial}`。仅解析配置及其 `Include` 文件。可确定的条目返回按 `HostName`、`Port` 或 `HostKeyAlias` 得到的查找名，供主机绑定选择器使用。 |
| `ssh.write_pub` | `path`（必须以 `.pub` 结尾）、`id` 或 `fingerprint` | `{pub_path, state}`：`created`/`same`/`mismatch`；从不覆盖 |
| `ssh.status` | — | `{supported, enabled, state, pipe, error, keys_loaded, keys_state, expires_at, vault_unlocked, ttl_s, allow_forwarding, connections}`；`state`：`listening`/`stopped`/`conflict`（管道名已被别的程序占用）/`error`/`unsupported`；`keys_state`：`empty`/`loaded`/`grace` |
| `ssh.keys` | — | `{vault_unlocked, keys:[…]}`：库解锁时为全部 SSH 条目（同 `vault.get` 的 `ssh` 块，外加 `group_path`）；锁着时只有 Agent 里已加载的钥匙 |
| `ssh.recent` | `limit?`（≤100） | `{uses:[…]}`，最新在前，字段同 `ssh.used` 事件 |
| `ssh.selftest` | — | `{ok, listed, fingerprints, error}`：作为客户端连自己的管道并列出钥匙（只列不签） |

除 `ssh.import/generate/export/set_agent/normalize/set_hosts` 外都可以在库锁着时调用。

Windows 的「查看 SSH 主机和迁移密钥」读取 `~/.ssh/config` 和 `Include` 文件。顶层 `config_hosts` 按声明列出实际命名的 `Host` 条目，即使目录内没有私钥也会返回。显示值来自对应配置段，全局默认值、通配规则和 `Match` 的生效结果由 OpenSSH 在连接时计算。读取失败或达到扫描上限时，`config_partial` 为真。

Windows 的「限定主机 → 设置」还可勾选配置里的主机，保存为 `known_hosts:<查找名>`。这种规则精确匹配 `known_hosts`（包括哈希条目），不会匹配同一地址的其他端口。无法静态确定的动态配置可手动绑定主机密钥指纹。

将对应私钥导入密码库并开启代理后，可继续用 `ssh 主机别名`。使用 `IdentitiesOnly yes` 时，OpenSSH 可用 `IdentityFile` 指定的公钥或原路径旁保留的匹配 `.pub` 选择代理里的私钥。迁移窗口会检查或补写公钥。指向其他代理或设为 `none` 的 `IdentityAgent` 仍按 OpenSSH 配置生效。

Windows 设置中的「配置 SSH 主机」可创建和编辑由 MoonPassword 管理的别名。现有别名可作为连接来源，新的配置保存在 `~/.ssh/moonpassword/hosts/`，由 `~/.ssh/config` 内的 `Include` 载入。

对使用标准 `~/.ssh/authorized_keys` 的 Unix 服务器，可通过旧钥匙或在 OpenSSH 窗口输入密码引导部署。应用生成 Ed25519 密钥，将私钥放在密码库，以服务器主机钥匙指纹限定 Agent 使用范围，再单独验证新密钥。部署成功且识别出旧公钥时，可确认移除该账号授权文件中的旧公钥。GitHub 和 Codeberg 用对应的账户网页注册公钥，再在应用中关联别名。

**主机绑定**（`hosts` 非空的钥匙）：ssh 连上服务器后会把服务器的主机钥匙告诉 Agent（session-bind，OpenSSH 8.9 起）。这样的连接上，Agent 只列出没有绑定的钥匙和绑定到这台服务器的钥匙，所以钥匙多时不会因为依次尝试而撞上服务器的尝试次数上限（默认 6 次）；登录签名请求如果来自别的服务器，拒绝（`ssh.refused`，`reason:"host"`）。主机名的判断靠用户的 `known_hosts`（`~/.ssh/known_hosts`、`known_hosts2` 和系统级的那份），哈希过的条目只能匹配不带通配符的主机名；指纹则不依赖 `known_hosts`。没有 session-bind 的连接（`ssh-add -l`、`ssh-keygen -Y sign` 这类本地用途，以及老版本客户端）照常列出全部钥匙；不是登录的签名（例如 git 提交签名）不受主机绑定限制。

**SSH 错误码**：`SSH_KEY_UNSUPPORTED`（`details.reason`：`dsa`、`sk`、`sshcom`、`putty1`、`pbes1`、`public_only`、`certificate`、`rsa_small`、`curve`、`multi`、`cipher`、`unknown`）、`SSH_KEY_PASSPHRASE`（`details.wrong`）、`SSH_KEY_CORRUPT`、`SSH_KEY_TOO_EXPENSIVE`（密钥派生参数超过核心上限，没有尝试解密）、`SSH_KEY_DUPLICATE`、`SSH_AGENT_UNAVAILABLE`。

**事件**（`ssh.used`、`ssh.refused`、`ssh.keys_needed` 合计限速约每秒 10 个，被丢弃的数量随下一个事件的 `suppressed` 报告；壳可随时用 `ssh.status`/`ssh.recent` 补齐）：

```json
{"type":"ssh.agent","state":"listening|stopped|conflict|error","pipe":"\\.\pipe\openssh-ssh-agent","error":""}
{"type":"ssh.keys","state":"loaded|grace|cleared|expired","count":2,"keys_state":"grace","reason":"idle","expires_at":1790000000}
{"type":"ssh.used","at":1790000000,"fingerprint":"SHA256:…","title":"GitHub","entry_id":"…","client":[{"pid":1,"name":"ssh.exe","path":"…"},{"pid":2,"name":"git.exe"}],"host":"github.com","host_key_fp":"SHA256:…","user":"git","kind":"userauth|sshsig|other","namespace":"git","forwarded":false,"result":"signed","reason":""}
{"type":"ssh.refused", …同上…, "result":"refused","reason":"forwarding|no_keys|unknown_key|host|error"}
{"type":"ssh.keys_needed","client":[…]}   // 有程序来要钥匙，但一把也没加载（库锁着）；每分钟最多一次
```

`client` 是发起请求的进程和最多 4 层父进程，只用于显示，不作为安全判断；`host` 只在 known_hosts 里有明文主机名时才有。

## 3.8 解码与资源限制（参考）

读取任何 KDBX 文件（打开、同步、导入、备份）都会先检查结构：条目 ≤ 20000、分组 ≤ 2000 且嵌套 ≤ 16 层、每个条目的附件 ≤ 64 个、历史 ≤ 100 项、单个文本字段 ≤ 4 MiB、全部文本 ≤ 64 MiB、解压后 ≤ 192 MiB；附件名里的路径字符会被替换（`/`、`\`、`:`、控制字符、开头的点）。不符合的文件整体拒绝（`VAULT_UNSUPPORTED`）。密钥派生成本低于下限（Argon2d 16 MiB × 2 次，或旧式 AES-KDF）的库在打开时**自动升级**为标准成本并重新保存（原文件进快照）。CSV 导出对文本列里以 `=`、`+`、`-`、`@`、制表符开头的值加一个前导 `'`（密码列和 TOTP 列不改），导入时去掉；CSV 导入的分组路径最多 15 层。

## 4. 壳层必须遵守的规则

1. **剪贴板：** 复制机密一律用 `vault.copy`（不是 `reveal`），然后：
   - Windows：写入时同时设置 `ExcludeClipboardContentFromMonitorProcessing`、`CanIncludeInClipboardHistory = 0`、`CanUploadToCloudClipboard = 0`；
   - Android 13+：`ClipDescription.EXTRA_IS_SENSITIVE = true`（`ClipboardAccess.writeText(..., sensitive = true)` 已支持）；
   - 在 `clear_after_s` 秒后清除，**仅当剪贴板内容仍是刚才写入的那份**（Windows 用 `GetClipboardSequenceNumber` 比对；Android 比对内容或描述）；
   - 不要把这段文本写进日志、通知、收件箱、最近使用列表。
2. **屏幕：** 库的界面默认不能被截屏/录屏/出现在最近任务缩略图里：Android 用 `FLAG_SECURE`（库页面与自动填充解锁/保存界面；默认开，可在主设置页关闭），Windows 用 `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)`（密码库设置里可关掉，默认开）。关闭只用于开发排查，界面必须明确警告。
3. **显示：** 机密默认遮盖，点“显示”才展开，展开后在若干秒（建议 15 秒）或离开页面时自动再遮盖；`vault.reveal` 的结果用完立即丢弃引用。
4. **锁定：** 系统锁屏/睡眠/切换用户、应用进入后台超过设定秒数 → `vault.lock`。锁定后清空列表、详情、搜索框、剪贴板中仍是我们写入的那份。
5. **危险操作再确认：** 导出（KDBX/CSV）、改主密码、清空回收站、永久删除、整库恢复，都要先弹确认；导出还要重新输入主密码（核心也会校验）。
6. **快速解锁（可选）：** 壳可以把主密码用设备密钥保护后保存（Android：Keystore + `BiometricPrompt`，`setUserAuthenticationRequired(true)`，生物识别变化即失效；Windows：暂不提供），解锁时取出并调用 `vault.unlock`。**有效期由壳的设置决定（默认 72 小时，可设 0 = 永不过期）；无论有效期多长，重启后或连续 5 次失败后必须重新输入主密码。** 核心不知道也不保存这个包装。
7. **kind 原样往返：** 壳不认识的 kind（例如旧版本遇到 `ssh`）必须原样保存，不能在编辑后改成 `login`；“认识的 kind”和“编辑器可以新建的 kind”是两张表（`ssh` 只能由导入/生成创建）。
8. **不要**缓存条目详情或机密到磁盘，不要把库内容放进系统备份（Android `allowBackup="false"` 已满足）。
9. **文案**中不得出现“忘记主密码可以找回”之类的话：忘记主密码就是永久无法打开，请在创建时明确告知，并引导用户做一份 `vault.export_file` 备份和（可选）密钥文件。

## 5. 与剪贴板同步的关系

- 密码库复制的内容不会被自动广播（`vault_origin`，登记有效 1 小时）；用户显式用 `clip.send` 发送仍然可以。
- 收到的剪贴板文本若被识别为敏感，依然永远不会自动写入（`plan-auto-broadcast.md`）。
- 多设备同步见 §3.6：匹配对设备默认自动同步、每台保存完整副本；传输的是加密文件，合并在本机解锁后进行；单台退出开关仍可用。
