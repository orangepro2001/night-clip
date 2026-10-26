using System.Text;

namespace ClipBridge.Windows.Logic;

public sealed class DraftField
{
    public string Name { get; set; } = "";
    public string Value { get; set; } = "";
    public bool Protected { get; set; }

    public DraftField Clone() => new() { Name = Name, Value = Value, Protected = Protected };
}

/// <summary>
/// The entry being edited: plain data without any UI type, so that "what is sent to the core" and "has
/// anything changed" are testable. The editor window fills it from vault.get plus vault.reveal calls.
/// </summary>
public sealed class EntryDraft
{
    public string Id { get; set; } = "";
    public string GroupId { get; set; } = "";
    public string Kind { get; set; } = EntryKinds.Login;
    public string Title { get; set; } = "";
    public string Username { get; set; } = "";
    public string Password { get; set; } = "";
    public string Url { get; set; } = "";
    public string Notes { get; set; } = "";
    public string Totp { get; set; } = "";

    /// <summary>Tags as the user types them: separated by comma, semicolon or whitespace-free text.</summary>
    public string TagsText { get; set; } = "";

    public long ExpiresAt { get; set; }
    public List<DraftField> Fields { get; set; } = new();

    public EntryDraft Clone() => new()
    {
        Id = Id, GroupId = GroupId, Kind = Kind, Title = Title, Username = Username, Password = Password, Url = Url,
        Notes = Notes, Totp = Totp, TagsText = TagsText, ExpiresAt = ExpiresAt, Fields = Fields.Select(f => f.Clone()).ToList(),
    };

    /// <summary>Tags split at comma / semicolon, trimmed, duplicates (case-insensitive) dropped, original order kept.</summary>
    public static IReadOnlyList<string> ParseTags(string text)
    {
        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        var tags = new List<string>();
        foreach (var raw in text.Split(new[] { ',', ';', '，', '；' }, StringSplitOptions.RemoveEmptyEntries))
        {
            var t = raw.Trim();
            if (t.Length > 0 && seen.Add(t)) tags.Add(t);
        }
        return tags;
    }

    public IReadOnlyList<string> Tags => ParseTags(TagsText);

    /// <summary>Same content? (Id and group are identity, not content.) Used for the "unsaved changes" prompt.</summary>
    public bool SameContentAs(EntryDraft other)
    {
        if (Kind != other.Kind || Title != other.Title || Username != other.Username || Password != other.Password
            || Url != other.Url || Notes != other.Notes || Totp != other.Totp || ExpiresAt != other.ExpiresAt) return false;
        if (!Tags.SequenceEqual(other.Tags, StringComparer.Ordinal)) return false;
        if (Fields.Count != other.Fields.Count) return false;
        for (var i = 0; i < Fields.Count; i++)
        {
            var a = Fields[i];
            var b = other.Fields[i];
            if (a.Name != b.Name || a.Value != b.Value || a.Protected != b.Protected) return false;
        }
        return true;
    }

    /// <summary>
    /// The <c>entry</c> object of vault.add / vault.update. Every field is sent, visible in the form or not:
    /// vault.update replaces the whole entry, so leaving a hidden one out would erase it.
    /// </summary>
    public Dictionary<string, object?> ToParams(bool includeGroup)
    {
        var p = new Dictionary<string, object?>
        {
            ["kind"] = Kind,
            ["title"] = Title.Trim(),
            ["username"] = Username,
            ["password"] = Password,
            ["url"] = Url.Trim(),
            ["notes"] = Notes,
            ["tags"] = Tags,
            ["fields"] = Fields.Where(f => f.Name.Trim().Length > 0 || f.Value.Length > 0)
                .Select(f => new Dictionary<string, object?> { ["name"] = f.Name.Trim(), ["value"] = f.Value, ["protected"] = f.Protected })
                .ToList(),
            ["totp"] = Totp.Trim(),
            ["expires_at"] = ExpiresAt,
        };
        if (includeGroup && GroupId.Length > 0) p["group_id"] = GroupId;
        return p;
    }
}

public static class EntryKinds
{
    public const string Login = "login";
    public const string ApiKey = "apikey";
    public const string Secret = "secret";
    public const string Note = "note";

    public static readonly IReadOnlyList<string> All = new[] { Login, ApiKey, Secret, Note };

    public static string Normalize(string? kind) => kind is Login or ApiKey or Secret or Note ? kind : Login;
}

/// <summary>Which form fields a kind shows, and what they are called (docs/vault-api.md §3.2).</summary>
public sealed record KindTemplate(
    string Kind, string Label, string Glyph, bool Username, bool Password, string PasswordLabel, bool Url, string UrlLabel,
    bool Totp, bool Notes, bool Tags, bool Expiry, bool CustomFields, IReadOnlyList<string> FieldSuggestions)
{
    public static KindTemplate For(string? kind) => EntryKinds.Normalize(kind) switch
    {
        EntryKinds.ApiKey => new(EntryKinds.ApiKey, Strings.KindApiKey, "🔑", false, true, Strings.LabelApiKeyValue, true, Strings.LabelServiceUrl,
            false, true, true, true, true, new[] { "Key ID", "Environment", "Region" }),
        EntryKinds.Secret => new(EntryKinds.Secret, Strings.KindSecret, "🔒", false, true, Strings.LabelSecretValue, false, Strings.LabelUrl,
            false, true, false, false, false, Array.Empty<string>()),
        EntryKinds.Note => new(EntryKinds.Note, Strings.KindNote, "📝", false, false, Strings.LabelPassword, false, Strings.LabelUrl,
            false, true, true, false, false, Array.Empty<string>()),
        _ => new(EntryKinds.Login, Strings.KindLogin, "👤", true, true, Strings.LabelPassword, true, Strings.LabelUrl,
            true, true, true, true, true, Array.Empty<string>()),
    };

    /// <summary>The icon for a kind in lists.</summary>
    public static string GlyphFor(string? kind) => For(kind).Glyph;
}

/// <summary>Which parts of the form are visible: the template says so, or the entry already holds a value there.</summary>
public readonly record struct FormVisibility(
    bool Username, bool Password, bool Url, bool Totp, bool Notes, bool Tags, bool Expiry, bool CustomFields)
{
    public static FormVisibility For(EntryDraft d)
    {
        var t = KindTemplate.For(d.Kind);
        return new FormVisibility(
            t.Username || d.Username.Length > 0,
            t.Password || d.Password.Length > 0,
            t.Url || d.Url.Length > 0,
            t.Totp || d.Totp.Length > 0,
            t.Notes || d.Notes.Length > 0,
            t.Tags || d.Tags.Count > 0,
            t.Expiry || d.ExpiresAt != 0,
            t.CustomFields || d.Fields.Count > 0);
    }
}

public sealed record ValidationIssue(string Field, string Message, bool IsError);

/// <summary>Client-side checks that mirror core/vault/model.go validate(), plus a few warnings.</summary>
public static class EntryValidator
{
    public const int MaxFieldBytes = 256 * 1024;
    public const int MaxFieldNameBytes = 128;
    public const int MaxCount = 100;

    private static readonly HashSet<string> Reserved = new(StringComparer.OrdinalIgnoreCase)
        { "title", "username", "password", "url", "notes", "otp" };

    private static int Bytes(string s) => Encoding.UTF8.GetByteCount(s);

    public static IReadOnlyList<ValidationIssue> Validate(EntryDraft d)
    {
        var issues = new List<ValidationIssue>();
        if (d.Title.Trim().Length == 0) issues.Add(new("title", Strings.ValTitleRequired, true));

        void Size(string field, string label, string value)
        {
            if (Bytes(value) > MaxFieldBytes) issues.Add(new(field, Strings.ValTooLarge(label), true));
        }
        Size("title", Strings.LabelTitle, d.Title);
        Size("username", Strings.LabelUsername, d.Username);
        Size("password", Strings.LabelPassword, d.Password);
        Size("url", Strings.LabelUrl, d.Url);
        Size("notes", Strings.LabelNotes, d.Notes);
        Size("totp", Strings.LabelTotp, d.Totp);

        if (d.Fields.Count > MaxCount || d.Tags.Count > MaxCount) issues.Add(new("fields", Strings.ValTooMany, true));

        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (var f in d.Fields)
        {
            var name = f.Name.Trim();
            if (name.Length == 0 && f.Value.Length == 0) continue; // an empty row is dropped when saving
            if (name.Length == 0 || Bytes(name) > MaxFieldNameBytes || Reserved.Contains(name) || name.StartsWith("kph:", StringComparison.OrdinalIgnoreCase))
                issues.Add(new("fields", Strings.ValFieldName(f.Name), true));
            else if (!seen.Add(name))
                issues.Add(new("fields", Strings.ValFieldDuplicate(name), true));
            if (Bytes(f.Value) > MaxFieldBytes) issues.Add(new("fields", Strings.ValTooLarge(name), true));
        }

        // Warnings: the entry can still be saved.
        var url = d.Url.Trim();
        if (url.Length > 0 && !UrlSafety.LooksLikeWebUrl(url)) issues.Add(new("url", Strings.ValUrlNotWeb, false));
        if (d.Totp.Trim().Length > 0 && !TotpInput.LooksValid(d.Totp)) issues.Add(new("totp", Strings.ValTotpFormat, false));
        return issues;
    }

    public static bool HasErrors(IReadOnlyList<ValidationIssue> issues) => issues.Any(i => i.IsError);
}

/// <summary>What a TOTP box may contain: an otpauth:// URI or a base32 secret (spaces and dashes are tolerated).</summary>
public static class TotpInput
{
    public static bool LooksValid(string text)
    {
        var t = text.Trim();
        if (t.StartsWith("otpauth://", StringComparison.OrdinalIgnoreCase)) return t.Length > "otpauth://".Length;
        var compact = new string(t.Where(c => c is not (' ' or '-')).ToArray()).TrimEnd('=').ToUpperInvariant();
        return compact.Length >= 8 && compact.All(c => c is >= 'A' and <= 'Z' or >= '2' and <= '7');
    }
}

/// <summary>Opening links: only explicit http / https addresses ever reach the default browser.</summary>
public static class UrlSafety
{
    public static Uri? TryGetWebUri(string? text)
    {
        var t = text?.Trim();
        if (string.IsNullOrEmpty(t) || t.Any(char.IsControl)) return null;
        if (!Uri.TryCreate(t, UriKind.Absolute, out var uri)) return null;
        if (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps) return null;
        return uri.Host.Length == 0 ? null : uri;
    }

    public static bool LooksLikeWebUrl(string? text) => TryGetWebUri(text) is not null;
}

/// <summary>Conversions of the expiry date picker (a local calendar date) to the core's Unix seconds.</summary>
public static class ExpiryDates
{
    /// <summary>The picked day counts through its last second (local time); null = no expiry.</summary>
    public static long FromDate(DateTime? localDate, TimeZoneInfo? zone = null)
    {
        if (localDate is null) return 0;
        zone ??= TimeZoneInfo.Local;
        var end = new DateTime(localDate.Value.Year, localDate.Value.Month, localDate.Value.Day, 23, 59, 59, DateTimeKind.Unspecified);
        return new DateTimeOffset(end, zone.GetUtcOffset(end)).ToUnixTimeSeconds();
    }

    public static DateTime? ToDate(long unix, TimeZoneInfo? zone = null)
    {
        if (unix <= 0) return null;
        zone ??= TimeZoneInfo.Local;
        return TimeZoneInfo.ConvertTime(DateTimeOffset.FromUnixTimeSeconds(unix), zone).Date;
    }
}
