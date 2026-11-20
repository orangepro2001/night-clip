# 整体架构：night-clip-notes

> 对应决策：[ADR-001](adr/001-notes-sync-and-storage.md)（同步与存储）、[ADR-002](adr/002-rich-text-and-markdown.md)（编辑格式）。
> 前置阅读：主仓库 `docs/adr/005-go-core-and-tailnet-p2p.md`（Go 核心 + 两个薄壳 + tailnet 直连）。

## 1. 结论先行

笔记模块**不是**第二个应用，而是 night-clip 的第三个业务域（前两个是剪贴板/文件、密码库）：

```text
Windows（WPF 壳）                                 Android（Kotlin Compose 壳）
  notes 界面 / 富文本编辑 / Markdown 预览             notes 界面 / 富文本编辑 / Markdown 预览
        │  P/Invoke                                     │  gomobile
        ▼                                               ▼
  Call("notes.*", json) / NextEvent(timeoutMs)    Call("notes.*", json) / NextEvent(timeoutMs)
        └──────────────────┬──────────────────────────────┘
                           ▼
              Go 核心  core/notes/（新包）
        笔记存储 · 同步引擎 · Markdown 解析/渲染数据 · 索引
              │                    │
              │ store（SQLite）    │ peer（同一 mTLS 服务器，新增 /v1/notes/* 路由）
              ▼                    ▼
         notes_* 表        已配对设备 ↔ 已配对设备（tailnet 直连）
```

**三条硬约束**（都来自现有架构，不新增组件）：

1. **一份业务逻辑**：存储、同步、Markdown 语义全部在 Go 核心。壳只做 UI 与平台适配（软键盘、文件选择、分享接收、系统字体）。
2. **无服务端**：数据只在已配对设备之间经 tailnet 的 mTLS 链路流动；不引入网盘、同步服务器或第三方账号。
3. **不干扰现有通道**：笔记同步走**独立的 HTTP 路由**，不复用剪贴板事件流的序号/游标/TTL 语义，也不碰密码库同步。

## 2. 组件与复用关系

| 组件 | 来源 | 说明 |
| --- | --- | --- |
| `core/identity` | **复用** | 设备 Ed25519 身份、自签证书、`device_id`、指纹。笔记同步的信任根不变 |
| `core/peer` | **复用（未修改）** | mTLS 服务器、证书固定、重连、`Handle` 路由注册、`Endpoint` 拨号客户端。笔记只通过 `Handle` 注册自己的路由（见 §4），`peer` 包本身没有改动 |
| `core/pairing` | **复用** | 配对 = 信任决定。笔记同步只对"已配对且未撤销"的设备开放 |
| `core/store` | **复用（扩展）** | 同一个 SQLite 库，schema 升到 v7，新增 `notes`、`note_revs`、`note_peer_state` 三张表（`migrationV7`） |
| `core/facade` | **复用（扩展）** | 新增 `notes.*` 方法；命名管道/CLI 白名单**不**加笔记方法（见 api-spec §5） |
| `core/proto` | **复用（扩展）** | 规范 JSON（修订 id 的哈希输入）、错误码格式；新增 `NOT_FOUND`/`CONFLICT`/`UNSUPPORTED_FORMAT`/`NOT_IMPLEMENTED` 四个码 |
| `core/notes` | **新增** | 本模块业务：笔记 CRUD、修订图、同步（HTTP 路由 + 拨号侧轮询）、head 解析与块级合并、冲突副本；搜索索引与导出是 Phase 2 |
| `core/notes/markdown` | **新增** | CommonMark + GFM 子集的解析与规范化；富文本块模型的序列化/反序列化 |
| `windows/ClipBridge.Windows` | **复用（新增视图）** | 新笔记页（源码编辑 + 渲染树预览）；P/Invoke 层与事件泵不变 |
| `android/app` | **复用（新增视图）** | 新笔记界面（Compose，源码编辑 + 渲染树预览）；`EventRouter` 增加笔记事件分支 |

**明确不复用**：密码库的 KDBX 存储与整库合并逻辑（笔记是逐条细粒度数据，合并规则不同）；剪贴板的回声/敏感内容过滤（笔记没有剪贴板语义）。

## 3. 数据模型（概要）

完整字段与同步语义见 [sync-model.md](sync-model.md) §2。实现：`core/store/migrate.go` 的 `migrationV7` 与 `core/store/notes.go`。

| 概念 | 说明 |
| --- | --- |
| `notes` 表 | 每篇笔记的**当前状态**（由 head 修订推导，可重建）：`note_id`（`nt_` + 随机 base32）、标题、预览、`head_rev`、`updated_at`、`deleted`、`conflict_of` |
| `note_revs` 表 | 不可变修订：`rev_id`（内容寻址）、`parents`（父修订数组）、`title`、`body`（规范化 Markdown）、`origin_device`、`created_at`、`deleted`、`conflict_of`，以及 `is_head` 标记。同步的是**修订**，不是整库 |
| `note_peer_state` 表 | 每个对端的同步记账：状态、上次成功时间、两端计数器、落后/领先数 |
| `note_blobs` / `note_blob_refs` 表 | 附件（按内容哈希、加密存储）与"每篇笔记当前引用了哪些附件"（迁移 V8），见 [sync-model.md](sync-model.md) §10 |
| 全文索引 | 只在内存里，不落盘（笔记在库里是加密的，FTS5 会写出明文索引），见 sync-model.md §11 |

标题、正文、预览在库里用现有数据库密钥（AES-256-GCM，绑定到行与列）加密，与身份私钥等私有列一致；`rev_id`、时间、`deleted` 等不涉及内容的列是明文，便于索引。

## 4. 传输层：笔记同步路由

决策（Phase 0 / P0-3 的结论，见 [development-plan.md](development-plan.md) §3）：**不**把笔记帧塞进剪贴板/文件共用的 WebSocket，而是在同一个 mTLS 服务器上加四条 HTTP 路由，做法与密码库同步（`core/vsync`）相同：

- `GET /v1/notes/head`、`GET /v1/notes/digest`、`POST /v1/notes/pull`、`POST /v1/notes/push`，以及附件的 `POST /v1/notes/blob_have`、`GET|PUT /v1/notes/blob/<id>`（流程见 [sync-model.md](sync-model.md) §3、§10）。
- **同一端口、同一证书固定、同一"只有已配对设备可访问"的检查**，不新增防火墙规则、不新增监听端口；每个请求都重新读取配对状态（撤销配对后立即拒绝）。
- **方向**：沿用现有规则——由拨号方发起；Android 只出站拨号，Windows 监听 tailnet 地址。监听侧只应答。
- **与剪贴板/文件通道隔离**：笔记不使用事件序号、发件箱、TTL，也就不会被大文件传输的尾延迟拖慢，不影响现有测试。
- **幂等**：`rev_id` 是内容哈希，重复推送是 no-op。
- **大小限制**：正文 1 MiB、标题 1 KiB；一次拉取/推送最多 400 个修订、约 6 MiB；请求体上限 10 MiB。超限被拒绝并给出明确错误，不静默截断。
- 对不认识这些路由的旧版本设备，请求得到 404，双方的剪贴板与密码库同步不受影响。

## 5. 门面接口

`Call(method, paramsJSON) → {ok:true,result}` / `{ok:false,error}`，与现有约定一致。方法清单见 [../api-spec.md](../api-spec.md)：`notes.list`、`notes.get`、`notes.create/update/delete`、`notes.history`、`notes.search`、`notes.render`（Markdown → 结构化渲染数据，供壳显示）、`notes.sync_status` / `notes.sync_now`。

**事件**（`NextEvent`）：`notes.changed`、`notes.sync.updated`、`notes.sync.conflict`。壳只订阅、不轮询。

## 6. 存储与一致性的分工

- **持久化**：笔记与修订全部落 SQLite（`core/store` 新迁移），崩溃/重启不丢；写路径使用事务，正文与修订、索引在一个事务内提交。
- **同步**：`core/notes` 拥有"本地状态 → 对端状态"的收敛逻辑；网络层只负责搬运字节。合并函数是**纯函数**（输入两端状态，输出合并结果），以便属性测试（交换律、幂等、收敛）。
- **渲染**：Markdown 解析在核心完成，壳拿到的是结构化的语法树/渲染指令，避免两端解析器行为漂移（尤其表格、任务列表、代码块）。壳可以在预览层做样式定制，但**不重写语义**。

## 7. 安全与隐私

- **信任**：只有已配对设备可同步；撤销配对后立即停止推送并拒绝拉取。
- **链路**：复用 tailnet 的 mTLS 与证书固定，不新增 payload 加密层（与主仓库 ADR-005 一致）。
- **静态**：笔记库在设备本地以现有数据库密钥保护（DPAPI/Keystore）；明文只在已解锁的进程内。
- **隐私边界**：笔记**永不**进入剪贴板自动广播路径（主仓库 `vault_origin` 的同类规则）；附件只在对端请求时传输，不做后台全量预取。
- **日志**：正文不写日志；只记录 `note_id`、`rev_id` 前缀与错误码。

## 8. 平台差异（壳层职责）

| 能力 | Windows（WPF） | Android（Kotlin Compose） |
| --- | --- | --- |
| 富文本编辑 | 具体控件选型见 [editor-and-markdown.md](editor-and-markdown.md) §4 | 同左 |
| 键盘/输入法 | 桌面键盘，快捷键 | 软键盘、`android:windowSoftInputMode`、IME 与滚动联动 |
| 分享接收 | 无 | `ACTION_SEND`（text/plain）→ 新建笔记 |
| 后台同步 | 随应用运行 + 托盘常驻 | 前台服务或 WorkManager 触发 `notes.sync_now`；受 Doze 影响，需实测 |
| 文件粘贴 | 剪贴板图片 → 附件 | 拍照/相册 → 附件 |

两端的**行为**必须一致；差异只允许在"触发时机"和"外观"两层。

## 9. 待决问题（Phase 0）与结论

1. ~~笔记帧是复用现有 WebSocket 连接还是新开专用连接？~~ **已决定**：都不是——用同一 mTLS 服务器上的 HTTP 路由（§4）。
2. Android 后台同步在 Doze 下的可靠性，以及是否需要前台服务常驻：**待真机验证**。目前 Android 在应用前台/打开笔记页时立即同步（`notes.sync_now`），应用进程存活期间（含已有的实时前台服务）每 10 秒轮询；没有为笔记单独新增后台机制。
3. 富文本编辑器选型：MVP 两端都用**源码编辑 + 实时预览**（核心渲染树绘制），不引入第三方控件；块级所见即所得编辑器留到 Phase 2（见 editor-and-markdown.md §4）。
4. 附件大小上限与是否允许视频：**已定**——任意文件，单个上限 32 MiB，不做视频专门处理（视频只是一个大文件，按普通附件链接显示）。
