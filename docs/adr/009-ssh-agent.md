# ADR-009：密码库托管 SSH 密钥，核心内置 SSH Agent

- 状态：**已采纳**（2026-11-20）。核心与 Windows 已实现；macOS 监听、Android 显示、主机绑定、Pageant 兼容、本地 CA 为后续阶段，见 [plan-ssh.md](../plan-ssh.md)。
- 修改：[ADR-007](007-password-vault.md) 第 4 条（密码库不对命名管道开放）和第 8 条（不做 SSH Agent）。
- 相关：[vault-api.md §3.9](../vault-api.md)、[api-spec.md §7.4](../api-spec.md)

## 背景

用户希望 SSH 私钥只录入密码库一次，本机的 ssh、git、VS Code 就能直接使用，不必再把私钥文件放在 `~/.ssh`。私钥手上有很多种格式：OpenSSH（各种加密方式）、PEM、PKCS#8、PuTTY，以及 KeePassXC 里带 KeeAgent 设置的条目。

可选方案有两类：本地 CA（给服务器签证书）和 SSH Agent。CA 解决的是“服务器信任哪些钥匙”，需要改每台服务器的 `sshd_config`，并不能让本机的 ssh 不读私钥文件；Agent 才是“私钥不落盘也能用”的机制。所以先做 Agent，CA 作为以后可选的阶段。

## 决定

1. **Agent 在 Go 核心里。** 新包 `core/sshagent` 实现 ssh-agent 协议（基于 `golang.org/x/crypto/ssh/agent`）。私钥只在核心进程内存里，对外只给公钥和签名。客户端的添加、删除、锁定请求一律拒绝，所以 KeePassXC 自己的 Agent 集成不能指向这个管道。
2. **只给同一用户，只能签名。** Windows 默认监听 `\\.\pipe\openssh-ssh-agent`（Windows ssh 在没设 `SSH_AUTH_SOCK` 时的默认地址），沿用 `core/pipe` 的写法：只允许本人访问的 DACL `D:P(A;;GA;;;OW)`、`FILE_FLAG_FIRST_PIPE_INSTANCE`（名字被占用就报“冲突”，绝不抢占）、拒绝远程客户端。密码库的 `vault.*` 和新的 `ssh.*` 方法仍然**不在**控制管道上（`core/pipe` 的测试固定这一点）。
3. **不确认，只通知。** 签名前不弹窗（用户的选择）；每次签名发 `ssh.used` 事件，壳按“每个程序首次使用”节流后弹气泡，并保留最近 100 次记录。经 `ssh -A` 转发来的请求（session-bind 标记 `is_forwarding`）默认拒绝，设置 `ssh_allow_forwarding` 才放行。
4. **钥匙圈独立于密码库。** 密码库解锁时、以及每次保存后（`Vault.SetOnSaved`）加载钥匙；签名不碰密码库，也不重置空闲计时。密码库因空闲自动锁定后，钥匙在 Agent 里再保留 `ssh_key_ttl_s`（默认 4 小时，从“本应锁定的时刻”算起，0 = 一起锁）；手动锁定、锁屏/注销/切换用户、睡眠、退出时立即清除。`vault.lock` 因此带 `reason`（user / session / suspend），且即使密码库已经锁着也会清除钥匙。
5. **存放方式与 KeePassXC 互通。** 条目 kind 为 `ssh`；私钥统一转成**不加密**的 OpenSSH 格式存为附件（密码库本身就是加密层）；同时写 `KeeAgent.settings`（KeePassXC/KeeAgent 的格式，UTF-16LE），`AddAtDatabaseOpen` 即“提供给 Agent”的开关。没有 `ClipBridge.Kind` 但带 `KeeAgent.settings` 的条目读作 `ssh`；旧客户端把它改成 `login` 时核心保持 `ssh`。能否进 Agent 看 `KeeAgent.settings`，不看 kind。
6. **格式识别在核心。** 新包 `core/sshkey` 按内容（不看文件名）识别并解密：OpenSSH（none、aes128/192/256-ctr/cbc、aes128/256-gcm、chacha20-poly1305、3des-cbc）、PEM PKCS#1/SEC1（含旧式 Proc-Type 加密）、PKCS#8（明文与 PBES2：PBKDF2-HMAC-SHA1/224/256/384/512 或 scrypt，AES-CBC 或 3DES）、裸 DER、PuTTY PPK v2/v3（Argon2i/d/id）。能识别但拒绝并说明原因：DSA、FIDO（sk-*）、ssh.com 格式、PPK v1、PBES1、公钥/证书、RSA < 2048、其它曲线、一文件多钥。每条解密路径都有成本上限（bcrypt 轮数、PBKDF2 次数、scrypt/Argon2 内存），解密在锁外进行。
7. **私钥只经 `ssh.export` 离开核心**，且要求再次输入主密码，文件以独占方式新建（不覆盖）。
8. **迁移 `~/.ssh` 默认不删文件。** 扫描只读元数据，从不返回文件内容。ssh 在私钥文件不存在、只有 `<路径>.pub` 时会改用 Agent 里的同一把钥匙（本机 Windows OpenSSH 9.5 实测），所以迁移只需补 `.pub`，`~/.ssh/config` 不用改。只有指纹一致、`.pub` 正确、Agent 已列出、且同目录脚本没有直接引用这个文件时，才提供（默认不勾选）移到回收站。

## 后果

- 正面：私钥不再需要以明文文件存在；多设备同步自动带上钥匙；和 KeePassXC 的条目互通；Windows ssh 不用任何配置；签名记录可查。
- 负面与风险：
  1. **没有确认环节。** 钥匙加载期间，同一用户下的任何进程都能用它们登录，这和系统自带的 ssh-agent 相同；宽限期把这个窗口延长到了密码库空闲锁定之后。
  2. Go 无法可靠擦除内存：RSA/ECDSA 私钥在 `big.Int` 里，只有 Ed25519 种子能清零；口令以 JSON 字符串传入。
  3. ClipBridge 不运行时 ssh 用不了这些钥匙。直接读私钥文件的工具（SSHFS-Win、未设 `core.sshCommand` 的 Git 自带 ssh、WSL、PuTTY/WinSCP、带 `-i` 的脚本）连不上 Agent，所以文件默认保留；Git 由设置页提供一键改用 Windows OpenSSH（先显示完整命令并确认，只撤销自己写的值）。
  4. 来自同步或 KDBX 导入、且 `AddAtDatabaseOpen` 为真的钥匙会直接进 Agent，`ssh.keys` 事件和列表会显示出来。
  5. `ssh.*` 事件限速（约每秒 10 个），避免本机进程刷请求挤掉其它事件。
  6. 历史版本恢复不恢复附件，因此 SSH 条目不提供“恢复历史版本”。

## 验证

- `core/sshkey`：30 个由 ssh-keygen/openssl 生成的样本逐一比对公钥，口令对/错/缺三种情况，全部拒绝原因，成本上限，导出往返，扫描不泄露内容；Fuzz 覆盖解析与 KeeAgent.settings 改写。PPK 只按 PuTTY 公开的格式说明测试（本机没有 puttygen）。
- `core/sshagent`：三种钥匙类型与 RSA 的三种签名算法、拒绝修改、session-bind 校验与转发、宽限期按墙上时钟到期、过期的加载不会把钥匙放回、协议帧 Fuzz。
- `core/facade`：在随机命名管道上跑完整流程；`CB_SSH_E2E=1` 时用 Windows 自带的 `ssh-add -l`、`ssh-keygen -Y sign/verify` 和 `ssh`（只有 `.pub`、`IdentitiesOnly yes`）登录一个测试服务器，确认 session-bind 带回了服务器的主机钥匙。
