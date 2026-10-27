using System.Text.Json;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class BlobRefTests
{
    private static readonly string Id = new('a', 64);
    private const string Mixed = "0123456789abcdefABCDEF0123456789abcdefABCDEF0123456789abcdefABCD";

    [Fact]
    public void AWellFormedAddress_GivesTheBlobId_InLowerCase()
    {
        Assert.True(BlobRef.TryParse("blob:" + Id, out var id));
        Assert.Equal(Id, id);
        Assert.True(BlobRef.TryParse("  BLOB:" + Mixed + "  ", out var mixed));
        Assert.Equal(Mixed.ToLowerInvariant(), mixed);
        Assert.Equal(64, mixed.Length);
    }

    [Theory]
    [InlineData("blob:")]
    [InlineData("blob:abc")]
    [InlineData("blob:../../etc/passwd")]
    [InlineData("blob:zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")]
    [InlineData("https://example.com")]
    [InlineData("mailto:a@b.cn")]
    [InlineData("relative.png")]
    [InlineData("")]
    [InlineData(null)]
    public void Anything_Else_IsNotAnAttachment(string? href)
    {
        Assert.False(BlobRef.TryParse(href, out var id));
        Assert.Equal("", id);
    }

    [Fact]
    public void TheIdHasToBeExactly64HexDigits()
    {
        Assert.False(BlobRef.TryParse("blob:" + new string('a', 63), out _));
        Assert.False(BlobRef.TryParse("blob:" + new string('a', 65), out _));
        Assert.False(BlobRef.TryParse("blob:" + new string('a', 63) + "g", out _));
        Assert.True(BlobRef.IsValidId(Id));
        Assert.False(BlobRef.IsValidId(null));
    }

    [Fact]
    public void IsBlob_RecognisesThePrefix_EvenOfAMalformedAddress()
    {
        Assert.True(BlobRef.IsBlob("blob:short"));
        Assert.True(BlobRef.IsBlob("Blob:" + Id));
        Assert.False(BlobRef.IsBlob("https://blob:"));
        Assert.False(BlobRef.IsBlob(null));
    }

    [Fact]
    public void ABlobAddress_IsNeverTreatedAsAWebAddress()
    {
        Assert.False(NoteLinks.IsOpenable("blob:" + Id));
        Assert.False(NoteLinks.IsOpenable("blob:https://example.com/x"));
        Assert.Null(NoteLinks.TryGetOpenableUri("blob:" + Id));
    }
}

public class AttachmentPolicyTests
{
    [Theory]
    [InlineData("setup.exe")]
    [InlineData("SETUP.EXE")]
    [InlineData("run.bat")]
    [InlineData("run.cmd")]
    [InlineData("script.ps1")]
    [InlineData("installer.msi")]
    [InlineData("macro.js")]
    [InlineData("macro.vbs")]
    [InlineData("shortcut.lnk")]
    [InlineData("screensaver.scr")]
    [InlineData("tool.jar")]
    [InlineData("x.hta")]
    [InlineData("link.url")]
    [InlineData("help.chm")]
    [InlineData("script.py")]
    [InlineData("invoice.pdf.exe")]            // the last extension counts
    [InlineData("readme.txt.lnk")]
    [InlineData("update.exe.")]                // Windows drops the trailing dot
    [InlineData("update.exe   ")]              // and the trailing blanks
    [InlineData(".exe")]                       // a file that is nothing but an extension
    [InlineData("evil\u202Efdp.exe")]          // a right-to-left override must not hide it
    public void ProgramsAndScripts_AreRisky(string name) => Assert.True(AttachmentPolicy.IsRisky(name), name);

    [Theory]
    [InlineData("report.pdf")]
    [InlineData("photo.png")]
    [InlineData("notes.txt")]
    [InlineData("archive.zip")]
    [InlineData("sheet.xlsx")]
    [InlineData("exe")]                        // no extension
    [InlineData("setup.exe.txt")]              // opens in a text editor
    [InlineData("")]
    [InlineData(null)]
    public void OrdinaryFiles_AreNot(string? name) => Assert.False(AttachmentPolicy.IsRisky(name), name);

    [Theory]
    [InlineData("a/b\\c.txt", "a_b_c.txt")]
    [InlineData("..\\..\\evil.txt", ".._.._evil.txt")]
    [InlineData("what?*.txt", "what__.txt")]
    [InlineData("a:b.txt", "a_b.txt")]
    [InlineData("name with spaces.pdf", "name with spaces.pdf")]
    [InlineData("文件 😀.docx", "文件 😀.docx")]
    [InlineData("tab\there.txt", "tabhere.txt")]
    [InlineData("dots...", "dots")]
    [InlineData("trailing .", "trailing")]
    [InlineData("evil\u202Efdp.exe", "evilfdp.exe")]
    public void FileNames_AreMadeSafeToCreate(string name, string safe) => Assert.Equal(safe, AttachmentPolicy.SafeFileName(name, "fallback"));

    [Theory]
    [InlineData("CON")]
    [InlineData("con.txt")]
    [InlineData("NUL.png")]
    [InlineData("com1")]
    [InlineData("LPT9.md")]
    public void DeviceNames_AreNotUsedAsTheyAre(string name)
    {
        var safe = AttachmentPolicy.SafeFileName(name, "fallback");
        Assert.StartsWith("_", safe);
        Assert.EndsWith(name, safe);
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("...")]
    [InlineData(null)]
    public void NothingLeft_GivesTheFallback(string? name) => Assert.Equal("fallback", AttachmentPolicy.SafeFileName(name, "fallback"));

    [Fact]
    public void ALongName_IsCut_ButKeepsItsExtension()
    {
        var safe = AttachmentPolicy.SafeFileName(new string('x', 400) + ".pdf", "fallback");
        Assert.True(safe.Length <= 120);
        Assert.EndsWith(".pdf", safe);
        var cjk = AttachmentPolicy.SafeFileName(new string('字', 300) + ".docx", "fallback");
        Assert.True(cjk.Length <= 120);
        Assert.EndsWith(".docx", cjk);
        var astral = AttachmentPolicy.SafeFileName(string.Concat(Enumerable.Repeat("😀", 200)) + ".png", "fallback");
        Assert.True(astral.Length <= 120);
        Assert.EndsWith(".png", astral);
        Assert.False(char.IsHighSurrogate(astral[^5]), "cut inside a surrogate pair");     // the character before ".png" is whole
    }

    [Fact]
    public void ThePastedPictureName_IsTheTimeInTheName() =>
        Assert.Equal("image-20261027-153009.png", AttachmentPolicy.PastedImageName(new DateTimeOffset(2026, 10, 27, 15, 30, 9, TimeSpan.Zero)));

    [Fact]
    public void RasterImagesAreDrawn_ButSvgIsNot()
    {
        foreach (var mime in new[] { "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "IMAGE/PNG", " image/png " })
            Assert.True(AttachmentTypes.IsRasterImage(mime), mime);
        foreach (var mime in new[] { "image/svg+xml", "application/pdf", "text/html", "", null })
            Assert.False(AttachmentTypes.IsRasterImage(mime), mime);
        Assert.Equal(".png", AttachmentTypes.CacheExtension("image/png"));
        Assert.Equal(".jpg", AttachmentTypes.CacheExtension("image/jpeg"));
        Assert.Equal(".bin", AttachmentTypes.CacheExtension("application/pdf"));
    }

    [Fact]
    public void TheLimitIs32MiB() => Assert.Equal(32L * 1024 * 1024, AttachmentPolicy.MaxBytes);
}

public sealed class TempDirs : IDisposable
{
    public string Root { get; } = Path.Combine(Path.GetTempPath(), "cb-attach-" + Guid.NewGuid().ToString("N"));
    public string Cache => Path.Combine(Root, "cache");
    public string Open => Path.Combine(Root, "open");

    public TempDirs() => Directory.CreateDirectory(Root);

    public void Dispose()
    {
        try { Directory.Delete(Root, recursive: true); } catch (IOException) { }
    }
}

public class AttachmentResolverTests : IDisposable
{
    private static readonly string Blob = new('c', 64);
    private static readonly byte[] Png = { 0x89, 0x50, 0x4E, 0x47, 1, 2, 3 };

    private readonly TempDirs _dirs = new();
    private readonly ScriptedCore _core = new();

    public void Dispose() => _dirs.Dispose();

    private AttachmentResolver Resolver() => new(new NotesApi(_core), _dirs.Cache, _dirs.Open);

    private ScriptedCore Present(string name = "photo.png", string mime = "image/png", long size = 7)
    {
        var answer = $$"""{"blob_id":"{{Blob}}","name":{{JsonSerializer.Serialize(name)}},"mime":"{{mime}}","size":{{size}},"present":true}""";
        _core.On("notes.attachment_get", answer);
        _core.On("notes.attachment_export", p =>
        {
            File.WriteAllBytes(p.GetProperty("path").GetString()!, Png);
            return answer;
        });
        return _core;
    }

    [Fact]
    public void ABlobTheNoteRefersToButTheCoreDoesNotHaveYet_IsMissing()
    {
        _core.On("notes.attachment_get", $$"""{"blob_id":"{{Blob}}","present":false}""");
        var view = Resolver().Resolve(Blob, wantImageFile: true);

        Assert.Equal(AttachmentState.Missing, view.State);
        Assert.False(view.IsPresent);
        Assert.Equal(0, _core.Count("notes.attachment_export"));
    }

    [Fact]
    public void APicture_IsWrittenToTheCacheOnce_UnderItsBlobId()
    {
        Present();
        var resolver = Resolver();
        var view = resolver.Resolve(Blob, wantImageFile: true);

        Assert.Equal(AttachmentState.Present, view.State);
        Assert.True(view.IsImage);
        Assert.Equal(Path.Combine(_dirs.Cache, Blob + ".png"), view.CachePath);
        Assert.Equal(Png, File.ReadAllBytes(view.CachePath));
        Assert.Equal(("photo.png", "image/png", 7L), (view.Name, view.Mime, view.Size));
        Assert.Empty(Directory.GetFiles(_dirs.Cache, "*.part"));           // written through a temporary name, which is gone

        var again = resolver.Resolve(Blob, wantImageFile: true);            // the next look needs neither the core nor the disk
        Assert.Equal(view.CachePath, again.CachePath);
        Assert.Equal(1, _core.Count("notes.attachment_get"));
        Assert.Equal(1, _core.Count("notes.attachment_export"));
    }

    [Fact]
    public void ACachedPictureFromAnEarlierRun_IsNotWrittenAgain()
    {
        Present();
        Directory.CreateDirectory(_dirs.Cache);
        File.WriteAllBytes(Path.Combine(_dirs.Cache, Blob + ".png"), Png);
        var view = Resolver().Resolve(Blob, wantImageFile: true);
        Assert.Equal(Path.Combine(_dirs.Cache, Blob + ".png"), view.CachePath);
        Assert.Equal(0, _core.Count("notes.attachment_export"));
    }

    [Fact]
    public void AnEmptyFileInTheCache_IsReplaced()
    {
        Present();
        Directory.CreateDirectory(_dirs.Cache);
        var path = Path.Combine(_dirs.Cache, Blob + ".png");
        File.WriteAllBytes(path, Array.Empty<byte>());
        // the half-written leftover is not taken for the picture: the file is exported again (Move fails over a file, so it stays)
        Resolver().Resolve(Blob, wantImageFile: true);
        Assert.Equal(1, _core.Count("notes.attachment_export"));
    }

    [Fact]
    public void AFileThatIsNotAPicture_IsNotWrittenToTheCache()
    {
        Present("report.pdf", "application/pdf", 900_000);
        var view = Resolver().Resolve(Blob, wantImageFile: true);

        Assert.True(view.IsPresent);
        Assert.False(view.IsImage);
        Assert.Equal("", view.CachePath);
        Assert.Equal(0, _core.Count("notes.attachment_export"));
        Assert.False(Directory.Exists(_dirs.Cache));
        Assert.Equal(Strings.AttachmentLabel("report.pdf", Formatting.Bytes(900_000)), view.Label);
    }

    [Fact]
    public void ASvg_IsTreatedAsAFile_NotAsAPicture()
    {
        Present("logo.svg", "image/svg+xml");
        var view = Resolver().Resolve(Blob, wantImageFile: true);
        Assert.False(view.IsImage);
        Assert.Equal("", view.CachePath);
    }

    [Fact]
    public void WithoutAskingForAFile_NothingIsWritten()
    {
        Present();
        var view = Resolver().Resolve(Blob, wantImageFile: false);
        Assert.True(view.IsImage);
        Assert.Equal("", view.CachePath);
        Assert.Equal(0, _core.Count("notes.attachment_export"));
    }

    [Fact]
    public void TheNameOfTheFile_IsMadeSafe()
    {
        Present("..\\..\\evil.exe");
        var view = Resolver().Resolve(Blob, wantImageFile: false);
        Assert.DoesNotContain("\\", view.Name);
        Assert.DoesNotContain("/", view.Name);
        Assert.True(AttachmentPolicy.IsRisky(view.Name));
    }

    [Fact]
    public void ACoreThatSaysNotFound_MeansMissing()
    {
        Present();
        _core.On("notes.attachment_export", _ => throw new CoreException("NOT_FOUND", "gone"));
        var view = Resolver().Resolve(Blob, wantImageFile: true);
        Assert.Equal(AttachmentState.Missing, view.State);
        Assert.False(Directory.Exists(_dirs.Cache) && Directory.GetFiles(_dirs.Cache).Length > 0);   // no stray file
    }

    [Fact]
    public void OtherFailures_AreReported_NotThrown()
    {
        _core.On("notes.attachment_get", _ => throw new CoreException("INTERNAL", "boom"));
        var view = Resolver().Resolve(Blob, wantImageFile: true);
        Assert.Equal((AttachmentState.Failed, "INTERNAL"), (view.State, view.ErrorCode));

        Present();
        var blocked = Path.Combine(_dirs.Root, "blocked");
        File.WriteAllText(blocked, "a file where the cache folder should be");
        var failing = new AttachmentResolver(new NotesApi(_core), blocked, _dirs.Open).Resolve(Blob, wantImageFile: true);
        Assert.Equal(AttachmentState.Failed, failing.State);
    }

    [Fact]
    public void ABadBlobId_IsRefusedWithoutAskingTheCore()
    {
        var view = Resolver().Resolve("../../x", wantImageFile: true);
        Assert.Equal((AttachmentState.Failed, "INVALID"), (view.State, view.ErrorCode));
        Assert.Empty(_core.Calls);
    }

    [Fact]
    public void Forget_MakesTheNextLookAskTheCoreAgain()
    {
        Present();
        var resolver = Resolver();
        resolver.Resolve(Blob, wantImageFile: false);
        resolver.Resolve(Blob, wantImageFile: false);
        Assert.Equal(1, _core.Count("notes.attachment_get"));
        resolver.Forget();
        resolver.Resolve(Blob, wantImageFile: false);
        Assert.Equal(2, _core.Count("notes.attachment_get"));
    }

    [Fact]
    public void AMissingAttachment_IsAskedAgainOnTheNextLook()
    {
        _core.On("notes.attachment_get", $$"""{"blob_id":"{{Blob}}","present":false}""");
        var resolver = Resolver();
        resolver.Resolve(Blob, false);
        resolver.Resolve(Blob, false);
        Assert.Equal(2, _core.Count("notes.attachment_get"));     // it may have arrived by now
    }

    [Fact]
    public void ExportForOpening_WritesTheRealNameIntoAFolderOfItsOwn()
    {
        Present("Quarterly report 2026.pdf", "application/pdf", 100);
        var (path, name) = Resolver().ExportForOpening(Blob);

        Assert.Equal("Quarterly report 2026.pdf", name);
        Assert.Equal(Path.Combine(_dirs.Open, Blob[..16], "Quarterly report 2026.pdf"), path);
        Assert.True(File.Exists(path));
        Assert.Equal(path, _core.ParamsOf("notes.attachment_export").Single().GetProperty("path").GetString());
    }

    [Fact]
    public void ExportForOpening_OfAMissingAttachment_SaysNotFound()
    {
        _core.On("notes.attachment_get", $$"""{"blob_id":"{{Blob}}","present":false}""");
        Assert.Equal("NOT_FOUND", Assert.Throws<CoreException>(() => Resolver().ExportForOpening(Blob)).Code);
        Assert.Equal("INVALID", Assert.Throws<CoreException>(() => Resolver().ExportForOpening("nope")).Code);
        Assert.Equal(0, _core.Count("notes.attachment_export"));
    }
}

public class AttachmentApiTests
{
    private static readonly string Blob = new('d', 64);

    [Fact]
    public void Add_SendsThePath_AndReadsTheAnswer()
    {
        var core = new ScriptedCore().On("notes.attachment_add", $$"""
            {"blob_id":"{{Blob}}","name":"a [b].png","mime":"image/png","size":1234,"present":true,"markdown":"![a \\[b\\]](blob:{{Blob}})"}
            """);
        var a = new NotesApi(core).AttachmentAdd(@"C:\Users\me\a [b].png");

        Assert.Equal(@"C:\Users\me\a [b].png", core.Calls.Single().Params.GetProperty("path").GetString());
        Assert.Equal((Blob, "a [b].png", "image/png", 1234L, true), (a.BlobId, a.Name, a.Mime, a.Size, a.Present));
        Assert.Equal($"![a \\[b\\]](blob:{Blob})", a.Markdown);
    }

    [Fact]
    public void Get_Export_AndTheirParameters()
    {
        var core = new ScriptedCore()
            .On("notes.attachment_get", $$"""{"blob_id":"{{Blob}}","present":false}""")
            .On("notes.attachment_export", $$"""{"blob_id":"{{Blob}}","name":"x.pdf","mime":"application/pdf","size":5,"present":true}""");
        var api = new NotesApi(core);

        var missing = api.AttachmentGet(Blob);
        Assert.False(missing.Present);
        Assert.Equal("", missing.Name);
        Assert.Equal(Blob, core.Calls[0].Params.GetProperty("blob_id").GetString());

        var exported = api.AttachmentExport(Blob, @"C:\cache\x.pdf");
        Assert.True(exported.Present);
        Assert.Equal((Blob, @"C:\cache\x.pdf"), (core.Calls[1].Params.GetProperty("blob_id").GetString(), core.Calls[1].Params.GetProperty("path").GetString()));
    }

    [Fact]
    public void Cleanup_ReadsTheNumbers()
    {
        var core = new ScriptedCore().On("notes.attachment_cleanup", """{"removed":3,"freed_bytes":3355443,"kept":9}""");
        var r = new NotesApi(core).AttachmentCleanup();
        Assert.Equal((3, 3355443L, 9), (r.Removed, r.FreedBytes, r.Kept));
        Assert.Equal(AttachmentCleanupResult.FromJson(VJ.Parse("{}")), new AttachmentCleanupResult(0, 0, 0));
    }

    [Fact]
    public void TheAttachmentEvent_CarriesTheBlob()
    {
        var e = Assert.IsType<NotesAttachmentEvent>(CoreEventParser.Parse($$"""{"type":"notes.attachment","blob_id":"{{Blob}}"}"""));
        Assert.Equal(Blob, e.BlobId);
        Assert.Equal("notes.attachment " + Blob, e.LogLine);
        Assert.Equal("", Assert.IsType<NotesAttachmentEvent>(CoreEventParser.Parse("""{"type":"notes.attachment"}""")).BlobId);
    }

    [Fact]
    public void ABlobImageAndLink_ComeThroughTheRenderTree()
    {
        var tree = RenderTree.FromJson(VJ.Parse($$"""
            {"blocks":[{"kind":"paragraph","inline":[
                {"t":"image","href":"blob:{{Blob}}","alt":"shot"},
                {"t":"link","href":"blob:{{Blob}}","children":[{"t":"text","text":"report.pdf"}]}]}]}
            """));
        var inline = tree.Blocks.Single().Inline;
        Assert.Equal(InlineKind.Image, inline[0].Kind);
        Assert.True(BlobRef.TryParse(inline[0].Href, out var imageBlob));
        Assert.Equal(Blob, imageBlob);
        Assert.Equal("shot", inline[0].Text);
        Assert.Equal(InlineKind.Link, inline[1].Kind);
        Assert.True(BlobRef.TryParse(inline[1].Href, out _));
        Assert.Equal("report.pdf", inline[1].PlainText());
    }

    [Fact]
    public void RenderBody_SendsOnlyTheText_AndTheAnswerHasNoIds()
    {
        var core = new ScriptedCore().On("notes.render", """{"blocks":[{"kind":"paragraph","inline":[{"t":"text","text":"x"},{"t":"softbr"},{"t":"text","text":"y"}]}],"toc":[]}""");
        var tree = new NotesApi(core).RenderBody("x\ny\n");

        var p = core.Calls.Single().Params;
        Assert.Equal("x\ny\n", p.GetProperty("body").GetString());
        Assert.False(p.TryGetProperty("note_id", out _));
        Assert.False(p.TryGetProperty("rev_id", out _));
        Assert.Equal(("", ""), (tree.NoteId, tree.RevId));
        Assert.Equal(InlineKind.SoftBreak, tree.Blocks.Single().Inline[1].Kind);
    }
}

public class MarkdownInsertBlockTests
{
    private const string Md = "![shot](blob:abc)";

    private static string After(TextEdit e, string text) => e.ApplyTo(text);

    [Fact]
    public void AtTheStartOfALine_TheMarkdownGetsTheLineAndTheCaretMovesBelow()
    {
        var edit = MarkdownEdit.InsertBlock("first\nsecond", 6, 0, Md);
        Assert.Equal("first\n" + Md + "\nsecond", After(edit, "first\nsecond"));
        Assert.Equal((After(edit, "first\nsecond").IndexOf("second", StringComparison.Ordinal), 0), (edit.SelectionStart, edit.SelectionLength));
    }

    [Fact]
    public void InTheMiddleOfALine_ALineBreakComesFirst()
    {
        var text = "before after";
        var edit = MarkdownEdit.InsertBlock(text, 7, 0, Md);
        Assert.Equal("before " + "\n" + Md + "\n" + "after", After(edit, text));
    }

    [Fact]
    public void AtTheEndOfTheText_AndInAnEmptyNote()
    {
        Assert.Equal("text\n" + Md + "\n", After(MarkdownEdit.InsertBlock("text", 4, 0, Md), "text"));
        Assert.Equal(Md + "\n", After(MarkdownEdit.InsertBlock("", 0, 0, Md), ""));
    }

    [Fact]
    public void ASelection_IsNotReplaced_TheMarkdownGoesAfterIt()
    {
        var text = "keep this";
        var edit = MarkdownEdit.InsertBlock(text, 0, 4, Md);
        Assert.Equal("keep" + "\n" + Md + "\n" + " this", After(edit, text));
    }

    [Fact]
    public void CrLfText_GetsCrLf_AndTheGivenLineBreakWins()
    {
        Assert.Equal("a\r\n" + Md + "\r\nb", After(MarkdownEdit.InsertBlock("a\r\nb", 3, 0, Md), "a\r\nb"));
        Assert.Equal("a\r\n" + Md + "\r\n", After(MarkdownEdit.InsertBlock("a", 1, 0, Md, "\r\n"), "a"));
    }

    [Fact]
    public void SeveralAttachments_FollowOneAnotherEachOnItsOwnLine()
    {
        var text = "intro";
        var first = MarkdownEdit.InsertBlock(text, 5, 0, "![a](blob:1)");
        text = After(first, text);
        var second = MarkdownEdit.InsertBlock(text, first.SelectionStart, first.SelectionLength, "[b](blob:2)");
        Assert.Equal("intro\n![a](blob:1)\n[b](blob:2)\n", After(second, text));
    }

    [Fact]
    public void OutOfRangePositions_AreClamped()
    {
        Assert.Equal("ab\n" + Md + "\n", After(MarkdownEdit.InsertBlock("ab", 50, 9, Md), "ab"));
        Assert.Equal(Md + "\nab", After(MarkdownEdit.InsertBlock("ab", -3, 0, Md), "ab"));
    }
}

public class NotesTransferTests : IDisposable
{
    private readonly TempDirs _dirs = new();

    public void Dispose() => _dirs.Dispose();

    private string Zip(string name = "notes.zip") => Path.Combine(_dirs.Root, name);

    [Fact]
    public void Export_WritesNextToTheTarget_ThenReplacesIt()
    {
        var target = Zip();
        File.WriteAllText(target, "the older export");
        string? written = null;
        var core = new ScriptedCore().On("notes.export", p =>
        {
            written = p.GetProperty("path").GetString();
            File.WriteAllText(written!, "new export");
            return $$"""{"path":"{{written!.Replace("\\", "\\\\")}}","count":4,"bytes":10}""";
        });

        var result = NotesTransfer.ExportTo(new NotesApi(core), target, includeTrash: true);

        Assert.NotEqual(target, written);                                   // the core never sees the final name
        Assert.StartsWith(target, written);
        Assert.True(core.Calls.Single().Params.GetProperty("include_trash").GetBoolean());
        Assert.Equal("new export", File.ReadAllText(target));
        Assert.False(File.Exists(written));
        Assert.Equal((target, 4, 10L), (result.Path, result.Count, result.Bytes));
    }

    [Fact]
    public void AFailedExport_LeavesTheOlderFileAlone_AndNoLeftovers()
    {
        var target = Zip();
        File.WriteAllText(target, "the older export");
        var core = new ScriptedCore().On("notes.export", p =>
        {
            File.WriteAllText(p.GetProperty("path").GetString()!, "half");
            throw new CoreException("INTERNAL", "disk full");
        });

        Assert.Equal("INTERNAL", Assert.Throws<CoreException>(() => NotesTransfer.ExportTo(new NotesApi(core), target, false)).Code);
        Assert.Equal("the older export", File.ReadAllText(target));
        Assert.Equal(new[] { target }, Directory.GetFiles(_dirs.Root));
        Assert.False(core.Calls.Single().Params.TryGetProperty("include_trash", out _));
    }

    [Fact]
    public void ARelativePath_IsRefusedBeforeTheCoreSeesIt()
    {
        var core = new ScriptedCore();
        Assert.Equal("INVALID", Assert.Throws<CoreException>(() => NotesTransfer.ExportTo(new NotesApi(core), "notes.zip", false)).Code);
        Assert.Equal("INVALID", Assert.Throws<CoreException>(() => NotesTransfer.Import(new NotesApi(core), "folder/notes.zip")).Code);
        Assert.Equal("INVALID", Assert.Throws<CoreException>(() => NotesTransfer.Import(new NotesApi(core), "")).Code);
        Assert.Empty(core.Calls);
    }

    [Fact]
    public void Import_PassesTheAbsolutePath_AndReadsTheResult()
    {
        var core = new ScriptedCore().On("notes.import", """
            {"imported":3,"skipped":[{"name":"big.md","reason":"larger than the 1 MiB limit of a note"},{"name":"x.bin","reason":"not a text file"}],"note_ids":["nt_a","nt_b"]}
            """);
        var result = NotesTransfer.Import(new NotesApi(core), _dirs.Root);

        Assert.Equal(_dirs.Root, core.Calls.Single().Params.GetProperty("path").GetString());
        Assert.Equal(3, result.Imported);
        Assert.Equal(new[] { "big.md", "x.bin" }, result.Skipped.Select(s => s.Name).ToArray());
        Assert.Equal(new[] { "nt_a", "nt_b" }, result.NoteIds.ToArray());
    }

    [Fact]
    public void Import_ToleratesAnEmptyAnswer()
    {
        var result = NoteImportResult.FromJson(VJ.Parse("{}"));
        Assert.Equal(0, result.Imported);
        Assert.Empty(result.Skipped);
        Assert.Empty(result.NoteIds);
        Assert.Equal(0, NoteExportResult.FromJson(VJ.Parse("{}")).Count);
    }

    [Fact]
    public void TheDefaultExportName_HasTheDate() =>
        Assert.Equal("ClipBridge 笔记 2026-10-27.zip", NotesTransfer.DefaultExportName(new DateTimeOffset(2026, 10, 27, 23, 0, 0, TimeSpan.FromHours(9))));
}

public class NotesTransferTextTests
{
    [Fact]
    public void ExportDone_NamesCountAndSize() =>
        Assert.Equal("已导出 12 篇笔记（1.5 MB）", NotesTransferText.ExportDone(new NoteExportResult(@"C:\x.zip", 12, 1_572_864)));

    [Fact]
    public void ImportSummary_SaysImportedAndSkipped()
    {
        Assert.Equal("导入了 5 篇", NotesTransferText.ImportSummary(new NoteImportResult(5, Array.Empty<NoteSkip>(), Array.Empty<string>())));
        Assert.Equal("导入了 3 篇，跳过 2 个",
            NotesTransferText.ImportSummary(new NoteImportResult(3, new[] { new NoteSkip("a", "x"), new NoteSkip("b", "y") }, Array.Empty<string>())));
        Assert.Equal("导入了 0 篇，跳过 1 个", NotesTransferText.ImportSummary(new NoteImportResult(0, new[] { new NoteSkip("a", "x") }, Array.Empty<string>())));
    }

    [Theory]
    [InlineData("not a regular file", "不是普通文件")]
    [InlineData("larger than the 1 MiB limit of a note", "超过 1 MiB")]
    [InlineData("cannot be read", "读不出来")]
    [InlineData("not a text file", "不是文本文件")]
    public void KnownReasons_AreInChinese(string reason, string fragment) => Assert.Contains(fragment, NotesTransferText.SkipReason(reason));

    [Fact]
    public void AnUnknownReason_IsShownAsItIs_AndAMissingOneAsUnknown()
    {
        Assert.Equal("some new reason", NotesTransferText.SkipReason("some new reason"));
        Assert.Equal(Strings.Unknown, NotesTransferText.SkipReason(""));
    }

    [Fact]
    public void TheDetails_ListEachSkippedFile_WithAHarmlessName()
    {
        var result = new NoteImportResult(1, new[] { new NoteSkip("dir/big.md", "larger than the 1 MiB limit of a note"), new NoteSkip("evil\u202Efdp.exe", "not a text file") }, Array.Empty<string>());
        var lines = NotesTransferText.ImportDetails(result).Split('\n');
        Assert.Equal(2, lines.Length);
        Assert.StartsWith("dir/big.md", lines[0]);
        Assert.Contains(Strings.NotesSkipTooLarge, lines[0]);
        Assert.DoesNotContain("\u202E", lines[1], StringComparison.Ordinal);    // (a culture-aware search treats that character as empty)
        Assert.Equal("", NotesTransferText.ImportDetails(new NoteImportResult(0, Array.Empty<NoteSkip>(), Array.Empty<string>())));
    }

    [Fact]
    public void Cleanup_ReportsTheFreedSpace_OrThatThereWasNothing()
    {
        Assert.Equal("清理了 3 个未使用的附件，释放了 3.2 MB", NotesTransferText.CleanupDone(new AttachmentCleanupResult(3, 3_355_443, 9)));
        Assert.Contains("释放了", NotesTransferText.CleanupDone(new AttachmentCleanupResult(1, 100, 0)));
        Assert.Equal(Strings.AttachmentCleanupNothing, NotesTransferText.CleanupDone(new AttachmentCleanupResult(0, 0, 4)));
    }

    [Fact]
    public void TheNewErrorCodes_HaveTheirOwnWording()
    {
        Assert.Equal(Strings.ErrAttachmentTooLarge, ErrorMessages.ForNotesAttachment("TOO_LARGE"));
        Assert.Contains("32 MiB", ErrorMessages.ForNotesAttachment("TOO_LARGE"));
        Assert.NotEqual(ErrorMessages.ForNotes("TOO_LARGE"), ErrorMessages.ForNotesAttachment("TOO_LARGE"));    // not the size of a note
        Assert.Equal(Strings.ErrAttachmentInvalid, ErrorMessages.ForNotesAttachment("INVALID"));
        Assert.Equal(Strings.ErrAttachmentNotFound, ErrorMessages.ForNotesAttachment("NOT_FOUND"));
        Assert.Equal(Strings.ErrNotesTransferInvalid, ErrorMessages.ForNotesTransfer("INVALID"));
        Assert.Equal(Strings.ErrNotesTransferNotFound, ErrorMessages.ForNotesTransfer("NOT_FOUND"));
        Assert.Equal(Strings.ErrNotesFileAccess, ErrorMessages.ForNotes("IO"));
        Assert.Equal(Strings.ErrNotesFileAccess, ErrorMessages.ForNotesAttachment("IO"));
        // everything else falls back to the notes wording
        Assert.Equal(ErrorMessages.ForNotes("CONFLICT"), ErrorMessages.ForNotesAttachment("CONFLICT"));
        Assert.Equal(ErrorMessages.ForNotes("NOT_INITIALIZED"), ErrorMessages.ForNotesTransfer("NOT_INITIALIZED"));
    }

    [Fact]
    public void ANotesResult_CanCarryItsOwnWording()
    {
        var failed = NotesResult<int>.Fail("TOO_LARGE", ErrorMessages.ForNotesAttachment);
        Assert.Equal(Strings.ErrAttachmentTooLarge, failed.Error);
        Assert.Equal(Strings.ErrNoteTooLarge, NotesResult<int>.Fail("TOO_LARGE").Error);
    }
}
