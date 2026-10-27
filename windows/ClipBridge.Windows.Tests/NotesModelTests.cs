using System.Text.Json;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

/// <summary>An <see cref="ICoreCaller"/> that answers per method from a handler and records every call.</summary>
internal sealed class ScriptedCore : ICoreCaller
{
    public readonly List<(string Method, JsonElement Params)> Calls = new();
    private readonly Dictionary<string, Func<JsonElement, string>> _handlers = new();

    /// <summary>The handler gets the parameters of the call and returns the JSON of the result (or throws a <see cref="CoreException"/>).</summary>
    public ScriptedCore On(string method, Func<JsonElement, string> handler)
    {
        _handlers[method] = handler;
        return this;
    }

    public ScriptedCore On(string method, string result) => On(method, _ => result);

    public JsonElement Call(string method, object? parameters = null)
    {
        var p = JsonDocument.Parse(JsonSerializer.Serialize(parameters ?? new { })).RootElement.Clone();
        Calls.Add((method, p));
        return VJ.Parse(_handlers.TryGetValue(method, out var h) ? h(p) : "{}");
    }

    public IEnumerable<JsonElement> ParamsOf(string method) => Calls.Where(c => c.Method == method).Select(c => c.Params);

    public int Count(string method) => Calls.Count(c => c.Method == method);
}

public class NotesApiTests
{
    private static NotesApi Api(ScriptedCore core) => new(core);

    [Fact]
    public void List_ParsesItems_AndSendsPagingParameters()
    {
        var core = new ScriptedCore().On("notes.list", """
            {"items":[{"note_id":"nt_a","title":"买菜","updated_at":1790000000123,"deleted":false,"preview":"牛奶 鸡蛋"},
                      {"note_id":"nt_b","title":"","updated_at":5,"deleted":true,"preview":"","conflict_of":"nt_a"}],
             "next_cursor":"c2"}
            """);
        var page = Api(core).List(deleted: true, limit: 25, cursor: "c1");

        Assert.Equal(2, page.Items.Count);
        Assert.Equal(("nt_a", "买菜", 1790000000123L, false, "牛奶 鸡蛋"), (page.Items[0].NoteId, page.Items[0].Title, page.Items[0].UpdatedAt, page.Items[0].Deleted, page.Items[0].Preview));
        Assert.False(page.Items[0].IsConflictCopy);
        Assert.True(page.Items[1].IsConflictCopy);
        Assert.Equal("nt_a", page.Items[1].ConflictOf);
        Assert.True(page.HasMore);
        Assert.Equal("c2", page.NextCursor);

        var p = core.Calls.Single().Params;
        Assert.Equal(25, p.GetProperty("limit").GetInt32());
        Assert.True(p.GetProperty("deleted").GetBoolean());
        Assert.Equal("c1", p.GetProperty("cursor").GetString());
    }

    [Fact]
    public void List_OmitsDefaults_AndToleratesMissingAndExtraFields()
    {
        var core = new ScriptedCore().On("notes.list", """{"items":[{"note_id":"nt_a"},{"title":"no id"},{"note_id":"nt_c","future_field":{"x":1},"updated_at":"nan"}]}""");
        var page = Api(core).List();

        Assert.Equal(new[] { "nt_a", "nt_c" }, page.Items.Select(i => i.NoteId).ToArray()); // an item without an id is dropped
        Assert.Equal("", page.Items[0].Title);
        Assert.Equal(0, page.Items[1].UpdatedAt);      // a mistyped field gives the default
        Assert.False(page.HasMore);
        var p = core.Calls.Single().Params;
        Assert.False(p.TryGetProperty("deleted", out _));
        Assert.False(p.TryGetProperty("cursor", out _));
    }

    [Fact]
    public void List_OfAnEmptyOrWrongAnswer_IsEmpty()
    {
        Assert.Empty(NotesPage.FromJson(VJ.Parse("{}")).Items);
        Assert.Empty(NotesPage.FromJson(VJ.Parse("""{"items":"x"}""")).Items);
        Assert.Empty(NotesPage.FromJson(VJ.Parse("[]")).Items);
    }

    [Fact]
    public void Get_ParsesTheRevision_AndAsksForASpecificOne()
    {
        var core = new ScriptedCore().On("notes.get", """
            {"note_id":"nt_a","title":"T","body":"正文\n","format_version":1,"rev_id":"r_2","parents":["r_1"],"created_at":10,"updated_at":20,
             "deleted":false,"origin_device":"dev_x","rev_count":2,"is_head":false,"conflict_of":"nt_z"}
            """);
        var d = Api(core).Get("nt_a", "r_2");

        Assert.Equal(("nt_a", "T", "正文\n", "r_2"), (d.NoteId, d.Title, d.Body, d.RevId));
        Assert.Equal(new[] { "r_1" }, d.Parents);
        Assert.Equal((10L, 20L, 2), (d.CreatedAt, d.UpdatedAt, d.RevCount));
        Assert.False(d.IsHead);
        Assert.Equal("nt_z", d.ConflictOf);
        Assert.Equal("dev_x", d.OriginDevice);
        var p = core.Calls.Single().Params;
        Assert.Equal("nt_a", p.GetProperty("note_id").GetString());
        Assert.Equal("r_2", p.GetProperty("rev_id").GetString());
    }

    [Fact]
    public void Get_WithoutRevision_OmitsIt_AndMissingFieldsGiveDefaults()
    {
        var core = new ScriptedCore().On("notes.get", """{"note_id":"nt_a","rev_id":"r_1"}""");
        var d = Api(core).Get("nt_a");

        Assert.False(core.Calls.Single().Params.TryGetProperty("rev_id", out _));
        Assert.Equal("", d.Title);
        Assert.Equal("", d.Body);
        Assert.Empty(d.Parents);
        Assert.True(d.IsHead);       // an older core that does not say is taken to mean the newest revision
        Assert.False(d.Deleted);
    }

    [Fact]
    public void Create_SendsOnlyWhatIsGiven()
    {
        var core = new ScriptedCore().On("notes.create", """{"note_id":"nt_new","rev_id":"r_1"}""");
        var empty = Api(core).Create();
        Assert.Equal(("nt_new", "r_1"), (empty.NoteId, empty.RevId));
        Assert.Equal("{}", core.Calls[0].Params.GetRawText());

        Api(core).Create("标题", "# 正文\n");
        Assert.Equal("标题", core.Calls[1].Params.GetProperty("title").GetString());
        Assert.Equal("# 正文\n", core.Calls[1].Params.GetProperty("body").GetString());
    }

    [Fact]
    public void Update_SendsBaseRevisionAndOnlyTheChangedParts()
    {
        var core = new ScriptedCore().On("notes.update", """{"rev_id":"r_2","merged":true,"conflict_note_id":"nt_copy"}""");
        var r = Api(core).Update("nt_a", "r_1", null, "new body");

        Assert.Equal(("r_2", true, "nt_copy"), (r.RevId, r.Merged, r.ConflictNoteId));
        var p = core.Calls.Single().Params;
        Assert.Equal("r_1", p.GetProperty("base_rev_id").GetString());
        Assert.Equal("new body", p.GetProperty("body").GetString());
        Assert.False(p.TryGetProperty("title", out _));   // unchanged parts are not sent
        Assert.False(p.TryGetProperty("patch", out _));

        Api(core).Update("nt_a", "", "", null);           // an empty title is a real change (it clears the title)
        var q = core.Calls[1].Params;
        Assert.Equal("", q.GetProperty("title").GetString());
        Assert.False(q.TryGetProperty("base_rev_id", out _));
        Assert.False(q.TryGetProperty("body", out _));
    }

    [Fact]
    public void Update_AnswerWithoutOptionalFields_IsNotMerged()
    {
        var r = Api(new ScriptedCore().On("notes.update", """{"rev_id":"r_9"}""")).Update("nt_a", "r_8", "t", null);
        Assert.Equal(("r_9", false, ""), (r.RevId, r.Merged, r.ConflictNoteId));
    }

    [Fact]
    public void DeleteRestore_ReturnTheRevision()
    {
        var core = new ScriptedCore().On("notes.delete", """{"rev_id":"r_d"}""").On("notes.restore", """{"rev_id":"r_r"}""");
        Assert.Equal("r_d", Api(core).Delete("nt_a"));
        Assert.Equal("r_r", Api(core).Restore("nt_a"));
        Assert.Equal("nt_a", core.Calls[0].Params.GetProperty("note_id").GetString());
    }

    [Fact]
    public void History_ParsesRevisions()
    {
        var core = new ScriptedCore().On("notes.history", """
            {"items":[{"rev_id":"r_3","title":"T","body_hash":"ab","origin_device":"merge","created_at":300,"parents":["r_1","r_2"],"deleted":false},
                      {"rev_id":"r_2","created_at":200,"deleted":true},{"title":"no id"}],
             "next_cursor":"n"}
            """);
        var page = Api(core).History("nt_a", 20, "c");

        Assert.Equal(2, page.Items.Count);
        Assert.True(page.Items[0].IsMerge);
        Assert.Equal(new[] { "r_1", "r_2" }, page.Items[0].Parents);
        Assert.True(page.Items[1].Deleted);
        Assert.True(page.HasMore);
        var p = core.Calls.Single().Params;
        Assert.Equal((20, "c", "nt_a"), (p.GetProperty("limit").GetInt32(), p.GetProperty("cursor").GetString(), p.GetProperty("note_id").GetString()));
    }

    [Fact]
    public void Normalize_And_SyncNow_And_Resolve_AndConflicts()
    {
        var core = new ScriptedCore()
            .On("notes.normalize", """{"body_norm":"a\n","body_hash":"h"}""")
            .On("notes.sync_now", """{"started":true}""")
            .On("notes.sync_resolve", """{"rev_id":"r_x"}""")
            .On("notes.sync_conflicts", """{"items":[{"note_id":"nt_a","conflict_note_id":"nt_c","title":"T","created_at":9},{"note_id":"nt_b"}],"next_cursor":"m"}""");
        var api = Api(core);

        var n = api.Normalize("a");
        Assert.Equal(("a\n", "h"), (n.Body, n.Hash));
        Assert.True(api.SyncNow("dev_p"));
        Assert.Equal("dev_p", core.ParamsOf("notes.sync_now").Single().GetProperty("peer_id").GetString());
        Assert.True(api.SyncNow());
        Assert.False(core.Calls[^1].Params.TryGetProperty("peer_id", out _));

        Assert.Equal("r_x", api.Resolve("nt_a", "nt_c", ConflictActions.KeepConflict));
        var rp = core.ParamsOf("notes.sync_resolve").Single();
        Assert.Equal(("nt_a", "nt_c", "keep_conflict"), (rp.GetProperty("note_id").GetString(), rp.GetProperty("conflict_note_id").GetString(), rp.GetProperty("action").GetString()));

        var conflicts = api.Conflicts();
        var c = Assert.Single(conflicts.Items);          // an item without a copy id is dropped
        Assert.Equal(("nt_a", "nt_c", "T", 9L), (c.NoteId, c.ConflictNoteId, c.Title, c.CreatedAt));
        Assert.True(conflicts.HasMore);
    }

    [Fact]
    public void Search_PassesTheCoresErrorsThrough()
    {
        var core = new ScriptedCore().On("notes.search", _ => throw new CoreException("INVALID", "empty query"));
        var ex = Assert.Throws<CoreException>(() => Api(core).Search(""));
        Assert.Equal("INVALID", ex.Code);
    }

    [Fact]
    public void CoreErrors_PassThroughWithTheirCode()
    {
        var core = new ScriptedCore().On("notes.update", _ => throw new CoreException("CONFLICT", "the note is in the trash"));
        Assert.Equal("CONFLICT", Assert.Throws<CoreException>(() => Api(core).Update("nt_a", "r_1", "t", null)).Code);
    }
}

public class SyncStatusTests
{
    private static readonly DateTimeOffset Now = DateTimeOffset.FromUnixTimeMilliseconds(1_800_000_000_000);

    private static NotesSyncStatus Status(string json) => NotesSyncStatus.FromJson(VJ.Parse(json));

    [Fact]
    public void Parses_PeersPendingAndTheGlobalConflictCount()
    {
        var s = Status("""
            {"peers":[{"peer_id":"dev_a","name":"Pixel","connected":true,"state":"syncing","detail":"d","last_sync_at":1799999000000,"behind":3,"conflict_count":2},
                      {"peer_id":"dev_b","conflict_count":2},{"name":"no id"}],"pending_push":4}
            """);
        Assert.Equal(2, s.Peers.Count);
        Assert.Equal(4, s.PendingPush);
        Assert.Equal(2, s.ConflictCount);
        Assert.Equal(("Pixel", true, "syncing", 3), (s.Peers[0].Name, s.Peers[0].Connected, s.Peers[0].State, s.Peers[0].Behind));
        Assert.Equal("idle", s.Peers[1].State);   // a missing state is taken as idle
        Assert.Equal("dev_b", s.Peers[1].DisplayName);
    }

    [Fact]
    public void EmptyAnswer_HasNoPeers()
    {
        var s = Status("{}");
        Assert.Empty(s.Peers);
        Assert.Equal(0, s.ConflictCount);
        Assert.Equal(Strings.NotesSyncNoPeers, NotesSyncText.Line(s, Now));
    }

    [Fact]
    public void Text_PerState()
    {
        var ago = Now.ToUnixTimeMilliseconds() - 5 * 60_000;
        string Peer(string json) => NotesSyncText.Peer(NotesPeerStatus.FromJson(VJ.Parse(json)), Now);

        Assert.Equal(Strings.NotesSyncDone("Pixel", Strings.LastSeenMinutes(5)), Peer($$"""{"peer_id":"d","name":"Pixel","connected":true,"state":"idle","last_sync_at":{{ago}}}"""));
        Assert.Equal(Strings.NotesSyncNever("Pixel"), Peer("""{"peer_id":"d","name":"Pixel","connected":true,"state":"idle","last_sync_at":0}"""));
        Assert.Equal(Strings.NotesSyncOffline("Pixel", Strings.LastSeenMinutes(5)), Peer($$"""{"peer_id":"d","name":"Pixel","connected":false,"state":"idle","last_sync_at":{{ago}}}"""));
        Assert.Equal(Strings.NotesSyncOfflineNever("Pixel"), Peer("""{"peer_id":"d","name":"Pixel","connected":false,"state":"idle"}"""));
        Assert.Equal(Strings.NotesSyncSyncing("Pixel"), Peer("""{"peer_id":"d","name":"Pixel","connected":true,"state":"syncing"}"""));
        Assert.Equal(Strings.NotesSyncSyncingBehind("Pixel", 7), Peer("""{"peer_id":"d","name":"Pixel","connected":true,"state":"syncing","behind":7}"""));
        Assert.Equal(Strings.NotesSyncError("Pixel"), Peer("""{"peer_id":"d","name":"Pixel","connected":true,"state":"error","detail":"boom"}"""));
        Assert.Equal(Strings.NotesSyncPeerOld("Pixel"), Peer("""{"peer_id":"d","name":"Pixel","connected":true,"state":"peer_old"}"""));
        Assert.EndsWith(Strings.NotesSyncBehindSuffix(2), Peer($$"""{"peer_id":"d","name":"Pixel","connected":true,"state":"idle","last_sync_at":{{ago}},"behind":2}"""));
    }

    [Fact]
    public void PeerOld_TellsToUpdateThisApp()
    {
        var text = Strings.NotesSyncPeerOld("Pixel");
        Assert.Contains("Pixel", text);
        Assert.Contains("更新", text);
        Assert.True(Status("""{"peers":[{"peer_id":"d","state":"peer_old"}]}""").AnyPeerOld);
    }

    [Fact]
    public void Line_JoinsDevices_AndAppendsWhatWaitsToBeSent()
    {
        var s = Status("""{"peers":[{"peer_id":"a","name":"A","connected":true,"state":"syncing"},{"peer_id":"b","name":"B","connected":false}],"pending_push":2}""");
        var line = NotesSyncText.Line(s, Now);
        Assert.Contains(Strings.NotesSyncSyncing("A"), line);
        Assert.Contains(Strings.NotesSyncOfflineNever("B"), line);
        Assert.EndsWith(Strings.NotesSyncPending(2), line);
    }

    [Fact]
    public void Details_ShowTheCoresReasonForAFailure()
    {
        var s = Status("""{"peers":[{"peer_id":"a","name":"A","state":"error","detail":"timeout"},{"peer_id":"b","name":"B","state":"idle","connected":true}]}""");
        var lines = NotesSyncText.Details(s, Now).Split('\n');
        Assert.Equal(2, lines.Length);
        Assert.Contains("timeout", lines[0]);
        Assert.DoesNotContain("(", lines[1]);
    }

    [Fact]
    public void ConflictNotice_NamesTheNote_OrFallsBackToUntitled()
    {
        var elsewhere = new NotesSyncConflictEvent { Reason = NotesSyncConflictEvent.DeletedElsewhere };
        Assert.Equal(Strings.NoteDeletedElsewhere("周报"), NotesSyncText.ConflictNotice(elsewhere, "周报"));
        Assert.Equal(Strings.NoteDeletedElsewhere(Strings.Untitled), NotesSyncText.ConflictNotice(elsewhere, ""));
        var diverged = new NotesSyncConflictEvent { Reason = NotesSyncConflictEvent.Diverged, ConflictNoteId = "nt_c" };
        Assert.Equal(Strings.NoteSyncConflictCopy("周报"), NotesSyncText.ConflictNotice(diverged, "周报"));
    }
}

public class NotesEventTests
{
    [Fact]
    public void Changed_CarriesNoteAndRevision_AndLogsOnlyTheId()
    {
        var e = Assert.IsType<NotesChangedEvent>(CoreEventParser.Parse("""{"type":"notes.changed","note_id":"nt_a","rev_id":"r_1"}"""));
        Assert.Equal(("nt_a", "r_1"), (e.NoteId, e.RevId));
        Assert.Equal("notes.changed nt_a", e.LogLine);
    }

    [Fact]
    public void SyncUpdated_And_Conflict()
    {
        var u = Assert.IsType<NotesSyncUpdatedEvent>(CoreEventParser.Parse("""{"type":"notes.sync.updated","peer_id":"dev_a","state":"syncing","behind":3}"""));
        Assert.Equal(("dev_a", "syncing", 3), (u.PeerId, u.State, u.Behind));

        var diverged = Assert.IsType<NotesSyncConflictEvent>(CoreEventParser.Parse(
            """{"type":"notes.sync.conflict","note_id":"nt_a","conflict_note_id":"nt_c","reason":"diverged"}"""));
        Assert.Equal(("nt_a", "nt_c", NotesSyncConflictEvent.Diverged), (diverged.NoteId, diverged.ConflictNoteId, diverged.Reason));

        var elsewhere = Assert.IsType<NotesSyncConflictEvent>(CoreEventParser.Parse(
            """{"type":"notes.sync.conflict","note_id":"nt_a","reason":"deleted_elsewhere"}"""));
        Assert.Equal("", elsewhere.ConflictNoteId);
        Assert.Equal(NotesSyncConflictEvent.DeletedElsewhere, elsewhere.Reason);
    }

    [Fact]
    public void MissingFields_GiveDefaults_AndNegativeBacklogIsZero()
    {
        var c = Assert.IsType<NotesChangedEvent>(CoreEventParser.Parse("""{"type":"notes.changed"}"""));
        Assert.Equal("", c.NoteId);
        Assert.Null(c.Id);
        var u = Assert.IsType<NotesSyncUpdatedEvent>(CoreEventParser.Parse("""{"type":"notes.sync.updated","behind":-4}"""));
        Assert.Equal(0, u.Behind);
    }
}

public class NotesErrorMessageTests
{
    [Theory]
    [InlineData("NOT_FOUND", "找不到这篇笔记")]
    [InlineData("CONFLICT", "回收站")]
    [InlineData("TOO_LARGE", "1 MiB")]
    [InlineData("UNSUPPORTED_FORMAT", "更新")]
    [InlineData("NOT_IMPLEMENTED", "暂时")]
    [InlineData("NOT_PAIRED", "配对")]
    public void NotesCodes_HaveTheirOwnWording(string code, string fragment)
    {
        var text = ErrorMessages.ForNotes(code);
        Assert.Contains(fragment, text);
        Assert.NotEqual(Strings.ErrorUnknown(code), text);
    }

    [Fact]
    public void NotFound_IsNotTheTransferMessage() =>
        Assert.NotEqual(ErrorMessages.ForCode("NOT_FOUND"), ErrorMessages.ForNotes("NOT_FOUND"));

    [Fact]
    public void OtherCodes_FallBackToTheGeneralMapping()
    {
        Assert.Equal(Strings.ErrInvalid, ErrorMessages.ForNotes("INVALID"));
        Assert.Equal(Strings.ErrNotInitialized, ErrorMessages.ForNotes("NOT_INITIALIZED"));
        Assert.Equal(Strings.ErrorGeneric, ErrorMessages.ForNotes(null));
        Assert.Equal(Strings.ErrorUnknown("WEIRD"), ErrorMessages.ForNotes("WEIRD"));
    }

    [Fact]
    public void NotesResult_CarriesTheMessage()
    {
        var failed = NotesResult<int>.Fail("CONFLICT");
        Assert.False(failed.Ok);
        Assert.Equal("CONFLICT", failed.ErrorCode);
        Assert.Equal(Strings.ErrNoteInTrash, failed.Error);
        Assert.True(NotesResult<int>.Success(3).Ok);
    }
}

public class NotesSupportTests
{
    [Theory]
    [InlineData("https://example.com/a?b=1", true)]
    [InlineData("HTTP://example.com", true)]
    [InlineData("mailto:me@example.com", true)]
    [InlineData("mailto:me@example.com?subject=hi", true)]
    [InlineData("javascript:alert(1)", false)]
    [InlineData("file:///C:/Windows/System32/calc.exe", false)]
    [InlineData("ms-msdt:/id", false)]
    [InlineData("data:text/html;base64,AA==", false)]
    [InlineData("//evil.example.com", false)]
    [InlineData("relative/path.md", false)]
    [InlineData("mailto:", false)]
    [InlineData("mailto:nobody", false)]
    [InlineData("https://exa\u0007mple.com", false)]
    [InlineData("", false)]
    [InlineData(null, false)]
    public void OnlyWebAndMailLinksMayBeOpened(string? href, bool openable) =>
        Assert.Equal(openable, NoteLinks.IsOpenable(href));

    [Fact]
    public void ViewModes_RoundTrip_AndUnknownGivesDefault()
    {
        foreach (var mode in Enum.GetValues<NotesViewMode>())
            Assert.Equal(mode, NotesViewModes.Parse(NotesViewModes.Key(mode)));
        Assert.Equal(NotesViewModes.Default, NotesViewModes.Parse(""));
        Assert.Equal(NotesViewModes.Default, NotesViewModes.Parse(null));
        Assert.Equal(NotesViewModes.Default, NotesViewModes.Parse("wysiwyg"));
    }

    [Fact]
    public void ShellPrefs_RemembersTheViewMode()
    {
        var dir = Path.Combine(Path.GetTempPath(), "cb-notes-prefs-" + Guid.NewGuid().ToString("N"));
        try
        {
            Assert.Equal(NotesViewMode.Split, ShellPrefs.Load(dir).NotesView);   // no file: default
            new ShellPrefs { NotesView = NotesViewMode.Preview, FirstRunDone = true }.Save(dir);
            var loaded = ShellPrefs.Load(dir);
            Assert.Equal(NotesViewMode.Preview, loaded.NotesView);
            Assert.True(loaded.FirstRunDone);

            File.WriteAllText(Path.Combine(dir, ShellPrefs.FileName), """{"first_run_done":true,"notes_view":"nonsense"}""");
            Assert.Equal(NotesViewModes.Default, ShellPrefs.Load(dir).NotesView);
            File.WriteAllText(Path.Combine(dir, ShellPrefs.FileName), """{"first_run_done":true}"""); // written by an older version
            Assert.Equal(NotesViewModes.Default, ShellPrefs.Load(dir).NotesView);
        }
        finally { if (Directory.Exists(dir)) Directory.Delete(dir, true); }
    }

    [Fact]
    public void RelativeTime_UsesMilliseconds()
    {
        var now = DateTimeOffset.FromUnixTimeMilliseconds(1_800_000_000_000);
        long Ago(long ms) => now.ToUnixTimeMilliseconds() - ms;

        Assert.Equal("", NotesTime.Relative(0, now));
        Assert.Equal(Strings.LastSeenNow, NotesTime.Relative(Ago(5_000), now));
        Assert.Equal(Strings.LastSeenNow, NotesTime.Relative(now.ToUnixTimeMilliseconds() + 30_000, now)); // a clock a bit ahead
        Assert.Equal(Strings.LastSeenMinutes(5), NotesTime.Relative(Ago(5 * 60_000 + 1), now));
        Assert.Equal(Strings.LastSeenHours(3), NotesTime.Relative(Ago(3 * 3_600_000L), now));
        Assert.Equal(Strings.LastSeenDays(2), NotesTime.Relative(Ago(2 * 86_400_000L), now));
        Assert.Equal(StampText.DateOnly((now.ToUnixTimeMilliseconds() - 10 * 86_400_000L) / 1000), NotesTime.Relative(Ago(10 * 86_400_000L), now));
        Assert.Equal(StampText.Local(1_790_000_000), NotesTime.Stamp(1_790_000_000_999));
    }

    [Fact]
    public void Rows_ShowTitleFallback_Badge_AndRelativeTime()
    {
        var now = DateTimeOffset.FromUnixTimeMilliseconds(1_800_000_000_000);
        var row = new NoteRow(new NoteListItem("nt_a", "", now.ToUnixTimeMilliseconds() - 120_000, false, "预览", "nt_orig"), now);
        Assert.Equal(Strings.Untitled, row.Title);
        Assert.Equal(Strings.NoteConflictBadge, row.ConflictBadge);
        Assert.True(row.HasBadge);
        Assert.True(row.HasPreview);
        Assert.Equal(Strings.LastSeenMinutes(2), row.TimeText);

        var plain = new NoteRow(new NoteListItem("nt_b", "标题", 0, false, "", ""), now);
        Assert.Equal("标题", plain.Title);
        Assert.False(plain.HasBadge);
        Assert.False(plain.HasPreview);
    }

    [Fact]
    public void RevisionRows_NameTheOrigin()
    {
        string Name(string id) => id == "dev_p" ? "Pixel" : "";
        NoteRevision Rev(string origin, bool deleted = false, string title = "T") => new("r", title, "h", origin, 1_790_000_000_000, Array.Empty<string>(), deleted);

        Assert.Equal(Strings.NoteOriginThisDevice, new NoteRevisionRow(Rev("dev_me"), "dev_me", Name).OriginText);
        Assert.Equal(Strings.NoteOriginMerge, new NoteRevisionRow(Rev("merge"), "dev_me", Name).OriginText);
        Assert.Equal("Pixel", new NoteRevisionRow(Rev("dev_p"), "dev_me", Name).OriginText);
        Assert.Equal(Strings.NoteOriginUnknown, new NoteRevisionRow(Rev("dev_gone"), "dev_me", Name).OriginText);
        Assert.Equal(Strings.NoteOriginUnknown, new NoteRevisionRow(Rev(""), "", Name).OriginText);
        Assert.Equal(Strings.NoteRevisionDeleted, new NoteRevisionRow(Rev("dev_me", deleted: true), "dev_me", Name).Title);
        Assert.Equal(Strings.Untitled, new NoteRevisionRow(Rev("dev_me", title: ""), "dev_me", Name).Title);
        Assert.Equal(StampText.Local(1_790_000_000), new NoteRevisionRow(Rev("dev_me"), "dev_me", Name).TimeText);
    }

    [Theory]
    [InlineData("a\r\nb\r\nc", 0, 0)]
    [InlineData("a\r\nb\r\nc", 1, 1)]
    [InlineData("a\r\nb\r\nc", 3, 2)]   // 'b'
    [InlineData("a\r\nb\r\nc", 6, 4)]   // 'c'
    [InlineData("a\r\nb\r\nc", 7, 5)]   // the end
    [InlineData("no breaks", 4, 4)]
    [InlineData("\n\n", 2, 2)]           // a bare "\n" is already one position
    public void TextOffsets_CountEachCrLfOnce(string text, int index, int position)
    {
        Assert.Equal(position, TextOffsets.ToBox(text, index));
        Assert.Equal(index, TextOffsets.FromBox(text, position));
    }

    [Fact]
    public void PreviewStyle_MarkersAndTables()
    {
        RenderBlock List(bool ordered, int start, params bool?[] checks) => RenderBlock.FromJson(VJ.Parse(
            $$"""{"kind":"list","ordered":{{(ordered ? "true" : "false")}},"start":{{start}},"items":[{{string.Join(",", checks.Select(c => c is null ? "{}" : "{\"checked\":" + (c.Value ? "true" : "false") + "}"))}}]}"""));

        var bullets = List(false, 1, null, null);
        Assert.Equal("•", NotePreviewStyle.Marker(bullets, 0, 0));
        Assert.Equal("◦", NotePreviewStyle.Marker(bullets, 0, 1));
        Assert.Equal("▪", NotePreviewStyle.Marker(bullets, 0, 2));
        Assert.Equal("•", NotePreviewStyle.Marker(bullets, 0, 3));

        var numbered = List(true, 3, null, null);
        Assert.Equal("3.", NotePreviewStyle.Marker(numbered, 0, 0));
        Assert.Equal("4.", NotePreviewStyle.Marker(numbered, 1, 0));

        var tasks = List(false, 1, true, false);
        Assert.Equal("☑", NotePreviewStyle.Marker(tasks, 0, 0));
        Assert.Equal("☐", NotePreviewStyle.Marker(tasks, 1, 0));

        Assert.True(NotePreviewStyle.HeadingSize(1) > NotePreviewStyle.HeadingSize(2));
        Assert.True(NotePreviewStyle.HeadingSize(6) >= NotePreviewStyle.BodySize);
    }
}
