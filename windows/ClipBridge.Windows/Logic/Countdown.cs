using System.Globalization;

namespace ClipBridge.Windows.Logic;

/// <summary>Formatting of the pairing-code lifetime.</summary>
public static class Countdown
{
    public static DateTimeOffset? ParseRfc3339(string? text) =>
        DateTimeOffset.TryParse(text, CultureInfo.InvariantCulture,
            DateTimeStyles.AssumeUniversal | DateTimeStyles.AdjustToUniversal, out var t) ? t : null;

    /// <summary>"mm:ss" for the time left, or null once it has run out.</summary>
    public static string? Remaining(DateTimeOffset expires, DateTimeOffset now)
    {
        var left = expires - now;
        if (left <= TimeSpan.Zero) return null;
        var secs = (int)Math.Ceiling(left.TotalSeconds);
        return $"{secs / 60:D2}:{secs % 60:D2}";
    }

    public static bool IsExpired(DateTimeOffset expires, DateTimeOffset now) => expires <= now;
}
