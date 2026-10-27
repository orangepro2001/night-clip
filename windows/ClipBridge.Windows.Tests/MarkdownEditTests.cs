using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class MarkdownEditTests
{
    private static string After(TextEdit e, string text) => e.ApplyTo(text);

    private static string Selected(TextEdit e, string text) => After(e, text).Substring(e.SelectionStart, e.SelectionLength);

    /// <summary>Applies an action and returns the new text and the text that is selected afterwards.</summary>
    private static (string Text, string Selection) Run(Func<string, int, int, TextEdit> action, string text, int start, int length)
    {
        var edit = action(text, start, length);
        return (After(edit, text), Selected(edit, text));
    }

    /// <summary>Applies an action twice, the second time to the selection the first one left: that must give the original text back.</summary>
    private static void AssertToggles(Func<string, int, int, TextEdit> action, string text, int start, int length)
    {
        var first = action(text, start, length);
        var once = After(first, text);
        Assert.NotEqual(text, once);
        var second = action(once, first.SelectionStart, first.SelectionLength);
        Assert.Equal(text, After(second, once));
    }

    // ---- bold, italic, strikethrough, code --------------------------------------------------------------------

    [Fact]
    public void Bold_WrapsTheSelection_AndSelectsTheWords()
    {
        var (text, selection) = Run(MarkdownEdit.Bold, "hello world", 0, 5);
        Assert.Equal("**hello** world", text);
        Assert.Equal("hello", selection);
    }

    [Fact]
    public void Bold_TakesTheMarksAway_WhetherTheyAreOutsideOrInsideTheSelection()
    {
        Assert.Equal("hello world", Run(MarkdownEdit.Bold, "**hello** world", 2, 5).Text);      // only the word selected
        Assert.Equal("hello world", Run(MarkdownEdit.Bold, "**hello** world", 0, 9).Text);      // the word with its marks
        Assert.Equal("hello", Run(MarkdownEdit.Bold, "**hello** world", 0, 9).Selection);
    }

    [Theory]
    [InlineData("hello world", 0, 5)]
    [InlineData("hello world", 6, 5)]
    [InlineData("你好，世界", 0, 2)]
    [InlineData("你好，世界", 3, 2)]
    [InlineData("😀 hi", 0, 2)]
    [InlineData("a\nb\n\nc", 0, 6)]
    public void EveryInlineAction_IsAToggle(string text, int start, int length)
    {
        AssertToggles(MarkdownEdit.Bold, text, start, length);
        AssertToggles(MarkdownEdit.Italic, text, start, length);
        AssertToggles(MarkdownEdit.Strikethrough, text, start, length);
        AssertToggles(MarkdownEdit.InlineCode, text, start, length);
    }

    [Fact]
    public void Italic_Strikethrough_Code_UseTheirMarkers()
    {
        Assert.Equal("*a* b", Run(MarkdownEdit.Italic, "a b", 0, 1).Text);
        Assert.Equal("~~a~~ b", Run(MarkdownEdit.Strikethrough, "a b", 0, 1).Text);
        Assert.Equal("`a` b", Run(MarkdownEdit.InlineCode, "a b", 0, 1).Text);
    }

    [Fact]
    public void Italic_And_Bold_CombineAndSeparate()
    {
        Assert.Equal("***x***", Run(MarkdownEdit.Italic, "**x**", 2, 1).Text);     // italic inside bold
        Assert.Equal("**x**", Run(MarkdownEdit.Italic, "***x***", 3, 1).Text);      // takes only the italic away
        Assert.Equal("*x*", Run(MarkdownEdit.Bold, "***x***", 3, 1).Text);          // takes only the bold away
        Assert.Equal("***x***", Run(MarkdownEdit.Bold, "*x*", 1, 1).Text);          // bold inside italic
    }

    [Fact]
    public void Inline_EmptySelection_InsertsMarksAroundAPlaceholder_ThatStaysSelected()
    {
        var (text, selection) = Run(MarkdownEdit.Bold, "ab", 1, 0);
        Assert.Equal("a**" + Strings.EditPlaceholderBold + "**b", text);
        Assert.Equal(Strings.EditPlaceholderBold, selection);

        // pressing it again unwraps the placeholder
        var edit = MarkdownEdit.Bold("a**" + Strings.EditPlaceholderBold + "**b", 3, Strings.EditPlaceholderBold.Length);
        Assert.Equal("a" + Strings.EditPlaceholderBold + "b", After(edit, "a**" + Strings.EditPlaceholderBold + "**b"));
    }

    [Fact]
    public void Inline_CaretBetweenEmptyMarks_RemovesThem()
    {
        var edit = MarkdownEdit.Bold("a****b", 3, 0);
        Assert.Equal("ab", After(edit, "a****b"));
        Assert.Equal((1, 0), (edit.SelectionStart, edit.SelectionLength));
        Assert.Equal("ab", After(MarkdownEdit.InlineCode("a``b", 2, 0), "a``b"));
    }

    [Fact]
    public void Inline_SpacesAtTheEdgesOfTheSelection_StayOutsideTheMarks()
    {
        Assert.Equal("  **hi**  ", Run(MarkdownEdit.Bold, "  hi  ", 0, 6).Text);
        Assert.Equal("hi", Run(MarkdownEdit.Bold, "  hi  ", 0, 6).Selection);
        Assert.Equal("a **b c** d", Run(MarkdownEdit.Bold, "a b c d", 1, 5).Text);        // " b c " selected
        Assert.Equal("　**你好**　", Run(MarkdownEdit.Bold, "　你好　", 0, 4).Text);        // ideographic spaces count as spaces
    }

    [Fact]
    public void Inline_SelectionOfOnlySpaces_ActsLikeACaret()
    {
        var (text, selection) = Run(MarkdownEdit.Bold, "a   b", 1, 3);
        Assert.Equal("a**" + Strings.EditPlaceholderBold + "**   b", text);
        Assert.Equal(Strings.EditPlaceholderBold, selection);
    }

    [Fact]
    public void Inline_MultiLineSelection_MarksEachLine_NotTheLineBreaks()
    {
        var (text, selection) = Run(MarkdownEdit.Bold, "one\ntwo\n\nthree", 0, 14);
        Assert.Equal("**one**\n**two**\n\n**three**", text);
        Assert.Equal(text, selection);                          // all of it, marks included, so that a second press finds them
        Assert.Equal("one\ntwo\n\nthree", Run(MarkdownEdit.Bold, text, 0, text.Length).Text);   // and takes them all away again
    }

    [Fact]
    public void Inline_MultiLine_MixedSelection_MarksOnlyWhatIsNotMarkedYet()
    {
        Assert.Equal("**a**\n**b**", Run(MarkdownEdit.Bold, "**a**\nb", 0, 7).Text);
    }

    [Fact]
    public void Inline_CrLfTextIsHandled()
    {
        Assert.Equal("**a**\r\n**b**", Run(MarkdownEdit.Bold, "a\r\nb", 0, 4).Text);
        Assert.Equal("a\r\nb", Run(MarkdownEdit.Bold, "**a**\r\n**b**", 0, 12).Text);
    }

    [Theory]
    [InlineData(-5, 3)]
    [InlineData(2, 100)]
    [InlineData(100, 100)]
    [InlineData(0, -1)]
    public void Inline_OutOfRangeSelections_AreClamped(int start, int length)
    {
        var edit = MarkdownEdit.Bold("abc", start, length);
        var text = After(edit, "abc");
        Assert.Contains("abc".Substring(0, Math.Clamp(start, 0, 3)), text);
        Assert.InRange(edit.SelectionStart, 0, text.Length);
        Assert.InRange(edit.SelectionStart + edit.SelectionLength, 0, text.Length);
    }

    [Fact]
    public void Inline_OnAnEmptyDocument_WorksToo() =>
        Assert.Equal("**" + Strings.EditPlaceholderBold + "**", After(MarkdownEdit.Bold("", 0, 0), ""));

    // ---- headings -------------------------------------------------------------------------------------------

    [Fact]
    public void Heading_AddsTheMarker_ReplacesALevel_AndTogglesOff()
    {
        Assert.Equal("## hello", After(MarkdownEdit.Heading("hello", 0, 0, 2), "hello"));
        Assert.Equal("## hello", After(MarkdownEdit.Heading("# hello", 0, 0, 2), "# hello"));
        Assert.Equal("hello", After(MarkdownEdit.Heading("## hello", 3, 0, 2), "## hello"));
        Assert.Equal("# 你好", After(MarkdownEdit.Heading("你好", 1, 0, 1), "你好"));
    }

    [Fact]
    public void Heading_CaretKeepsItsPlaceInTheLine()
    {
        var edit = MarkdownEdit.Heading("hello", 2, 0, 1);                 // he|llo
        var text = After(edit, "hello");
        Assert.Equal("# hello", text);
        Assert.Equal(("# he", 0), (text[..edit.SelectionStart], edit.SelectionLength));
    }

    [Fact]
    public void Heading_Levels_AreClampedToOneToSix()
    {
        Assert.Equal("###### a", After(MarkdownEdit.Heading("a", 0, 0, 9), "a"));
        Assert.Equal("# a", After(MarkdownEdit.Heading("a", 0, 0, -2), "a"));
    }

    [Fact]
    public void Heading_OnSeveralLines_SkipsBlankOnes_AndStopsAtTheSelection()
    {
        Assert.Equal("# a\n\n# b", After(MarkdownEdit.Heading("a\n\nb", 0, 4, 1), "a\n\nb"));
        Assert.Equal("# a\nb", After(MarkdownEdit.Heading("a\nb", 0, 2, 1), "a\nb"));      // the selection ends after the line break
        Assert.Equal("# a\r\n# b", After(MarkdownEdit.Heading("a\r\nb", 0, 4, 1), "a\r\nb"));
    }

    [Fact]
    public void Heading_OnAnEmptyLine_OrDocument_InsertsTheMarker()
    {
        Assert.Equal("# ", After(MarkdownEdit.Heading("", 0, 0, 1), ""));
        Assert.Equal("a\n## \nb", After(MarkdownEdit.Heading("a\n\nb", 2, 0, 2), "a\n\nb"));
    }

    [Fact]
    public void Heading_WithASelection_IsAToggle()
    {
        AssertToggles((t, s, l) => MarkdownEdit.Heading(t, s, l, 2), "alpha\nbeta", 0, 10);
        AssertToggles((t, s, l) => MarkdownEdit.Heading(t, s, l, 1), "alpha", 0, 5);
    }

    // ---- lists ---------------------------------------------------------------------------------------------

    [Fact]
    public void BulletList_MakesItemsOfEveryLine_AndTogglesOff()
    {
        const string text = "a\nb";
        var (list, _) = Run(MarkdownEdit.BulletList, text, 0, 3);
        Assert.Equal("- a\n- b", list);
        AssertToggles(MarkdownEdit.BulletList, text, 0, 3);
    }

    [Fact]
    public void NumberedList_Numbers_SkippingBlankLines()
    {
        Assert.Equal("1. a\n\n2. b\n3. c", Run(MarkdownEdit.NumberedList, "a\n\nb\nc", 0, 6).Text);
        AssertToggles(MarkdownEdit.NumberedList, "a\nb\nc", 0, 5);
    }

    [Fact]
    public void TaskList_AddsCheckboxes_KeepsTickedOnes_AndTogglesOff()
    {
        Assert.Equal("- [ ] a\n- [ ] b", Run(MarkdownEdit.TaskList, "a\nb", 0, 3).Text);
        Assert.Equal("- [x] a\n- [ ] b", Run(MarkdownEdit.TaskList, "- [x] a\nb", 0, 9).Text);
        Assert.Equal("a\nb", Run(MarkdownEdit.TaskList, "- [ ] a\n- [x] b", 0, 15).Text);
        AssertToggles(MarkdownEdit.TaskList, "a\nb", 0, 3);
    }

    [Fact]
    public void ListKinds_ReplaceEachOther_AndKeepTheIndentation()
    {
        Assert.Equal("1. a\n2. b", Run(MarkdownEdit.NumberedList, "- a\n- b", 0, 7).Text);
        Assert.Equal("- a\n- b", Run(MarkdownEdit.BulletList, "1. a\n2. b", 0, 9).Text);
        Assert.Equal("- [ ] a", Run(MarkdownEdit.TaskList, "- a", 0, 3).Text);
        Assert.Equal("- a", Run(MarkdownEdit.BulletList, "- [ ] a", 0, 7).Text);
        Assert.Equal("  1. a", Run(MarkdownEdit.NumberedList, "  - a", 0, 5).Text);
        Assert.Equal("  - x\n    - y", Run(MarkdownEdit.BulletList, "  x\n    y", 0, 9).Text);
    }

    [Fact]
    public void List_WithOnlyACaret_FormatsThatLine_AndKeepsTheCaretInTheText()
    {
        var edit = MarkdownEdit.BulletList("abc", 3, 0);
        Assert.Equal("- abc", After(edit, "abc"));
        Assert.Equal((5, 0), (edit.SelectionStart, edit.SelectionLength));
        Assert.Equal("1. ", After(MarkdownEdit.NumberedList("", 0, 0), ""));
    }

    [Fact]
    public void List_Cjk_And_CrLf()
    {
        Assert.Equal("- 买菜\n- 写周报", Run(MarkdownEdit.BulletList, "买菜\n写周报", 0, 7).Text);
        Assert.Equal("1. a\r\n2. b", Run(MarkdownEdit.NumberedList, "a\r\nb", 0, 4).Text);
    }

    // ---- quote -------------------------------------------------------------------------------------------------

    [Fact]
    public void Quote_PrefixesEveryLine_KeepsBlankLinesInsideTheQuote_AndTogglesOff()
    {
        Assert.Equal("> a\n> b", Run(MarkdownEdit.Quote, "a\nb", 0, 3).Text);
        Assert.Equal("> a\n>\n> b", Run(MarkdownEdit.Quote, "a\n\nb", 0, 4).Text);
        Assert.Equal("a\n\nb", Run(MarkdownEdit.Quote, "> a\n>\n> b", 0, 9).Text);
        AssertToggles(MarkdownEdit.Quote, "a\n\nb", 0, 4);
        AssertToggles(MarkdownEdit.Quote, "你好", 0, 2);
    }

    [Fact]
    public void Quote_TakesOneLevelAway_AndWorksOnAnEmptyLine()
    {
        Assert.Equal("> a", Run(MarkdownEdit.Quote, "> > a", 0, 5).Text);
        Assert.Equal("> ", After(MarkdownEdit.Quote("", 0, 0), ""));
        Assert.Equal("", After(MarkdownEdit.Quote("> ", 2, 0), "> "));
    }

    // ---- code block -------------------------------------------------------------------------------------------

    [Fact]
    public void CodeBlock_FencesTheLines_AndSelectsTheCode()
    {
        var (text, selection) = Run((t, s, l) => MarkdownEdit.CodeBlock(t, s, l), "a\nb", 0, 3);
        Assert.Equal("```\na\nb\n```", text);
        Assert.Equal("a\nb", selection);
    }

    [Fact]
    public void CodeBlock_TakesTheFenceAway_FromTheCodeOrFromTheWholeBlock()
    {
        const string fenced = "```\ncode\n```";
        Assert.Equal("code", After(MarkdownEdit.CodeBlock(fenced, 4, 4), fenced));          // the code selected
        Assert.Equal("code", After(MarkdownEdit.CodeBlock(fenced, 0, fenced.Length), fenced)); // everything selected
        AssertToggles((t, s, l) => MarkdownEdit.CodeBlock(t, s, l), "x = 1", 0, 5);
    }

    [Fact]
    public void CodeBlock_InsideADocument_OnlyTouchesItsOwnLines()
    {
        Assert.Equal("x\n\n```\ny\n```\n\nz", After(MarkdownEdit.CodeBlock("x\n\ny\n\nz", 3, 1), "x\n\ny\n\nz"));
        const string doc = "x\n\n```\ny\n```\n\nz";
        Assert.Equal("x\n\ny\n\nz", After(MarkdownEdit.CodeBlock(doc, 7, 1), doc));
    }

    [Fact]
    public void CodeBlock_OnAnEmptyLine_MakesAnEmptyBlock_WithTheCaretInside()
    {
        var edit = MarkdownEdit.CodeBlock("", 0, 0);
        Assert.Equal("```\n\n```", After(edit, ""));
        Assert.Equal((4, 0), (edit.SelectionStart, edit.SelectionLength));
    }

    [Fact]
    public void CodeBlock_UsesTheLineBreaksOfTheText_OrTheOnesGiven()
    {
        Assert.Equal("```\r\na\r\nb\r\n```", After(MarkdownEdit.CodeBlock("a\r\nb", 0, 4), "a\r\nb"));
        Assert.Equal("```\r\na\r\n```", After(MarkdownEdit.CodeBlock("a", 0, 1, "\r\n"), "a"));
    }

    // ---- link ------------------------------------------------------------------------------------------------

    [Fact]
    public void Link_SelectionBecomesTheText_AndTheAddressIsSelectedToType()
    {
        var edit = MarkdownEdit.Link("see site now", 4, 4);
        var text = After(edit, "see site now");
        Assert.Equal("see [site](https://) now", text);
        Assert.Equal("https://", text.Substring(edit.SelectionStart, edit.SelectionLength));
    }

    [Fact]
    public void Link_SelectedAddressBecomesTheTarget()
    {
        var edit = MarkdownEdit.Link("https://example.com", 0, 19);
        var text = After(edit, "https://example.com");
        Assert.Equal("[" + Strings.EditPlaceholderLink + "](https://example.com)", text);
        Assert.Equal(Strings.EditPlaceholderLink, text.Substring(edit.SelectionStart, edit.SelectionLength));
        Assert.Contains("(mailto:a@b.cn)", After(MarkdownEdit.Link("mailto:a@b.cn", 0, 13), "mailto:a@b.cn"));
    }

    [Fact]
    public void Link_WithoutSelection_InsertsAPlaceholderLink()
    {
        var edit = MarkdownEdit.Link("ab", 1, 0);
        var text = After(edit, "ab");
        Assert.Equal("a[" + Strings.EditPlaceholderLink + "](https://)b", text);
        Assert.Equal(Strings.EditPlaceholderLink, text.Substring(edit.SelectionStart, edit.SelectionLength));
    }

    [Fact]
    public void Link_SelectedLink_IsTurnedBackIntoItsText()
    {
        const string text = "x [词](https://a.cn) y";
        var edit = MarkdownEdit.Link(text, 2, 17);
        Assert.Equal("x 词 y", After(edit, text));
        Assert.Equal("词", Selected(edit, text));
    }

    [Fact]
    public void Link_CjkSelection()
    {
        var text = After(MarkdownEdit.Link("点这里", 0, 3), "点这里");
        Assert.Equal("[点这里](https://)", text);
    }

    // ---- horizontal rule ---------------------------------------------------------------------------------------

    [Fact]
    public void HorizontalRule_GoesOnItsOwnLine_NeverRightUnderText()
    {
        var edit = MarkdownEdit.HorizontalRule("para", 2, 0);
        var text = After(edit, "para");
        Assert.Equal("para\n\n---\n", text);                    // a blank line first: "---" under text would make a heading
        Assert.Equal(text.Length, edit.SelectionStart);

        Assert.Equal("para\n\n---\n\nnext", After(MarkdownEdit.HorizontalRule("para\n\nnext", 1, 0), "para\n\nnext"));
        Assert.Equal("a\n\n---\nb", After(MarkdownEdit.HorizontalRule("a\nb", 0, 0), "a\nb"));
    }

    [Fact]
    public void HorizontalRule_OnAnEmptyLine_TakesItsPlace()
    {
        Assert.Equal("---\n", After(MarkdownEdit.HorizontalRule("", 0, 0), ""));
        Assert.Equal("para\n\n---\n", After(MarkdownEdit.HorizontalRule("para\n\n", 6, 0), "para\n\n"));
        Assert.Equal("para\n\n---\nnext", After(MarkdownEdit.HorizontalRule("para\n\nnext", 5, 0), "para\n\nnext"));  // the empty line between the two
        Assert.Equal("para\n\n---\n", After(MarkdownEdit.HorizontalRule("para\n", 5, 0), "para\n"));                  // text right above: a blank line first
    }

    [Fact]
    public void HorizontalRule_AfterASelection_GoesBelowItsLastLine_WithTheGivenLineBreak()
    {
        Assert.Equal("a\nb\r\n\r\n---\r\n", After(MarkdownEdit.HorizontalRule("a\nb", 0, 3, "\r\n"), "a\nb"));
        Assert.Equal("a\n\n---\nb", After(MarkdownEdit.HorizontalRule("a\nb", 0, 2), "a\nb"));   // the selection ends after "a\n"
    }

    // ---- the dispatcher -------------------------------------------------------------------------------------

    [Fact]
    public void Apply_RunsTheActionOfTheButton()
    {
        foreach (var action in Enum.GetValues<MarkdownAction>())
        {
            var edit = MarkdownEdit.Apply(action, "text", 0, 4, "\n");
            Assert.NotEqual("text", edit.ApplyTo("text"));        // every button changes something
        }
        Assert.Equal("**text**", After(MarkdownEdit.Apply(MarkdownAction.Bold, "text", 0, 4), "text"));
        Assert.Equal("### text", After(MarkdownEdit.Apply(MarkdownAction.Heading3, "text", 0, 4), "text"));
    }

    [Fact]
    public void TextEdit_ApplyTo_ReplacesTheRange()
    {
        Assert.Equal("a-X-d", new TextEdit(1, 2, "-X-", 0, 0).ApplyTo("abcd"));
        Assert.Equal("abcd!", new TextEdit(4, 0, "!", 0, 0).ApplyTo("abcd"));
        Assert.Equal("", new TextEdit(0, 4, "", 0, 0).ApplyTo("abcd"));
    }

    [Fact]
    public void DetectEol_PrefersCrLfWhenThePresent()
    {
        Assert.Equal("\n", MarkdownEdit.DetectEol("a\nb"));
        Assert.Equal("\r\n", MarkdownEdit.DetectEol("a\r\nb"));
        Assert.Equal("\n", MarkdownEdit.DetectEol(""));
    }
}
