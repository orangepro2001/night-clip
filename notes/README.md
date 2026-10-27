# night-clip-notes：去中心化笔记模块

> 状态：**Go 核心已实现并有自动化测试**；Windows / Android 界面的状态见 [docs/development-plan.md](docs/development-plan.md) §8。真机与真实 Tailscale 网络验证未做。
> 基线：`feature/notes` 分支，基于 `main @ e580d6e`。本目录放设计文档；代码在仓库的 `core/notes/`、`core/store/notes.go`、`core/facade/notes.go`、`windows/`、`android/`。
> 模块内文档索引：[docs/README.md](docs/README.md)。

## 1. 一句话定位

在 night-clip 现有 tailnet 直连架构上，新增一个**去中心化、无服务端的笔记应用**：Windows 桌面与 Android 两端同步，支持**富文本编辑器**与 **Markdown** 两种编辑方式。

## 2. 与 night-clip 主模块的关系

| 项 | night-clip（现有） | night-clip-notes（本模块） |
| --- | --- | --- |
| 产品力 | 剪贴板文本同步 + 文件传输 + 密码库 | 去中心化笔记（多端同步、富文本、Markdown） |
| 核心 | Go `core/`，JSON 门面 `Call`/`NextEvent` | **复用同一模式**，业务放 `core/notes/`（目标位置，见下） |
| 壳 | Windows WPF + Android Kotlin Compose | **复用同一壳模式与构建链路** |
| 信任与传输 | Tailscale tailnet 内 mTLS + 证书固定 + 配对 | **复用**：笔记作为独立数据通道，不引入新服务端 |
| 数据边界 | 剪贴板事件 | 笔记条目，与剪贴板/密码库数据隔离 |

**复用原则**：不出第二套协议、不复制身份/配对/链路代码。笔记模块在现有 `identity`、`peer`、`store`、`facade` 之上增量实现，新增部分全部带 `notes` 前缀或位于 `notes` 包内。

## 3. 范围与边界（本轮决定）

- **平台**：Windows 桌面（WPF）+ Android（Kotlin Compose）。不做 Web 端、不做 iOS/macOS/Linux。
- **前提**：设备在同一 Tailscale tailnet 内，复用现有身份与配对信任关系；**没有云端业务服务**。
- **同步模型**：按笔记条目同步（不是整库同步），离线可编辑，重连后补发；冲突策略见 [docs/sync-model.md](docs/sync-model.md)。
- **编辑格式**：Markdown 为权威存储，富文本编辑器做渲染与编辑；转换规则与编辑器选型见 [docs/editor-and-markdown.md](docs/editor-and-markdown.md)。
- **不做**（至少 MVP 不做）：协作实时编辑（字符级 CRDT）、共享笔记/多用户、端到端加密的独立密钥体系（沿用 tailnet/mTLS 链路保护）、第三方网盘同步、插件系统。

## 4. 目录结构与代码位置

```text
notes/                            ← 设计文档（本目录）
├── README.md                     本文件：定位、边界、导航
├── api-spec.md                   `notes.*` 门面方法（已实现，是壳层的契约）
└── docs/
    ├── README.md                 设计文档索引
    ├── architecture.md           整体架构与复用关系
    ├── sync-model.md             同步模型：修订图、合并、冲突、离线、最终一致
    ├── editor-and-markdown.md    Markdown 为权威、块模型、编辑器选型
    ├── development-plan.md       分阶段计划、验收标准、验证记录
    └── adr/                      notes ADR-001（同步与存储）、ADR-002（富文本与 Markdown）

core/notes/                       业务：CRUD、修订图、合并、同步路由与客户端
core/notes/markdown/              CommonMark + GFM 子集的规范化、块切分、渲染树
core/store/notes.go               notes / note_revs / note_peer_state 的持久化（迁移 V7）
core/facade/notes.go              `notes.*` 方法
windows/ClipBridge.Windows/       笔记页（WPF）
android/app/                      笔记界面（Compose）
```

## 5. 当前状态

| 项 | 状态 |
| --- | --- |
| 设计文档 | ✅ |
| ADR | ✅ `Accepted` |
| Go 核心（含合并与同步的收敛属性测试） | ✅ `make notes-check` |
| 双端 UI | 见 [docs/development-plan.md](docs/development-plan.md) §8 |
| 真机 / 真实 tailnet 验证 | ❓ 未做 |
| 修订压缩、回收站清理、附件、全文搜索、导入导出 | ⛔ Phase 2 |

状态标记沿用主仓库约定：✅ 已实现且有测试；🟡 已实现/仅编译，未经真机验证；⛔ 计划中；❓ 需要真机或用户决策。

## 6. 建议阅读顺序

1. [docs/README.md](docs/README.md) — 文档索引与阅读顺序
2. [docs/architecture.md](docs/architecture.md) — 模块怎么接进现有架构
3. [docs/adr/001-notes-sync-and-storage.md](docs/adr/001-notes-sync-and-storage.md) — 为什么这样存储和同步
4. [docs/adr/002-rich-text-and-markdown.md](docs/adr/002-rich-text-and-markdown.md) — 为什么以 Markdown 为权威
5. [docs/development-plan.md](docs/development-plan.md) — 下一步做什么
