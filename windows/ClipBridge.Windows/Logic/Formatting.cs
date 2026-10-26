using System.Globalization;

namespace ClipBridge.Windows.Logic;

/// <summary>Sizes, speeds, durations and "last seen" text.</summary>
public static class Formatting
{
    private static readonly string[] Units = { "B", "KB", "MB", "GB", "TB" };

    public static string Bytes(long bytes)
    {
        if (bytes < 0) bytes = 0;
        if (bytes < 1024) return $"{bytes} B";
        double v = bytes;
        var u = 0;
        while (v >= 1024 && u < Units.Length - 1) { v /= 1024; u++; }
        // 1 decimal below 100, none above: "1.5 MB", "12.3 MB", "123 MB".
        return v < 100 ? string.Create(CultureInfo.InvariantCulture, $"{v:0.0} {Units[u]}")
                       : string.Create(CultureInfo.InvariantCulture, $"{v:0} {Units[u]}");
    }

    public static string Speed(long bytesPerSecond) =>
        bytesPerSecond <= 0 ? Strings.Dash : Bytes(bytesPerSecond) + "/s";

    /// <summary>Remaining time as "1 小时 5 分钟" / "3 分钟 20 秒" / "12 秒"; empty when unknown.</summary>
    public static string Eta(long remainingBytes, long bytesPerSecond)
    {
        if (remainingBytes <= 0 || bytesPerSecond <= 0) return "";
        var secs = (long)Math.Ceiling((double)remainingBytes / bytesPerSecond);
        return Duration(secs);
    }

    public static string Duration(long secs)
    {
        if (secs < 0) secs = 0;
        if (secs >= 86400 * 2) return Strings.DurationHours((int)Math.Min(int.MaxValue, secs / 3600));
        if (secs >= 3600)
        {
            var h = (int)(secs / 3600);
            var m = (int)(secs % 3600 / 60);
            return m == 0 ? Strings.DurationHours(h) : Strings.DurationHoursMinutes(h, m);
        }
        if (secs >= 60)
        {
            var m = (int)(secs / 60);
            var s = (int)(secs % 60);
            return s == 0 ? Strings.DurationMinutes(m) : $"{m} 分钟 {s} 秒";
        }
        return Strings.DurationSeconds((int)secs);
    }

    /// <summary>Unix seconds (milliseconds are tolerated) to "刚刚" / "5 分钟前" / ...</summary>
    public static string LastSeen(long unix, DateTimeOffset now)
    {
        if (unix <= 0) return Strings.LastSeenNever;
        if (unix > 100_000_000_000) unix /= 1000; // milliseconds
        var ago = now.ToUnixTimeSeconds() - unix;
        if (ago < 60) return Strings.LastSeenNow;
        if (ago < 3600) return Strings.LastSeenMinutes((int)(ago / 60));
        if (ago < 86400) return Strings.LastSeenHours((int)(ago / 3600));
        return Strings.LastSeenDays((int)Math.Min(int.MaxValue, ago / 86400));
    }

    /// <summary>Local wall-clock text for inbox rows: "HH:mm" today, "MM-dd HH:mm" otherwise.</summary>
    public static string Time(long unix, DateTimeOffset now, TimeZoneInfo? zone = null)
    {
        if (unix <= 0) return "";
        if (unix > 100_000_000_000) unix /= 1000;
        zone ??= TimeZoneInfo.Local;
        var t = TimeZoneInfo.ConvertTime(DateTimeOffset.FromUnixTimeSeconds(unix), zone);
        var n = TimeZoneInfo.ConvertTime(now, zone);
        return t.Date == n.Date
            ? t.ToString("HH:mm", CultureInfo.InvariantCulture)
            : t.ToString("MM-dd HH:mm", CultureInfo.InvariantCulture);
    }

    /// <summary>"50%" regardless of the machine's culture.</summary>
    public static string Percent(double fraction) =>
        string.Create(CultureInfo.InvariantCulture, $"{(int)Math.Round(Math.Clamp(fraction, 0, 1) * 100)}%");

    /// <summary>Seconds as a friendly hint next to the TTL box: "15 分钟".</summary>
    public static string SecondsHint(int secs) => Duration(secs);
}
