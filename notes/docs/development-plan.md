# 开发计划：night-clip-notes

> 状态标记沿用主仓库：✅ 已实现且有测试；🟡 已实现/仅编译，未经真机验证；⛔ 计划中；❓ 需要真机或用户决策。
> 本计划中的每一项在落地前都**不会**出现在 `notes.*` 门面里（不做半成品 API）。

## 1. 总览

| 阶段 | 目标 | 交付物 | 验收 |
| --- | --- | --- | --- |
| **Phase 0：PoC** | 证明关键技术假设成立 | 格式往返测试、单笔记双端同步原型、编辑器可行性结论 | 往返测试通过；双端同步在一个进程内 + Wine/模拟器里跑通；编辑器选型有结论 |
| **Phase 1：MVP** | 可日常使用的最小笔记应用 | `core/notes` + `notes.*` 门面 + 两端笔记列表/编辑/同步 UI | 属性测试全绿；`make notes-check` 通过；两端真机同步（两设备）可用 |
| **Phase 2：增强** | 体验与规模 | 富文本块编辑器、附件、全文搜索、导入导出 | 对应的测试与真机验证清单 |

## 2. Phase 0：PoC

最初的计划是把实验放在 `notes/poc/` 里、结束后删除。实际做法有变：P0-1 ～ P0-3 的验收标准直接写成**产品代码的永久测试**（比一次性探针更有价值，且不会失效），所以没有 `notes/poc/`。P0-4 / P0-5 需要真实界面和真机，不能在 Linux 上完成。

| # | 实验 | 通过标准 | 结论与证据 |
| --- | --- | --- | --- |
| P0-1 | Markdown 规范化与往返 | 往返后字节等于 `body_norm` | ✅ 见"Phase 0 结果"。`core/notes/markdown` 的测试：幂等性属性测试（固定种子随机输入）、`Join(Split(x)) == Normalize(x)`、原生模糊测试 |
| P0-2 | 块级合并可行性 | "改标题 / 末尾追加 / 修改代码块"三类场景无冲突副本 | ✅ `TestMerge3Scenarios`、`TestConcurrentEditsOfDifferentBlocksMergeIdenticallyOnBothDevices`（改标题 + 末尾追加 = 无冲突副本；同一块的两处编辑一定产生副本，这是设计内的行为） |
| P0-3 | 传输层复用 | 大文件传输时笔记延迟可接受 | ✅ 结论：用同一 mTLS 端口上的独立 HTTP 路由，**不占用 WebSocket 通道**（§3）。没有做"大文件传输时的延迟测量"，因为路由与文件字节流是不同的 HTTP 请求，不共享帧队列；若真机上发现同一条 TCP 连接的排队问题，再单独评估 |
| P0-4 | 编辑器选型 | 两端最小可行编辑器 | 🟡 结论：MVP 用**源码编辑 + 核心渲染树预览**，两端自研、不引入依赖。实际手感（IME、滚动、长文）需真机 |
| P0-5 | Android 后台同步 | 无前台服务时延迟可接受或明确需要前台服务 | ❓ **未验证**。当前做法：应用前台立即同步 + 进程存活时每 10 秒轮询（沿用现有实时前台服务保活）。Doze 下的行为需要真机 |

### Phase 0 结果（需要登记的发现）

- 修订需要**多个父修订**（`parents` 数组），合并结果必须是"每台设备独立算出同一个修订"，而不是互相交换合并结果；否则三台设备并发时无法收敛。
- 冲突副本的 `note_id` 必须由被放在一边的内容决定，否则两台设备会各自生成一篇。
- 标题后缀里不能放设备名（只在本机已知）。
- 详见 [sync-model.md](sync-model.md) §9。

## 3. 传输层的两条路线（P0-3 决定）

| 路线 | 说明 | 优点 | 风险 |
| --- | --- | --- | --- |
| **A：复用同一条 WebSocket，按帧类型分流** | 笔记帧与剪贴板/文件帧共用连接 | 连接数少、实现简单 | 大文件传输会占用通道，笔记延迟受尾延迟影响 |
| **B：新开专用 notes 连接（同一 mTLS 配置）** | 独立连接与流量隔离 | 互不干扰、可独立限速 | Windows 需要第二个监听端口或路径；Android 拨号逻辑要复制一份 |

> **决定**：采用 B 的变体——同一端口、独立的 HTTP 路由（`/v1/notes/*`），不新开连接也不占用 WebSocket。理由与细节见 [architecture.md](architecture.md) §4。

## 4. Phase 1：MVP 工作分解

| 工作项 | 位置 | 状态 | 证据 / 验收 |
| --- | --- | --- | --- |
| 存储迁移 `notes` / `note_revs` / `note_peer_state` | `core/store` | ✅ | `core/store/notes_test.go`；旧库（v6）就地升级、其余迁移测试已随新表更新 |
| Markdown 解析/规范化/块/渲染树 | `core/notes/markdown` | ✅ | 约 300 个 golden 用例；20000 个随机行拼接 + 10000 个结构化 + 3000 个深嵌套文档的属性测试（幂等、`Join(Split(x)) == Normalize(x)`、JSON 确定性）；原生模糊测试 `FuzzNormalize`；1 MiB 病态输入的性能守卫；未与参考 CommonMark 实现逐例对照 |
| 笔记 CRUD + 修订图 | `core/notes` | ✅ | `service_test.go`（含 race） |
| 同步（head/digest/pull/push）、合并、冲突副本、墓碑 | `core/notes` | ✅ | `diff3_test.go`、`sync_test.go`：收敛属性测试、交换律、幂等、不丢内容、墓碑优先 |
| 门面 `notes.*` + 事件 | `core/facade/notes.go` | ✅ | `facade/notes_test.go`（两个真实 Core 经 mTLS 同步）；`core/pipe` 测试确保管道白名单不含 `notes.*` |
| Windows 笔记界面 | `windows/` | 🟡 | 编译通过、逻辑与真实核心的互操作测试通过；WPF 渲染与交互未实际运行 |
| Android 笔记界面 | `android/` | 🟡 | 编译、JVM 单元测试与用真实核心录制的回复回放测试通过；未在设备上运行 |
| Makefile 目标 `notes-check` | `Makefile` | ✅ | 与 `make check` 并列，不改变现有目标语义 |

## 5. Phase 2：增强

| 项 | 状态 | 说明 / 证据 |
| --- | --- | --- |
| 全文搜索 | ✅ 核心；🟡 两端界面（编译 + 单元/互操作测试，未运行） | `core/notes/search.go`、`search_test.go`、`facade/notes_test.go`。**与计划的差异**：内存索引而不是 FTS5（FTS5 会把明文写到磁盘，笔记在库里是加密的）。标签未引入 |
| 导入导出 | ✅ 核心；🟡 两端界面（同上；导出的 ZIP 不含附件） | `core/notes/portable.go`：ZIP（每篇一个带 front matter 的 `.md`）、导入 ZIP / 单个文件 / 文件夹，总是新建笔记 |
| 附件 | ✅ 核心；🟡 两端界面（选择/粘贴/拖放、预览、用系统程序打开、分享入口：同上） | 迁移 V8、`core/notes/blobs.go`、同步路由、`core/store/blobs.go`；两个真实 Core 经 mTLS 传附件的测试。图片粘贴/拍照由壳完成（写临时文件再 `notes.attachment_add`） |
| 块内按行合并 | ✅ | 清单里两人各改一项不再产生副本（见 sync-model.md §4） |
| 实时预览（`notes.render {body}`） | ✅ 核心；🟡 两端已接入（停顿 150 ms 后渲染未保存文本） | |
| 字符级 CRDT | 📄 已评估，**不采用** | [crdt-evaluation.md](crdt-evaluation.md) |
| 块级富文本（所见即所得）编辑器 | ⛔ **未做** | 现在两端是"源码编辑 + 格式工具栏 + 实时预览"。设计不变：底层仍是 Markdown，按 `block_id` 用 `notes.update` 的 `patch` 提交；需要真机反复调输入法与焦点，所以没有在没有设备的情况下硬做 |
| 修订链压缩、回收站物理清理 | ⛔ 有设计结论、未实现 | sync-model.md §8：需要双向确认水位，风险高于收益 |

## 6. 风险

| # | 风险 | 影响 | 缓解 |
| --- | --- | --- | --- |
| R1 | 富文本控件无法在"底层 Markdown"约束下承担编辑 | 体验打折或倒退到源码编辑 | Phase 0 P0-4 先验证；允许 MVP 用源码 + 预览两栏 |
| R2 | Android 后台同步不可靠 | 多端"不同步"观感 | 前台打开时立即同步 + 手动同步按钮 + 明确状态显示 |
| R3 | 修订链压缩与慢设备离线冲突 | 祖先缺失导致整篇拉取 | 压缩窗口按对端最低水位计算；降级路径已定义 |
| R4 | 与主刻板同步/密码库共享 `peer` 的回归风险 | 影响现有功能 | 笔记走独立的 HTTP 路由，`core/peer` 未修改；现有测试必须全部保持绿 |
| R5 | 冲突副本过多 | 用户困惑 | 块级合并优先；冲突提示可批量处理 |
| R6 | 真机验证未知 | 不能宣称可用 | 沿用主仓库规则：只编译不算验证，真机清单必须执行 |

## 7. 与主仓库的协作约定

- 本模块的改动**只在** `feature/notes` 分支；合并前必须保证主仓库 `make check` 全绿（包含 541 个 C# 测试、443 个 Android JVM 测试等现有基线）。
- 每个 Phase 结束时写一份交接快照（格式沿用主仓库 `HANDOFF-YYYYMMDD-HHMM.md`）。
- 不在本分支修改主仓库的 `README.md` / `STATUS.md`，除非合并时需要同步更新索引。

## 8. 当前状态

| 项 | 状态 |
| --- | --- |
| 设计与计划文档、ADR-001/002 | ✅ ADR 已 `Accepted` |
| Go 核心（存储、修订图、合并、同步、门面） | ✅ 有自动化测试（`make notes-check`） |
| Markdown 包 | ✅ 有自动化测试 |
| Windows / Android 界面 | 🟡 已编译、有单元/互操作测试；**从未在真实 WPF 窗口或设备上运行**（见"验证记录"） |
| 真机 / 真实 Tailscale 网络验证 | ❓ 未做 |
| 修订压缩、回收站清理、附件、全文搜索、导入导出 | ⛔ 未实现（Phase 2） |

### 验证记录（2026-10-27）

Phase 1 与 Phase 2 的最终代码树上，`make check`（vet、Go 全部包 `-race`、Linux 与 Wine 下的 DLL 冒烟、C# 互操作测试、Windows 应用 Release 构建、AAR、Android 单元测试与 debug APK）全部通过：

| 项 | 结果 |
| --- | --- |
| Go（`go test -race ./...`） | 全部包 ok，含 `core/notes`、`core/notes/markdown`、`core/store`、`core/facade`、`core/pipe` |
| C#（`windows/ClipBridge.Windows.Tests`，对真实 Linux 核心） | 935 通过 / 0 失败 |
| Windows 应用构建（Release，含 XAML 编译） | 0 警告 / 0 错误 |
| Android（`testDebugUnitTest` + `assembleDebug`） | 764 通过 / 0 失败（含用真实核心录制的 66 条回复的回放测试） |
| `make notes-check` | 本模块的 Go 包 + 两端笔记测试 |

一次整体运行中 `xfer`、`peer` 和 facade 的文件传输测试（与笔记无关的时序用例）超时失败；单独重跑这几个包通过（facade 文件传输测试连跑 3 次），再次整体运行全部通过。这台机器当时有一个占 50% CPU 的模拟器，判断为负载引起的偶发，没有找到别的原因。

**没有验证过**：真实 WPF 渲染与交互（布局、预览绘制与图片/附件、搜索结果的高亮、对话框、拖放、剪贴板图片粘贴、焦点丢失保存）；Android 设备上的界面、输入法组合输入、工具栏在软键盘上方的位置、选择器（`CreateDocument`/`OpenDocument`/`PickVisualMedia`）、FileProvider 打开附件、分享菜单、大图解码与内存；真实 Tailscale 网络上两台真机的同步（包括附件）；Doze 下的后台同步。Android 模拟器冒烟测试曾尝试，软件模拟的系统进程约 20 分钟后崩溃，没有结果。
