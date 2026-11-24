# 验证码迁移与 Firefox 扩展

## Windows 与 Android 验证码

在密码库的更多菜单选择「添加验证码（OTP）」。粘贴一条或多条 `otpauth://totp` 链接，或选择二维码图片。Android 也支持相机扫码。给已有登录添加验证码时，可使用条目编辑页的验证码输入框或扫码按钮。

从 Google Authenticator 导出时，如果得到多张二维码，请把同一批的二维码全部加入再导入。导入会跳过重复密钥和不支持的计数器型验证码（HOTP）。

在条目详情可复制当前验证码。选择「导出与迁移验证码」并重新验证主密码后，可显示二维码、复制链接或导出文本文件。MoonPassword 可批量导回该文本文件，其他验证器可逐个扫描标准 TOTP 二维码。导出的链接和二维码含原始密钥，长期备份可使用加密 KDBX。

## Firefox 桌面扩展

使用 Firefox 142 及以上版本和 Windows 版 MoonPassword。Windows 发布包附带 `MoonPassword-Firefox.zip`。

1. 在 MoonPassword 的「设置 → 浏览器扩展」打开「Firefox 本机连接」，点「打开扩展文件夹」找到安装包。
2. 在 Firefox 打开 `about:debugging#/runtime/this-firefox`，选「临时载入附加组件」并选择该 ZIP。临时扩展随浏览器重启卸载，正式安装需要 Mozilla 签名的 XPI。
3. 打开登录页并点击 Firefox 工具栏中的 MoonPassword。扩展按协议、主机和端口匹配网址及备用网址，填充前在 Windows 应用确认来源及账号。
4. 在注册或改密页可生成密码。选「生成、填充并保存」时，Windows 应用确认后会添加一条新登录。

扩展通过 Firefox Native Messaging 把网站来源发送至本机应用。选择保存时会传递用户名与刚生成的密码。扩展没有远程请求，也不会持久化密码和浏览记录。关闭「Firefox 本机连接」即可停止本机通信。
