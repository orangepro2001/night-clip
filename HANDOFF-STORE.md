# store 数据层交接（S1–S5 完成）

> 写于 2026-10-26，同日更新。最初范围限定在 S1；随后用户指示继续推进（按推荐方案 A 保留已写代码），又完成了 S3（CRUD 测试）、S4（门面接线）、S5（验收与文档）。
> 项目级交接仍是 [HANDOFF.md](HANDOFF.md)；设计文档在 [`../docs`](../docs)（`architecture.md` §5、`development-plan.md` §3.2）。

## 0. 一页摘要

- `core/store`：SQLite schema v1（10 张表 + 4 个索引）、幂等迁移、AES-256-GCM 字段加密、身份落盘、peers/links/outbox/inbox/settings/clip_state/revoked_fps 的 CRUD。依赖 `modernc.org/sqlite v1.61.0`（纯 Go，无 cgo）。
- **已接线**：`facade.Init` 解析 `data_dir`（必须绝对路径）与 `db_key_b64`（标准 base64，带/不带填充皆可，必须解码为 32 字节），`store.Open` 后 `LoadOrCreateIdentity`。同一 `data_dir` + 同一 key，`device_id` 与 `fingerprint` 重启不变。
- 错误码：`INVALID`（配置问题）、`DB_UNAVAILABLE`（库打不开/迁移失败/密文损坏）、`KEY_INVALID`（换了 key）；已写入 `api-spec.md` §1.4。
- 失败路径：`Init` 任何一步失败都会关库、不发 `core.started`，且可以用正确配置重试。`Shutdown` 关库（幂等）；之后所有方法返回 `NOT_INITIALIZED`，再 `Init` 返回 `INVALID`。
- 壳：Windows `Interop/DevKey.cs`、Android `DevKey.kt` 各生成一个随机 32 字节 key，明文 base64 存在数据目录的 `devkey.b64`（用户已接受这一临时方案；`TODO(WP9: DPAPI)` / `TODO(WP10: Keystore)`）。key 文件损坏会报错，**不会**重建。
- 验证（2026-10-26）：`make check` 全绿——`vet`、`go test -race`（store 40、facade 15 顶层/25 含子用例）、`dll-smoke`、`dll-wine`、`test-interop`（2 个）、`build-windows-app`、`cli`、`aar`、`apk`。
- 体积：AAR 7.6 → 15.6 MB；release APK 15 → 28.9 MB；debug APK 50.3 MB（均含 arm64-v8a + x86_64）。若要压小，发布时只打 arm64（见 §6 第 12 条）。
- 提交：仓库 `git@github.com:orangepro2001/night-clip.git`，分支 `main`。

## 1. 变更清单

| 文件 | 内容 |
| --- | --- |
| `core/store/db.go` | `Open`/`Close`/`Path`、PRAGMA、单连接池、`seal`/`unseal`/`rowAAD`、`dbErr` |
| `core/store/migrate.go` | `schema_version` 表、`migrations` 切片、事务化迁移、版本过新检测 |
| `core/store/identity.go` | `LoadOrCreateIdentity`，私钥封装、平台/名称校验、损坏检测 |
| `core/store/{peers,links,outbox,inbox,settings,clipstate,revoked}.go` | 数据层 CRUD |
| `core/store/{store,identity,crud}_test.go` | 6 + 8 + 26 = 40 个测试 |
| `core/facade/facade.go` | `Init` 接 store；`parseDBKey`；`Shutdown` 关库；`dispatch` 在关闭后一律 `NOT_INITIALIZED` |
| `core/facade/facade_test.go` | 配置校验表驱动、重启身份稳定、错 key、平台不符、失败可重试、Shutdown 语义 |
| `core/test/abi/smoke.c` / `smoke_win.c` | 临时 `data_dir` + 测试 key；被拒配置不影响后续 init；关闭后 `NOT_INITIALIZED`；清理临时目录 |
| `windows/.../Interop/DevKey.cs`、`MainWindow.xaml.cs`、`CoreInteropTests.cs`、测试 csproj | 临时 key 文件；窗口传入 `db_key_b64`；互操作测试用临时目录（并测 `DevKey`） |
| `android/.../DevKey.kt`、`CoreHost.kt` | 同上，key 放 `filesDir/clipbridge/devkey.b64` |
| `core/proto/errors.go` | `CodeDBUnavailable`、`CodeKeyInvalid` |
| `docs/api-spec.md`、`docs/development-plan.md`（仓库外） | 错误码表补齐；`store` 勾选 |

另外：`MarkOutboxSent` 现在对已是 `sent` 的行也累加 `attempts`（重连后重发要计数），`acked`/`expired` 不会退回 `sent`。

## 2. 已验证 / 未验证

| 项 | 状态 | 证据 |
| --- | --- | --- |
| `store` 单测 40 个 | ✅ | `go test -race ./store/`；CRUD 测试做过变异检查（还原 `MarkOutboxSent` 修复后 `TestOutboxLifecycle` 失败） |
| `facade` 单测 15 个顶层 | ✅ | 含身份重启稳定、错 key → `KEY_INVALID`、失败无 `core.started`、Shutdown 语义 |
| 全仓库 vet / gofmt / race | ✅ | `make check` |
| C ABI（Linux `.so`） | ✅ | `make dll-smoke` |
| Windows DLL + SQLite 在 Wine 下 | ✅ | `make dll-wine` |
| C# 互操作（临时目录 + `DevKey`） | ✅ | `make test-interop`（2 个测试，对 Linux `.so`） |
| WPF 应用编译 | ✅ | `make build-windows-app` |
| AAR / debug APK / release APK | ✅ | `make aar apk`；release 另跑 `assembleRelease` |
| DLL / AAR 依赖图含 modernc | ✅（预期） | `go list -deps ./cmd/cbcore-dll`、`./mobile` 均含 modernc |
| 真实 Windows 上 WPF + 真实 DLL + `devkey.b64` | ❌ | 需要 Windows 或 CI |
| Android 真机上 `Core.init` + `devkey.b64` | ❌ | 需要真机 |
| 多进程同时打开同一 `data_dir` | ❌ | 无跨进程锁，见 §6 第 6 条 |

## 3. 设计要点（以及为什么）

### 3.1 schema v1

10 张表：`schema_version`、`settings`、`identity`、`peers`、`links`、`outbox`、`inbox`、`transfers`、`clip_state`、`revoked_fps`；索引 4 个（outbox 的 peer/state、outbox 过期、inbox 过期、inbox 接收时间）。

- `transfers` 只建表，没有 Go API（WP7 再补）。
- `outbox` 的 `UNIQUE(peer_id, sequence)` 是有意的：sequence 分配属于 `peer`，这里只保证「分配错了会立刻报错」，不会悄悄重复。
- `inbox` 的 `UNIQUE(peer_id, sequence)` 同理；去重入口是 `event_id` 主键。
- 时间戳统一 Unix 秒（`int64`），与信封里的 RFC3339 字符串互不影响。
- `transfers."offset"` **必须加引号**：`offset` 是 SQLite 关键字（这是一个真会踩的坑）。
- 迁移是 append-only：已经发布的 `migrationV1` 不要改，加版本请追加 `migrationV2`。

### 3.2 打开与迁移

- `Open(dir, key)`：key 必须 32 字节；`MkdirAll(dir, 0o700)`；库文件 `dir/clipbridge.db`。
- 用**普通文件名**而不是 `file:` DSN：Windows 路径和空格都不用做 URI 转义，PRAGMA 在打开后显式执行。
- `PRAGMA journal_mode=WAL` 必须用 `QueryRow`（这条语句有返回值，`Exec` 拿不到模式）；随后回读确认 `wal`，否则报 `DB_UNAVAILABLE`。
- `synchronous=NORMAL` + `foreign_keys=ON` + `busy_timeout=5000`。
- `SetMaxOpenConns(1)`：所有读写串行化，避免 SQLite 的文件锁竞争和 `-race` 下的抖动。代价是读也被串行——当前事件量下没问题；将来如果文件传输要并发读元数据，再评估调大并把写操作用互斥量包起来。
- **注意死锁**：连接池只有 1 条连接，`rows` 没 `Close` 之前再发查询会卡住。所有现有函数都 `defer rows.Close()`，新增代码要保持。
- 迁移每个版本一个事务，失败回滚，版本号只在提交时推进；库版本 > 代码版本 → `DB_UNAVAILABLE`（不静默降级）。

### 3.3 加密

- AES-256-GCM；密文 `nonce(12) || ciphertext || tag`；每次新随机 nonce。
- AAD = `table \x00 column \x00 primary-key`，例如 `identity.key_blob_enc\x00dev_…`、`outbox.envelope_enc\x00<event_id>`。密文搬到别的行/列会解不开。
- **加解密封装在 `store` 内部**（`seal`/`unseal` 不导出）：`OutboxRow.Envelope`、`InboxRow.Payload` 都是明文进出，AAD 不会用错。
- 没有额外的「key 校验哨兵」：错 key 在读身份私钥时就会失败并返回 `KEY_INVALID`。已知语义缺口：**全新空库**如果用了错 key，会生成一份用错 key 加密的身份，之后正确 key 打不开（届时报 `KEY_INVALID`）。
- 库内仍是明文的部分：设备名、kind、大小、时间戳、证书 DER（公钥材料）。敏感内容只有私钥、事件信封、payload 三处。
- 日志不打印任何 payload、私钥、key；`dbErr` 只包操作名和驱动错误。

### 3.4 错误码

| code | 何时 |
| --- | --- |
| `INVALID` | key 长度不对（配置问题）、`data_dir` 为空、平台与已存身份不符 |
| `DB_UNAVAILABLE` | 目录建不了、库打不开、迁移失败、schema 过新、身份行损坏、密文解不开（更可能是数据损坏，因为错 key 在 Init 早期就会失败） |
| `KEY_INVALID` | 身份私钥解封失败（典型原因：换了 `db_key_b64`） |

### 3.5 store 的边界

`store` **不做策略**：不分配 sequence、不重试、不扫 TTL、不判断该不该应用事件、不选拨号方向。它只负责「存进去、取出来、加密、去重入口」。这些规则属于 `peer` / `clip`。

## 4. 已实现的 API

```go
// db.go
func Open(dir string, key []byte) (*DB, error)
func (db *DB) Close() error
func (db *DB) Path() string

// identity.go
func (db *DB) LoadOrCreateIdentity(name, platform string) (*identity.Identity, error)

// peers.go
type Peer struct{ DeviceID, Name, Platform, CertFP string; TailnetAddrs []string; Port int
                   Role, State, ApplyMode string; PairedAt, LastSeenAt int64 }
func (db *DB) PutPeer(Peer) error
func (db *DB) GetPeer(deviceID string) (Peer, bool, error)
func (db *DB) FindPeerByFingerprint(certFP string) (Peer, bool, error)
func (db *DB) ListPeers() ([]Peer, error)
func (db *DB) DeletePeer(deviceID string) error

// links.go
type Link struct{ PeerID string; NextSeqOut, RecvCursor, UpdatedAt int64 }
func (db *DB) GetLink(peerID string) (Link, bool, error) // 无行时返回 {NextSeqOut:1}
func (db *DB) SetLink(Link) error

// outbox.go
type OutboxRow struct{ EventID, PeerID, Kind, State string; Sequence int64; Envelope []byte
                       CreatedAt, ExpiresAt, AckedAt int64; Attempts int; AckResult string }
func (db *DB) InsertOutbox(OutboxRow) error
func (db *DB) ListOutbox(peerID string, afterSeq int64, limit int) ([]OutboxRow, error)
func (db *DB) GetOutbox(eventID string) (OutboxRow, bool, error)
func (db *DB) MarkOutboxSent(eventID string) error
func (db *DB) MarkOutboxAcked(eventID, result string, at int64) error
func (db *DB) ExpireOutbox(now int64) (int64, error)

// inbox.go
type InboxRow struct{ EventID, PeerID, Kind, State string; Sequence int64; Payload []byte
                      ReceivedAt, ExpiresAt, AppliedAt int64 }
func (db *DB) InsertInbox(InboxRow) (inserted bool, err error)
func (db *DB) HasInbox(eventID string) (bool, error)
func (db *DB) GetInbox(eventID string) (InboxRow, bool, error)
func (db *DB) ListInbox(limit int) ([]InboxRow, error)
func (db *DB) SetInboxState(eventID, state string, appliedAt int64) error
func (db *DB) ExpireInbox(now int64) (int64, error)

// settings.go / clipstate.go / revoked.go
func (db *DB) GetSetting(key string) (string, bool, error)
func (db *DB) SetSetting(key, value string) error
func (db *DB) DeleteSetting(key string) error
type ClipState struct{ LastAppliedEventID, LastAppliedHash, LastLocalSentHash string; SuppressUntil int64 }
func (db *DB) GetClipState() (ClipState, error)
func (db *DB) SetClipState(ClipState) error
func (db *DB) AddRevokedFP(certFP string, at int64) error
func (db *DB) IsRevoked(certFP string) (bool, error)
```

## 5. 构建与环境（本机特有，换机器可忽略）

- **Go 构建缓存是只读的**：沙箱下 `~/.cache/go-build` 不能写，任何编译都会报 `read-only file system`。本机做法：
  ```sh
  GOCACHE=/home/tony/dev/Bark/night-clip/.gocache make vet test dll-smoke
  ```
  该目录已加进 `.gitignore`。**不加 `GOCACHE` 的命令会失败**，这不是代码问题。
- **模块缓存也是只读的**：`go get` / `go mod tidy` 需要往 `~/go/pkg/mod` 写，必须一次性提升到 `danger-full-access`。本次已经执行过，`go.sum` 是完整的；日常 `go build` / `go test` 只读缓存，正常工作。
- 工具链位置见 [HANDOFF.md](HANDOFF.md) §1；`gomobile` 在 `~/go/bin`，`.NET SDK` 在 `~/.dotnet/dotnet`。
- 不需要 `sqlite3` CLI：驱动是进程内的。
- `modernc.org/sqlite v1.61.0` 要求 Go ≥ 1.26.0，本机 1.27.3。

## 6. 坑与已知限制（按踩坑概率排序）

1. **`offset` 是关键字**：`transfers."offset"` 必须带引号，查询也是。
2. **单连接池**：`rows` 不关就再查询会死锁；`SetMaxOpenConns(1)` 是刻意的。
3. **`journal_mode` 不能 `Exec`**：它返回一行，必须 `QueryRow`。
4. **迁移不可改**：改 `migrationV1` 会让已有库与代码不一致；加版本请追加。
5. **空库 + 错 key**：会用错 key 生成身份，之后正确 key 打不开（`KEY_INVALID`）。这是「没有 key 哨兵」的代价，已记入设计决定。
6. **一个 data_dir 只能有一个核心**：没有跨进程锁，只靠 `busy_timeout` 串行；两个进程同时写会偶发 `database is locked`（映射成 `DB_UNAVAILABLE`）。DLL 的进程级单例和 Android 的 `CoreHost` 单例目前保证了这一点。
7. **WAL 在网络盘/某些同步盘上会失败**：`Open` 会直接报 `DB_UNAVAILABLE`，不会退化到 delete 模式。
8. **库内明文元数据**：设备名、事件 kind、大小、时间戳可见；payload/信封/私钥是密文。
9. **`inbox` 的 `(peer, seq)` 唯一约束**：乱序缓存若要用到相同 sequence 的占位行，得先在 WP2 里设计好（当前实现是直接插入，冲突即报错）。
10. **`go mod tidy` 之后 `modernc.org/sqlite` 才是直接依赖**：现在已经是；如果谁手改 `go.mod` 去掉它，`store` 会编译失败。
11. **测试用的 key 是常量**（`testKey(seed)`）：只用于测试，不要复制到壳里。
12. **体积**：modernc 把整个 SQLite（Go 转译版）链进二进制。Release APK 现在含两个 ABI，28.9 MB；发布构建只打 `android/arm64` 可以省掉约一半（真机基本都是 arm64，x86_64 仅模拟器需要）。
13. **壳里的 key 是明文文件**：见 §0。换成 DPAPI/Keystore 时，要写一次性迁移（读旧 `devkey.b64` → 封装 → 删除明文），否则已有数据库会被新 key 锁死。
14. **`MarkOutboxAcked`/`SetInboxState` 对不存在的行静默成功**（0 行受影响不报错）。调用方不要依赖它们来判断行是否存在，需要时先 `GetOutbox`/`GetInbox`。

## 7. 没做的事

- `transfers` 表只建了表，没有 Go API（WP7）。
- `peer` 还没有调用 outbox/inbox/links（WP2）；这些 API 的并发语义（单连接串行）已测，但「事务内递增 `next_seq_out` 并插入 outbox」这种组合操作还没有封装，WP2 需要一个 `db.Tx(func(tx))` 或专用方法。
- 没有 key 校验哨兵（§6 第 5 条）。
- Android 壳不处理 `Core.init` 异常（HANDOFF §5 第 7 条）。
- `facade` 的 panic 日志仍写 stderr。
- 真机/真 Windows 上的验证：WPF 里的 `DevKey` 与真实 DLL 加载没有跑过。
