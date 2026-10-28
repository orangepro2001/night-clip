# 发版操作说明（桥接版 + 之后每次发版）

给操作的人（用户本人）逐步照做。原理和取舍见 [update.md](update.md)，可复用的接入经验见 [release-publish-integration.md](release-publish-integration.md)，现状和待办见 [handoff-2026-10-28-release-publish.md](handoff-2026-10-28-release-publish.md)。

## 0. 先弄清楚现在是哪种发版

| 情况 | 做什么 |
| --- | --- |
| **第一次发版**（设备上装的还是 ≤ 0.1.79，只认旧源 `/clipbridge`） | 做 §1 → §2 → §3 → §4：发到 release-publish，**再把同一个包推到旧源**。这一版叫「桥接版」。 |
| **桥接版之后的每次发版**（所有设备都已装过桥接版或更新的版本） | 只做 §1 → §2 → §4：只发到 release-publish，不再碰旧源。 |

判断依据：桥接版装上之后，设备才开始问 `…/release`。可以用 `relhub stats -app clipbridge` 看有没有来自新版本号的检查（统计按版本号计数，不区分设备），也可以在每台设备的「关于」里看版本号。

## 1. 发版前

1. **内存**。完整发版会同时跑 gomobile、Gradle（`testDebugUnitTest lintRelease assembleDebug assembleRelease`，含 R8）和 `dotnet publish`，峰值很高。2026-10-28 第一次发 0.1.82 时，机器上已有一台约 10 GB 的 qemu 虚拟机在跑，内存吃紧，构建被系统在 R8 阶段中止（没有发布任何东西，relhub 和仓库都没变）。再次发版前先 `free -h`，保证可用内存充足（经验值：可用 ≥ 12 GB），必要时先关掉不用的虚拟机或大进程。
2. **提交**。版本号 = `git rev-list --count HEAD`，所以要发的内容必须已经提交，工作区必须干净（脚本拒绝脏工作区）。要发布的提交数就是 `versionCode`。
3. **预检**（几秒，不构建、不上传，可以随时跑）：

   ```sh
   cd ~/dev/Bark/night-clip
   scripts/release-local.sh --check
   ```

   期望输出 `preflight ok: …`。它会检查：服务能连上；服务**正在用的签名公钥**在 `core/update/keys.go` 的受信任列表里；这个 `versionCode` 在服务里还没被用过。任何一项失败都不要继续，先看错误信息（常见：`release.local.env` 里的 `CB_UPDATE_URL` 不对 / `release-publish` 服务没起）。

## 2. 发布到 release-publish

```sh
scripts/release-local.sh --notes "更新说明（一两句话，设备上会显示）"
```

它依次做：预检 → 构建 Android APK 和 Windows 包 → `relhub publish` → 按装好的 App 的方式验证（用编译进 App 的公钥，通过公开 HTTPS 地址验签并完整下载，与本地产物逐字节比较）。大约几分钟。

- 只发一个平台：`--platform android` 或 `--platform windows`。
- 强制所有旧版本必须更新：`--force`（抬高 `minVersionCode`，谨慎使用）。
- 一个平台成功、另一个失败：修好后 `scripts/release-local.sh --platform <失败的平台> --no-build`。
- 构建中途被系统杀掉（例如内存不足）：发布发生在构建**之后**，所以什么都没发布；释放内存后原样重跑即可。
- 号码一旦被服务用掉就不会回来（`relhub yank` 撤回也不释放号码）。脚本已经在构建前核对过，不要手工绕过。

成功后核对：

```sh
relhub list -all -app clipbridge          # 两个平台都出现新版本，渠道 stable
scripts/probe-update.sh -c 1               # 用真实核心代码 + 编译进去的公钥，通过公开地址检查并完整下载（只读）
```

`probe-update.sh` 每行输出 `平台 versionCode versionName 大小 sha256 路径`，应与刚发布的版本一致。

## 3. 桥接：把同一个包推到旧源（只有第一次发版需要）

**这一步会让真机收到更新。** 桥接版如果更新链路有错（公钥、地址、应用 id、平台名），装了它的设备就再也收不到更新，只能手动重装。所以一定要在 §2 全部通过之后再做，并且推荐先在一台设备上验证（见 §5）。

```sh
cd ~/dev/Bark/night-clip
eval "$(scripts/version.sh)"        # 得到 CB_VERSION_CODE / CB_VERSION_NAME，与 §2 发布的相同
cbdist=~/.local/bin/cbdist
feed=~/.local/share/clipbridge/releases
key=~/.config/clipbridge/release-ed25519.key
notes="更新说明（与 §2 相同）"
tmp=$(mktemp -d)

for p in android windows; do
  case $p in android) ext=apk ;; windows) ext=zip ;; esac
  # 包取 release-publish 里的那份，保证两边字节相同
  relhub download -app clipbridge -platform $p -out "$tmp/bridge.$ext"
  $cbdist publish -dir "$feed" -key "$key" -platform $p -file "$tmp/bridge.$ext" \
    -code "$CB_VERSION_CODE" -name "$CB_VERSION_NAME" -notes "$notes"
done
rm -rf "$tmp"
```

要点：

- 旧工具只在 `~/.local/bin/cbdist`（仓库里已无源码，最后一个带它的提交是 `29326f3`），用旧 Ed25519 私钥 `~/.config/clipbridge/release-ed25519.key` 签名。旧服务 `clipbridge-dist` 仍在跑，不需要重启。
- 旧源要求 `-code` 严格高于已发布的（现在是 79），所以与 release-publish 用同一个 `versionCode` 即可。
- 核对旧源：`cat ~/.local/share/clipbridge/releases/android/latest.json`、`…/windows/latest.json` 里应是新版本号、`sha256` 与 release-publish 中的相同。

## 4. 发完之后

- 推送：`git push origin main`，然后看 GitHub CI（`gh run list`；本机 `gh` 可能没登录）。
- 把这一版的版本号、时间和验证结果记到 [handoff-2026-10-28-release-publish.md](handoff-2026-10-28-release-publish.md) 的「状态」一节（并提交）。

## 5. 真机验证（桥接版必须做）

1. 一台装着 0.1.79 的设备：打开应用，应在约几秒内收到新版本通知并自动下载（Android 通知栏 / Windows 任务栏闪烁），点击安装。
2. 安装后在「关于」确认版本号是新版本。
3. 此后这台设备问的是 `…/release`：`relhub stats -app clipbridge` 里应出现来自新版本号的检查。
4. 再发下一个版本（只做 §1 → §2 → §4），确认这台设备能从 release-publish 收到——这才证明新通道是通的。如果这一步失败，旧源和旧工具还在，可以补发一个修复版到两边。
5. 两台设备（Windows 与 Android）都过了这几步之后，才考虑退役旧源。

## 6. 退役旧源（以后、单独决定）

只有在所有设备都确认已经是桥接版或更新的版本之后。每一步都影响线上，需要你明确决定后手动执行，步骤见 [update.md §7](update.md#7-从旧分发服务cbdist迁移) 的「退役旧源」。不要用不带路径的 `tailscale serve … off` 或 `tailscale serve reset`，会清掉别的服务。

## 7. 常见问题

| 现象 | 原因 / 处理 |
| --- | --- |
| 预检报 `the service signs with … which core/update/keys.go does not trust` | 服务换过签名密钥。**不要继续发版。** 把新公钥加进 `core/update/keys.go`（保留旧的）并提交后再发，且先让设备装上一个同时信任两把钥匙的版本。 |
| 预检报 `already has versionCode … or higher` | 这个提交数已经发过。先提交新的内容再发，不要改脚本。 |
| `cannot reach the update service` | `curl -fsS $CB_UPDATE_URL/healthz`（服务怎么跑由 release-publish 项目决定，目前是容器 `release-publish`，`podman ps` 可看）；`release.local.env` 里的 `CB_UPDATE_URL` 要是 `https://<本机>.ts.net/release`（注意它会覆盖命令行里同名的环境变量）。 |
| Android 签名与上次不一致 | 脚本钉了签名证书 SHA-256（`~/.config/clipbridge/android-signer.sha256`）。签名不同的 APK 装不上去，检查是不是换了 keystore。 |
| 构建被杀 / 内存不足 | 见 §1 第 1 条；释放内存后原样重跑。 |
| 想撤回一个坏版本 | `relhub channel promote` 把渠道指回旧版本，再 `relhub yank`。已升级的设备不会被降级；要让它们换到修复版，必须发更高 `versionCode` 的新版本，必要时 `--force`。 |

## 8. 不要做的事

- 不要在生产数据目录上运行 `relhub keys rotate`，不要动 `go-nx` 等别的应用。
- 不要为这个项目新增 `tailscale serve` 路径；不要对 serve 用 `reset`。
- 签名私钥不进仓库、不进 CI。`~/backups/release-publish-keys/` 和 `~/backups/clipbridge-keys/` 请另存一份到本机之外。

## 附：macOS（2026-10-28 起）

`scripts/release-local.sh` 的 `--platform` 多了 `macos`，`all` 现在是 android + windows + macos，全部在 Linux 上构建（`make dist-macos`，需要 zig 和 rcodesign）。验证用 `scripts/probe-update.sh -p macos`。细节见 [handoff-2026-10-28-macos.md](handoff-2026-10-28-macos.md)。
