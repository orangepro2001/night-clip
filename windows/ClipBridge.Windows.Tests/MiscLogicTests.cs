using System.Text.Json;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class TrayModelTests
{
    [Theory]
    [InlineData(false, TrayState.Warning)]
    [InlineData(true, TrayState.Normal)]
    public void TrayState_Compute(bool running, TrayState expected) =>
        Assert.Equal(expected, TrayModel.Compute(running));

    [Fact]
    public void Tooltip_FitsNotifyIconLimit_AndSaysWhetherTheVaultIsOpen()
    {
        foreach (var st in Enum.GetValues<TrayState>())
            foreach (var unlocked in new[] { false, true })
                Assert.True(TrayModel.Tooltip(st, unlocked).Length <= 63);
        Assert.Equal(Strings.TrayTipUnlocked, TrayModel.Tooltip(TrayState.Normal, true));
        Assert.Equal(Strings.TrayTipLocked, TrayModel.Tooltip(TrayState.Normal, false));
        Assert.Equal(Strings.TrayTipWarning, TrayModel.Tooltip(TrayState.Warning, true));
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
        Assert.True(p.Notifications);
    }

    [Fact]
    public void Notifications_RoundTrip()
    {
        new ShellPrefs { Notifications = false }.Save(_dir);
        Assert.False(ShellPrefs.Load(_dir).Notifications);
        new ShellPrefs { Notifications = true }.Save(_dir);
        Assert.True(ShellPrefs.Load(_dir).Notifications);
    }

    [Fact]
    public void SaveAndLoad_RoundTrip()
    {
        new ShellPrefs { FirstRunDone = true, Theme = "dark" }.Save(_dir);
        var p = ShellPrefs.Load(_dir);
        Assert.True(p.FirstRunDone);
        Assert.Equal("dark", p.Theme);
        Assert.False(File.Exists(Path.Combine(_dir, ShellPrefs.FileName + ".tmp")));
    }

    [Fact]
    public void CorruptFile_MeansDefaults_NotACrash()
    {
        Directory.CreateDirectory(_dir);
        File.WriteAllText(Path.Combine(_dir, ShellPrefs.FileName), "{{{{ not json");
        Assert.False(ShellPrefs.Load(_dir).FirstRunDone);
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
          ""core"":""0.1.0"",""platform"":""windows"",""events_dropped"":2,
          ""db_key"":""LEAKED-KEY"",""master_password"":""LEAKED-PW"",
          ""ssh"":{""enabled"":true,""state"":""listening"",""pipe"":""\\\\.\\pipe\\openssh-ssh-agent"",""keys_loaded"":2,""keys_state"":""loaded"",""private_key"":""LEAKED-PRIV""},
          ""update"":{""state"":""up_to_date"",""enabled"":true,""current_name"":""0.1.5"",""current_code"":5}}");
        var text = DiagnosticsText.Build(d.RootElement, "0.1.5", DateTimeOffset.Parse("2026-10-26T10:00:00+00:00"));
        Assert.DoesNotContain("LEAKED", text);
        Assert.Contains("0.1.0", text);
        Assert.Contains("windows", text);
        Assert.Contains("listening", text);
        Assert.Contains("已加载 2 把", text);
        Assert.Contains("up_to_date", text);
        Assert.StartsWith(Strings.AppName, text);
    }

    [Fact]
    public void EmptyResult_StillProducesText()
    {
        using var d = JsonDocument.Parse("{}");
        var text = DiagnosticsText.Build(d.RootElement, "dev", DateTimeOffset.UnixEpoch);
        Assert.Contains("应用版本: dev", text);
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
        api.SettingsSet("vault_autolock_s", "300");
        api.SettingsGet();
        api.Status();
        api.Diagnostics();
        api.UpdateConfigAuto(true);
        api.UpdateCheck();

        Assert.Equal(new[]
        {
            ("settings.set", "{\"key\":\"vault_autolock_s\",\"value\":\"300\"}"),
            ("settings.get", "{}"),
            ("status", "{}"),
            ("diagnostics.get", "{}"),
            ("update.config", "{\"auto\":true}"),
            ("update.check", "{}"),
        }, f.Calls.Select(c => (c.Method, c.Params)).ToArray());
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
