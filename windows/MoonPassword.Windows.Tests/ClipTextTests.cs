using MoonPassword.Windows.Logic;
using Xunit;

namespace MoonPassword.Windows.Tests;

public class ClipTextTests
{
    [Theory]
    [InlineData("a\r\nb", "a\nb")]
    [InlineData("a\rb", "a\nb")]
    [InlineData("a\nb", "a\nb")]
    [InlineData("a\r\n\r\nb\r\n", "a\n\nb\n")]
    [InlineData("", "")]
    [InlineData("no breaks", "no breaks")]
    public void ToCore_NormalisesLineBreaks(string input, string expected) =>
        Assert.Equal(expected, ClipText.ToCore(input));

    [Theory]
    [InlineData("a\nb", "a\r\nb")]
    [InlineData("a\r\nb", "a\r\nb")] // never doubled
    [InlineData("a\n\nb\n", "a\r\n\r\nb\r\n")]
    [InlineData("plain", "plain")]
    public void ToClipboard_UsesCrLf(string input, string expected) =>
        Assert.Equal(expected, ClipText.ToClipboard(input));

    [Fact]
    public void RoundTrip_IsStable()
    {
        const string core = "line1\nline2\n\n行三 😀\n";
        Assert.Equal(core, ClipText.ToCore(ClipText.ToClipboard(core)));
    }

    [Fact]
    public void Size_IsMeasuredInUtf8Bytes()
    {
        Assert.False(ClipText.IsTooLarge(new string('a', ClipText.MaxBytes)));
        Assert.True(ClipText.IsTooLarge(new string('a', ClipText.MaxBytes + 1)));
        // 3 bytes per CJK character: 21846 characters = 65538 bytes
        Assert.True(ClipText.IsTooLarge(new string('汉', 21846)));
        Assert.False(ClipText.IsTooLarge(new string('汉', 21845)));
    }

    [Fact]
    public void Preview_CollapsesWhitespaceAndTruncates()
    {
        Assert.Equal("a b c", ClipText.Preview("  a\r\n\tb \n c  "));
        var p = ClipText.Preview(new string('x', 200));
        Assert.Equal(ClipText.PreviewChars + 1, p.Length);
        Assert.EndsWith("…", p);
        Assert.Equal("short", ClipText.Preview("short"));
        Assert.Equal("", ClipText.Preview("  \n\t "));
    }

    [Fact]
    public void Preview_NeverSplitsASurrogatePair()
    {
        // 79 ASCII characters followed by an emoji (two UTF-16 units) that straddles the cut.
        var text = new string('a', 79) + "😀" + "tail";
        var p = ClipText.Preview(text);
        Assert.EndsWith("…", p);
        foreach (var (c, i) in p.Select((c, i) => (c, i)))
            if (char.IsHighSurrogate(c)) Assert.True(i + 1 < p.Length && char.IsLowSurrogate(p[i + 1]));
    }
}
