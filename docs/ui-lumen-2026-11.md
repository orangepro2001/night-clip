# Windows 端界面迁移到 Avalonia + Lumen.UI（2026-11-20）

用户的要求：用本机的 C# 组件库 Lumen.UI（`C:\develop\CSharpGUIFramework`，模板 `lumen-app`）重做 Windows 端页面，和现代应用对齐，大幅改进交互，顺手修掉原来页面上的小问题。路线由用户选定：**Windows 端界面层整体迁到 Avalonia 12 + Lumen.UI**（而不是在 WPF 里仿造 Lumen 的外观）。

核心、协议、同步、配对、密码库格式、后台剪贴板行为都没有改；`Logic/` 层（与 macOS 端、单元测试共用）只做了加法。

## 结构

| 部分 | 现在 |
| --- | --- |
| 项目 | `windows/MoonPassword.Windows/MoonPassword.Windows.csproj`：`net10.0-windows`，`Avalonia.Win32` + `Avalonia.Skia` + `Avalonia.HarfBuzz` 12.1.6 + `Lumen.UI` 0.1.0（nuget.org）。只带 Windows 后端（不用 `Avalonia.Desktop`，省掉 X11 / macOS / D-Bus 的库）。仍开 `UseWindowsForms`，只为托盘的 `NotifyIcon`。 |
| 启动 | `Program.cs`：先处理更新器参数（`--apply…`，不启动 Avalonia），再做单实例检查，然后 `StartWithClassicDesktopLifetime(OnExplicitShutdown)`。`App.axaml(.cs)`：`LumenTheme` + 应用样式；恢复主题与强调色；启动核心；致命错误用系统消息框（`Services/NativeDialogs.cs`）。 |
| 主窗口 | `MainWindow.axaml(.cs)`：Lumen 的 `AppShell`。侧栏分组（同步 / 工具 / 系统），收件箱、传输、诊断有实时角标（`Views/NavPage.cs` + `App.axaml` 里覆盖 `NavList` 的条目模板——Lumen 的 `NavItem` 不会通知变化）；侧栏可折叠并记住状态。顶栏：同步状态（点击可暂停 15 分钟 / 1 小时 / 4 小时或关闭同步）、“配对新设备”、深浅色切换。侧栏底部：本机名和在线设备数。 |
| 提示 | 底部状态栏改为右下角 Toast（成功 / 警告 / 错误分色）。核心警告和可用更新改为每页顶部的提示条（`Views/PageFrame.cs`），更新提示条上可直接下载 / 安装，可关闭到下一个版本。 |
| 快捷键 | Ctrl+1…5 切页，Ctrl+, 设置，Ctrl+F 聚焦当前页的搜索框；笔记里 Ctrl+B / I / K / S。 |
| 服务 | `ShellController*` 换成 Avalonia 的 `Dispatcher` / `DispatcherTimer`（`Services/UiTimer.cs`）；`ClipboardMonitor` 自建消息窗口（`CreateWindowEx(HWND_MESSAGE)`，不再依赖 WPF 的 `HwndSource`）；`DisplayAffinity`、`TaskbarFlash` 用 `TryGetPlatformHandle()`；托盘菜单换成与 Lumen 一致的扁平样式，跟随应用深浅色，Windows 11 上为圆角（`Services/TrayMenuRenderer.cs`）。 |

## 各页改了什么

- **设备**：每台设备一行（平台图标、状态徽章、最后在线、收到时的策略、排队条数），右侧图标按钮：发送文字 / 剪贴板 / 文件、重新连接、设置。新增“快速发送”输入框（发给所有在线设备或指定设备，Ctrl+Enter 发送）。重命名、策略、自动发送、解除配对合并成一个“设备设置”对话框。文件拖到设备行上即发送，拖动时该行高亮。空状态带“配对新设备 / 加入配对”按钮。
- **配对**：“配对新设备”“加入配对”改为主窗口内的对话框（从托盘打开时先显示主窗口）；二维码放在白底上（深色主题下也好扫）；状态行带转圈 / 成功 / 失败图标；成功后自动关闭。对方发来的配对请求、文件、图片仍是独立的小窗口（主窗口在托盘里时也要弹出）。
- **收件箱**：全部 / 未处理 / 已复制 筛选，搜索（敏感内容不参与搜索），长文本“查看全文”对话框，复制 / 忽略 / 显示为图标按钮，清空需要确认（危险样式）。
- **传输**：全部 / 进行中 / 已结束 筛选，方向图标、状态徽章、进度条与速度，“打开接收文件夹”。
- **密码库**：解锁 / 创建改为居中卡片；三栏（筛选与分组 / 列表 / 详情卡片）；条目按类型显示图标；详情里字段的显示 / 复制 / 打开链接是图标按钮，TOTP 圆环，标签是徽章；编辑器的类型用分段控件，密码生成器改为编辑窗口里的对话框；所有对话框沿用 Lumen 样式，锁定时一并关闭，并继续排除截屏。
- **笔记**：编辑器卡片里标题、视图切换（源码 / 分栏 / 预览）与历史、删除在同一行，格式工具栏改为图标按钮；预览用 Lumen 配色重绘（引用条用强调色，代码块浅底）；状态行带保存状态圆点；导出 / 导入结果改为对话框。
- **设置**：全部即时保存（数字框失焦或回车时校验并保存，错误显示在输入框下面）；新增“外观”（跟随系统 / 浅色 / 深色，9 种强调色）；设置行统一为“标题 + 说明 + 右侧开关”。
- **诊断**：顶部三个统计卡（核心、在线设备、警告），报告卡片可复制。

## 修掉的原有小问题

1. 设置页的“保存”按钮放在“文件”卡片里，却会保存整页的同步设置——改为全部即时保存，不再有这个按钮。
2. 状态提示只在底部状态栏显示 8 秒、字很小，容易错过——改为 Toast。
3. 设备卡片里空的“排队”行仍占一行高度，卡片间距不齐——没有内容就不显示。
4. 设备卡片上最多 8 个文字按钮挤在一行，窄窗口时被截断——改为图标按钮 + 设备设置对话框。
5. 收件箱的“清空”是主色按钮，与它的危险程度不符，空收件箱时也可点——改为幽灵按钮 + 危险确认，空时禁用。
6. 传输页空状态的标题与说明重复——说明改为告诉用户怎样发送文件。
7. 侧栏没有任何未读 / 进行中提示——收件箱、传输、诊断有角标（折叠时为圆点）。
8. 配对二维码在深色主题下会被画在深色底上——始终放在白底上。

## 开发辅助

- **独立开发实例**：设置环境变量 `MOONPASSWORD_DATA_DIR` 为一个绝对路径，就用该目录作为数据目录，并使用独立的单实例名，可以和已安装的 MoonPassword 同时运行；开发实例不修改开机自启，错误日志带调用栈。
- **截图**：`MoonPassword.exe --snapshot <目录>`（仅开发实例）把每个页面、主要对话框、小窗口和托盘菜单在浅色与深色下各渲染一张 PNG 后退出。为了有内容可看，它会在开发数据目录里通过正常的核心调用写入几条密码库条目和笔记，并只在内存里填充示例设备、收件箱和传输（不进核心）。

```powershell
$env:MOONPASSWORD_DATA_DIR = "$env:TEMP\cb-dev"
.\windows\MoonPassword.Windows\bin\Debug\net10.0-windows\win-x64\MoonPassword.exe --snapshot "$env:TEMP\cb-shots"
```

## 验证情况（2026-11-20，Windows 11）

| 项目 | 结果 |
| --- | --- |
| `dotnet build`（Debug / Release） | 0 警告 0 错误 |
| `dotnet test windows/MoonPassword.Windows.Tests` | 945 通过 |
| `dotnet build macos/MoonPassword.Mac`（共用 Logic） | 0 警告 0 错误 |
| `dotnet publish -r win-x64 --self-contained false`（发布参数同 `make dist-windows`） | 43 MB（其中核心 DLL 15 MB），不含 .pdb |
| 开发实例启动真实核心、显示主窗口、渲染全部页面 / 对话框 / 小窗口 / 托盘菜单（浅色 + 深色）、正常退出 | 通过，日志无错误 |
| 两台设备之间的配对、剪贴板收发、文件传输、托盘气泡点击 | **未在本次验证**（需要另一台设备） |
| 从已安装的 WPF 版（0.1.110）自更新到本版本 | **未验证**：更新器由旧版启动新版 exe 完成文件替换，新版的 `Program.Main` 在启动 Avalonia 前处理该参数；建议发布前在一台装有旧版的机器上实测一次 |
