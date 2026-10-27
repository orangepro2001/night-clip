using System.Text.Json;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class NotesListTests
{
    /// <summary>A core that holds notes newest first and pages them with a numeric cursor, like notes.list does.</summary>
    private sealed class PagedCore
    {
        public readonly List<(string Id, string Title, bool Deleted, long Updated)> Notes = new();
        public readonly ScriptedCore Core = new();
        public int ListCalls;

        public PagedCore()
        {
            Core.On("notes.list", p =>
            {
                ListCalls++;
                var trash = p.TryGetProperty("deleted", out var d) && d.GetBoolean();
                var limit = p.GetProperty("limit").GetInt32();
                var from = p.TryGetProperty("cursor", out var c) ? int.Parse(c.GetString()!) : 0;
                var all = Notes.Where(n => n.Deleted == trash).OrderByDescending(n => n.Updated).ToList();
                var page = all.Skip(from).Take(limit).ToList();
                var next = from + page.Count < all.Count ? (from + page.Count).ToString() : "";
                var items = string.Join(",", page.Select(n => $$"""{"note_id":"{{n.Id}}","title":"{{n.Title}}","updated_at":{{n.Updated}},"deleted":{{(n.Deleted ? "true" : "false")}},"preview":"p"}"""));
                return $$"""{"items":[{{items}}],"next_cursor":"{{next}}"}""";
            });
        }

        public PagedCore Add(int count, bool deleted = false, long startTime = 1000)
        {
            for (var i = 0; i < count; i++) Notes.Add(($"nt_{Notes.Count}", $"T{Notes.Count}", deleted, startTime + i));
            return this;
        }

        public NotesApi Api => new(Core);
    }

    [Fact]
    public void Show_LoadsTheFirstPage_AndMoreLoadsTheRest()
    {
        var core = new PagedCore().Add(120);
        var list = new NotesList();

        list.Show(core.Api, trash: false);
        Assert.Equal(NotesList.PageSize, list.Items.Count);
        Assert.True(list.HasMore);
        Assert.Equal("T119", list.Items[0].Title);       // newest first

        Assert.True(list.LoadMore(core.Api));
        Assert.Equal(100, list.Items.Count);
        Assert.True(list.LoadMore(core.Api));
        Assert.Equal(120, list.Items.Count);
        Assert.False(list.HasMore);
        Assert.False(list.LoadMore(core.Api));            // nothing more: no call
        Assert.Equal(3, core.ListCalls);
        Assert.Equal(120, list.Items.Select(i => i.NoteId).Distinct().Count());
    }

    [Fact]
    public void Trash_ShowsOnlyDeletedNotes_AndSwitchingResetsTheList()
    {
        var core = new PagedCore().Add(3).Add(2, deleted: true, startTime: 5000);
        var list = new NotesList();

        list.Show(core.Api, trash: false);
        Assert.Equal(3, list.Items.Count);
        Assert.False(list.Trash);

        list.Show(core.Api, trash: true);
        Assert.True(list.Trash);
        Assert.Equal(2, list.Items.Count);
        Assert.All(list.Items, i => Assert.True(i.Deleted));
        Assert.True(core.Core.ParamsOf("notes.list").Last().GetProperty("deleted").GetBoolean());

        list.Show(core.Api, trash: false);
        Assert.False(core.Core.ParamsOf("notes.list").Last().TryGetProperty("deleted", out _));
        Assert.Equal(3, list.Items.Count);
    }

    [Fact]
    public void LoadMore_InTrash_KeepsAskingForTheTrash()
    {
        var core = new PagedCore().Add(60, deleted: true);
        var list = new NotesList();
        list.Show(core.Api, trash: true);
        list.LoadMore(core.Api);
        Assert.Equal(60, list.Items.Count);
        Assert.All(core.Core.ParamsOf("notes.list"), p => Assert.True(p.GetProperty("deleted").GetBoolean()));
    }

    [Fact]
    public void Refresh_OfOnePage_IsOneCall_AndBringsAChangedNoteToTheTop()
    {
        var core = new PagedCore().Add(10);
        var list = new NotesList();
        list.Show(core.Api, trash: false);
        core.ListCalls = 0;

        var i = core.Notes.FindIndex(n => n.Id == "nt_2");
        core.Notes[i] = core.Notes[i] with { Updated = 9999, Title = "edited" };
        list.Refresh(core.Api);

        Assert.Equal(1, core.ListCalls);
        Assert.Equal("nt_2", list.Items[0].NoteId);
        Assert.Equal("edited", list.Items[0].Title);
        Assert.Equal(10, list.Items.Count);
    }

    [Fact]
    public void Refresh_DropsNotesThatLeftTheView_AndAddsNewOnes()
    {
        var core = new PagedCore().Add(5);
        var list = new NotesList();
        list.Show(core.Api, trash: false);

        core.Notes[1] = core.Notes[1] with { Deleted = true };                 // deleted elsewhere
        core.Notes.Add(("nt_new", "new", false, 8000));                         // created elsewhere
        list.Refresh(core.Api);

        Assert.DoesNotContain(list.Items, i => i.NoteId == "nt_1");
        Assert.Equal("nt_new", list.Items[0].NoteId);
        Assert.Equal(5, list.Items.Count);
    }

    [Fact]
    public void Refresh_AfterScrolling_ReloadsTheLoadedPages_AndKeepsTheCursorConsistent()
    {
        var core = new PagedCore().Add(130);
        var list = new NotesList();
        list.Show(core.Api, trash: false);
        list.LoadMore(core.Api);
        Assert.Equal(100, list.Items.Count);
        core.ListCalls = 0;

        core.Notes.Add(("nt_new", "new", false, 99999));
        list.Refresh(core.Api);

        Assert.Equal(2, core.ListCalls);                  // two pages were loaded, two are fetched again
        Assert.Equal(100, list.Items.Count);
        Assert.Equal("nt_new", list.Items[0].NoteId);
        Assert.Equal(100, list.Items.Select(i => i.NoteId).Distinct().Count());
        Assert.True(list.HasMore);
        Assert.True(list.LoadMore(core.Api));             // the next page continues right after
        Assert.Equal(131, list.Items.Count);
        Assert.False(list.HasMore);
    }

    [Fact]
    public void Refresh_NeverReadsMoreThanTheCap()
    {
        var core = new PagedCore().Add(NotesList.PageSize * (NotesList.MaxRefreshPages + 3));
        var list = new NotesList();
        list.Show(core.Api, trash: false);
        while (list.LoadMore(core.Api)) { }
        core.ListCalls = 0;

        list.Refresh(core.Api);
        Assert.Equal(NotesList.MaxRefreshPages, core.ListCalls);
        Assert.Equal(NotesList.PageSize * NotesList.MaxRefreshPages, list.Items.Count);
        Assert.True(list.HasMore);
    }

    [Fact]
    public void Refresh_OfAnEmptyList_AsksForOnePage()
    {
        var core = new PagedCore();
        var list = new NotesList();
        list.Refresh(core.Api);
        Assert.Equal(1, core.ListCalls);
        Assert.Empty(list.Items);
        Assert.False(list.HasMore);
    }

    [Fact]
    public void AFailedCall_LeavesTheListAsItWas()
    {
        var core = new PagedCore().Add(3);
        var list = new NotesList();
        list.Show(core.Api, trash: false);
        core.Core.On("notes.list", _ => throw new Interop.CoreException("DB_UNAVAILABLE", "x"));

        Assert.Throws<Interop.CoreException>(() => list.Refresh(core.Api));
        Assert.Throws<Interop.CoreException>(() => list.Show(core.Api, trash: true));
        Assert.Equal(3, list.Items.Count);
        Assert.False(list.Trash);                          // the switch did not happen
    }

    [Fact]
    public void Duplicates_FromPagesThatMoved_AreDropped()
    {
        var core = new ScriptedCore();
        var calls = 0;
        core.On("notes.list", _ => ++calls == 1
            ? """{"items":[{"note_id":"nt_a"},{"note_id":"nt_b"}],"next_cursor":"2"}"""
            : """{"items":[{"note_id":"nt_b"},{"note_id":"nt_c"}]}""");
        var list = new NotesList();
        list.Show(new NotesApi(core), false);
        Assert.True(list.LoadMore(new NotesApi(core)));
        Assert.Equal(new[] { "nt_a", "nt_b", "nt_c" }, list.Items.Select(i => i.NoteId).ToArray());
    }

    [Fact]
    public void Contains_FindsLoadedNotes()
    {
        var core = new PagedCore().Add(3);
        var list = new NotesList();
        list.Show(core.Api, false);
        Assert.True(list.Contains("nt_1"));
        Assert.False(list.Contains("nt_77"));
    }
}
