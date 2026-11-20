# 交接：Android 发送图片/视频与拍照并发送（2026-10-28）

> 读完这一份就能接着干。用户使用说明在 [user-guide.md §6](user-guide.md)，Android 壳的契约在 [shell-guide.md §4 第 9 条](shell-guide.md)。

## 1. 需求与范围

用户（单人开发者，自用）在真机收到更新推送后提出：**Android 版支持发送图片和视频，并提供“立刻拍照并传输”的按钮（相机图标）**。

做了：设备卡片上的“图片/视频”入口、设备卡片和设备页顶部的相机图标。没做（有意）：一键拍视频、把拍的照片存进相册、Windows 端改动（接收端把它们当普通文件，核心没有类型限制，单文件上限默认 4 GiB）。

## 2. 行为

| 入口 | 行为 |
| --- | --- |
| 设备卡片“发送文件” | 不变：`GET_CONTENT */*`，可多选，任意文件 |
| 设备卡片“图片/视频” | 系统照片选择器 `PickMultipleVisualMedia(ImageAndVideo)`，照片和视频、可多选，不需要存储权限。选择器不可用（没有照片选择器且没有 `OPEN_DOCUMENT` 的 ROM）时退回 `GET_CONTENT */*` |
| 设备卡片相机图标 | 拍照后发给这台设备 |
| 设备页顶部相机图标 | 只有一台设备可发时直接拍；多台时弹窗选一台或“所有已连接的设备”（`CaptureFiles.everyoneTargets`：已连接且可发的，没有已连接的就是所有可发的，这样离线时用户看到的是真实的错误） |

拍照流程：检查 `CAMERA` 权限（清单声明了它用于扫码配对；声明之后系统相机必须在权限已授予时才能拉起，否则 `SecurityException`，所以先申请）→ `CameraCapture.prepare` 清理缓存里超过 24 小时的旧照片并预留 `IMG_yyyyMMdd_HHmmss.jpg` → `TakePicture` 把照片写进 `<cacheDir>/camera/`（FileProvider，复用 authority `<包名>.updates`，`res/xml/update_paths.xml` 新增 `camera` 目录）→ 返回后 `AppViewModel.sendCapture`：文件为空则提示“没有拍到照片，未发送”，否则对每个目标各开一次描述符并 offer（一个描述符只能交给核心一次）。**照片不进相册**；文件留在缓存、不在发送后删除（核心读的是描述符，且对方可能很久之后才接收），下一次拍照时清理。发送失败只显示错误，需要重拍。

状态保存：拍照期间系统相机在前台，本页可能被重建甚至进程被回收，所以“照片路径”和“目标设备”存在 `rememberSaveable` 里（目标用逗号拼成字符串）。

## 3. 改了哪些文件

| 文件 | 内容 |
| --- | --- |
| `android/.../logic/CaptureFiles.kt`（新） | 纯逻辑：照片命名、过期判断、“所有设备”的目标选择、目标列表存取 |
| `android/.../platform/CameraCapture.kt`（新） | 预留文件、清理旧照片、FileProvider URI |
| `android/.../ui/DevicesScreen.kt` | 照片选择器与相机启动器、权限请求、顶部相机按钮和选设备弹窗、设备卡片新按钮（`FlowRow`，窄屏换行） |
| `android/.../ui/AppViewModel.kt` | `sendCapture` |
| `android/app/src/main/res/drawable/ic_photo_camera.xml`（新） | 相机图标（项目没有 `material-icons-extended`，就地放一个矢量图；已渲染确认是相机） |
| `res/xml/update_paths.xml`、`AndroidManifest.xml`（注释）、`res/values/strings.xml` | `camera/` 目录；文案 |
| `android/app/src/test/.../logic/CaptureFilesTest.kt`（新） | 7 个测试 |

## 4. 验证（2026-10-28）

- `:app:testDebugUnitTest` 全部通过（含新增的 7 个）；`:app:assembleDebug` 通过；`:app:lintDebug` 没有新增告警（只有原来的两条）。
- **没有真机验证，也没有在模拟器里跑**：本机的两个 AVD 因为没有 `/dev/kvm` 起不来。下面这些只有真机能确认，请用 [HANDOFF.md §6](../HANDOFF.md) 的清单走一遍：
  1. 第一次点相机图标：弹出相机权限；允许后进入系统相机；拒绝后提示“没有相机权限…”。
  2. 拍照确认后，照片作为 `IMG_….jpg` 出现在对方电脑的“接收”提示里，接收后内容正确。
  3. 系统相机能写入 FileProvider 的 URI（不同厂商相机有差异；失败时现象多半是“没有拍到照片，未发送”）。
  4. “图片/视频”：照片选择器能多选并混选照片和视频；大视频（几百 MB）能发出并在对方收齐后校验通过。
  5. 顶部相机图标在两台设备在线时弹出选设备对话框；窄屏下设备卡片按钮换行不挤压。
- 不要改的：`FileSender` 的描述符所有权约定（detach 之后属于核心，失败也不再关闭）。

## 5. 以后可能要做

- 一键拍视频（`CaptureVideo`），和照片共用同一条路径。
- 把拍的照片同时存进相册（`MediaStore`，API 29+ 不需要权限；API 26–28 要写存储权限）——是否需要取决于用户用起来的感受。
- 发送失败时保留并提供“重发”。
