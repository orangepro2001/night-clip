# Codex 和 Grok Bot 连接器

Windows 版设置中的「AI 连接器」提供两个独立连接。用 Moon SSO 登录，解锁密码库，打开「接收 AI 请求」，再为每个客户端生成连接令牌。令牌有效期为 30 天。重新生成会替换此客户端的旧令牌，撤销会使其令牌和待批准请求失效。

远程 MCP 地址为 `https://release.moonl.cloud/vault-agent/mcp`。

## Grok Bot

在应用连接页面选「添加自定义连接器」。名称填 MoonPassword，类型选「远程 HTTPS」，服务器 URL 填上面的地址。在 Windows 版的 Grok Bot 令牌窗口点「复制 HTTP 标头值」。表单的标头名称填 `Authorization`，值粘贴刚复制的内容。客户端 ID 留空。

## Codex CLI

在 PowerShell 添加服务器，并从 MoonPassword 的 Codex 令牌窗口复制令牌。用交互输入让令牌留在当前 PowerShell 和它启动的 Codex 进程中。

```powershell
$env:MOONPASSWORD_MCP_TOKEN = Read-Host "MoonPassword Codex 连接令牌" -MaskInput
codex mcp add moonpassword --url https://release.moonl.cloud/vault-agent/mcp --bearer-token-env-var MOONPASSWORD_MCP_TOKEN
codex
```

新终端需要重新设置环境变量。运行 `codex mcp list` 可查看配置。如果直接在客户端填写 HTTP 请求头，使用 `Authorization` 和 `Bearer <Codex 令牌>`。

## 使用

在密码库创建一条「API 密钥」，网址填 `https://github.com` 或 `https://api.github.com`，将要使用的 GitHub 访问令牌放在密钥字段。用最小权限授予目标仓库的元数据读取权限。

让客户端用 MoonPassword 读取一个 `owner/repo` 的仓库概要。Windows 密码库解锁且开启接收时，会显示请求来源和仓库名。选中 API 密钥，再点「允许一次」。客户端用 `check_request` 查询结果。请求及结果两分钟后过期。锁定密码库会暂停接收和批准。

客户端获得仓库名、是否私有、默认分支及归档状态。连接令牌只用于发起和查询这个操作。GitHub 令牌由 Windows 发送给 GitHub。中继持久化连接令牌哈希和授权范围，短暂保存请求和结果。网页代填及云浏览器的安全登录集成属于其它功能。
