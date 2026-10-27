namespace ClipBridge.Windows.Logic;

/// <summary>Which panes of the editor show: only the Markdown source, source and preview side by side, or only the preview.</summary>
public enum NotesViewMode { Source, Split, Preview }

public static class NotesViewModes
{
    public const NotesViewMode Default = NotesViewMode.Split;

    /// <summary>The value stored in shell.json.</summary>
    public static string Key(NotesViewMode mode) => mode switch
    {
        NotesViewMode.Source => "source",
        NotesViewMode.Preview => "preview",
        _ => "split",
    };

    /// <summary>Anything unknown (an empty file, a value of a newer version) gives the default.</summary>
    public static NotesViewMode Parse(string? key) => key switch
    {
        "source" => NotesViewMode.Source,
        "preview" => NotesViewMode.Preview,
        "split" => NotesViewMode.Split,
        _ => Default,
    };
}

/// <summary>Opening links of a note: only http, https and mailto ever reach the system. Notes come from other devices, so nothing else may run.</summary>
public static class NoteLinks
{
    /// <summary>The address to open, or null when the link is not safe to open (any other scheme, control characters, no host).</summary>
    public static Uri? TryGetOpenableUri(string? href)
    {
        var web = UrlSafety.TryGetWebUri(href); // http / https with a host, no control characters
        if (web is not null) return web;
        var t = href?.Trim();
        if (string.IsNullOrEmpty(t) || t.Any(char.IsControl) || !t.StartsWith("mailto:", StringComparison.OrdinalIgnoreCase)) return null;
        if (!Uri.TryCreate(t, UriKind.Absolute, out var uri) || uri.Scheme != Uri.UriSchemeMailto) return null;
        return uri.UserInfo.Length > 0 && uri.Host.Length > 0 ? uri : null;
    }

    public static bool IsOpenable(string? href) => TryGetOpenableUri(href) is not null;
}

/// <summary>The sync line under the note list: one phrase per paired device, from <c>notes.sync_status</c>.</summary>
public static class NotesSyncText
{
    public static string Peer(NotesPeerStatus p, DateTimeOffset now)
    {
        var name = p.DisplayName;
        switch (p.State)
        {
            case NotesSyncStates.PeerOld:
                return Strings.NotesSyncPeerOld(name);
            case NotesSyncStates.Error:
                return Strings.NotesSyncError(name);
            case NotesSyncStates.Syncing:
                return p.Behind > 0 ? Strings.NotesSyncSyncingBehind(name, p.Behind) : Strings.NotesSyncSyncing(name);
        }
        var when = NotesTime.Relative(p.LastSyncAt, now);
        string text;
        if (p.LastSyncAt <= 0) text = p.Connected ? Strings.NotesSyncNever(name) : Strings.NotesSyncOfflineNever(name);
        else text = p.Connected ? Strings.NotesSyncDone(name, when) : Strings.NotesSyncOffline(name, when);
        return p.Behind > 0 ? text + Strings.NotesSyncBehindSuffix(p.Behind) : text;
    }

    /// <summary>The one line: every device, then what waits to be sent.</summary>
    public static string Line(NotesSyncStatus status, DateTimeOffset now)
    {
        if (status.Peers.Count == 0) return Strings.NotesSyncNoPeers;
        var parts = status.Peers.Select(p => Peer(p, now)).ToList();
        if (status.PendingPush > 0) parts.Add(Strings.NotesSyncPending(status.PendingPush));
        return string.Join("  ·  ", parts);
    }

    /// <summary>The tooltip: one line per device, with the reason the core gives for a failure.</summary>
    public static string Details(NotesSyncStatus status, DateTimeOffset now) =>
        string.Join("\n", status.Peers.Select(p => p.Detail.Length > 0 ? Peer(p, now) + " (" + p.Detail + ")" : Peer(p, now)));

    /// <summary>The text for a <c>notes.sync.conflict</c> event whose copy was set aside or whose note was deleted elsewhere.</summary>
    public static string ConflictNotice(NotesSyncConflictEvent e, string noteTitle)
    {
        var title = noteTitle.Length > 0 ? noteTitle : Strings.Untitled;
        return e.Reason == NotesSyncConflictEvent.DeletedElsewhere ? Strings.NoteDeletedElsewhere(title) : Strings.NoteSyncConflictCopy(title);
    }
}

/// <summary>The sizes and glyphs the preview uses for the blocks of the render tree (the shell decides how it looks, not what it means).</summary>
public static class NotePreviewStyle
{
    public const double BodySize = 13;

    public static double HeadingSize(int level) => level switch { 1 => 24, 2 => 20, 3 => 17, 4 => 15, _ => 13 };

    /// <summary>
    /// Both kinds of line break are shown as a line break: a hard one (two spaces or a backslash at the end of a line) and a soft
    /// one (the plain Enter of the source). That is the note-taking convention (Obsidian's default) and what the Android client does.
    /// </summary>
    public static bool IsLineBreak(InlineKind kind) => kind is InlineKind.HardBreak or InlineKind.SoftBreak;

    public static string BulletGlyph(int depth) => (depth % 3) switch { 0 => "•", 1 => "◦", _ => "▪" };

    /// <summary>The marker in front of list item <paramref name="index"/> (0-based): a checkbox for task items, a number or a bullet.</summary>
    public static string Marker(RenderBlock list, int index, int depth)
    {
        var item = list.Items[index];
        if (item.Checked is { } done) return done ? "☑" : "☐";
        return list.Ordered ? (list.Start + index).ToString(System.Globalization.CultureInfo.InvariantCulture) + "." : BulletGlyph(depth);
    }

    public static int ColumnCount(RenderBlock table) =>
        Math.Max(table.Header.Count, table.Rows.Count == 0 ? 0 : table.Rows.Max(r => r.Count));

    public static ColumnAlign AlignOf(RenderBlock table, int column) =>
        column >= 0 && column < table.Align.Count ? table.Align[column] : ColumnAlign.None;
}

/// <summary>
/// The text box counts positions in its own way. If a line break ("\r\n" in <c>Text</c>) is one position for
/// <c>SelectionStart</c> and <c>Select</c> (the shell probes this once), indexes have to be converted; otherwise they are
/// the same numbers.
/// </summary>
public static class TextOffsets
{
    /// <summary>Index into the text to position in the box, when every "\r\n" counts as one.</summary>
    public static int ToBox(string text, int index)
    {
        var shift = 0;
        for (var i = 0; i < index && i < text.Length; i++)
        {
            if (text[i] == '\r' && i + 1 < index && i + 1 < text.Length && text[i + 1] == '\n')
            {
                shift++;
                i++;
            }
        }
        return index - shift;
    }

    /// <summary>Position in the box to index into the text, when every "\r\n" counts as one.</summary>
    public static int FromBox(string text, int position)
    {
        var i = 0;
        for (var pos = 0; pos < position && i < text.Length; pos++)
            i += text[i] == '\r' && i + 1 < text.Length && text[i + 1] == '\n' ? 2 : 1;
        return i;
    }
}
