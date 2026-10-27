using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

internal static class ND
{
    public static NoteDetail Note(string rev = "r_1", string title = "", string body = "", bool deleted = false, string id = "nt_a") =>
        new(id, title, body, 1, rev, Array.Empty<string>(), 1, 2, deleted, "dev_me", 1, true, "");

    public static NoteSaveResult Saved(string rev, bool merged = false, string conflict = "") => new(rev, merged, conflict);
}

public class NoteEditorModelTests
{
    private static readonly DateTimeOffset T0 = new(2030, 1, 1, 12, 0, 0, TimeSpan.Zero);

    private static NoteEditorModel Open(string body = "alpha\n", string title = "T", bool deleted = false)
    {
        var m = new NoteEditorModel();
        m.Load(ND.Note(title: title, body: body, deleted: deleted));
        return m;
    }

    private static SaveRequest Take(NoteEditorModel m, DateTimeOffset at) =>
        m.TakeDue(at) ?? throw new Xunit.Sdk.XunitException("a save should have been due");

    // ---- loading and dirtiness -----------------------------------------------------------------------------

    [Fact]
    public void Load_MakesTheRevisionTheBase_AndIsClean()
    {
        var m = Open();
        Assert.True(m.IsOpen);
        Assert.Equal(("nt_a", "r_1", "T", "alpha\n"), (m.NoteId, m.BaseRevId, m.Title, m.Body));
        Assert.False(m.Dirty);
        Assert.Equal(EditorSaveState.Saved, m.State);
        Assert.Null(m.DueAt);
    }

    [Fact]
    public void NothingIsOpenBeforeLoad_AndEditsAreIgnored()
    {
        var m = new NoteEditorModel();
        Assert.False(m.IsOpen);
        Assert.False(m.Edit("a", "b", T0));
        Assert.Null(m.TakeNow());
        Assert.Equal(EditorSaveState.Saved, m.State);
    }

    [Fact]
    public void Edit_MarksDirty_AndLineBreaksAreNormalised()
    {
        var m = Open("a\nb\n");
        Assert.False(m.Edit("T", "a\r\nb\r\n", T0));     // the same text with the line breaks of the text box
        Assert.False(m.Dirty);
        Assert.True(m.Edit("T", "a\r\nb\r\nc\r", T0));
        Assert.Equal("a\nb\nc\n", m.Body);
        Assert.True(m.Dirty);
        Assert.Equal(EditorSaveState.Dirty, m.State);
    }

    [Fact]
    public void TypingBackToTheSavedText_IsCleanAgain_AndCancelsTheSave()
    {
        var m = Open("abc");
        m.Edit("T", "abcd", T0);
        Assert.NotNull(m.DueAt);
        m.Edit("T", "abc", T0.AddMilliseconds(100));
        Assert.False(m.Dirty);
        Assert.Null(m.DueAt);
        Assert.Null(m.TakeNow());
    }

    // ---- debounce ---------------------------------------------------------------------------------------------

    [Fact]
    public void SaveIsDue500MsAfterTheLastEdit()
    {
        var m = Open();
        m.Edit("T", "alpha\nb", T0);
        Assert.Equal(T0.AddMilliseconds(500), m.DueAt);
        Assert.Null(m.TakeDue(T0.AddMilliseconds(499)));
        Assert.NotNull(m.TakeDue(T0.AddMilliseconds(500)));
    }

    [Fact]
    public void EveryKeystrokeRestartsTheWait_SoTypingMakesNoRevisionStorm()
    {
        var m = Open("");
        var saves = 0;
        var now = T0;
        for (var i = 1; i <= 100; i++)
        {
            now = now.AddMilliseconds(120);                  // a fast typist
            m.Edit("T", new string('x', i), now);
            if (m.TakeDue(now) is not null) saves++;
        }
        Assert.Equal(0, saves);                              // 12 seconds of typing and not a single save
        Assert.Equal(1, Count(m, now.AddMilliseconds(500)));  // one save after the pause
    }

    private static int Count(NoteEditorModel m, DateTimeOffset at) => m.TakeDue(at) is null ? 0 : 1;

    [Fact]
    public void OnlyOneSaveRunsAtATime()
    {
        var m = Open();
        m.Edit("T", "one", T0);
        var first = Take(m, T0.AddSeconds(1));
        m.Edit("T", "one two", T0.AddSeconds(2));
        Assert.Null(m.TakeDue(T0.AddSeconds(5)));            // the first has not finished
        Assert.Null(m.TakeNow());
        Assert.True(m.IsSaving);
        Assert.Equal(EditorSaveState.Saving, m.State);

        m.Complete(first, ND.Saved("r_2"), ownChild: true);
        var second = Take(m, T0.AddSeconds(5));
        Assert.Equal("r_2", second.BaseRevId);               // the answer of the first became the base of the second
        Assert.Equal("one two", second.BodyChange);
    }

    [Fact]
    public void TakeNow_IgnoresTheDebounce_ForFocusLossSwitchingAndClosing()
    {
        var m = Open();
        m.Edit("T", "typed", T0);
        var req = m.TakeNow();
        Assert.NotNull(req);
        Assert.Null(m.DueAt);
    }

    [Fact]
    public void TheRequest_CarriesTheBase_AndOnlyWhatChanged()
    {
        var m = Open("body\n", "Title");
        m.Edit("New title", "body\n", T0);
        var titleOnly = m.TakeNow()!;
        Assert.Equal(("nt_a", "r_1", "New title", null), (titleOnly.NoteId, titleOnly.BaseRevId, titleOnly.TitleChange, titleOnly.BodyChange));
        m.Complete(titleOnly, ND.Saved("r_2"), true);

        m.Edit("New title", "body\nmore\n", T0);
        var bodyOnly = m.TakeNow()!;
        Assert.Equal(("r_2", null, "body\nmore\n"), (bodyOnly.BaseRevId, bodyOnly.TitleChange, bodyOnly.BodyChange));
        m.Complete(bodyOnly, ND.Saved("r_3"), true);

        m.Edit("", "body\nmore\n", T0);                       // clearing the title is a change
        Assert.Equal("", m.TakeNow()!.TitleChange);
    }

    // ---- results ------------------------------------------------------------------------------------------------

    [Fact]
    public void ASuccessfulSave_MakesTheReturnedRevisionTheBase_AndTheTextClean()
    {
        var m = Open();
        m.Edit("T", "new", T0);
        var req = m.TakeNow()!;
        var outcome = m.Complete(req, ND.Saved("r_2"), true);
        Assert.Equal(SaveOutcomeKind.Saved, outcome.Kind);
        Assert.Equal("r_2", m.BaseRevId);
        Assert.False(m.Dirty);
        Assert.Equal(EditorSaveState.Saved, m.State);
    }

    [Fact]
    public void TextTypedDuringASave_StaysDirty_AndIsSavedNext()
    {
        var m = Open();
        m.Edit("T", "first", T0);
        var req = m.TakeNow()!;
        m.Edit("T", "first and more", T0.AddMilliseconds(30));
        m.Complete(req, ND.Saved("r_2"), true);

        Assert.True(m.Dirty);
        Assert.Equal("first and more", m.Body);               // nothing typed is lost
        var next = Take(m, T0.AddMilliseconds(600));
        Assert.Equal(("r_2", "first and more"), (next.BaseRevId, next.BodyChange));
    }

    [Fact]
    public void AMergedAnswer_AsksForAReload_AndLeavesBaseAndTextAlone()
    {
        var m = Open();
        m.Edit("T", "mine", T0);
        var req = m.TakeNow()!;
        var outcome = m.Complete(req, ND.Saved("r_m", merged: true), true);

        Assert.Equal(SaveOutcomeKind.NeedsReload, outcome.Kind);
        Assert.True(outcome.Merged);
        Assert.Equal("r_m", outcome.RevId);
        Assert.Equal("r_1", m.BaseRevId);
        Assert.Equal("mine", m.Body);

        var result = m.ApplyReload(ND.Note("r_m", "T", "mine + theirs\n"));
        Assert.Equal(ReloadResult.Replaced, result);
        Assert.Equal(("r_m", "mine + theirs\n"), (m.BaseRevId, m.Body));
        Assert.False(m.Dirty);
    }

    [Fact]
    public void AConflictCopy_AsksForAReload_AndNamesTheCopy()
    {
        var m = Open();
        m.Edit("T", "mine", T0);
        var outcome = m.Complete(m.TakeNow()!, ND.Saved("r_x", conflict: "nt_copy"), true);
        Assert.Equal(SaveOutcomeKind.NeedsReload, outcome.Kind);
        Assert.Equal("nt_copy", outcome.ConflictNoteId);
        Assert.False(outcome.Merged);
    }

    [Fact]
    public void ARevisionThatIsNotOurOwnChild_IsReloaded_NotAdoptedAsTheBase()
    {
        // The core answers with the newest revision when the text came out like an older base: it does not hold our edit.
        var m = Open();
        m.Edit("T", "typed", T0);
        var outcome = m.Complete(m.TakeNow()!, ND.Saved("r_head"), ownChild: false);
        Assert.Equal(SaveOutcomeKind.NeedsReload, outcome.Kind);
        Assert.Equal("r_1", m.BaseRevId);
        Assert.True(m.Dirty);
    }

    [Fact]
    public void AnAnswerWithoutARevisionId_IsReloaded()
    {
        var m = Open();
        m.Edit("T", "typed", T0);
        Assert.Equal(SaveOutcomeKind.NeedsReload, m.Complete(m.TakeNow()!, ND.Saved(""), true).Kind);
    }

    [Fact]
    public void ReloadAfterAMerge_KeepsWhatWasTypedMeanwhile()
    {
        var m = Open();
        m.Edit("T", "mine", T0);
        var req = m.TakeNow()!;
        m.Complete(req, ND.Saved("r_m", merged: true), true);
        m.Edit("T", "mine, still typing", T0.AddMilliseconds(20));

        var result = m.ApplyReload(ND.Note("r_m", "T", "mine + theirs\n"));
        Assert.Equal(ReloadResult.Kept, result);
        Assert.Equal("mine, still typing", m.Body);
        Assert.Equal("r_1", m.BaseRevId);                     // the next save merges again through the old base
        Assert.True(m.Dirty);
    }

    [Fact]
    public void ReloadIsIgnoredWhileASaveRuns_AndForOtherNotes()
    {
        var m = Open();
        m.Edit("T", "x", T0);
        var req = m.TakeNow()!;
        Assert.Equal(ReloadResult.Ignored, m.ApplyReload(ND.Note("r_9", "T", "other")));
        m.Complete(req, ND.Saved("r_2"), true);
        Assert.Equal(ReloadResult.Ignored, m.ApplyReload(ND.Note("r_9", id: "nt_other")));
    }

    [Fact]
    public void ReloadOfTheSameText_OnlyMovesTheBase()
    {
        var m = Open("same\n");
        Assert.Equal(ReloadResult.Unchanged, m.ApplyReload(ND.Note("r_5", "T", "same\n")));
        Assert.Equal("r_5", m.BaseRevId);
    }

    // ---- changes from elsewhere ---------------------------------------------------------------------------

    [Fact]
    public void RemoteChange_WhileClean_ReloadsSilently()
    {
        var m = Open();
        Assert.Equal(RemoteAction.Reload, m.OnRemoteChange("nt_a", "r_2"));
        Assert.Equal(ReloadResult.Replaced, m.ApplyReload(ND.Note("r_2", "T", "from the other device\n")));
        Assert.Equal("from the other device\n", m.Body);
        Assert.False(m.Dirty);
        Assert.Equal(RemoteAction.None, m.OnRemoteChange("nt_a", "r_2"));   // the same event again changes nothing
    }

    [Fact]
    public void RemoteChange_WhileTyping_LeavesTheTextAlone()
    {
        var m = Open();
        m.Edit("T", "typing", T0);
        Assert.Equal(RemoteAction.None, m.OnRemoteChange("nt_a", "r_2"));
        Assert.Equal("typing", m.Body);
        Assert.Equal("r_1", m.BaseRevId);                      // the save will name r_1 as its base, so the core merges
        Assert.Equal("r_1", Take(m, T0.AddSeconds(1)).BaseRevId);
    }

    [Fact]
    public void RemoteChange_ForOtherNotesOrOurOwnRevisions_IsIgnored()
    {
        var m = Open();
        Assert.Equal(RemoteAction.None, m.OnRemoteChange("nt_other", "r_2"));
        Assert.Equal(RemoteAction.None, m.OnRemoteChange("nt_a", "r_1"));   // the revision we show
        Assert.Equal(RemoteAction.None, m.OnRemoteChange("nt_a", ""));

        m.Edit("T", "x", T0);
        m.Complete(m.TakeNow()!, ND.Saved("r_2"), true);
        Assert.Equal(RemoteAction.None, m.OnRemoteChange("nt_a", "r_2"));   // the event of our own save
        Assert.Equal(RemoteAction.Reload, m.OnRemoteChange("nt_a", "r_3"));
    }

    [Fact]
    public void TheEventOfOurOwnSave_ArrivingBeforeItsAnswer_IsRecognisedAfterwards()
    {
        var m = Open();
        m.Edit("T", "x", T0);
        var req = m.TakeNow()!;
        Assert.Equal(RemoteAction.None, m.OnRemoteChange("nt_a", "r_2"));   // arrives first: held back
        var outcome = m.Complete(req, ND.Saved("r_2"), true);
        Assert.Equal("", outcome.PendingRemoteRev);                          // it was ours
    }

    [Fact]
    public void ARemoteRevisionSeenDuringASave_IsCheckedAgainAfterwards()
    {
        var m = Open();
        m.Edit("T", "x", T0);
        var req = m.TakeNow()!;
        m.OnRemoteChange("nt_a", "r_remote");
        var outcome = m.Complete(req, ND.Saved("r_2"), true);
        Assert.Equal("r_remote", outcome.PendingRemoteRev);
        Assert.Equal(RemoteAction.Reload, m.OnRemoteChange("nt_a", outcome.PendingRemoteRev));
    }

    // ---- failures -------------------------------------------------------------------------------------------------

    [Fact]
    public void AFailedSave_KeepsTheText_ShowsFailed_AndRetriesOnTheNextEditOrFlush()
    {
        var m = Open();
        m.Edit("T", "typed", T0);
        m.Fail(m.TakeNow()!, "DB_UNAVAILABLE");

        Assert.Equal(EditorSaveState.Failed, m.State);
        Assert.Equal("typed", m.Body);
        Assert.True(m.NeedsSave);
        Assert.Null(m.TakeDue(T0.AddMinutes(5)));              // no blind retry loop
        Assert.NotNull(m.TakeNow());                           // a flush tries again
    }

    [Fact]
    public void AfterAFailure_TheNextEditRearmsTheSave()
    {
        var m = Open();
        m.Edit("T", "typed", T0);
        m.Fail(m.TakeNow()!, "INTERNAL");
        m.Edit("T", "typed more", T0.AddSeconds(10));
        Assert.Equal(EditorSaveState.Dirty, m.State);
        Assert.NotNull(m.TakeDue(T0.AddSeconds(11)));
    }

    [Fact]
    public void ANoteThatWentToTheTrash_SuspendsSaving_UntilItIsRestored()
    {
        var m = Open();
        m.Edit("T", "typed", T0);
        m.Fail(m.TakeNow()!, "CONFLICT");

        Assert.True(m.InTrash);
        Assert.False(m.ReadOnly);                              // the text can still be edited and is not lost
        Assert.Equal(EditorSaveState.Trashed, m.State);
        m.Edit("T", "typed again", T0.AddSeconds(1));
        Assert.Null(m.TakeNow());
        Assert.False(m.IsSaving);

        m.MarkRestored(T0.AddSeconds(2));
        Assert.False(m.InTrash);
        Assert.NotNull(m.TakeDue(T0.AddSeconds(2)));           // typed text is saved at once
    }

    [Fact]
    public void ATooLargeText_IsNotSent()
    {
        var m = Open("");
        m.Edit("T", new string('x', NoteLimits.MaxBodyBytes + 1), T0);
        Assert.Equal(EditorSaveState.TooLarge, m.State);
        Assert.Null(m.DueAt);
        Assert.Null(m.TakeNow());
        Assert.True(m.NeedsSave);

        m.Edit("T", "small", T0.AddSeconds(1));                // shrinking it makes it savable again
        Assert.Equal(EditorSaveState.Dirty, m.State);
        Assert.NotNull(m.TakeNow());
    }

    [Fact]
    public void TheLimitsCountBytes_NotCharacters()
    {
        var cjkTitle = new string('字', NoteLimits.MaxTitleBytes / 3 + 1);   // 342 characters = 1026 bytes
        Assert.True(NoteLimits.IsTooLarge(cjkTitle, ""));
        Assert.False(NoteLimits.IsTooLarge(new string('字', NoteLimits.TitleBoxChars), ""));   // what the title box allows always fits
        Assert.False(NoteLimits.IsTooLarge("", new string('x', NoteLimits.MaxBodyBytes)));
        Assert.True(NoteLimits.IsTooLarge("", new string('字', NoteLimits.MaxBodyBytes / 3 + 1)));
    }

    // ---- recycle bin, closing -------------------------------------------------------------------------------------

    [Fact]
    public void ANoteOpenedFromTheTrash_IsReadOnly()
    {
        var m = Open(deleted: true);
        Assert.True(m.ReadOnly);
        Assert.True(m.InTrash);
        Assert.False(m.Edit("T", "x", T0));
        Assert.Null(m.TakeNow());
        Assert.False(m.NeedsSave);
        m.MarkRestored(T0);
        Assert.False(m.ReadOnly);
    }

    [Fact]
    public void ARemoteDeletion_OfACleanNote_MakesItReadOnly()
    {
        var m = Open();
        Assert.Equal(RemoteAction.Reload, m.OnRemoteChange("nt_a", "r_del"));
        m.ApplyReload(ND.Note("r_del", "T", "alpha\n", deleted: true));
        Assert.True(m.ReadOnly);
        Assert.Equal(EditorSaveState.Saved, m.State);
    }

    [Fact]
    public void ARemoteDeletion_WhileTyping_DoesNotThrowTheTextAway()
    {
        var m = Open();
        m.Edit("T", "typing", T0);
        var req = m.TakeNow()!;
        m.Complete(req, ND.Saved("r_m", merged: true), true);
        m.Edit("T", "typing on", T0.AddMilliseconds(10));
        Assert.Equal(ReloadResult.Kept, m.ApplyReload(ND.Note("r_m", "T", "", deleted: true)));
        Assert.Equal("typing on", m.Body);
        Assert.True(m.InTrash);
        Assert.Equal(EditorSaveState.Trashed, m.State);
    }

    [Fact]
    public void ALateAnswer_ForAClosedOrReplacedNote_IsStale()
    {
        var m = Open();
        m.Edit("T", "x", T0);
        var req = m.TakeNow()!;
        m.Load(ND.Note("r_9", id: "nt_b", body: "other"));
        Assert.Equal(SaveOutcomeKind.Stale, m.Complete(req, ND.Saved("r_2"), true).Kind);
        Assert.Equal("nt_b", m.NoteId);
        Assert.Equal("r_9", m.BaseRevId);
        m.Fail(req, "CONFLICT");                                // a late failure changes nothing either
        Assert.False(m.InTrash);

        m.Close();
        Assert.False(m.IsOpen);
        Assert.Equal(("", ""), (m.NoteId, m.Body));
        Assert.Null(m.TakeNow());
    }

    [Fact]
    public void ReloadFailure_OfCleanText_IsNotAnErrorState_ButOfTypedTextIs()
    {
        var m = Open();
        m.ReloadFailed();
        Assert.Equal(EditorSaveState.Saved, m.State);
        m.Edit("T", "typed", T0);
        m.ReloadFailed();
        Assert.Equal(EditorSaveState.Failed, m.State);
    }

    [Fact]
    public void CjkAndEmoji_AreKeptByteForByte()
    {
        var m = Open("");
        var text = "# 今日计划\n- 买菜 🥬\n- 写周报 ✍️\n";
        m.Edit("标题：周末", text, T0);
        var req = m.TakeNow()!;
        Assert.Equal((text, "标题：周末"), (req.BodyChange, req.TitleChange));
        m.Complete(req, ND.Saved("r_2"), true);
        Assert.False(m.Dirty);
    }
}
