namespace ClipBridge.Windows.Logic;

/// <summary>The quick filters of the left column.</summary>
public enum QuickFilter { All, Logins, ApiKeys, Secrets, Notes, Expiring, Trash }

public enum ExpiryState { None, Ok, Soon, Expired }

public static class QuickFilters
{
    /// <summary>Entries expiring within this many days count as "Expiring".</summary>
    public const int SoonDays = 30;

    public static readonly IReadOnlyList<QuickFilter> All = Enum.GetValues<QuickFilter>();

    public static string Label(QuickFilter f) => f switch
    {
        QuickFilter.Logins => Strings.FilterLogins,
        QuickFilter.ApiKeys => Strings.FilterApiKeys,
        QuickFilter.Secrets => Strings.FilterSecrets,
        QuickFilter.Notes => Strings.FilterNotes,
        QuickFilter.Expiring => Strings.FilterExpiring,
        QuickFilter.Trash => Strings.FilterTrash,
        _ => Strings.FilterAll,
    };

    public static string KindOf(QuickFilter f) => f switch
    {
        QuickFilter.Logins => EntryKinds.Login,
        QuickFilter.ApiKeys => EntryKinds.ApiKey,
        QuickFilter.Secrets => EntryKinds.Secret,
        QuickFilter.Notes => EntryKinds.Note,
        _ => "",
    };

    /// <summary>
    /// The vault.list parameters. Groups are filtered on the client (a group includes its subgroups), so no
    /// group id is sent. Searching looks through the whole vault, ignoring the selected group.
    /// </summary>
    public static VaultQuery ToQuery(QuickFilter f, string search) =>
        new(Query: search.Trim(), Kind: KindOf(f), OnlyTrash: f == QuickFilter.Trash);

    public static ExpiryState Expiry(long expiresAt, DateTimeOffset now)
    {
        if (expiresAt <= 0) return ExpiryState.None;
        var t = now.ToUnixTimeSeconds();
        if (expiresAt < t) return ExpiryState.Expired;
        return expiresAt - t <= SoonDays * 86400L ? ExpiryState.Soon : ExpiryState.Ok;
    }

    /// <summary>Applies the client-side part: the group (with subgroups) and the "expiring" filter (soonest first).</summary>
    public static IReadOnlyList<EntrySummary> Apply(
        IReadOnlyList<EntrySummary> entries, QuickFilter f, ISet<string>? groupIds, string search, DateTimeOffset now)
    {
        IEnumerable<EntrySummary> q = entries;
        if (groupIds is not null && search.Trim().Length == 0 && f != QuickFilter.Trash)
            q = q.Where(e => groupIds.Contains(e.GroupId));
        if (f == QuickFilter.Expiring)
            return q.Where(e => Expiry(e.ExpiresAt, now) is ExpiryState.Soon or ExpiryState.Expired)
                .OrderBy(e => e.ExpiresAt).ThenBy(e => e.Title, StringComparer.CurrentCultureIgnoreCase).ToList();
        return q.ToList();
    }
}

/// <summary>One row of the group tree (flattened, with its depth for the indentation).</summary>
public sealed record GroupNode(string Id, string Name, int Depth, int Entries)
{
    public string Indent => new string(' ', Depth * 3);
    public string Display => Indent + Name;
}

public static class GroupTree
{
    /// <summary>
    /// The groups below the root, depth first, siblings by name; the root itself and the recycle bin are left out
    /// (the bin is the "回收站" filter). Groups whose parent is unknown are shown at the top level.
    /// </summary>
    public static IReadOnlyList<GroupNode> Flatten(IReadOnlyList<GroupInfo> groups)
    {
        var root = groups.FirstOrDefault(g => g.ParentId.Length == 0 && !g.IsTrash);
        var byParent = groups.Where(g => !g.IsTrash && g.Id != root?.Id).GroupBy(g => g.ParentId)
            .ToDictionary(x => x.Key, x => x.OrderBy(g => g.Name, StringComparer.CurrentCultureIgnoreCase).ToList());
        var known = groups.Select(g => g.Id).ToHashSet();
        var result = new List<GroupNode>();

        void Walk(string parentId, int depth, HashSet<string> visited)
        {
            if (!byParent.TryGetValue(parentId, out var kids)) return;
            foreach (var g in kids)
            {
                if (!visited.Add(g.Id)) continue; // a cycle in a damaged file must not hang the UI
                result.Add(new GroupNode(g.Id, g.Name, depth, g.Entries));
                Walk(g.Id, depth + 1, visited);
            }
        }

        var seen = new HashSet<string>();
        Walk(root?.Id ?? "", 0, seen);
        foreach (var orphanParent in byParent.Keys.Where(k => k != (root?.Id ?? "") && !known.Contains(k)))
            Walk(orphanParent, 0, seen);
        return result;
    }

    /// <summary>The group and everything below it.</summary>
    public static ISet<string> WithDescendants(IReadOnlyList<GroupInfo> groups, string id)
    {
        var set = new HashSet<string> { id };
        var grew = true;
        while (grew)
        {
            grew = false;
            foreach (var g in groups)
                if (!set.Contains(g.Id) && set.Contains(g.ParentId)) { set.Add(g.Id); grew = true; }
        }
        return set;
    }

    /// <summary>The choices of the editor's group box: the root (top level) and every group below it, indented.</summary>
    public static IReadOnlyList<GroupNode> Choices(IReadOnlyList<GroupInfo> groups)
    {
        var root = groups.FirstOrDefault(g => g.ParentId.Length == 0 && !g.IsTrash);
        var list = new List<GroupNode>();
        if (root is not null) list.Add(new GroupNode(root.Id, Strings.GroupRoot, 0, root.Entries));
        foreach (var n in Flatten(groups)) list.Add(n with { Depth = n.Depth + 1 });
        return list;
    }
}

/// <summary>One list row: what the list shows, no secrets.</summary>
public sealed class EntryRow
{
    public EntryRow(EntrySummary e, DateTimeOffset now)
    {
        Summary = e;
        Id = e.Id;
        Title = e.Title.Length > 0 ? e.Title : Strings.Untitled;
        Glyph = KindTemplate.GlyphFor(e.Kind);
        Subtitle = Describe(e);
        Url = e.Url;
        TotpBadge = e.HasTotp ? Strings.BadgeTotp : "";
        var state = QuickFilters.Expiry(e.ExpiresAt, now);
        ExpiryBadge = state switch { ExpiryState.Expired => Strings.BadgeExpired, ExpiryState.Soon => Strings.BadgeExpiring, _ => "" };
        ExpiryBrushArgb = state == ExpiryState.Expired ? 0xFFD93025u : 0xFFB36B00u;
        AttachBadge = e.HasAttachments ? "📎" : "";
    }

    public EntrySummary Summary { get; }
    public string Id { get; }
    public string Title { get; }
    public string Glyph { get; }
    public string Subtitle { get; }
    public string Url { get; }
    public string TotpBadge { get; }
    public string ExpiryBadge { get; }
    public uint ExpiryBrushArgb { get; }
    public string AttachBadge { get; }
    public bool InTrash => Summary.InTrash;

    /// <summary>The line under the title: the username, else the address (host only), else nothing.</summary>
    public static string Describe(EntrySummary e)
    {
        var parts = new List<string>();
        if (e.Username.Length > 0) parts.Add(e.Username);
        if (e.Url.Length > 0) parts.Add(UrlText.Short(e.Url));
        return string.Join("  ·  ", parts);
    }

    public static IReadOnlyList<EntryRow> From(IEnumerable<EntrySummary> entries, DateTimeOffset now) =>
        entries.Select(e => new EntryRow(e, now)).ToList();
}

public static class UrlText
{
    /// <summary>The host of a web address ("https://a.b/c" gives "a.b"); anything else is shortened as is.</summary>
    public static string Short(string url, int max = 48)
    {
        var uri = UrlSafety.TryGetWebUri(url);
        var text = uri is not null ? uri.Host : url.Trim();
        return text.Length <= max ? text : text[..(max - 1)] + "…";
    }
}

/// <summary>How the list is currently labelled: "共 N 项" or the empty-state text per filter.</summary>
public static class ListTexts
{
    public static string Count(int n) => Strings.VaultCount(n);

    public static string Empty(QuickFilter f, bool searching) =>
        searching ? Strings.VaultNoMatches : f == QuickFilter.Trash ? Strings.VaultTrashEmpty : Strings.VaultEmpty;
}
