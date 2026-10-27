using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class RenderTreeTests
{
    private static RenderTree Tree(string blocksJson, string extra = "") =>
        RenderTree.FromJson(VJ.Parse($$"""{"note_id":"nt_a","rev_id":"r_1","blocks":{{blocksJson}}{{extra}}}"""));

    [Fact]
    public void Heading_And_Paragraph_WithEveryInlineKind()
    {
        var t = Tree("""
            [{"block_id":"b_1","kind":"heading","raw":"## Hi","level":2,"inline":[{"t":"text","text":"Hi "},{"t":"em","children":[{"t":"text","text":"there"}]}]},
             {"block_id":"b_2","kind":"paragraph","inline":[
               {"t":"text","text":"a "},{"t":"strong","children":[{"t":"text","text":"b"}]},{"t":"text","text":" "},
               {"t":"del","children":[{"t":"text","text":"c"}]},{"t":"code","text":"d()"},{"t":"br"},{"t":"softbr"},
               {"t":"link","href":"https://x.org","title":"X","children":[{"t":"text","text":"site"}]},
               {"t":"image","href":"https://x.org/i.png","alt":"pic","title":"P"}]}]
            """, ""","toc":[{"block_id":"b_1","level":2,"text":"Hi there"}]""");

        Assert.Equal(("nt_a", "r_1"), (t.NoteId, t.RevId));
        Assert.Equal(2, t.Blocks.Count);
        var h = t.Blocks[0];
        Assert.Equal((BlockKind.Heading, 2, "b_1", "## Hi"), (h.Kind, h.Level, h.BlockId, h.Raw));
        Assert.Equal(InlineKind.Em, h.Inline[1].Kind);
        Assert.Equal("there", h.Inline[1].Children.Single().Text);

        var kinds = t.Blocks[1].Inline.Select(i => i.Kind).ToArray();
        Assert.Equal(new[]
        {
            InlineKind.Text, InlineKind.Strong, InlineKind.Text, InlineKind.Del, InlineKind.Code, InlineKind.HardBreak, InlineKind.SoftBreak,
            InlineKind.Link, InlineKind.Image,
        }, kinds);
        Assert.Equal("d()", t.Blocks[1].Inline[4].Text);
        var link = t.Blocks[1].Inline[7];
        Assert.Equal(("https://x.org", "X", "site"), (link.Href, link.Title, link.PlainText()));
        var image = t.Blocks[1].Inline[8];
        Assert.Equal(("https://x.org/i.png", "pic"), (image.Href, image.Text));
        Assert.Equal("pic", image.PlainText());
        var toc = Assert.Single(t.Toc);
        Assert.Equal(("b_1", 2, "Hi there"), (toc.BlockId, toc.Level, toc.Text));
    }

    [Fact]
    public void Code_Quote_Hr_Html_Unknown()
    {
        var t = Tree("""
            [{"kind":"code","lang":"go","info":"go title=x","text":"fmt.Println(1)"},
             {"kind":"quote","children":[{"kind":"paragraph","inline":[{"t":"text","text":"q"}]},{"kind":"quote","children":[{"kind":"hr"}]}]},
             {"kind":"hr","raw":"---"},
             {"kind":"html","text":"<script>alert(1)</script>","raw":"x"},
             {"kind":"unknown","raw":"[^1]: note"},
             {"kind":"from-the-future","text":"t"}]
            """);

        Assert.Equal(6, t.Blocks.Count);
        var code = t.Blocks[0];
        Assert.Equal((BlockKind.Code, "go", "go title=x", "fmt.Println(1)"), (code.Kind, code.Lang, code.Info, code.Text));
        var quote = t.Blocks[1];
        Assert.Equal(BlockKind.Quote, quote.Kind);
        Assert.Equal(2, quote.Children.Count);
        Assert.Equal(BlockKind.Hr, quote.Children[1].Children.Single().Kind);
        Assert.Equal(BlockKind.Hr, t.Blocks[2].Kind);
        Assert.Equal(("<script>alert(1)</script>", BlockKind.Html), (t.Blocks[3].LiteralText, t.Blocks[3].Kind));
        Assert.Equal("[^1]: note", t.Blocks[4].LiteralText);        // an unknown block without text shows its Markdown
        Assert.Equal(BlockKind.Unknown, t.Blocks[5].Kind);          // a kind of a newer core
        Assert.Equal("t", t.Blocks[5].LiteralText);
    }

    [Fact]
    public void List_OrderedStart_Tight_TaskItems_AndNesting()
    {
        var t = Tree("""
            [{"kind":"list","ordered":true,"start":3,"tight":true,"items":[
                {"checked":null,"blocks":[{"kind":"paragraph","inline":[{"t":"text","text":"one"}]},
                   {"kind":"list","items":[{"blocks":[{"kind":"paragraph","inline":[{"t":"text","text":"nested"}]}]}]}]},
                {"checked":true,"blocks":[]},
                {"checked":false}]},
             {"kind":"list","items":[{}]}]
            """);

        var list = t.Blocks[0];
        Assert.Equal((true, 3, true), (list.Ordered, list.Start, list.Tight));
        Assert.Equal(3, list.Items.Count);
        Assert.Null(list.Items[0].Checked);
        Assert.True(list.Items[1].Checked);
        Assert.False(list.Items[2].Checked);
        Assert.Equal(2, list.Items[0].Blocks.Count);
        var nested = list.Items[0].Blocks[1];
        Assert.Equal(BlockKind.List, nested.Kind);
        Assert.False(nested.Ordered);
        Assert.Equal("nested", nested.Items[0].Blocks[0].Inline[0].Text);
        Assert.Empty(list.Items[2].Blocks);              // an item without blocks

        var bare = t.Blocks[1];
        Assert.False(bare.Ordered);
        Assert.False(bare.Tight);
        Assert.Equal(1, bare.Start);                      // the start of a list that says nothing is 1
        Assert.Single(bare.Items);
    }

    [Fact]
    public void Table_HeaderRowsAndAlignment_WithRaggedRows()
    {
        var t = Tree("""
            [{"kind":"table","align":["none","left","center","right","weird"],
              "header":[[{"t":"text","text":"A"}],[{"t":"text","text":"B"}],[],[{"t":"text","text":"D"}]],
              "rows":[[[{"t":"text","text":"1"}],[{"t":"code","text":"2"}]],
                      [[{"t":"text","text":"x"}],[{"t":"text","text":"y"}],[{"t":"text","text":"z"}],[{"t":"text","text":"w"}],[{"t":"text","text":"extra"}]]]}]
            """);

        var table = t.Blocks.Single();
        Assert.Equal(BlockKind.Table, table.Kind);
        Assert.Equal(new[] { ColumnAlign.None, ColumnAlign.Left, ColumnAlign.Center, ColumnAlign.Right, ColumnAlign.None }, table.Align);
        Assert.Equal(4, table.Header.Count);
        Assert.Empty(table.Header[2]);
        Assert.Equal(2, table.Rows.Count);
        Assert.Equal(InlineKind.Code, table.Rows[0][1][0].Kind);
        Assert.Equal(5, NotePreviewStyle.ColumnCount(table));                      // the widest row decides
        Assert.Equal(ColumnAlign.Center, NotePreviewStyle.AlignOf(table, 2));
        Assert.Equal(ColumnAlign.None, NotePreviewStyle.AlignOf(table, 9));        // columns without alignment
        Assert.Equal(ColumnAlign.None, NotePreviewStyle.AlignOf(table, -1));
    }

    [Fact]
    public void OmittedFields_ReadAsDefaults()
    {
        var t = Tree("""[{"kind":"heading"},{"kind":"paragraph"},{"kind":"code"},{"kind":"table"},{"kind":"quote"},{}]""");
        Assert.Equal(6, t.Blocks.Count);
        var h = t.Blocks[0];
        Assert.Equal((1, ""), (h.Level, h.BlockId));
        Assert.Empty(h.Inline);
        Assert.Equal("", t.Blocks[2].Text);
        Assert.Equal(0, NotePreviewStyle.ColumnCount(t.Blocks[3]));
        Assert.Empty(t.Blocks[4].Children);
        Assert.Equal(BlockKind.Unknown, t.Blocks[5].Kind);
        Assert.Empty(t.Toc);
    }

    [Fact]
    public void HeadingLevel_IsClamped()
    {
        var t = Tree("""[{"kind":"heading","level":9},{"kind":"heading","level":0},{"kind":"heading","level":-3}]""");
        Assert.Equal(new[] { 6, 1, 1 }, t.Blocks.Select(b => b.Level).ToArray());
    }

    [Fact]
    public void UnknownInlineKinds_KeepTheirWords()
    {
        var t = Tree("""[{"kind":"paragraph","inline":[{"t":"math","text":"x^2"},{"t":"future","children":[{"t":"text","text":"kept"}]},{"t":"text"}]}]""");
        var runs = t.Blocks[0].Inline;
        Assert.Equal("x^2", runs[0].Text);
        Assert.Equal("kept", RenderInline.PlainText(new[] { runs[1] }));
        Assert.Equal("", runs[2].Text);
    }

    [Fact]
    public void PlainText_FlattensEmphasisLinksAndBreaks()
    {
        var t = Tree("""[{"kind":"paragraph","inline":[{"t":"text","text":"a "},{"t":"em","children":[{"t":"link","href":"h","children":[{"t":"text","text":"b"}]}]},{"t":"br"},{"t":"code","text":"c"}]}]""");
        Assert.Equal("a b\nc", RenderInline.PlainText(t.Blocks[0].Inline));
    }

    [Fact]
    public void WrongShapes_DoNotThrow()
    {
        var t = RenderTree.FromJson(VJ.Parse("""{"blocks":[1,"x",null,{"kind":"paragraph","inline":[3,{"t":"text","text":"ok"}]},{"kind":"table","header":"no","rows":[1,[[]]],"align":[1]}],"toc":"x"}"""));
        Assert.Equal(2, t.Blocks.Count);                 // only objects are blocks
        Assert.Equal("ok", t.Blocks[0].Inline.Single().Text);
        Assert.Empty(t.Blocks[1].Header);
        Assert.Equal(2, t.Blocks[1].Rows.Count);
        Assert.Empty(RenderTree.FromJson(VJ.Parse("null")).Blocks);
        Assert.Empty(RenderTree.FromJson(VJ.Parse("{}")).Toc);
    }

    [Fact]
    public void NestingAsDeepAsTheJsonParserAllows_IsParsed()
    {
        // JsonDocument stops at 64 levels; every quote adds two (object and array), so 25 quotes are the realistic maximum.
        var json = "{\"kind\":\"paragraph\",\"inline\":[{\"t\":\"text\",\"text\":\"deep\"}]}";
        for (var i = 0; i < 25; i++) json = "{\"kind\":\"quote\",\"children\":[" + json + "]}";
        var t = Tree("[" + json + "]");
        var block = t.Blocks[0];
        var depth = 0;
        while (block.Kind == BlockKind.Quote) { block = block.Children[0]; depth++; }
        Assert.Equal(25, depth);
        Assert.Equal("deep", block.Inline[0].Text);
    }

    [Fact]
    public void LargeNote_IsParsedQuickly()
    {
        var paragraphs = string.Join(",", Enumerable.Range(0, 5000).Select(i => $$"""{"kind":"paragraph","inline":[{"t":"text","text":"line {{i}}"}]}"""));
        var started = DateTime.UtcNow;
        var t = Tree("[" + paragraphs + "]");
        Assert.Equal(5000, t.Blocks.Count);
        Assert.True(DateTime.UtcNow - started < TimeSpan.FromSeconds(5));
    }
}
