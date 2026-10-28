using System.Collections.ObjectModel;
using System.Text.Json;
using Avalonia.Threading;
using ClipBridge.Mac.Platform;
using ClipBridge.Windows;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Update;

namespace ClipBridge.Mac;

public enum MainPage { Devices, Inbox, Transfers, Vault, Settings }

internal enum ApplyOutcome { Written, Downgraded, Failed }

/// <summary>
/// The macOS counterpart of the Windows ShellController (same rules, same shared Logic): it owns the core,
/// the clipboard watcher and the models the views show.
/// <list type="bullet">
/// <item>the core's event pump thread only parses events and posts them to the UI thread;</item>
/// <item>no core call runs on the UI thread;</item>
/// <item>every clipboard read and write runs on one serial worker, so "write + clip.mark_applied" finishes
/// before the next read.</item>
/// </list>
/// The password vault lives in MacController.Vault.cs; notes are not part of the macOS shell yet (docs/plan-macos.md §7).
/// </summary>
internal sealed partial class MacController : IDisposable
{
    public static MacController Current { get; private set; } = null!;

    private static readonly TimeSpan PollInterval = TimeSpan.FromMilliseconds(500);

    private readonly CoreHost _core = new();
    private readonly SerialQueue _clipQueue;
    private readonly DispatcherTimer _clipPoll;
    private readonly Coalescer _peersReload;
    private readonly Coalescer _transfersReload;
    private readonly Coalescer _updateProgressReload;
    private uint _lastSeenChange;
    private volatile uint _ownWriteChange;
    private volatile bool _captureOn;
    private bool _disposed;
    private string _lastUpdateNotice = "";

    public CoreApi Api { get; }
    public ShellPrefs Prefs { get; }
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

    /// <summary>Every core event, typed, on the UI thread (dialogs subscribe to this).</summary>
    public event Action<CoreEvent>? EventReceived;

    /// <summary>Peers, settings, warnings or update state changed.</summary>
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
        SettingsModel.UnixPaths = true; // download folders are "/…" here (the shared validation is textual)
        Api = new CoreApi(_core);
        Prefs = ShellPrefs.Load(MacPaths.DataDir);
        Settings = new SettingsValues(true, "auto", SettingsValues.DefaultTtl, SettingsValues.DefaultHistory, true,
            ShellPrefs.ResolveDownloadDir(null, Prefs.DownloadDir, MacPaths.DefaultReceiveDir), SettingsValues.DefaultMaxFileBytes);
        _peersReload = new Coalescer(LoadPeersAsync);
        _transfersReload = new Coalescer(LoadTransfersAsync);
        _updateProgressReload = new Coalescer(RefreshUpdateAsync);
        _clipQueue = new SerialQueue("clipbridge-clipboard", ex => MacLog.Error("clipboard worker", ex));
        _clipPoll = new DispatcherTimer { Interval = PollInterval };
        _clipPoll.Tick += (_, _) => PollClipboard();
    }

    // ---- start / stop --------------------------------------------------------------------

    /// <exception cref="KeyStoreException">The key is damaged or missing next to an existing database.</exception>
    public async Task StartAsync()
    {
        Directory.CreateDirectory(MacPaths.DataDir);
        // Held images of an earlier run have no preview window any more: they were never saved, so they go.
        try { if (Directory.Exists(ImageHoldRoot)) Directory.Delete(ImageHoldRoot, recursive: true); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { MacLog.Error("image hold cleanup", ex); }
        var key = await Task.Run(() => MacKeyStore.LoadOrCreate(MacPaths.DataDir));
        _core.EventReceived += OnPumpEvent;
        var config = new
        {
            data_dir = MacPaths.DataDir,
            db_key_b64 = key,
            device_name = Environment.MachineName,
            platform = "macos",
            listen = true,
            port = 47821,
            app_version_code = BuildInfo.VersionCode,
            app_version_name = BuildInfo.VersionName,
            update_url = BuildInfo.UpdateUrl,
        };
        await Task.Run(() => _core.Start(config));
        CoreRunning = true;
        _lastSeenChange = MacPasteboard.ChangeCount(); // what is on the clipboard at start is not "copied now"
        _clipPoll.Start();
        InitVault();
        await ReloadAllAsync();
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _clipPoll.Stop();
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
        // Recorded on arrival, before any dispatch delay, so a copy made meanwhile is noticed (shell-guide §3.5).
        if (ev is ClipReceivedEvent clip) ev = clip with { SeqAtArrival = MacPasteboard.ChangeCount() };
        var typed = ev;
        UiPost(() => Dispatch(typed));
    }

    private void Dispatch(CoreEvent ev)
    {
        MacLog.Event(ev);
        try
        {
            if (Warnings.Apply(ev)) StateChanged?.Invoke();
            switch (ev)
            {
                case CoreListeningEvent:
                    _ = RefreshStatusAsync();
                    break;
                case PeerStateEvent:
                    _ = _peersReload.RunAsync();
                    break;
                case PeerRevokedEvent r:
                    Notify(Strings.PeerRevokedTitle, Strings.PeerRevokedText(r.Name.Length > 0 ? r.Name : r.DeviceId));
                    _ = _peersReload.RunAsync();
                    break;
                case ClipReceivedEvent c:
                    HandleClipReceived(c);
                    break;
                case PairRequestEvent p:
                    Views.PairRequestWindow.Open(p);
                    Notify(Strings.PairRequestBalloonTitle, Strings.PairRequestBalloon(p.Name));
                    break;
                case PairResultEvent pr:
                    _ = _peersReload.RunAsync();
                    if (pr.State == "accepted" && pr.Role == "listener")
                        Notify(Strings.PairSuccess, Strings.PairSuccessWith(pr.Name.Length > 0 ? pr.Name : Strings.Unknown));
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
            }
        }
        catch (Exception ex) { MacLog.Error("dispatch " + ev.Type, ex); }

        try { EventReceived?.Invoke(ev); }
        catch (Exception ex) { MacLog.Error("event subscriber " + ev.Type, ex); }
    }

    // ---- clipboard: receive --------------------------------------------------------------

    private void HandleClipReceived(ClipReceivedEvent c)
    {
        if (c.EventId.Length > 0 && !Inbox.Any(r => r.EventId == c.EventId))
        {
            Inbox.Insert(0, InboxRow.FromEvent(c, DateTimeOffset.Now));
            while (Inbox.Count > Math.Max(10, Settings.HistoryLimit)) Inbox.RemoveAt(Inbox.Count - 1);
        }
        var from = c.FromName.Length > 0 ? c.FromName : c.From;
        var body = c.Sensitive ? Strings.ReceivedSensitive : ClipText.Preview(c.Text, ClipText.PreviewChars);
        switch (ClipboardDecider.Decide(c.Decision, c.Sensitive, c.SeqAtArrival, MacPasteboard.ChangeCount()))
        {
            case ClipAction.WriteNow:
                QueueApply(c.Text, c.EventId, c.Sensitive, c.SeqAtArrival, user: false, from, null);
                break;
            case ClipAction.NotifyManual:
                Notify(Strings.ReceivedTitle(from), body + "\n" + MacStrings.ReceivedManualHint);
                break;
            case ClipAction.NotifyDowngraded:
                Notify(Strings.ReceivedTitle(from), body + "\n" + Strings.ReceivedDowngraded);
                break;
            default:
                Notify(Strings.ReceivedTitle(from), body + "\n" + MacStrings.ReceivedNotifyHint);
                break;
        }
    }

    private void QueueApply(string coreText, string eventId, bool sensitive, uint seqAtArrival, bool user, string fromName,
        Action<ApplyOutcome>? done)
    {
        _clipQueue.Post(() =>
        {
            var outcome = ApplyOnWorker(coreText, eventId, sensitive, seqAtArrival, user);
            UiPost(() =>
            {
                if (outcome == ApplyOutcome.Written)
                {
                    Inbox.FirstOrDefault(r => r.EventId == eventId)?.SetState("applied");
                    if (user) ShowStatus(Strings.AppliedToast);
                    else if (!sensitive) Notify(Strings.ReceivedTitle(fromName), ClipText.Preview(coreText) + "\n" + Strings.AppliedToast);
                }
                else if (outcome == ApplyOutcome.Downgraded)
                    Notify(Strings.ReceivedTitle(fromName), Strings.ReceivedDowngraded);
                else
                    Notify(Strings.ReceivedTitle(fromName), Strings.ApplyFailed);
                done?.Invoke(outcome);
            });
        });
    }

    /// <summary>Clipboard worker: begin_apply opens the echo window before the write, mark_applied closes it.</summary>
    private ApplyOutcome ApplyOnWorker(string coreText, string eventId, bool sensitive, uint seqAtArrival, bool user)
    {
        if (_disposed) return ApplyOutcome.Failed;
        if (!user && !ClipboardDecider.StillSafeToWrite(sensitive, seqAtArrival, MacPasteboard.ChangeCount()))
            return ApplyOutcome.Downgraded; // the user copied something since the event arrived
        if (eventId.Length > 0)
        {
            try { Api.BeginApply(eventId); }
            catch (CoreException ex)
            {
                MacLog.Write("clip.begin_apply failed: " + ex.Code);
                return ApplyOutcome.Failed;
            }
        }
        if (MacPasteboard.WriteText(ClipText.ToCore(coreText), sensitive) is not { } change) return ApplyOutcome.Failed;
        _ownWriteChange = change; // the poller must not send our own write back out
        if (eventId.Length > 0)
        {
            try { Api.MarkApplied(eventId); }
            catch (CoreException ex) { MacLog.Write("clip.mark_applied failed: " + ex.Code); }
        }
        return ApplyOutcome.Written;
    }

    public Task<string?> CopyInboxItemAsync(InboxRow row)
    {
        var tcs = new TaskCompletionSource<string?>();
        QueueApply(row.Text, row.EventId, row.Sensitive, 0, user: true, row.FromName,
            outcome => tcs.TrySetResult(outcome == ApplyOutcome.Written ? null : Strings.ApplyFailed));
        return tcs.Task;
    }

    /// <summary>Copies app-generated text (a pairing link) so that it is not synced as if the user had copied it.</summary>
    public Task<bool> CopyTextAsync(string text)
    {
        var tcs = new TaskCompletionSource<bool>();
        QueueApply(text, "", false, 0, user: true, "", outcome => tcs.TrySetResult(outcome == ApplyOutcome.Written));
        return tcs.Task;
    }

    // ---- clipboard: send -----------------------------------------------------------------

    /// <summary>UI timer: compares the change count only; reading happens on the worker.</summary>
    private void PollClipboard()
    {
        if (_disposed) return;
        uint change;
        try { change = MacPasteboard.ChangeCount(); }
        catch (Exception ex) { MacLog.Error("pasteboard", ex); _clipPoll.Stop(); return; }
        if (change == _lastSeenChange) return;
        _lastSeenChange = change;
        _clipQueue.Post(() => ReadAndSendLocalClipboard(change));
    }

    private void ReadAndSendLocalClipboard(uint change)
    {
        if (_disposed || change == _ownWriteChange) return;
        var read = MacPasteboard.ReadText();
        if (read.Status != PasteReadStatus.Ok) return; // no text, or a password manager's copy
        var text = ClipText.ToCore(read.Text);
        switch (LocalCapture.Decide(text, _captureOn))
        {
            case LocalCaptureDecision.SkipSilently:
                return;
            case LocalCaptureDecision.SkipTooLarge:
                UiPost(() => Notify(Strings.NotSentTitle, Strings.TooLargeToSend));
                return;
        }
        try
        {
            var result = Api.ClipLocalChanged(text);
            if (ErrorMessages.ForSuppressed(result.Suppressed) is { } message) UiPost(() => Notify(Strings.NotSentTitle, message));
        }
        catch (CoreException ex) { MacLog.Write("clip.local_changed failed: " + ex.Code); }
    }

    /// <summary>"发送剪贴板": sends even with sync off, to all peers.</summary>
    public void SendCurrentClipboard() => _clipQueue.Post(() =>
    {
        if (_disposed) return;
        var read = MacPasteboard.ReadText();
        if (read.Status != PasteReadStatus.Ok)
        {
            UiPost(() => ShowStatus(Strings.SendNowNothing));
            return;
        }
        try
        {
            var result = Api.ClipSend(ClipText.ToCore(read.Text));
            UiPost(() => ShowStatus(result.Sent.Count > 0 ? Strings.SendNowDone : Strings.SendNowNoPeers));
        }
        catch (CoreException ex) { UiPost(() => ShowStatus(ErrorMessages.ForCode(ex.Code))); }
    });

    /// <summary>Typed text: to the named devices, or (null) to every device online now (<see cref="SendTargets.Online"/>).</summary>
    public async Task<string?> SendTextAsync(string text, IReadOnlyCollection<string>? to)
    {
        if (string.IsNullOrWhiteSpace(text)) return null;
        var targets = to ?? SendTargets.Online(Peers);
        if (targets.Count == 0) return Strings.SendNoOnline;
        var (result, error) = await CallAsync(a => a.ClipSendTo(ClipText.ToCore(text), targets));
        await _peersReload.RunAsync();
        return error ?? Strings.SentTo(result!.Sent.Count);
    }

    /// <summary>A received image onto the clipboard. Our own write: the poller must not treat it as a copy.</summary>
    public bool CopyImageFile(string path)
    {
        try
        {
            if (MacPasteboard.WriteImageFile(path) is not { } change) return false;
            _ownWriteChange = change;
            return true;
        }
        catch (Exception ex)
        {
            MacLog.Error("image copy", ex);
            return false;
        }
    }

    /// <summary>A device card's "发送剪贴板": the current clipboard to that device only.</summary>
    public void SendClipboardTo(PeerItem peer) => _clipQueue.Post(() =>
    {
        if (_disposed) return;
        var read = MacPasteboard.ReadText();
        if (read.Status != PasteReadStatus.Ok)
        {
            UiPost(() => ShowStatus(Strings.SendNowNothing));
            return;
        }
        try
        {
            Api.ClipSendTo(ClipText.ToCore(read.Text), new[] { peer.DeviceId });
            UiPost(() => ShowStatus(Strings.SentTo(1)));
        }
        catch (CoreException ex) { UiPost(() => ShowStatus(ErrorMessages.ForCode(ex.Code))); }
    });

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

    // ---- peers ---------------------------------------------------------------------------

    public async Task<string?> RenamePeerAsync(PeerItem peer, string newName)
    {
        var name = PeerItem.NormalizeName(newName);
        if (name is null) return Strings.NameInvalid;
        var error = await CallAsync(a => a.PeerRename(peer.DeviceId, name));
        await _peersReload.RunAsync();
        return error;
    }

    public async Task<string?> SetPeerModeAsync(PeerItem peer, string mode)
    {
        var error = await CallAsync(a => a.PeerSetMode(peer.DeviceId, mode));
        await _peersReload.RunAsync();
        return error;
    }

    public async Task<string?> SetPeerSendAsync(PeerItem peer, bool enabled)
    {
        var error = await CallAsync(a => a.PeerSetSend(peer.DeviceId, enabled));
        await _peersReload.RunAsync();
        return error;
    }

    public async Task<string?> RemovePeerAsync(PeerItem peer)
    {
        var error = await CallAsync(a => a.PeerRemove(peer.DeviceId, false));
        await _peersReload.RunAsync();
        return error;
    }

    public async Task<string?> ReconnectPeerAsync(PeerItem peer)
    {
        try { await Task.Run(() => Api.PeerReconnect(peer.DeviceId)); }
        catch (CoreException ex) when (ex.Code == "METHOD_NOT_FOUND")
        {
            await _peersReload.RunAsync();
            return Strings.ReconnectRefreshed;
        }
        catch (CoreException ex) { return ErrorMessages.ForCode(ex.Code); }
        await _peersReload.RunAsync();
        return null;
    }

    // ---- inbox ---------------------------------------------------------------------------

    public async Task<string?> DismissInboxItemAsync(InboxRow row)
    {
        var error = await CallAsync(a => a.Dismiss(row.EventId));
        if (error is null) row.SetState("ignored");
        return error;
    }

    public async Task<string?> ClearInboxAsync()
    {
        var error = await CallAsync(a => a.InboxClear());
        if (error is null) Inbox.Clear();
        return error;
    }

    // ---- settings ------------------------------------------------------------------------

    public async Task<string?> SetCaptureAsync(bool on)
    {
        var error = await CallAsync(a => a.SettingsSet("capture", on ? "on" : "off"));
        if (error is null)
        {
            _captureOn = on;
            Settings = Settings with { Capture = on };
        }
        StateChanged?.Invoke();
        return error;
    }

    public async Task<string?> SaveSettingsAsync(SettingsValues updated)
    {
        string? firstError = null;
        foreach (var (key, value) in SettingsModel.Diff(Settings, updated))
        {
            try { await Task.Run(() => Api.SettingsSet(key, value)); }
            catch (CoreException ex) { firstError ??= ErrorMessages.ForCode(ex.Code); }
        }
        await ReloadSettingsAsync();
        return firstError;
    }

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

    // ---- files ---------------------------------------------------------------------------

    private int _openOfferDialogs;
    private const int MaxOfferDialogs = 3;

    private void HandleFileEvent(CoreEvent ev)
    {
        if (!Transfers.Apply(ev)) _ = _transfersReload.RunAsync();
        switch (ev)
        {
            case FileOfferedEvent o:
            {
                var from = o.FromName.Length > 0 ? o.FromName : o.From;
                Notify(Strings.FileOfferBalloonTitle, Strings.FileOfferBalloon(from, FileDisplay.Shorten(o.Name, 40), Formatting.Bytes(o.Size)));
                if (_openOfferDialogs < MaxOfferDialogs)
                {
                    _openOfferDialogs++;
                    Views.FileOfferWindow.Open(o, () => _openOfferDialogs--);
                }
                break;
            }
            case FileDoneEvent d when d.Direction == "in":
            {
                // Images open a preview (copy to clipboard / show in Finder); other files, or too many previews, a banner.
                var from = Transfers.Find(d.TransferId)?.PeerName is { Length: > 0 } n ? n : Strings.Unknown;
                var held = ReceivedImages.IsHeld(d.Path, ImageHoldRoot);
                if (FileDisplay.IsPreviewableImage(d.Name, d.Size) && Views.ImagePreviewWindow.TryOpen(d.Path, d.Name, from, held)) break;
                // No preview window free: a held image must not be lost, so it goes to the download folder after all.
                if (held) MoveHeldToDownloads(d.Path);
                Notify(Strings.FileDoneTitle, Strings.FileDoneText(FileDisplay.Shorten(d.Name, 60)));
                break;
            }
            case FileFailedEvent f:
                Notify(Strings.FileFailedTitle, ErrorMessages.ForFile(f.Code));
                break;
        }
        TransfersChanged?.Invoke();
    }

    /// <summary>The transfer list changed (rows added, removed or updated).</summary>
    public event Action? TransfersChanged;

    public async Task<string?> SendFilesAsync(PeerItem peer, IEnumerable<string> paths)
    {
        var files = new List<string>();
        var skippedFolders = false;
        foreach (var p in paths)
        {
            if (File.Exists(p)) files.Add(p);
            else if (Directory.Exists(p)) skippedFolders = true;
        }
        if (files.Count == 0) return skippedFolders ? Strings.DropNoFiles : Strings.FileMissing;
        var errors = new List<string>();
        var started = 0;
        await Task.Run(() =>
        {
            foreach (var path in files)
            {
                try { Api.FileOffer(peer.DeviceId, path); started++; }
                catch (CoreException ex)
                {
                    errors.Add(Strings.FileSendFailed(FileDisplay.Shorten(FileDisplay.FileNameOf(path), 40), ErrorMessages.ForFile(ex.Code)));
                }
            }
        });
        await _transfersReload.RunAsync();
        if (started > 0) ShowStatus(files.Count == 1 ? Strings.FileSendStarted : Strings.FileSendStartedN(started));
        if (skippedFolders) errors.Add(Strings.DropNoFiles);
        return errors.Count == 0 ? null : string.Join("\n", errors);
    }

    /// <summary>Where received images wait for "保存到…" (ReceivedImages); emptied at every start.</summary>
    public static string ImageHoldRoot => Path.Combine(MacPaths.DataDir, ReceivedImages.HoldDirName);

    public async Task<string?> AcceptTransferAsync(string transferId, string? saveDir)
    {
        var dir = string.IsNullOrWhiteSpace(saveDir) ? Settings.DownloadDir : saveDir;
        // An image for the default folder is held privately: the preview decides whether and where it is saved.
        if (Transfers.Find(transferId) is { } row && ReceivedImages.ShouldHold(row.Name, row.Size, saveDir, Settings.DownloadDir))
            dir = ReceivedImages.HoldDirFor(ImageHoldRoot, transferId);
        try { Directory.CreateDirectory(dir); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or ArgumentException) { return Strings.ValidationDownloadDir; }
        var error = await CallAsync(a => a.FileAccept(transferId, dir));
        await _transfersReload.RunAsync();
        return error;
    }

    private void MoveHeldToDownloads(string path)
    {
        try
        {
            var dir = Settings.DownloadDir;
            Directory.CreateDirectory(dir);
            File.Move(path, Path.Combine(dir, ReceivedImages.UniqueName(Path.GetFileName(path), n => File.Exists(Path.Combine(dir, n)))));
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { MacLog.Error("move held image", ex); }
    }

    public async Task<string?> RejectTransferAsync(string transferId)
    {
        var error = await CallAsync(a => a.FileReject(transferId));
        await _transfersReload.RunAsync();
        return error;
    }

    public async Task<string?> CancelTransferAsync(string transferId)
    {
        var error = await CallAsync(a => a.FileCancel(transferId));
        await _transfersReload.RunAsync();
        return error;
    }

    // ---- snapshots -----------------------------------------------------------------------

    public async Task ReloadAllAsync()
    {
        await Task.WhenAll(RefreshStatusAsync(), ReloadSettingsAsync(), _peersReload.RunAsync(),
            ReloadInboxAsync(), _transfersReload.RunAsync(), RefreshUpdateAsync());
        StateChanged?.Invoke();
    }

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
        catch (CoreException ex) { MacLog.Write("status failed: " + ex.Code); }
    }

    private async Task LoadPeersAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.PeersList());
            var list = PeerItem.ListFromJson(json, DateTimeOffset.Now);
            Peers.Clear();
            foreach (var p in list) Peers.Add(p);
            StateChanged?.Invoke();
        }
        catch (CoreException ex) { MacLog.Write("peers.list failed: " + ex.Code); }
    }

    public async Task ReloadSettingsAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.SettingsGet());
            Settings = SettingsValues.FromJson(json, ShellPrefs.ResolveDownloadDir(null, Prefs.DownloadDir, MacPaths.DefaultReceiveDir));
            _captureOn = Settings.Capture;
            StateChanged?.Invoke();
        }
        catch (CoreException ex) { MacLog.Write("settings.get failed: " + ex.Code); }
    }

    private async Task ReloadInboxAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.InboxList(Math.Clamp(Settings.HistoryLimit, 10, 500)));
            var rows = InboxRow.ListFromJson(json, DateTimeOffset.Now);
            Inbox.Clear();
            foreach (var r in rows) Inbox.Add(r);
        }
        catch (CoreException ex) { MacLog.Write("inbox.list failed: " + ex.Code); }
    }

    private async Task LoadTransfersAsync()
    {
        try
        {
            var json = await Task.Run(() => Api.TransfersList());
            Transfers.ReplaceAll(json);
            TransfersChanged?.Invoke();
        }
        catch (CoreException ex) { MacLog.Write("transfers.list failed: " + ex.Code); }
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
