using System.Globalization;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace ClipBridge.Windows.Logic;

/// <summary>The values behind the settings page (api-spec §7.3 keys plus the file keys of shell-guide §2).</summary>
public sealed record SettingsValues(
    bool Capture, string DefaultApplyMode, int ClipTtlSeconds, int HistoryLimit, bool SendSensitive,
    string DownloadDir, long MaxFileBytes)
{
    public const int DefaultTtl = 900;
    public const int DefaultHistory = 200;
    public const long DefaultMaxFileBytes = 4294967296;

    /// <summary>From a <c>settings.get</c> result; missing keys take their defaults.</summary>
    public static SettingsValues FromJson(JsonElement result, string defaultDownloadDir)
    {
        var s = JsonRead.Obj(result, "settings");
        string Get(string key) => JsonRead.Str(s, key);
        return new SettingsValues(
            Capture: Get("capture") == "on",
            DefaultApplyMode: SettingsModel.Modes.Contains(Get("default_apply_mode")) ? Get("default_apply_mode") : "notify",
            ClipTtlSeconds: int.TryParse(Get("clip_ttl_s"), NumberStyles.None, CultureInfo.InvariantCulture, out var ttl) ? ttl : DefaultTtl,
            HistoryLimit: int.TryParse(Get("history_limit"), NumberStyles.None, CultureInfo.InvariantCulture, out var h) ? h : DefaultHistory,
            SendSensitive: Get("send_sensitive") != "off",
            DownloadDir: Get("download_dir") is { Length: > 0 } d && SettingsModel.IsAbsoluteWindowsPath(d) ? d : defaultDownloadDir,
            MaxFileBytes: long.TryParse(Get("max_file_bytes"), NumberStyles.None, CultureInfo.InvariantCulture, out var m) && m > 0 ? m : DefaultMaxFileBytes);
    }
}

/// <summary>What the user typed on the settings page (text boxes stay strings until validated).</summary>
public sealed record SettingsInput(
    bool Capture, string DefaultApplyMode, string TtlText, string HistoryText, bool SendSensitive, string DownloadDir);

public sealed record SettingsValidation(SettingsValues? Values, IReadOnlyList<string> Errors)
{
    public bool Ok => Errors.Count == 0 && Values is not null;
}

public static class SettingsModel
{
    public static readonly string[] Modes = { "notify", "manual", "auto" };
    public const int TtlMin = 30, TtlMax = 86400, HistoryMin = 10, HistoryMax = 5000;

    private static readonly Regex DriveAbsolute = new(@"^[A-Za-z]:[\\/]", RegexOptions.Compiled);
    private static readonly Regex Unc = new(@"^\\\\[^\\/?*""<>|\s][^\\/?*""<>|]*\\[^\\/?*""<>|]+", RegexOptions.Compiled);
    private static readonly char[] BadPathChars = { '<', '>', '"', '|', '?', '*' };

    /// <summary>"C:\dir" or "\\server\share" (checked textually, so it also works off Windows).</summary>
    public static bool IsAbsoluteWindowsPath(string? path)
    {
        if (string.IsNullOrWhiteSpace(path) || path.Length > 259 || path.IndexOf('\0') >= 0) return false;
        if (path.IndexOfAny(BadPathChars) >= 0) return false; // ':' is only legal as the drive separator, checked below
        var p = path.Trim();
        if (p.StartsWith(@"\\?\", StringComparison.Ordinal)) return false; // device paths are not folders the user picked
        if (DriveAbsolute.IsMatch(p)) return p.IndexOf(':', 2) < 0;
        return Unc.IsMatch(p) && p.IndexOf(':') < 0;
    }

    public static SettingsValidation Validate(SettingsInput input)
    {
        var errors = new List<string>();
        if (!Modes.Contains(input.DefaultApplyMode)) errors.Add(Strings.ValidationMode);

        if (!int.TryParse(input.TtlText?.Trim(), NumberStyles.None, CultureInfo.InvariantCulture, out var ttl) || ttl < TtlMin || ttl > TtlMax)
        {
            errors.Add(Strings.ValidationTtl);
            ttl = SettingsValues.DefaultTtl;
        }
        if (!int.TryParse(input.HistoryText?.Trim(), NumberStyles.None, CultureInfo.InvariantCulture, out var history) || history < HistoryMin || history > HistoryMax)
        {
            errors.Add(Strings.ValidationHistory);
            history = SettingsValues.DefaultHistory;
        }
        var dir = (input.DownloadDir ?? "").Trim().TrimEnd('\\', '/');
        if (dir.Length == 2 && dir[1] == ':') dir += "\\"; // keep "C:\" intact
        if (!IsAbsoluteWindowsPath(dir)) errors.Add(Strings.ValidationDownloadDir);

        if (errors.Count > 0) return new SettingsValidation(null, errors);
        return new SettingsValidation(new SettingsValues(input.Capture, input.DefaultApplyMode, ttl, history,
            input.SendSensitive, dir, SettingsValues.DefaultMaxFileBytes), errors);
    }

    /// <summary>The <c>settings.set</c> calls needed to go from <paramref name="old"/> to <paramref name="updated"/>.</summary>
    public static IReadOnlyList<KeyValuePair<string, string>> Diff(SettingsValues old, SettingsValues updated)
    {
        var changes = new List<KeyValuePair<string, string>>();
        void Add(string k, string v) => changes.Add(new(k, v));
        if (old.Capture != updated.Capture) Add("capture", updated.Capture ? "on" : "off");
        if (old.DefaultApplyMode != updated.DefaultApplyMode) Add("default_apply_mode", updated.DefaultApplyMode);
        if (old.ClipTtlSeconds != updated.ClipTtlSeconds) Add("clip_ttl_s", updated.ClipTtlSeconds.ToString(CultureInfo.InvariantCulture));
        if (old.HistoryLimit != updated.HistoryLimit) Add("history_limit", updated.HistoryLimit.ToString(CultureInfo.InvariantCulture));
        if (old.SendSensitive != updated.SendSensitive) Add("send_sensitive", updated.SendSensitive ? "on" : "off");
        if (!string.Equals(old.DownloadDir, updated.DownloadDir, StringComparison.Ordinal)) Add("download_dir", updated.DownloadDir);
        return changes;
    }
}
