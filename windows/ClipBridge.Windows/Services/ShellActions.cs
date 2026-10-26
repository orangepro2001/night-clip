using System.Diagnostics;
using System.Windows;
using System.Windows.Threading;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Update;

namespace ClipBridge.Windows.Services;

/// <summary>Clipboard flow and the actions the pages trigger. Every method returns an error text (null = success) instead of throwing.</summary>
internal sealed partial class ShellController
{
    // =====================================================================================
    //  Clipboard: receive
    // =====================================================================================

    private void HandleClipReceived(ClipReceivedEvent c)
    {
        if (c.EventId.Length > 0 && !Inbox.Any(r => r.EventId == c.EventId))
        {
            Inbox.Insert(0, InboxRow.FromEvent(c, DateTimeOffset.Now));
            while (Inbox.Count > Math.Max(10, Settings.HistoryLimit)) Inbox.RemoveAt(Inbox.Count - 1);
        }

        var action = ClipboardDecider.Decide(c.Decision, c.Sensitive, c.SeqAtArrival, ClipboardAccess.SequenceNumber());
        var title = Strings.ReceivedTitle(c.FromName.Length > 0 ? c.FromName : c.From);
        var body = c.Sensitive ? Strings.ReceivedSensitive : ClipText.Preview(c.Text, ClipText.PreviewChars);

        switch (action)
        {
            case ClipAction.WriteNow:
                QueueApply(c, user: false);
                break;
            case ClipAction.NotifyManual:
                Balloon(title, body + "\n" + Strings.ReceivedManualHint, () => QueueApply(c, user: true));
                break;
            case ClipAction.NotifyDowngraded:
                Balloon(title, body + "\n" + Strings.ReceivedDowngraded, () => ShowMainWindow(MainPage.Inbox));
                break;
            default:
                Balloon(title, body + "\n" + Strings.ReceivedNotifyHint, () => ShowMainWindow(MainPage.Inbox));
                break;
        }
    }

    /// <summary>Writes a received item to the clipboard on the clipboard worker. <paramref name="user"/> = the user asked for it.</summary>
    private void QueueApply(ClipReceivedEvent c, bool user) =>
        QueueApply(c.Text, c.EventId, c.Sensitive, c.SeqAtArrival, user, c.FromName.Length > 0 ? c.FromName : c.From, null);

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
                    else if (!sensitive)
                        Balloon(Strings.ReceivedTitle(fromName), ClipText.Preview(coreText) + "\n" + Strings.AppliedToast);
                }
                else if (outcome == ApplyOutcome.Downgraded)
                    Balloon(Strings.ReceivedTitle(fromName), Strings.ReceivedDowngraded, () => ShowMainWindow(MainPage.Inbox));
                else
                    Balloon(Strings.ReceivedTitle(fromName), Strings.ApplyFailed);
                done?.Invoke(outcome);
            });
        });
    }

    /// <summary>
    /// Runs on the clipboard worker. The write itself happens on the UI thread (the clipboard owner window
    /// lives there) and is awaited; clip.mark_applied follows immediately on this same worker, before
    /// any later clipboard read can run, so the core's echo suppression is in place before the
    /// WM_CLIPBOARDUPDATE caused by our own write is processed.
    /// </summary>
    private ApplyOutcome ApplyOnWorker(string coreText, string eventId, bool sensitive, uint seqAtArrival, bool user)
    {
        if (_disposed || _ui.HasShutdownStarted) return ApplyOutcome.Failed;
        var text = ClipText.ToClipboard(coreText);
        var outcome = ApplyOutcome.Failed;

        void Write()
        {
            // The last look before overwriting: the user may have copied something since the event arrived.
            if (!user && !ClipboardDecider.StillSafeToWrite(sensitive, seqAtArrival, ClipboardAccess.SequenceNumber()))
            {
                outcome = ApplyOutcome.Downgraded;
                return;
            }
            // Remember what the automatic write replaces so the user can take it back. Kept in memory only,
            // and only when it was plain text (password-manager copies read as "excluded" and are never kept).
            string? previous = null;
            if (!user && eventId.Length > 0 && _clip.ReadText() is { Status: ClipReadStatus.Ok } prev) previous = prev.Text;
            if (_clip.WriteText(_monitor.Handle, text))
            {
                _ownWriteSeq = ClipboardAccess.SequenceNumber(); // lets ReadAndSendLocalClipboard recognise our own write
                outcome = ApplyOutcome.Written;
                if (previous is not null) RememberUndo(previous);
            }
        }

        try { _ui.Invoke(Write, DispatcherPriority.Send, CancellationToken.None, TimeSpan.FromSeconds(3)); }
        catch (Exception ex) when (ex is OperationCanceledException or TimeoutException) { return ApplyOutcome.Failed; }

        if (outcome == ApplyOutcome.Written && eventId.Length > 0)
        {
            try { Api.MarkApplied(eventId); }
            catch (CoreException ex) { AppLog.Write("clip.mark_applied failed: " + ex.Code); }
        }
        return outcome;
    }

    /// <summary>The inbox "复制" button.</summary>
    public Task<string?> CopyInboxItemAsync(InboxRow row)
    {
        var tcs = new TaskCompletionSource<string?>();
        QueueApply(row.Text, row.EventId, row.Sensitive, 0, user: true, row.FromName,
            outcome => tcs.TrySetResult(outcome == ApplyOutcome.Written ? null : Strings.ApplyFailed));
        return tcs.Task;
    }

    /// <summary>
    /// Copies app-generated text (diagnostics, a pairing link) through the clipboard worker, remembering the
    /// resulting sequence number so that the monitor does not mistake it for something the user copied
    /// (it would otherwise be synced to the other devices).
    /// </summary>
    public Task<bool> CopyTextAsync(string text)
    {
        var tcs = new TaskCompletionSource<bool>();
        QueueApply(text, "", false, 0, user: true, "", outcome => tcs.TrySetResult(outcome == ApplyOutcome.Written));
        return tcs.Task;
    }

    // =====================================================================================
    //  Clipboard: send
    // =====================================================================================

    /// <summary>Runs on the clipboard worker after the debounced WM_CLIPBOARDUPDATE.</summary>
    private void ReadAndSendLocalClipboard()
    {
        if (_disposed) return;
        var seq = ClipboardAccess.SequenceNumber();
        if (seq != 0 && seq == _ownWriteSeq) return; // that was our own write

        var read = _clip.ReadText();
        switch (read.Status)
        {
            case ClipReadStatus.Ok:
                break;
            case ClipReadStatus.Busy:
                AppLog.Write("clipboard busy, change skipped");
                return;
            default: // no text, excluded (password manager), empty, huge
                return;
        }

        var text = ClipText.ToCore(read.Text);
        switch (LocalCapture.Decide(text, _captureOn))
        {
            case LocalCaptureDecision.SkipSilently:
                return;
            case LocalCaptureDecision.SkipTooLarge:
                UiPost(() => Balloon(Strings.NotSentTitle, Strings.TooLargeToSend, () => ShowMainWindow()));
                return;
        }

        try
        {
            var result = Api.ClipLocalChanged(text);
            if (ErrorMessages.ForSuppressed(result.Suppressed) is { } message)
                UiPost(() => Balloon(Strings.NotSentTitle, message));
        }
        catch (CoreException ex)
        {
            AppLog.Write("clip.local_changed failed: " + ex.Code);
        }
    }

    /// <summary>Tray "立即发送当前剪贴板" (clipboard worker): sends even with sync off, to all peers.</summary>
    private void SendCurrentClipboard()
    {
        if (_disposed) return;
        var read = _clip.ReadText();
        if (read.Status != ClipReadStatus.Ok)
        {
            UiPost(() => Balloon(Strings.AppName, Strings.SendNowNothing));
            return;
        }
        try
        {
            var result = Api.ClipSend(ClipText.ToCore(read.Text));
            UiPost(() => Balloon(Strings.AppName, result.Sent.Count > 0 ? Strings.SendNowDone : Strings.SendNowNoPeers));
        }
        catch (CoreException ex)
        {
            UiPost(() => Balloon(Strings.NotSentTitle, ErrorMessages.ForCode(ex.Code)));
        }
    }

    // =====================================================================================
    //  Generic call helpers
    // =====================================================================================

    /// <summary>Runs a core call off the UI thread; returns the user-facing error text, or null on success.</summary>
    public async Task<string?> CallAsync(Action<CoreApi> call)
    {
        try
        {
            await Task.Run(() => call(Api));
            return null;
        }
        catch (CoreException ex)
        {
            AppLog.Write("core call failed: " + ex.Code);
            return ErrorMessages.ForCode(ex.Code);
        }
        catch (Exception ex) when (IsMalformedReply(ex))
        {
            AppLog.Error("core reply", ex);
            return ErrorMessages.ForCode("INTERNAL");
        }
    }

    /// <summary>CoreHost.Call parses the reply envelope itself; a broken one surfaces as one of these.</summary>
    private static bool IsMalformedReply(Exception ex) =>
        ex is System.Text.Json.JsonException or KeyNotFoundException or InvalidOperationException;

    public async Task<(T? Value, string? Error)> CallAsync<T>(Func<CoreApi, T> call)
    {
        try { return (await Task.Run(() => call(Api)), null); }
        catch (CoreException ex)
        {
            AppLog.Write("core call failed: " + ex.Code);
            return (default, ErrorMessages.ForCode(ex.Code));
        }
        catch (Exception ex) when (IsMalformedReply(ex))
        {
            AppLog.Error("core reply", ex);
            return (default, ErrorMessages.ForCode("INTERNAL"));
        }
    }

    // =====================================================================================
    //  Peers
    // =====================================================================================

    public async Task<string?> RenamePeerAsync(PeerItem peer, string newName)
    {
        var name = PeerItem.NormalizeName(newName);
        if (name is null) return Strings.NameInvalid;
        var error = await CallAsync(a => a.PeerRename(peer.DeviceId, name));
        await ReloadPeersAsync();
        return error;
    }

    public async Task<string?> SetPeerSendAsync(PeerItem peer, bool enabled)
    {
        var error = await CallAsync(a => a.PeerSetSend(peer.DeviceId, enabled));
        await ReloadPeersAsync();
        return error;
    }

    // ---- pause and undo ------------------------------------------------------------------

    private string? _undoText;
    private DispatcherTimer? _undoTimer;
    private DispatcherTimer? _pauseTimer;
    private static readonly TimeSpan UndoWindow = TimeSpan.FromMinutes(2);

    /// <summary>Runs on the UI thread (inside the write).</summary>
    private void RememberUndo(string previous)
    {
        _undoText = previous;
        _tray.SetUndoAvailable(true);
        _undoTimer?.Stop();
        _undoTimer = new DispatcherTimer(UndoWindow, DispatcherPriority.Background, (_, _) => ForgetUndo(), _ui);
    }

    private void ForgetUndo()
    {
        _undoTimer?.Stop();
        _undoTimer = null;
        _undoText = null;
        if (!_disposed) _tray.SetUndoAvailable(false);
    }

    /// <summary>Tray "撤销上次自动写入": puts back what the last automatic write replaced.</summary>
    public Task UndoLastAutoApplyAsync()
    {
        var previous = _undoText;
        if (previous is null) return Task.CompletedTask;
        ForgetUndo();
        var tcs = new TaskCompletionSource();
        _clipQueue.Post(() =>
        {
            var ok = false;
            try
            {
                _ui.Invoke(() =>
                {
                    if (_clip.WriteText(_monitor.Handle, previous))
                    {
                        _ownWriteSeq = ClipboardAccess.SequenceNumber(); // our own write: never sent on
                        ok = true;
                    }
                }, DispatcherPriority.Send, CancellationToken.None, TimeSpan.FromSeconds(3));
            }
            catch (Exception ex) when (ex is OperationCanceledException or TimeoutException) { }
            UiPost(() => Balloon(Strings.AppName, ok ? Strings.UndoDone : Strings.ApplyFailed));
            tcs.TrySetResult();
        });
        return tcs.Task;
    }

    /// <summary>Tray pause: <paramref name="minutes"/> &gt; 0 pauses sending and automatic writing for that long, 0 resumes.</summary>
    public async Task<string?> SetPausedAsync(int minutes)
    {
        var until = minutes > 0 ? DateTimeOffset.UtcNow.AddMinutes(minutes).ToUnixTimeSeconds() : 0;
        var error = await CallAsync(a => a.SettingsSet("paused_until", until.ToString(System.Globalization.CultureInfo.InvariantCulture)));
        if (error is null) Settings = Settings with { PausedUntil = until };
        else Balloon(Strings.AppName, error);
        ApplyPauseUi();
        StateChanged?.Invoke();
        return error;
    }

    /// <summary>Menu text and the timer that flips it back when the pause ends. UI thread.</summary>
    private void ApplyPauseUi()
    {
        _pauseTimer?.Stop();
        _pauseTimer = null;
        var until = DateTimeOffset.FromUnixTimeSeconds(Math.Clamp(Settings.PausedUntil, 0, 253402300799));
        var remaining = until - DateTimeOffset.UtcNow;
        if (Settings.PausedUntil <= 0 || remaining <= TimeSpan.Zero)
        {
            _tray.SetPaused(null);
            return;
        }
        _tray.SetPaused(until.LocalDateTime);
        _pauseTimer = new DispatcherTimer(remaining + TimeSpan.FromSeconds(1), DispatcherPriority.Background, (_, _) => ApplyPauseUi(), _ui);
    }

    public async Task<string?> SetPeerModeAsync(PeerItem peer, string mode)
    {
        var error = await CallAsync(a => a.PeerSetMode(peer.DeviceId, mode));
        await ReloadPeersAsync();
        return error;
    }

    public async Task<string?> RemovePeerAsync(PeerItem peer, bool forget)
    {
        var error = await CallAsync(a => a.PeerRemove(peer.DeviceId, forget));
        await ReloadPeersAsync();
        return error;
    }

    /// <summary>"重新连接": the core redials by itself; this nudges it when it supports <c>peers.reconnect</c>.</summary>
    public async Task<string?> ReconnectPeerAsync(PeerItem peer)
    {
        try
        {
            await Task.Run(() => Api.PeerReconnect(peer.DeviceId));
        }
        catch (CoreException ex) when (ex.Code == "METHOD_NOT_FOUND")
        {
            await ReloadPeersAsync();
            return Strings.ReconnectRefreshed; // informational: nothing to nudge in this core
        }
        catch (CoreException ex) { return ErrorMessages.ForCode(ex.Code); }
        await ReloadPeersAsync();
        return null;
    }

    // =====================================================================================
    //  Inbox
    // =====================================================================================

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

    // =====================================================================================
    //  Settings
    // =====================================================================================

    public async Task<string?> SetCaptureAsync(bool on)
    {
        var error = await CallAsync(a => a.SettingsSet("capture", on ? "on" : "off"));
        if (error is null)
        {
            _captureOn = on;
            Settings = Settings with { Capture = on };
            if (!Prefs.FirstRunDone) { Prefs.FirstRunDone = true; Prefs.Save(App.DataDir); }
        }
        else Balloon(Strings.AppName, error);
        _tray.SetSyncChecked(Settings.Capture); // re-assert the real state (undoes the menu's own toggle on failure)
        StateChanged?.Invoke();
        return error;
    }

    /// <summary>Saves every changed setting; returns the first error, or null. A core without <c>download_dir</c> keeps it in shell.json.</summary>
    public async Task<string?> SaveSettingsAsync(SettingsValues updated)
    {
        var changes = SettingsModel.Diff(Settings, updated);
        string? firstError = null;
        foreach (var (key, value) in changes)
        {
            try { await Task.Run(() => Api.SettingsSet(key, value)); }
            catch (CoreException ex) when (key == "download_dir" && ex.Code == "INVALID")
            {
                // The core does not know this key (yet): remember it on the shell side.
                Prefs.DownloadDir = value;
                Prefs.Save(App.DataDir);
            }
            catch (CoreException ex)
            {
                firstError ??= ErrorMessages.ForCode(ex.Code);
            }
        }
        await ReloadSettingsAsync();
        return firstError;
    }

    public async Task<string?> SetAutoStartAsync(bool on)
    {
        var ok = await Task.Run(() => AutoStartRegistry.Set(on));
        _tray.SetAutoStartChecked(AutoStartRegistry.IsEnabled());
        StateChanged?.Invoke();
        return ok ? null : Strings.ErrInternal;
    }

    public bool AutoStartEnabled => AutoStartRegistry.IsEnabled();

    // =====================================================================================
    //  Updates
    // =====================================================================================

    public async Task<string?> UpdateActionAsync(UpdateInfo.Action action)
    {
        string? error = null;
        try
        {
            switch (action)
            {
                case UpdateInfo.Action.Check:
                    ShowStatus(Strings.UpdateChecking);
                    await Task.Run(() => Api.UpdateCheck()); // up to 30 s: never on the UI thread
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

    /// <summary>Stages the verified package, starts the staged exe to swap the files and exits.</summary>
    private async Task<string?> InstallUpdateAsync()
    {
        try
        {
            var ready = Update.ReadyPath;
            var target = AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar);
            var pid = Environment.ProcessId;
            await Task.Run(() =>
            {
                var staged = UpdateApplier.Stage(ready, App.StagingDir);
                UpdateApplier.LaunchApplier(staged, target, pid);
            });
        }
        catch (Exception ex) when (ex is IOException or InvalidDataException or InvalidOperationException
                                       or UnauthorizedAccessException or System.ComponentModel.Win32Exception)
        {
            AppLog.Error("update install", ex);
            return Strings.UpdatePrepareFailed(ex.Message);
        }
        // The updater waits for this process to exit; OnExit disposes the core first.
        ExitApp();
        return null;
    }

    // =====================================================================================
    //  Files
    // =====================================================================================

    /// <summary>Offers files to one device, one <c>file.offer</c> per file. Returns a summary of failures, or null.</summary>
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
        await ReloadTransfersAsync();
        if (started > 0) ShowStatus(files.Count == 1 ? Strings.FileSendStarted : Strings.FileSendStartedN(started));
        if (skippedFolders) errors.Add(Strings.DropNoFiles);
        return errors.Count == 0 ? null : string.Join("\n", errors);
    }

    public async Task<string?> AcceptTransferAsync(string transferId, string? saveDir)
    {
        var dir = string.IsNullOrWhiteSpace(saveDir) ? Settings.DownloadDir : saveDir;
        try { Directory.CreateDirectory(dir); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or ArgumentException or NotSupportedException)
        {
            return Strings.ValidationDownloadDir;
        }
        var error = await CallAsync(a => a.FileAccept(transferId, dir));
        await ReloadTransfersAsync();
        return error;
    }

    public async Task<string?> RejectTransferAsync(string transferId)
    {
        var error = await CallAsync(a => a.FileReject(transferId));
        await ReloadTransfersAsync();
        return error;
    }

    public async Task<string?> CancelTransferAsync(string transferId)
    {
        var error = await CallAsync(a => a.FileCancel(transferId));
        await ReloadTransfersAsync();
        return error;
    }

    /// <summary>Opens a received file with its default program; executables ask for confirmation first.</summary>
    public string? OpenTransferFile(TransferRow row, Window? owner)
    {
        if (!File.Exists(row.Path)) return Strings.FileMissing;
        if (FileDisplay.IsExecutable(row.Path) || FileDisplay.IsExecutable(row.Name))
        {
            var answer = owner is null
                ? MessageBox.Show(Strings.FileDangerText(FileDisplay.Shorten(row.Name, 60)), Strings.FileDangerTitle, MessageBoxButton.YesNo, MessageBoxImage.Warning, MessageBoxResult.No)
                : MessageBox.Show(owner, Strings.FileDangerText(FileDisplay.Shorten(row.Name, 60)), Strings.FileDangerTitle, MessageBoxButton.YesNo, MessageBoxImage.Warning, MessageBoxResult.No);
            if (answer != MessageBoxResult.Yes) return null;
        }
        return Launch(new ProcessStartInfo(row.Path) { UseShellExecute = true });
    }

    public string? OpenContainingFolder(string path)
    {
        if (File.Exists(path))
            return Launch(new ProcessStartInfo("explorer.exe", "/select,\"" + path + "\"") { UseShellExecute = true });
        var dir = FileDisplay.DirectoryOf(path);
        return Directory.Exists(dir) ? OpenFolder(dir) : Strings.FileMissing;
    }

    public string? OpenFolder(string dir)
    {
        try { Directory.CreateDirectory(dir); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { return Strings.FileMissing; }
        return Launch(new ProcessStartInfo("explorer.exe", "\"" + dir + "\"") { UseShellExecute = true });
    }

    private static string? Launch(ProcessStartInfo info)
    {
        try
        {
            using var p = Process.Start(info);
            return null;
        }
        catch (Exception ex) when (ex is System.ComponentModel.Win32Exception or InvalidOperationException or FileNotFoundException)
        {
            AppLog.Error("launch", ex);
            return ex.Message;
        }
    }
}
