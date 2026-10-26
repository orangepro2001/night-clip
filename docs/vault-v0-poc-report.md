# 密码库 V0 可行性验证报告：gokeepasslib（KDBX 4）

> 日期：2026-10-26。对应 [plan-vault.md](plan-vault.md) §4.2 与 §10 的 V0。  
> 被测：`github.com/tobischo/gokeepasslib/v3 v3.7.1`（MIT）。机器：i7-8700（12 线程）、32 GB、Go 1.27.3，全部 `CGO_ENABLED=0`。  
> 方法：由一个独立子代理在仓库之外（`/tmp/vault-poc`）完成，并由我复核：重跑其全部测试（通过），并直接读库源码核对了两个最关键的结论（见 §3）。PoC 代码和日志只在本机 `/tmp/vault-poc`，**没有**入库。  
> **最大的未验证项：没有与 KeePassXC / KeePassDX / Keepass2Android 做过互操作**（本机没有安装，也没有擅自安装）。

## 1. 结论

**可以使用这个库，但必须包一层并打少量补丁，不建议自己从零实现 KDBX 4。V1 放行前仍须通过真实 KeePassXC 的互操作测试。**

| # | 项目 | 结果 | 要点 |
| --- | --- | --- | --- |
| 1 | 纯 Go 构建与交叉编译 | 通过 | linux/windows 的 amd64/arm64、android/arm64 可编译（android/arm64 `go vet` 通过并能链接）；依赖闭包 4 个模块，无 cgo；Windows 测试二进制在 Wine 下通过。注意：android/amd64（模拟器）链接需要 cgo，真机 arm64 不受影响 |
| 2 | 往返无损 | 通过 | 4.0/4.1 × AES-256/ChaCha20/Twofish × 仅密码/密码+XML 密钥文件/原始密钥文件，共 18 组；嵌套 Unicode 分组、保护/非保护自定义字段、标签、过期、CRLF 备注、1.1 MiB 与 5 MiB 附件（SHA-256 一致）、历史、各级 CustomData、`otp`、回收站、DeletedObjects 都保留；另读写 8 个第三方（Generator=KeePass）夹具，规范化 XML 无损 |
| 2d | **Argon2id** | **不支持，且静默错误** | 只有 Argon2d 分支；其它任何 KDF UUID 都会落入 AES-KDF 分支。请求 Argon2id 的文件头写着 Argon2id，实际用 `Rounds=0` 的 AES-KDF（等于没有密钥拉伸），保存只花 0.35 ms（真 Argon2d 为 80–117 ms）。KeePassXC 生成的 Argon2id 库无法打开，报"密码错误" |
| 3 | 错误与损坏输入 | 通过 | 错密码/错密钥文件/缺密钥文件/空密码全部是错误；逐字节翻转（每种密码 5130 次）与所有截断长度：0 次 panic、0 次返回垃圾。例外：文件末尾 36 字节未被认证，追加垃圾被静默接受 |
| 4 | 性能 | 通过 | Argon2d：64 MiB/3 次/并行 2 在本机约 160 ms（RSS≈68 MiB），128 MiB 约 280 ms；峰值内存≈M+4 MiB。arm64/Android 用通用 Go 实现，约慢 1.2–1.4×（x86 上用 `purego` 标签实测）；低端手机按 5–10× 外推约 1–2.3 s（**外推，未在手机上测**）。5 MiB 附件解锁约多占 28 MiB |
| 5 | 模糊测试 | **有缺陷** | 合计约 6 分钟：原始字节输入未发现 panic；对"密钥正确的合法容器 + 任意载荷"发现 1 处 panic（`content.go:119`，内部头字段短于 4 字节时 `Uint32` 越界）；补 3 行长度检查后 100 秒无 panic。**6 分钟的模糊测试证据很弱，不能据此认为没有其它 panic** |
| 5b | 解析前的资源上限 | **没有** | HMAC 在密钥派生之后才校验，攻击者无需密钥即可构造文件头：Argon2 内存 1 GiB → 1.4 s/1.05 GB；约 4 GiB → 5.7 s/4.2 GB；迭代数 2³²−1 或 AES-KDF 轮数 2⁶³−1 → **挂死**（25 s 仍未结束，无法取消）。几百字节的文件就能让手机 OOM 或 ANR |
| 5c | 解压炸弹 | **没有上限** | 需要密钥（仅导入/同步场景）：1.0 MiB 的文件含 512 MiB 全零附件，解锁时 RSS 2.5 GB |
| 6 | 合并/同步 | **没有** | 源码里没有任何 merge/sync/conflict；plan-vault §5.3 的合并必须自己写 |
| 7a | 与 KeePassXC 等真实软件互操作 | **未验证** | 本机没有 `keepassxc`、`keepassxc-cli`、`kpcli`、`pykeepass` |
| 7b | 第二个独立实现交叉读 | 部分通过 | 找不到可用的第二个 Go KDBX4 库；子代理按规范用 Python 另写了读取器（含自己的 Argon2d），先在 3 个真实 KeePass 夹具上验证读取器本身，再读 gokeepasslib 写出的 24 个文件，**24/24 内容一致**。这不是 KeePassXC，所以闸门仍开着 |
| 8 | 许可 | 通过 | gokeepasslib MIT；tobischo/argon2、x/crypto、x/sys 为 BSD-3；无 GPL/LGPL/AGPL |

## 2. 风险与处置（进入 V1 的待办）

| 编号 | 问题 | 处置 | 需要补丁 |
| --- | --- | --- | --- |
| R1 | 解析前没有任何资源上限（见 5b） | 在调用 `Decode` 之前由包装层窥探文件头：拒绝 KDF 内存 > 256 MiB、迭代/轮数超过上限、文件大小超过上限；拒绝未知 KDF UUID | 否（包装层） |
| R2 | 解压炸弹 | 对 gzip 读取加 `io.LimitReader`；并限制附件总量 | 是（`decoder.go` 一行） |
| R3 | 内部头长度未检查导致 panic；库内没有 `recover` | 补长度检查；门面层统一 `recover`（本项目已有门面 panic 保护，但密码库调用必须不记录参数） | 是 |
| R4 | 写入端怪癖：KDBX4 终止块后多写 36 个零字节；ChaCha20 的 gzip 后有 1–16 字节填充（Python 严格 gzip 拒绝） | 补丁去掉；**必须用真实 KeePassXC 验证严格读取方是否接受** | 是 |
| R5 | **重新保存时沿用旧的 MasterSeed、IV、KDF 盐、内部流密钥**，ChaCha20 下同密钥同 IV 的密钥流在不同文件代际间重用（备份与同步副本都受影响） | 包装层每次保存都重新生成这些随机量（PoC 的 `Reseed()` 已验证可行）。**这是 V1 必须有的安全修复** | 否（包装层） |
| R6 | 不支持 Argon2id；未知 KDF 静默退化为无拉伸 | 先拒绝未知 KDF；是否补 Argon2id（`tobischo/argon2` 有 `IDKey`）取决于 KeePassXC/KeePassDX 是否读取它（仍待验证） | 可选 |
| R7 | API 陷阱：编码前必须锁定受保护值且之后保持锁定；重复解锁会破坏值；改 `CipherID` 不会调整 IV 长度，AES/Twofish 配默认 12 字节 IV 时 `Encode` panic | 包装层隐藏全部这些细节，对外只暴露不可变的快照 | 否 |
| R8 | 重新保存会丢弃未知 XML 元素（前向兼容风险；已知元素如 AutoType Association 保留） | 记录为限制；如果日后 KeePassXC 新增字段，会在保存时被我们丢弃，需要在说明书里提示或补丁保留 | 可选 |
| R9 | 机密以 Go 字符串保存，无法擦除；"密码错误"与"被篡改"同一个消息 | 在包装层尽量缩短明文生命周期；错误信息保持不泄露区别（这也是好事） | 否 |

## 3. 我自己复核的内容

- 重跑 `/tmp/vault-poc` 全部测试：通过（17 s 与 9.6 s 两次）。
- 直接读 `credentials.go`：KDF 分支只有 `reflect.DeepEqual(UUID, KdfArgon2)` 一个 Argon2 分支，`else` 一律走 AES-KDF；传给 `argon2.DKey` 的内存/迭代/并行度只检查了 `<1`，没有上限；全库源码里没有 `argon2id`/`IDKey`。这印证了 5b 与 2d。
- **没有**复核的：24/24 的 Python 交叉读取（只看了结果摘要）、模糊测试的具体数字、Wine 下的运行、Android 链接结果。

## 4. 对计划的影响

1. plan-vault §4.2 的"放行条件"更新为：库往返与第二实现交叉读已通过；**KeePassXC 互操作和补丁后的模糊测试仍待做**。
2. V1 工作量增加：包装层（R1、R5、R6、R7）、4 个小补丁（R2、R3、R4，以及可选的 Argon2id/R8）、更长时间的模糊测试（建议每个补丁后至少数小时，并加入 CI 的短时模糊）。
3. 默认参数建议：Argon2d，64 MiB / 3 次 / 并行 2（本机约 0.16–0.2 s）；导入时的接收上限 256 MiB；手机上的最终值要在真机测量后定。
4. 回退方案：如果补丁后的写入端无法通过 KeePassXC 的严格读取，改写一个最小的 KDBX4 容器写入器（PoC 里的"伪造器"约 100 行，可作为起点），读取仍用库或自写。

## 5. 没有验证的内容（别当事实）

- 与 KeePassXC、KeePass2、KeePassDX、Keepass2Android 的任何互操作；
- Android 真机的耗时与内存（只测了 arm64 构建和 x86 上的通用实现）；
- 其它 panic 点的有无（模糊测试时间太短）；
- Python 参考读取器是子代理按规范自写的，对规范的理解可能与它写的库有共同偏差（它先在 3 个真实 KeePass 夹具上验证过，缓解了但没有消除这一点）。
