using System.Text;

namespace ClipBridge.Windows.Logic;

/// <summary>Clipboard text conventions (docs/shell-guide.md §1): the core speaks "\n", Windows speaks "\r\n".</summary>
public static class ClipText
{
    /// <summary>The core accepts at most 64 KiB of UTF-8.</summary>
    public const int MaxBytes = 64 * 1024;

    public const int PreviewChars = 80;

    /// <summary>Clipboard text to core text: "\r\n" and lone "\r" become "\n".</summary>
    public static string ToCore(string text) =>
        text.Contains('\r') ? text.Replace("\r\n", "\n").Replace('\r', '\n') : text;

    /// <summary>Core text to clipboard text: every "\n" becomes "\r\n" (existing "\r\n" is not doubled).</summary>
    public static string ToClipboard(string text) => ToCore(text).Replace("\n", "\r\n");

    public static int Utf8Length(string text) => Encoding.UTF8.GetByteCount(text);

    public static bool IsTooLarge(string text) => Utf8Length(text) > MaxBytes;

    /// <summary>
    /// A one-line preview: control characters and line breaks become single spaces, the result is
    /// trimmed and cut at <paramref name="max"/> characters (never inside a surrogate pair) with an ellipsis.
    /// </summary>
    public static string Preview(string text, int max = PreviewChars)
    {
        var sb = new StringBuilder(Math.Min(text.Length, max + 2));
        var lastSpace = true; // swallows leading whitespace
        foreach (var ch in text)
        {
            var c = char.IsControl(ch) || char.IsWhiteSpace(ch) ? ' ' : ch;
            if (c == ' ')
            {
                if (lastSpace) continue;
                lastSpace = true;
            }
            else lastSpace = false;
            sb.Append(c);
            if (sb.Length > max + 1) break; // enough to know whether we must truncate
        }
        var s = sb.ToString().TrimEnd();
        if (s.Length <= max) return s;
        var cut = max;
        if (char.IsHighSurrogate(s[cut - 1])) cut--;
        return s[..cut].TrimEnd() + "…";
    }
}
