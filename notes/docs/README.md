# night-clip-notes 设计文档索引

> 本目录只放**设计与计划文档**，不放实现代码。代码位置见 [../README.md](../README.md) §4。

## 阅读顺序

| 顺序 | 文档 | 回答的问题 |
| --- | --- | --- |
| 1 | [architecture.md](architecture.md) | 笔记模块怎么接进 night-clip 的 Go 核心 + 两个薄壳？复用什么、新增什么？ |
| 2 | [sync-model.md](sync-model.md) | 多端怎么同步？冲突怎么办？离线了怎么办？"最终一致"靠什么保证？ |
| 3 | [editor-and-markdown.md](editor-and-markdown.md) | 富文本和 Markdown 怎么共存？谁是权威？转换会丢什么？编辑器选哪个？ |
| 4 | [development-plan.md](development-plan.md) | 分几期做？每期的验收标准是什么？风险在哪？ |
| 5 | [../api-spec.md](../api-spec.md) | 壳层调用核心的 `notes.*` 方法、事件、渲染树长什么样？ |

## 决策记录（ADR）

| ADR | 标题 | 状态 |
| --- | --- | --- |
| [ADR-001](adr/001-notes-sync-and-storage.md) | 笔记同步与存储模型 | Accepted |
| [ADR-002](adr/002-rich-text-and-markdown.md) | 富文本与 Markdown 双格式编辑 | Accepted |

ADR 格式沿用主仓库 `docs/adr/`（状态 / 日期 / 决策者 / 相关文档 / 背景 / 决策 / 替代方案 / 结果）。本模块 ADR 的编号在模块内独立，文件名带 `notes-` 语义前缀以免与主仓库 ADR-007 混淆；引用时写"notes ADR-00x"。

## 文档与代码的关系

- **文档先行**，代码落地后以代码为准：与实现不一致的地方记在 [sync-model.md](sync-model.md) §9。
- **代码落地时**：每个 ADR 状态改为 `Accepted` 或 `Superseded`，并在 `development-plan.md` 登记实施状态与验证证据（测试名、`make` 目标）。
- **不写"已实现"**，除非有自动化测试或构建证据；这与主仓库 `STATUS.md` 的四种状态标记一致。

## 与主仓库文档的关系

| 主仓库文档 | 本模块相关处 |
| --- | --- |
| `docs/adr/005-go-core-and-tailnet-p2p.md` | 复用其"单一 Go 核心 + 两个薄壳 + JSON 门面"决策与 tailnet 直连传输 |
| `docs/adr/007-password-vault.md` | 参考其"独立包 + 前缀门面方法 + 同步合并规则 + 属性测试"的做法 |
| `docs/architecture.md` | 事件、发件箱、游标、TTL 的现有语义；笔记通道需要与它隔离，见 [sync-model.md](sync-model.md) §6 |
| `docs/api-spec.md` | `Call`/`NextEvent` 通用约定与错误码格式 |
| `HANDOFF.md` | 主模块当前状态与验证矩阵（本模块不改动它们） |
