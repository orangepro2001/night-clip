# store 数据层交接（S1 完成）

> 写于 2026-10-26。**范围被限定在 S1**：门面没有接线，产品行为与本次改动前完全一致。
> 项目级交接仍是 [HANDOFF.md](HANDOFF.md)；设计文档在 [`../docs`](../docs)（`architecture.md` §5、`development-plan.md` §3.2）。

## 0. 一页摘要

- 新增 `core/store` 包：SQLite schema v1（10 张表 + 4 个索引）、幂等迁移、AES-256-GCM 字段加密、以及一组数据层 API。
- 新增依赖 `modernc.org/sqlite v1.61.0`（纯 Go，无 cgo；`go.mod` 里已是直接依赖）。
- 新增两个稳定错误码 `DB_UNAVAILABLE`、`KEY_INVALID`（只在 `proto/errors.go`，`api-spec.md` **尚未**同步）。
- **没有接线**：`facade.Init` 仍然调用 `identity.Generate`，`device_id` 每次启动仍会变；`go list -deps` 确认 DLL/AAR 的依赖图里没有 modernc，**二进制体积与行为都没变**。
- 验证结果：`go vet ./...`、`go test -race ./...`（14 个 store 测试）、`make dll-smoke` 全绿；`store` 在 `windows/amd64`、`android/arm64` 下交叉编译通过。
- 未跑：`make dll-wine`、`make test-interop`、`make aar`、`make apk`、完整 `make check`。原因：本增量没有改这些路径，也没有任何二进制链接 store（见上一条）。
- 没有 git 仓库、没有提交。

## 1. 变更清单

| 文件 | 状态 | 内容 |
| --- | --- | --- |
| `core/store/db.go` | 新增 | `Open`/`Close`/`Path`、PRAGMA、单连接池、`seal`/`unseal`/`rowAAD`、`dbErr` |
| `core/store/migrate.go` | 新增 | `schema_version` 表、`migrations` 切片、事务化迁移、版本过新检测 |
| `core/store/store_test.go` | 新增 | 6 个测试：建表/重开、坏 key、版本过新、seal 往返与篡改、并发访问、PRAGMA 生效 |
| `core/store/identity.go` | 新增（**超出 S1**） | `LoadOrCreateIdentity`，私钥封装、平台/名称校验、损坏检测 |
| `core/store/identity_test.go` | 新增（**超出 S1**） | 8 个测试：重开不变、私钥不以明文落盘、错 key、平台不符、改名、证书损坏、密钥截断、证书与密钥不匹配 |
| `core/store/peers.go` `links.go` `outbox.go` `inbox.go` `settings.go` `clipstate.go` `revoked.go` | 新增（**超出 S1**） | 数据层 CRUD，**没有专门单测**，也没有生产代码调用 |
| `core/store/doc.go` | 删除 | 包注释并入 `db.go` |
| `core/proto/errors.go` | 修改 | 追加 `CodeDBUnavailable`、`CodeKeyInvalid` |
| `core/go.mod` / `go.sum` | 修改 | `modernc.org/sqlite v1.61.0` 直接依赖；`go.sum` 54 行 |
| `.gitignore` | 修改 | 加 `.gocache/`（本机沙箱的 Go 构建缓存，见 §6） |

`go.mod` 新增的间接依赖：`modernc.org/libc v1.79.0`、`mathutil`、`memory`、`github.com/ncruces/go-strftime`、`dustin/go-humanize`、`mattn/go-isatty`、`remyoudompheng/bigfft`、`google/uuid`。

## 2. 已验证 / 未验证

| 项 | 状态 | 证据 |
| --- | --- | --- |
| `store` 单测 14 个 | ✅ | `go test ./store/`（约 0.04 s）；`-race` 约 1.5 s |
| 全仓库 Go 单测 + race | ✅ | `go test -race ./...`（facade / identity / proto / store） |
| go vet / gofmt | ✅ | `make vet` |
| C ABI 冒烟（Linux `.so`） | ✅ | `make dll-smoke`：`status`/`core.started`/超时/未知方法/重复 init/关闭唤醒，全过 |
| `store` 交叉编译 | ✅ | `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./store`；`GOOS=android GOARCH=arm64` 同样通过 |
| DLL 链接 modernc | ❌ 不需要 | `go list -deps ./cmd/cbcore-dll \| grep -c modernc` → 0 |
| AAR 链接 modernc | ❌ 不需要 | `go list -deps ./mobile \| grep -c modernc` → 0 |
| Wine / C# interop / AAR / APK / `make check` | ⏭ 未跑 | 本增量未改这些路径；跑一遍是 S5 的事 |
| 门面接线 | ❌ 未做 | `facade.Init` 仍是 `identity.Generate` |
| 真实设备 | ❌ | 与 HANDOFF §6 相同，无变化 |

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

## 4. 超出 S1 的代码（必须先看这一节）

收到「只写到 S1 就结束」时，`identity.go` 和 7 个 CRUD 文件**已经写出来了**。按你的指示我没有继续往下做（门面、调用点、文档都没动），但这些代码留在了树里。它们的状态：

- `identity.go` + `identity_test.go`：**有 8 个测试，全绿**，实际上等于 S2 已做完。
- `peers.go` / `links.go` / `outbox.go` / `inbox.go` / `settings.go` / `clipstate.go` / `revoked.go`：编译通过、`go vet` 干净，但**没有专门单测**，也**没有任何生产代码调用**。`TestConcurrentSettingsAccess` 会顺带碰到 `settings` 表。
- 它们不改变任何现有行为：`facade` 没有 import `store`，DLL/AAR 依赖图里没有它们。

两条路，选一条：

**A. 保留（推荐）**
1. 先补 `store/crud_test.go`，覆盖：peers 的 put/get/list/find-by-fp/delete 级联；links 的默认值与 upsert；outbox 插入→列表（信封原文一致）→`(peer,seq)` 唯一冲突→sent/acked→过期清扫；inbox 的 `inserted=false` 去重、按 `(peer,seq)` 的冲突报错、状态流转、过期；settings 的 get/set/delete；clip_state 往返；revoked_fps 幂等。
2. 再进 S4（门面接线），顺序见 §5。

**B. 删除，回到纯 S1**
```sh
rm core/store/identity.go core/store/identity_test.go \
   core/store/peers.go core/store/links.go core/store/outbox.go \
   core/store/inbox.go core/store/settings.go core/store/clipstate.go core/store/revoked.go
```
`TestConcurrentSettingsAccess` 要改成直接 `INSERT INTO settings` 的并发测试；API 设计见 §5.3，可以照着重写。

## 5. 下一步（按依赖顺序）

### 5.1 S2：身份落盘（代码已存在，见 §4）
只剩「确认保留 + 在 S4 里接线」。注意 `LoadOrCreateIdentity(name, platform)` 的语义：平台不符是 `INVALID`（不是静默改名）；名称变了只更新 `name` 列；`device_id` 永远从证书 SPKI 派生并与库内比对。

### 5.2 S3：CRUD 单测
见 §4.A 的清单。补完后 `store` 才算「有测试的数据层」。

### 5.3 S4：门面接线 + 所有调用点
这是「让 `device_id` 真正稳定」的那一步，改动点：

1. `core/facade/facade.go`
   - `Init`：校验 `data_dir` 必须是**绝对路径**；`base64.StdEncoding`（可接受无填充）解码 `db_key_b64`，必须 32 字节；`store.Open(data_dir, key)`；`store.LoadOrCreateIdentity(cfg.DeviceName, cfg.Platform)`；把 `db` 存进 `Core`。
   - 任何一步失败：关闭已打开的库，`started` 保持 false，**不发** `core.started`。
   - `Shutdown`：`close(done)` 之后 `db.Close()`（幂等）。
   - `dispatch`：`closed` 之后一律 `NOT_INITIALIZED`。
   - `status` / `identity.get` 字段不变。
2. `core/facade/facade_test.go`：配置改为 `t.TempDir()` + 随机 32 字节；新增「同目录同 key 的两个 `New()` 得到同一 `device_id`」「缺 key / 短 key / 坏 base64 / 相对路径各自的错误码」「失败时没有 `core.started`」「Shutdown 后 `status` → `NOT_INITIALIZED`」。
3. `core/test/abi/smoke.c`：`mkdtemp("/tmp/clipbridge-smoke-XXXXXX")`，config 里加 `data_dir` 和一个固定的测试 key（base64 常量即可，仅测试）。
4. `core/test/abi/smoke_win.c`：`GetTempPathA` + `_mkdir`，同样的 key；其余断言不变。
5. `windows/ClipBridge.Windows.Tests/CoreInteropTests.cs`：`Path.Combine(Path.GetTempPath(), Guid…)` + `RandomNumberGenerator.GetBytes(32)` → base64。
6. `windows/ClipBridge.Windows/MainWindow.xaml.cs`：临时 key 文件 `%LOCALAPPDATA%\ClipBridge\devkey.b64`（首次生成），`TODO(WP9: DPAPI)`。
7. `android/.../CoreHost.kt`：`filesDir/clipbridge/devkey.b64`（应用私有目录），`TODO(WP10: Keystore)`。

> 第 6、7 条是临时方案：key 未加密，只靠 OS 文件 ACL / 应用沙箱。如果你不接受，就只改测试，让壳在 `Init` 处明确失败，并在 HANDOFF §5 记一条。

### 5.4 S5：验收与文档
- `make check` 全绿（注意 §6 的 GOCACHE / 工具缓存问题）。
- 记录体积变化：`core/dist/windows-amd64/clipbridge_core.dll`、`android/app/libs/clipbridge-core.aar`、APK（sqlite 会让它们变大，这是预期）。
- 文档：`../docs/api-spec.md` §1.4 加两个错误码；`../docs/development-plan.md` §3.2 勾 `store`；[HANDOFF.md](HANDOFF.md) 的 §1/§3.2/§4/§5(1)/§10/§11；[README.md](README.md) 第 17、39 行。

### 5.5 已实现的 API（删除后照此重写）

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

## 6. 构建与环境（本机特有，换机器可忽略）

- **Go 构建缓存是只读的**：沙箱下 `~/.cache/go-build` 不能写，任何编译都会报 `read-only file system`。本机做法：
  ```sh
  GOCACHE=/home/tony/dev/Bark/night-clip/.gocache make vet test dll-smoke
  ```
  该目录已加进 `.gitignore`。**不加 `GOCACHE` 的命令会失败**，这不是代码问题。
- **模块缓存也是只读的**：`go get` / `go mod tidy` 需要往 `~/go/pkg/mod` 写，必须一次性提升到 `danger-full-access`。本次已经执行过，`go.sum` 是完整的；日常 `go build` / `go test` 只读缓存，正常工作。
- 工具链位置见 [HANDOFF.md](HANDOFF.md) §1；`gomobile` 在 `~/go/bin`，`.NET SDK` 在 `~/.dotnet/dotnet`。
- 不需要 `sqlite3` CLI：驱动是进程内的。
- `modernc.org/sqlite v1.61.0` 要求 Go ≥ 1.26.0，本机 1.27.3。

## 7. 坑与已知限制（按踩坑概率排序）

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

## 8. 本次没有做的事（避免被误认为已完成）

- `facade` 未接线，`device_id` 仍不稳定（HANDOFF §5 第 1 条仍然成立）。
- 没有 `crud_test.go`（§4）。
- 没有改 `api-spec.md`（两个新错误码还没进规范）、`development-plan.md`、[HANDOFF.md](HANDOFF.md)、[README.md](README.md)。
- 没有跑 `make check` / `dll-wine` / `test-interop` / `aar` / `apk`。
- 没有 `git init`、没有提交、没有推送。
- 没有做 §5 第 10 条的规范补齐（SAS 排序、proof 编码、`/v1/pair` 限流）。
