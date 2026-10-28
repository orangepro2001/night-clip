using System.Collections.ObjectModel;
using System.Windows;
using System.Windows.Threading;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Update;
using ClipBridge.Windows.Views;

namespace ClipBridge.Windows.Services;

public enum MainPage { Devices, Inbox, Transfers, Vault, Notes, Settings, Diagnostics }

internal enum ApplyOutcome { Written, Downgraded, Failed }

/// <summary>
/// The application's brain, living on the UI thread: it owns the core, the tray, the clipboard
/// monitor and the models the pages bind to. Rules it keeps:
/// <list type="bullet">
/// <item>the core event pump thread only parses events and marshals them to the dispatcher;</item>
/// <item>no core call runs on the UI thread (everything goes through Task.Run or the clipboard queue);</item>
/// <item>everything that reads or writes the clipboard runs on one serial worker, so "write + clip.mark_applied"
/// always finishes before the next clipboard read starts.</item>
/// </list>
/// </summary>
internal sealed partial class ShellController : IDisposable
{
    public static ShellController Current { get; private set; } = null!;

    private readonly Dispatcher _ui;
    private readonly CoreHost _core = new();
    private readonly SerialQueue _clipQueue;
    private readonly ClipboardAccess _clip = new();
    private readonly ClipboardMonitor _monitor;
    private readonly TrayService _tray;
    private readonly Coalescer _peersReload;
    private readonly Coalescer _transfersReload;
    private readonly Coalescer _updateProgressReload;
    private volatile uint _ownWriteSeq;
    private volatile bool _captureOn;
    private bool _disposed;
    private bool _exiting;
    private string _lastUpdateNotice = "";

    public CoreApi Api { get; }
    public ShellPrefs Prefs { get; private set; }
    public WarningTracker Warnings { get; } = new();
    public ObservableCollection<PeerItem> Peers { get; } = new();
    public ObservableCollection<InboxRow> Inbox { get; } = new();
    public TransferTracker Transfers { get; } = new();
    public SettingsValues Settings { get; private set; }
    public UpdateInfo Update { get; private set; } = UpdateInfo.FromJson(default);
    public bool CoreRunning { get; private set; }
    public string DeviceId { get; private set; } = "";
    public string DeviceName { get; private set; } = Environment.MachineName;
    public string FingerprintPrefix { get; private set; } = "";
    public string? StartupWarning { get; private set; }

    /// <summary>Every core event, typed, on the UI thread (dialogs subscribe to this).</summary>
    public event Action<CoreEvent>? EventReceived;

    /// <summary>Peers, settings, warnings or update state changed.</summary>
    public event Action? StateChanged;

    /// <summary>A short message for the main window's status line.</summary>
    public event Action<string>? StatusMessage;

    public ShellController(Dispatcher ui)
    {
        Current = this;
        _ui = ui;
        Api = new CoreApi(_core);
        Prefs = ShellPrefs.Load(App.DataDir);
        Settings = new SettingsValues(true, "auto", SettingsValues.DefaultTtl, SettingsValues.DefaultHistory, true,
            ShellPrefs.ResolveDownloadDir(null, Prefs.DownloadDir, KnownFolders.DefaultReceiveDir()), SettingsValues.DefaultMaxFileBytes);

        _peersReload = new Coalescer(LoadPeersAsync);
        _transfersReload = new Coalescer(LoadTransfersAsync);
        _updateProgressReload = new Coalescer(RefreshUpdateAsync);
        _clipQueue = new SerialQueue("clipbridge-clipboard", ex => AppLog.Error("clipboard worker", ex));
        _monitor = new ClipboardMonitor(ui);
        _monitor.Changed += () => _clipQueue.Post(ReadAndSendLocalClipboard);

        _tray = new TrayService();
        _tray.OpenRequested += () => ShowMainWindow();
        _tray.PairRequested += ShowPairListen;
        _tray.SyncToggled += on => _ = SetCaptureAsync(on);
        _tray.SendNowRequested += () => _clipQueue.Post(SendCurrentClipboard);
        _tray.PauseRequested += minutes => _ = SetPausedAsync(minutes);
        _tray.UndoRequested += () => _ = UndoLastAutoApplyAsync();
        _tray.AutoStartToggled += on => _ = SetAutoStartAsync(on);
        _tray.CheckUpdateRequested += () => _ = CheckUpdateFromTrayAsync();
        _tray.ExitRequested += ExitApp;
        _tray.SetAutoStartChecked(AutoStartRegistry.IsEnabled());
        InitVault();
        UpdateTray();
    }

    // ---- start / stop --------------------------------------------------------------------

    /// <summary>Opens the key store, starts the core and the clipboard listener, and loads the first snapshot.</summary>
    /// <exception cref="DevKeyException">The key file is damaged or cannot be decrypted (never regenerated).</exception>
    public async Task StartAsync()
    {
        AppLog.Init();
        // Held images of an earlier run have no preview window any more: they were never saved, so they go.
        try { if (Directory.Exists(ImageHoldRoot)) Directory.Delete(ImageHoldRoot, recursive: true); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { AppLog.Error("image hold cleanup", ex); }
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
            listen = true,
            port = 47821,
            app_version_code = BuildInfo.VersionCode,
            app_version_name = BuildInfo.VersionName,
            update_url = BuildInfo.UpdateUrl,
        };
        await Task.Run(() => _core.Start(config)); // opens the database: keep it off the UI thread
        CoreRunning = true;

        if (!_monitor.Start()) AppLog.Write("clipboard listener unavailable");
        await ReloadAllAsync();
        UpdateTray();
    }

    /// <summary>
    /// First launch: sync is on by default (the core's default), so there is nothing to ask. The user is told once,
    /// non-blockingly, that copying now reaches every paired device and where to pause it.
    /// </summary>
    public Task RunFirstRunIfNeededAsync(bool minimized)
    {
        if (Prefs.FirstRunDone) return Task.CompletedTask;
        if (Settings.Capture && !minimized) Balloon(Strings.AutoSyncOnTitle, Strings.AutoSyncOnText);
        else if (Settings.Capture) return Task.CompletedTask; // started at login: tell them on the next normal start
        Prefs.FirstRunDone = true;
        Prefs.Save(App.DataDir);
        return Task.CompletedTask;
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _tray.Dispose();      // first, so the icon disappears at once
        FlushNotesOnExit();   // typed note text is saved while the core still runs
        DisposeVault();       // session hooks off, our secret off the clipboard (needs the monitor's window)
        _monitor.Dispose();   // RemoveClipboardFormatListener + RemoveHook
        _clipQueue.Dispose();
        _core.Dispose();
    }

    // ---- window helpers ------------------------------------------------------------------

    public MainWindow? Main => MainWindow.Instance;

    public void ShowMainWindow(MainPage? page = null)
    {
        if (_disposed) return;
        var w = Main ?? new MainWindow();
        if (!w.IsVisible) w.Show();
        if (w.WindowState == WindowState.Minimized) w.WindowState = WindowState.Normal;
        if (page is { } p) w.ShowPage(p);
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
        Application.Current.Shutdown();
    }

    public void ShowStatus(string text)
    {
        if (!_disposed) StatusMessage?.Invoke(text);
    }

    public void Balloon(string title, string text, Action? onClick = null)
    {
        if (!_disposed && Prefs.Notifications) _tray.ShowBalloon(title, text, onClick); // Settings → 显示通知
    }

    private void UiPost(Action action)
    {
        if (_disposed || _ui.HasShutdownStarted) return;
        _ui.BeginInvoke(DispatcherPriority.Normal, new Action(() =>
        {
            try { action(); }
            catch (Exception ex) { AppLog.Error("ui callback", ex); }
        }));
    }

    // ---- core events ---------------------------------------------------------------------

    /// <summary>Runs on the core's event-pump thread: parse, snapshot the clipboard sequence, hand over to the UI thread.</summary>
    private void OnPumpEvent(System.Text.Json.JsonElement raw)
    {
        CoreEvent ev;
        try { ev = CoreEventParser.Parse(raw); }
        catch (Exception ex) { AppLog.Error("event parse", ex); return; }

        // docs/shell-guide.md §3 item 5: the sequence number is recorded when the event arrives,
        // before any dispatching delay, so a copy made in the meantime is always noticed.
        if (ev is ClipReceivedEvent clip) ev = clip with { SeqAtArrival = ClipboardAccess.SequenceNumber() };

        var typed = ev;
        UiPost(() => Dispatch(typed));
    }

    private void Dispatch(CoreEvent ev)
    {
        AppLog.Event(ev);
        try
        {
            if (Warnings.Apply(ev)) { UpdateTray(); StateChanged?.Invoke(); }

            switch (ev)
            {
                case CoreListeningEvent:
                    _ = RefreshStatusAsync();
                    break;
                case PeerStateEvent:
                    _ = _peersReload.RunAsync();
                    break;
                case PeerRevokedEvent r:
                    Balloon(Strings.PeerRevokedTitle, Strings.PeerRevokedText(r.Name.Length > 0 ? r.Name : r.DeviceId));
                    _ = _peersReload.RunAsync();
                    break;
                case ClipReceivedEvent c:
                    HandleClipReceived(c);
                    break;
                case PairRequestEvent p:
                    HandlePairRequest(p);
                    break;
                case PairResultEvent pr:
                    HandlePairResult(pr);
                    break;
                case FileOfferedEvent or FileStateEvent or FileProgressEvent or FileDoneEvent or FileFailedEvent:
                    HandleFileEvent(ev);
                    break;
                case UpdateEvent u:
                    HandleUpdateEvent(u);
                    break;
                case VaultLockedEvent v:
                    OnVaultLockedEvent(v);
                    break;
                case VaultChangedEvent:
                    VaultChanged?.Invoke();
                    break;
                case NotesChangedEvent or NotesSyncUpdatedEvent or NotesSyncConflictEvent or NotesAttachmentEvent:
                    HandleNotesEvent(ev);
                    break;
            }
        }
        catch (Exception ex) { AppLog.Error("dispatch " + ev.Type, ex); }

        try { EventReceived?.Invoke(ev); }
        catch (Exception ex) { AppLog.Error("event subscriber " + ev.Type, ex); }
    }

    // ---- tray state ----------------------------------------------------------------------

    private void UpdateTray()
    {
        var states = Peers.Select(p => p.State).ToList();
        var state = TrayModel.Compute(CoreRunning, Warnings.Any, states);
        _tray.SetState(state, TrayModel.Tooltip(state, states));
    }

    // ---- pairing -------------------------------------------------------------------------

    public void ShowPairListen() => PairListenWindow.ShowOrActivate();

    public void ShowPairJoin() => PairJoinWindow.ShowOrActivate();

    private readonly Dictionary<string, PairRequestWindow> _pairRequestWindows = new();

    private void HandlePairRequest(PairRequestEvent p)
    {
        if (p.PairId.Length == 0 || _pairRequestWindows.ContainsKey(p.PairId)) return;
        var window = new PairRequestWindow(p);
        _pairRequestWindows[p.PairId] = window;
        window.Closed += (_, _) => _pairRequestWindows.Remove(p.PairId);
        Balloon(Strings.PairRequestBalloonTitle, Strings.PairRequestBalloon(p.Name),
            () => { if (_pairRequestWindows.TryGetValue(p.PairId, out var w)) w.Activate(); });
        window.Show();
        if (Main is { IsVisible: true } m) TaskbarFlash.Flash(m);
    }

    private void HandlePairResult(PairResultEvent r)
    {
        _ = _peersReload.RunAsync();
        if (r.State == "accepted" && r.Role == "listener")
        {
            var name = r.Name.Length > 0 ? r.Name : Strings.Unknown;
            Balloon(Strings.PairSuccess, Strings.PairSuccessWith(name));
        }
    }

    // ---- files ---------------------------------------------------------------------------

    private int _openOfferDialogs;
    private const int MaxOfferDialogs = 3;

    private void HandleFileEvent(CoreEvent ev)
    {
        var known = Transfers.Apply(ev);
        if (!known) _ = _transfersReload.RunAsync();

        switch (ev)
        {
            case FileOfferedEvent o:
            {
                var from = o.FromName.Length > 0 ? o.FromName : o.From;
                Balloon(Strings.FileOfferBalloonTitle,
                    Strings.FileOfferBalloon(from, FileDisplay.Shorten(o.Name, 40), Formatting.Bytes(o.Size)),
                    () => ShowMainWindow(MainPage.Transfers));
                if (_openOfferDialogs < MaxOfferDialogs)
                {
                    _openOfferDialogs++;
                    var window = new FileOfferWindow(o);
                    window.Closed += (_, _) => _openOfferDialogs--;
                    window.Show();
                }
                if (Main is { IsVisible: true } m) TaskbarFlash.Flash(m);
                break;
            }
            case FileDoneEvent d when d.Direction == "in":
            {
                // Images open a preview (copy to clipboard / show in Explorer); other files, or too many previews, a balloon.
                var from = Transfers.Find(d.TransferId)?.PeerName is { Length: > 0 } n ? n : Strings.Unknown;
                var held = ReceivedImages.IsHeld(d.Path, ImageHoldRoot);
                if (FileDisplay.IsPreviewableImage(d.Name, d.Size) && ImagePreviewWindow.TryOpen(d.Path, d.Name, from, held)) break;
                // No preview window free: a held image must not be lost, so it goes to the download folder after all.
                if (held && MoveHeldToDownloads(d.Path) is { } moved) d = d with { Path = moved };
                Balloon(Strings.FileDoneTitle, Strings.FileDoneText(FileDisplay.Shorten(d.Name, 60)),
                    () => OpenContainingFolder(d.Path));
                break;
            }
            case FileFailedEvent f:
                Balloon(Strings.FileFailedTitle, ErrorMessages.ForFile(f.Code), () => ShowMainWindow(MainPage.Transfers));
                break;
        }
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
        await Task.WhenAll(RefreshStatusAsync(), ReloadSettingsAsync(), _peersReload.RunAsync(),
            ReloadInboxAsync(), _transfersReload.RunAsync(), RefreshUpdateAsync());
        UpdateTray();
        StateChanged?.Invoke();
    }

    public Task ReloadPeersAsync() => _peersReload.RunAsync();
    public Task ReloadTransfersAsync() => _transfersReload.RunAsync();

    private async Task RefreshStatusAsync()
    {
        try
        {
            var st = await Task.Run(() => Api.Status());
            DeviceId = JsonRead.Str(st, "device_id");
            DeviceName = JsonRead.Str(st, "device_name", DeviceName);
            var id = await Task.Run(() => Api.Identity());
            var fp = JsonRead.Str(id, "fingerprint");
            FingerprintPrefix = fp.Length >= 8 ? fp[..8] : fp;
            StateChanged?.Invoke();
        }
        catch (CoreException ex) { AppLog.Write("status failed: " + ex.Code); }
    }

    private async Task LoadPeersAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.PeersList());
            var list = PeerItem.ListFromJson(json, DateTimeOffset.Now);
            Peers.Clear();
            foreach (var p in list) Peers.Add(p);
            UpdateTray();
            StateChanged?.Invoke();
        }
        catch (CoreException ex) { AppLog.Write("peers.list failed: " + ex.Code); }
    }

    public async Task ReloadSettingsAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.SettingsGet());
            Settings = SettingsValues.FromJson(json, ShellPrefs.ResolveDownloadDir(null, Prefs.DownloadDir, KnownFolders.DefaultReceiveDir()));
            _captureOn = Settings.Capture;
            _tray.SetSyncChecked(Settings.Capture);
            ApplyPauseUi();
            StateChanged?.Invoke();
        }
        catch (CoreException ex) { AppLog.Write("settings.get failed: " + ex.Code); }
    }

    public async Task ReloadInboxAsync()
    {
        try
        {
            var limit = Math.Clamp(Settings.HistoryLimit, 10, 500);
            var json = await Task.Run(() => Api.InboxList(limit));
            var rows = InboxRow.ListFromJson(json, DateTimeOffset.Now);
            Inbox.Clear();
            foreach (var r in rows) Inbox.Add(r);
        }
        catch (CoreException ex) { AppLog.Write("inbox.list failed: " + ex.Code); }
    }

    private async Task LoadTransfersAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.TransfersList());
            Transfers.ReplaceAll(json);
        }
        catch (CoreException ex) { AppLog.Write("transfers.list failed: " + ex.Code); } // METHOD_NOT_FOUND on cores without file transfer
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
