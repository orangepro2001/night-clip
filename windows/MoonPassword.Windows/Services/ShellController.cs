using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Threading;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Update;
using MoonPassword.Windows.Views;

namespace MoonPassword.Windows.Services;

public enum MainPage { Vault, Settings, Diagnostics }

/// <summary>How a status message is shown (toast colour).</summary>
public enum StatusKind { Info, Success, Warning, Error }

/// <summary>
/// The application's brain, living on the UI thread: it owns the core, the tray and the models the pages bind to.
/// Rules it keeps:
/// <list type="bullet">
/// <item>the core event pump thread only parses events and marshals them to the dispatcher;</item>
/// <item>no core call runs on the UI thread (everything goes through Task.Run or the clipboard queue);</item>
/// <item>every clipboard write and clear runs on one serial worker, so a "clear after N seconds" can never
/// overtake the write it belongs to.</item>
/// </list>
/// </summary>
internal sealed partial class ShellController : IDisposable
{
    public static ShellController Current { get; private set; } = null!;

    private readonly Dispatcher _ui;
    private readonly CoreHost _core = new();
    private readonly SerialQueue _clipQueue;
    private readonly ClipboardAccess _clip = new();
    private readonly ClipboardOwnerWindow _clipOwner = new();
    private readonly TrayService _tray;
    private readonly Coalescer _updateProgressReload;
    private bool _disposed;
    private bool _exiting;
    private volatile bool _uiShutdown;
    private bool _vaultUnlocked;
    private string _lastUpdateNotice = "";

    public CoreApi Api { get; }
    public ShellPrefs Prefs { get; private set; }
    public UpdateInfo Update { get; private set; } = UpdateInfo.FromJson(default);
    public bool CoreRunning { get; private set; }
    public string DeviceName { get; private set; } = Environment.MachineName;
    public string? StartupWarning { get; private set; }

    /// <summary>A problem every page shows in its warning banner, or null.</summary>
    public string? BannerWarning => CoreRunning ? null : Strings.CoreNotRunningBanner;

    /// <summary>Every core event, typed, on the UI thread (dialogs subscribe to this).</summary>
    public event Action<CoreEvent>? EventReceived;

    /// <summary>Settings, the vault state or the update state changed.</summary>
    public event Action? StateChanged;

    /// <summary>A short message for the main window's status line.</summary>
    public event Action<string, StatusKind>? StatusMessage;

    public ShellController(Dispatcher ui)
    {
        Current = this;
        _ui = ui;
        _ui.ShutdownStarted += (_, _) => _uiShutdown = true;
        Api = new CoreApi(_core);
        Prefs = ShellPrefs.Load(App.DataDir);

        _updateProgressReload = new Coalescer(RefreshUpdateAsync);
        _clipQueue = new SerialQueue("moonpassword-clipboard", ex => AppLog.Error("clipboard worker", ex));

        _tray = new TrayService();
        _tray.OpenRequested += () => ShowMainWindow();
        _tray.SettingsRequested += () => ShowMainWindow(MainPage.Settings);
        _tray.AutoStartToggled += on => _ = SetAutoStartAsync(on);
        _tray.CheckUpdateRequested += () => _ = CheckUpdateFromTrayAsync();
        _tray.ExitRequested += ExitApp;
        _tray.SetAutoStartChecked(AutoStartRegistry.IsEnabled());
        InitVault();
        UpdateTray();
    }

    // ---- start / stop --------------------------------------------------------------------

    /// <summary>Opens the key store, starts the core and loads the first snapshot.</summary>
    /// <exception cref="DevKeyException">The key file is damaged or cannot be decrypted (never regenerated).</exception>
    public async Task StartAsync()
    {
        AppLog.Init();
        Directory.CreateDirectory(App.DataDir);
        var key = new DpapiKeyStore(new DpapiProtector()).LoadOrCreate(App.DataDir);
        AppLog.Write("key store: " + key.Source);
        StartupWarning = key.Warning;

        _core.EventReceived += OnPumpEvent;
        var config = new
        {
            data_dir = App.DataDir,
            db_key_b64 = key.KeyBase64,
            device_name = Environment.MachineName,
            platform = "windows",
            ssh_agent_host_path = Path.Combine(AppContext.BaseDirectory, "moonpassword-ssh-agent.exe"),
            app_version_code = BuildInfo.VersionCode,
            app_version_name = BuildInfo.VersionName,
            update_url = BuildInfo.UpdateUrl,
        };
        await Task.Run(() => _core.Start(config)); // opens the database: keep it off the UI thread
        CoreRunning = true;
        StartAgentPoll();
        RefreshFirefoxBridge();

        await ReloadAllAsync();
        UpdateTray();
    }

    /// <summary>First launch: nothing to ask; the flag is kept for the introduction a later version shows.</summary>
    public Task RunFirstRunIfNeededAsync(bool minimized)
    {
        if (Prefs.FirstRunDone || minimized) return Task.CompletedTask;
        Prefs.FirstRunDone = true;
        Prefs.Save(App.DataDir);
        return Task.CompletedTask;
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _agentPoll?.Stop();
        _firefoxBridge?.Dispose();
        _tray.Dispose();      // first, so the icon disappears at once
        DisposeVault();       // session hooks off, our secret off the clipboard (needs the owner window)
        _clipOwner.Dispose();
        _clipQueue.Dispose();
        _core.Dispose();
    }

    private FirefoxBridge? _firefoxBridge;

    public string? SetFirefoxIntegration(bool enabled)
    {
        try
        {
            if (enabled) FirefoxBridge.Register(); else FirefoxBridge.Unregister();
            RefreshFirefoxBridge();
            return null;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or System.Security.SecurityException or InvalidOperationException)
        {
            return "无法设置 Firefox 本机连接，请检查当前 Windows 用户是否已有其他 MoonPassword 安装启用了连接。";
        }
    }

    private void RefreshFirefoxBridge()
    {
        if (!FirefoxBridge.IsRegistered)
        {
            _firefoxBridge?.Dispose();
            _firefoxBridge = null;
        }
        else if (CoreRunning && _firefoxBridge is null)
        {
            _firefoxBridge = new FirefoxBridge(this);
            _firefoxBridge.Start();
        }
    }

    // ---- window helpers ------------------------------------------------------------------

    public MainWindow? Main => MainWindow.Instance;

    /// <summary>The tray, for the development snapshots only.</summary>
    internal TrayService TrayForSnapshot => _tray;

    public void ShowMainWindow(MainPage? page = null)
    {
        if (_disposed) return;
        var w = Main ?? new MainWindow(this);
        if (page is { } p) w.ShowPage(p);
        if (!w.IsVisible) w.Show();
        if (w.WindowState == WindowState.Minimized) w.WindowState = WindowState.Normal;
        w.Activate();
        // Bring to front even if another window has focus (Topmost toggle keeps it from staying on top).
        w.Topmost = true;
        w.Topmost = false;
        w.Focus();
    }

    public bool IsExiting => _exiting;

    /// <summary>Windows is ending the session: the main window must really close instead of hiding.</summary>
    public void MarkExiting() => _exiting = true;

    public void ExitApp()
    {
        if (_exiting) return;
        _exiting = true;
        (Application.Current?.ApplicationLifetime as IClassicDesktopStyleApplicationLifetime)?.Shutdown();
    }

    /// <summary>A short message for the user: a toast in the main window while it is shown.</summary>
    public void ShowStatus(string text, StatusKind kind = StatusKind.Info)
    {
        if (!_disposed && text.Length > 0) StatusMessage?.Invoke(text, kind);
    }

    public void Balloon(string title, string text, Action? onClick = null)
    {
        if (!_disposed && Prefs.Notifications) _tray.ShowBalloon(title, text, onClick); // Settings → 显示通知
    }

    private void UiPost(Action action)
    {
        if (_disposed || _uiShutdown) return;
        _ui.Post(() =>
        {
            try { action(); }
            catch (Exception ex) { AppLog.Error("ui callback", ex); }
        }, DispatcherPriority.Normal);
    }

    // ---- core events ---------------------------------------------------------------------

    /// <summary>Runs on the core's event-pump thread: parse and hand over to the UI thread.</summary>
    private void OnPumpEvent(System.Text.Json.JsonElement raw)
    {
        CoreEvent ev;
        try { ev = CoreEventParser.Parse(raw); }
        catch (Exception ex) { AppLog.Error("event parse", ex); return; }
        UiPost(() => Dispatch(ev));
    }

    private void Dispatch(CoreEvent ev)
    {
        AppLog.Event(ev);
        try
        {
            switch (ev)
            {
                case UpdateEvent u:
                    HandleUpdateEvent(u);
                    break;
                case VaultLockedEvent v:
                    SetVaultUnlocked(false);
                    OnVaultLockedEvent(v);
                    break;
                case VaultChangedEvent:
                    VaultChanged?.Invoke();
                    break;
                case SshUsedEvent or SshKeysNeededEvent or SshKeysEvent or SshAgentEvent:
                    HandleSshEvent(ev);
                    break;
            }
        }
        catch (Exception ex) { AppLog.Error("dispatch " + ev.Type, ex); }

        try { EventReceived?.Invoke(ev); }
        catch (Exception ex) { AppLog.Error("event subscriber " + ev.Type, ex); }
    }

    // ---- tray state ----------------------------------------------------------------------

    /// <summary>The vault page reports unlocks; locks come from the core's vault.locked event.</summary>
    public void SetVaultUnlocked(bool unlocked)
    {
        if (_vaultUnlocked == unlocked) return;
        _vaultUnlocked = unlocked;
        UpdateTray();
        StateChanged?.Invoke();
    }

    public bool VaultUnlocked => _vaultUnlocked;

    public Task<System.Text.Json.JsonElement> SessionCallAsync(string method, object? parameters = null) =>
        Task.Run(() => _core.Call(method, parameters));

    private void UpdateTray()
    {
        var state = TrayModel.Compute(CoreRunning);
        _tray.SetState(state, TrayModel.Tooltip(state, _vaultUnlocked));
        _tray.SetVaultUnlocked(_vaultUnlocked);
    }

    // ---- updates -------------------------------------------------------------------------

    private void HandleUpdateEvent(UpdateEvent u)
    {
        if (u.Type == "update.progress")
        {
            _ = _updateProgressReload.RunAsync(); // several per second: merged
            return;
        }
        _ = RefreshUpdateAsync();

        // update.available / update.ready: tray balloon (the taskbar button flashes too).
        if (u.Type is not ("update.available" or "update.ready")) return;
        var key = u.Type + ":" + u.VersionName;
        if (key == _lastUpdateNotice) return;
        _lastUpdateNotice = key;
        var text = u.Type == "update.ready" ? Strings.UpdateBannerReady(u.VersionName) : Strings.UpdateBannerAvailable(u.VersionName);
        if (u.Mandatory) text += Strings.UpdateMandatorySuffix;
        Balloon(Strings.UpdateBannerTitle, text, () => ShowMainWindow(MainPage.Settings));
        if (Main is { IsVisible: true } m) TaskbarFlash.Flash(m);
    }

    private async Task CheckUpdateFromTrayAsync()
    {
        ShowMainWindow(MainPage.Settings);
        var error = await UpdateActionAsync(UpdateInfo.Action.Check);
        if (error is not null) ShowStatus(error);
    }

    // ---- reloading snapshots -------------------------------------------------------------

    public async Task ReloadAllAsync()
    {
        await Task.WhenAll(RefreshStatusAsync(), RefreshUpdateAsync(), RefreshSshStatusAsync());
        UpdateTray();
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
        catch (CoreException ex) { AppLog.Write("status failed: " + ex.Code); }
    }

    public async Task RefreshUpdateAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.UpdateStatus());
            Update = UpdateInfo.FromJson(json);
            StateChanged?.Invoke();
        }
        catch (CoreException ex) { AppLog.Write("update.status failed: " + ex.Code); }
    }
}
