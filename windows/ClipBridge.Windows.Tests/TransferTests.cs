using System.Text.Json;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class FormattingTests
{
    [Theory]
    [InlineData(0, "0 B")]
    [InlineData(1023, "1023 B")]
    [InlineData(1024, "1.0 KB")]
    [InlineData(1536, "1.5 KB")]
    [InlineData(1048576, "1.0 MB")]
    [InlineData(52428800, "50.0 MB")]
    [InlineData(123L * 1024 * 1024, "123 MB")]
    [InlineData(4294967296, "4.0 GB")]
    [InlineData(-5, "0 B")]
    public void Bytes(long n, string expected) => Assert.Equal(expected, Formatting.Bytes(n));

    [Fact]
    public void Speed()
    {
        Assert.Equal("50.0 MB/s", Formatting.Speed(52428800));
        Assert.Equal("1.0 KB/s", Formatting.Speed(1024));
        Assert.Equal(Strings.Dash, Formatting.Speed(0));
    }

    [Theory]
    [InlineData(100, 10, "10 秒")]
    [InlineData(100, 30, "4 秒")]               // 3.33 s rounds up
    [InlineData(1000, 10, "1 分钟 40 秒")]
    [InlineData(600, 10, "1 分钟")]
    [InlineData(36000, 10, "1 小时")]
    [InlineData(39000, 10, "1 小时 5 分钟")]
    public void Eta(long remaining, long rate, string expected) => Assert.Equal(expected, Formatting.Eta(remaining, rate));

    [Fact]
    public void Eta_UnknownWhenNoRateOrNothingLeft()
    {
        Assert.Equal("", Formatting.Eta(100, 0));
        Assert.Equal("", Formatting.Eta(0, 100));
    }

    [Fact]
    public void LastSeen()
    {
        var now = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000);
        Assert.Equal(Strings.LastSeenNever, Formatting.LastSeen(0, now));
        Assert.Equal(Strings.LastSeenNow, Formatting.LastSeen(1_789_999_970, now));
        Assert.Equal("5 分钟前", Formatting.LastSeen(1_790_000_000 - 300, now));
        Assert.Equal("3 小时前", Formatting.LastSeen(1_790_000_000 - 3 * 3600, now));
        Assert.Equal("2 天前", Formatting.LastSeen(1_790_000_000 - 2 * 86400, now));
        Assert.Equal("5 分钟前", Formatting.LastSeen((1_790_000_000 - 300) * 1000L, now)); // milliseconds tolerated
    }

    [Fact]
    public void Time_TodayVsEarlier()
    {
        var utc = TimeZoneInfo.Utc;
        var now = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000); // 2026-09-21 ~13:46 UTC
        Assert.Equal(now.AddSeconds(-600).UtcDateTime.ToString("HH:mm"), Formatting.Time(1_790_000_000 - 600, now, utc));
        Assert.Matches(@"^\d\d-\d\d \d\d:\d\d$", Formatting.Time(1_790_000_000 - 3 * 86400, now, utc));
        Assert.Equal("", Formatting.Time(0, now, utc));
    }
}

public class FileDisplayTests
{
    [Fact]
    public void IsPreviewableImage_ByTypeAndSize()
    {
        Assert.True(FileDisplay.IsPreviewableImage("IMG_20261028_133012.jpg", 2_000_000));
        Assert.True(FileDisplay.IsPreviewableImage("截图.PNG", 10));
        Assert.True(FileDisplay.IsPreviewableImage("a.webp", FileDisplay.MaxPreviewBytes));
        Assert.False(FileDisplay.IsPreviewableImage("a.webp", FileDisplay.MaxPreviewBytes + 1));
        Assert.False(FileDisplay.IsPreviewableImage("a.png", 0));
        Assert.False(FileDisplay.IsPreviewableImage("movie.mp4", 1000));
        Assert.False(FileDisplay.IsPreviewableImage("photo.heic", 1000));
        Assert.False(FileDisplay.IsPreviewableImage("evil\u202Egnp.exe", 1000));
        Assert.False(FileDisplay.IsPreviewableImage(null, 1000));
    }

    [Fact]
    public void Sanitize_RemovesControlsAndBidiOverrides()
    {
        // U+202E (RLO) makes "evilfdp.exe" render as "evilexe.pdf".
        Assert.Equal("evilfdp.exe", FileDisplay.Sanitize("evil\u202Efdp.exe"));
        Assert.Equal("ab", FileDisplay.Sanitize("a\0\u0007b"));
        Assert.Equal("ab", FileDisplay.Sanitize("a\u200Bb\uFEFF"));
        Assert.Equal("", FileDisplay.Sanitize(null));
        Assert.Equal("文件 (1).pdf", FileDisplay.Sanitize("  文件 (1).pdf "));
    }

    [Fact]
    public void Shorten_KeepsTheExtension()
    {
        Assert.Equal("short.pdf", FileDisplay.Shorten("short.pdf", 20));
        var s = FileDisplay.Shorten(new string('x', 80) + ".pdf", 24);
        Assert.Equal(24, s.Length);
        Assert.EndsWith("….pdf", s);
    }

    [Fact]
    public void Shorten_HandlesNoExtensionAndSurrogates()
    {
        var s = FileDisplay.Shorten(new string('y', 100), 20);
        Assert.Equal(20, s.Length);
        Assert.EndsWith("…", s);
        var emoji = FileDisplay.Shorten(string.Concat(Enumerable.Repeat("😀", 40)), 21);
        foreach (var (c, i) in emoji.Select((c, i) => (c, i)))
            if (char.IsHighSurrogate(c)) Assert.True(i + 1 < emoji.Length && char.IsLowSurrogate(emoji[i + 1]));
    }

    [Theory]
    [InlineData("setup.exe", true)]
    [InlineData("SETUP.EXE", true)]
    [InlineData("run.ps1", true)]
    [InlineData("doc.pdf", false)]
    [InlineData("noext", false)]
    [InlineData("evil\u202Efdp.exe", true)]
    public void IsExecutable(string name, bool expected) => Assert.Equal(expected, FileDisplay.IsExecutable(name));

    [Theory]
    [InlineData(@"C:\Users\me\a.pdf", "a.pdf", @"C:\Users\me")]
    [InlineData("/tmp/x/b.txt", "b.txt", "/tmp/x")]
    [InlineData("plain.txt", "plain.txt", "")]
    [InlineData("", "", "")]
    public void PathParts(string path, string file, string dir)
    {
        Assert.Equal(file, FileDisplay.FileNameOf(path));
        Assert.Equal(dir, FileDisplay.DirectoryOf(path));
    }
}

public class TransferTests
{
    private static CoreEvent Ev(string json) => CoreEventParser.Parse(json);

    private static JsonElement List(string json)
    {
        using var d = JsonDocument.Parse(json);
        return d.RootElement.Clone();
    }

    private const string Offered =
        "{\"type\":\"file.offered\",\"transfer_id\":\"tr_1\",\"from\":\"dev_a\",\"from_name\":\"Pixel\",\"name\":\"photo.jpg\",\"size\":10485760}";

    [Fact]
    public void Offer_CreatesAnIncomingRowAtTheTop()
    {
        var t = new TransferTracker();
        t.Apply(Ev(Offered));
        var r = Assert.Single(t.Rows);
        Assert.True(r.Incoming);
        Assert.Equal("offered", r.State);
        Assert.True(r.CanAccept);
        Assert.True(r.CanReject);
        Assert.False(r.CanCancel);
        Assert.Equal("10.0 MB", r.SizeText);
        Assert.Equal("Pixel", r.PeerName);
        Assert.Equal(Strings.TransferOfferedIn, r.StateText);
    }

    [Fact]
    public void ProgressFlow_ComputesPercentSpeedAndEta()
    {
        var t = new TransferTracker();
        t.Apply(Ev(Offered));
        Assert.True(t.Apply(Ev("{\"type\":\"file.state\",\"transfer_id\":\"tr_1\",\"state\":\"accepted\"}")));
        Assert.True(t.Apply(Ev("{\"type\":\"file.progress\",\"transfer_id\":\"tr_1\",\"offset\":5242880,\"size\":10485760,\"rate_bps\":1048576}")));
        var r = t.Rows[0];
        Assert.Equal("transferring", r.State);
        Assert.Equal(0.5, r.Progress, 3);
        Assert.Equal("1.0 MB/s", r.SpeedText);
        Assert.Equal("5 秒", r.EtaText);
        Assert.Contains("50%", r.ProgressText);
        Assert.Contains("1.0 MB/s", r.Detail);
        Assert.True(r.CanCancel);
        Assert.True(r.ShowProgress);
        Assert.False(r.CanOpen);
    }

    [Fact]
    public void Done_EnablesOpen_And_ClearsRate()
    {
        var t = new TransferTracker();
        t.Apply(Ev(Offered));
        t.Apply(Ev("{\"type\":\"file.progress\",\"transfer_id\":\"tr_1\",\"offset\":1,\"size\":10485760,\"rate_bps\":99}"));
        t.Apply(Ev("{\"type\":\"file.done\",\"transfer_id\":\"tr_1\",\"direction\":\"in\",\"path\":\"C:\\\\d\\\\photo.jpg\",\"name\":\"photo.jpg\",\"size\":10485760}"));
        var r = t.Rows[0];
        Assert.Equal("done", r.State);
        Assert.Equal(1.0, r.Progress, 3);
        Assert.True(r.CanOpen);
        Assert.False(r.CanCancel);
        Assert.Equal(0, r.RateBps);
        Assert.Equal(@"C:\d\photo.jpg", r.Path);
        Assert.Equal(Strings.TransferDone, r.StateText);
    }

    [Fact]
    public void Failure_ShowsTheMappedReason()
    {
        var t = new TransferTracker();
        t.Apply(Ev(Offered));
        t.Apply(Ev("{\"type\":\"file.failed\",\"transfer_id\":\"tr_1\",\"code\":\"HASH_MISMATCH\"}"));
        var r = t.Rows[0];
        Assert.Equal("failed", r.State);
        Assert.Contains("文件校验失败，请让对方重发", r.StateText);
        Assert.True(r.IsTerminal);
        Assert.False(r.CanCancel);
    }

    [Fact]
    public void UnknownTransfer_AsksForAReload()
    {
        var t = new TransferTracker();
        Assert.False(t.Apply(Ev("{\"type\":\"file.progress\",\"transfer_id\":\"tr_x\",\"offset\":1,\"size\":2,\"rate_bps\":3}")));
        Assert.False(t.Apply(Ev("{\"type\":\"file.state\",\"transfer_id\":\"tr_x\",\"state\":\"accepted\"}")));
        Assert.Empty(t.Rows);
    }

    [Fact]
    public void ReplaceAll_LoadsTheCoreList_AndKeepsRowIdentity()
    {
        var t = new TransferTracker();
        var json = "{\"transfers\":[" +
                   "{\"transfer_id\":\"tr_2\",\"peer_id\":\"dev_b\",\"peer_name\":\"PC\",\"direction\":\"out\",\"name\":\"a.zip\",\"size\":100,\"state\":\"hashing\",\"offset\":0,\"rate_bps\":0}," +
                   "{\"transfer_id\":\"tr_1\",\"peer_id\":\"dev_a\",\"peer_name\":\"Pixel\",\"direction\":\"in\",\"name\":\"b.txt\",\"size\":10,\"state\":\"done\",\"offset\":10,\"path\":\"C:\\\\x\\\\b.txt\"}]}";
        Assert.Equal(2, t.ReplaceAll(List(json)));
        Assert.Equal(new[] { "tr_2", "tr_1" }, t.Rows.Select(r => r.TransferId));
        var first = t.Rows[0];
        Assert.False(first.Incoming);
        Assert.Equal(Strings.TransferHashing, first.StateText);
        Assert.True(first.CanCancel);
        Assert.True(t.Rows[1].CanOpen);

        // A second load updates the same objects (the UI keeps its bindings) and drops vanished ones.
        Assert.Equal(1, t.ReplaceAll(List("{\"transfers\":[{\"transfer_id\":\"tr_2\",\"direction\":\"out\",\"name\":\"a.zip\",\"size\":100,\"state\":\"transferring\",\"offset\":50,\"rate_bps\":10}]}")));
        Assert.Same(first, t.Rows[0]);
        Assert.Equal("transferring", first.State);
        Assert.Null(t.Find("tr_1"));
    }

    [Fact]
    public void Row_RaisesPropertyChanged()
    {
        var t = new TransferTracker();
        t.Apply(Ev(Offered));
        var raised = 0;
        t.Rows[0].PropertyChanged += (_, _) => raised++;
        t.Apply(Ev("{\"type\":\"file.state\",\"transfer_id\":\"tr_1\",\"state\":\"accepted\"}"));
        Assert.True(raised > 0);
    }

    [Fact]
    public void OutgoingOffered_CanBeCancelledNotAccepted()
    {
        var t = new TransferTracker();
        t.ReplaceAll(List("{\"transfers\":[{\"transfer_id\":\"tr_9\",\"direction\":\"out\",\"name\":\"x\",\"size\":1,\"state\":\"offered\"}]}"));
        var r = t.Rows[0];
        Assert.False(r.CanAccept);
        Assert.True(r.CanCancel);
        Assert.Equal(Strings.TransferOffered, r.StateText);
        Assert.True(t.HasActive);
    }
}
