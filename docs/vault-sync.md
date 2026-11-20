# 密码库自动同步

Windows 和 Android 在「设置」中登录 Moon SSO。解锁本机密码库后，首台设备选择「创建云端密码库」，再复制并备份同步密钥。另一台设备用同一账号登录，创建或解锁其本机库，选择「连接已有云端库」，粘贴密钥即可合并。各设备可以使用不同的本机主密码。断开设备保留两边的密码库，重新连接需要同步密钥。

应用运行且密码库解锁期间，保存本地改动会触发同步，应用也每 30 秒检查云端。离线时继续使用本机库，随后按条目 UUID 合并。新版本、冲突历史和删除记录沿用 KeePass 合并规则。设置中显示最近同步时间，也可立即同步。

## 服务与加密

客户端从 32 字节随机数生成同步密钥，以标准 KDBX 4 格式加密同步副本。本机主密码和同步密钥都留在设备上，设备用系统密钥保护的数据库保存同步密钥。服务器每个 Moon SSO 主体只保存一个密文版本以及验证写入用的 Ed25519 公钥。客户端从同步密钥派生写入签名密钥，签名绑定账号、期望版本和上传密文。服务验证 OAuth 令牌的签发者、受众和会话声明，核对签名和 ETag，再原子写入。两个设备同时写入时，其中一个重新下载、合并并重试。

## 部署与验证

- Moon 上的服务使用 [systemd 单元](../deploy/moonpassword-sync.service)，仅监听 `127.0.0.1:18652`。在 `core` 中以 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o moonpassword-sync ./cmd/moonpassword-sync` 构建。数据目录是 `/var/lib/moonpassword-sync`。
- Caddy 将 `https://release.moonl.cloud/vault-sync/*` 转发到该服务并移除路径前缀。公开的 `GET /vault-sync/healthz` 应返回 204，未授权请求 `/vault-sync/v1/vault` 应返回 401。
- `go test ./vaultsync ./facade` 验证并发版本检查、账号隔离、写入密钥校验、独立本机主密码、双端离线合并、解锁后的自动上传。
