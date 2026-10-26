using System.Text.Json;

namespace ClipBridge.Windows.Logic;

/// <summary>One entry of <c>peers.list</c> in display form (immutable; the page replaces the whole list on refresh).</summary>
public sealed record PeerItem(
    string DeviceId, string Name, string Platform, string State, int Pending, long LastSeenAt,
    string ApplyMode, IReadOnlyList<string> Addrs, DateTimeOffset Now, bool SendEnabled = true)
{
    public const int MaxNameLength = 64;

    public static PeerItem FromJson(JsonElement p, DateTimeOffset now) => new(
        JsonRead.Str(p, "device_id"), JsonRead.Str(p, "name"), JsonRead.Str(p, "platform"), JsonRead.Str(p, "state"),
        (int)Math.Clamp(JsonRead.Long(p, "outbox_pending"), 0, int.MaxValue), JsonRead.Long(p, "last_seen_at"),
        JsonRead.Str(p, "apply_mode", "notify"), JsonRead.StrArr(p, "tailnet_addrs"), now,
        JsonRead.Bool(p, "send_enabled", true));

    public static IReadOnlyList<PeerItem> ListFromJson(JsonElement result, DateTimeOffset now) =>
        JsonRead.Arr(result, "peers").Select(p => FromJson(p, now)).Where(p => p.DeviceId.Length > 0).ToList();

    public string DisplayName => Name.Length > 0 ? Name : DeviceId;

    public string PlatformText => Platform switch
    {
        "windows" => Strings.PlatformWindows,
        "android" => Strings.PlatformAndroid,
        "" => Strings.Unknown,
        _ => Platform,
    };

    public string StateText => State switch
    {
        "connected" => Strings.StateConnected,
        "connecting" => Strings.StateConnecting,
        "offline" => Strings.OfflineWithPending(Pending),
        "unpaired" => Strings.StateUnpaired,
        "revoked" => Strings.StateRevoked,
        _ => State,
    };

    /// <summary>Queue size shown on its own for states other than "offline" (which already includes it).</summary>
    public string PendingText => State == "offline" ? "" : Strings.PendingText(Pending);

    public string LastSeenText => Strings.LastSeenLabel(
        State == "connected" ? Strings.StateConnected : Formatting.LastSeen(LastSeenAt, Now));

    public string ApplyModeText => ApplyMode switch
    {
        "manual" => Strings.ModeManual,
        "auto" => Strings.ModeAuto,
        _ => Strings.ModeNotify,
    };

    public bool IsConnected => State == "connected";
    public bool IsRevoked => State == "revoked";
    public bool IsUnpaired => State == "unpaired";
    public bool CanSendFile => State is "connected" or "connecting" or "offline";
    public bool CanChangeSettings => !IsRevoked;
    public bool CanReconnect => State is "offline" or "unpaired" or "connecting";

    /// <summary>Validated, trimmed name for <c>peers.update</c>; null when it is not acceptable.</summary>
    public static string? NormalizeName(string? input)
    {
        var s = (input ?? "").Trim();
        return s.Length is >= 1 and <= MaxNameLength ? s : null;
    }
}
