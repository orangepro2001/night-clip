using System.Diagnostics;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

/// <summary>
/// Drives the REAL core's notes.* methods through CoreHost / NotesApi / NoteEditorSession, the same path the WPF shell uses.
/// It proves the shell reads what the core really sends (field names, milliseconds, the render tree, the merge answers).
/// Nothing here needs a Windows desktop.
/// </summary>
[Collection(CoreCollection.Name)]
public class NotesInteropTests
{
    private readonly NativeCoreFixture _fx;
    private readonly NotesApi _api;
    private readonly int _eventsBefore;

    public NotesInteropTests(NativeCoreFixture fx)
    {
        _fx = fx;
        _api = new NotesApi(fx.Core);
        lock (fx.RawEvents) _eventsBefore = fx.RawEvents.Count;
    }

    private IEnumerable<CoreEvent> NewEvents()
    {
        lock (_fx.RawEvents) return _fx.RawEvents.Skip(_eventsBefore).Select(CoreEventParser.Parse).ToList();
    }

    private bool WaitForEvent(Func<CoreEvent, bool> match, int ms = 5000)
    {
        var sw = Stopwatch.StartNew();
        while (sw.ElapsedMilliseconds < ms)
        {
            if (NewEvents().Any(match)) return true;
            Thread.Sleep(20);
        }
        return false;
    }

    private static string Code(Action a) => Assert.Throws<CoreException>(a).Code;

    [Fact]
    public void CreateGetUpdateList_RoundTripTheTextAndTheMilliseconds()
    {
        var before = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        var created = _api.Create("周末计划", "买菜 🥬\n\n写周报");
        Assert.Matches("^nt_[a-z2-7]{26}$", created.NoteId);
        Assert.Matches("^r_[0-9a-f]{32}$", created.RevId);

        var note = _api.Get(created.NoteId);
        Assert.Equal("周末计划", note.Title);
        Assert.Equal("买菜 🥬\n\n写周报\n", note.Body);                 // stored in canonical form: one trailing line break
        Assert.True(note.IsHead);
        Assert.InRange(note.CreatedAt, before - 1000, DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() + 1000);   // milliseconds, not seconds
        Assert.Equal(created.RevId, note.RevId);

        var saved = _api.Update(created.NoteId, created.RevId, null, "买菜 🥬\n\n写周报\n\n- 洗衣服\n");
        Assert.False(saved.Merged);
        Assert.Equal("", saved.ConflictNoteId);
        Assert.NotEqual(created.RevId, saved.RevId);
        Assert.Equal(new[] { created.RevId }, _api.Get(created.NoteId, saved.RevId).Parents);

        var listed = _api.List().Items.Single(i => i.NoteId == created.NoteId);
        Assert.Equal("周末计划", listed.Title);
        Assert.InRange(listed.UpdatedAt, before - 1000, DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() + 1000);
        Assert.False(listed.Deleted);
        Assert.NotEqual("", listed.Preview);
    }

    [Fact]
    public void DeleteRestore_MoveBetweenTheListAndTheTrash_AndEditingATrashedNoteIsRefused()
    {
        var id = _api.Create("to trash", "text").NoteId;
        _api.Delete(id);

        Assert.DoesNotContain(_api.List().Items, i => i.NoteId == id);
        var trashed = Assert.Single(_api.List(deleted: true).Items, i => i.NoteId == id);
        Assert.True(trashed.Deleted);
        Assert.True(_api.Get(id).Deleted);
        Assert.Equal("CONFLICT", Code(() => _api.Update(id, "", "x", null)));
        Assert.Equal(Strings.ErrNoteInTrash, ErrorMessages.ForNotes("CONFLICT"));

        _api.Restore(id);
        Assert.Contains(_api.List().Items, i => i.NoteId == id);
        Assert.False(_api.Get(id).Deleted);
    }

    [Fact]
    public void Errors_CarryTheirCodes()
    {
        Assert.Equal("NOT_FOUND", Code(() => _api.Get("nt_" + new string('a', 26))));
        Assert.Equal("INVALID", Code(() => _api.Get("garbage")));
        Assert.Equal("INVALID", Code(() => _api.Search("")));                 // search exists now; an empty query is the one thing it refuses
        var id = _api.Create("big", "").NoteId;
        Assert.Equal("TOO_LARGE", Code(() => _api.Update(id, "", null, new string('x', NoteLimits.MaxBodyBytes + 10))));
        Assert.Equal("TOO_LARGE", Code(() => _api.Update(id, "", new string('t', NoteLimits.MaxTitleBytes + 1), null)));
        Assert.Equal("NOT_PAIRED", Code(() => _api.SyncNow("dev_" + new string('a', 26))));
    }

    [Fact]
    public void Render_ReturnsTheBlockTree_TheShellDraws()
    {
        const string body = "# 标题\n\nHello **bold** *it* ~~del~~ `code` [site](https://example.com) ![pic](https://example.com/p.png)\n\n" +
            "> quote\n\n- a\n  - nested\n- [x] done\n\n1. one\n2. two\n\n```go\nfmt.Println(1)\n```\n\n" +
            "| A | B |\n| :-- | --: |\n| 1 | 2 |\n\n---\n\n<div onclick=\"x()\">raw</div>\n";
        var id = _api.Create("render", body).NoteId;

        var tree = _api.Render(id);
        Assert.Equal(id, tree.NoteId);
        var kinds = tree.Blocks.Select(b => b.Kind).ToList();
        Assert.Contains(BlockKind.Heading, kinds);
        Assert.Contains(BlockKind.Paragraph, kinds);
        Assert.Contains(BlockKind.Quote, kinds);
        Assert.Contains(BlockKind.List, kinds);
        Assert.Contains(BlockKind.Code, kinds);
        Assert.Contains(BlockKind.Table, kinds);
        Assert.Contains(BlockKind.Hr, kinds);
        Assert.Contains(BlockKind.Html, kinds);                        // inline-level HTML stays a literal block

        var heading = tree.Blocks.First(b => b.Kind == BlockKind.Heading);
        Assert.Equal(1, heading.Level);
        Assert.Equal("标题", RenderInline.PlainText(heading.Inline));
        Assert.Matches("^b_[0-9a-f]{16}$", heading.BlockId);

        var para = tree.Blocks.First(b => b.Kind == BlockKind.Paragraph).Inline;
        Assert.Contains(para, r => r.Kind == InlineKind.Strong && r.PlainText() == "bold");
        Assert.Contains(para, r => r.Kind == InlineKind.Em);
        Assert.Contains(para, r => r.Kind == InlineKind.Del);
        Assert.Contains(para, r => r.Kind == InlineKind.Code && r.Text == "code");
        var link = para.First(r => r.Kind == InlineKind.Link);
        Assert.Equal(("https://example.com", "site"), (link.Href, link.PlainText()));
        Assert.True(NoteLinks.IsOpenable(link.Href));
        var image = para.First(r => r.Kind == InlineKind.Image);
        Assert.Equal(("https://example.com/p.png", "pic"), (image.Href, image.Text));

        var lists = tree.Blocks.Where(b => b.Kind == BlockKind.List).ToList();
        Assert.Contains(lists, l => l.Items.Any(i => i.Checked == true));                      // the task item
        Assert.Contains(lists, l => l.Items.Any(i => i.Blocks.Any(b => b.Kind == BlockKind.List)));   // nesting
        Assert.Contains(lists, l => l.Ordered);

        var code = tree.Blocks.First(b => b.Kind == BlockKind.Code);
        Assert.Equal(("go", "fmt.Println(1)"), (code.Lang, code.Text));

        var table = tree.Blocks.First(b => b.Kind == BlockKind.Table);
        Assert.Equal(new[] { ColumnAlign.Left, ColumnAlign.Right }, table.Align.Take(2).ToArray());
        Assert.Equal(2, table.Header.Count);
        Assert.Single(table.Rows);

        var html = tree.Blocks.First(b => b.Kind == BlockKind.Html);
        Assert.Contains("onclick", html.LiteralText);                  // kept as text, never interpreted
        Assert.NotEmpty(tree.Toc);
    }

    [Fact]
    public void Render_OfAnOlderRevision_ShowsThatRevision()
    {
        var created = _api.Create("v", "first version");
        var second = _api.Update(created.NoteId, created.RevId, null, "second version");
        Assert.Contains("first", RenderInline.PlainText(_api.Render(created.NoteId, created.RevId).Blocks[0].Inline));
        Assert.Contains("second", RenderInline.PlainText(_api.Render(created.NoteId, second.RevId).Blocks[0].Inline));
        Assert.Equal(second.RevId, _api.Render(created.NoteId).RevId);
    }

    [Fact]
    public void History_ListsRevisionsNewestFirst_WithBodiesAvailableByRevision()
    {
        var created = _api.Create("h", "one");
        var second = _api.Update(created.NoteId, created.RevId, null, "two");
        var third = _api.Update(created.NoteId, second.RevId, null, "three");

        var history = _api.History(created.NoteId).Items;
        Assert.Equal(new[] { third.RevId, second.RevId, created.RevId }, history.Select(r => r.RevId).ToArray());
        Assert.All(history, r => Assert.InRange(r.CreatedAt, 1_500_000_000_000L, 5_000_000_000_000L));
        Assert.Equal("two\n", _api.Get(created.NoteId, second.RevId).Body);
        Assert.False(_api.Get(created.NoteId, second.RevId).IsHead);

        // "restore this version" is a normal update with that revision's text
        var restored = _api.Update(created.NoteId, third.RevId, null, _api.Get(created.NoteId, second.RevId).Body);
        Assert.Equal("two\n", _api.Get(created.NoteId).Body);
        Assert.Equal(4, _api.History(created.NoteId).Items.Count);
        Assert.NotEqual(second.RevId, restored.RevId);                 // a new revision, the old ones stay
    }

    [Fact]
    public void Normalize_PreviewsWhatTheNoteBecomes()
    {
        var n = _api.Normalize("a  \r\n\r\n\r\n* item");
        Assert.Contains("- item", n.Body);
        Assert.DoesNotContain("\r", n.Body);
        Assert.Matches("^sha256:[0-9a-f]{64}$", n.Hash);
    }

    [Fact]
    public void SyncStatus_WithoutPeers_IsEmpty_AndNoConflictsExist()
    {
        var status = _api.SyncStatus();
        Assert.Empty(status.Peers);
        Assert.True(status.PendingPush >= 0);
        Assert.Equal(Strings.NotesSyncNoPeers, NotesSyncText.Line(status, DateTimeOffset.Now));
        Assert.Empty(_api.Conflicts().Items);
    }

    [Fact]
    public void ChangedEvents_ArriveForCreateUpdateDelete_AndNameTheRevision()
    {
        var created = _api.Create("events", "x");
        Assert.True(WaitForEvent(e => e is NotesChangedEvent c && c.NoteId == created.NoteId && c.RevId == created.RevId));
        var saved = _api.Update(created.NoteId, created.RevId, null, "y");
        Assert.True(WaitForEvent(e => e is NotesChangedEvent c && c.RevId == saved.RevId));
        var deleted = _api.Delete(created.NoteId);
        Assert.True(WaitForEvent(e => e is NotesChangedEvent c && c.RevId == deleted));
    }

    // ---- the editor session against the real core ------------------------------------------------------------

    private static NoteEditorSession Session(NotesApi api, out Func<DateTimeOffset> clock, out Action<TimeSpan> advance)
    {
        var now = DateTimeOffset.UtcNow;
        clock = () => now;
        advance = by => now += by;
        return new NoteEditorSession(new ThreadPoolNotesGateway(api), () => now);
    }

    [Fact]
    public async Task Session_SavesAfterThePause_AndTheNextSaveBuildsOnTheFirst()
    {
        var created = _api.Create("session", "start");
        var session = Session(_api, out _, out var advance);
        session.Open(_api.Get(created.NoteId));

        session.OnEdited("session", "start\n\nmore");
        await session.TickAsync();
        Assert.Equal(created.RevId, session.Model.BaseRevId);          // not due yet
        advance(TimeSpan.FromMilliseconds(600));
        await session.TickAsync();
        var first = session.Model.BaseRevId;
        Assert.NotEqual(created.RevId, first);
        Assert.Equal("start\n\nmore\n", _api.Get(created.NoteId).Body);

        session.OnEdited("session", "start\n\nmore\n\nand more");
        advance(TimeSpan.FromMilliseconds(600));
        await session.TickAsync();
        Assert.Equal(new[] { first }, _api.Get(created.NoteId, session.Model.BaseRevId).Parents);
        Assert.Equal(3, _api.History(created.NoteId).Items.Count);
        Assert.False(session.Model.Dirty);
    }

    [Fact]
    public async Task Session_MergesAnEditMadeElsewhere_AndShowsTheMergedText()
    {
        var created = _api.Create("merge", "first paragraph\n\nsecond paragraph\n");
        var session = Session(_api, out _, out var advance);
        var replaced = new List<(string Title, string Body)>();
        var notices = new List<string>();
        session.TextReplaced += (t, b) => replaced.Add((t, b));
        session.Notice += notices.Add;
        session.Open(_api.Get(created.NoteId));

        // "the other device" changes the second paragraph while this editor still shows the first revision
        _api.Update(created.NoteId, created.RevId, null, "first paragraph\n\nsecond paragraph, edited elsewhere\n");
        // this editor changes the first one
        session.OnEdited("merge", "first paragraph, edited here\n\nsecond paragraph\n");
        advance(TimeSpan.FromMilliseconds(600));
        await session.TickAsync();

        var merged = Assert.Single(replaced);
        Assert.Contains("edited here", merged.Body);
        Assert.Contains("edited elsewhere", merged.Body);              // both changes survive
        Assert.Equal(Strings.NoteMerged, Assert.Single(notices));
        Assert.Equal(_api.Get(created.NoteId).RevId, session.Model.BaseRevId);
        Assert.False(session.Model.Dirty);
        Assert.Empty(_api.Conflicts().Items);
    }

    [Fact]
    public async Task Session_SetsAConflictCopyAside_WhenBothSidesEditedTheSameBlock()
    {
        var created = _api.Create("conflict", "one shared paragraph\n");
        var session = Session(_api, out _, out var advance);
        var copies = new List<string>();
        var notices = new List<string>();
        session.ConflictCopyMade += copies.Add;
        session.Notice += notices.Add;
        session.Open(_api.Get(created.NoteId));

        _api.Update(created.NoteId, created.RevId, null, "one shared paragraph, theirs\n");
        session.OnEdited("conflict", "one shared paragraph, mine\n");
        advance(TimeSpan.FromMilliseconds(600));
        await session.TickAsync();

        var copyId = Assert.Single(copies);
        Assert.Equal(Strings.NoteConflictCopyMade, Assert.Single(notices));
        var conflicts = _api.Conflicts().Items;
        var conflict = Assert.Single(conflicts, c => c.ConflictNoteId == copyId);
        Assert.Equal(created.NoteId, conflict.NoteId);
        Assert.True(_api.List().Items.Single(i => i.NoteId == copyId).IsConflictCopy);
        Assert.Equal(created.NoteId, _api.Get(copyId).ConflictOf);
        Assert.False(session.Model.Dirty);                              // the box shows the winner, nothing typed is pending

        // both texts are still there: the note and the copy
        var texts = new[] { _api.Get(created.NoteId).Body, _api.Get(copyId).Body };
        Assert.Contains(texts, t => t.Contains("theirs"));
        Assert.Contains(texts, t => t.Contains("mine"));

        // keep the copy: its text replaces the note, the copy goes to the trash and the banner empties
        _api.Resolve(created.NoteId, copyId, ConflictActions.KeepConflict);
        Assert.DoesNotContain(_api.Conflicts().Items, c => c.ConflictNoteId == copyId);
        Assert.Contains(_api.List(deleted: true).Items, i => i.NoteId == copyId);
    }

    [Fact]
    public async Task Session_ARemoteEvent_ReloadsACleanEditor_AndLeavesATypingOneAlone()
    {
        var created = _api.Create("remote", "original\n");
        var session = Session(_api, out _, out var advance);
        var replaced = new List<(string Title, string Body)>();
        session.TextReplaced += (t, b) => replaced.Add((t, b));
        session.Open(_api.Get(created.NoteId));

        var other = _api.Update(created.NoteId, created.RevId, null, "changed on the other device\n");
        await session.RemoteChangedAsync(created.NoteId, other.RevId);
        Assert.Equal("changed on the other device\n", Assert.Single(replaced).Body);

        session.OnEdited("remote", "changed on the other device\n\nmy own addition");
        var again = _api.Update(created.NoteId, other.RevId, "retitled", null);
        await session.RemoteChangedAsync(created.NoteId, again.RevId);
        Assert.Single(replaced);                                        // typing: untouched
        Assert.Equal("changed on the other device\n\nmy own addition", session.Model.Body);

        advance(TimeSpan.FromMilliseconds(600));
        await session.TickAsync();                                       // the save merges the retitle and the addition
        Assert.Equal("retitled", _api.Get(created.NoteId).Title);
        Assert.Contains("my own addition", _api.Get(created.NoteId).Body);
    }

    [Fact]
    public async Task Session_ANoteDeletedElsewhere_WhileTyping_KeepsTheTextAndRestoreContinues()
    {
        var created = _api.Create("trashed", "words\n");
        var session = Session(_api, out _, out var advance);
        var errors = new List<string>();
        session.Failed += errors.Add;
        session.Open(_api.Get(created.NoteId));

        _api.Delete(created.NoteId);                                     // "the other device" deleted it
        session.OnEdited("trashed", "words\n\nstill typing");
        advance(TimeSpan.FromMilliseconds(600));
        await session.TickAsync();

        Assert.True(session.Model.InTrash);
        Assert.Equal(Strings.ErrNoteInTrash, Assert.Single(errors));
        Assert.Equal("words\n\nstill typing", session.Model.Body);      // not lost

        Assert.True(await session.RestoreNoteAsync());
        advance(TimeSpan.FromMilliseconds(600));
        await session.TickAsync();
        var note = _api.Get(created.NoteId);
        Assert.False(note.Deleted);
        Assert.Contains("still typing", note.Body);
    }

    [Fact]
    public async Task Session_UseRevision_RestoresAnOlderText()
    {
        var created = _api.Create("hist", "old text");
        _api.Update(created.NoteId, created.RevId, null, "new text");
        var session = Session(_api, out _, out _);
        session.Open(_api.Get(created.NoteId));

        Assert.True(await session.UseRevisionAsync("hist", _api.Get(created.NoteId, created.RevId).Body));

        Assert.Equal("old text\n", _api.Get(created.NoteId).Body);
        Assert.Equal(3, _api.History(created.NoteId).Items.Count);
    }

    [Fact]
    public void Render_OfTheDeepestNestingTheCoreAllows_IsStillReadable()
    {
        // lists as deep as the core lets them go, the innermost item carrying deeply nested emphasis
        // (measured on the core: 38 JSON levels)
        var body = new System.Text.StringBuilder();
        for (var i = 0; i < 20; i++) body.Append(string.Concat(Enumerable.Repeat("  ", i))).Append("- l\n");
        var open = new System.Text.StringBuilder();
        var close = "";
        for (var i = 0; i < 20; i++)
        {
            var mark = i % 2 == 0 ? "**" : "*";
            open.Append(mark).Append("a ");
            close = mark + close;
        }
        body.Append(string.Concat(Enumerable.Repeat("  ", 20))).Append("- ").Append(open).Append('x').Append(close).Append('\n');
        var created = _api.Create("deep", body.ToString());

        var tree = _api.Render(created.NoteId);

        Assert.NotEmpty(tree.Blocks);
    }

    [Fact]
    public void Session_FlushBlocking_SavesAtExit()
    {
        var created = _api.Create("exit", "x");
        var session = Session(_api, out _, out _);
        session.Open(_api.Get(created.NoteId));
        session.OnEdited("exit", "typed right before the window closed");

        Assert.True(session.FlushBlocking());
        Assert.Equal("typed right before the window closed\n", _api.Get(created.NoteId).Body);
    }
}
