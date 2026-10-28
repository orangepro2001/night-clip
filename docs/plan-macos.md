# macOS 端开发计划

2026-10-28 起草，**未开工**。本文根据当时的代码写成：Windows 壳约 17.3k 行 C#，核心 Go 方法 104 个。开工前先读 §2 的前提和 §8 的决策点。

> 和现有文档的关系：[architecture.md](architecture.md) §2.4/§11 写着「放弃 iOS/macOS/Linux 客户端」。开工时要先写一份新 ADR 推翻这一条，并引用本文。

## 1. 结论先说

- **核心可以整体复用。** `core/` 是纯 Go，`GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...` 现在就能编过。2026-10-28 实测通过。
  - 控制套接字已经有 Unix 实现（`pipe/listen_unix.go`）。
  - 磁盘空间检查也有 Unix 实现（`xfer/space_unix.go`）。
  - 唯一的 cgo 是 C ABI 导出层 `cmd/cbcore-dll`（5 个函数），在 macOS 上原样编成 `c-archive` 即可。
- **UI 需要单独写一份，和 Windows 不共用。** WPF 不能跨平台。项目本来就是「一个核心 + 每个平台一个原生薄壳」：Android 用 Compose，Windows 用 WPF。macOS 用 SwiftUI + AppKit，这是第三个壳。理由和备选方案见 §5。
- **大部分功能能完整移植。** 受限的主要是三项：剪贴板监听（只能轮询）、截屏防护（新版截屏 API 不受控）、未付费签名下的更新体验。难以做到的是系统级密码自动填充和分享扩展，这两项需要 Apple 开发者账号。
- **最大的前提不在代码里，在机器和账号上。**
  - Swift 应用只能在 Mac 上编译（Xcode），也只能在 Mac 上测试。
  - 本机是 Linux，现有发布流程跑在本机。
  - 有没有一台可用的 Mac、要不要交 99 美元/年的 Apple Developer 会员，会决定构建、签名和更新怎么做（§6）。

## 2. 前提条件

| 项 | 需要什么 | 现状 |
| --- | --- | --- |
| 构建机 | 一台 Mac（Apple Silicon 优先），装 Xcode 16+ | 未知，需用户确认 |
| 替代构建 | GitHub Actions `macos-latest` runner | 私有仓库按 10 倍计费分钟，免费额度约 200 分钟/月，够偶尔发版，不够频繁迭代 |
| 真机测试 | 同一台 Mac，并装好 Tailscale | 未知 |
| 最低系统 | 建议 macOS 13 Ventura。`SMAppService` 开机启动和 `MenuBarExtra` 都从 13 开始有 | 待定 |
| 架构 | arm64；Intel 用 `lipo` 合成 universal | 待定 |
| 签名 | 三选一：ad-hoc、自签证书或 Developer ID（§6） | 待定 |

### 2.1 能不能不在 Mac 上编译（2026-10-28 补充）

用户的 Mac 是 M1、250 GB，希望不在它上面开发。逐层看：

| 部分 | 在 Linux 上交叉编译 | 说明 |
| --- | --- | --- |
| Go 核心（纯 Go 部分） | **能**，已实测 | `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64`，Go 链接器会自动给 arm64 程序加 ad-hoc 签名 |
| Go 核心做成库（`c-archive`/`c-shared`，有 cgo） | **能，要装 zig** | 用 `CC="zig cc -target aarch64-macos"`，纯 C 不依赖 macOS SDK；未实测 |
| Go 核心做成独立进程，壳经套接字调用 | 能，不需要 cgo | 但现在的控制套接字只开放 15 个方法、没有事件流（`pipe/server.go` 的 `Allowed`），要新开一个全权限通道，有安全代价。不推荐 |
| SwiftUI 壳 | **不能** | Swift 在 Linux 上没有受支持的 AppKit/SwiftUI 目标 |
| Avalonia（.NET）壳 | **能** | `dotnet publish -r osx-arm64` 支持在 Linux 上发布；`.app` 只是目录加 `Info.plist`；签名用能在 Linux 上运行的 `rcodesign`（apple-codesign）。未实测 |
| 纯 Go 调 AppKit（purego/objc，无 cgo） | 能 | 菜单栏、剪贴板这些小功能可以，复杂界面（笔记、密码库）手写 `objc_msgSend` 太费力 |

所以有两条路线：

- **A. Avalonia 壳 + zig 编译核心，全部在 Linux 上构建。** Mac 只用来装和测。
  - 能直接共用 Windows 的 `Logic/`（约 6.8k 行）和测试；发布流程留在本机。
  - 代价：
    - 界面不是 Mac 原生；
    - 包体积约 60–100 MB；
    - 钥匙串、剪贴板 `changeCount` 要用 P/Invoke 调系统框架。
  - 交叉编译不受 Mac 性能影响，**推荐用于这个约束**。
- **B. SwiftUI 壳，Mac 当远程构建机。**
  - 只装 Command Line Tools（几 GB，不装完整 Xcode），用 SwiftPM 构建。
  - 打开「远程登录」后，经 tailscale SSH 由本机驱动构建，也能用 `screencapture` 远程截图。
  - M1 编一个万行级的 SwiftUI 应用只要一两分钟，性能不是瓶颈。
  - 缺点：每次发布都要那台 Mac 在线。

两条路线都还要先做半天验证。A 要验证三件事：

1. zig 能编出 darwin 的 Go 动态库；
2. `dotnet publish osx-arm64` 加 `rcodesign` 签名后，在 M1 上能直接运行；
3. Avalonia 的托盘和剪贴板在这台 Mac 上能正常工作。

验证要在本机用户目录装 zig 和 rcodesign，需用户同意。

## 3. 功能移植分级

依据：Windows 壳的功能清单，以及 Android 独有的功能。

### 3.1 完整移植（逻辑都在核心，壳只做 UI 和系统调用）

| 功能 | Windows 现在怎么做 | macOS 怎么做 |
| --- | --- | --- |
| 配对：显示二维码、输入配对码、比对数字 | PairListen/PairJoin/PairRequest 窗口 + `QrRenderer` | SwiftUI 窗口；二维码用 CoreImage `CIQRCodeGenerator`，不需要第三方库 |
| 设备列表、发送剪贴板、发送文本 | DevicesPage | 同上，SwiftUI |
| 收件箱、收到文本后写剪贴板 | InboxPage + `ClipboardAccess` | `NSPasteboard`（写入方式见 §3.2） |
| 文件发送 | 拖放到设备、选择文件 | 拖放（`onDrop`/`NSDraggingDestination`）、`NSOpenPanel`、拖到 Dock 图标 |
| 文件接收：确认、进度、保存到下载、在文件夹中显示 | FileOfferWindow、TransfersPage、`KnownFolders`、`explorer /select` | 通知加确认窗口；`FileManager` 取 Downloads；`NSWorkspace.activateFileViewerSelecting` |
| 笔记 | NotesPage、编辑器、冲突、历史、导入导出、附件 | 核心的 `notes.*` 方法都能直接用；工作量在编辑器 UI（§4 M6） |
| 密码库 | 解锁、列表、详情、编辑、生成器、备份 | 核心的 `vault.*` 都能直接用；纯 UI 工作 |
| 设置、诊断、日志 | SettingsPage、DiagnosticsPage | 同上 |
| 托盘 | `TrayService`，带气泡通知 | 菜单栏图标 `MenuBarExtra`/`NSStatusItem`；通知用 `UNUserNotificationCenter`，可带「接受/拒绝」按钮，比 Windows 气泡更强 |
| 提醒用户注意 | `FlashWindowEx` | `NSApp.requestUserAttention`（Dock 图标跳动） |
| 单实例 | 命名互斥量 | .app 包本身就是单实例；再次打开时由 `applicationShouldHandleReopen` 把窗口调出来 |
| 开机启动 | 注册表 `Run` 键 | `SMAppService.mainApp.register()`（macOS 13+） |
| 主密钥保护 | DPAPI | 钥匙串（Keychain，`kSecClassGenericPassword`） |
| 命令行 `cbridge` | 命名管道 | 核心已有 Unix 套接字；`cbridge` 直接编 darwin 版，比 Windows 还省事 |
| 自动更新 | 下载 zip，辅助进程替换文件后重启 | 同一思路：辅助进程等旧进程退出，替换整个 `.app`，再用 `open` 重启（§3.2 签名一节有限制） |

### 3.2 受限移植（能做，但效果或体验比 Windows 差）

- **剪贴板监听只能轮询。**
  - macOS 没有「剪贴板变化」通知，只能定时读 `NSPasteboard.general.changeCount`。0.5 秒一次足够，成本很低，所有剪贴板工具都这么做。
  - 风险：苹果在新系统里加了剪贴板隐私控制（应用在用户没有粘贴动作时读剪贴板，可能弹窗询问或被拒）。
  - 对策：只比较 `changeCount`，确认有变化才读内容。读之前先用不触发提示的类型探测接口判断是不是文本。
  - **这一项要在目标系统版本上实测**，它决定「复制即同步」能不能做到无感。
- **写入剪贴板时排除历史记录和云同步。**
  - Windows 是给条目打标记，排除系统剪贴板历史和云剪贴板。
  - macOS 可以用 `prepareForNewContents(with: .currentHostOnly)`（macOS 12+），不让条目经「通用剪贴板」传到其他苹果设备。
  - 再按 nspasteboard.org 的约定加 `org.nspasteboard.TransientType`，排除第三方剪贴板管理器。但这个标记只对遵守约定的工具有效。
- **密码库窗口的截屏防护。**
  - Windows 用 `SetWindowDisplayAffinity`。
  - macOS 设 `NSWindow.sharingType = .none`，只对旧截屏接口有效，较新的 ScreenCaptureKit 可能照样截到。
  - 只能当「尽力而为」，界面文字不要承诺「无法截屏」。
- **未付费签名下的更新和权限体验。**
  - 详见 §6。签名身份如果每次构建都变：
    - 钥匙串每次更新后会再次询问「允许访问」；
    - 防火墙的「允许传入连接」也可能每次更新后重问。
  - 用固定的自签证书签名可以解决大部分问题。
- **入站连接。**
  - macOS 的作用和 Windows 一样：监听 tailnet 地址，等手机拨入。
  - 打开了 macOS 应用防火墙的话，首次会弹「允许传入连接」。
  - Tailscale 有 App Store 版和独立版两种，都应能在 100.x 地址上接收连接。需要实测。
- **睡眠和唤醒。**
  - 笔记本合盖后连接全断。
  - 监听 `NSWorkspace.didWakeNotification`，唤醒时让核心立即重连，不等退避。
  - Windows 也有这个问题，但 Mac 合盖更常见。

### 3.3 难以做到或不建议做

| 功能 | 原因 | 替代 |
| --- | --- | --- |
| 密码库系统级自动填充（Android 有） | 要写 AutoFill Credential Provider 扩展，并带专用 entitlement，必须有付费开发者账号和描述文件 | v1 不做；用「复制密码，限时自动清除」代替 |
| 系统分享菜单（分享扩展） | 应用扩展、App Group 都要正规签名 | 用 **Services 菜单**代替（Info.plist 里的 `NSServices`，不需要扩展）：选中文本或文件 → 服务 → 用 ClipBridge 发送。再加拖放到 Dock 图标 |
| Mac App Store 上架 | 沙盒限制监听端口、Unix 套接字和随意的文件读写，与架构冲突 | 不做，只自行分发 |
| 拍照即发（Android 新功能） | Mac 上没有使用场景 | 不做 |
| Android 的后台读剪贴板技巧（logcat、悬浮窗、Shizuku） | macOS 不需要，轮询即可 | 不适用 |
| 在 Linux 上交叉编译 macOS 壳 | Swift/AppKit 和签名都离不开 Xcode 工具链；osxcross 加 SDK 的许可证不清楚 | 只在 Mac 或 macOS CI 上构建 |

## 4. 核心要改什么（在 Linux 上就能做，先做）

现在核心有几处把平台写死成 `"windows" | "android"`：

| 位置 | 现状 | 改法 |
| --- | --- | --- |
| `facade/facade.go:127` | 初始化时只接受 windows/android | 加 `macos` |
| `pairing/manager.go:319`、`pairing/join.go:172` | 配对请求只接受两种平台；未知值被当成 windows | 加 `macos` |
| `peer/dial.go:41` | 拨号方向：Windows 之间 ID 小的拨号，Android 只拨出 | 改成按「桌面端（监听）/移动端（只拨出）」判断，macOS 归桌面端 |
| `peer/engine.go:96`、`xfer/service.go:104,222` | 默认保存目录按平台选 | macOS 的分支与 Windows 相同（下载目录由壳传入） |
| `update/manifest.go:17-30`、`updatetest/server.go:76` | 只有 android/windows 两个平台 | 加 `PlatformMacOS = "macos"`，包格式为 `.zip` |
| `Makefile` | 只有 windows/linux/android 目标 | 加 `core-darwin`：`CGO_ENABLED=1 go build -buildmode=c-archive`。**必须在 Mac 上跑** |
| `scripts/release-local.sh` | `--platform android\|windows\|all` | 加 `macos`。但安装包在 Mac 上构建，脚本要支持「发布一个已构建好的文件」（§6） |

**兼容性陷阱：** 老版本的 Windows 和 Android 会拒绝平台为 `macos` 的配对请求（`manager.go:319`）。所以要按顺序来：

1. 先发一个只改核心、能识别 `macos` 的版本，让所有现有设备更新；
2. 再发 macOS 端。

不要为了省事让 Mac 冒充 `windows`。拨号规则和保存目录会跟着错。

## 5. UI 要不要和 Windows 完全不同？

**要，建议用 SwiftUI（必要处用 AppKit）单独写。** 理由：

1. **WPF 不能在 macOS 上跑。** 要共用就得换框架。
2. **macOS 端的重心是系统集成。** 菜单栏、剪贴板、钥匙串、通知、Services 菜单、开机启动、截屏控制，都是 Swift 一行就能调的 API；用跨平台框架反而要绕 Objective-C 互操作。
3. **项目已经是这个模式。** 协议、存储、加密、重试、笔记合并、密码库都在 Go 核心，壳只负责界面和系统调用。Android 和 Windows 两个壳本来就没有共用 UI 代码。

**能复用的部分：**
- Go 核心全部复用，104 个方法都可直接用。
- 方法名和 JSON 契约原样沿用，见 `api-spec.md`、`vault-api.md`。
- Windows `Logic/` 里约 6.8k 行状态和格式化逻辑，可以作为「规格加测试用例」翻译成 Swift。涉及剪贴板决策、合并去抖、收件箱和传输状态、笔记列表和搜索、密码库列表和详情、错误文案、更新视图。
- 文案（`Strings*.cs`，约 880 行）。

**备选方案：Avalonia（C#，跨平台 XAML）。**
- 好处：Windows 的 `Logic/` 和测试几乎能原样共用，XAML 也大体能迁过去。
- 坏处：
  - macOS 的系统集成（菜单栏、钥匙串、剪贴板隐私、Services）仍要手写原生互操作；
  - 应用要带 .NET 运行时，体积约 60–100 MB；
  - 外观不像 Mac 原生应用。
  - Windows 如果继续用 WPF，就有两套 XAML 方言要维护。真想共用，Windows 也得一起迁到 Avalonia，那是一次大的重写和回归。
- 结论：只有当「少维护一份逻辑」比「原生体验」和「迁移风险」更重要时才选它。本计划不选。

**减少三份壳重复的办法：** 开工前审一遍 Windows `Logic/` 和 Android `logic/`。把两边都实现过、而且与界面无关的逻辑下沉到核心，作为新的核心方法。例如错误码到文案键的映射、传输状态归并、笔记搜索结果整形。这样 macOS 不用写第三遍，以后改一处三端都生效。这一步可以在 Linux 上做，是 M1 的一部分。

## 6. 构建、签名、分发和自动更新

### 6.1 构建链

1. 在 Mac 上用 `make core-darwin` 生成 `libclipbridge_core.a` 和头文件（arm64 版和 amd64 版各一份，再用 `lipo` 合并）。
2. Xcode 工程（或 Swift Package）把它和桥接头文件一起链接，生成 `ClipBridge.app`。`cbridge` 命令行工具放进 `Contents/MacOS/`。
3. 用 `ditto -c -k --keepParent ClipBridge.app clipbridge-macos.zip` 打包。
   - **不要用 `scripts/zipdir.py`。** 它不一定保留 .app 里的符号链接和扩展属性，签名会因此失效。

### 6.2 签名三选一

| 方案 | 成本 | 首次安装 | 更新后 |
| --- | --- | --- | --- |
| ad-hoc（`codesign -s -`） | 0 | 要右键「打开」，或执行 `xattr -dr com.apple.quarantine` | 签名身份每次都变：钥匙串会再问「允许访问」，防火墙可能重新询问 |
| **固定自签证书**（在钥匙串里生成一张代码签名证书，一直用它签） | 0 | 同 ad-hoc | 身份不变，钥匙串和防火墙的授权保留。**个人自用推荐** |
| Developer ID + 公证（notarytool） | 99 美元/年 | 双击即可打开 | 最顺。将来要做自动填充或分享扩展，也必须走这条 |

另外：应用自己用 `URLSession` 下载的更新包不会带隔离属性（隔离属性只有浏览器这类主动声明的应用才加）。所以未公证的包也能自更新，不会被 Gatekeeper 拦。这一点需要在 M5 实测确认。

### 6.3 发到 release-publish

- relhub 的 `clipbridge` 应用要新增 `macos` 平台。这是新增，不改动现有平台和分发地址。
- 构建在 Mac 上，清单签名在本机的 release-publish 里，两边要接起来。建议的流程：
  1. 本机和 Mac 检出同一个提交。版本号等于提交数，所以两边一致。
  2. Mac 构建、签名，把 zip 经 tailnet 用 `scp` 传到本机。
  3. 本机执行 `scripts/release-local.sh --platform macos --artifact <zip>`。这个参数要新增，作用是跳过构建，校验提交号后直接发布。
- 用 GitHub Actions macOS runner 也行：runner 构建后把产物作为 artifact，本机用 `gh run download` 取回再发布。签名证书要放进 Actions secrets。这正是「签名密钥不进 CI」的老问题。**自签证书可以接受，Developer ID 证书要慎重。**

### 6.4 自更新的替换流程

1. 核心下载并校验签名清单和包，与现有流程相同。
2. 用 `ditto -x -k` 解压到 `~/Library/Caches/ClipBridge/staging/`，校验包内 `ClipBridge.app` 的签名（`codesign --verify`）。
3. 启动辅助进程，传入 pid 和目标路径。
4. 主程序退出。辅助进程等旧进程结束后，把旧 `.app` 改名为 `.old`，把新的移过去。失败就回滚。
5. 用 `open -n` 启动新版本。
6. 如果应用在 `/Applications` 下，而用户没有那里的写权限（例如非管理员账户），就不能静默替换，只提示用户手动下载。

## 7. 分阶段计划

工作量按一个人加 AI 辅助估算，前提是有 Mac 可用。

| 阶段 | 内容 | 在哪做 | 估计 | 完成标准 |
| --- | --- | --- | --- | --- |
| **M0 决策** | 回答 §8 的问题，写 ADR | 文档 | 0.5 天 | ADR 合入 |
| **M1 核心准备** | §4 的平台改动、拨号规则泛化、`macos` 更新平台、发布脚本加 `--artifact`；把三端重复的逻辑下沉到核心；**先发一个兼容版本给现有设备** | Linux | 2–3 天 | Go 测试全绿；Windows/Android 新版能和「平台 = macos」的测试对端配对 |
| **M2 壳骨架** | Xcode 工程、`c-archive` 链接、`CoreBridge`（`cb_init/cb_call/cb_next_event` 加事件泵线程）、菜单栏图标、主窗口侧边栏、钥匙串主密钥、日志、开机启动 | Mac | 2–3 天 | 应用能启动，诊断页显示核心版本和设备 ID |
| **M3 配对和剪贴板** | 显示二维码、加入、比对数字；剪贴板轮询和发送；接收写剪贴板（只留本机）；收件箱；通知 | Mac | 3–4 天 | 和手机、Windows 互相复制文本，往返无回声 |
| **M4 文件** | 拖放、选择文件、Dock 拖放、Services 菜单、接收确认、进度、在 Finder 中显示 | Mac | 2–3 天 | 双向传 1 GB 文件，断点续传可用 |
| **M5 更新和打包** | 自更新辅助进程、固定签名、`ditto` 打包、发布到 relhub | Mac + Linux | 2–3 天 | 装 0.1.N，发布 0.1.N+1，自动更新成功，钥匙串不再弹窗 |
| **M6 密码库** | 解锁、列表、详情、编辑、生成器、备份、截屏防护（尽力而为）、复制后限时清除 | Mac | 4–6 天 | 与 Windows 的密码库验收清单一致 |
| **M7 笔记** | 列表、搜索、Markdown 编辑和预览、冲突、历史、导入导出、附件 | Mac | 5–8 天 | 与 Windows 的笔记验收清单一致 |

- **MVP（M1–M5）：约 2–3 周。** 有了 MVP，Mac 就能当一台完整的剪贴板和文件设备。
- **与 Windows 功能对齐（加 M6、M7）：再加约 2 周。**
- 笔记编辑器是最大的不确定项。可以先做只读预览加纯文本编辑，富编辑放到后面。

## 8. 开工前需要用户决定的问题

1. **有没有可用的 Mac？** 如果没有，是只用 GitHub macOS runner 构建（测试就没法做），还是暂缓？
2. **要不要买 Apple Developer（99 美元/年）？** 不买就用固定自签证书。代价是：自动填充和分享扩展做不了，首次安装要手动放行。
3. **最低系统版本：** 建议 macOS 13。
4. **要不要 Intel 支持：** 要的话打 universal 包。
5. **MVP 范围：** 建议先做 M1–M5，密码库和笔记放在后面。
6. **UI 框架：** 本计划推荐 SwiftUI（§5）。如果你更看重少维护一份逻辑，可以改选 Avalonia，但 Windows 也要跟着迁。

## 9. 风险

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| 剪贴板隐私弹窗 | 「复制即同步」变成每次都要确认 | M3 第一天就在目标系统上实测；必要时改成「菜单栏点一下才发送」 |
| 老设备拒绝 `macos` 平台的配对 | Mac 配不上 | 按 M1 的顺序，先发兼容版本 |
| 签名身份变化 | 钥匙串和防火墙反复询问 | 用固定自签证书 |
| 构建在 Mac、发布在 Linux | 两边提交不一致，版本号错位 | `--artifact` 发布时校验包内记录的提交号 |
| 三端重复逻辑 | 改一个行为要改三遍 | M1 先把可下沉的逻辑放进核心 |
| Mac 合盖睡眠 | 消息延迟 | 唤醒时立即重连；发件箱本来就会补发 |
