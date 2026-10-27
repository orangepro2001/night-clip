using System.Text.Json;

namespace ClipBridge.Windows.Logic;

/// <summary>
/// Shapes of the notes.* answers (notes/api-spec.md). Every <c>*_at</c> field is Unix MILLISECONDS (the rest of the app
/// uses seconds). Parsing is as tolerant as <see cref="JsonRead"/>: a missing or mistyped field gives its default and
/// unknown fields are ignored, so a newer core never breaks an older shell.
/// </summary>
public static class NoteLimits
{
    /// <summary>The core refuses bodies over 1 MiB and titles over 1 KiB (UTF-8 bytes).</summary>
    public const int MaxBodyBytes = 1 << 20;

    public const int MaxTitleBytes = 1024;

    /// <summary>Characters the title box accepts: 300 characters are at most 900 bytes in UTF-8 (CJK takes 3).</summary>
    public const int TitleBoxChars = 300;

    public static bool IsTooLarge(string title, string body) =>
        System.Text.Encoding.UTF8.GetByteCount(title) > MaxTitleBytes || System.Text.Encoding.UTF8.GetByteCount(body) > MaxBodyBytes;
}

/// <summary>One line of <c>notes.list</c>.</summary>
public sealed record NoteListItem(string NoteId, string Title, long UpdatedAt, bool Deleted, string Preview, string ConflictOf)
{
    /// <summary>A copy the sync set aside; <see cref="ConflictOf"/> is the original note.</summary>
    public bool IsConflictCopy => ConflictOf.Length > 0;

    public static NoteListItem FromJson(JsonElement e) => new(
        JsonRead.Str(e, "note_id"), JsonRead.Str(e, "title"), JsonRead.Long(e, "updated_at"), JsonRead.Bool(e, "deleted"),
        JsonRead.Str(e, "preview"), JsonRead.Str(e, "conflict_of"));
}

/// <summary>One page of <c>notes.list</c>; <see cref="NextCursor"/> is empty on the last page.</summary>
public sealed record NotesPage(IReadOnlyList<NoteListItem> Items, string NextCursor)
{
    public bool HasMore => NextCursor.Length > 0;

    public static NotesPage FromJson(JsonElement r) => new(
        JsonRead.Arr(r, "items").Select(NoteListItem.FromJson).Where(i => i.NoteId.Length > 0).ToList(),
        JsonRead.Str(r, "next_cursor"));
}

/// <summary><c>notes.get</c>: one revision of a note with its text.</summary>
public sealed record NoteDetail(
    string NoteId, string Title, string Body, int FormatVersion, string RevId, IReadOnlyList<string> Parents,
    long CreatedAt, long UpdatedAt, bool Deleted, string OriginDevice, int RevCount, bool IsHead, string ConflictOf)
{
    public static NoteDetail FromJson(JsonElement r) => new(
        JsonRead.Str(r, "note_id"), JsonRead.Str(r, "title"), JsonRead.Str(r, "body"), (int)JsonRead.Long(r, "format_version"),
        JsonRead.Str(r, "rev_id"), JsonRead.StrArr(r, "parents"), JsonRead.Long(r, "created_at"), JsonRead.Long(r, "updated_at"),
        JsonRead.Bool(r, "deleted"), JsonRead.Str(r, "origin_device"), (int)JsonRead.Long(r, "rev_count"),
        JsonRead.Bool(r, "is_head", true), JsonRead.Str(r, "conflict_of"));
}

/// <summary><c>notes.create</c>.</summary>
public sealed record NoteCreated(string NoteId, string RevId)
{
    public static NoteCreated FromJson(JsonElement r) => new(JsonRead.Str(r, "note_id"), JsonRead.Str(r, "rev_id"));
}

/// <summary>
/// <c>notes.update</c>: <see cref="RevId"/> is the revision the note has now. <see cref="Merged"/> says the edit was
/// combined with changes made elsewhere; <see cref="ConflictNoteId"/> names the copy set aside when both sides edited the
/// same block (the newer text is the note, the older one the copy).
/// </summary>
public sealed record NoteSaveResult(string RevId, bool Merged, string ConflictNoteId)
{
    public static NoteSaveResult FromJson(JsonElement r) => new(
        JsonRead.Str(r, "rev_id"), JsonRead.Bool(r, "merged"), JsonRead.Str(r, "conflict_note_id"));
}

/// <summary>One revision of <c>notes.history</c> (no text: fetch it with <c>notes.get</c> and the revision id).</summary>
public sealed record NoteRevision(
    string RevId, string Title, string BodyHash, string OriginDevice, long CreatedAt, IReadOnlyList<string> Parents, bool Deleted)
{
    /// <summary>The origin of revisions the sync merged by itself.</summary>
    public const string MergeOrigin = "merge";

    public bool IsMerge => OriginDevice == MergeOrigin;

    public static NoteRevision FromJson(JsonElement e) => new(
        JsonRead.Str(e, "rev_id"), JsonRead.Str(e, "title"), JsonRead.Str(e, "body_hash"), JsonRead.Str(e, "origin_device"),
        JsonRead.Long(e, "created_at"), JsonRead.StrArr(e, "parents"), JsonRead.Bool(e, "deleted"));
}

public sealed record NoteRevisionPage(IReadOnlyList<NoteRevision> Items, string NextCursor)
{
    public bool HasMore => NextCursor.Length > 0;

    public static NoteRevisionPage FromJson(JsonElement r) => new(
        JsonRead.Arr(r, "items").Select(NoteRevision.FromJson).Where(i => i.RevId.Length > 0).ToList(),
        JsonRead.Str(r, "next_cursor"));
}

/// <summary><c>notes.normalize</c>: what a text becomes once stored.</summary>
public sealed record NoteNormalized(string Body, string Hash)
{
    public static NoteNormalized FromJson(JsonElement r) => new(JsonRead.Str(r, "body_norm"), JsonRead.Str(r, "body_hash"));
}

/// <summary>A conflict copy (<c>notes.sync_conflicts</c>): <see cref="NoteId"/> is the original, <see cref="ConflictNoteId"/> the copy.</summary>
public sealed record NoteConflict(string NoteId, string ConflictNoteId, string Title, long CreatedAt)
{
    public static NoteConflict FromJson(JsonElement e) => new(
        JsonRead.Str(e, "note_id"), JsonRead.Str(e, "conflict_note_id"), JsonRead.Str(e, "title"), JsonRead.Long(e, "created_at"));
}

public sealed record NoteConflictPage(IReadOnlyList<NoteConflict> Items, string NextCursor)
{
    public bool HasMore => NextCursor.Length > 0;

    public static NoteConflictPage FromJson(JsonElement r) => new(
        JsonRead.Arr(r, "items").Select(NoteConflict.FromJson).Where(i => i.NoteId.Length > 0 && i.ConflictNoteId.Length > 0).ToList(),
        JsonRead.Str(r, "next_cursor"));
}

/// <summary>The values of the <c>action</c> of <c>notes.sync_resolve</c>.</summary>
public static class ConflictActions
{
    public const string KeepMain = "keep_main";
    public const string KeepConflict = "keep_conflict";
    public const string Merge = "merge";
}

/// <summary>The states of a peer in <c>notes.sync_status</c> and <c>notes.sync.updated</c>.</summary>
public static class NotesSyncStates
{
    public const string Idle = "idle";
    public const string Syncing = "syncing";
    public const string Error = "error";

    /// <summary>The other device uses a newer note format: this app has to be updated.</summary>
    public const string PeerOld = "peer_old";
}

public sealed record NotesPeerStatus(
    string PeerId, string Name, bool Connected, string State, string Detail, long LastSyncAt, int Behind, int ConflictCount)
{
    public string DisplayName => Name.Length > 0 ? Name : PeerId;

    public static NotesPeerStatus FromJson(JsonElement e) => new(
        JsonRead.Str(e, "peer_id"), JsonRead.Str(e, "name"), JsonRead.Bool(e, "connected"), JsonRead.Str(e, "state", NotesSyncStates.Idle),
        JsonRead.Str(e, "detail"), JsonRead.Long(e, "last_sync_at"), (int)Math.Clamp(JsonRead.Long(e, "behind"), 0, int.MaxValue),
        (int)Math.Clamp(JsonRead.Long(e, "conflict_count"), 0, int.MaxValue));
}

/// <summary><c>notes.sync_status</c>.</summary>
public sealed record NotesSyncStatus(IReadOnlyList<NotesPeerStatus> Peers, int PendingPush)
{
    /// <summary>The core reports the number of unresolved conflict copies on every peer; it is one global count.</summary>
    public int ConflictCount => Peers.Count == 0 ? 0 : Peers.Max(p => p.ConflictCount);

    public bool AnyPeerOld => Peers.Any(p => p.State == NotesSyncStates.PeerOld);

    public static NotesSyncStatus FromJson(JsonElement r) => new(
        JsonRead.Arr(r, "peers").Select(NotesPeerStatus.FromJson).Where(p => p.PeerId.Length > 0).ToList(),
        (int)Math.Clamp(JsonRead.Long(r, "pending_push"), 0, int.MaxValue));
}

// ---- the render tree (notes.render, api-spec §2.1) ---------------------------------------------------------------

public enum BlockKind { Heading, Paragraph, Code, Quote, List, Table, Hr, Html, Unknown }

public enum InlineKind { Text, Em, Strong, Del, Code, Link, Image, HardBreak, SoftBreak }

public enum ColumnAlign { None, Left, Center, Right }

/// <summary>
/// One inline run. <see cref="Text"/> is the text of Text and Code runs and the alt text of an image; links and
/// emphasis carry <see cref="Children"/>.
/// </summary>
public sealed record RenderInline(InlineKind Kind, string Text, string Href, string Title, IReadOnlyList<RenderInline> Children)
{
    private static readonly IReadOnlyList<RenderInline> NoChildren = Array.Empty<RenderInline>();

    public static RenderInline Plain(string text) => new(InlineKind.Text, text, "", "", NoChildren);

    /// <summary>The text of the run without any formatting (alt text for images, a line break for breaks).</summary>
    public string PlainText() => Kind switch
    {
        InlineKind.HardBreak or InlineKind.SoftBreak => "\n",
        InlineKind.Text or InlineKind.Code or InlineKind.Image when Children.Count == 0 => Text,
        _ => string.Concat(Children.Select(c => c.PlainText())),
    };

    public static string PlainText(IEnumerable<RenderInline> runs) => string.Concat(runs.Select(r => r.PlainText()));

    internal static RenderInline FromJson(JsonElement e)
    {
        var children = ListFromJson(e, "children");
        var text = JsonRead.Str(e, "text");
        var href = JsonRead.Str(e, "href");
        var title = JsonRead.Str(e, "title");
        switch (JsonRead.Str(e, "t"))
        {
            case "text": return new RenderInline(InlineKind.Text, text, "", "", NoChildren);
            case "em": return new RenderInline(InlineKind.Em, "", "", "", children);
            case "strong": return new RenderInline(InlineKind.Strong, "", "", "", children);
            case "del": return new RenderInline(InlineKind.Del, "", "", "", children);
            case "code": return new RenderInline(InlineKind.Code, text, "", "", NoChildren);
            case "link": return new RenderInline(InlineKind.Link, "", href, title, children);
            case "image": return new RenderInline(InlineKind.Image, JsonRead.Str(e, "alt"), href, title, NoChildren);
            case "br": return new RenderInline(InlineKind.HardBreak, "", "", "", NoChildren);
            case "softbr": return new RenderInline(InlineKind.SoftBreak, "", "", "", NoChildren);
            default:
                // A kind this shell does not know: keep its words as plain text rather than dropping them.
                return text.Length > 0 ? Plain(text) : new RenderInline(InlineKind.Text, "", "", "", children);
        }
    }

    internal static IReadOnlyList<RenderInline> ListFromJson(JsonElement e, string name) =>
        JsonRead.Arr(e, name).Where(c => c.ValueKind == JsonValueKind.Object).Select(FromJson).ToList();
}

/// <summary>One item of a list: <see cref="Checked"/> is null for ordinary items and true/false for task items.</summary>
public sealed record RenderListItem(bool? Checked, IReadOnlyList<RenderBlock> Blocks);

/// <summary>
/// One block of the tree. Only the fields of its <see cref="Kind"/> are filled: headings and paragraphs carry
/// <see cref="Inline"/>, code blocks <see cref="Text"/> and <see cref="Lang"/>, quotes <see cref="Children"/>, lists
/// <see cref="Items"/>, tables <see cref="Header"/>/<see cref="Rows"/>/<see cref="Align"/>; HTML and unknown blocks keep
/// their source in <see cref="Text"/> and are only ever shown as literal text.
/// </summary>
public sealed record RenderBlock(
    string BlockId, BlockKind Kind, string Raw, int Level, IReadOnlyList<RenderInline> Inline, string Lang, string Info, string Text,
    IReadOnlyList<RenderBlock> Children, bool Ordered, int Start, bool Tight, IReadOnlyList<RenderListItem> Items,
    IReadOnlyList<ColumnAlign> Align, IReadOnlyList<IReadOnlyList<RenderInline>> Header,
    IReadOnlyList<IReadOnlyList<IReadOnlyList<RenderInline>>> Rows)
{
    /// <summary>What an HTML or unknown block shows: its original text, else its normalised Markdown.</summary>
    public string LiteralText => Text.Length > 0 ? Text : Raw;

    internal static RenderBlock FromJson(JsonElement e)
    {
        var kind = JsonRead.Str(e, "kind") switch
        {
            "heading" => BlockKind.Heading,
            "paragraph" => BlockKind.Paragraph,
            "code" => BlockKind.Code,
            "quote" => BlockKind.Quote,
            "list" => BlockKind.List,
            "table" => BlockKind.Table,
            "hr" => BlockKind.Hr,
            "html" => BlockKind.Html,
            _ => BlockKind.Unknown,
        };
        var ordered = JsonRead.Bool(e, "ordered");
        return new RenderBlock(
            JsonRead.Str(e, "block_id"), kind, JsonRead.Str(e, "raw"), (int)Math.Clamp(JsonRead.Long(e, "level", 1), 1, 6),
            RenderInline.ListFromJson(e, "inline"), JsonRead.Str(e, "lang"), JsonRead.Str(e, "info"), JsonRead.Str(e, "text"),
            ListFromJson(e, "children"), ordered, (int)Math.Clamp(JsonRead.Long(e, "start", 1), 0, int.MaxValue), JsonRead.Bool(e, "tight"),
            JsonRead.Arr(e, "items").Where(i => i.ValueKind == JsonValueKind.Object).Select(ItemFromJson).ToList(),
            JsonRead.Arr(e, "align").Select(AlignFromJson).ToList(),
            JsonRead.Arr(e, "header").Select(CellFromJson).ToList(),
            JsonRead.Arr(e, "rows").Select(r => (IReadOnlyList<IReadOnlyList<RenderInline>>)
                (r.ValueKind == JsonValueKind.Array ? r.EnumerateArray().Select(CellFromJson).ToList() : new List<IReadOnlyList<RenderInline>>())).ToList());
    }

    internal static IReadOnlyList<RenderBlock> ListFromJson(JsonElement e, string name) =>
        JsonRead.Arr(e, name).Where(b => b.ValueKind == JsonValueKind.Object).Select(FromJson).ToList();

    private static RenderListItem ItemFromJson(JsonElement e)
    {
        bool? isChecked = e.TryGetProperty("checked", out var c)
            ? c.ValueKind switch { JsonValueKind.True => true, JsonValueKind.False => false, _ => null }
            : null;
        return new RenderListItem(isChecked, ListFromJson(e, "blocks"));
    }

    private static ColumnAlign AlignFromJson(JsonElement e) =>
        e.ValueKind == JsonValueKind.String
            ? e.GetString() switch { "left" => ColumnAlign.Left, "center" => ColumnAlign.Center, "right" => ColumnAlign.Right, _ => ColumnAlign.None }
            : ColumnAlign.None;

    private static IReadOnlyList<RenderInline> CellFromJson(JsonElement cell) =>
        cell.ValueKind == JsonValueKind.Array
            ? cell.EnumerateArray().Where(c => c.ValueKind == JsonValueKind.Object).Select(RenderInline.FromJson).ToList()
            : Array.Empty<RenderInline>();
}

public sealed record TocEntry(string BlockId, int Level, string Text);

/// <summary><c>notes.render</c>: the block tree the preview draws. The shell never parses Markdown itself.</summary>
public sealed record RenderTree(string NoteId, string RevId, IReadOnlyList<RenderBlock> Blocks, IReadOnlyList<TocEntry> Toc)
{
    public static RenderTree FromJson(JsonElement r) => new(
        JsonRead.Str(r, "note_id"), JsonRead.Str(r, "rev_id"), RenderBlock.ListFromJson(r, "blocks"),
        JsonRead.Arr(r, "toc").Select(t => new TocEntry(JsonRead.Str(t, "block_id"), (int)JsonRead.Long(t, "level", 1), JsonRead.Str(t, "text"))).ToList());
}

/// <summary>The outcome of a notes call made through the shell: a value, or the error code and its friendly text.</summary>
public sealed record NotesResult<T>(T? Value, string? ErrorCode, string? Error)
{
    public bool Ok => Error is null;

    public static NotesResult<T> Success(T value) => new(value, null, null);

    public static NotesResult<T> Fail(string code) => new(default, code, ErrorMessages.ForNotes(code));
}
