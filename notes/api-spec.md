# notes：门面接口（`notes.*`）

> 状态：**已实现（Go 核心 + 测试）**；双端界面见 [docs/development-plan.md](docs/development-plan.md) §8。
> 通用约定（`Call(method, paramsJSON)` 返回 `{"ok":true,"result":…}` / `{"ok":false,"error":{code,message,retryable?,details?}}`，`NextEvent(timeoutMs)` 事件流）沿用主仓库 `docs/api-spec.md`。
> 字段名一律 snake_case。**所有 `*_at` 字段都是 Unix 毫秒**（主仓库其它方法用秒，注意区别）。数据模型见 [docs/sync-model.md](docs/sync-model.md)；传输层见 [docs/architecture.md](docs/architecture.md) §4。
> 实现：`core/notes/`（业务）、`core/notes/markdown/`（解析与规范化）、`core/facade/notes.go`（本表的方法）、`core/store/notes.go`（存储）。

## 1. 笔记 CRUD

| 方法 | 参数 | 结果 | 备注 |
| --- | --- | --- | --- |
| `notes.list` | `{limit?, cursor?, deleted?}` | `{items:[{note_id,title,updated_at,deleted,preview,conflict_of?}], next_cursor?}` | 不含正文；按 `updated_at` 倒序；`deleted:true` 列回收站；`limit` 缺省 100、最大 500；`title` 为空时核心用正文首个文本块代替（标题最多 80 字符、不含 Markdown 符号）；`preview` 是纯文本，最多 160 字符（标题是由正文推导出来时，预览不重复它） |
| `notes.get` | `{note_id, rev_id?}` | `{note_id,title,body,format_version,rev_id,parents,created_at,updated_at,deleted,origin_device,rev_count,is_head,conflict_of?}` | `rev_id` 缺省取最新修订；`created_at` 是笔记创建时间，`updated_at` 是该修订的时间；`origin_device` 为设备 id，或 `"merge"`（合并产生的修订） |
| `notes.create` | `{title?, body?}` | `{note_id, rev_id}` | 标题与正文都可以为空；正文先被规范化（见 editor-and-markdown §2） |
| `notes.update` | `{note_id, base_rev_id?, title?, body?}` 或 `{note_id, base_rev_id?, patch:[{block_id, body}]}` | `{rev_id, changed, merged, conflict_note_id?}` | 给出 `title`/`body` 即替换；`patch` 按块替换（`body` 为空串表示删除该块），与 `body` 互斥。`base_rev_id` 缺省为最新修订；它不是最新修订时，编辑被合并进最新修订（不同块的改动合并，`merged:true`；同一块冲突时较新的是正文，较旧的成为冲突副本，返回 `conflict_note_id`）。相对 `base_rev_id` 没有变化时不产生修订，返回 `changed:false` 与笔记当前的 `rev_id`（即使 `base_rev_id` 已过时，也不会把别处的修订误当成这次编辑的结果）；产生了修订则 `changed:true`。回收站里的笔记不能编辑（`CONFLICT`） |
| `notes.delete` | `{note_id}` | `{rev_id}` | 写墓碑修订，进回收站；已删除时是空操作 |
| `notes.restore` | `{note_id}` | `{rev_id}` | 从回收站恢复；未删除时是空操作 |
| `notes.history` | `{note_id, limit?, cursor?}` | `{items:[{rev_id,title,body_hash,origin_device,created_at,parents,deleted}], next_cursor?}` | 本机持有的修订，按到达顺序倒序 |

## 2. 渲染与编辑辅助

| 方法 | 参数 | 结果 | 备注 |
| --- | --- | --- | --- |
| `notes.render` | `{note_id, rev_id?}` 或 `{body}` | `{note_id?, rev_id?, blocks:[RBlock], toc:[{block_id,level,text}]}` | 核心解析出的块树，**两端只按它绘制，不自己解析 Markdown**。结构见 §2.1。给 `body` 则渲染**未保存的文本**（实时预览，结果不含 `note_id`/`rev_id`），与 `note_id` 互斥 |
| `notes.normalize` | `{body}` | `{body_norm, body_hash}` | 预演规范化结果，让壳知道"存进去会变成什么样" |
| `notes.search` | `{query, limit?, cursor?}` | `{items:[{note_id,title,updated_at,snippet,highlights:[{start,end}],title_highlights:[{start,end}],conflict_of?}], next_cursor?}` | 全文搜索未删除的笔记。`query` 按空白切成最多 8 个词，**每个词都要出现**（不区分大小写的子串，所以中文不需要分词）；标题命中排在前面，其次出现次数多的，再其次较新的。`highlights`/`title_highlights` 是 `snippet`/`title` 里的命中区间，**单位是 Unicode 码点**（不是 UTF-16 单元，壳要自己换算）。`limit` 缺省 30、最大 100。空查询返回 `INVALID`。索引只在内存里（笔记在库里是加密的），第一次搜索时建立，之后增量刷新 |

### 2.1 渲染树（`notes.render` 的 `blocks`）

块（`RBlock`）：

```jsonc
{"block_id":"b_…","kind":"heading|paragraph|code|quote|list|table|hr|html|unknown","raw":"该块的规范化 Markdown",
 "level":2,                       // heading：1..6
 "inline":[Inline…],              // heading、paragraph
 "lang":"go","info":"go title=x","text":"代码…",   // code（text 无结尾换行）；html/unknown：text 是原文
 "children":[RBlock…],            // quote
 "ordered":true,"start":1,"tight":true,"items":[{"checked":null|true|false,"blocks":[RBlock…]}],  // list
 "align":["none","left","center","right"],"header":[[Inline…]],"rows":[[[Inline…]]]}              // table
```

行内（`Inline`）：`{"t":"text","text":"…"}`、`{"t":"em|strong|del","children":[…]}`、`{"t":"code","text":"…"}`、
`{"t":"link","href":"…","title":"…","children":[…]}`、`{"t":"image","href":"…","alt":"…","title":"…"}`、`{"t":"br"}`（硬换行）、`{"t":"softbr"}`。

- 内联 HTML 与不支持的语法按纯文本/`html`/`unknown` 块保留原文，**绝不执行**。
- 空字段会被省略（`omitempty`），壳必须容忍缺失；`toc` 永远是数组。`start` 对有序列表**总是存在**（即使是 0），非有序列表没有它。
- 块 id 由内容决定：改动哪一块只会改变哪一块的 id（供 `notes.update` 的 `patch` 使用）。

### 2.2 附件

笔记用普通的 Markdown 图片/链接引用附件，目标是 `blob:<64 位十六进制 SHA-256>`：图片是 `![名字](blob:…)`，其它文件是 `[名字](blob:…)`。渲染树里它就是 `image`（`href:"blob:…"`）或 `link`（`href:"blob:…"`）行内节点；**壳要自己识别 `blob:` 前缀**，而不是当作网址打开。每个附件最大 32 MiB，相同内容只存一份。同步只搬运"某篇笔记当前文本引用着"的附件（包含回收站里的笔记）；对端没有的附件在下一轮同步到达，到达时发出 `notes.attachment` 事件。

| 方法 | 参数 | 结果 | 备注 |
| --- | --- | --- | --- |
| `notes.attachment_add` | `{path}` | `{blob_id,name,mime,size,present:true,markdown}` | 把绝对路径上的文件存为附件。`markdown` 是应当插入笔记的文本（`image/png|jpeg|gif|webp|bmp` 是图片语法，其余——包括 SVG——是链接语法；名字里的 `[` `]` `\` 已转义）。壳要把剪贴板图片/相册/相机图片先写成临时文件再调用。超过 32 MiB 返回 `TOO_LARGE`；不是文件/读不到返回 `INVALID` |
| `notes.attachment_get` | `{blob_id}` | `{blob_id,name,mime,size,present}` | `present:false` 表示笔记引用了它，但文件还没同步到本机（其余字段为空）：壳显示占位，收到 `notes.attachment` 事件后再取 |
| `notes.attachment_export` | `{blob_id, path}` | `{blob_id,name,mime,size,present:true}` | 把附件写到绝对路径（已有的普通文件会被替换；父目录必须已存在，否则返回 `INVALID`）。壳用它把图片写进缓存目录来显示，或写到用户选的位置来打开/保存。没有该附件返回 `NOT_FOUND` |
| `notes.attachment_cleanup` | — | `{removed, freed_bytes, kept}` | 删除"任何笔记的任何修订（含历史）都不再引用、且已存在超过 7 天"的附件。由用户手动触发 |

### 2.3 导入与导出

| 方法 | 参数 | 结果 | 备注 |
| --- | --- | --- | --- |
| `notes.export` | `{path, include_trash?}` | `{path,count,bytes}` | 在绝对路径 `path` 写一个 ZIP（已存在则返回 `INVALID`，绝不覆盖）。每篇笔记一个 `.md`，文件名来自标题（非法字符换成 `_`，重名加 ` (2)`），文件开头是 front matter（`title`、`id`、`created`、`updated`），后面是规范化 Markdown。`include_trash:true` 时回收站的笔记放在 `_trash/` 下。**附件不在导出里**（笔记里的 `blob:` 链接会保留在文本中） |
| `notes.import` | `{path}` | `{imported, skipped:[{name,reason}], note_ids}` | `path` 是绝对路径的 `.zip`、单个 `.md`/`.markdown`/`.txt` 文件，或一个文件夹（递归，跳过以 `.` 开头的目录和符号链接）。**总是新建笔记**，不会覆盖现有笔记（所以重复导入会得到副本）；路径以 `_trash/` 开头的文件（`include_trash` 导出的回收站笔记）导入后会回到回收站。标题取 front matter 的 `title`，没有就用文件名。大于 1 MiB、含 NUL 的文件被跳过并说明原因。`note_ids` 是最多前 5 篇**未删除**的新笔记。导入后只发最多 50 个 `notes.changed`，壳应当重新读列表 |

## 3. 同步

| 方法 | 参数 | 结果 | 备注 |
| --- | --- | --- | --- |
| `notes.sync_status` | — | `{peers:[{peer_id,name,connected,state,detail?,last_sync_at,behind,conflict_count}], pending_push}` | `state`：`idle`/`syncing`/`error`/`peer_old`（对端笔记格式更新，需要升级本应用）；`last_sync_at` 为 0 表示从未成功；`conflict_count` 是全局未处理的冲突副本数 |
| `notes.sync_now` | `{peer_id?}` | `{started:true}` | 异步，立即对该设备（缺省：所有设备）发起一轮；**只在拨号一侧起作用**（监听一侧只应答，等对方来拉/推）。未配对的 `peer_id` 返回 `NOT_PAIRED` |
| `notes.sync_conflicts` | `{limit?, cursor?}` | `{items:[{note_id,conflict_note_id,title,created_at}]}` | 冲突副本列表；`note_id` 是原笔记，`conflict_note_id` 是副本（它本身是一篇普通笔记，`conflict_of` 指向原笔记） |
| `notes.sync_resolve` | `{note_id, conflict_note_id, action:"keep_main"\|"keep_conflict"\|"merge"}` | `{rev_id}` | `keep_main` 丢掉副本；`keep_conflict` 把副本的标题与正文（去掉说明块）放回原笔记；`merge` 把副本正文接在原笔记之后（以 `---` 分隔）。三者都会把副本移入回收站。返回原笔记当前的 `rev_id` |

## 4. 事件（`NextEvent`）

```jsonc
{"type":"notes.changed","note_id":"nt_…","rev_id":"r_…"}                      // 本机或对端产生的可见变化（新建、编辑、删除、恢复、合并、同步到达）
{"type":"notes.sync.updated","peer_id":"dev_…","state":"syncing|idle|error|peer_old","behind":3}
{"type":"notes.sync.conflict","note_id":"nt_…","conflict_note_id":"nt_…","reason":"diverged"}  // 产生了冲突副本
{"type":"notes.sync.conflict","note_id":"nt_…","reason":"deleted_elsewhere"}   // 另一端删除了这篇，但本机有较新的编辑，编辑被保留
{"type":"notes.attachment","blob_id":"<64 hex>"}                                // 一个附件刚到达本机（重新显示引用它的笔记）
```

- 事件**不携带正文**，壳用 `notes.get` 按需拉取。
- 与 `clip.*` / `vault.*` 事件并列在同一个 `NextEvent` 流中，壳的事件路由增加 `notes.` 分支。

## 5. 开放范围（安全）

- **不**加入命名管道 / CLI 白名单（与 `vault.*` 同样的理由）；`core/pipe` 有测试保证。
- 同步路由（`/v1/notes/*`）只对已配对且未撤销的设备开放，其余返回 `NOT_PAIRED`；每个请求都重新读取配对状态。
- 错误码：沿用主仓库格式，新增 `NOT_FOUND`、`CONFLICT`、`UNSUPPORTED_FORMAT`、`NOT_IMPLEMENTED`；`TOO_LARGE`（正文 > 1 MiB、标题 > 1 KiB）、`INVALID`（`note_id` 格式、游标、互斥参数等）、`NOT_PAIRED`、`NOT_INITIALIZED` 为已有码。

## 6. 数据格式要点

- `note_id`：`nt_` + 26 位小写 base32；`rev_id`：`r_` + 32 位十六进制（内容寻址）；`block_id`：`b_` + 16 位十六进制。壳应把它们当作不透明字符串。
- 正文始终是规范化 Markdown（LF 换行、结尾一个 `\n`）；`notes.get` 返回什么，`notes.update` 就应当回传什么，不必自己再规范化。
- 修订可以有多个父修订（合并修订），所以 `parents` 是数组。

## 7. 兼容与迁移

- 数据库迁移 V7、V8 随核心启动自动执行（新增 `notes`、`note_revs`、`note_peer_state`、`note_blobs`、`note_blob_refs` 五张表），旧库就地升级，原有数据不变。迁移失败会使整个核心无法打开数据库（与其它迁移一致），没有单独的"只关闭笔记"模式。
- `notes.*` 随主仓库 `VERSION_BASE` 单调演进；线路协议新增 `/v1/notes/*` 路由，旧版本设备不认识这些路由（返回 404），双方的剪贴板与密码库同步不受影响。
