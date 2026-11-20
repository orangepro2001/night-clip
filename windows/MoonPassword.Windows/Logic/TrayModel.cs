namespace MoonPassword.Windows.Logic;

public enum TrayState { Normal, Warning }

public static class TrayModel
{
    /// <summary>The icon turns to its warning look when the core is not running.</summary>
    public static TrayState Compute(bool coreRunning) => coreRunning ? TrayState.Normal : TrayState.Warning;

    /// <summary>Tooltip text; NotifyIcon allows at most 63 characters.</summary>
    public static string Tooltip(TrayState state, bool vaultUnlocked)
    {
        var text = state switch
        {
            TrayState.Warning => Strings.TrayTipWarning,
            _ when vaultUnlocked => Strings.TrayTipUnlocked,
            _ => Strings.TrayTipLocked,
        };
        return text.Length <= 63 ? text : text[..63];
    }
}
