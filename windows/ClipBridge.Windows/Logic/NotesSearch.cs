namespace ClipBridge.Windows.Logic;

/// <summary>A stretch of text, shown either plain or emphasised.</summary>
public sealed record TextSegment(string Text, bool Highlighted);

/// <summary>
/// notes.search counts highlight positions in Unicode CODE POINTS (an emoji is one), .NET strings in UTF-16 units (an emoji is
/// two). These functions convert, so that "😀 hello" with a hit on "hello" (code points 2..7) highlights the right letters.
/// Ranges that make no sense (negative, empty, backwards, beyond the end) are dropped or clipped, never thrown over.
/// </summary>
public static class CodePointText
{
    /// <summary>The number of code points; a lone surrogate counts as one.</summary>
    public static int Count(string text)
    {
        var n = 0;
        for (var i = 0; i < text.Length; i++)
        {
            if (char.IsHighSurrogate(text[i]) && i + 1 < text.Length && char.IsLowSurrogate(text[i + 1])) i++;
            n++;
        }
        return n;
    }

    /// <summary>UTF-16 index of every code point plus the end: <c>index[k]</c> is where code point <c>k</c> starts.</summary>
    private static int[] Starts(string text)
    {
        var starts = new List<int>(text.Length + 1);
        for (var i = 0; i < text.Length; i++)
        {
            starts.Add(i);
            if (char.IsHighSurrogate(text[i]) && i + 1 < text.Length && char.IsLowSurrogate(text[i + 1])) i++;
        }
        starts.Add(text.Length);
        return starts.ToArray();
    }

    /// <summary>
    /// The ranges as UTF-16 [start, end) pairs of <paramref name="text"/>: sorted, clipped to the text, empty ones removed,
    /// overlapping or touching ones merged.
    /// </summary>
    public static IReadOnlyList<(int Start, int End)> ToUtf16(string text, IEnumerable<CodePointRange> ranges)
    {
        var starts = Starts(text);
        var last = starts.Length - 1; // the number of code points
        var spans = new List<(int Start, int End)>();
        foreach (var r in ranges)
        {
            var from = Math.Clamp(r.Start, 0, last);
            var to = Math.Clamp(r.End, 0, last);
            if (to <= from) continue;
            spans.Add((starts[from], starts[to]));
        }
        spans.Sort();
        var merged = new List<(int Start, int End)>();
        foreach (var s in spans)
        {
            if (merged.Count > 0 && s.Start <= merged[^1].End) merged[^1] = (merged[^1].Start, Math.Max(merged[^1].End, s.End));
            else merged.Add(s);
        }
        return merged;
    }

    /// <summary>The text cut into pieces that are highlighted or not, in order, without empty pieces. No ranges: the whole text as one plain piece.</summary>
    public static IReadOnlyList<TextSegment> Segments(string text, IEnumerable<CodePointRange> ranges)
    {
        var result = new List<TextSegment>();
        var at = 0;
        foreach (var (start, end) in ToUtf16(text, ranges))
        {
            if (start > at) result.Add(new TextSegment(text[at..start], false));
            result.Add(new TextSegment(text[start..end], true));
            at = end;
        }
        if (at < text.Length) result.Add(new TextSegment(text[at..], false));
        return result;
    }

    public static IReadOnlyList<TextSegment> Plain(string text) =>
        text.Length == 0 ? Array.Empty<TextSegment>() : new[] { new TextSegment(text, false) };
}

/// <summary>
/// The hits shown while a query is typed in the search box: the first page replaces them, "load more" appends the next
/// one. Answers are tied to the query they were asked for, so a late answer of an older query is ignored.
/// </summary>
public sealed class NoteSearchResults
{
    public const int PageSize = 30;

    private readonly List<NoteSearchHit> _items = new();

    public string Query { get; private set; } = "";
    public string NextCursor { get; private set; } = "";
    public IReadOnlyList<NoteSearchHit> Items => _items;
    public bool HasMore => NextCursor.Length > 0;

    /// <summary>Whether a query is active: an empty one means the normal list.</summary>
    public bool Active => Query.Length > 0;

    /// <summary>The query as it is sent: trimmed and cut to the length the search box allows.</summary>
    public static string Normalize(string? text) => SearchInput.Normalize(text);

    public void Clear()
    {
        Query = "";
        NextCursor = "";
        _items.Clear();
    }

    /// <summary>Starts over with the first page of <paramref name="query"/>.</summary>
    public void Replace(string query, NoteSearchPage page)
    {
        Query = query;
        _items.Clear();
        AddNew(page);
        NextCursor = page.NextCursor;
    }

    /// <summary>Adds the next page of the current query; false (and nothing changes) when the page belongs to another query.</summary>
    public bool Append(string query, NoteSearchPage page)
    {
        if (query != Query) return false;
        AddNew(page);
        NextCursor = page.NextCursor;
        return true;
    }

    private void AddNew(NoteSearchPage page)
    {
        var known = _items.Select(i => i.NoteId).ToHashSet();
        _items.AddRange(page.Items.Where(i => known.Add(i.NoteId)));
    }
}
