using System.IO.Compression;
using System.Security.Cryptography;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

/// <summary>
/// The second round of notes features against the REAL core: live preview of unsaved text, the changed flag of notes.update,
/// search with code-point highlights, export and import, attachments. Every test uses words of its own, because the core is
/// shared by the whole test run.
/// </summary>
[Collection(CoreCollection.Name)]
public class NotesPhase2InteropTests : IDisposable
{
    private readonly NotesApi _api;
    private readonly TempDirs _dirs = new();

    public NotesPhase2InteropTests(NativeCoreFixture fx) => _api = new NotesApi(fx.Core);

    public void Dispose() => _dirs.Dispose();

    private static string Unique() => "zq" + Guid.NewGuid().ToString("N")[..10];

    private static string Code(Action a) => Assert.Throws<CoreException>(a).Code;

    // ---- live preview ---------------------------------------------------------------------------------

    [Fact]
    public void RenderBody_RendersUnsavedText_WithoutNoteOrRevision()
    {
        var tree = _api.RenderBody("# 标题\n\nfirst line\nsecond line\n\n- item\n");

        Assert.Equal(("", ""), (tree.NoteId, tree.RevId));
        Assert.Equal(BlockKind.Heading, tree.Blocks[0].Kind);
        var para = tree.Blocks.First(b => b.Kind == BlockKind.Paragraph).Inline;
        Assert.Contains(para, r => r.Kind == InlineKind.SoftBreak);            // a plain Enter is a soft break ...
        Assert.True(NotePreviewStyle.IsLineBreak(InlineKind.SoftBreak));        // ... which the preview draws as a line break
        Assert.Contains(tree.Blocks, b => b.Kind == BlockKind.List);
        Assert.NotEmpty(tree.Toc);
    }

    [Fact]
    public void RenderBody_OfAnEmptyOrUnfinishedText_Works()
    {
        Assert.Empty(_api.RenderBody("").Blocks);
        var open = _api.RenderBody("```go\nfmt.Println(1)");                    // a fence that is not closed yet
        Assert.Equal(BlockKind.Code, open.Blocks.Single().Kind);
        Assert.NotEmpty(_api.RenderBody("| a | b |\n| - | - |\n| 1").Blocks);     // half a table
    }

    [Fact]
    public void RenderBody_ShowsWhatTheNoteWillBecome_NotWhatIsSaved()
    {
        var created = _api.Create("live", "saved text");
        var live = _api.RenderBody("typed but not saved yet");

        Assert.Contains("typed", RenderInline.PlainText(live.Blocks[0].Inline));
        Assert.Contains("saved text", RenderInline.PlainText(_api.Render(created.NoteId).Blocks[0].Inline));
    }

    [Fact]
    public void RenderBody_ABlobImageComesThroughAsAnAttachmentAddress()
    {
        var blob = new string('9', 64);
        var tree = _api.RenderBody($"![shot](blob:{blob})\n\n[report.pdf](blob:{blob})\n");
        var image = tree.Blocks[0].Inline.Single(r => r.Kind == InlineKind.Image);
        Assert.True(BlobRef.TryParse(image.Href, out var id));
        Assert.Equal(blob, id);
        var link = tree.Blocks[1].Inline.Single(r => r.Kind == InlineKind.Link);
        Assert.True(BlobRef.TryParse(link.Href, out _));
        Assert.False(NoteLinks.IsOpenable(link.Href));
    }

    // ---- notes.update: changed ---------------------------------------------------------------------------

    [Fact]
    public void Update_SaysWhetherItStoredARevision()
    {
        var created = _api.Create("changed", "one");
        var same = _api.Update(created.NoteId, created.RevId, null, "one");
        Assert.False(same.Changed);
        Assert.Equal(created.RevId, same.RevId);

        var edited = _api.Update(created.NoteId, created.RevId, null, "two");
        Assert.True(edited.Changed);
        Assert.NotEqual(created.RevId, edited.RevId);
        Assert.False(edited.Merged);
    }

    [Fact]
    public void Update_WithAnOutdatedBase_AndNoChange_NamesTheNewestRevisionButSaysUnchanged()
    {
        var created = _api.Create("moved", "one");
        var elsewhere = _api.Update(created.NoteId, created.RevId, null, "edited elsewhere");

        var stale = _api.Update(created.NoteId, created.RevId, null, "one");      // the text equals the old base
        Assert.False(stale.Changed);
        Assert.Equal(elsewhere.RevId, stale.RevId);                               // not mistaken for the result of this edit
    }

    [Fact]
    public async Task Session_ANoOpEdit_WhileTheNoteMovedOn_ShowsTheNewerText()
    {
        var created = _api.Create("noop", "one");
        var now = DateTimeOffset.UtcNow;
        var session = new NoteEditorSession(new ThreadPoolNotesGateway(_api), () => now);
        var replaced = new List<(string Title, string Body)>();
        session.TextReplaced += (t, b) => replaced.Add((t, b));
        session.Open(_api.Get(created.NoteId));

        _api.Update(created.NoteId, created.RevId, null, "edited elsewhere");
        session.OnEdited("noop", "one\n  ");                                      // trailing blanks: normalises to the old base
        now += TimeSpan.FromMilliseconds(600);
        await session.TickAsync();

        Assert.Equal("edited elsewhere\n", Assert.Single(replaced).Body);          // the box shows what the note really holds
        Assert.False(session.Model.Dirty);
        Assert.Equal(_api.Get(created.NoteId).RevId, session.Model.BaseRevId);
    }

    [Fact]
    public async Task Session_ASave_NeedsNoExtraRead_AndTheBaseIsTheNewRevision()
    {
        var created = _api.Create("plain", "start");
        var now = DateTimeOffset.UtcNow;
        var session = new NoteEditorSession(new ThreadPoolNotesGateway(_api), () => now);
        session.Open(_api.Get(created.NoteId));
        session.OnEdited("plain", "start, more");
        now += TimeSpan.FromMilliseconds(600);
        await session.TickAsync();

        Assert.Equal(_api.Get(created.NoteId).RevId, session.Model.BaseRevId);
        Assert.NotEqual(created.RevId, session.Model.BaseRevId);
        Assert.False(session.Model.Dirty);
    }

    // ---- search -------------------------------------------------------------------------------------------

    [Fact]
    public void Search_FindsTitleAndBody_AndTheHighlightsLandOnTheWordsDespiteEmoji()
    {
        var word = Unique();
        var id = _api.Create($"😀😀 {word} list", $"买菜 😀 {word} and more {word.ToUpperInvariant()} 😀 end").NoteId;

        var page = _api.Search(word);
        var hit = Assert.Single(page.Items, h => h.NoteId == id);

        var titleWords = CodePointText.Segments(hit.Title, hit.TitleHighlights).Where(s => s.Highlighted).Select(s => s.Text).ToList();
        Assert.Equal(new[] { word }, titleWords);                                    // code points converted: not shifted by the emoji
        var snippetWords = CodePointText.Segments(hit.Snippet, hit.Highlights).Where(s => s.Highlighted).Select(s => s.Text.ToLowerInvariant()).ToList();
        Assert.NotEmpty(snippetWords);
        Assert.All(snippetWords, w => Assert.Equal(word, w));                         // case-insensitive hits, still the whole word
        // the numbers are NOT valid UTF-16 positions: reading them as such cuts the wrong letters
        var first = hit.TitleHighlights[0];
        Assert.NotEqual(word, hit.Title.Substring(first.Start, first.End - first.Start));
        Assert.InRange(hit.UpdatedAt, 1_500_000_000_000L, 5_000_000_000_000L);
    }

    [Fact]
    public void Search_NeedsEveryWord_FindsChinese_AndIgnoresTheTrash()
    {
        var a = Unique();
        var b = Unique();
        var both = _api.Create("both", $"{a} {b}").NoteId;
        var onlyA = _api.Create("only a", a).NoteId;
        var chinese = _api.Create("周末计划", $"去超市买{a}").NoteId;

        var ids = _api.Search($"{a} {b}").Items.Select(h => h.NoteId).ToList();
        Assert.Contains(both, ids);
        Assert.DoesNotContain(onlyA, ids);
        Assert.Contains(chinese, _api.Search("周末").Items.Select(h => h.NoteId));    // no word splitting needed
        Assert.Contains(chinese, _api.Search("超市买" + a).Items.Select(h => h.NoteId));

        _api.Delete(both);
        Assert.DoesNotContain(both, _api.Search($"{a} {b}").Items.Select(h => h.NoteId));
        Assert.Empty(_api.Search(Unique()).Items);
    }

    [Fact]
    public void Search_PagesWithTheCursor_AndTheResultsListCollectsThemAll()
    {
        var word = Unique();
        var created = Enumerable.Range(0, 23).Select(i => _api.Create($"paging {i}", $"{word} number {i}").NoteId).ToHashSet();

        var results = new NoteSearchResults();
        results.Replace(word, _api.Search(word, 10));
        Assert.Equal(10, results.Items.Count);
        Assert.True(results.HasMore);
        var pages = 1;
        while (results.HasMore)
        {
            Assert.True(results.Append(word, _api.Search(word, 10, results.NextCursor)));
            pages++;
        }
        Assert.Equal(3, pages);
        Assert.Equal(created, results.Items.Select(i => i.NoteId).ToHashSet());
        Assert.Equal(23, results.Items.Count);                                       // nothing twice, nothing missing
    }

    [Fact]
    public void Search_ConflictCopiesAreResultsToo()
    {
        var word = Unique();
        var created = _api.Create("conflicted", $"one paragraph {word}\n");
        _api.Update(created.NoteId, created.RevId, null, $"one paragraph {word}, theirs\n");
        var mine = _api.Update(created.NoteId, created.RevId, null, $"one paragraph {word}, mine\n");
        Assert.NotEqual("", mine.ConflictNoteId);

        var hits = _api.Search(word).Items;
        Assert.Contains(hits, h => h.NoteId == created.NoteId && !h.IsConflictCopy);
        var copy = Assert.Single(hits, h => h.NoteId == mine.ConflictNoteId);
        Assert.Equal(created.NoteId, copy.ConflictOf);
        Assert.Equal(Strings.NoteConflictBadge, new NoteRow(copy, DateTimeOffset.Now).ConflictBadge);
    }

    [Fact]
    public void Search_AnEmptyQueryIsInvalid_AndTheShellNeverAsksForIt()
    {
        Assert.Equal("INVALID", Code(() => _api.Search("")));
        Assert.Equal("INVALID", Code(() => _api.Search("   ")));
        Assert.Equal("", NoteSearchResults.Normalize("   "));
    }

    // ---- export and import --------------------------------------------------------------------------------

    [Fact]
    public void ExportThenImport_RoundTripsIntoTheSameCore()
    {
        var word = Unique();
        var live1 = _api.Create($"export one {word}", $"first body {word}\n\n- item\n").NoteId;
        var live2 = _api.Create($"导出 {word}", "第二篇 😀\n").NoteId;
        var trashed = _api.Create($"export trash {word}", $"in the trash {word}").NoteId;
        _api.Delete(trashed);

        var withoutTrash = NotesTransfer.ExportTo(_api, Path.Combine(_dirs.Root, "a.zip"), includeTrash: false);
        var withTrash = NotesTransfer.ExportTo(_api, Path.Combine(_dirs.Root, "b.zip"), includeTrash: true);
        Assert.True(withTrash.Count >= withoutTrash.Count + 1);
        Assert.True(withoutTrash.Bytes > 0);
        Assert.Equal(withTrash.Bytes, new FileInfo(withTrash.Path).Length);

        using (var zip = ZipFile.OpenRead(withTrash.Path))
        {
            Assert.Contains(zip.Entries, e => e.FullName.StartsWith("_trash/", StringComparison.Ordinal));
            var entry = zip.Entries.First(e => e.Name.Contains(word) && !e.FullName.StartsWith("_trash/", StringComparison.Ordinal));
            using var reader = new StreamReader(entry.Open());
            var text = reader.ReadToEnd();
            Assert.StartsWith("---", text);                                           // front matter first
            Assert.Contains("title:", text);
        }
        using (var zip = ZipFile.OpenRead(withoutTrash.Path))
            Assert.DoesNotContain(zip.Entries, e => e.FullName.StartsWith("_trash/", StringComparison.Ordinal));

        var before = _api.Search(word).Items.Count;
        var imported = NotesTransfer.Import(_api, withTrash.Path);
        Assert.Equal(withTrash.Count, imported.Imported);
        Assert.Empty(imported.Skipped);
        Assert.InRange(imported.NoteIds.Count, 1, 5);                                 // at most the first five are named
        // Import always makes new notes: the two live ones again; the one whose file sat in _trash/ goes back to the trash.
        Assert.Equal(before + 2, _api.Search(word).Items.Count);                         // search only finds live notes
        Assert.Equal(2, _api.List(deleted: true, limit: 500).Items.Count(i => i.Title == $"export trash {word}"));   // the original and the imported one
        Assert.Equal(NotesTransferText.ImportSummary(imported), Strings.NotesImportDone(withTrash.Count));
        Assert.Contains(live1, _api.Search(word).Items.Select(h => h.NoteId));          // the originals are untouched
        Assert.Contains(live2, _api.Search("导出 " + word).Items.Select(h => h.NoteId));
    }

    [Fact]
    public void Export_ToAnExistingFile_IsRefusedByTheCore_ButReplacedByTheShell()
    {
        var path = Path.Combine(_dirs.Root, "again.zip");
        _api.Create("for the export", "x");
        Assert.True(_api.Export(path, false).Count >= 1);
        Assert.Equal("INVALID", Code(() => _api.Export(path, false)));                  // the core never overwrites
        var before = new FileInfo(path).Length;

        var second = NotesTransfer.ExportTo(_api, path, false);                         // the shell's way: write beside it, then replace
        Assert.Equal(path, second.Path);
        Assert.True(File.Exists(path));
        Assert.Equal(second.Bytes, new FileInfo(path).Length);
        Assert.NotEqual(0, before);
        Assert.Equal(new[] { path }, Directory.GetFiles(_dirs.Root, "again*"));         // no .part left over
        Assert.Equal("INVALID", Code(() => _api.Export(Path.Combine(_dirs.Root, "missing-folder", "x.zip"), false)));
        Assert.Equal(Strings.ErrNotesTransferInvalid, ErrorMessages.ForNotesTransfer("INVALID"));
    }

    [Fact]
    public void Import_AFolder_SkipsWhatItCannotTake_AndSaysWhy()
    {
        var word = Unique();
        var folder = Path.Combine(_dirs.Root, "to-import");
        Directory.CreateDirectory(Path.Combine(folder, "sub"));
        File.WriteAllText(Path.Combine(folder, "plain.md"), $"# Plain {word}\n\nbody\n");
        File.WriteAllText(Path.Combine(folder, "sub", "notes.txt"), $"text note {word}");
        File.WriteAllText(Path.Combine(folder, "with-front-matter.md"), $"---\ntitle: Front {word}\n---\n\nthe body\n");
        File.WriteAllText(Path.Combine(folder, "big.md"), new string('x', NoteLimits.MaxBodyBytes + 100));
        File.WriteAllBytes(Path.Combine(folder, "binary.md"), new byte[] { 65, 0, 66, 0, 67 });
        Directory.CreateDirectory(Path.Combine(folder, ".hidden"));
        File.WriteAllText(Path.Combine(folder, ".hidden", "secret.md"), "never imported");

        var result = NotesTransfer.Import(_api, folder);

        Assert.Equal(3, result.Imported);
        Assert.Equal(2, result.Skipped.Count);
        Assert.Contains(result.Skipped, s => s.Name.EndsWith("big.md", StringComparison.Ordinal));
        Assert.Contains(result.Skipped, s => s.Name.EndsWith("binary.md", StringComparison.Ordinal));
        Assert.All(result.Skipped, s => Assert.NotEqual(s.Reason, NotesTransferText.SkipReason(s.Reason)));   // every reason has Chinese words
        Assert.Equal("导入了 3 篇，跳过 2 个", NotesTransferText.ImportSummary(result));
        Assert.Contains("big.md", NotesTransferText.ImportDetails(result));
        Assert.Equal(3, _api.Search(word).Items.Count);                                // the three notes that were taken, found by their word
        Assert.Contains(_api.Search(word).Items, h => h.Title == "Front " + word);      // the title comes from the front matter
    }

    [Fact]
    public void Import_AMarkdownFile_AndTheWrongPaths()
    {
        var word = Unique();
        var file = Path.Combine(_dirs.Root, $"{word}.md");
        File.WriteAllText(file, "# Heading\n\ncontent\n");
        var result = NotesTransfer.Import(_api, file);
        Assert.Equal(1, result.Imported);
        Assert.Contains(_api.Search(word).Items, h => h.Title.Contains(word));          // titled after the file name (no front matter)

        Assert.Equal("INVALID", Code(() => NotesTransfer.Import(_api, "relative.md")));   // refused before the core
        var gone = Assert.Throws<CoreException>(() => NotesTransfer.Import(_api, Path.Combine(_dirs.Root, "nothing-here.zip")));
        Assert.Contains(gone.Code, new[] { "INVALID", "NOT_FOUND" });
        Assert.NotEqual(Strings.ErrorGeneric, ErrorMessages.ForNotesTransfer(gone.Code));
    }

    // ---- attachments --------------------------------------------------------------------------------------

    private string WriteFile(string name, byte[] bytes)
    {
        var path = Path.Combine(_dirs.Root, name);
        File.WriteAllBytes(path, bytes);
        return path;
    }

    private static byte[] RandomBytes(int n)
    {
        var bytes = new byte[n];
        RandomNumberGenerator.Fill(bytes);
        return bytes;
    }

    [Fact]
    public void Attachment_AddGetExport_RoundTripsTheBytes()
    {
        var bytes = RandomBytes(5000);
        var path = WriteFile("pic [1].png", bytes);
        var added = _api.AttachmentAdd(path);

        Assert.Equal(Convert.ToHexString(SHA256.HashData(bytes)).ToLowerInvariant(), added.BlobId);   // the id is the SHA-256 of the file
        Assert.True(BlobRef.IsValidId(added.BlobId));
        Assert.Equal(("pic [1].png", "image/png", 5000L, true), (added.Name, added.Mime, added.Size, added.Present));
        Assert.StartsWith("![", added.Markdown);                                       // pictures are image syntax
        Assert.EndsWith($"](blob:{added.BlobId})", added.Markdown);
        Assert.DoesNotContain("[1]", added.Markdown);                                  // the brackets of the name are escaped

        var got = _api.AttachmentGet(added.BlobId);
        Assert.True(got.Present);
        Assert.Equal(("pic [1].png", "image/png", 5000L), (got.Name, got.Mime, got.Size));

        var target = Path.Combine(_dirs.Root, "exported.png");
        File.WriteAllText(target, "an older file");
        var exported = _api.AttachmentExport(added.BlobId, target);
        Assert.True(exported.Present);
        Assert.Equal(bytes, File.ReadAllBytes(target));                                // the core replaces a plain file

        Assert.Equal(added.BlobId, _api.AttachmentAdd(WriteFile("same content.png", bytes)).BlobId);   // the same bytes are stored once
    }

    [Fact]
    public void Attachment_OtherFilesAreLinks_AndSvgIsNotAPicture()
    {
        var pdf = _api.AttachmentAdd(WriteFile("report.pdf", RandomBytes(300)));
        Assert.StartsWith("[", pdf.Markdown);
        Assert.DoesNotContain("![", pdf.Markdown);
        Assert.False(AttachmentTypes.IsRasterImage(pdf.Mime));

        var svg = _api.AttachmentAdd(WriteFile("logo.svg", System.Text.Encoding.UTF8.GetBytes("<svg xmlns='http://www.w3.org/2000/svg'/>")));
        Assert.StartsWith("[", svg.Markdown);
        Assert.False(AttachmentTypes.IsRasterImage(svg.Mime));
    }

    [Fact]
    public void Attachment_Errors_HaveTheirCodesAndWords()
    {
        var big = Path.Combine(_dirs.Root, "big.bin");
        using (var stream = File.Create(big)) stream.SetLength(AttachmentPolicy.MaxBytes + 1);
        var tooLarge = Code(() => _api.AttachmentAdd(big));
        Assert.Equal("TOO_LARGE", tooLarge);
        Assert.Contains("32 MiB", ErrorMessages.ForNotesAttachment(tooLarge));

        Assert.Equal("INVALID", Code(() => _api.AttachmentAdd(_dirs.Root)));                         // a folder
        Assert.Equal("INVALID", Code(() => _api.AttachmentAdd(Path.Combine(_dirs.Root, "nothing.png"))));
        var unknown = new string('f', 64);
        Assert.Equal("NOT_FOUND", Code(() => _api.AttachmentExport(unknown, Path.Combine(_dirs.Root, "x.bin"))));
        Assert.False(File.Exists(Path.Combine(_dirs.Root, "x.bin")));

        var missing = Record.Exception(() => _api.AttachmentGet(unknown));
        if (missing is CoreException c) Assert.Equal("NOT_FOUND", c.Code);
        else Assert.False(_api.AttachmentGet(unknown).Present);
    }

    [Fact]
    public void Attachment_TheResolverDrawsWhatTheNoteRefersTo()
    {
        var bytes = RandomBytes(2000);
        var picture = _api.AttachmentAdd(WriteFile("shot.png", bytes));
        var doc = _api.AttachmentAdd(WriteFile("Q3 report.pdf", RandomBytes(900)));
        var elsewhere = new string('e', 64);
        var body = $"{picture.Markdown}\n\n{doc.Markdown}\n\n![from the other device](blob:{elsewhere})\n";

        var blobs = _api.RenderBody(body).Blocks.SelectMany(b => b.Inline).Where(r => BlobRef.IsBlob(r.Href)).ToList();
        Assert.Equal(3, blobs.Count);

        var resolver = new AttachmentResolver(_api, _dirs.Cache, _dirs.Open);
        var shown = resolver.Resolve(picture.BlobId, wantImageFile: true);
        Assert.True(shown.IsImage);
        Assert.Equal(bytes, File.ReadAllBytes(shown.CachePath));
        Assert.Equal(Path.Combine(_dirs.Cache, picture.BlobId + ".png"), shown.CachePath);

        var chip = resolver.Resolve(doc.BlobId, wantImageFile: false);
        Assert.Equal(("Q3 report.pdf", 900L), (chip.Name, chip.Size));
        Assert.Contains("Q3 report.pdf", chip.Label);

        var pending = resolver.Resolve(elsewhere, wantImageFile: true);                         // referenced, not synced yet
        Assert.Equal(AttachmentState.Missing, pending.State);

        var (opened, name) = resolver.ExportForOpening(doc.BlobId);
        Assert.Equal("Q3 report.pdf", name);
        Assert.Equal(900, new FileInfo(opened).Length);
        Assert.False(AttachmentPolicy.IsRisky(name));
    }

    [Fact]
    public void Attachment_ARiskyFileKeepsItsNameForTheQuestion()
    {
        var exe = _api.AttachmentAdd(WriteFile("setup.exe", RandomBytes(64)));
        var view = new AttachmentResolver(_api, _dirs.Cache, _dirs.Open).Resolve(exe.BlobId, wantImageFile: false);
        Assert.Equal("setup.exe", view.Name);
        Assert.True(AttachmentPolicy.IsRisky(view.Name));              // the page asks before opening it
    }

    [Fact]
    public void Attachment_CleanupKeepsFreshAttachments()
    {
        var added = _api.AttachmentAdd(WriteFile("fresh.png", RandomBytes(100)));
        var result = _api.AttachmentCleanup();                          // only attachments older than a week are ever removed
        Assert.True(result.Removed >= 0 && result.FreedBytes >= 0 && result.Kept >= 1);
        Assert.True(_api.AttachmentGet(added.BlobId).Present);
        Assert.Equal(Strings.AttachmentCleanupNothing, NotesTransferText.CleanupDone(new AttachmentCleanupResult(0, 0, result.Kept)));
    }

    [Fact]
    public async Task Attachment_InsertedMarkdownIsSavedAndRendersAsABlob()
    {
        var added = _api.AttachmentAdd(WriteFile("inline.png", RandomBytes(400)));
        var created = _api.Create("with attachment", "some text");
        var now = DateTimeOffset.UtcNow;
        var session = new NoteEditorSession(new ThreadPoolNotesGateway(_api), () => now);
        session.Open(_api.Get(created.NoteId));

        var edit = MarkdownEdit.InsertBlock(session.Model.Body, session.Model.Body.Length, 0, added.Markdown);
        session.OnEdited("with attachment", edit.ApplyTo(session.Model.Body));
        now += TimeSpan.FromMilliseconds(600);
        await session.TickAsync();

        var saved = _api.Get(created.NoteId).Body;
        Assert.Contains($"(blob:{added.BlobId})", saved);
        Assert.Equal("some text\n" + added.Markdown + "\n", saved);                     // on a line of its own
        var image = _api.Render(created.NoteId).Blocks.SelectMany(b => b.Inline).Single(r => r.Kind == InlineKind.Image);
        Assert.True(BlobRef.TryParse(image.Href, out var id));
        Assert.Equal(added.BlobId, id);
    }
}
