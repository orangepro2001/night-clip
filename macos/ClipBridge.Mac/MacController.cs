using System.Text.Json;
using Avalonia.Threading;
using ClipBridge.Mac.Platform;
using ClipBridge.Windows;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Update;

namespace ClipBridge.Mac;

public enum MainPage { Vault, Settings }

/// <summary>
/// The macOS counterpart of the Windows ShellController (same rules, same shared Logic): it owns the core and the
/// models the views show.
/// <list type="bullet">
/// <item>the core's event pump thread only parses events and posts them to the UI thread;</item>
/// <item>no core call runs on the UI thread;</item>
/// <item>every pasteboard write and clear runs on one serial worker, so a "clear after N seconds" never overtakes
/// the write it belongs to.</item>
/// </list>
/// The password vault lives in MacController.Vault.cs.
/// </summary>
internal sealed partial class MacController : IDisposable
{
    public static MacController Current { get; private set; } = null!;

    private readonly CoreHost _core = new();
    private readonly SerialQueue _clipQueue;
    private readonly Coalescer _updateProgressReload;
    private bool _disposed;
    private string _lastUpdateNotice = "";

    public CoreApi Api { get; }
    public ShellPrefs Prefs { get; }
    public UpdateInfo Update { get; private set; } = UpdateInfo.FromJson(default);
    public bool CoreRunning { get; private set; }
    public string DeviceName { get; private set; } = Environment.MachineName;

    /// <summary>Every core event, typed, on the UI thread (dialogs subscribe to this).</summary>
    public event Action<CoreEvent>? EventReceived;

    /// <summary>Settings or update state changed.</summary>
    public event Action? StateChanged;

    /// <summary>A short message for the main window's status line.</summary>
    public event Action<string>? StatusMessage;

    /// <summary>The main window should come up, optionally on a page.</summary>
    public event Action<MainPage?>? ShowRequested;

    /// <summary>The application should quit.</summary>
    public event Action? ExitRequested;

    public MacController()
    {
        Current = this;
        Api = new CoreApi(_core);
        Prefs = ShellPrefs.Load(MacPaths.DataDir);
        _updateProgressReload = new Coalescer(RefreshUpdateAsync);
        _clipQueue = new SerialQueue("moonpassword-clipboard", ex => MacLog.Error("clipboard worker", ex));
    }

    // ---- start / stop --------------------------------------------------------------------

    /// <exception cref="KeyStoreException">The key is damaged or missing next to an existing database.</exception>
    public async Task StartAsync()
    {
        Directory.CreateDirectory(MacPaths.DataDir);
        var key = await Task.Run(() => MacKeyStore.LoadOrCreate(MacPaths.DataDir));
        _core.EventReceived += OnPumpEvent;
        var config = new
        {
            data_dir = MacPaths.DataDir,
            db_key_b64 = key,
            device_name = Environment.MachineName,
            platform = "macos",
            app_version_code = BuildInfo.VersionCode,
            app_version_name = BuildInfo.VersionName,
            update_url = BuildInfo.UpdateUrl,
        };
        await Task.Run(() => _core.Start(config));
        CoreRunning = true;
        InitVault();
        await ReloadAllAsync();
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        DisposeVault();       // session watcher off, our secret off the pasteboard
        _clipQueue.Dispose();
        _core.Dispose();
    }

    public void ShowMain(MainPage? page = null) => ShowRequested?.Invoke(page);

    public void Exit() => ExitRequested?.Invoke();

    public void ShowStatus(string text)
    {
        if (!_disposed) StatusMessage?.Invoke(text);
    }

    /// <summary>A Notification Center banner, unless switched off in Settings → 显示通知.</summary>
    public void Notify(string title, string text)
    {
        if (Prefs.Notifications) MacNotifier.Show(title, text);
    }

    public void SetNotifications(bool on)
    {
        Prefs.Notifications = on;
        Prefs.Save(MacPaths.DataDir);
        StateChanged?.Invoke();
    }

    private void UiPost(Action action)
    {
        if (_disposed) return;
        Dispatcher.UIThread.Post(() =>
        {
            try { action(); }
            catch (Exception ex) { MacLog.Error("ui callback", ex); }
        });
    }

    // ---- core events ---------------------------------------------------------------------

    private void OnPumpEvent(JsonElement raw)
    {
        CoreEvent ev;
        try { ev = CoreEventParser.Parse(raw); }
        catch (Exception ex) { MacLog.Error("event parse", ex); return; }
        UiPost(() => Dispatch(ev));
    }

    private void Dispatch(CoreEvent ev)
    {
        MacLog.Event(ev);
        try
        {
            switch (ev)
            {
                case UpdateEvent u:
                    HandleUpdateEvent(u);
                    break;
                case VaultLockedEvent v:
                    OnVaultLockedEvent(v);
                    break;
                case VaultChangedEvent:
                    VaultChanged?.Invoke();
                    break;
            }
        }
        catch (Exception ex) { MacLog.Error("dispatch " + ev.Type, ex); }

        try { EventReceived?.Invoke(ev); }
        catch (Exception ex) { MacLog.Error("event subscriber " + ev.Type, ex); }
    }

    // ---- plain text to the pasteboard ----------------------------------------------------

    /// <summary>Copies app-generated, non-secret text through the pasteboard worker.</summary>
    public Task<bool> CopyTextAsync(string text)
    {
        var tcs = new TaskCompletionSource<bool>();
        _clipQueue.Post(() =>
        {
            var ok = false;
            if (!_disposed)
            {
                try { ok = MacPasteboard.WriteText(ClipText.ToCore(text), sensitive: false) is not null; }
                catch (Exception ex) { MacLog.Error("pasteboard write", ex); }
            }
            tcs.TrySetResult(ok);
        });
        return tcs.Task;
    }

    // ---- generic call helpers ------------------------------------------------------------

    public async Task<string?> CallAsync(Action<CoreApi> call)
    {
        try
        {
            await Task.Run(() => call(Api));
            return null;
        }
        catch (CoreException ex)
        {
            MacLog.Write("core call failed: " + ex.Code);
            return ErrorMessages.ForCode(ex.Code);
        }
        catch (Exception ex) when (ex is JsonException or KeyNotFoundException or InvalidOperationException)
        {
            MacLog.Error("core reply", ex);
            return ErrorMessages.ForCode("INTERNAL");
        }
    }

    public async Task<(T? Value, string? Error)> CallAsync<T>(Func<CoreApi, T> call)
    {
        try { return (await Task.Run(() => call(Api)), null); }
        catch (CoreException ex)
        {
            MacLog.Write("core call failed: " + ex.Code);
            return (default, ErrorMessages.ForCode(ex.Code));
        }
        catch (Exception ex) when (ex is JsonException or KeyNotFoundException or InvalidOperationException)
        {
            MacLog.Error("core reply", ex);
            return (default, ErrorMessages.ForCode("INTERNAL"));
        }
    }

    // ---- start-up ------------------------------------------------------------------------

    public bool AutoStartEnabled => MacAutoStart.IsEnabled();

    public async Task<string?> SetAutoStartAsync(bool on)
    {
        var ok = await Task.Run(() => MacAutoStart.Set(on));
        StateChanged?.Invoke();
        return ok ? null : MacStrings.AutoStartFailed;
    }

    // ---- updates -------------------------------------------------------------------------

    private void HandleUpdateEvent(UpdateEvent u)
    {
        if (u.Type == "update.progress")
        {
            _ = _updateProgressReload.RunAsync();
            return;
        }
        _ = RefreshUpdateAsync();
        if (u.Type is not ("update.available" or "update.ready")) return;
        var key = u.Type + ":" + u.VersionName;
        if (key == _lastUpdateNotice) return;
        _lastUpdateNotice = key;
        var text = u.Type == "update.ready" ? Strings.UpdateBannerReady(u.VersionName) : Strings.UpdateBannerAvailable(u.VersionName);
        if (u.Mandatory) text += Strings.UpdateMandatorySuffix;
        Notify(Strings.UpdateBannerTitle, text);
    }

    public async Task<string?> UpdateActionAsync(UpdateInfo.Action action)
    {
        string? error = null;
        try
        {
            switch (action)
            {
                case UpdateInfo.Action.Check:
                    ShowStatus(Strings.UpdateChecking);
                    await Task.Run(() => Api.UpdateCheck());
                    break;
                case UpdateInfo.Action.Download:
                    await Task.Run(() => Api.UpdateDownload());
                    break;
                case UpdateInfo.Action.Install:
                    return await InstallUpdateAsync();
            }
        }
        catch (CoreException ex) { error = ErrorMessages.ForCode(ex.Code); }
        await RefreshUpdateAsync();
        return error;
    }

    public async Task<string?> SetAutoUpdateAsync(bool on)
    {
        var error = await CallAsync(a => a.UpdateConfigAuto(on));
        await RefreshUpdateAsync();
        return error;
    }

    private async Task<string?> InstallUpdateAsync()
    {
        if (MacPaths.BundlePath is not { } target) return MacStrings.UpdateNeedsBundle;
        try
        {
            var ready = Update.ReadyPath;
            var pid = Environment.ProcessId;
            await Task.Run(() =>
            {
                var staged = MacUpdateApplier.Stage(ready, MacPaths.StagingDir);
                MacUpdateApplier.LaunchSwap(staged, target, pid);
            });
        }
        catch (Exception ex) when (ex is IOException or InvalidDataException or UnauthorizedAccessException
                                       or System.ComponentModel.Win32Exception)
        {
            MacLog.Error("update install", ex);
            return Strings.UpdatePrepareFailed(ex.Message);
        }
        Exit(); // the swap script waits for this process to exit
        return null;
    }

    // ---- snapshots -----------------------------------------------------------------------

    public async Task ReloadAllAsync()
    {
        await Task.WhenAll(RefreshStatusAsync(), RefreshUpdateAsync());
        StateChanged?.Invoke();
    }

    private async Task RefreshStatusAsync()
    {
        try
        {
            var st = await Task.Run(() => Api.Status());
            DeviceName = JsonRead.Str(st, "device_name", DeviceName);
            StateChanged?.Invoke();
        }
        catch (CoreException ex) { MacLog.Write("status failed: " + ex.Code); }
    }

    public async Task RefreshUpdateAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.UpdateStatus());
            Update = UpdateInfo.FromJson(json);
            StateChanged?.Invoke();
        }
        catch (CoreException ex) { MacLog.Write("update.status failed: " + ex.Code); }
    }
}
