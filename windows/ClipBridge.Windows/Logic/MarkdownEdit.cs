using System.Text;
using System.Text.RegularExpressions;

namespace ClipBridge.Windows.Logic;

/// <summary>
/// One change of the source text: <see cref="RemoveLength"/> characters at <see cref="Start"/> are replaced by
/// <see cref="Insert"/>, then <see cref="SelectionLength"/> characters at <see cref="SelectionStart"/> (positions in the NEW
/// text) are selected. A single splice keeps the text box's undo history intact (it is applied as a replacement of the
/// selection, not by assigning the whole text).
/// </summary>
public readonly record struct TextEdit(int Start, int RemoveLength, string Insert, int SelectionStart, int SelectionLength)
{
    public string ApplyTo(string text) => string.Concat(text.AsSpan(0, Start), Insert, text.AsSpan(Start + RemoveLength));
}

/// <summary>What a toolbar button or shortcut does to the source text.</summary>
public enum MarkdownAction
{
    Bold, Italic, Strikethrough, InlineCode,
    Heading1, Heading2, Heading3,
    BulletList, NumberedList, TaskList, Quote, CodeBlock, Link, HorizontalRule,
}

/// <summary>
/// The formatting actions of the editor as pure functions: text and selection in, one <see cref="TextEdit"/> out. Every
/// action is a toggle (applying it to its own result takes it back), keeps the selection on the formatted text so that it
/// can be applied again, and never touches text outside the selected lines or words. The shell edits Markdown source only;
/// what the Markdown means is the core's business, so these functions only insert and remove the markers.
/// Positions are indexes into the text (UTF-16 chars, line breaks "\n" or "\r\n"). New line breaks are the <c>eol</c>
/// argument, by default the one the text already uses.
/// </summary>
public static class MarkdownEdit
{
    public static TextEdit Apply(MarkdownAction action, string text, int selStart, int selLength, string? eol = null) => action switch
    {
        MarkdownAction.Bold => Bold(text, selStart, selLength),
        MarkdownAction.Italic => Italic(text, selStart, selLength),
        MarkdownAction.Strikethrough => Strikethrough(text, selStart, selLength),
        MarkdownAction.InlineCode => InlineCode(text, selStart, selLength),
        MarkdownAction.Heading1 => Heading(text, selStart, selLength, 1),
        MarkdownAction.Heading2 => Heading(text, selStart, selLength, 2),
        MarkdownAction.Heading3 => Heading(text, selStart, selLength, 3),
        MarkdownAction.BulletList => BulletList(text, selStart, selLength),
        MarkdownAction.NumberedList => NumberedList(text, selStart, selLength),
        MarkdownAction.TaskList => TaskList(text, selStart, selLength),
        MarkdownAction.Quote => Quote(text, selStart, selLength),
        MarkdownAction.CodeBlock => CodeBlock(text, selStart, selLength, eol),
        MarkdownAction.Link => Link(text, selStart, selLength),
        _ => HorizontalRule(text, selStart, selLength, eol),
    };

    public static string DetectEol(string text) => text.Contains("\r\n") ? "\r\n" : "\n";

    private static void Clamp(string t, ref int selStart, ref int selLength)
    {
        selStart = Math.Clamp(selStart, 0, t.Length);
        selLength = Math.Clamp(selLength, 0, t.Length - selStart);
    }

    // =============================================================================================================
    //  Inline marks: **bold**, *italic*, ~~strikethrough~~, `code`
    // =============================================================================================================

    private readonly record struct Mark(string Marker, char Char, string Placeholder)
    {
        public int Length => Marker.Length;
    }

    private static readonly Mark BoldMark = new("**", '*', Strings.EditPlaceholderBold);
    private static readonly Mark ItalicMark = new("*", '*', Strings.EditPlaceholderItalic);
    private static readonly Mark StrikeMark = new("~~", '~', Strings.EditPlaceholderStrike);
    private static readonly Mark CodeMark = new("`", '`', Strings.EditPlaceholderCode);

    public static TextEdit Bold(string text, int selStart, int selLength) => ToggleInline(text, selStart, selLength, BoldMark);
    public static TextEdit Italic(string text, int selStart, int selLength) => ToggleInline(text, selStart, selLength, ItalicMark);
    public static TextEdit Strikethrough(string text, int selStart, int selLength) => ToggleInline(text, selStart, selLength, StrikeMark);
    public static TextEdit InlineCode(string text, int selStart, int selLength) => ToggleInline(text, selStart, selLength, CodeMark);

    /// <summary>How a piece of selected text sits in a mark: not at all, marks right outside it, or marks at its two ends.</summary>
    private enum Wrapping { None, Outside, Inside }

    private static int RunBefore(string t, int i, char c)
    {
        var n = 0;
        while (i - 1 - n >= 0 && t[i - 1 - n] == c) n++;
        return n;
    }

    private static int RunAfter(string t, int i, char c)
    {
        var n = 0;
        while (i + n < t.Length && t[i + n] == c) n++;
        return n;
    }

    /// <summary>Whether a run of mark characters contains this mark: italic is an odd number of asterisks, the others need their length.</summary>
    private static bool RunHasMark(int run, Mark m) => m.Marker == "*" ? run % 2 == 1 : run >= m.Length;

    private static Wrapping WrappingOf(string t, int a, int b, Mark m)
    {
        var lead = Math.Min(RunAfter(t, a, m.Char), b - a);
        var trail = Math.Min(RunBefore(t, b, m.Char), b - a);
        var inside = Math.Min(lead, trail);
        if (lead + trail < b - a && inside >= m.Length && RunHasMark(inside, m)) return Wrapping.Inside;
        var outside = Math.Min(RunBefore(t, a, m.Char), RunAfter(t, b, m.Char));
        return outside >= m.Length && RunHasMark(outside, m) ? Wrapping.Outside : Wrapping.None;
    }

    /// <summary>The pieces of the selection that can carry a mark: per line, without the spaces at both ends, blank ones skipped.</summary>
    private static List<(int A, int B)> Segments(string t, int start, int end)
    {
        var result = new List<(int, int)>();
        var i = start;
        while (i <= end)
        {
            var nl = t.IndexOf('\n', i, end - i);
            var a = i;
            var b = nl < 0 ? end : nl;
            while (a < b && char.IsWhiteSpace(t[a])) a++;
            while (b > a && char.IsWhiteSpace(t[b - 1])) b--;
            if (b > a) result.Add((a, b));
            if (nl < 0) break;
            i = nl + 1;
        }
        return result;
    }

    /// <summary>One piece of the selection after the action: <c>[Lo, Hi)</c> of the old text becomes Before + Inner + After.</summary>
    private readonly record struct Piece(int Lo, int Hi, string Before, string Inner, string After);

    private static TextEdit ToggleInline(string t, int selStart, int selLength, Mark m)
    {
        Clamp(t, ref selStart, ref selLength);
        var segments = selLength == 0 ? new List<(int A, int B)>() : Segments(t, selStart, selStart + selLength);
        if (segments.Count == 0) return InlineAtCaret(t, selStart, m);

        var states = segments.Select(s => WrappingOf(t, s.A, s.B, m)).ToList();
        var unwrap = states.All(s => s != Wrapping.None); // everything is marked already: take the marks away
        var pieces = new List<Piece>();
        for (var i = 0; i < segments.Count; i++)
        {
            var (a, b) = segments[i];
            var plain = t.Substring(a, b - a);
            pieces.Add((unwrap, states[i]) switch
            {
                (true, Wrapping.Outside) => new Piece(a - m.Length, b + m.Length, "", plain, ""),
                (true, _) => new Piece(a, b, "", t.Substring(a + m.Length, b - a - 2 * m.Length), ""),
                (false, Wrapping.None) => new Piece(a, b, m.Marker, plain, m.Marker),
                _ => new Piece(a, b, "", plain, ""), // marked already while others are not: left as it is
            });
        }

        var first = pieces[0].Lo;
        var sb = new StringBuilder();
        var cursor = first;
        var selFrom = 0;
        var selTo = 0;
        foreach (var p in pieces)
        {
            sb.Append(t, cursor, p.Lo - cursor).Append(p.Before);
            if (p.Lo == first) selFrom = sb.Length;
            sb.Append(p.Inner);
            selTo = sb.Length;
            sb.Append(p.After);
            cursor = p.Hi;
        }
        // One piece: select its words (the marks are just outside, which is how a second press finds them). Several pieces: select
        // all of it, marks included, so that a second press finds every mark inside the selection.
        if (pieces.Count > 1)
        {
            selFrom = 0;
            selTo = sb.Length;
        }
        return new TextEdit(first, cursor - first, sb.ToString(), first + selFrom, selTo - selFrom);
    }

    /// <summary>Nothing selected: "**|**" is taken away again, anything else gets the marks around a placeholder that stays selected.</summary>
    private static TextEdit InlineAtCaret(string t, int caret, Mark m)
    {
        if (RunBefore(t, caret, m.Char) == m.Length && RunAfter(t, caret, m.Char) == m.Length)
            return new TextEdit(caret - m.Length, 2 * m.Length, "", caret - m.Length, 0);
        return new TextEdit(caret, 0, m.Marker + m.Placeholder + m.Marker, caret + m.Length, m.Placeholder.Length);
    }

    // =============================================================================================================
    //  Lines: headings, lists, quote, code block
    // =============================================================================================================

    private readonly record struct Ln(string Content, string Eol);

    /// <summary>A line of the selection; <see cref="Target"/> lines are the ones an action changes (blank lines in between are not).</summary>
    private readonly record struct Item(string Content, bool Target);

    private static int LineStart(string t, int i) => i <= 0 ? 0 : t.LastIndexOf('\n', i - 1) + 1;

    /// <summary>The end of the line that contains <paramref name="i"/>, before its line break ("\r\n" included).</summary>
    private static int LineEnd(string t, int i, int lineStart)
    {
        var nl = t.IndexOf('\n', Math.Min(i, t.Length));
        var end = nl < 0 ? t.Length : nl;
        return nl >= 0 && end > lineStart && t[end - 1] == '\r' ? end - 1 : end;
    }

    /// <summary>The whole lines the selection touches (not the line after a selection that ends with a line break), without the last line break.</summary>
    private static (int Start, int End) Region(string t, int selStart, int selLength)
    {
        var start = LineStart(t, selStart);
        var pos = selStart + selLength;
        if (selLength > 0 && pos > start && t[pos - 1] == '\n') pos--;
        return (start, LineEnd(t, pos, start));
    }

    private static List<Ln> SplitLines(string region)
    {
        var list = new List<Ln>();
        var i = 0;
        while (true)
        {
            var nl = region.IndexOf('\n', i);
            if (nl < 0)
            {
                list.Add(new Ln(region[i..], ""));
                return list;
            }
            var contentEnd = nl > i && region[nl - 1] == '\r' ? nl - 1 : nl;
            list.Add(new Ln(region[i..contentEnd], region[contentEnd..(nl + 1)]));
            i = nl + 1;
        }
    }

    private static bool IsBlank(string content) => string.IsNullOrWhiteSpace(content);

    /// <summary>
    /// Rewrites the lines the selection touches: <paramref name="rewrite"/> gets every line (the non-blank ones are the targets;
    /// when all are blank the first one is) and returns the new content of every line. A selection stays a selection over the
    /// changed lines so that the action can be applied again; a caret keeps its distance to the end of its line.
    /// </summary>
    private static TextEdit EditLines(string t, int selStart, int selLength, Func<IReadOnlyList<Item>, IReadOnlyList<string>> rewrite)
    {
        Clamp(t, ref selStart, ref selLength);
        var (start, end) = Region(t, selStart, selLength);
        var lines = SplitLines(t[start..end]);
        var items = lines.Select(l => new Item(l.Content, !IsBlank(l.Content))).ToList();
        if (!items.Any(i => i.Target)) items[0] = items[0] with { Target = true };

        var contents = rewrite(items);
        var sb = new StringBuilder();
        for (var i = 0; i < lines.Count; i++) sb.Append(contents[i]).Append(lines[i].Eol);
        var replacement = sb.ToString();
        if (selLength == 0)
        {
            var caret = Math.Max(start, start + replacement.Length - (end - selStart));
            return new TextEdit(start, end - start, replacement, caret, 0);
        }
        return new TextEdit(start, end - start, replacement, start, replacement.Length);
    }

    // ---- headings ---------------------------------------------------------------------------------------------

    private static readonly Regex HeadingRx = new(@"^ {0,3}(#{1,6})[ \t]+", RegexOptions.Compiled);

    private static int HeadingLevel(string content) => HeadingRx.Match(content) is { Success: true } m ? m.Groups[1].Length : 0;

    /// <summary>"# " to "###### " in front of every line; the same level again takes it away.</summary>
    public static TextEdit Heading(string text, int selStart, int selLength, int level)
    {
        level = Math.Clamp(level, 1, 6);
        var prefix = new string('#', level) + " ";
        return EditLines(text, selStart, selLength, items =>
        {
            var remove = items.Where(i => i.Target).All(i => HeadingLevel(i.Content) == level);
            return items.Select(i => !i.Target ? i.Content : remove ? HeadingRx.Replace(i.Content, "") : prefix + HeadingRx.Replace(i.Content, "")).ToList();
        });
    }

    // ---- lists ------------------------------------------------------------------------------------------------

    private enum ListKind { None, Bullet, Number, Task }

    private static readonly Regex TaskRx = new(@"^(\s*)[-*+]\s+\[[ xX]\](?:\s+(.*))?$", RegexOptions.Compiled);
    private static readonly Regex BulletRx = new(@"^(\s*)[-*+]\s+(.*)$", RegexOptions.Compiled);
    private static readonly Regex NumberRx = new(@"^(\s*)\d{1,9}[.)]\s+(.*)$", RegexOptions.Compiled);

    /// <summary>Splits a line into its indentation, list kind and text without the marker.</summary>
    private static (string Indent, ListKind Kind, string Remainder) ParseListLine(string content)
    {
        if (TaskRx.Match(content) is { Success: true } t) return (t.Groups[1].Value, ListKind.Task, t.Groups[2].Value);
        if (BulletRx.Match(content) is { Success: true } b) return (b.Groups[1].Value, ListKind.Bullet, b.Groups[2].Value);
        if (NumberRx.Match(content) is { Success: true } n) return (n.Groups[1].Value, ListKind.Number, n.Groups[2].Value);
        var rest = content.TrimStart();
        return (content[..(content.Length - rest.Length)], ListKind.None, rest);
    }

    /// <summary>
    /// Makes every line a list item of <paramref name="kind"/> (other list markers are replaced, indentation is kept); when all
    /// of them are items of that kind already, the markers are taken away.
    /// </summary>
    private static TextEdit ToggleList(string text, int selStart, int selLength, ListKind kind) =>
        EditLines(text, selStart, selLength, items =>
        {
            var remove = items.Where(i => i.Target).All(i => ParseListLine(i.Content).Kind == kind);
            var result = new List<string>(items.Count);
            var number = 0;
            foreach (var item in items)
            {
                if (!item.Target) { result.Add(item.Content); continue; }
                var (indent, current, rest) = ParseListLine(item.Content);
                if (remove) { result.Add(indent + rest); continue; }
                if (kind == ListKind.Task && current == ListKind.Task) { result.Add(item.Content); continue; } // keeps [x]
                number++;
                result.Add(kind switch
                {
                    ListKind.Number => indent + number + ". " + rest,
                    ListKind.Task => indent + "- [ ] " + rest,
                    _ => indent + "- " + rest,
                });
            }
            return result;
        });

    public static TextEdit BulletList(string text, int selStart, int selLength) => ToggleList(text, selStart, selLength, ListKind.Bullet);
    public static TextEdit NumberedList(string text, int selStart, int selLength) => ToggleList(text, selStart, selLength, ListKind.Number);
    public static TextEdit TaskList(string text, int selStart, int selLength) => ToggleList(text, selStart, selLength, ListKind.Task);

    // ---- quote ------------------------------------------------------------------------------------------------

    private static readonly Regex QuoteRx = new(@"^ {0,3}>[ ]?", RegexOptions.Compiled);

    /// <summary>"> " in front of every line (blank lines in between get a bare ">" so the quote stays one block); again takes one level away.</summary>
    public static TextEdit Quote(string text, int selStart, int selLength) =>
        EditLines(text, selStart, selLength, items =>
        {
            var remove = items.Where(i => i.Target).All(i => QuoteRx.IsMatch(i.Content));
            return items.Select(i =>
                i.Target ? (remove ? QuoteRx.Replace(i.Content, "", 1) : "> " + i.Content)
                : remove || items.Count == 1 ? i.Content : ">").ToList();
        });

    // ---- code block ---------------------------------------------------------------------------------------------

    private static bool IsFence(string content) => content.TrimStart().StartsWith("```", StringComparison.Ordinal);

    /// <summary>Puts the lines of the selection (the line of the caret) in a ``` fence of their own; fenced lines are taken out of it again.</summary>
    public static TextEdit CodeBlock(string text, int selStart, int selLength, string? eol = null)
    {
        Clamp(text, ref selStart, ref selLength);
        eol ??= DetectEol(text);
        var (start, end) = Region(text, selStart, selLength);
        var lines = SplitLines(text[start..end]);

        // the selection includes both fences
        if (lines.Count >= 2 && IsFence(lines[0].Content) && IsFence(lines[^1].Content))
        {
            var inner = lines.Skip(1).Take(lines.Count - 2).ToList();
            var kept = string.Concat(inner.Select((l, i) => l.Content + (i < inner.Count - 1 ? l.Eol : "")));
            return new TextEdit(start, end - start, kept, start, kept.Length);
        }

        // the fences are the lines right above and below the selection
        if (start > 0 && end < text.Length)
        {
            var prevStart = LineStart(text, start - 1);
            var prevEnd = LineEnd(text, start - 1, prevStart);
            var nextStart = end + (text[end] == '\r' ? 2 : 1);
            if (nextStart <= text.Length)
            {
                var nextEnd = LineEnd(text, nextStart, nextStart);
                if (IsFence(text[prevStart..prevEnd]) && IsFence(text[nextStart..nextEnd]))
                {
                    var inside = text[start..end];
                    return new TextEdit(prevStart, nextEnd - prevStart, inside, prevStart, inside.Length);
                }
            }
        }

        var region = text[start..end];
        return new TextEdit(start, end - start, "```" + eol + region + eol + "```", start + 3 + eol.Length, region.Length);
    }

    // =============================================================================================================
    //  Link and rule
    // =============================================================================================================

    private static readonly Regex WholeLinkRx = new(@"^\[([^\[\]\r\n]*)\]\(([^()\r\n]*)\)$", RegexOptions.Compiled);
    private static readonly Regex UrlRx = new(@"^(?:https?://|mailto:)\S+$", RegexOptions.Compiled | RegexOptions.IgnoreCase);

    /// <summary>
    /// [text](url): a selection becomes the link text (a selected address becomes the target instead), nothing selected inserts
    /// a link with placeholders. A selected link is turned back into its text.
    /// </summary>
    public static TextEdit Link(string text, int selStart, int selLength)
    {
        Clamp(text, ref selStart, ref selLength);
        var selected = text.Substring(selStart, selLength);
        if (WholeLinkRx.Match(selected) is { Success: true } whole)
        {
            var label = whole.Groups[1].Value;
            return new TextEdit(selStart, selLength, label, selStart, label.Length);
        }
        if (selLength == 0)
            return new TextEdit(selStart, 0, "[" + Strings.EditPlaceholderLink + "](" + Strings.EditPlaceholderUrl + ")", selStart + 1, Strings.EditPlaceholderLink.Length);
        if (UrlRx.IsMatch(selected.Trim()))
            return new TextEdit(selStart, selLength, "[" + Strings.EditPlaceholderLink + "](" + selected + ")", selStart + 1, Strings.EditPlaceholderLink.Length);
        return new TextEdit(selStart, selLength, "[" + selected + "](" + Strings.EditPlaceholderUrl + ")",
            selStart + selected.Length + 3, Strings.EditPlaceholderUrl.Length);
    }

    /// <summary>
    /// Puts <paramref name="markdown"/> (the image or link syntax of a new attachment) on a line of its own after the selection:
    /// a line break first unless the caret is at the start of a line, one after it. The selection is not replaced; the caret ends
    /// up on the line below, ready for the next one.
    /// </summary>
    public static TextEdit InsertBlock(string text, int selStart, int selLength, string markdown, string? eol = null)
    {
        Clamp(text, ref selStart, ref selLength);
        eol ??= DetectEol(text);
        var at = selStart + selLength;
        var insert = (at == 0 || text[at - 1] == '\n' ? "" : eol) + markdown + eol;
        return new TextEdit(at, 0, insert, at + insert.Length, 0);
    }

    private static bool PreviousLineBlank(string t, int lineStart)
    {
        if (lineStart == 0) return true;
        var prevStart = LineStart(t, lineStart - 1);
        return IsBlank(t[prevStart..LineEnd(t, lineStart - 1, prevStart)]);
    }

    /// <summary>
    /// A "---" line of its own after the line the selection ends on, with a blank line before it (right under text it would turn
    /// that text into a heading). The caret ends up after the rule.
    /// </summary>
    public static TextEdit HorizontalRule(string text, int selStart, int selLength, string? eol = null)
    {
        Clamp(text, ref selStart, ref selLength);
        eol ??= DetectEol(text);
        var (_, lineEnd) = Region(text, selStart, selLength);
        var lineStart = LineStart(text, lineEnd);
        var atEnd = lineEnd >= text.Length;
        if (IsBlank(text[lineStart..lineEnd]))
        {
            // an empty line: the rule takes its place, with a blank line before it unless there is one already
            var onEmpty = (PreviousLineBlank(text, lineStart) ? "" : eol) + "---" + (atEnd ? eol : "");
            return new TextEdit(lineStart, lineEnd - lineStart, onEmpty, lineStart + onEmpty.Length, 0);
        }
        var after = eol + eol + "---" + (atEnd ? eol : "");
        return new TextEdit(lineEnd, 0, after, lineEnd + after.Length, 0);
    }
}
