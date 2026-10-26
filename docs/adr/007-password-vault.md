# ADR-007：密码库（KDBX 4，本机加密，经 tailnet 在自己的设备间同步）

- 状态：Accepted（2026-10-27；实现中，见 [plan-vault.md](../plan-vault.md)）
- 相关：[plan-vault.md](../plan-vault.md)、[vault-v0-poc-report.md](../vault-v0-poc-report.md)、[vault-api.md](../vault-api.md)、[ADR-005](005-go-core-and-tailnet-p2p.md)

## 背景

用户要在 ClipBridge 里集成类似 KeePassXC 的密码/凭据/API 密钥/口令管理，不另开应用；要求只在 tailnet 内使用，安全与易用平衡，备份/恢复完整。

## 决策

1. **格式：KDBX 4**（KeePass 数据库格式），不自创加密方案。库文件 `vault/vault.kdbx` 可被 KeePassXC 直接打开（已用 KeePassXC 2.7.10 双向互测：我们写它读、它写我们读、它的 KDBX 3.1 库自动升级为 4）。退出成本为零是备份策略的一部分。
2. **库：** vendored `gokeepasslib` v3.7.1（MIT），打了补丁：密钥派生前的资源上限、拒绝未知 KDF、gzip 展开上限、内部头越界修复、KDBX 4 附件按原样返回（`core/third_party/gokeepasslib/PATCHES.md`）。**每次保存都重新生成**主种子、IV、KDF 盐和内部流密钥（库默认沿用，会让 ChaCha20 的密钥流在不同文件代际间重复）。
3. **密钥：** 主密码 + 可选密钥文件，Argon2d（默认 64 MiB × 3 × 2）。没有恢复后门：忘记主密码就无法打开。设备级保护（DPAPI/Keystore）**不**用来保护库本身，只用于壳的“快速解锁”（可选，限时，生物识别）。
4. **边界：** `core/vault` 独立包；门面方法 `vault.*`；**不对命名管道和命令行开放**；机密只经 `vault.reveal`/`vault.copy` 离开核心；复制出的内容登记为 `vault_origin`，**永不被自动广播**。
5. **备份：** 原子写入 + 上一代 + 每次改动前快照（保留 20 + 每日 7 + 每周 4）+ 手动导出；恢复前先快照；恢复支持预览差异、选定条目、合并、整库替换。
6. **同步：** 传输加密的 `.kdbx` 文件（不是明文条目），默认关闭、逐设备双向开启；接收方在解锁后按 KeePass 规则合并（按 UUID、较新的修改胜出、旧版本进历史、位置单独判断、删除只胜过没动过的对象、平局按内容哈希）；不是同一个库或一批会删除 5 个以上条目时需要用户批准。合并满足交换律并最终一致（随机操作的属性测试）。
7. **不做：** 无障碍服务填充、自带输入法、泄露库联网查询（HIBP）、SSH Agent、共享库/紧急访问。自动填充（Android AutofillService）与通行密钥是后续阶段。

## 后果

- 正面：成熟格式与生态；备份即复制文件；多设备同步无需服务端；机密不进剪贴板同步。
- 负面：整库同步而非逐条；更改主密码后其它设备需要输入新密码才能合并；更新机制仍是最大的系统性风险（发布密钥在开发机，见 plan-vault.md §9 R1）；Go 无法擦除字符串，已解锁时同用户的恶意进程可读内存。
