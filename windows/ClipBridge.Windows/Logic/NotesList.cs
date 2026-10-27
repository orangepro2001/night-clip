namespace ClipBridge.Windows.Logic;

/// <summary>Times of notes (the core counts in milliseconds) as the list and the sync line show them.</summary>
public static class NotesTime
{
    /// <summary>"刚刚" / "5 分钟前" / "3 小时前" / "2 天前", and the date after a week. Empty for an unset time.</summary>
    public static string Relative(long unixMs, DateTimeOffset now, TimeZoneInfo? zone = null)
    {
        if (unixMs <= 0) return "";
        var ago = now.ToUnixTimeMilliseconds() - unixMs;
        if (ago < 60_000) return Strings.LastSeenNow; // also a time slightly in the future (clocks of two devices differ)
        if (ago < 3_600_000) return Strings.LastSeenMinutes((int)(ago / 60_000));
        if (ago < 86_400_000) return Strings.LastSeenHours((int)(ago / 3_600_000));
        if (ago < 7 * 86_400_000L) return Strings.LastSeenDays((int)(ago / 86_400_000));
        return StampText.DateOnly(unixMs / 1000, zone);
    }

    /// <summary>"2026-10-26 22:00" in local time, for revisions.</summary>
    public static string Stamp(long unixMs, TimeZoneInfo? zone = null) => StampText.Local(unixMs / 1000, zone);
}

/// <summary>One row of the note list: what it shows, no text of the note beyond its preview line.</summary>
public sealed class NoteRow
{
    public NoteRow(NoteListItem item, DateTimeOffset now)
    {
        Item = item;
        NoteId = item.NoteId;
        Title = item.Title.Length > 0 ? item.Title : Strings.Untitled;
        Preview = item.Preview;
        TimeText = NotesTime.Relative(item.UpdatedAt, now);
        ConflictBadge = item.IsConflictCopy ? Strings.NoteConflictBadge : "";
        TitleSegments = CodePointText.Plain(Title);
        PreviewSegments = CodePointText.Plain(Preview);
    }

    /// <summary>A hit of the search: the title and the snippet carry the words that were found as highlighted pieces.</summary>
    public NoteRow(NoteSearchHit hit, DateTimeOffset now)
    {
        Item = new NoteListItem(hit.NoteId, hit.Title, hit.UpdatedAt, false, hit.Snippet, hit.ConflictOf);
        NoteId = hit.NoteId;
        Title = hit.Title.Length > 0 ? hit.Title : Strings.Untitled;
        Preview = hit.Snippet;
        TimeText = NotesTime.Relative(hit.UpdatedAt, now);
        ConflictBadge = hit.IsConflictCopy ? Strings.NoteConflictBadge : "";
        TitleSegments = hit.Title.Length > 0 ? CodePointText.Segments(hit.Title, hit.TitleHighlights) : CodePointText.Plain(Title);
        PreviewSegments = CodePointText.Segments(hit.Snippet, hit.Highlights);
    }

    public NoteListItem Item { get; }
    public string NoteId { get; }
    public string Title { get; }
    public string Preview { get; }
    public string TimeText { get; }
    public string ConflictBadge { get; }
    public IReadOnlyList<TextSegment> TitleSegments { get; }
    public IReadOnlyList<TextSegment> PreviewSegments { get; }
    public bool HasPreview => Preview.Length > 0;
    public bool HasBadge => ConflictBadge.Length > 0;

    public static IReadOnlyList<NoteRow> From(IEnumerable<NoteListItem> items, DateTimeOffset now) =>
        items.Select(i => new NoteRow(i, now)).ToList();

    public static IReadOnlyList<NoteRow> From(IEnumerable<NoteSearchHit> hits, DateTimeOffset now) =>
        hits.Select(h => new NoteRow(h, now)).ToList();
}

/// <summary>
/// The notes the list shows: the live notes or the recycle bin, newest first, loaded page by page with the core's
/// cursor. Its methods block (they call the core) and are safe to call from worker threads one after another; the
/// UI thread only reads <see cref="Items"/>, which is replaced as a whole and never changed in place.
/// </summary>
public sealed class NotesList
{
    public const int PageSize = 50;

    /// <summary>A refresh never reloads more than this many pages, however far the user scrolled.</summary>
    public const int MaxRefreshPages = 10;

    private readonly object _gate = new();
    private volatile IReadOnlyList<NoteListItem> _items = Array.Empty<NoteListItem>();
    private volatile string _next = "";
    private volatile bool _trash;

    public bool Trash => _trash;
    public IReadOnlyList<NoteListItem> Items => _items;
    public bool HasMore => _next.Length > 0;

    public bool Contains(string noteId) => _items.Any(i => i.NoteId == noteId);

    /// <summary>Switches between live notes and the recycle bin and loads the first page of the chosen one.</summary>
    public void Show(NotesApi api, bool trash)
    {
        lock (_gate)
        {
            var page = api.List(trash, PageSize);
            _trash = trash;
            _items = Dedupe(page.Items);
            _next = page.NextCursor;
        }
    }

    /// <summary>Loads the next page and appends it. False when there is nothing more.</summary>
    public bool LoadMore(NotesApi api)
    {
        lock (_gate)
        {
            if (_next.Length == 0) return false;
            var page = api.List(_trash, PageSize, _next);
            var known = _items.Select(i => i.NoteId).ToHashSet();
            var added = page.Items.Where(i => known.Add(i.NoteId)).ToList();
            _items = _items.Concat(added).ToList();
            _next = page.NextCursor;
            return added.Count > 0;
        }
    }

    /// <summary>
    /// Brings the list up to date after <c>notes.changed</c> events. A change moves a note to the top (or out of the
    /// view when it was deleted or restored), so the first page is always fetched again; pages the user scrolled to are
    /// fetched again too (at most <see cref="MaxRefreshPages"/>) so that the cursor stays consistent. Nothing else is read.
    /// </summary>
    public void Refresh(NotesApi api)
    {
        lock (_gate)
        {
            var pages = Math.Clamp((_items.Count + PageSize - 1) / PageSize, 1, MaxRefreshPages);
            var all = new List<NoteListItem>();
            var cursor = "";
            for (var i = 0; i < pages; i++)
            {
                var page = api.List(_trash, PageSize, cursor);
                all.AddRange(page.Items);
                cursor = page.NextCursor;
                if (cursor.Length == 0) break;
            }
            _items = Dedupe(all);
            _next = cursor;
        }
    }

    private static IReadOnlyList<NoteListItem> Dedupe(IEnumerable<NoteListItem> items)
    {
        var seen = new HashSet<string>();
        return items.Where(i => seen.Add(i.NoteId)).ToList();
    }
}
