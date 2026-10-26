using ClipBridge.Windows.Update;

namespace ClipBridge.Windows.Logic;

/// <summary>Simplified-Chinese wording for <see cref="UpdateInfo"/> (the info record's own text is the English fallback).</summary>
public static class UpdateView
{
    public static string Text(UpdateInfo u)
    {
        if (!u.Enabled) return u.CurrentCode == 0 ? Strings.UpdateDevBuild : Strings.UpdateNotConfigured;
        return u.State switch
        {
            "checking" => Strings.UpdateChecking,
            "up_to_date" => Strings.UpdateUpToDate(u.CurrentName),
            "available" => Strings.UpdateAvailable(u.LatestName, u.Mandatory),
            "downloading" => Strings.UpdateDownloading(u.LatestName, Formatting.Percent(u.ProgressFraction)),
            "ready" => Strings.UpdateReady(u.LatestName, u.Mandatory),
            "error" => Strings.UpdateError(!string.IsNullOrEmpty(u.ErrorCode) ? ErrorMessages.ForCode(u.ErrorCode)
                                           : u.ErrorMessage),
            _ => Strings.UpdateCurrent(u.CurrentName),
        };
    }

    public static string ButtonText(UpdateInfo u) => u.ButtonAction switch
    {
        UpdateInfo.Action.Check => Strings.UpdateButtonCheck,
        UpdateInfo.Action.Download => Strings.UpdateButtonDownload,
        UpdateInfo.Action.Install => Strings.UpdateButtonInstall,
        _ => "",
    };

    /// <summary>True for states the user should be told about in a balloon.</summary>
    public static bool IsActionable(UpdateInfo u) => u.State is "available" or "ready";
}
