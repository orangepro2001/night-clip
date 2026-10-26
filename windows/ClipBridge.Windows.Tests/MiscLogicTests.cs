using System.Text.Json;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class TrayModelTests
{
    private static CoreEvent Ev(string json) => CoreEventParser.Parse(json);

    [Fact]
    public void WarningTracker_TracksAndClears()
    {
        var w = new WarningTracker();
        Assert.True(w.Apply(Ev("{\"type\":\"core.warning\",\"code\":\"NO_TAILNET\"}")));
        Assert.False(w.Apply(Ev("{\"type\":\"core.warning\",\"code\":\"NO_TAILNET\"}")));   // no change
        Assert.True(w.Any);
        Assert.Equal(Strings.NoTailnetBanner, w.Banner);
        Assert.True(w.Apply(Ev("{\"type\":\"core.listening\",\"addrs\":[\"100.64.0.1\"],\"port\":47821}")));
        Assert.False(w.Any);
        Assert.Null(w.Banner);
        Assert.False(w.Apply(Ev("{\"type\":\"peer.state\",\"device_id\":\"d\",\"state\":\"connected\"}")));
    }

    [Fact]
    public void WarningTracker_FingerprintMismatchSticks()
    {
        var w = new WarningTracker();
        w.Apply(Ev("{\"type\":\"core.warning\",\"code\":\"FINGERPRINT_MISMATCH\"}"));
        w.Apply(Ev("{\"type\":\"core.listening\",\"addrs\":[],\"port\":1}"));
        Assert.True(w.Has("FINGERPRINT_MISMATCH"));
        Assert.Equal(Strings.FingerprintMismatchBanner, w.Banner);
    }

    [Theory]
    [InlineData(false, false, new string[0], TrayState.Warning)]
    [InlineData(true, true, new[] { "connected" }, TrayState.Warning)]
    [InlineData(true, false, new string[0], TrayState.Normal)]
    [InlineData(true, false, new[] { "connected", "offline" }, TrayState.Normal)]
    [InlineData(true, false, new[] { "offline", "connecting" }, TrayState.NoConnection)]
    [InlineData(true, false, new[] { "revoked" }, TrayState.Normal)]
    [InlineData(true, false, new[] { "unpaired" }, TrayState.NoConnection)]
    public void TrayState_Compute(bool running, bool warn, string[] peers, TrayState expected) =>
        Assert.Equal(expected, TrayModel.Compute(running, warn, peers));

    [Fact]
    public void Tooltip_FitsNotifyIconLimit()
    {
        foreach (var st in Enum.GetValues<TrayState>())
            foreach (var peers in new[] { new string[0], new[] { "connected" }, new[] { "offline" } })
                Assert.True(TrayModel.Tooltip(st, peers).Length <= 63);
        Assert.Equal(Strings.TrayTipConnected(1, 2), TrayModel.Tooltip(TrayState.Normal, new[] { "connected", "offline" }));
    }
}

public class AppLaunchTests
{
    [Fact]
    public void Minimized_Flag()
    {
        Assert.True(StartupOptions.Parse(new[] { "--minimized" }).Minimized);
        Assert.True(StartupOptions.Parse(new[] { "x", "--MINIMIZED" }).Minimized);
        Assert.False(StartupOptions.Parse(Array.Empty<string>()).Minimized);
        Assert.False(StartupOptions.Parse(new[] { "--other" }).Minimized);
    }

    [Fact]
    public void AutoStartCommand_QuotesThePath_AndPassesMinimized()
    {
        var c = AutoStartCommand.Build(@"C:\Program Files\ClipBridge\ClipBridge.exe");
        Assert.Equal("\"C:\\Program Files\\ClipBridge\\ClipBridge.exe\" --minimized", c);
        Assert.True(AutoStartCommand.Matches(c, @"c:\program files\clipbridge\clipbridge.exe"));
    }

    [Theory]
    [InlineData(null, false)]
    [InlineData("", false)]
    [InlineData("\"D:\\Other\\ClipBridge.exe\" --minimized", false)]
    [InlineData("D:\\Apps\\ClipBridge.exe --minimized", true)]
    [InlineData("\"D:\\Apps\\ClipBridge.exe\"", true)]
    public void AutoStartCommand_Matches(string? stored, bool expected) =>
        Assert.Equal(expected, AutoStartCommand.Matches(stored, @"D:\Apps\ClipBridge.exe"));
}

public sealed class ShellPrefsTests : IDisposable
{
    private readonly string _dir = Path.Combine(Path.GetTempPath(), "clipbridge-prefs-" + Guid.NewGuid().ToString("N"));
    public void Dispose() { try { Directory.Delete(_dir, true); } catch (IOException) { } }

    [Fact]
    public void MissingFile_MeansDefaults()
    {
        var p = ShellPrefs.Load(_dir);
        Assert.False(p.FirstRunDone);
        Assert.Null(p.DownloadDir);
    }

    [Fact]
    public void SaveAndLoad_RoundTrip()
    {
        new ShellPrefs { FirstRunDone = true, DownloadDir = @"D:\In" }.Save(_dir);
        var p = ShellPrefs.Load(_dir);
        Assert.True(p.FirstRunDone);
        Assert.Equal(@"D:\In", p.DownloadDir);
        Assert.False(File.Exists(Path.Combine(_dir, ShellPrefs.FileName + ".tmp")));
    }

    [Fact]
    public void CorruptFile_MeansDefaults_NotACrash()
    {
        Directory.CreateDirectory(_dir);
        File.WriteAllText(Path.Combine(_dir, ShellPrefs.FileName), "{{{{ not json");
        Assert.False(ShellPrefs.Load(_dir).FirstRunDone);
    }

    [Fact]
    public void ResolveDownloadDir_Order()
    {
        Assert.Equal(@"C:\core", ShellPrefs.ResolveDownloadDir(@"C:\core", @"D:\prefs", @"E:\sys"));
        Assert.Equal(@"D:\prefs", ShellPrefs.ResolveDownloadDir("", @"D:\prefs", @"E:\sys"));
        Assert.Equal(@"D:\prefs", ShellPrefs.ResolveDownloadDir("relative", @"D:\prefs", @"E:\sys"));
        Assert.Equal(@"E:\sys", ShellPrefs.ResolveDownloadDir(null, null, @"E:\sys"));
    }
}

public sealed class LogFileTests : IDisposable
{
    private readonly string _dir = Path.Combine(Path.GetTempPath(), "clipbridge-log-" + Guid.NewGuid().ToString("N"));
    public void Dispose() { try { Directory.Delete(_dir, true); } catch (IOException) { } }

    [Fact]
    public void Writes_OneLinePerCall_AndRotates()
    {
        var log = new LogFile(Path.Combine(_dir, "sub", "app.log"), maxBytes: 200);
        log.Write("first\nsecond", DateTimeOffset.Parse("2026-10-26T10:00:00+00:00"));
        var text = File.ReadAllText(log.Path);
        Assert.Single(text.Split('\n', StringSplitOptions.RemoveEmptyEntries));
        Assert.Contains("first second", text);
        for (var i = 0; i < 20; i++) log.Write("filler line number " + i);
        Assert.True(File.Exists(log.Path + ".1"));
        Assert.True(new FileInfo(log.Path).Length < 400);
    }

    [Fact]
    public void UnwritablePath_DoesNotThrow()
    {
        var blocker = Path.Combine(_dir, "file");
        Directory.CreateDirectory(_dir);
        File.WriteAllText(blocker, "x");
        new LogFile(Path.Combine(blocker, "nested", "app.log")).Write("hi"); // parent is a file: IOException swallowed
    }
}

public class DiagnosticsTextTests
{
    [Fact]
    public void ListsWhitelistedFields_AndNothingElse()
    {
        using var d = JsonDocument.Parse(@"{
          ""core"":""0.1.0"",""protocol"":""clipbridge/v1"",""device_id"":""dev_abc"",""fingerprint_prefix"":""ABCD1234"",
          ""platform"":""windows"",""listen"":true,""listening_addrs"":[""100.64.0.1""],""port"":47821,""events_dropped"":2,
          ""psk"":""LEAKED-PSK"",""db_key"":""LEAKED-KEY"",
          ""peers"":[{""device_id"":""dev_p"",""name"":""Pixel"",""platform"":""android"",""state"":""connected"",""outbox_pending"":1,
                      ""recv_cursor"":7,""next_seq_out"":9,""apply_mode"":""manual"",""tailnet_addrs"":[""100.64.0.2""],""private_key"":""LEAKED-PRIV""}],
          ""update"":{""state"":""up_to_date"",""enabled"":true,""current_name"":""0.1.5"",""current_code"":5}}");
        var text = DiagnosticsText.Build(d.RootElement, "0.1.5", DateTimeOffset.Parse("2026-10-26T10:00:00+00:00"), new[] { "NO_TAILNET" });
        Assert.DoesNotContain("LEAKED", text);
        Assert.Contains("dev_abc", text);
        Assert.Contains("ABCD1234", text);
        Assert.Contains("100.64.0.1", text);
        Assert.Contains("47821", text);
        Assert.Contains("Pixel", text);
        Assert.Contains("接收游标: 7", text);
        Assert.Contains("发送序号: 9", text);
        Assert.Contains("NO_TAILNET", text);
        Assert.Contains("up_to_date", text);
    }

    [Fact]
    public void EmptyResult_StillProducesText()
    {
        using var d = JsonDocument.Parse("{}");
        var text = DiagnosticsText.Build(d.RootElement, "dev", DateTimeOffset.UnixEpoch);
        Assert.Contains("（无）", text);
    }
}

public class CountdownTests
{
    private static readonly DateTimeOffset T0 = DateTimeOffset.Parse("2026-10-26T10:00:00Z");

    [Fact]
    public void Remaining()
    {
        Assert.Equal("04:32", Countdown.Remaining(T0.AddSeconds(272), T0));
        Assert.Equal("00:01", Countdown.Remaining(T0.AddMilliseconds(100), T0));
        Assert.Equal("10:00", Countdown.Remaining(T0.AddMinutes(10), T0));
        Assert.Null(Countdown.Remaining(T0, T0));
        Assert.Null(Countdown.Remaining(T0.AddSeconds(-5), T0));
    }

    [Fact]
    public void ParseRfc3339()
    {
        Assert.Equal(T0, Countdown.ParseRfc3339("2026-10-26T10:00:00Z"));
        Assert.Equal(T0, Countdown.ParseRfc3339("2026-10-26T12:00:00+02:00"));
        Assert.Null(Countdown.ParseRfc3339("garbage"));
        Assert.Null(Countdown.ParseRfc3339(null));
        Assert.True(Countdown.IsExpired(T0, T0));
    }
}

public class CoreApiTests
{
    private sealed class Fake : ICoreCaller
    {
        public readonly List<(string Method, string Params)> Calls = new();
        public Func<string, string>? Reply;

        public JsonElement Call(string method, object? parameters = null)
        {
            Calls.Add((method, parameters is null ? "{}" : JsonSerializer.Serialize(parameters)));
            using var d = JsonDocument.Parse(Reply?.Invoke(method) ?? "{}");
            return d.RootElement.Clone();
        }
    }

    [Fact]
    public void Calls_UseTheDocumentedMethodNamesAndParameters()
    {
        var f = new Fake();
        var api = new CoreApi(f);
        api.SettingsSet("capture", "on");
        api.PairCancel("s1");
        api.PairConfirm("p1", true);
        api.PeerRename("dev_a", "新名字");
        api.PeerSetMode("dev_a", "auto");
        api.PeerRemove("dev_a");
        api.PeerRemove("dev_a", forget: true);
        api.MarkApplied("e1");
        api.Dismiss("e2");
        api.InboxClear();
        api.FileAccept("tr_1");
        api.FileAccept("tr_2", @"D:\In");
        api.FileReject("tr_3");
        api.FileCancel("tr_4");
        api.UpdateConfigAuto(true);

        Assert.Equal(new[]
        {
            ("settings.set", "{\"key\":\"capture\",\"value\":\"on\"}"),
            ("pair.cancel", "{\"session_id\":\"s1\"}"),
            ("pair.confirm", "{\"pair_id\":\"p1\",\"accept\":true}"),
            ("peers.update", "{\"device_id\":\"dev_a\",\"name\":\"\\u65B0\\u540D\\u5B57\"}"),
            ("peers.update", "{\"device_id\":\"dev_a\",\"apply_mode\":\"auto\"}"),
            ("peers.remove", "{\"device_id\":\"dev_a\",\"forget\":false}"),
            ("peers.remove", "{\"device_id\":\"dev_a\",\"forget\":true}"),
            ("clip.mark_applied", "{\"event_id\":\"e1\"}"),
            ("clip.dismiss", "{\"event_id\":\"e2\"}"),
            ("inbox.clear", "{}"),
            ("file.accept", "{\"transfer_id\":\"tr_1\"}"),
            ("file.accept", "{\"transfer_id\":\"tr_2\",\"save_dir\":\"D:\\\\In\"}"),
            ("file.reject", "{\"transfer_id\":\"tr_3\"}"),
            ("file.cancel", "{\"transfer_id\":\"tr_4\"}"),
            ("update.config", "{\"auto\":true}"),
        }, f.Calls.Select(c => (c.Method, c.Params)).ToArray());
    }

    [Fact]
    public void FileOffer_SendsThePath_AndReturnsTheTransferId()
    {
        var f = new Fake { Reply = _ => "{\"transfer_id\":\"tr_77\"}" };
        Assert.Equal("tr_77", new CoreApi(f).FileOffer("dev_a", @"C:\a.txt"));
        Assert.Equal(("file.offer", "{\"to\":\"dev_a\",\"path\":\"C:\\\\a.txt\"}"), f.Calls[0]);
    }

    [Fact]
    public void PairBegin_ParsesTheQrMatrix()
    {
        var f = new Fake { Reply = _ => "{\"session_id\":\"s\",\"link\":\"clipbridge://pair?x\",\"host\":\"100.64.0.1\",\"port\":47821,\"expires_at\":\"2026-10-26T10:00:00Z\",\"qr\":{\"size\":2,\"rows\":[\"01\",\"10\"]}}" };
        var b = new CoreApi(f).PairBegin();
        Assert.Equal(("s", "clipbridge://pair?x", 47821, 2), (b.SessionId, b.Link, b.Port, b.QrSize));
        Assert.Equal(new[] { "01", "10" }, b.QrRows);
        Assert.NotNull(b.Expires);
    }

    [Fact]
    public void PairBegin_WithoutQr_HasNoRows()
    {
        var f = new Fake { Reply = _ => "{\"session_id\":\"s\",\"link\":\"l\",\"host\":\"h\",\"port\":1,\"expires_at\":\"x\"}" };
        var b = new CoreApi(f).PairBegin();
        Assert.Empty(b.QrRows);
        Assert.Null(b.Expires);
    }

    [Fact]
    public void ClipLocalChanged_ParsesSentAndSuppressed()
    {
        var f = new Fake { Reply = _ => "{\"sent\":[{\"device_id\":\"d\",\"name\":\"Pixel\",\"event_id\":\"e\"}],\"suppressed\":\"\"}" };
        var r = new CoreApi(f).ClipLocalChanged("hi");
        Assert.Equal("Pixel", Assert.Single(r.Sent).Name);
        Assert.Equal("{\"text\":\"hi\"}", f.Calls[0].Params);
        var g = new Fake { Reply = _ => "{\"sent\":[],\"suppressed\":\"too_large\"}" };
        Assert.Equal("too_large", new CoreApi(g).ClipLocalChanged("x").Suppressed);
    }

    [Fact]
    public void PairJoin_Params()
    {
        var f = new Fake { Reply = _ => "{\"join_id\":\"j9\"}" };
        var api = new CoreApi(f);
        Assert.Equal("j9", api.PairJoin(new PairJoinRequest("clipbridge://pair?a", null, 0, null, null)));
        Assert.Equal("{\"link\":\"clipbridge://pair?a\"}", f.Calls[0].Params);
        api.PairJoin(new PairJoinRequest(null, "100.64.0.1", 47821, "psk", "fp"));
        Assert.Equal("{\"host\":\"100.64.0.1\",\"port\":47821,\"psk\":\"psk\",\"fp\":\"fp\"}", f.Calls[1].Params);
    }
}

public class SerialQueueTests
{
    [Fact]
    public async Task RunsJobsInOrder_OnOneThread()
    {
        using var q = new SerialQueue("test-order");
        var seen = new List<int>();
        var threads = new HashSet<int>();
        for (var i = 0; i < 50; i++)
        {
            var n = i;
            q.Post(() => { lock (seen) { seen.Add(n); threads.Add(Environment.CurrentManagedThreadId); } });
        }
        await q.Drain().WaitAsync(TimeSpan.FromSeconds(5));
        Assert.Equal(Enumerable.Range(0, 50), seen);
        Assert.Single(threads);
    }

    [Fact]
    public async Task AThrowingJob_IsReported_AndDoesNotStopTheWorker()
    {
        var errors = new List<Exception>();
        using var q = new SerialQueue("test-throw", errors.Add);
        var ran = false;
        q.Post(() => throw new InvalidOperationException("boom"));
        q.Post(() => ran = true);
        await q.Drain().WaitAsync(TimeSpan.FromSeconds(5));
        Assert.True(ran);
        Assert.Single(errors);
    }

    [Fact]
    public async Task AFailingErrorHandler_DoesNotKillTheWorker()
    {
        using var q = new SerialQueue("test-handler", _ => throw new Exception("handler bug"));
        q.Post(() => throw new Exception("job bug"));
        await q.Drain().WaitAsync(TimeSpan.FromSeconds(5));
    }

    [Fact]
    public void Dispose_RunsQueuedJobsThenStops_AndIgnoresLaterPosts()
    {
        var q = new SerialQueue("test-dispose");
        var count = 0;
        q.Post(() => { Thread.Sleep(50); Interlocked.Increment(ref count); });
        q.Post(() => Interlocked.Increment(ref count));
        q.Dispose();
        Assert.Equal(2, count);
        q.Post(() => Interlocked.Increment(ref count));
        Assert.Equal(2, count);
    }
}

public class CoalescerTests
{
    [Fact]
    public async Task RequestsDuringARun_CollapseIntoOneExtraRun()
    {
        var runs = 0;
        var gate = new TaskCompletionSource();
        Coalescer? c = null;
        c = new Coalescer(async () =>
        {
            runs++;
            if (runs == 1) await gate.Task;
        });
        var first = c.RunAsync();
        Assert.True(c.IsRunning);
        await c.RunAsync();           // merged
        await c.RunAsync();           // merged
        Assert.Equal(1, runs);
        gate.SetResult();
        await first;
        Assert.Equal(2, runs);
        Assert.False(c.IsRunning);
        await c.RunAsync();
        Assert.Equal(3, runs);
    }

    [Fact]
    public async Task AFailedRun_DoesNotWedgeTheCoalescer()
    {
        var fail = true;
        var runs = 0;
        var c = new Coalescer(() => { runs++; return fail ? throw new InvalidOperationException() : Task.CompletedTask; });
        await Assert.ThrowsAsync<InvalidOperationException>(() => c.RunAsync());
        Assert.False(c.IsRunning);
        fail = false;
        await c.RunAsync();
        Assert.Equal(2, runs);
    }
}
