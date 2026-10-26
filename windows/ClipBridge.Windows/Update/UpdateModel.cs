using System.Text.Json;

namespace ClipBridge.Windows.Update;

/// <summary>The core's <c>update.status</c> result in a UI-friendly shape.</summary>
public sealed record UpdateInfo(
    bool Enabled, bool Auto, string State, long CurrentCode, string CurrentName,
    long LatestCode, string LatestName, string Notes, bool Mandatory,
    long Received, long Total, string ReadyPath, string ErrorCode, string ErrorMessage)
{
    public static UpdateInfo FromJson(JsonElement s)
    {
        string Str(JsonElement e, string name) =>
            e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.String
                ? v.GetString() ?? "" : "";
        long Num(JsonElement e, string name) =>
            e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.Number
                ? v.GetInt64() : 0;
        bool Bool(JsonElement e, string name) =>
            e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.True;

        var latest = s.ValueKind == JsonValueKind.Object && s.TryGetProperty("latest", out var l) ? l : default;
        var error = s.ValueKind == JsonValueKind.Object && s.TryGetProperty("error", out var er) ? er : default;
        return new UpdateInfo(
            Bool(s, "enabled"), Bool(s, "auto"), Str(s, "state"), Num(s, "current_code"), Str(s, "current_name"),
            Num(latest, "version_code"), Str(latest, "version_name"), Str(latest, "notes"), Bool(s, "mandatory"),
            Num(s, "received"), Num(s, "total"), Str(s, "ready_path"), Str(error, "code"), Str(error, "message"));
    }

    /// <summary>What the single action button does in this state.</summary>
    public enum Action { None, Check, Download, Install }

    public Action ButtonAction => !Enabled ? Action.None : State switch
    {
        "available" => Action.Download,
        "ready" => Action.Install,
        "checking" or "downloading" => Action.None,
        _ => Action.Check,
    };

    public string ButtonText => ButtonAction switch
    {
        Action.Check => "Check for updates",
        Action.Download => "Download",
        Action.Install => "Restart and update",
        _ => "",
    };

    public double ProgressFraction => Total > 0 ? Math.Clamp((double)Received / Total, 0, 1) : 0;

    public string Text
    {
        get
        {
            if (!Enabled) return CurrentCode == 0 ? "Development build: updates are off" : "Updates are not configured";
            var must = Mandatory ? " (required)" : "";
            return State switch
            {
                "checking" => "Checking for updates…",
                "up_to_date" => $"Up to date ({CurrentName})",
                "available" => $"Version {LatestName} is available{must}",
                "downloading" => $"Downloading {LatestName}… {ProgressFraction:P0}",
                "ready" => $"Version {LatestName} is ready to install{must}",
                "error" => $"Update failed: {(string.IsNullOrEmpty(ErrorMessage) ? ErrorCode : ErrorMessage)}",
                _ => $"Version {CurrentName}",
            };
        }
    }
}
