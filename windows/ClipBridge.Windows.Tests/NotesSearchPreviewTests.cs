using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class DebouncedWorkTests
{
    private static readonly DateTimeOffset T0 = new(2030, 1, 1, 12, 0, 0, TimeSpan.Zero);

    private static DateTimeOffset At(int ms) => T0.AddMilliseconds(ms);

    [Fact]
    public void AValue_IsDueOnlyAfterTheDelay_AndOnlyOnce()
    {
        var w = new DebouncedWork<string>(150);
        w.Post("a", At(0));
        Assert.Null(w.TakeDue(At(149)));
        var job = w.TakeDue(At(150));
        Assert.NotNull(job);
        Assert.Equal("a", job!.Value);
        Assert.Null(w.TakeDue(At(1000)));                 // taken: nothing left to do
        Assert.False(w.HasPending);
    }

    [Fact]
    public void EveryNewValue_RestartsTheWait_AndOnlyTheNewestIsWorkedOn()
    {
        var w = new DebouncedWork<string>(150);
        for (var i = 0; i < 50; i++)
        {
            w.Post("v" + i, At(i * 100));                 // a burst with 100 ms between values
            Assert.Null(w.TakeDue(At(i * 100)));
        }
        Assert.Equal("v49", w.TakeDue(At(49 * 100 + 150))!.Value);
    }

    [Fact]
    public void PostNow_NeedsNoWaiting()
    {
        var w = new DebouncedWork<string>(150);
        w.PostNow("a", At(0));
        Assert.Equal("a", w.TakeDue(At(0))!.Value);
    }

    [Fact]
    public void OnlyOneJobRunsAtATime_AndAnAnswerOvertakenByANewerValueIsStale()
    {
        var w = new DebouncedWork<string>(150);
        w.Post("old", At(0));
        var first = w.TakeDue(At(150))!;
        Assert.True(w.Busy);

        w.Post("new", At(160));
        Assert.Null(w.TakeDue(At(1000)));                 // the first is still running
        Assert.False(w.Finish(first));                    // its answer is stale: the newer value overtook it
        Assert.False(w.Busy);

        var second = w.TakeDue(At(1000))!;
        Assert.Equal("new", second.Value);
        Assert.True(w.Finish(second));                    // nothing newer: this answer may be shown
    }

    [Fact]
    public void AnAnswerIsCurrent_WhenNothingWasPostedMeanwhile()
    {
        var w = new DebouncedWork<string>(150);
        w.Post("a", At(0));
        var job = w.TakeDue(At(150))!;
        Assert.True(w.Finish(job));
        Assert.False(w.Finish(job));                      // a job can be finished only once
    }

    [Fact]
    public void Cancel_DropsTheWaitingValue_AndMakesARunningAnswerStale()
    {
        var w = new DebouncedWork<string>(150);
        w.Post("a", At(0));
        w.Cancel();
        Assert.Null(w.TakeDue(At(1000)));

        w.Post("b", At(2000));
        var job = w.TakeDue(At(2150))!;
        w.Cancel();
        Assert.False(w.Finish(job));
    }

    [Fact]
    public void DueAt_ShowsWhenTheWaitingValueWillRun()
    {
        var w = new DebouncedWork<string>(150);
        Assert.Null(w.DueAt);
        w.Post("a", At(10));
        Assert.Equal(At(160), w.DueAt);
    }
}

public class LivePreviewTests
{
    private static readonly DateTimeOffset T0 = new(2030, 1, 1, 12, 0, 0, TimeSpan.Zero);

    private static DateTimeOffset At(int ms) => T0.AddMilliseconds(ms);

    [Fact]
    public void TheTextIsRenderedAbout150MsAfterTheLastKeystroke()
    {
        Assert.Equal(150, LivePreview.DebounceMs);
        var p = new LivePreview();
        p.Request("# Hi", At(0));
        Assert.Null(p.TakeDue(At(149)));
        var job = p.TakeDue(At(150));
        Assert.Equal("# Hi", job!.Value);
    }

    [Fact]
    public void ContinuousTyping_RendersNothingUntilThePause()
    {
        var p = new LivePreview();
        var rendered = 0;
        for (var i = 1; i <= 100; i++)
        {
            p.Request(new string('x', i), At(i * 100));
            if (p.TakeDue(At(i * 100)) is not null) rendered++;
        }
        Assert.Equal(0, rendered);
        Assert.Equal(new string('x', 100), p.TakeDue(At(100 * 100 + 150))!.Value);
    }

    [Fact]
    public void TheSameTextTwice_IsRenderedOnce()
    {
        var p = new LivePreview();
        p.Request("same", At(0));
        var job = p.TakeDue(At(200))!;
        Assert.True(p.Accept(job));
        p.Request("same", At(300));                        // e.g. an edit that changed nothing, or a reload with the same text
        Assert.Null(p.TakeDue(At(1000)));
    }

    [Fact]
    public void ASlowRender_IsDropped_WhenTheTextMovedOn()
    {
        var p = new LivePreview();
        p.Request("one", At(0));
        var slow = p.TakeDue(At(150))!;                     // the render of "one" is under way
        p.Request("one two", At(200));
        Assert.Null(p.TakeDue(At(1000)));                   // one render at a time
        Assert.False(p.Accept(slow));                       // "one" is stale: it must not replace the preview
        Assert.Equal("one two", p.TakeDue(At(1000))!.Value);
    }

    [Fact]
    public void AFailedRender_KeepsTheLastTree_AndTheSameTextCanBeTriedAgain()
    {
        var p = new LivePreview();
        p.Request("text", At(0));
        var job = p.TakeDue(At(150))!;
        p.Fail(job);
        p.Request("text", At(500));                         // not skipped as "already rendered"
        Assert.Equal("text", p.TakeDue(At(650))!.Value);
    }

    [Fact]
    public void AFailureOfAnOldText_DoesNotForgetTheNewestRequest()
    {
        var p = new LivePreview();
        p.Request("old", At(0));
        var job = p.TakeDue(At(150))!;
        p.Request("new", At(160));
        p.Fail(job);                                        // stale failure
        p.Request("new", At(170));                          // still the same text as requested: nothing new to do
        Assert.Equal("new", p.TakeDue(At(400))!.Value);
        Assert.Null(p.TakeDue(At(500)));
    }

    [Fact]
    public void Reset_ForAnotherNote_DropsWaitingTextAndRunningAnswers()
    {
        var p = new LivePreview();
        p.Request("note A", At(0));
        var running = p.TakeDue(At(150))!;
        p.Request("note A, more", At(160));
        p.Reset();

        Assert.False(p.Accept(running));
        Assert.Null(p.TakeDue(At(1000)));
        p.Request("note A, more", At(1100));                // forgotten: asked for again for the new note
        Assert.NotNull(p.TakeDue(At(1250)));
    }

    [Fact]
    public void Immediately_SkipsTheWait()
    {
        var p = new LivePreview();
        p.Request("opened", At(0), immediately: true);
        Assert.Equal("opened", p.TakeDue(At(0))!.Value);
    }
}

public class CodePointTextTests
{
    private static string Cut(string text, params (int Start, int End)[] spans) =>
        string.Join("|", spans.Select(s => text[s.Start..s.End]));

    [Fact]
    public void Ascii_IsTheSameInBothUnits()
    {
        var text = "hello world";
        var spans = CodePointText.ToUtf16(text, new[] { new CodePointRange(6, 11) });
        Assert.Equal("world", Cut(text, spans.ToArray()));
    }

    [Fact]
    public void AnEmoji_IsOneCodePoint_ButTwoUtf16Units()
    {
        const string text = "😀 hello";
        Assert.Equal(7, CodePointText.Count(text));
        Assert.Equal(8, text.Length);
        // the core says "hello" is code points 2..7; in UTF-16 that is 3..8
        var spans = CodePointText.ToUtf16(text, new[] { new CodePointRange(2, 7) });
        Assert.Equal((3, 8), spans.Single());
        Assert.Equal("hello", Cut(text, spans.ToArray()));
        // read as UTF-16 positions the same numbers would cut the wrong letters
        Assert.NotEqual("hello", text[2..7]);
    }

    [Fact]
    public void ManyAstralCharacters_BeforeAndInsideTheHit()
    {
        const string text = "𝒜𝒷𝒸 日本語 😀x😀 end";
        var cps = CodePointText.Count(text);
        Assert.Equal(text.Length - 5, cps);                 // five characters outside the BMP (𝒜 𝒷 𝒸 😀 😀) take two units each
        var start = text.EnumerateRunes().Select(r => r.ToString()).ToList().IndexOf("x");
        var spans = CodePointText.ToUtf16(text, new[] { new CodePointRange(start - 1, start + 2) });
        Assert.Equal("😀x😀", Cut(text, spans.ToArray()));
    }

    [Fact]
    public void Cjk_NeedsNoConversion_ButStillWorks()
    {
        const string text = "你好世界 hello";
        var spans = CodePointText.ToUtf16(text, new[] { new CodePointRange(0, 2), new CodePointRange(5, 10) });
        Assert.Equal("你好|hello", Cut(text, spans.ToArray()));
    }

    [Fact]
    public void RangesThatMakeNoSense_AreClippedOrDropped()
    {
        const string text = "abc";
        Assert.Empty(CodePointText.ToUtf16(text, new[] { new CodePointRange(2, 2) }));          // empty
        Assert.Empty(CodePointText.ToUtf16(text, new[] { new CodePointRange(3, 1) }));          // backwards
        Assert.Empty(CodePointText.ToUtf16(text, new[] { new CodePointRange(7, 9) }));          // beyond the end
        Assert.Empty(CodePointText.ToUtf16("", new[] { new CodePointRange(0, 4) }));
        Assert.Equal((1, 3), CodePointText.ToUtf16(text, new[] { new CodePointRange(1, 99) }).Single());   // clipped to the end
        Assert.Equal((0, 2), CodePointText.ToUtf16(text, new[] { new CodePointRange(-5, 2) }).Single());   // clipped to the start
    }

    [Fact]
    public void Ranges_AreSorted_AndOverlappingOrTouchingOnesAreMerged()
    {
        const string text = "0123456789";
        var spans = CodePointText.ToUtf16(text, new[]
        {
            new CodePointRange(6, 8), new CodePointRange(1, 3), new CodePointRange(2, 4), new CodePointRange(4, 5), new CodePointRange(9, 10),
        });
        Assert.Equal(new[] { (1, 5), (6, 8), (9, 10) }, spans.ToArray());
    }

    [Fact]
    public void ALoneSurrogate_CountsAsOneCodePoint_AndNothingThrows()
    {
        var text = "a\ud83dbc";                              // a high surrogate without its partner
        Assert.Equal(4, CodePointText.Count(text));
        var spans = CodePointText.ToUtf16(text, new[] { new CodePointRange(1, 3) });
        Assert.Equal((1, 3), spans.Single());
    }

    [Fact]
    public void Segments_CoverTheWholeText_InOrder()
    {
        const string text = "😀 hello 世界 hello";
        var hello1 = new CodePointRange(2, 7);
        var hello2 = new CodePointRange(11, 16);
        var segments = CodePointText.Segments(text, new[] { hello1, hello2 });

        Assert.Equal(text, string.Concat(segments.Select(s => s.Text)));
        Assert.Equal(new[] { "😀 ", "hello", " 世界 ", "hello" }, segments.Select(s => s.Text).ToArray());
        Assert.Equal(new[] { false, true, false, true }, segments.Select(s => s.Highlighted).ToArray());
    }

    [Fact]
    public void Segments_WithoutRanges_AreOnePlainPiece()
    {
        var segments = CodePointText.Segments("plain", Array.Empty<CodePointRange>());
        Assert.Equal(new TextSegment("plain", false), segments.Single());
        Assert.Empty(CodePointText.Segments("", new[] { new CodePointRange(0, 3) }));
        Assert.Empty(CodePointText.Plain(""));
        Assert.Equal(new TextSegment("x", false), CodePointText.Plain("x").Single());
    }

    [Fact]
    public void Segments_HighlightAtTheEdges_HaveNoEmptyPieces()
    {
        var segments = CodePointText.Segments("abc", new[] { new CodePointRange(0, 3) });
        Assert.Equal(new TextSegment("abc", true), segments.Single());
    }
}

public class NoteSearchTests
{
    private static NoteSearchPage Page(string json) => NoteSearchPage.FromJson(VJ.Parse(json));

    private static NoteSearchHit Hit(string id, string title = "T") => new(id, title, 1, "snippet", Array.Empty<CodePointRange>(), Array.Empty<CodePointRange>(), "");

    [Fact]
    public void Hits_ParseTitleSnippetHighlightsAndConflictCopies()
    {
        var page = Page("""
            {"items":[
              {"note_id":"nt_a","title":"买菜清单","updated_at":1790000000123,"snippet":"…牛奶 鸡蛋 😀 面包…",
               "highlights":[{"start":3,"end":5},{"start":9,"end":11}],"title_highlights":[{"start":0,"end":2}]},
              {"note_id":"nt_b","title":"","snippet":"s","conflict_of":"nt_a"},
              {"title":"no id"}],
             "next_cursor":"c2"}
            """);

        Assert.Equal(2, page.Items.Count);                     // a hit without an id is dropped
        var a = page.Items[0];
        Assert.Equal(("nt_a", "买菜清单", 1790000000123L), (a.NoteId, a.Title, a.UpdatedAt));
        Assert.Equal(new[] { new CodePointRange(3, 5), new CodePointRange(9, 11) }, a.Highlights.ToArray());
        Assert.Equal(new CodePointRange(0, 2), a.TitleHighlights.Single());
        Assert.False(a.IsConflictCopy);
        Assert.True(page.Items[1].IsConflictCopy);
        Assert.Empty(page.Items[1].Highlights);
        Assert.True(page.HasMore);
        Assert.Equal("c2", page.NextCursor);
    }

    [Fact]
    public void Hits_ToleratesMissingWrongAndExtraFields()
    {
        var page = Page("""{"items":[{"note_id":"nt_a","highlights":"no","title_highlights":[1,{"start":"x","end":4},{"start":-3,"end":2}],"future":1}]}""");
        var hit = page.Items.Single();
        Assert.Empty(hit.Highlights);
        Assert.Equal(new[] { new CodePointRange(0, 4), new CodePointRange(0, 2) }, hit.TitleHighlights.ToArray());   // non-numbers read as 0, negatives as 0
        Assert.False(page.HasMore);
        Assert.Empty(NoteSearchPage.FromJson(VJ.Parse("{}")).Items);
    }

    [Fact]
    public void TheSearchCall_SendsQueryLimitAndCursor()
    {
        var core = new ScriptedCore().On("notes.search", """{"items":[]}""");
        new NotesApi(core).Search("买菜 milk", 30, "c1");
        var p = core.Calls.Single().Params;
        Assert.Equal(("买菜 milk", 30, "c1"), (p.GetProperty("query").GetString(), p.GetProperty("limit").GetInt32(), p.GetProperty("cursor").GetString()));
        new NotesApi(core).Search("x");
        Assert.False(core.Calls[1].Params.TryGetProperty("cursor", out _));
        Assert.Equal(NoteSearchResults.PageSize, core.Calls[1].Params.GetProperty("limit").GetInt32());
    }

    [Fact]
    public void Results_ReplaceTheFirstPage_AndAppendTheNext_WithoutDuplicates()
    {
        var results = new NoteSearchResults();
        Assert.False(results.Active);
        results.Replace("milk", new NoteSearchPage(new[] { Hit("nt_1"), Hit("nt_2") }, "c1"));
        Assert.True(results.Active);
        Assert.True(results.HasMore);

        Assert.True(results.Append("milk", new NoteSearchPage(new[] { Hit("nt_2"), Hit("nt_3") }, "")));   // nt_2 moved between pages
        Assert.Equal(new[] { "nt_1", "nt_2", "nt_3" }, results.Items.Select(i => i.NoteId).ToArray());
        Assert.False(results.HasMore);
    }

    [Fact]
    public void ALatePageOfAnOlderQuery_IsIgnored()
    {
        var results = new NoteSearchResults();
        results.Replace("milk", new NoteSearchPage(new[] { Hit("nt_1") }, "c1"));
        results.Replace("milky way", new NoteSearchPage(new[] { Hit("nt_9") }, ""));

        Assert.False(results.Append("milk", new NoteSearchPage(new[] { Hit("nt_2") }, "c2")));
        Assert.Equal(new[] { "nt_9" }, results.Items.Select(i => i.NoteId).ToArray());
        Assert.False(results.HasMore);
    }

    [Fact]
    public void Clear_EndsTheSearch()
    {
        var results = new NoteSearchResults();
        results.Replace("milk", new NoteSearchPage(new[] { Hit("nt_1") }, "c1"));
        results.Clear();
        Assert.False(results.Active);
        Assert.Empty(results.Items);
        Assert.False(results.HasMore);
    }

    [Theory]
    [InlineData("  milk  ", "milk")]
    [InlineData("", "")]
    [InlineData("   ", "")]
    [InlineData(null, "")]
    public void TheQuery_IsTrimmed(string? typed, string sent) => Assert.Equal(sent, NoteSearchResults.Normalize(typed));

    [Fact]
    public void TheQuery_IsCutAtTheLengthOfTheBox() =>
        Assert.Equal(SearchInput.MaxChars, NoteSearchResults.Normalize(new string('a', 500)).Length);

    [Fact]
    public void ARow_ForAHit_EmphasisesTheWordsInTitleAndSnippet()
    {
        var now = DateTimeOffset.FromUnixTimeMilliseconds(1_800_000_000_000);
        var hit = new NoteSearchHit("nt_a", "😀 milk list", now.ToUnixTimeMilliseconds() - 120_000, "buy milk and eggs",
            new[] { new CodePointRange(4, 8) }, new[] { new CodePointRange(2, 6) }, "nt_orig");
        var row = new NoteRow(hit, now);

        Assert.Equal("nt_a", row.NoteId);
        Assert.Equal(new[] { "😀 ", "milk", " list" }, row.TitleSegments.Select(s => s.Text).ToArray());
        Assert.Equal(new[] { false, true, false }, row.TitleSegments.Select(s => s.Highlighted).ToArray());
        Assert.Equal(new[] { "buy ", "milk", " and eggs" }, row.PreviewSegments.Select(s => s.Text).ToArray());
        Assert.Equal(Strings.NoteConflictBadge, row.ConflictBadge);        // conflict copies are results too
        Assert.Equal(Strings.LastSeenMinutes(2), row.TimeText);
    }

    [Fact]
    public void ARow_ForAHitWithoutTitle_ShowsUntitled_AndRowsOfTheListAreOnePlainPiece()
    {
        var now = DateTimeOffset.FromUnixTimeMilliseconds(1_800_000_000_000);
        var untitled = new NoteRow(new NoteSearchHit("nt_a", "", 0, "", Array.Empty<CodePointRange>(), Array.Empty<CodePointRange>(), ""), now);
        Assert.Equal(Strings.Untitled, untitled.Title);
        Assert.Equal(new TextSegment(Strings.Untitled, false), untitled.TitleSegments.Single());
        Assert.Empty(untitled.PreviewSegments);

        var plain = new NoteRow(new NoteListItem("nt_b", "标题", 0, false, "预览", ""), now);
        Assert.Equal(new TextSegment("标题", false), plain.TitleSegments.Single());
        Assert.Equal(new TextSegment("预览", false), plain.PreviewSegments.Single());
        Assert.Equal(2, NoteRow.From(new[] { new NoteSearchHit("nt_a", "a", 0, "", Array.Empty<CodePointRange>(), Array.Empty<CodePointRange>(), ""), new NoteSearchHit("nt_b", "b", 0, "", Array.Empty<CodePointRange>(), Array.Empty<CodePointRange>(), "") }, now).Count);
    }
}

public class SoftBreakTests
{
    [Fact]
    public void BothKindsOfLineBreak_AreShownAsLineBreaks()
    {
        Assert.True(NotePreviewStyle.IsLineBreak(InlineKind.SoftBreak));
        Assert.True(NotePreviewStyle.IsLineBreak(InlineKind.HardBreak));
        foreach (var kind in Enum.GetValues<InlineKind>().Where(k => k is not (InlineKind.SoftBreak or InlineKind.HardBreak)))
            Assert.False(NotePreviewStyle.IsLineBreak(kind));
    }
}
