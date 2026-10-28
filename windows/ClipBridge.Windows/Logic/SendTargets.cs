namespace ClipBridge.Windows.Logic;

/// <summary>Who typed text goes to (same rule on all three shells; Android: logic/SendTargets.kt).</summary>
public static class SendTargets
{
    /// <summary>
    /// The typed-text broadcast: every connected device whose "自动发送" switch is on (like the core's
    /// "everyone"). Offline devices are left out rather than queued; a single device is sent to from its card.
    /// </summary>
    public static IReadOnlyList<string> Online(IEnumerable<PeerItem> peers) =>
        peers.Where(p => p.IsConnected && p.SendEnabled).Select(p => p.DeviceId).ToList();
}
