using System.Text.Json;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

/// <summary>Runs the core calls at once, on the calling thread.</summary>
internal sealed class InlineGateway : INotesGateway
{
    private readonly NotesApi _api;
    public InlineGateway(NotesApi api) => _api = api;

    public Task<T> RunAsync<T>(Func<NotesApi, T> call)
    {
        try { return Task.FromResult(call(_api)); }
        catch (Exception ex) { return Task.FromException<T>(ex); }
    }

    public T Run<T>(Func<NotesApi, T> call) => call(_api);
}

/// <summary>Holds every call back until the test releases it, to type "while the core is working".</summary>
internal sealed class GatedGateway : INotesGateway
{
    private readonly NotesApi _api;
    private readonly Queue<Action> _pending = new();
    public GatedGateway(NotesApi api) => _api = api;

    public int Pending => _pending.Count;

    public Task<T> RunAsync<T>(Func<NotesApi, T> call)
    {
        var done = new TaskCompletionSource<T>();
        _pending.Enqueue(() =>
        {
            try { done.SetResult(call(_api)); }
            catch (Exception ex) { done.SetException(ex); }
        });
        return done.Task;
    }

    public T Run<T>(Func<NotesApi, T> call) => call(_api);

    /// <summary>Lets the held calls run, including the ones the continuations start.</summary>
    public void ReleaseAll()
    {
        while (_pending.Count > 0) _pending.Dequeue()();
    }

    /// <summary>Keeps releasing held calls until the given tasks are done (continuations may run a moment later, on another thread).</summary>
    public async Task ReleaseUntilDone(params Task[] tasks)
    {
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (!tasks.All(t => t.IsCompleted))
        {
            ReleaseAll();
            if (DateTime.UtcNow > deadline) throw new TimeoutException("the editor session did not finish");
            await Task.Delay(1);
        }
        foreach (var t in tasks) await t;
    }
}

public class NoteEditorSessionTests
{
    private sealed class Harness
    {
        public readonly ScriptedCore Core = new();
        public DateTimeOffset Now = new(2030, 1, 1, 12, 0, 0, TimeSpan.Zero);
        public readonly List<(string Title, string Body)> Replaced = new();
        public readonly List<string> Notices = new();
        public readonly List<string> Errors = new();
        public readonly List<(string Note, string Rev)> Revisions = new();
        public readonly List<string> ConflictCopies = new();
        public readonly List<string> Order = new();
        public NoteEditorSession Session = null!;
        public GatedGateway? Gate;

        public Harness(bool gated = false, string openBody = "start\n", string openTitle = "T")
        {
            var api = new NotesApi(Core);
            INotesGateway gateway;
            if (gated) gateway = Gate = new GatedGateway(api);
            else gateway = new InlineGateway(api);
            Session = new NoteEditorSession(gateway, () => Now);
            Session.TextReplaced += (t, b) => Replaced.Add((t, b));
            Session.Notice += Notices.Add;
            Session.Failed += Errors.Add;
            Session.RevisionChanged += (n, r) => Revisions.Add((n, r));
            Session.ConflictCopyMade += ConflictCopies.Add;
            Session.Open(ND.Note(title: openTitle, body: openBody));
        }

        private readonly Dictionary<string, string> _parentOf = new();

        /// <summary>Answers each update with the next revision of the list and each get with that revision as a child of the update's base.</summary>
        public Harness UpdateAnswers(params string[] revs)
        {
            var i = 0;
            Core.On("notes.update", p =>
            {
                var rev = revs[Math.Min(i++, revs.Length - 1)];
                _parentOf[rev] = p.TryGetProperty("base_rev_id", out var b) ? b.GetString()! : "";
                return $$"""{"rev_id":"{{rev}}"}""";
            });
            Core.On("notes.get", p =>
            {
                var rev = p.TryGetProperty("rev_id", out var r) ? r.GetString()! : "r_head";
                var parents = _parentOf.TryGetValue(rev, out var parent) ? "\"" + parent + "\"" : "";
                return $$"""{"note_id":"nt_a","rev_id":"{{rev}}","parents":[{{parents}}],"title":"T","body":"server\n"}""";
            });
            return this;
        }

        public void Type(string body, string? title = null)
        {
            Session.OnEdited(title ?? Session.Model.Title, body);
        }

        public Task Pause() { Now = Now.AddMilliseconds(NoteEditorModel.DebounceMs + 1); return Session.TickAsync(); }

        public JsonElement LastUpdate => Core.ParamsOf("notes.update").Last();
    }

    // ---- saving ---------------------------------------------------------------------------------------------

    [Fact]
    public async Task TypingThenPausing_SavesOnceWithTheBaseRevision()
    {
        var h = new Harness().UpdateAnswers("r_2");
        h.Type("start\nmore");

        h.Now = h.Now.AddMilliseconds(499);
        await h.Session.TickAsync();
        Assert.Equal(0, h.Core.Count("notes.update"));         // still inside the debounce

        h.Now = h.Now.AddMilliseconds(2);
        await h.Session.TickAsync();
        Assert.Equal(1, h.Core.Count("notes.update"));
        var p = h.LastUpdate;
        Assert.Equal(("nt_a", "r_1", "start\nmore"), (p.GetProperty("note_id").GetString(), p.GetProperty("base_rev_id").GetString(), p.GetProperty("body").GetString()));
        Assert.False(p.TryGetProperty("title", out _));
        Assert.Equal("r_2", h.Session.Model.BaseRevId);
        Assert.Equal(EditorSaveState.Saved, h.Session.Model.State);
        Assert.Equal(("nt_a", "r_2"), h.Revisions.Single());    // the preview is told to render the new revision
        Assert.Empty(h.Errors);
        Assert.Empty(h.Notices);
        Assert.Empty(h.Replaced);
    }

    [Fact]
    public async Task ALongBurstOfTyping_IsOneRevision()
    {
        var h = new Harness().UpdateAnswers("r_2");
        for (var i = 0; i < 200; i++)
        {
            h.Now = h.Now.AddMilliseconds(80);
            h.Type("start\n" + new string('x', i + 1));
            await h.Session.TickAsync();
        }
        Assert.Equal(0, h.Core.Count("notes.update"));
        await h.Pause();
        Assert.Equal(1, h.Core.Count("notes.update"));
    }

    [Fact]
    public async Task TheSecondSave_UsesTheRevisionTheFirstReturned()
    {
        var h = new Harness().UpdateAnswers("r_2", "r_3");
        h.Type("a");
        await h.Pause();
        h.Type("ab");
        await h.Pause();

        var updates = h.Core.ParamsOf("notes.update").ToList();
        Assert.Equal(new[] { "r_1", "r_2" }, updates.Select(u => u.GetProperty("base_rev_id").GetString()).ToArray());
        Assert.Equal("r_3", h.Session.Model.BaseRevId);
    }

    [Fact]
    public async Task OwnChildCheck_ConfirmsTheStoredRevisionIsAChildOfTheBase()
    {
        var h = new Harness().UpdateAnswers("r_2");
        h.Type("a");
        await h.Pause();
        var get = Assert.Single(h.Core.ParamsOf("notes.get"));
        Assert.Equal("r_2", get.GetProperty("rev_id").GetString());
    }

    [Fact]
    public async Task ANewestRevisionThatIsNotOurChild_IsReloaded_AndNothingIsAdoptedBlindly()
    {
        var h = new Harness().UpdateAnswers("r_head");
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_head","parents":["r_other"],"body":"remote\n","title":"T"}""");

        h.Type("typed");
        await h.Pause();

        Assert.Equal("r_1", h.LastUpdate.GetProperty("base_rev_id").GetString());
        Assert.Equal(("T", "remote\n"), h.Replaced.Single());   // the box shows what the core holds
        Assert.Empty(h.Notices);                                // silent: nothing was merged
        Assert.Equal("r_head", h.Session.Model.BaseRevId);
    }

    // ---- merges ------------------------------------------------------------------------------------------------

    [Fact]
    public async Task AMergedAnswer_ReloadsTheNote_AndTellsTheUser()
    {
        var h = new Harness();
        h.Core.On("notes.update", """{"rev_id":"r_m","merged":true}""");
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_m","parents":["r_c","r_x"],"title":"T","body":"mine\nand theirs\n"}""");

        h.Type("mine\n");
        await h.Pause();

        Assert.Equal(("T", "mine\nand theirs\n"), h.Replaced.Single());
        Assert.Equal(Strings.NoteMerged, h.Notices.Single());
        Assert.Equal("r_m", h.Session.Model.BaseRevId);
        Assert.Equal("mine\nand theirs\n", h.Session.Model.Body);
        Assert.False(h.Session.Model.Dirty);
        Assert.Equal(("nt_a", "r_m"), h.Revisions.Single());
    }

    [Fact]
    public async Task AConflictCopy_IsReported_ReloadsTheNote_AndTellsTheUser()
    {
        var h = new Harness();
        h.Core.On("notes.update", """{"rev_id":"r_w","merged":false,"conflict_note_id":"nt_copy"}""");
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_w","title":"T","body":"the newer text\n"}""");

        h.Type("older edit\n");
        await h.Pause();

        Assert.Equal("nt_copy", h.ConflictCopies.Single());
        Assert.Equal(Strings.NoteConflictCopyMade, h.Notices.Single());
        Assert.Equal("the newer text\n", h.Replaced.Single().Body);
    }

    [Fact]
    public async Task TypingWhileTheMergedSaveRuns_IsNeverReplaced()
    {
        var h = new Harness(gated: true);
        h.Core.On("notes.update", """{"rev_id":"r_m","merged":true}""");
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_m","title":"T","body":"mine\nand theirs\n"}""");

        h.Type("mine\n");
        h.Now = h.Now.AddMilliseconds(600);
        var tick = h.Session.TickAsync();                       // the save is under way, held back by the gate
        h.Type("mine\nmore typing");                            // the user keeps typing
        await h.Gate!.ReleaseUntilDone(tick);

        Assert.Empty(h.Replaced);                               // the box is left alone
        Assert.Equal("mine\nmore typing", h.Session.Model.Body);
        Assert.Equal(Strings.NoteMergedKept, h.Notices.Single());
        Assert.True(h.Session.Model.Dirty);
        Assert.Equal("r_1", h.Session.Model.BaseRevId);          // it will merge again through the old base
    }

    // ---- changes from the other device --------------------------------------------------------------------------

    [Fact]
    public async Task ARemoteChange_WhileNothingIsUnsaved_ReloadsSilently()
    {
        var h = new Harness();
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_2","title":"T","body":"changed elsewhere\n"}""");

        await h.Session.RemoteChangedAsync("nt_a", "r_2");

        Assert.Equal(("T", "changed elsewhere\n"), h.Replaced.Single());
        Assert.Empty(h.Notices);
        Assert.Equal("r_2", h.Session.Model.BaseRevId);
        Assert.Equal(("nt_a", "r_2"), h.Revisions.Single());
    }

    [Fact]
    public async Task ARemoteChange_WhileTyping_IsLeftToTheNextSave()
    {
        var h = new Harness().UpdateAnswers("r_9");
        h.Core.On("notes.update", """{"rev_id":"r_m","merged":true}""");
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_m","title":"T","body":"mine\nand theirs\n"}""");

        h.Type("typing");
        await h.Session.RemoteChangedAsync("nt_a", "r_2");
        Assert.Equal(0, h.Core.Count("notes.get"));              // nothing was read
        Assert.Empty(h.Replaced);
        Assert.Equal("typing", h.Session.Model.Body);

        await h.Pause();                                         // the save names the old base: the core merges
        Assert.Equal("r_1", h.LastUpdate.GetProperty("base_rev_id").GetString());
        Assert.Equal("mine\nand theirs\n", h.Replaced.Single().Body);
    }

    [Fact]
    public async Task TheEventOfOurOwnSave_CausesNoReload()
    {
        var h = new Harness().UpdateAnswers("r_2");
        h.Type("a");
        await h.Pause();
        var gets = h.Core.Count("notes.get");                    // the own-child check

        await h.Session.RemoteChangedAsync("nt_a", "r_2");

        Assert.Equal(gets, h.Core.Count("notes.get"));
        Assert.Empty(h.Replaced);
    }

    [Fact]
    public async Task EventsForOtherNotes_AreIgnored()
    {
        var h = new Harness();
        await h.Session.RemoteChangedAsync("nt_other", "r_2");
        Assert.Empty(h.Core.Calls);
    }

    [Fact]
    public async Task ARemoteChange_ThatArrivesDuringASave_IsPickedUpAfterwards()
    {
        var h = new Harness(gated: true);
        h.Core.On("notes.update", """{"rev_id":"r_2"}""");
        h.Core.On("notes.get", p => p.TryGetProperty("rev_id", out var rev) && rev.GetString() == "r_2"
            ? """{"note_id":"nt_a","rev_id":"r_2","parents":["r_1"],"title":"T","body":"typed\n"}"""
            : """{"note_id":"nt_a","rev_id":"r_remote","title":"T","body":"remote text\n"}""");

        h.Type("typed\n");
        h.Now = h.Now.AddMilliseconds(600);
        var tick = h.Session.TickAsync();
        var remote = h.Session.RemoteChangedAsync("nt_a", "r_remote");   // held back: a save is running
        await h.Gate!.ReleaseUntilDone(tick, remote);

        Assert.Equal(("T", "remote text\n"), h.Replaced.Single());
        Assert.Equal("r_remote", h.Session.Model.BaseRevId);
    }

    // ---- flushing -------------------------------------------------------------------------------------------------

    [Fact]
    public async Task Flush_SavesAtOnce_WithoutWaitingForTheDebounce()
    {
        var h = new Harness().UpdateAnswers("r_2");
        h.Type("typed just now");
        Assert.True(await h.Session.FlushAsync());
        Assert.Equal(1, h.Core.Count("notes.update"));
        Assert.False(h.Session.Model.Dirty);
        Assert.True(await h.Session.FlushAsync());               // nothing left: no second call
        Assert.Equal(1, h.Core.Count("notes.update"));
    }

    [Fact]
    public async Task Flush_WaitsForARunningSave_ThenSavesWhatWasTypedSince()
    {
        var h = new Harness(gated: true).UpdateAnswers("r_2", "r_3");

        h.Type("first");
        h.Now = h.Now.AddMilliseconds(600);
        var tick = h.Session.TickAsync();
        h.Type("first, second");
        var flush = h.Session.FlushAsync();
        await h.Gate!.ReleaseUntilDone(tick, flush);

        Assert.True(await flush);
        var bodies = h.Core.ParamsOf("notes.update").Select(u => u.GetProperty("body").GetString()).ToArray();
        Assert.Equal(new[] { "first", "first, second" }, bodies);
        Assert.False(h.Session.Model.Dirty);
    }

    [Fact]
    public async Task Flush_ThatFails_ReportsFalse_AndKeepsTheText()
    {
        var h = new Harness();
        h.Core.On("notes.update", _ => throw new CoreException("DB_UNAVAILABLE", "closed"));
        h.Type("precious words");

        Assert.False(await h.Session.FlushAsync());

        Assert.Equal("precious words", h.Session.Model.Body);
        Assert.True(h.Session.Model.NeedsSave);
        Assert.Equal(EditorSaveState.Failed, h.Session.Model.State);
        Assert.Equal(ErrorMessages.ForNotes("DB_UNAVAILABLE"), h.Errors.Single());
    }

    [Fact]
    public async Task Flush_OfACleanNote_DoesNothing()
    {
        var h = new Harness();
        Assert.True(await h.Session.FlushAsync());
        Assert.Empty(h.Core.Calls);
    }

    [Fact]
    public async Task AFailedSave_IsRetriedByTheNextFlush()
    {
        var h = new Harness();
        var fail = true;
        h.Core.On("notes.update", _ => fail ? throw new CoreException("INTERNAL", "x") : """{"rev_id":"r_2"}""");
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_2","parents":["r_1"]}""");
        h.Type("words");
        Assert.False(await h.Session.FlushAsync());
        fail = false;
        Assert.True(await h.Session.FlushAsync());
        Assert.Equal("r_2", h.Session.Model.BaseRevId);
    }

    [Fact]
    public async Task TooLargeText_IsNeverSent_AndFlushSaysSo()
    {
        var h = new Harness();
        h.Type(new string('x', NoteLimits.MaxBodyBytes + 1));
        Assert.False(await h.Session.FlushAsync());
        Assert.Equal(0, h.Core.Count("notes.update"));
        Assert.Equal(EditorSaveState.TooLarge, h.Session.Model.State);
    }

    [Fact]
    public void FlushBlocking_SavesSynchronously_ForTheMomentTheApplicationExits()
    {
        var h = new Harness().UpdateAnswers("r_2");
        h.Type("typed right before exit");
        Assert.True(h.Session.FlushBlocking());
        Assert.Equal("typed right before exit", h.LastUpdate.GetProperty("body").GetString());
        Assert.False(h.Session.Model.Dirty);
        Assert.Empty(h.Revisions);                               // no events at exit
    }

    [Fact]
    public void FlushBlocking_ThatFails_ReportsFalse()
    {
        var h = new Harness();
        h.Core.On("notes.update", _ => throw new CoreException("DB_UNAVAILABLE", "x"));
        h.Type("words");
        Assert.False(h.Session.FlushBlocking());
        Assert.True(h.Session.Model.NeedsSave);
    }

    // ---- switching notes ------------------------------------------------------------------------------------------

    [Fact]
    public async Task SwitchingNotes_SavesTheOldOneFirst_AndTheLateEventsDoNotTouchTheNewOne()
    {
        var h = new Harness().UpdateAnswers("r_2");
        h.Type("unsaved words");
        Assert.True(await h.Session.FlushAsync());                // what the page does before opening another note
        h.Session.Open(ND.Note("r_7", id: "nt_b", body: "other note\n"));

        Assert.Equal("nt_a", h.LastUpdate.GetProperty("note_id").GetString());
        Assert.Equal(("nt_b", "other note\n"), (h.Session.Model.NoteId, h.Session.Model.Body));
        Assert.False(h.Session.Model.Dirty);
        await h.Session.RemoteChangedAsync("nt_a", "r_9");        // an event of the note that was left
        Assert.Empty(h.Replaced);
    }

    [Fact]
    public async Task ASaveThatFinishesAfterTheNoteWasClosed_ChangesNothing()
    {
        var h = new Harness(gated: true);
        h.Core.On("notes.update", """{"rev_id":"r_2"}""");
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_2","parents":["r_1"]}""");
        h.Type("x");
        h.Now = h.Now.AddMilliseconds(600);
        var tick = h.Session.TickAsync();
        h.Session.Open(ND.Note("r_7", id: "nt_b", body: "other\n"));
        await h.Gate!.ReleaseUntilDone(tick);

        Assert.Equal(("nt_b", "r_7"), (h.Session.Model.NoteId, h.Session.Model.BaseRevId));
        Assert.Empty(h.Revisions);
        Assert.Empty(h.Notices);
    }

    // ---- recycle bin ---------------------------------------------------------------------------------------------

    [Fact]
    public async Task EditingANoteThatIsInTheTrash_ExplainsAndKeepsTheText_AndRestoreContinues()
    {
        var h = new Harness();
        var inTrash = true;
        h.Core.On("notes.update", _ => inTrash ? throw new CoreException("CONFLICT", "the note is in the trash") : """{"rev_id":"r_3","merged":true}""");
        h.Core.On("notes.restore", _ => { inTrash = false; return """{"rev_id":"r_2"}"""; });
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_3","title":"T","body":"typed while in the trash\n"}""");

        h.Type("typed while in the trash\n");
        await h.Pause();
        Assert.True(h.Session.Model.InTrash);
        Assert.Equal(Strings.ErrNoteInTrash, h.Errors.Single());
        Assert.Equal("typed while in the trash\n", h.Session.Model.Body);
        h.Now = h.Now.AddSeconds(30);
        await h.Session.TickAsync();                              // no endless retries
        Assert.Equal(1, h.Core.Count("notes.update"));

        Assert.True(await h.Session.RestoreNoteAsync());
        Assert.Equal(1, h.Core.Count("notes.restore"));
        Assert.False(h.Session.Model.InTrash);
        await h.Session.TickAsync();                              // the typed text goes in now
        Assert.Equal(2, h.Core.Count("notes.update"));
    }

    [Fact]
    public async Task RestoringACleanTrashedNote_ReadsItAgain()
    {
        var h = new Harness();
        h.Session.Open(ND.Note("r_del", body: "gone\n", deleted: true));
        h.Core.On("notes.restore", """{"rev_id":"r_back"}""");
        h.Core.On("notes.get", """{"note_id":"nt_a","rev_id":"r_back","title":"T","body":"gone\n","deleted":false}""");

        Assert.True(h.Session.Model.ReadOnly);
        Assert.True(await h.Session.RestoreNoteAsync());
        Assert.False(h.Session.Model.ReadOnly);
        Assert.Equal("r_back", h.Session.Model.BaseRevId);
    }

    [Fact]
    public async Task ARestoreThatFails_ReportsTheError()
    {
        var h = new Harness();
        h.Core.On("notes.restore", _ => throw new CoreException("NOT_FOUND", "gone"));
        Assert.False(await h.Session.RestoreNoteAsync());
        Assert.Equal(Strings.ErrNoteNotFound, h.Errors.Single());
    }

    // ---- history ----------------------------------------------------------------------------------------------------

    [Fact]
    public async Task RestoringAnOlderVersion_IsANormalSave_AfterSavingWhatWasTyped()
    {
        var h = new Harness().UpdateAnswers("r_2", "r_3");
        h.Type("typed but not saved");

        Assert.True(await h.Session.UseRevisionAsync("Old title", "the old text\n"));

        var bodies = h.Core.ParamsOf("notes.update").Select(u => u.GetProperty("body").GetString()).ToArray();
        Assert.Equal(new[] { "typed but not saved", "the old text\n" }, bodies);   // nothing typed is lost to the restore
        Assert.Equal(("Old title", "the old text\n"), h.Replaced.Single());
        Assert.Equal("r_3", h.Session.Model.BaseRevId);
        Assert.Equal("Old title", h.LastUpdate.GetProperty("title").GetString());
    }

    [Fact]
    public async Task RestoringTheVersionThatIsShownAlready_SaysSo()
    {
        var h = new Harness();
        Assert.True(await h.Session.UseRevisionAsync("T", "start\n"));
        Assert.Equal(Strings.NoteRevisionSame, h.Notices.Single());
        Assert.Equal(0, h.Core.Count("notes.update"));
    }

    [Fact]
    public async Task ANoteInTheTrash_CannotTakeAnOlderVersion()
    {
        var h = new Harness();
        h.Session.Open(ND.Note(deleted: true));
        Assert.False(await h.Session.UseRevisionAsync("a", "b"));
        Assert.Equal(0, h.Core.Count("notes.update"));
    }

    // ---- state notifications ------------------------------------------------------------------------------------

    [Fact]
    public async Task StateChanged_FiresForTypingAndForTheWholeSave()
    {
        var h = new Harness().UpdateAnswers("r_2");
        var states = new List<EditorSaveState>();
        h.Session.StateChanged += () => states.Add(h.Session.Model.State);

        h.Type("a");
        await h.Pause();

        Assert.Contains(EditorSaveState.Dirty, states);
        Assert.Contains(EditorSaveState.Saving, states);
        Assert.Equal(EditorSaveState.Saved, states[^1]);
    }

    [Fact]
    public async Task ACorruptAnswer_IsAnErrorNotACrash()
    {
        var h = new Harness();
        h.Core.On("notes.update", _ => throw new JsonException("not json"));
        h.Type("x");
        Assert.False(await h.Session.FlushAsync());
        Assert.Equal(ErrorMessages.ForNotes("INTERNAL"), h.Errors.Single());
    }
}
