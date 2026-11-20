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
/// Development aid: <c>--snapshot &lt;dir&gt;</c> (only together with <see cref="App.DataDirVariable"/>, so never against
/// the real data) renders every page of the main window and the vault and SSH windows in light and dark to PNG files and
/// exits. Used to review the UI on a machine without a person looking at the screen.
/// <para>
/// To have something to show it writes a few vault entries into that development data folder through the normal core calls.
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
            foreach (var page in Enum.GetValues<MainPage>())
            {
                main.ShowPage(page);
                await Task.Delay(page is MainPage.Vault ? 1500 : 700);
                if (page is MainPage.Vault)
                {
                    SelectFirst(main, typeof(EntryRow));
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

    /// <summary>Vault entries in the development data folder, through the normal core calls (only once).</summary>
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
            var key = await shell.SshCallAsync(a => a.Generate("ed25519", 0, "GitHub 部署密钥", "deploy@moonpassword"));
            if (key.Ok) await shell.SshCallAsync(a => a.SetHosts(key.Value!.Id, new[] { "github.com", "*.github.com" }));
        }
        // The agent on its own pipe (never the real ssh one), so the SSH screens show a running agent with loaded keys.
        if (!shell.SshState.Listening)
        {
            await shell.CallAsync(a => a.SettingsSet("ssh_agent_pipe", @"\\.\pipe\moonpassword-snapshot-" + Environment.ProcessId));
            await shell.SetSshAgentAsync(true);
        }
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
