using System.Diagnostics;
using System.Windows;
using System.Windows.Threading;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Microsoft.Win32;

namespace ClipBridge.Windows.Services;

/// <summary>
/// The shell's side of the password vault: calling the core, writing secrets to the clipboard the safe way,
/// clearing them again, locking on session events, and keeping the main window out of screen captures.
/// All decisions live in Logic/ (ClipboardClearPolicy, LockPolicy, ...); this file is the Win32 glue.
/// </summary>
internal sealed partial class ShellController
{
    private VaultApi? _vault;
    private readonly ClipboardClearGuard _vaultClip = new();
    private readonly object _vaultClipGate = new();
    private DispatcherTimer? _vaultClipTimer;
    private bool _systemEventsHooked;
    private bool _vaultPageVisible;
    private string? _lockReasonOverride;

    public VaultApi Vault => _vault ??= new VaultApi(_core);

    /// <summary>The vault was locked (by the core, the user, or a session event). UI thread. The page must drop everything it holds.</summary>
    public event Action<VaultLockedEvent>? VaultLocked;

    // ---- calling the core -----------------------------------------------------------------

    /// <summary>Runs a vault call off the UI thread. Errors come back as a friendly text (and the code, for RATE_LIMITED countdowns).</summary>
    public async Task<VaultResult<T>> VaultCallAsync<T>(Func<VaultApi, T> call)
    {
        try
        {
            return VaultResult<T>.Success(await Task.Run(() => call(Vault)));
        }
        catch (CoreException ex)
        {
            AppLog.Write("vault call failed: " + ex.Code); // the code only: never parameters or values
            return VaultResult<T>.Fail(ex.Code, ex.RetryAfterSeconds);
        }
        catch (Exception ex) when (IsMalformedReply(ex))
        {
            AppLog.Error("vault reply", ex);
            return VaultResult<T>.Fail("INTERNAL", 0);
        }
    }

    public async Task<VaultResult<bool>> VaultCallAsync(Action<VaultApi> call) =>
        await VaultCallAsync(api => { call(api); return true; });

    // ---- wiring (called from the constructor / Dispose) ----------------------------------------

    private void InitVault()
    {
        _tray.LockVaultRequested += () => _ = LockVaultAsync();
        try
        {
            SystemEvents.SessionSwitch += OnSessionSwitch;
            SystemEvents.PowerModeChanged += OnPowerModeChanged;
            _systemEventsHooked = true;
        }
        catch (Exception ex) when (ex is InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            AppLog.Error("system events", ex); // the vault still auto-locks when idle
        }
    }

    private void DisposeVault()
    {
        if (_systemEventsHooked)
        {
            SystemEvents.SessionSwitch -= OnSessionSwitch;
            SystemEvents.PowerModeChanged -= OnPowerModeChanged;
            _systemEventsHooked = false;
        }
        _vaultClipTimer?.Stop();
        // The process is going away: take our secret off the clipboard now (the UI thread owns the clipboard window).
        try { ClearVaultClipboardNow(onLock: true); }
        catch (Exception ex) { AppLog.Error("clear clipboard on exit", ex); }
    }

    // ---- locking -------------------------------------------------------------------------

    /// <summary>Locks the vault (vault.lock) and removes our secret from the clipboard.</summary>
    public async Task LockVaultAsync()
    {
        await VaultCallAsync(a => a.Lock());
        ClearVaultClipboardForLock();
    }

    private void OnVaultLockedEvent(VaultLockedEvent ev)
    {
        // vault.lock always reports reason "user"; when the shell locked because of a session event, say so.
        var reason = ev.Reason;
        if (_lockReasonOverride is { } o)
        {
            if (reason is "user" or "") reason = o;
            _lockReasonOverride = null;
        }
        ClearVaultClipboardForLock();
        try { VaultLocked?.Invoke(ev with { Reason = reason }); }
        catch (Exception ex) { AppLog.Error("vault locked subscriber", ex); }
    }

    private void OnSessionSwitch(object? sender, SessionSwitchEventArgs e)
    {
        var trigger = e.Reason switch
        {
            SessionSwitchReason.SessionLock => LockTrigger.SessionLock,
            SessionSwitchReason.SessionLogoff => LockTrigger.SessionLogoff,
            SessionSwitchReason.ConsoleDisconnect => LockTrigger.UserSwitch,
            SessionSwitchReason.RemoteDisconnect => LockTrigger.RemoteDisconnect,
            SessionSwitchReason.SessionUnlock => LockTrigger.SessionUnlock,
            _ => LockTrigger.Other,
        };
        LockFromSystemEvent(trigger);
    }

    private void OnPowerModeChanged(object? sender, PowerModeChangedEventArgs e)
    {
        var trigger = e.Mode switch
        {
            PowerModes.Suspend => LockTrigger.Suspend,
            PowerModes.Resume => LockTrigger.Resume,
            _ => LockTrigger.Other,
        };
        LockFromSystemEvent(trigger);
    }

    /// <summary>Runs on the SystemEvents thread. The core call is made right here: a suspend must not outrun it.</summary>
    private void LockFromSystemEvent(LockTrigger trigger)
    {
        if (_disposed || !LockPolicy.ShouldLock(trigger)) return;
        _lockReasonOverride = "session";
        try { Vault.Lock(); }
        catch (CoreException ex) { AppLog.Write("lock on session event failed: " + ex.Code); }
        UiPost(ClearVaultClipboardForLock);
    }

    // ---- secrets on the clipboard -------------------------------------------------------------

    /// <summary>
    /// vault.copy, then write the text with the three "do not monitor / do not keep / do not upload" flags,
    /// remember the sequence number and clear after clear_after_s if nothing else was copied meanwhile.
    /// Returns an error text, or null.
    /// </summary>
    public async Task<string?> CopyVaultSecretAsync(string entryId, string field, string label)
    {
        var r = await VaultCallAsync(a => a.Copy(entryId, field));
        if (!r.Ok) return r.Error;
        var copy = r.Value!;
        var tcs = new TaskCompletionSource<bool>();
        _clipQueue.Post(() => tcs.TrySetResult(WriteVaultSecretOnWorker(copy.Value, copy.ClearAfterSeconds, field == "notes")));
        if (!await tcs.Task) return Strings.ClipboardCopyFailed;
        ShowStatus(Strings.Copied(label, copy.ClearAfterSeconds));
        return null;
    }

    /// <summary>Clipboard worker thread. The write runs on the UI thread (it owns the clipboard window), like every other write.</summary>
    private bool WriteVaultSecretOnWorker(string value, int clearAfterSeconds, bool convertNewlines)
    {
        if (_disposed || _ui.HasShutdownStarted) return false;
        var text = convertNewlines ? ClipText.ToClipboard(value) : value;
        var written = false;

        void Write()
        {
            if (!_clip.WriteText(_monitor.Handle, text, sensitive: true)) return;
            var seq = ClipboardAccess.SequenceNumber();
            _ownWriteSeq = seq; // the monitor must not mistake this for something the user copied
            lock (_vaultClipGate) _vaultClip.Arm(seq, clearAfterSeconds);
            written = true;
        }

        try { _ui.Invoke(Write, DispatcherPriority.Send, CancellationToken.None, TimeSpan.FromSeconds(3)); }
        catch (Exception ex) when (ex is OperationCanceledException or TimeoutException) { return false; }
        if (written) UiPost(() => ScheduleVaultClipClear(clearAfterSeconds));
        return written;
    }

    private void ScheduleVaultClipClear(int seconds)
    {
        _vaultClipTimer?.Stop();
        _vaultClipTimer = null;
        if (seconds <= 0 || _disposed) return;
        _vaultClipTimer = new DispatcherTimer(TimeSpan.FromSeconds(seconds), DispatcherPriority.Background, (_, _) =>
        {
            _vaultClipTimer?.Stop();
            _vaultClipTimer = null;
            _clipQueue.Post(() => ClearVaultClipboardOnWorker(onLock: false));
        }, _ui);
    }

    private void ClearVaultClipboardForLock()
    {
        UiPostOrRun(() =>
        {
            _vaultClipTimer?.Stop();
            _vaultClipTimer = null;
        });
        if (!_disposed) _clipQueue.Post(() => ClearVaultClipboardOnWorker(onLock: true));
    }

    private void UiPostOrRun(Action action)
    {
        if (_ui.CheckAccess()) action();
        else UiPost(action);
    }

    private void ClearVaultClipboardOnWorker(bool onLock)
    {
        if (_disposed || _ui.HasShutdownStarted) return;
        try { _ui.Invoke(() => ClearVaultClipboardNow(onLock), DispatcherPriority.Send, CancellationToken.None, TimeSpan.FromSeconds(3)); }
        catch (Exception ex) when (ex is OperationCanceledException or TimeoutException) { }
    }

    /// <summary>UI thread. Clears the clipboard only when its sequence number is still the one we got after writing the secret.</summary>
    private void ClearVaultClipboardNow(bool onLock)
    {
        ClearDecision decision;
        lock (_vaultClipGate)
        {
            var now = ClipboardAccess.SequenceNumber();
            decision = onLock ? _vaultClip.OnLock(now) : _vaultClip.OnTimer(now);
            _vaultClip.Disarm(); // spent either way
        }
        if (decision != ClearDecision.Clear) return;

        var cleared = false;
        for (var attempt = 0; attempt < 5 && !cleared; attempt++)
        {
            cleared = _clip.Clear(_monitor.Handle);
            if (!cleared) Thread.Sleep(100);
        }
        if (cleared)
        {
            _ownWriteSeq = ClipboardAccess.SequenceNumber();
            if (!onLock) ShowStatus(Strings.ClipboardClearedByTimer);
        }
        else AppLog.Write("vault clipboard clear failed");
    }

    // ---- opening links --------------------------------------------------------------------

    /// <summary>Opens an http / https address in the default browser; anything else is refused. Returns an error text or null.</summary>
    public string? OpenWebUrl(string? url)
    {
        var uri = UrlSafety.TryGetWebUri(url);
        if (uri is null) return Strings.UrlNotOpened;
        return Launch(new ProcessStartInfo(uri.AbsoluteUri) { UseShellExecute = true });
    }

    // ---- window capture exclusion -----------------------------------------------------------

    public bool PreventCapture => Prefs.PreventCapture;

    public void SetPreventCapture(bool on)
    {
        Prefs.PreventCapture = on;
        Prefs.Save(App.DataDir);
        ApplyCaptureExclusion();
    }

    /// <summary>The main window is excluded from captures exactly while the vault page is showing.</summary>
    public void SetVaultPageVisible(bool visible)
    {
        if (_vaultPageVisible == visible) return; // do not create the window handle needlessly (a --minimized start)
        _vaultPageVisible = visible;
        ApplyCaptureExclusion();
    }

    private void ApplyCaptureExclusion()
    {
        if (Main is { } w) DisplayAffinity.Set(w, _vaultPageVisible && Prefs.PreventCapture);
    }

    /// <summary>Vault dialogs show secrets too: they get the same exclusion as soon as their window exists.</summary>
    public void ProtectDialog(Window dialog)
    {
        dialog.SourceInitialized += (_, _) => DisplayAffinity.Set(dialog, Prefs.PreventCapture);
    }
}
