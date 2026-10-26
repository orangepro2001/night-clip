namespace ClipBridge.Windows.Logic;

public enum DetailKind { Text, Link, Secret, Notes }

/// <summary>
/// One line of the detail pane. <see cref="RevealField"/> is the argument of vault.reveal / vault.copy.
/// A line with <see cref="Concealed"/> is masked until the user presses "显示"; <see cref="KnownValue"/>
/// is the plain value when vault.get already delivered it (user name, URL, notes, unprotected fields).
/// </summary>
public sealed record DetailRow(string Key, string Label, DetailKind Kind, string RevealField, string KnownValue, bool Concealed, bool CanCopy)
{
    public const string Mask = "••••••••";
}

public static class DetailRows
{
    public static string FieldKey(string name) => "field:" + name;

    /// <summary>The lines for an entry, in display order. TOTP and attachments have their own areas.</summary>
    public static IReadOnlyList<DetailRow> Build(EntryDetail d)
    {
        var template = KindTemplate.For(d.Summary.Kind);
        var rows = new List<DetailRow>();
        if (d.Summary.Username.Length > 0)
            rows.Add(new DetailRow("username", Strings.LabelUsername, DetailKind.Text, "username", d.Summary.Username, false, true));
        if (d.HasPassword)
            rows.Add(new DetailRow("password", template.PasswordLabel, DetailKind.Secret, "password", "", true, true));
        if (d.Summary.Url.Length > 0)
            rows.Add(new DetailRow("url", template.UrlLabel, DetailKind.Link, "url", d.Summary.Url, false, true));
        foreach (var f in d.Fields)
        {
            if (f.Protected)
            {
                if (f.HasValue)
                    rows.Add(new DetailRow(FieldKey(f.Name), f.Name, DetailKind.Secret, FieldKey(f.Name), "", true, true));
                else
                    rows.Add(new DetailRow(FieldKey(f.Name), f.Name, DetailKind.Text, FieldKey(f.Name), "", false, false));
            }
            else
                rows.Add(new DetailRow(FieldKey(f.Name), f.Name, DetailKind.Text, FieldKey(f.Name), f.Value, false, f.Value.Length > 0));
        }
        if (d.Notes.Length > 0)
        {
            // A secure note is the secret itself: masked until shown. Notes of other kinds are plain text.
            var concealed = d.Summary.Kind is EntryKinds.Note or EntryKinds.Secret;
            rows.Add(new DetailRow("notes", Strings.LabelNotes, DetailKind.Notes, "notes", d.Notes, concealed, true));
        }
        return rows;
    }

    /// <summary>What the line shows right now. <paramref name="revealed"/> is the value fetched with vault.reveal (null = masked).</summary>
    public static string Display(DetailRow row, string? revealed)
    {
        if (!row.Concealed) return row.KnownValue.Length > 0 || row.CanCopy ? row.KnownValue : Strings.Dash;
        if (revealed is null) return DetailRow.Mask;
        return revealed.Length > 0 ? revealed : row.KnownValue;
    }

    /// <summary>Notes are already in vault.get's answer: revealing them needs no extra call.</summary>
    public static bool NeedsFetch(DetailRow row) => row.Concealed && row.KnownValue.Length == 0;
}

/// <summary>
/// Which secret lines are currently shown. They are masked again after <see cref="Seconds"/> or when another
/// entry is selected (docs/vault-api.md §4 rule 3). The values themselves live in the view and are dropped
/// whenever <see cref="Expired"/> or <see cref="Clear"/> say so.
/// </summary>
public sealed class RevealState
{
    public const int Seconds = 15;

    private readonly Dictionary<string, DateTimeOffset> _until = new();
    private string _entryId = "";

    public string EntryId => _entryId;

    /// <summary>Switching entries masks everything.</summary>
    public void SelectEntry(string entryId)
    {
        if (entryId != _entryId) _until.Clear();
        _entryId = entryId;
    }

    public void Reveal(string key, DateTimeOffset now) => _until[key] = now.AddSeconds(Seconds);

    public void Hide(string key) => _until.Remove(key);

    public bool IsRevealed(string key, DateTimeOffset now) => _until.TryGetValue(key, out var t) && t > now;

    /// <summary>Removes and returns the keys whose time is up.</summary>
    public IReadOnlyList<string> Expired(DateTimeOffset now)
    {
        var due = _until.Where(kv => kv.Value <= now).Select(kv => kv.Key).ToList();
        foreach (var k in due) _until.Remove(k);
        return due;
    }

    public bool Any => _until.Count > 0;

    public void Clear()
    {
        _until.Clear();
        _entryId = "";
    }
}

/// <summary>
/// The TOTP ring. vault.totp answers with the code and the seconds left; between answers the seconds are
/// counted down locally. A new code is fetched when the time runs out. The period is not part of the
/// answer, so it is inferred: 30 s unless more seconds are left than that, then the next standard period (60, 90...).
/// </summary>
public sealed class TotpCountdown
{
    private DateTimeOffset _expires = DateTimeOffset.MinValue;
    private int _period = 30;

    public string Code { get; private set; } = "";
    public bool HasCode => Code.Length > 0;

    public void Update(TotpCode code, DateTimeOffset now)
    {
        Code = code.Code;
        var left = Math.Max(0, code.SecondsLeft);
        _expires = now.AddSeconds(left);
        if (left > _period) _period = (int)Math.Ceiling(left / 30.0) * 30;
    }

    public void Clear()
    {
        Code = "";
        _expires = DateTimeOffset.MinValue;
        _period = 30;
    }

    public int SecondsLeft(DateTimeOffset now)
    {
        var left = _expires - now;
        return left <= TimeSpan.Zero ? 0 : (int)Math.Ceiling(left.TotalSeconds);
    }

    /// <summary>1 = the code was just issued, 0 = about to change (for the ring).</summary>
    public double Fraction(DateTimeOffset now) => Math.Clamp(SecondsLeft(now) / (double)_period, 0, 1);

    /// <summary>Time to ask the core for the next code.</summary>
    public bool NeedsRefresh(DateTimeOffset now) => !HasCode || SecondsLeft(now) <= 0;

    /// <summary>A code the user can read: "123 456" (6 digits), "1234 5678" (8), others unchanged.</summary>
    public static string Group(string code) => code.Length switch
    {
        6 => code[..3] + " " + code[3..],
        8 => code[..4] + " " + code[4..],
        7 => code[..3] + " " + code[3..],
        _ => code,
    };

    /// <summary>Ring colour: red in the last five seconds.</summary>
    public static uint ColorArgb(int secondsLeft) => secondsLeft <= 5 ? 0xFFD93025u : 0xFF1A73E8u;
}

public enum ClearDecision { Clear, SkipChanged, SkipDisabled, SkipUnknown }

/// <summary>
/// "Clear the clipboard after N seconds, but only if it still holds what we wrote" (docs/vault-api.md §4 rule 1).
/// The proof is the clipboard sequence number remembered right after our own write.
/// </summary>
public static class ClipboardClearPolicy
{
    /// <param name="ourSeq">The sequence number after our write (0 = unknown).</param>
    /// <param name="currentSeq">The sequence number now.</param>
    /// <param name="clearAfterSeconds">The core's clear_after_s (0 = never clear).</param>
    public static ClearDecision Decide(uint ourSeq, uint currentSeq, int clearAfterSeconds)
    {
        if (clearAfterSeconds <= 0) return ClearDecision.SkipDisabled;
        return DecideOnLock(ourSeq, currentSeq);
    }

    /// <summary>On lock the secret goes whatever the timer says, but still only if it is ours.</summary>
    public static ClearDecision DecideOnLock(uint ourSeq, uint currentSeq)
    {
        if (ourSeq == 0 || currentSeq == 0) return ClearDecision.SkipUnknown; // cannot prove it is ours: leave the user's data alone
        return ourSeq == currentSeq ? ClearDecision.Clear : ClearDecision.SkipChanged;
    }
}

/// <summary>Remembers the last secret we put on the clipboard (only its sequence number and deadline, never the text).</summary>
public sealed class ClipboardClearGuard
{
    private uint _seq;
    private int _afterSeconds;

    public bool Armed => _seq != 0;
    public uint Sequence => _seq;
    public int AfterSeconds => _afterSeconds;

    public void Arm(uint sequenceAfterWrite, int clearAfterSeconds)
    {
        _seq = sequenceAfterWrite;
        _afterSeconds = clearAfterSeconds;
    }

    public void Disarm()
    {
        _seq = 0;
        _afterSeconds = 0;
    }

    /// <summary>The timer fired.</summary>
    public ClearDecision OnTimer(uint currentSeq) => _seq == 0 ? ClearDecision.SkipUnknown : ClipboardClearPolicy.Decide(_seq, currentSeq, _afterSeconds);

    /// <summary>The vault was locked.</summary>
    public ClearDecision OnLock(uint currentSeq) => _seq == 0 ? ClearDecision.SkipUnknown : ClipboardClearPolicy.DecideOnLock(_seq, currentSeq);
}

public enum LockTrigger { SessionLock, SessionLogoff, UserSwitch, RemoteDisconnect, Suspend, SessionUnlock, Resume, Other }

public static class LockPolicy
{
    /// <summary>Locking happens when the workstation locks, the user logs off or switches away, or the PC suspends.</summary>
    public static bool ShouldLock(LockTrigger t) =>
        t is LockTrigger.SessionLock or LockTrigger.SessionLogoff or LockTrigger.UserSwitch or LockTrigger.RemoteDisconnect or LockTrigger.Suspend;
}

public static class BackupLabels
{
    public static string Reason(string reason) => reason switch
    {
        "save" => Strings.ReasonSave,
        "delete" => Strings.ReasonDelete,
        "import" => Strings.ReasonImport,
        "restore" => Strings.ReasonRestore,
        "rekey" => Strings.ReasonRekey,
        "merge" => Strings.ReasonMerge,
        "create" => Strings.ReasonCreate,
        "upgrade" => Strings.ReasonUpgrade,
        "manual" => Strings.ReasonManual,
        "previous" => Strings.ReasonPrevious,
        "export" => Strings.ReasonExport,
        "" => Strings.Dash,
        _ => reason,
    };
}

/// <summary>A backup list row.</summary>
public sealed record BackupRow(BackupInfo Info, string TimeText, string ReasonText, string SizeText)
{
    public string Name => Info.Name;

    public static BackupRow From(BackupInfo b, DateTimeOffset now, TimeZoneInfo? zone = null)
    {
        zone ??= TimeZoneInfo.Local;
        var time = b.Time > 0
            ? TimeZoneInfo.ConvertTime(DateTimeOffset.FromUnixTimeSeconds(b.Time), zone).ToString("yyyy-MM-dd HH:mm", System.Globalization.CultureInfo.InvariantCulture)
            : Strings.Dash;
        return new BackupRow(b, time, BackupLabels.Reason(b.Reason), Formatting.Bytes(b.Size));
    }
}

public enum RestoreMode { Entries, Merge, Replace }

public static class RestoreModes
{
    public static string Wire(RestoreMode m) => m switch { RestoreMode.Merge => "merge", RestoreMode.Replace => "replace", _ => "entries" };

    public static string Label(RestoreMode m) => m switch
    {
        RestoreMode.Merge => Strings.RestoreModeMerge,
        RestoreMode.Replace => Strings.RestoreModeReplace,
        _ => Strings.RestoreModeEntries,
    };

    public static string Describe(RestoreMode m) => m switch
    {
        RestoreMode.Merge => Strings.RestoreMergeHint,
        RestoreMode.Replace => Strings.RestoreReplaceHint,
        _ => Strings.RestoreEntriesHint,
    };

    /// <summary>A full replace needs the user to type this word.</summary>
    public static bool ReplaceConfirmed(string typed) => string.Equals(typed.Trim(), Strings.RestoreReplaceWord, StringComparison.Ordinal);
}

public enum DiffSection { OnlyInBackup, OnlyInCurrent, Changed }

public sealed record DiffGroup(DiffSection Section, string Title, string Hint, IReadOnlyList<DiffItem> Items, bool Selectable);

/// <summary>The restore preview, grouped for display, plus the selection for "entries" mode.</summary>
public sealed class RestoreSelection
{
    private readonly HashSet<string> _selected = new(StringComparer.Ordinal);

    public RestorePreview Preview { get; }

    public RestoreSelection(RestorePreview preview)
    {
        Preview = preview;
        // Sensible default for "entries": everything that could come back or that differs.
        foreach (var i in preview.OnlyInBackup) _selected.Add(i.Id);
        foreach (var i in preview.Changed) _selected.Add(i.Id);
    }

    public IReadOnlyList<DiffGroup> Groups()
    {
        var groups = new List<DiffGroup>();
        if (Preview.OnlyInBackup.Count > 0)
            groups.Add(new(DiffSection.OnlyInBackup, Strings.DiffOnlyInBackup, Strings.DiffOnlyInBackupHint, Preview.OnlyInBackup.OrderBy(i => i.Title, StringComparer.CurrentCultureIgnoreCase).ToList(), true));
        if (Preview.Changed.Count > 0)
            groups.Add(new(DiffSection.Changed, Strings.DiffChanged, Strings.DiffChangedHint, Preview.Changed.OrderBy(i => i.Title, StringComparer.CurrentCultureIgnoreCase).ToList(), true));
        if (Preview.OnlyInCurrent.Count > 0)
            groups.Add(new(DiffSection.OnlyInCurrent, Strings.DiffOnlyInCurrent, Strings.DiffOnlyInCurrentHint, Preview.OnlyInCurrent.OrderBy(i => i.Title, StringComparer.CurrentCultureIgnoreCase).ToList(), false));
        return groups;
    }

    public string Summary => Strings.DiffSummary(Preview.OnlyInBackup.Count, Preview.Changed.Count, Preview.OnlyInCurrent.Count, Preview.Same);

    public bool IsEmpty => Preview.OnlyInBackup.Count + Preview.Changed.Count + Preview.OnlyInCurrent.Count == 0;

    private bool Restorable(string id) => Preview.OnlyInBackup.Any(i => i.Id == id) || Preview.Changed.Any(i => i.Id == id);

    public bool IsSelected(string id) => _selected.Contains(id);

    public void Set(string id, bool selected)
    {
        if (!Restorable(id)) return; // entries that only exist in the current vault cannot be "restored"
        if (selected) _selected.Add(id); else _selected.Remove(id);
    }

    public void SetAll(DiffSection section, bool selected)
    {
        var items = section switch { DiffSection.OnlyInBackup => Preview.OnlyInBackup, DiffSection.Changed => Preview.Changed, _ => Array.Empty<DiffItem>() };
        foreach (var i in items) Set(i.Id, selected);
    }

    public IReadOnlyList<string> SelectedIds => _selected.Where(Restorable).OrderBy(x => x, StringComparer.Ordinal).ToList();

    /// <summary>Entries mode needs at least one tick; merge and replace restore the whole backup.</summary>
    public bool CanRestore(RestoreMode mode) => mode != RestoreMode.Entries || SelectedIds.Count > 0;
}

/// <summary>Result text of a restore / import.</summary>
public static class StatsText
{
    public static string Describe(MergeStats s) =>
        Strings.StatsLine(s.EntriesAdded, s.EntriesUpdated, s.EntriesRemoved, s.EntriesMoved, s.GroupsAdded, s.GroupsRemoved);
}

public enum HealthSection { Weak, Reused, Old, Expired }

public sealed record HealthGroup(HealthSection Section, string Title, string Hint, IReadOnlyList<HealthRow> Items);

public sealed record HealthRow(string Id, string Title, string Info);

public static class HealthView
{
    /// <summary>Only sections that have entries; empty means "all good".</summary>
    public static IReadOnlyList<HealthGroup> Groups(HealthReport r)
    {
        var list = new List<HealthGroup>();
        void Add(HealthSection s, string title, string hint, IReadOnlyList<HealthIssue> items, Func<string, string> info)
        {
            if (items.Count > 0)
                list.Add(new HealthGroup(s, title, hint, items.Select(i => new HealthRow(i.Id, i.Title.Length > 0 ? i.Title : Strings.Untitled, info(i.Info))).ToList()));
        }
        Add(HealthSection.Weak, Strings.HealthWeak, Strings.HealthWeakHint, r.Weak, i => StrengthDisplay.Text(i));
        Add(HealthSection.Reused, Strings.HealthReused, Strings.HealthReusedHint, r.Reused, i => int.TryParse(i, out var n) ? Strings.HealthSharedBy(n) : "");
        Add(HealthSection.Old, Strings.HealthOld, Strings.HealthOldHint, r.Old, _ => "");
        Add(HealthSection.Expired, Strings.HealthExpired, Strings.HealthExpiredHint, r.Expired, _ => "");
        return list;
    }

    public static string Summary(HealthReport r) =>
        r.IssueCount == 0 ? Strings.HealthAllGood(r.Total) : Strings.HealthSummary(r.Total, r.Weak.Count, r.Reused.Count, r.Old.Count, r.Expired.Count);
}
