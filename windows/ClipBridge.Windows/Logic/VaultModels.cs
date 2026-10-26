using System.Text.Json;

namespace ClipBridge.Windows.Logic;

// Typed views of the vault.* replies (docs/vault-api.md §3). Parsing is tolerant like JsonRead:
// a missing or mistyped field gives the default, never an exception. None of these types holds a
// secret except where a method explicitly returns one (see VaultApi.Reveal / Copy / Generate).

public sealed record VaultStatus(
    bool Exists, bool Unlocked, int AutolockS, int ClipClearS, int RetryAfterS, int Entries, int Backups,
    long LastManualBackupAt, string Kdf)
{
    public static VaultStatus FromJson(JsonElement r)
    {
        var kdf = JsonRead.Obj(r, "kdf");
        var kdfText = kdf.ValueKind == JsonValueKind.Object
            ? $"Argon2d {JsonRead.Long(kdf, "MemoryMiB")} MiB × {JsonRead.Long(kdf, "Iterations")}"
            : "";
        return new VaultStatus(
            JsonRead.Bool(r, "exists"), JsonRead.Bool(r, "unlocked"),
            (int)JsonRead.Long(r, "autolock_s", 300), (int)JsonRead.Long(r, "clip_clear_s", 20),
            (int)JsonRead.Long(r, "retry_after_s"), (int)JsonRead.Long(r, "entries"), (int)JsonRead.Long(r, "backups"),
            JsonRead.Long(r, "last_manual_backup_at"), kdfText);
    }
}

public sealed record PasswordStrength(double Bits, string Label)
{
    public static PasswordStrength FromJson(JsonElement r) =>
        new(r.ValueKind == JsonValueKind.Object && r.TryGetProperty("bits", out var b) && b.ValueKind == JsonValueKind.Number ? b.GetDouble() : 0,
            JsonRead.Str(r, "label", "very_weak"));
}

public sealed record EntrySummary(
    string Id, string GroupId, string Kind, string Title, string Username, string Url, IReadOnlyList<string> Tags,
    bool HasTotp, bool HasAttachments, long ModifiedAt, long ExpiresAt, bool InTrash)
{
    public static EntrySummary FromJson(JsonElement e) => new(
        JsonRead.Str(e, "id"), JsonRead.Str(e, "group_id"), JsonRead.Str(e, "kind", "login"), JsonRead.Str(e, "title"),
        JsonRead.Str(e, "username"), JsonRead.Str(e, "url"), JsonRead.StrArr(e, "tags"),
        JsonRead.Bool(e, "has_totp"), JsonRead.Bool(e, "has_attachments"), JsonRead.Long(e, "modified_at"),
        JsonRead.Long(e, "expires_at"), JsonRead.Bool(e, "in_trash"));

    public static IReadOnlyList<EntrySummary> ListFromJson(JsonElement r) =>
        JsonRead.Arr(r, "entries").Select(FromJson).ToList();
}

public sealed record EntryFieldInfo(string Name, string Value, bool Protected, bool HasValue);

public sealed record AttachmentInfo(string Name, long Size);

/// <summary>vault.get: everything but the secrets (password, TOTP secret, protected field values).</summary>
public sealed record EntryDetail(
    EntrySummary Summary, string Notes, IReadOnlyList<EntryFieldInfo> Fields, bool HasPassword,
    IReadOnlyList<AttachmentInfo> Attachments, int History, long CreatedAt, PasswordStrength Strength)
{
    public string Id => Summary.Id;

    public static EntryDetail FromJson(JsonElement e) => new(
        EntrySummary.FromJson(e), JsonRead.Str(e, "notes"),
        JsonRead.Arr(e, "fields").Select(f => new EntryFieldInfo(JsonRead.Str(f, "name"), JsonRead.Str(f, "value"),
            JsonRead.Bool(f, "protected"), JsonRead.Bool(f, "has_value"))).ToList(),
        JsonRead.Bool(e, "has_password"),
        JsonRead.Arr(e, "attachments").Select(a => new AttachmentInfo(JsonRead.Str(a, "name"), JsonRead.Long(a, "size"))).ToList(),
        (int)JsonRead.Long(e, "history"), JsonRead.Long(e, "created_at"),
        PasswordStrength.FromJson(JsonRead.Obj(e, "password_strength")));
}

public sealed record GroupInfo(string Id, string ParentId, string Name, string Path, int Entries, bool IsTrash)
{
    public static IReadOnlyList<GroupInfo> ListFromJson(JsonElement r) =>
        JsonRead.Arr(r, "groups").Select(g => new GroupInfo(JsonRead.Str(g, "id"), JsonRead.Str(g, "parent_id"),
            JsonRead.Str(g, "name"), JsonRead.Str(g, "path"), (int)JsonRead.Long(g, "entries"), JsonRead.Bool(g, "is_trash"))).ToList();
}

public sealed record VersionInfo(int Index, long ModifiedAt, string Title, string Username, string Url)
{
    public static IReadOnlyList<VersionInfo> ListFromJson(JsonElement r) =>
        JsonRead.Arr(r, "versions").Select(v => new VersionInfo((int)JsonRead.Long(v, "index"), JsonRead.Long(v, "modified_at"),
            JsonRead.Str(v, "title"), JsonRead.Str(v, "username"), JsonRead.Str(v, "url"))).ToList();
}

public sealed record TotpCode(string Code, int SecondsLeft);

public sealed record BackupInfo(string Name, long Time, long Size, string Reason)
{
    public static BackupInfo FromJson(JsonElement b) =>
        new(JsonRead.Str(b, "name"), JsonRead.Long(b, "time"), JsonRead.Long(b, "size"), JsonRead.Str(b, "reason"));

    public static IReadOnlyList<BackupInfo> ListFromJson(JsonElement r) => JsonRead.Arr(r, "backups").Select(FromJson).ToList();
}

public sealed record BackupCheck(int Entries, int Groups, int InTrash, long ModifiedAt)
{
    public static BackupCheck FromJson(JsonElement r) =>
        new((int)JsonRead.Long(r, "entries"), (int)JsonRead.Long(r, "groups"), (int)JsonRead.Long(r, "in_trash"), JsonRead.Long(r, "modified_at"));
}

public sealed record DiffItem(string Id, string Title, long ModifiedAt);

public sealed record RestorePreview(
    IReadOnlyList<DiffItem> OnlyInBackup, IReadOnlyList<DiffItem> OnlyInCurrent, IReadOnlyList<DiffItem> Changed, int Same)
{
    private static IReadOnlyList<DiffItem> Items(JsonElement r, string name) =>
        JsonRead.Arr(r, name).Select(i => new DiffItem(JsonRead.Str(i, "id"), JsonRead.Str(i, "title"), JsonRead.Long(i, "modified_at"))).ToList();

    public static RestorePreview FromJson(JsonElement r) =>
        new(Items(r, "only_in_backup"), Items(r, "only_in_current"), Items(r, "changed"), (int)JsonRead.Long(r, "same"));
}

public sealed record MergeStats(int EntriesAdded, int EntriesUpdated, int EntriesRemoved, int EntriesMoved, int GroupsAdded, int GroupsRemoved)
{
    public static MergeStats FromJson(JsonElement r)
    {
        var s = JsonRead.Obj(r, "stats");
        return new((int)JsonRead.Long(s, "entries_added"), (int)JsonRead.Long(s, "entries_updated"), (int)JsonRead.Long(s, "entries_removed"),
            (int)JsonRead.Long(s, "entries_moved"), (int)JsonRead.Long(s, "groups_added"), (int)JsonRead.Long(s, "groups_removed"));
    }
}

public sealed record HealthIssue(string Id, string Title, string Info);

public sealed record HealthReport(
    IReadOnlyList<HealthIssue> Weak, IReadOnlyList<HealthIssue> Reused, IReadOnlyList<HealthIssue> Old,
    IReadOnlyList<HealthIssue> Expired, int WithoutTotp, int Total)
{
    private static IReadOnlyList<HealthIssue> Issues(JsonElement r, string name) =>
        JsonRead.Arr(r, name).Select(i => new HealthIssue(JsonRead.Str(i, "id"), JsonRead.Str(i, "title"), JsonRead.Str(i, "info"))).ToList();

    public static HealthReport FromJson(JsonElement r) =>
        new(Issues(r, "weak"), Issues(r, "reused"), Issues(r, "old"), Issues(r, "expired"),
            (int)JsonRead.Long(r, "without_totp"), (int)JsonRead.Long(r, "total"));

    public int IssueCount => Weak.Count + Reused.Count + Old.Count + Expired.Count;
}

public sealed record GeneratedSecret(string Value, PasswordStrength Strength);

/// <summary>vault.copy: the text for the clipboard and how long to keep it there.</summary>
public sealed record CopyResult(string Value, int ClearAfterSeconds);
