using System.Text.Json;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Interactivity;
using Avalonia.Media.Imaging;
using Avalonia.Threading;
using Avalonia.VisualTree;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// Development aid: <c>ClipBridge.exe --snapshot &lt;dir&gt;</c> (only together with <see cref="App.DataDirVariable"/>, so never
/// against the real data) renders every page of the main window, the main dialogs and the small windows in light and dark to
/// PNG files and exits. Used to review the UI on a machine without a person looking at the screen.
/// <para>
/// To have something to show it writes a few vault entries and notes into that development data folder through the normal core
/// calls, and fills the device, inbox and transfer lists <em>in memory only</em> with sample rows (nothing of that reaches
/// the core; the next reload replaces it).
/// </para>
/// </summary>
internal static class Snapshots
{
    public const string Flag = "--snapshot";
    private const string DevVaultPassword = "snapshot-dev-only-password";

    public static string? ParseDir(string[] args)
    {
        var i = Array.FindIndex(args, a => string.Equals(a, Flag, StringComparison.OrdinalIgnoreCase));
        return i >= 0 && i + 1 < args.Length ? args[i + 1] : null;
    }

    public static async Task RunAsync(ShellController shell, string dir)
    {
        Directory.CreateDirectory(dir);
        shell.ShowMainWindow();
        var main = MainWindow.Instance!;
        await Task.Delay(1200);
        try { await SeedCoreDataAsync(shell); }
        catch (Exception ex) { AppLog.Error("snapshot seed", ex); }

        foreach (var mode in new[] { ThemeMode.Light, ThemeMode.Dark })
        {
            var suffix = mode.ToString().ToLowerInvariant();
            ThemeManager.SetMode(mode);
            SeedPreviewLists(shell);
            foreach (var page in Enum.GetValues<MainPage>())
            {
                main.ShowPage(page);
                await Task.Delay(page is MainPage.Vault or MainPage.Notes ? 1500 : 700);
                if (page is MainPage.Inbox or MainPage.Transfers)
                {
                    SeedPreviewLists(shell); // the page reloaded the (empty) real lists when it was shown
                    await Task.Delay(400);
                }
                if (page is MainPage.Vault or MainPage.Notes)
                {
                    SelectFirst(main, page == MainPage.Vault ? typeof(EntryRow) : typeof(NoteRow));
                    await Task.Delay(1200);
                }
                Save(main, Path.Combine(dir, $"{(int)page}-{page}.{suffix}.png"));
                if (page == MainPage.Settings && main.GetVisualDescendants().OfType<SshSettingsCard>().FirstOrDefault() is { } sshCard)
                {
                    sshCard.BringIntoView();
                    await Task.Delay(900);
                    Save(main, Path.Combine(dir, $"{(int)page}-{page}-ssh.{suffix}.png"));
                }
                if (page == MainPage.Vault && SelectWhere(main, typeof(EntryRow), o => o is EntryRow r && r.Summary.Kind == EntryKinds.Ssh))
                {
                    await Task.Delay(1500);
                    Save(main, Path.Combine(dir, $"{(int)page}-{page}-ssh.{suffix}.png"));
                }
            }

            // dialogs over the main window
            main.ShowPage(MainPage.Devices);
            await Task.Delay(500);
            shell.ShowPairListen();
            await Task.Delay(1500);
            Save(main, Path.Combine(dir, $"dlg-pair-listen.{suffix}.png"));
            DialogHost.FindFor(main)?.Close();
            await Task.Delay(300);
            shell.ShowPairJoin();
            await Task.Delay(600);
            Save(main, Path.Combine(dir, $"dlg-pair-join.{suffix}.png"));
            DialogHost.FindFor(main)?.Close();
            await Task.Delay(300);
            if (Click(main, Strings.DeviceSettingsTip))
            {
                await Task.Delay(600);
                Save(main, Path.Combine(dir, $"dlg-device-settings.{suffix}.png"));
                DialogHost.FindFor(main)?.Close();
                await Task.Delay(300);
            }

            // small windows
            var offer = new FileOfferWindow(new FileOfferedEvent { Type = "file.offered", TransferId = "tr_snapshot", From = "dev_sample", FromName = "Pixel 9", Name = "季度报告-最终版.pdf", Size = 3_482_112 });
            offer.ShowOffer();
            await Task.Delay(700);
            Save(offer, Path.Combine(dir, $"win-file-offer.{suffix}.png"));
            offer.Close();
            var request = new PairRequestWindow(new PairRequestEvent { Type = "pair.request", PairId = "pr_snapshot", Name = "MacBook Air", Platform = "macos", DeviceId = "dev_sample_mac", Sas = "482 913", ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(4).ToString("O") });
            request.ShowRequest();
            await Task.Delay(700);
            Save(request, Path.Combine(dir, $"win-pair-request.{suffix}.png"));
            request.Close();

            if (TopLevel.GetTopLevel(main) is { } && Application.Current?.ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
            {
                var groups = await shell.VaultCallAsync(a => a.Groups());
                var editor = EntryEditorWindow.ShowAsync(main, shell, groups.Ok ? groups.Value! : Array.Empty<GroupInfo>(), "", "", EntryKinds.Login);
                await Task.Delay(900);
                if (desktop.Windows.OfType<EntryEditorWindow>().FirstOrDefault() is { } w)
                {
                    Save(w, Path.Combine(dir, $"win-entry-editor.{suffix}.png"));
                    w.Close();
                }
                await editor;

                // SSH windows (docs/adr/009-ssh-agent.md)
                await SnapWindowAsync(desktop, () => SshGenerateWindow.ShowAsync(main, shell), Path.Combine(dir, $"win-ssh-generate.{suffix}.png"));
                await SnapWindowAsync(desktop, () => SshImportWindow.ShowAsync(main, shell, "", SampleKeys(dir)), Path.Combine(dir, $"win-ssh-import.{suffix}.png"));
                await SnapWindowAsync(desktop, () => SshMigrateWindow.ShowAsync(main, shell), Path.Combine(dir, $"win-ssh-migrate.{suffix}.png"), 2500);
            }
            await Task.Delay(300);
        }
        foreach (var mode in new[] { ThemeMode.Light, ThemeMode.Dark })
        {
            ThemeManager.SetMode(mode);
            SaveTrayMenu(shell, Path.Combine(dir, $"tray-menu.{mode.ToString().ToLowerInvariant()}.png"));
            await Task.Delay(300);
        }
        ThemeManager.SetMode(Appearance.ToMode(shell.Prefs.Theme));
        AppLog.Write("snapshots written to " + dir);
        shell.ExitApp();
    }

    // ---- sample content -------------------------------------------------------------------------

    /// <summary>Vault entries and notes in the development data folder, through the normal core calls (only once).</summary>
    private static async Task SeedCoreDataAsync(ShellController shell)
    {
        var creds = new VaultCreds(DevVaultPassword, null);
        var status = await shell.VaultCallAsync(a => a.Status());
        if (status.Ok && VaultScreens.From(status.Value!) == VaultScreen.Create) await shell.VaultCallAsync(a => a.Create(creds, "low"));
        else await shell.VaultCallAsync(a => a.Unlock(creds));
        var list = await shell.VaultCallAsync(a => a.List(new VaultQuery()));
        if (list.Ok && list.Value!.Count == 0)
        {
            await shell.VaultCallAsync(a => a.Add(new EntryDraft { Kind = EntryKinds.Login, Title = "GitHub", Username = "octo@example.com", Password = "c0rrect-H0rse-battery", Url = "https://github.com", Totp = "JBSWY3DPEHPK3PXP", TagsText = "work dev" }));
            await shell.VaultCallAsync(a => a.Add(new EntryDraft { Kind = EntryKinds.ApiKey, Title = "OpenWeather API", Password = "sk_live_51HxQ2example", Url = "https://api.openweathermap.org" }));
            await shell.VaultCallAsync(a => a.Add(new EntryDraft { Kind = EntryKinds.Login, Title = "家里路由器", Username = "admin", Password = "Tr0ub4dor&3", Url = "http://192.168.1.1" }));
            await shell.VaultCallAsync(a => a.Add(new EntryDraft { Kind = EntryKinds.Note, Title = "Wi-Fi 访客网络", Notes = "SSID: Home-Guest\n密码写在冰箱上。" }));
            await shell.SshCallAsync(a => a.Generate("ed25519", 0, "GitHub 部署密钥", "deploy@clipbridge"));
        }

        var notes = await shell.NotesCallAsync(api => api.List());
        if (notes.Ok && notes.Value!.Items.Count == 0)
        {
            await shell.NotesCallAsync(api => api.Create("购物清单", "# 周末采购\n\n- [x] 牛奶\n- [ ] 鸡蛋 **两盒**\n- [ ] 咖啡豆（*深烘*）\n\n> 记得带购物袋。\n"));
            await shell.NotesCallAsync(api => api.Create("部署笔记", "## 发布步骤\n\n1. 打标签 `v0.1.111`\n2. 运行 `make dist-windows`\n3. 上传到 release-publish\n\n```powershell\ndotnet publish -c Release\n```\n\n| 平台 | 状态 |\n|:--|:--:|\n| Windows | ✅ |\n| macOS | ⏳ |\n\n参见 [文档](https://example.com/docs)。\n"));
        }
    }

    /// <summary>Sample devices, inbox items and transfers, in memory only (never sent to the core).</summary>
    private static void SeedPreviewLists(ShellController shell)
    {
        var now = DateTimeOffset.Now;
        var nowS = now.ToUnixTimeSeconds();
        using (var peers = JsonDocument.Parse($$"""
            {"peers":[
              {"device_id":"dev_pixel","name":"Pixel 9","platform":"android","state":"connected","outbox_pending":0,"last_seen_at":{{nowS}},"apply_mode":"auto","send_enabled":true},
              {"device_id":"dev_mac","name":"MacBook Air","platform":"macos","state":"offline","outbox_pending":3,"last_seen_at":{{nowS - 5400}},"apply_mode":"manual","send_enabled":true},
              {"device_id":"dev_office","name":"办公室台式机","platform":"windows","state":"connecting","outbox_pending":0,"last_seen_at":{{nowS - 60}},"apply_mode":"notify","send_enabled":false},
              {"device_id":"dev_old","name":"旧平板","platform":"android","state":"unpaired","outbox_pending":0,"last_seen_at":{{nowS - 864000}},"apply_mode":"notify","send_enabled":true}
            ]}
            """))
        {
            shell.Peers.Clear();
            foreach (var p in PeerItem.ListFromJson(peers.RootElement, now)) shell.Peers.Add(p);
        }

        shell.Inbox.Clear();
        shell.Inbox.Add(new InboxRow("ev_1", "dev_pixel", "Pixel 9", "https://maps.app.goo.gl/wRpqk3sN8xFj2", false, "received", nowS - 40, now));
        shell.Inbox.Add(new InboxRow("ev_2", "dev_mac", "MacBook Air", "会议纪要：\n1. 周五前完成 Windows 端界面迁移\n2. 下周一评审设计稿，确认深色模式配色\n3. 更新用户手册里关于配对的截图和说明，删掉过时的旧版本流程描述，并补充二维码扫描失败时的处理方式与常见问题。", false, "applied", nowS - 3600, now));
        shell.Inbox.Add(new InboxRow("ev_3", "dev_pixel", "Pixel 9", "839201", true, "received", nowS - 7200, now));
        shell.Inbox.Add(new InboxRow("ev_4", "dev_office", "办公室台式机", "SELECT * FROM orders WHERE created_at > now() - interval '1 day';", false, "ignored", nowS - 90000, now));

        using var transfers = JsonDocument.Parse($$"""
            {"transfers":[
              {"transfer_id":"tr_1","peer_id":"dev_pixel","peer_name":"Pixel 9","direction":"in","name":"IMG_20261120_0815.jpg","size":4823112,"offset":2911000,"rate_bps":5242880,"state":"transferring","created_at":{{nowS - 20}}},
              {"transfer_id":"tr_2","peer_id":"dev_mac","peer_name":"MacBook Air","direction":"in","name":"设计稿-v3.fig","size":28311000,"offset":0,"rate_bps":0,"state":"offered","created_at":{{nowS - 120}}},
              {"transfer_id":"tr_3","peer_id":"dev_pixel","peer_name":"Pixel 9","direction":"out","name":"发票-2026-11.pdf","size":381220,"offset":381220,"rate_bps":0,"state":"done","path":"C:\\Users\\Public\\Documents\\发票-2026-11.pdf","created_at":{{nowS - 4000}}},
              {"transfer_id":"tr_4","peer_id":"dev_office","peer_name":"办公室台式机","direction":"out","name":"backup.zip","size":981220000,"offset":120000000,"rate_bps":0,"state":"failed","error":"PEER_OFFLINE","created_at":{{nowS - 9000}}}
            ]}
            """);
        shell.Transfers.ReplaceAll(transfers.RootElement);
    }

    // ---- driving the UI -------------------------------------------------------------------------

    /// <summary>Selects the first row of the list whose items are of <paramref name="rowType"/>.</summary>
    private static void SelectFirst(Window window, Type rowType)
    {
        var list = window.GetVisualDescendants().OfType<ListBox>()
            .FirstOrDefault(l => l.IsEffectivelyVisible && l.ItemsSource is System.Collections.IEnumerable items && items.Cast<object>().FirstOrDefault()?.GetType() == rowType);
        if (list is not null) list.SelectedIndex = 0;
    }

    /// <summary>Two throwaway keys for the import window: a plain PKCS#8 one and a passphrase-protected one.</summary>
    private static IReadOnlyList<string> SampleKeys(string dir)
    {
        var plain = Path.Combine(dir, "sample-plain.pem");
        var locked = Path.Combine(dir, "sample-locked.pem");
        using var a = System.Security.Cryptography.ECDsa.Create(System.Security.Cryptography.ECCurve.NamedCurves.nistP256);
        File.WriteAllText(plain, a.ExportPkcs8PrivateKeyPem());
        using var b = System.Security.Cryptography.ECDsa.Create(System.Security.Cryptography.ECCurve.NamedCurves.nistP384);
        File.WriteAllText(locked, b.ExportEncryptedPkcs8PrivateKeyPem("snapshot",
            new System.Security.Cryptography.PbeParameters(System.Security.Cryptography.PbeEncryptionAlgorithm.Aes256Cbc, System.Security.Cryptography.HashAlgorithmName.SHA256, 10_000)));
        return new[] { plain, locked };
    }

    /// <summary>Selects the first row of a <paramref name="rowType"/> list that matches; false when there is none.</summary>
    private static bool SelectWhere(Window window, Type rowType, Func<object, bool> match)
    {
        var list = window.GetVisualDescendants().OfType<ListBox>()
            .FirstOrDefault(l => l.IsEffectivelyVisible && l.ItemsSource is System.Collections.IEnumerable items && items.Cast<object>().FirstOrDefault()?.GetType() == rowType);
        if (list?.ItemsSource is not System.Collections.IEnumerable all) return false;
        var index = all.Cast<object>().ToList().FindIndex(o => match(o));
        if (index < 0) return false;
        list.SelectedIndex = index;
        return true;
    }

    /// <summary>
    /// Opens a vault window through <paramref name="open"/>, saves it once it has settled, closes it. The window types
    /// are not Windows themselves (they own a VaultWindow), so the newest VaultWindow is the one captured.
    /// </summary>
    private static async Task SnapWindowAsync(IClassicDesktopStyleApplicationLifetime desktop, Func<Task> open, string path, int settleMs = 1200)
    {
        var before = desktop.Windows.OfType<VaultWindow>().ToHashSet();
        var task = open();
        await Task.Delay(settleMs);
        if (desktop.Windows.OfType<VaultWindow>().FirstOrDefault(w => !before.Contains(w)) is { } w)
        {
            Save(w, path);
            w.Close();
        }
        await task;
    }

    /// <summary>Clicks the first visible button with the given tooltip.</summary>
    private static bool Click(Window window, string tooltip)
    {
        var button = window.GetVisualDescendants().OfType<Button>().FirstOrDefault(b => b.IsEffectivelyVisible && Equals(ToolTip.GetTip(b), tooltip));
        if (button is null) return false;
        button.RaiseEvent(new RoutedEventArgs(Button.ClickEvent));
        return true;
    }

    /// <summary>The tray menu is a WinForms popup: opened at a fixed point and painted into a bitmap.</summary>
    private static void SaveTrayMenu(ShellController shell, string path)
    {
        var tray = shell.TrayForSnapshot;
        var bounds = tray.ShowMenuForSnapshot(new System.Drawing.Point(200, 200));
        System.Windows.Forms.Application.DoEvents(); // let the menu lay out before it is painted
        using (var shot = new System.Drawing.Bitmap(bounds.Width, bounds.Height))
        {
            tray.DrawMenu(shot);
            shot.Save(path, System.Drawing.Imaging.ImageFormat.Png);
        }
        tray.HideMenu();
    }

    private static void Save(Window window, string path)
    {
        Dispatcher.UIThread.RunJobs();
        var scale = window.RenderScaling;
        var size = new PixelSize((int)(window.ClientSize.Width * scale), (int)(window.ClientSize.Height * scale));
        using var bitmap = new RenderTargetBitmap(size, new Vector(96 * scale, 96 * scale));
        bitmap.Render(window);
#pragma warning disable CS0618 // the encoder-options overload is not needed for a plain PNG
        bitmap.Save(path);
#pragma warning restore CS0618
    }
}
