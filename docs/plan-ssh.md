# 计划与实施状态：SSH 密钥与 SSH Agent

- 决策：[ADR-009](adr/009-ssh-agent.md)　接口：[vault-api.md §3.9](vault-api.md)
- 目标：SSH 私钥只录入密码库一次，本机的 ssh、git、VS Code 就能直接用，不必再把私钥文件放在 `~/.ssh`；能识别手上各种格式和加密方式的私钥。

## 已定的取舍（2026-11-20，用户选择）

1. 先做 Agent；本地 CA 放到后期，只写设计。
2. 密码库**空闲**自动锁定后，钥匙在 Agent 里再保留一段时间（默认 4 小时，可设，0 = 一起锁）；手动锁定、锁屏/注销/切换用户、睡眠、退出时立即清除。
3. 签名前不弹确认，签完只发通知。
4. 先做 Windows；macOS 下一阶段；Android 只显示 SSH 条目、复制公钥。

## 阶段与状态

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| 0 验证 | 测试样本（ssh-keygen 各种 `-Z` 加密、openssl 传统 PEM / PKCS#8 PBES2 / scrypt / DER、拒绝样本）；Windows OpenSSH 9.5 实测 | **完成**。`core/sshkey/testdata/gen.sh` 可重新生成；`CB_SSH_E2E=1` 的测试确认：命名管道可用、只有 `.pub` + `IdentitiesOnly yes` 时 ssh 改用 Agent 里的钥匙、ssh 会发 session-bind。PPK 样本缺 puttygen，只按格式说明测试；KeePassXC 2.8 写出的 `KeeAgent.settings` 原始字节未实测（读取兼容 UTF-8/UTF-16） |
| 1 格式与入库 | `core/sshkey`（识别、解密、导出、生成、扫描 `~/.ssh`、KeeAgent.settings）；vault 的 `ssh` kind、推断规则、`AddEntryWithAttachments`、`ReplaceAttachments`、`SSHCandidates`；`ssh.*` 门面方法；C#/macOS 编辑器原样保留不认识的 kind | **完成** |
| 2 核心 Agent | `core/sshagent`（钥匙圈、协议、session-bind、请求分类、known_hosts 反查、进程链）；命名管道 `ClientPID` 与提前创建下一实例；`vault.lock {reason}`；设置 `ssh_agent`、`ssh_agent_pipe`、`ssh_key_ttl_s`、`ssh_allow_forwarding`；事件限速 | **完成** |
| 3 Windows 界面 | 密码库“SSH 密钥”筛选与详情区块、导入 / 生成 / 导出 / 迁移窗口、设置页“SSH 代理”卡片（环境检查、Git 一键修复、服务冲突处理、最近使用）、气泡节流 | **完成**；真机验证见下 |
| 4 macOS | `listen_unix` 用 LOCAL_PEERPID；`~/.moonpassword/agent.sock`；经同意后用 Include 引入受管的 `IdentityAgent` 配置；移植界面 | 未开始 |
| 5 Android | ssh kind 的显示、筛选、复制公钥；Agent 方法返回 UNSUPPORTED | 未开始（核心在 Android 上已返回 `state:"unsupported"`） |
| 6a 主机绑定 | 每把钥匙可限定服务器（主机名 / 通配符 / `[主机]:端口` / 主机钥匙指纹），按 session-bind 的主机钥匙和 `known_hosts`（含哈希条目）筛选列表、拒绝发往别处的登录签名；`ssh.set_hosts`；Windows 详情里“限定主机”区块和“只用于最近登录过的主机”一键设置 | **完成**（2026-11-20）。`CB_SSH_E2E=1` 的测试用真实 ssh 验证：绑到别的服务器时登录失败，绑到这台时成功 |
| 6 以后 | Pageant 兼容与 PPK 导出；Git SSH 签名助手；本地 SSH CA（CA 私钥在密码库，给选中的钥匙签短期证书，生成 `TrustedUserCAKeys` / `@cert-authority` 行与主机证书，需要改服务器配置，只做可选）；macOS（阶段 4）、Android（阶段 5） | 未开始 |

## 发布

- **0.1.112**（提交 `894efc6`，2026-11-20）：阶段 0–3，同时把更新服务换成 `https://release.moonl.cloud`（[update.md §7b](update.md#7b-2026-11-20迁到-releasemoonlcloud)）。三个平台都已发布并按 App 的方式验证；Windows 包在本机开发实例里跑过完整截图（真实核心：SSH 条目、导入预览、迁移扫描）。
- 主机绑定（阶段 6a）在 0.1.112 之后，随下一个版本发布。

## 真机还要验证的

本机没有 cgo 工具链，核心 DLL 在构建机（`AI`）上交叉编译。用开发实例（`MOONPASSWORD_DATA_DIR`）依次确认：

1. 设置页开启 SSH 代理，状态为“正在运行”，自检列出钥匙；
2. 用“从 ~/.ssh 迁移”导入现有钥匙（先不移走文件），`ssh-add -l` 能列出；
3. `ssh -v` 登录 `IdentitiesOnly` 的主机，日志显示用的是 Agent 里的钥匙；
4. 设置页“让 Git 使用 Windows OpenSSH”后 `git ls-remote` GitHub 仓库成功；
5. 宽限期（空闲锁定后仍能用）、锁屏立即清除、气泡节流。

改 Git 全局配置、把私钥文件移到回收站，都要用户在界面里确认后才发生。
