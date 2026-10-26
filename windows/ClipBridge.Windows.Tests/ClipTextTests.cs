using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

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

public class ClipboardDeciderTests
{
    [Theory]
    [InlineData("notify", ClipAction.Notify)]
    [InlineData("manual", ClipAction.NotifyManual)]
    [InlineData("", ClipAction.Notify)]
    [InlineData("something-new", ClipAction.Notify)]
    public void NonAutoDecisions_NeverWrite(string decision, ClipAction expected) =>
        Assert.Equal(expected, ClipboardDecider.Decide(decision, false, 10, 10));

    [Fact]
    public void Auto_WritesOnlyWhenSequenceUnchanged()
    {
        Assert.Equal(ClipAction.WriteNow, ClipboardDecider.Decide("auto", false, 42, 42));
        Assert.Equal(ClipAction.NotifyDowngraded, ClipboardDecider.Decide("auto", false, 42, 43));
        Assert.Equal(ClipAction.NotifyDowngraded, ClipboardDecider.Decide("auto", false, 42, 41));
    }

    [Fact]
    public void Auto_WithUnknownSequence_IsDowngraded()
    {
        Assert.Equal(ClipAction.NotifyDowngraded, ClipboardDecider.Decide("auto", false, 0, 0));
        Assert.Equal(ClipAction.NotifyDowngraded, ClipboardDecider.Decide("auto", false, 5, 0));
        Assert.Equal(ClipAction.NotifyDowngraded, ClipboardDecider.Decide("auto", false, 0, 5));
    }

    [Fact]
    public void Sensitive_NeverWrites_EvenIfCoreSaidAuto()
    {
        Assert.Equal(ClipAction.Notify, ClipboardDecider.Decide("auto", true, 7, 7));
        Assert.Equal(ClipAction.NotifyManual, ClipboardDecider.Decide("manual", true, 7, 7)); // manual = user's click
    }

    [Theory]
    [InlineData(false, 3u, 3u, true)]
    [InlineData(false, 3u, 4u, false)]
    [InlineData(true, 3u, 3u, false)]
    [InlineData(false, 0u, 0u, false)]
    public void StillSafeToWrite_RechecksJustBeforeTheWrite(bool sensitive, uint arrival, uint now, bool expected) =>
        Assert.Equal(expected, ClipboardDecider.StillSafeToWrite(sensitive, arrival, now));

    [Fact]
    public void LocalCapture_Decisions()
    {
        Assert.Equal(LocalCaptureDecision.Send, LocalCapture.Decide("hello", true));
        Assert.Equal(LocalCaptureDecision.SkipSilently, LocalCapture.Decide("", true));
        Assert.Equal(LocalCaptureDecision.SkipSilently, LocalCapture.Decide("hello", false));
        Assert.Equal(LocalCaptureDecision.SkipTooLarge, LocalCapture.Decide(new string('a', ClipText.MaxBytes + 1), true));
        // Capture off: do not even nag about size.
        Assert.Equal(LocalCaptureDecision.SkipSilently, LocalCapture.Decide(new string('a', ClipText.MaxBytes + 1), false));
    }
}
