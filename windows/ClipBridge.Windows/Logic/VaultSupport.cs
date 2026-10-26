namespace ClipBridge.Windows.Logic;

/// <summary>The result of a vault call made through the shell: a value, or the error code and its friendly text.</summary>
public sealed record VaultResult<T>(T? Value, string? ErrorCode, string? Error, int RetryAfterSeconds)
{
    public bool Ok => Error is null;

    public static VaultResult<T> Success(T value) => new(value, null, null, 0);

    public static VaultResult<T> Fail(string code, int retryAfter) => new(default, code, ErrorMessages.ForVault(code, retryAfter), retryAfter);
}

/// <summary>
/// The core resets its idle timer on every vault.* call except status. Polling vault.totp for an open
/// entry must therefore stop while the user is away, or the vault would never auto-lock.
/// </summary>
public static class TotpActivityPolicy
{
    /// <summary>How long after the last user input the code keeps refreshing: a third of the auto-lock time, 10 to 60 seconds.</summary>
    public static TimeSpan ActiveWindow(int autolockSeconds) => TimeSpan.FromSeconds(Math.Clamp(autolockSeconds / 3, 10, 60));

    public static bool ShouldPoll(DateTimeOffset lastActivity, DateTimeOffset now, int autolockSeconds) =>
        now - lastActivity <= ActiveWindow(autolockSeconds);
}

/// <summary>Attachments: one file up to 5 MiB (core/vault MaxAttachmentBytes).</summary>
public static class AttachmentRules
{
    public const long MaxBytes = 5L << 20;

    public static string? Check(long length) => length > MaxBytes ? Strings.AttachTooBig : null;
}

/// <summary>Group names as the core accepts them: trimmed, 1 to 128 bytes, no control line breaks or NUL.</summary>
public static class GroupNames
{
    public static string? Normalize(string? name)
    {
        var n = (name ?? "").Trim();
        if (n.Length == 0 || System.Text.Encoding.UTF8.GetByteCount(n) > 128) return null;
        return n.IndexOfAny(new[] { '\0', '\r', '\n' }) >= 0 ? null : n;
    }
}

/// <summary>Search box debouncing and the length cap of a query.</summary>
public static class SearchInput
{
    public const int DebounceMs = 250;
    public const int MaxChars = 200;

    public static string Normalize(string? text)
    {
        var t = (text ?? "").Trim();
        return t.Length <= MaxChars ? t : t[..MaxChars];
    }
}

/// <summary>Plain text of a timestamp for the detail pane ("2026-10-26 22:00"); empty for unset.</summary>
public static class StampText
{
    public static string Local(long unix, TimeZoneInfo? zone = null)
    {
        if (unix <= 0) return "";
        zone ??= TimeZoneInfo.Local;
        return TimeZoneInfo.ConvertTime(DateTimeOffset.FromUnixTimeSeconds(unix), zone)
            .ToString("yyyy-MM-dd HH:mm", System.Globalization.CultureInfo.InvariantCulture);
    }

    public static string DateOnly(long unix, TimeZoneInfo? zone = null)
    {
        if (unix <= 0) return "";
        zone ??= TimeZoneInfo.Local;
        return TimeZoneInfo.ConvertTime(DateTimeOffset.FromUnixTimeSeconds(unix), zone)
            .ToString("yyyy-MM-dd", System.Globalization.CultureInfo.InvariantCulture);
    }
}
