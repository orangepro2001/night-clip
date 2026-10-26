namespace ClipBridge.Windows.Logic;

public enum TrayState { Normal, NoConnection, Warning }

/// <summary>Tracks which <c>core.warning</c> codes are currently active.</summary>
public sealed class WarningTracker
{
    private readonly HashSet<string> _active = new(StringComparer.Ordinal);

    public IReadOnlyCollection<string> Active => _active;
    public bool Has(string code) => _active.Contains(code);
    public bool Any => _active.Count > 0;

    /// <summary>Returns true when the set of warnings changed.</summary>
    public bool Apply(CoreEvent ev)
    {
        switch (ev)
        {
            case CoreWarningEvent w when w.Code.Length > 0:
                return _active.Add(w.Code);
            case CoreListeningEvent:
                // Listening succeeded: the address and bind problems are over.
                var a = _active.Remove("NO_TAILNET");
                var b = _active.Remove("LISTEN_FAILED");
                return a || b;
            default:
                return false;
        }
    }

    /// <summary>Banner text for the most important active warning, or null.</summary>
    public string? Banner =>
        Has("NO_TAILNET") ? Strings.NoTailnetBanner
        : Has("LISTEN_FAILED") ? Strings.ListenFailedBanner
        : Has("FINGERPRINT_MISMATCH") ? Strings.FingerprintMismatchBanner
        : null;
}

public static class TrayModel
{
    /// <summary>Warning beats everything; otherwise "no connection" when peers exist but none is connected.</summary>
    public static TrayState Compute(bool coreRunning, bool anyWarning, IReadOnlyCollection<string> peerStates)
    {
        if (!coreRunning || anyWarning) return TrayState.Warning;
        var live = peerStates.Where(s => s != "revoked").ToList();
        if (live.Count > 0 && !live.Contains("connected")) return TrayState.NoConnection;
        return TrayState.Normal;
    }

    /// <summary>Tooltip text; NotifyIcon allows at most 63 characters.</summary>
    public static string Tooltip(TrayState state, IReadOnlyCollection<string> peerStates)
    {
        var live = peerStates.Where(s => s != "revoked").ToList();
        var text = state switch
        {
            TrayState.Warning => Strings.TrayTipWarning,
            _ when live.Count == 0 => Strings.TrayTipNoPeers,
            TrayState.NoConnection => Strings.TrayTipNoConnection,
            _ => Strings.TrayTipConnected(live.Count(s => s == "connected"), live.Count),
        };
        return text.Length <= 63 ? text : text[..63];
    }
}
