using System.Text.Json;
using Avalonia.Controls;
using Avalonia.Threading;
using ClipBridge.Mac.Platform;
using ClipBridge.Windows;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac;

/// <summary>One paired device in <c>vault.sync_status</c>.</summary>
internal sealed record VaultSyncPeer(
    string DeviceId, string Name, bool Enabled, bool Connected, string State, string Detail, long LastOkAt, string IncomingSha256)
{
    public static IReadOnlyList<VaultSyncPeer> ListFromJson(JsonElement r) =>
        JsonRead.Arr(r, "peers").Select(p => new VaultSyncPeer(
            JsonRead.Str(p, "device_id"), JsonRead.Str(p, "name"), JsonRead.Bool(p, "enabled"), JsonRead.Bool(p, "connected"),
            JsonRead.Str(p, "state", "idle"), JsonRead.Str(p, "detail"), JsonRead.Long(p, "last_ok_at"), JsonRead.Str(p, "incoming_sha256"))).ToList();
}

/// <summary>
/// The macOS side of the password vault (port of the Windows ShellController.Vault): calling the core, writing secrets
/// to the pasteboard the safe way and clearing them again, locking on session events (<see cref="MacSessionWatcher"/>),
/// and keeping vault windows out of screen captures (<see cref="MacWindowPrivacy"/>). Decisions live in the shared Logic/.
/// </summary>
internal sealed partial class MacController
{
    private VaultApi? _vault;
    private readonly ClipboardClearGuard _vaultClip = new();
    private readonly object _vaultClipGate = new();
    private DispatcherTimer? _vaultClipTimer;
    private DispatcherTimer? _keepAliveTimer;
    private MacSessionWatcher? _sessionWatcher;
    private bool _vaultPageVisible;
    private volatile string? _lockReasonOverride;
    private DateTimeOffset _lastVaultActivity = DateTimeOffset.MinValue;
    private DateTimeOffset _lastKeepAlive = DateTimeOffset.MinValue;

    /// <summary>The core's auto-lock time in seconds (read from vault.status by the vault page).</summary>
    public int VaultAutolockSeconds { get; set; } = 300;

    public VaultApi Vault => _vault ??= new VaultApi(_core);

    /// <summary>The main window (set by it), for the screen-capture exclusion of the vault page.</summary>
    public Window? Main { get; set; }

    /// <summary>The vault was locked (by the core, the user, or a session event). UI thread. The page must drop everything it holds.</summary>
    public event Action<VaultLockedEvent>? VaultLocked;

    /// <summary>The unlocked vault was changed by something other than this UI (a sync merge): reload the list. UI thread.</summary>
    public event Action? VaultChanged;

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
            MacLog.Write("vault call failed: " + ex.Code); // the code only: never parameters or values
            return VaultResult<T>.Fail(ex.Code, ex.RetryAfterSeconds);
        }
        catch (Exception ex) when (ex is JsonException or KeyNotFoundException or InvalidOperationException)
        {
            MacLog.Error("vault reply", ex);
            return VaultResult<T>.Fail("INTERNAL", 0);
        }
    }

    public async Task<VaultResult<bool>> VaultCallAsync(Action<VaultApi> call) =>
        await VaultCallAsync(api => { call(api); return true; });

    // ---- wiring (StartAsync / Dispose) ---------------------------------------------------------

    private void InitVault()
    {
        PurgeOpenedAttachments(); // left over from a run that did not end cleanly
        _keepAliveTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(5) };
        _keepAliveTimer.Tick += (_, _) => KeepAliveTick();
        _keepAliveTimer.Start();
        _sessionWatcher = new MacSessionWatcher();
        _sessionWatcher.Triggered += LockFromSystemEvent;
        _sessionWatcher.Start();
    }

    /// <summary>The user touched a vault window (key, click, mouse move). UI thread.</summary>
    public void NoteVaultActivity() => _lastVaultActivity = DateTimeOffset.UtcNow;

    private void KeepAliveTick()
    {
        var now = DateTimeOffset.UtcNow;
        if (_disposed || !IdleKeepAlive.ShouldPing(_lastVaultActivity, _lastKeepAlive, now, VaultAutolockSeconds)) return;
        _lastKeepAlive = now;
        _ = Task.Run(() =>
        {
            try { Vault.Groups(); }   // any vault.* call except status restarts the core's idle timer
            catch (CoreException) { /* locked meanwhile: the vault.locked event handles the UI */ }
            catch (Exception ex) when (ex is InvalidOperationException or ObjectDisposedException) { }
        });
    }

    /// <summary>Called from Dispose (after <c>_disposed</c> is set, before the clipboard worker and the core stop).</summary>
    private void DisposeVault()
    {
        _keepAliveTimer?.Stop();
        _sessionWatcher?.Dispose();
        _vaultClipTimer?.Stop();
        _vaultClipTimer = null;
        // The process is going away: take our secret off the pasteboard now.
        try { ClearVaultClipboardNow(onLock: true); }
        catch (Exception ex) { MacLog.Error("clear clipboard on exit", ex); }
        PurgeOpenedAttachments();
    }

    // ---- locking -------------------------------------------------------------------------

    /// <summary>Locks the vault (vault.lock) and removes our secret from the clipboard.</summary>
    public async Task LockVaultAsync()
    {
        await VaultCallAsync(a => a.Lock());
        ClearVaultClipboardForLock();
        PurgeOpenedAttachments();
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
        PurgeOpenedAttachments();
        try { VaultLocked?.Invoke(ev with { Reason = reason }); }
        catch (Exception ex) { MacLog.Error("vault locked subscriber", ex); }
    }

    /// <summary>UI thread (the session watcher's timer). The core call runs on the thread pool.</summary>
    private void LockFromSystemEvent(LockTrigger trigger)
    {
        if (_disposed || !CoreRunning || !LockPolicy.ShouldLock(trigger)) return;
        _lockReasonOverride = "session"; // set before the call: the vault.locked event can reach the UI thread first
        _ = Task.Run(() =>
        {
            try
            {
                if (!Vault.Lock()) _lockReasonOverride = null; // it was not unlocked: no event will come
            }
            catch (CoreException ex)
            {
                _lockReasonOverride = null;
                MacLog.Write("lock on session event failed: " + ex.Code);
            }
            catch (Exception ex) when (ex is InvalidOperationException or ObjectDisposedException)
            {
                _lockReasonOverride = null;
            }
            UiPost(ClearVaultClipboardForLock);
        });
    }

    // ---- secrets on the clipboard -------------------------------------------------------------

    /// <summary>
    /// vault.copy, then write the text flagged concealed + transient (password managers' convention, also kept off
    /// Universal Clipboard), remember the change count and clear after clear_after_s if nothing else was copied meanwhile.
    /// Returns an error text, or null.
    /// </summary>
    public async Task<string?> CopyVaultSecretAsync(string entryId, string field, string label)
    {
        var r = await VaultCallAsync(a => a.Copy(entryId, field));
        if (!r.Ok) return r.Error;
        var copy = r.Value!;
        var tcs = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        if (_disposed) return Strings.ClipboardCopyFailed;
        _clipQueue.Post(() => tcs.TrySetResult(WriteVaultSecretOnWorker(copy.Value, copy.ClearAfterSeconds, field == "notes")));
        if (!await tcs.Task) return Strings.ClipboardCopyFailed;
        ShowStatus(Strings.Copied(label, copy.ClearAfterSeconds));
        return null;
    }

    /// <summary>Clipboard worker thread.</summary>
    private bool WriteVaultSecretOnWorker(string value, int clearAfterSeconds, bool normalizeNewlines)
    {
        if (_disposed) return false;
        var text = normalizeNewlines ? ClipText.ToCore(value) : value;
        uint? change;
        try { change = MacPasteboard.WriteText(text, sensitive: true); }
        catch (Exception ex)
        {
            MacLog.Error("vault clipboard write", ex);
            return false;
        }
        if (change is not { } c) return false;
        _ownWriteChange = c; // the poller must not mistake this for something the user copied
        lock (_vaultClipGate) _vaultClip.Arm(c, clearAfterSeconds);
        UiPost(() => ScheduleVaultClipClear(clearAfterSeconds));
        return true;
    }

    private void ScheduleVaultClipClear(int seconds)
    {
        _vaultClipTimer?.Stop();
        _vaultClipTimer = null;
        if (seconds <= 0 || _disposed) return;
        var timer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(seconds) };
        timer.Tick += (_, _) =>
        {
            timer.Stop();
            if (_vaultClipTimer == timer) _vaultClipTimer = null;
            if (!_disposed) _clipQueue.Post(() => ClearVaultClipboardNow(onLock: false));
        };
        _vaultClipTimer = timer;
        timer.Start();
    }

    private void ClearVaultClipboardForLock()
    {
        if (Dispatcher.UIThread.CheckAccess()) StopClipTimer();
        else UiPost(StopClipTimer);
        if (!_disposed) _clipQueue.Post(() => ClearVaultClipboardNow(onLock: true));

        void StopClipTimer()
        {
            _vaultClipTimer?.Stop();
            _vaultClipTimer = null;
        }
    }

    /// <summary>Any thread (normally the clipboard worker). Clears only when the change count is still the one after our write.</summary>
    private void ClearVaultClipboardNow(bool onLock)
    {
        ClearDecision decision;
        lock (_vaultClipGate)
        {
            if (!_vaultClip.Armed) return;
            var now = MacPasteboard.ChangeCount();
            decision = onLock ? _vaultClip.OnLock(now) : _vaultClip.OnTimer(now);
            _vaultClip.Disarm(); // spent either way
        }
        if (decision != ClearDecision.Clear) return;
        try
        {
            _ownWriteChange = MacPasteboard.Clear();
            if (!onLock) UiPost(() => ShowStatus(Strings.ClipboardClearedByTimer));
        }
        catch (Exception ex) { MacLog.Error("vault clipboard clear", ex); }
    }

    // ---- links and attachments ------------------------------------------------------------------

    /// <summary>Opens an http / https address in the default browser; anything else is refused. Returns an error text or null.</summary>
    public string? OpenWebUrl(string? url)
    {
        var uri = UrlSafety.TryGetWebUri(url);
        return uri is null ? Strings.UrlNotOpened : MacOpen.File(uri.AbsoluteUri);
    }

    /// <summary>Where attachments opened for viewing are written (per-user temp folder, owner-only, emptied on lock and exit).</summary>
    private static string OpenedAttachmentsRoot => Path.Combine(Path.GetTempPath(), "ClipBridge-vault-open");

    private const UnixFileMode OwnerOnly = UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute;

    /// <summary>Writes the attachment to a private temporary file and opens it with its default application.</summary>
    public async Task<string?> OpenVaultAttachmentAsync(string entryId, string name)
    {
        if (OperatingSystem.IsWindows()) return Strings.ErrVaultUnexpected; // never: this shell runs on macOS only
        string path;
        try
        {
            var root = OpenedAttachmentsRoot;
            Directory.CreateDirectory(root, OwnerOnly);
            var dir = Path.Combine(root, Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(dir, OwnerOnly);
            var safe = new string(name.Select(c => c is '/' or ':' or '\0' ? '_' : c).ToArray()).Trim();
            if (safe.Length == 0 || safe is "." or "..") safe = "attachment";
            path = Path.Combine(dir, safe);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or ArgumentException or PlatformNotSupportedException)
        {
            MacLog.Error("attachment temp dir", ex);
            return ex.Message;
        }
        var r = await VaultCallAsync(a => a.AttachSave(entryId, name, path));
        if (!r.Ok) return r.Error;
        return MacOpen.File(path);
    }

    private static void PurgeOpenedAttachments()
    {
        try
        {
            if (Directory.Exists(OpenedAttachmentsRoot)) Directory.Delete(OpenedAttachmentsRoot, recursive: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { MacLog.Error("attachment temp cleanup", ex); }
    }

    // ---- device sync (vault.sync_*) ----------------------------------------------------------------

    public Task<VaultResult<IReadOnlyList<VaultSyncPeer>>> VaultSyncStatusAsync() =>
        VaultCallAsync(_ => VaultSyncPeer.ListFromJson(_core.Call("vault.sync_status")));

    public Task<VaultResult<bool>> VaultSyncEnableAsync(string deviceId, bool enabled) =>
        VaultCallAsync(_ => { _core.Call("vault.sync_enable", new { device_id = deviceId, enabled }); });

    public Task<VaultResult<bool>> VaultSyncNowAsync() => VaultCallAsync(_ => { _core.Call("vault.sync_now"); });

    /// <summary><c>needs_password</c>: the other vault's current master password.</summary>
    public Task<VaultResult<bool>> VaultSyncResolvePasswordAsync(string deviceId, VaultCreds creds) =>
        VaultCallAsync(_ =>
        {
            var p = new Dictionary<string, object?> { ["device_id"] = deviceId };
            creds.AddTo(p);
            _core.Call("vault.sync_resolve", p);
        });

    /// <summary><c>review</c>: approve exactly the file the user saw (its sha256 from vault.sync_status).</summary>
    public Task<VaultResult<bool>> VaultSyncApproveAsync(string deviceId, string sha256) =>
        VaultCallAsync(_ => { _core.Call("vault.sync_resolve", new { device_id = deviceId, approve = true, sha256 }); });

    // ---- window capture exclusion -----------------------------------------------------------

    public bool PreventCapture => Prefs.PreventCapture;

    public void SetPreventCapture(bool on)
    {
        Prefs.PreventCapture = on;
        Prefs.Save(MacPaths.DataDir);
        ApplyCaptureExclusion();
    }

    /// <summary>The main window is excluded from captures exactly while the vault page is showing.</summary>
    public void SetVaultPageVisible(bool visible)
    {
        if (_vaultPageVisible == visible) return;
        _vaultPageVisible = visible;
        ApplyCaptureExclusion();
    }

    private void ApplyCaptureExclusion()
    {
        if (Main is { } w) MacWindowPrivacy.Apply(w, _vaultPageVisible && Prefs.PreventCapture);
    }

    /// <summary>Vault dialogs show secrets too: they get the same exclusion as soon as they open.</summary>
    public void ProtectDialog(Window dialog)
    {
        dialog.Opened += (_, _) => MacWindowPrivacy.Apply(dialog, Prefs.PreventCapture);
    }
}
