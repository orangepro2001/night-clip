using System.Text.Json;
using ClipBridge.Windows.Interop;

namespace ClipBridge.Windows.Logic;

public sealed record SentTo(string DeviceId, string Name, string EventId);

public sealed record SendResult(IReadOnlyList<SentTo> Sent, string Suppressed);

public sealed record PairBeginResult(
    string SessionId, string Link, string Host, int Port, string ExpiresAt, int QrSize, IReadOnlyList<string> QrRows)
{
    public DateTimeOffset? Expires => Countdown.ParseRfc3339(ExpiresAt);
}

/// <summary>
/// Typed wrapper over the core facade (docs/api-spec.md §7.3, docs/shell-guide.md §2). Every method
/// is a blocking call: run it off the UI thread. Methods throw <see cref="CoreException"/> with the core's code.
/// </summary>
public sealed class CoreApi
{
    private readonly ICoreCaller _core;
    public CoreApi(ICoreCaller core) => _core = core;

    // ---- status / identity ------------------------------------------------------------
    public JsonElement Status() => _core.Call("status");
    public JsonElement Identity() => _core.Call("identity.get");
    public JsonElement Diagnostics() => _core.Call("diagnostics.get");

    // ---- settings ---------------------------------------------------------------------
    public JsonElement SettingsGet() => _core.Call("settings.get");
    public void SettingsSet(string key, string value) => _core.Call("settings.set", new { key, value });

    // ---- pairing ----------------------------------------------------------------------
    public PairBeginResult PairBegin()
    {
        var r = _core.Call("pair.begin");
        var qr = JsonRead.Obj(r, "qr");
        return new PairBeginResult(
            JsonRead.Str(r, "session_id"), JsonRead.Str(r, "link"), JsonRead.Str(r, "host"), (int)JsonRead.Long(r, "port"),
            JsonRead.Str(r, "expires_at"), (int)JsonRead.Long(qr, "size"), JsonRead.StrArr(qr, "rows"));
    }

    public void PairCancel(string sessionId) => _core.Call("pair.cancel", new { session_id = sessionId });

    public JsonElement PairPending() => _core.Call("pair.pending");

    public void PairConfirm(string pairId, bool accept) => _core.Call("pair.confirm", new { pair_id = pairId, accept });

    /// <summary>Returns the join id.</summary>
    public string PairJoin(PairJoinRequest request) =>
        JsonRead.Str(_core.Call("pair.join", request.ToParams()), "join_id");

    // ---- peers ------------------------------------------------------------------------
    public JsonElement PeersList() => _core.Call("peers.list");
    public void PeerRename(string deviceId, string name) => _core.Call("peers.update", new { device_id = deviceId, name });
    public void PeerSetSend(string deviceId, bool enabled) => _core.Call("peers.update", new { device_id = deviceId, send_enabled = enabled });
    public void PeerSetMode(string deviceId, string applyMode) => _core.Call("peers.update", new { device_id = deviceId, apply_mode = applyMode });
    public void PeerRemove(string deviceId, bool forget = false) => _core.Call("peers.remove", new { device_id = deviceId, forget });

    /// <summary>Asks the core to redial now. The method is not in api-spec §7.3; callers must tolerate METHOD_NOT_FOUND.</summary>
    public void PeerReconnect(string deviceId) => _core.Call("peers.reconnect", new { device_id = deviceId });

    // ---- clipboard / inbox ------------------------------------------------------------
    public SendResult ClipLocalChanged(string coreText) => ParseSend(_core.Call("clip.local_changed", new { text = coreText }));

    public SendResult ClipSend(string coreText) => ParseSend(_core.Call("clip.send", new { text = coreText }));

    /// <summary>Open the echo window before the shell writes the clipboard; MarkApplied follows after a successful write.</summary>
    public void BeginApply(string eventId) => _core.Call("clip.begin_apply", new { event_id = eventId });

    public void MarkApplied(string eventId) => _core.Call("clip.mark_applied", new { event_id = eventId });
    public void Dismiss(string eventId) => _core.Call("clip.dismiss", new { event_id = eventId });
    public JsonElement InboxList(int limit = 200) => _core.Call("inbox.list", new { limit });
    public void InboxClear() => _core.Call("inbox.clear");

    private static SendResult ParseSend(JsonElement r) => new(
        JsonRead.Arr(r, "sent").Select(s => new SentTo(JsonRead.Str(s, "device_id"), JsonRead.Str(s, "name"), JsonRead.Str(s, "event_id"))).ToList(),
        JsonRead.Str(r, "suppressed"));

    // ---- files ------------------------------------------------------------------------
    /// <summary>Returns the transfer id. The core hashes the file first, which can take a while for big files.</summary>
    public string FileOffer(string toDeviceId, string path) =>
        JsonRead.Str(_core.Call("file.offer", new { to = toDeviceId, path }), "transfer_id");

    public void FileAccept(string transferId, string? saveDir = null)
    {
        if (string.IsNullOrEmpty(saveDir)) _core.Call("file.accept", new { transfer_id = transferId });
        else _core.Call("file.accept", new { transfer_id = transferId, save_dir = saveDir });
    }

    public void FileReject(string transferId) => _core.Call("file.reject", new { transfer_id = transferId });
    public void FileCancel(string transferId) => _core.Call("file.cancel", new { transfer_id = transferId });
    public JsonElement TransfersList(int limit = 100) => _core.Call("transfers.list", new { limit });

    // ---- update -----------------------------------------------------------------------
    public JsonElement UpdateStatus() => _core.Call("update.status");
    public void UpdateCheck() => _core.Call("update.check"); // up to 30 s
    public void UpdateDownload() => _core.Call("update.download");
    public void UpdateConfigAuto(bool auto) => _core.Call("update.config", new { auto });
}
