namespace ClipBridge.Windows.Logic;

/// <summary>Key files: any file's bytes, 1 byte to 1 MiB (docs/vault-api.md §3).</summary>
public static class KeyFileRules
{
    public const int MaxBytes = 1 << 20;

    /// <summary>Null when acceptable, otherwise the message to show.</summary>
    public static string? Check(long length) =>
        length <= 0 ? Strings.KeyFileEmpty : length > MaxBytes ? Strings.KeyFileTooBig : null;

    /// <summary>Reads a key file (checking the size first so a huge file is never loaded).</summary>
    public static (byte[]? Bytes, string? Error) Load(string path)
    {
        try
        {
            var info = new FileInfo(path);
            if (!info.Exists) return (null, Strings.KeyFileMissing);
            if (Check(info.Length) is { } error) return (null, error);
            var bytes = File.ReadAllBytes(path);
            return Check(bytes.Length) is { } e2 ? (null, e2) : (bytes, null);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            return (null, Strings.KeyFileUnreadable);
        }
    }
}

/// <summary>The strength bar: label to color, fill and Chinese text. Colors are ARGB values (UI converts them to brushes).</summary>
public static class StrengthDisplay
{
    public const uint Gray = 0xFF9AA0A6;

    public static string Normalize(string? label) => label switch
    {
        "very_weak" or "weak" or "fair" or "good" or "strong" => label,
        _ => "very_weak",
    };

    public static string Text(string? label) => Normalize(label) switch
    {
        "weak" => Strings.StrengthWeak,
        "fair" => Strings.StrengthFair,
        "good" => Strings.StrengthGood,
        "strong" => Strings.StrengthStrong,
        _ => Strings.StrengthVeryWeak,
    };

    public static uint Color(string? label) => Normalize(label) switch
    {
        "weak" => 0xFFE8710A,
        "fair" => 0xFFF2B600,
        "good" => 0xFF7CB342,
        "strong" => 0xFF1E8E3E,
        _ => 0xFFD93025,
    };

    /// <summary>Number of filled segments out of 5 (very weak = 1 ... strong = 5).</summary>
    public static int Segments(string? label) => Normalize(label) switch
    {
        "weak" => 2,
        "fair" => 3,
        "good" => 4,
        "strong" => 5,
        _ => 1,
    };

    public static double Fraction(string? label) => Segments(label) / 5.0;

    /// <summary>True for the labels that deserve a warning when saving or choosing a master password.</summary>
    public static bool IsWeak(string? label) => Normalize(label) is "very_weak" or "weak";

    public static string Bits(double bits) => bits <= 0 ? "" : Strings.StrengthBits((int)Math.Round(bits));
}

/// <summary>
/// The wait after too many wrong passwords (RATE_LIMITED). The core says "retry in N seconds"; this turns
/// that into a deadline so a ticking timer can show the countdown and enable the button again.
/// </summary>
public sealed class RateLimitCountdown
{
    private DateTimeOffset _until = DateTimeOffset.MinValue;

    public void Start(int seconds, DateTimeOffset now) =>
        _until = seconds > 0 ? now.AddSeconds(seconds) : DateTimeOffset.MinValue;

    public void Clear() => _until = DateTimeOffset.MinValue;

    /// <summary>Whole seconds still to wait (rounded up); 0 when free to try again.</summary>
    public int SecondsLeft(DateTimeOffset now)
    {
        var left = _until - now;
        return left <= TimeSpan.Zero ? 0 : (int)Math.Ceiling(left.TotalSeconds);
    }

    public bool IsBlocked(DateTimeOffset now) => SecondsLeft(now) > 0;
}

/// <summary>The screens of the vault page.</summary>
public enum VaultScreen { Loading, Create, Locked, Unlocked }

/// <summary>What the locked / create screens show after an attempt.</summary>
public sealed record UnlockOutcome(string Message, int RetryAfterSeconds, bool IsWrongCredentials)
{
    public static UnlockOutcome FromError(string code, int retryAfter) =>
        new(ErrorMessages.ForVault(code, retryAfter), code == "RATE_LIMITED" ? Math.Max(1, retryAfter) : 0, code == "VAULT_WRONG_CREDENTIALS");
}

/// <summary>Which screen follows from vault.status, and how the locked screen reacts to vault.locked events.</summary>
public static class VaultScreens
{
    public static VaultScreen From(VaultStatus s) => !s.Exists ? VaultScreen.Create : s.Unlocked ? VaultScreen.Unlocked : VaultScreen.Locked;

    /// <summary>The note shown on the locked screen after the core or the shell locked the vault.</summary>
    public static string LockedNote(string? reason) => reason switch
    {
        "idle" => Strings.LockedIdle,
        "session" => Strings.LockedSession,
        "user" => "",
        "" or null => "",
        _ => Strings.LockedOther,
    };
}

/// <summary>Checks for the create-vault form.</summary>
public static class CreateVaultForm
{
    public static readonly IReadOnlyList<string> KdfChoices = new[] { "low", "standard", "high" };

    public static string KdfLabel(string kdf) => kdf switch
    {
        "low" => Strings.KdfLow,
        "high" => Strings.KdfHigh,
        _ => Strings.KdfStandard,
    };

    /// <summary>Errors block creating; <paramref name="weak"/> is only a warning (the user may keep a weak password).</summary>
    public static (string? Error, bool WeakWarning) Check(string password, string confirm, string? strengthLabel)
    {
        if (password.Length == 0) return (Strings.CreateEmptyPassword, false);
        if (password != confirm) return (Strings.CreateMismatch, false);
        return (null, StrengthDisplay.IsWeak(strengthLabel));
    }
}

/// <summary>Checks for the change-master-password form.</summary>
public static class ChangeMasterForm
{
    public static string? Check(string current, string next, string confirm)
    {
        if (current.Length == 0) return Strings.ChangeNeedCurrent;
        if (next.Length == 0) return Strings.CreateEmptyPassword;
        if (next != confirm) return Strings.CreateMismatch;
        return null;
    }
}

/// <summary>Checks for the export dialogs.</summary>
public static class ExportForm
{
    /// <summary>KDBX export: the master password again, and a NEW password for the file (it should differ from the master).</summary>
    public static string? CheckKdbx(string master, string filePassword, string confirm)
    {
        if (master.Length == 0) return Strings.ExportNeedMaster;
        if (filePassword.Length == 0) return Strings.CreateEmptyPassword;
        if (filePassword != confirm) return Strings.CreateMismatch;
        return null;
    }

    public static string? CheckCsv(string master) => master.Length == 0 ? Strings.ExportNeedMaster : null;

    /// <summary>The wording the user has to type to confirm a plain-text export.</summary>
    public static bool CsvConfirmed(string typed) => string.Equals(typed.Trim(), Strings.ExportCsvConfirmWord, StringComparison.Ordinal);

    public static string DefaultFileName(string kind, DateTimeOffset now) =>
        $"night-clip-vault-{now.LocalDateTime:yyyyMMdd-HHmm}.{(kind == "csv" ? "csv" : "kdbx")}";
}

/// <summary>Picks a name that does not exist yet: the core never overwrites a file.</summary>
public static class FreeName
{
    public static string Next(string path, Func<string, bool> exists)
    {
        if (!exists(path)) return path;
        var dir = Path.GetDirectoryName(path) ?? "";
        var stem = Path.GetFileNameWithoutExtension(path);
        var ext = Path.GetExtension(path);
        for (var i = 1; i < 1000; i++)
        {
            var candidate = Path.Combine(dir, $"{stem} ({i}){ext}");
            if (!exists(candidate)) return candidate;
        }
        return Path.Combine(dir, $"{stem}-{Guid.NewGuid():N}{ext}");
    }
}

/// <summary>Vault settings (settings.set vault_autolock_s / vault_clip_clear_s).</summary>
public static class VaultSettingsForm
{
    public const int AutolockMin = 0; // 0 = never lock
    public const int AutolockMax = 86400;
    public const int ClipClearMin = 5;
    public const int ClipClearMax = 300;

    public static (int? Value, string? Error) ParseAutolock(string text) =>
        int.TryParse(text.Trim(), System.Globalization.NumberStyles.None, System.Globalization.CultureInfo.InvariantCulture, out var v)
        && (v == 0 || v >= 30 && v <= AutolockMax)
            ? (v, null) : (null, Strings.VaultAutolockInvalid(AutolockMin, AutolockMax));

    /// <summary>0 turns clearing off.</summary>
    public static (int? Value, string? Error) ParseClipClear(string text) =>
        int.TryParse(text.Trim(), System.Globalization.NumberStyles.None, System.Globalization.CultureInfo.InvariantCulture, out var v)
        && (v == 0 || (v >= ClipClearMin && v <= ClipClearMax))
            ? (v, null) : (null, Strings.VaultClipClearInvalid(ClipClearMin, ClipClearMax));
}

/// <summary>Options of the password generator popup (vault.generate).</summary>
public sealed record GeneratorOptions(
    bool Passphrase = false, int Length = 20, bool Lower = true, bool Upper = true, bool Digits = true, bool Symbols = true,
    bool ExcludeAmbiguous = false, int Words = 6, string Separator = "-", bool Capitalize = true, bool Number = false)
{
    public const int MinLength = 8;
    public const int MaxLength = 64;
    public const int MinWords = 3;
    public const int MaxWords = 20;

    public GeneratorOptions Clamped() => this with
    {
        Length = Math.Clamp(Length, MinLength, MaxLength),
        Words = Math.Clamp(Words, MinWords, MaxWords),
        // The core treats "no class selected" as "all classes"; make that explicit in the UI as well.
        Lower = Lower || !(Upper || Digits || Symbols),
        Separator = Separator.Length > 8 ? Separator[..8] : Separator,
    };

    public Dictionary<string, object?> ToParams()
    {
        var o = Clamped();
        if (o.Passphrase)
            return new Dictionary<string, object?>
            {
                ["type"] = "passphrase", ["words"] = o.Words, ["separator"] = o.Separator.Length == 0 ? "-" : o.Separator,
                ["capitalize"] = o.Capitalize, ["number"] = o.Number,
            };
        return new Dictionary<string, object?>
        {
            ["type"] = "password", ["length"] = o.Length, ["lower"] = o.Lower, ["upper"] = o.Upper, ["digits"] = o.Digits,
            ["symbols"] = o.Symbols, ["exclude_ambiguous"] = o.ExcludeAmbiguous,
        };
    }
}
