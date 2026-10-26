using System.Diagnostics;
using System.Text.Json;
using ClipBridge.Windows.Interop;
using Xunit;

namespace ClipBridge.Windows.Tests;

/// <summary>
/// The Go core is a process-wide singleton that cannot be started again after it has shut down, so every test that
/// needs the real core shares this one instance (an xUnit collection fixture). Started once with a UTF-8 device name;
/// shut down when the whole collection is done, where the "Dispose is prompt" check lives.
/// </summary>
public sealed class NativeCoreFixture : IDisposable
{
    // Non-ASCII on purpose: proves UTF-8 survives P/Invoke in both directions.
    public const string DeviceName = "测试-PC-é";

    public string DataDir { get; }
    public string Key { get; }
    public CoreHost Core { get; } = new();
    public List<JsonElement> RawEvents { get; } = new();
    public ManualResetEventSlim GotStarted { get; } = new();
    public CoreException? RejectedConfig { get; }

    public NativeCoreFixture()
    {
        DataDir = Path.Combine(Path.GetTempPath(), "clipbridge-interop-" + Guid.NewGuid().ToString("N"));
        Key = DevKey.LoadOrCreate(DataDir);

        // A rejected config surfaces as INIT_FAILED and leaves the process-wide core usable.
        using (var rejected = new CoreHost())
        {
            RejectedConfig = Assert.Throws<CoreException>(() => rejected.Start(new
            {
                data_dir = "relative/dir", db_key_b64 = Key, device_name = "x", platform = "windows",
            }));
        }

        Core.EventReceived += ev =>
        {
            lock (RawEvents) RawEvents.Add(ev);
            if (ev.GetProperty("type").GetString() == "core.started") GotStarted.Set();
        };
        Core.Start(new
        {
            data_dir = DataDir, db_key_b64 = Key,
            device_name = DeviceName, platform = "windows", listen = false, port = 47821,
        });
    }

    public void Dispose()
    {
        // Dispose must stop the pump promptly (cb_shutdown wakes cb_next_event).
        var sw = Stopwatch.StartNew();
        Core.Dispose();
        var elapsed = sw.Elapsed;
        try { Directory.Delete(DataDir, recursive: true); } catch (IOException) { }
        Assert.True(elapsed < TimeSpan.FromSeconds(2), $"Dispose took {elapsed}");
    }
}

[CollectionDefinition(Name)]
public class CoreCollection : ICollectionFixture<NativeCoreFixture>
{
    public const string Name = "native-core";
}

[Collection(CoreCollection.Name)]
public class CoreInteropTests
{
    private readonly NativeCoreFixture _fx;

    public CoreInteropTests(NativeCoreFixture fx) => _fx = fx;

    [Fact]
    public void RejectedConfig_SurfacesAsInitFailed()
    {
        Assert.Equal("INIT_FAILED", _fx.RejectedConfig!.Code);
        Assert.Contains("data_dir", _fx.RejectedConfig.Message);
    }

    [Fact]
    public void Start_CreatesTheDatabase_AndStatusRoundTripsUtf8()
    {
        Assert.True(File.Exists(Path.Combine(_fx.DataDir, "clipbridge.db")), "the core did not create its database");

        // status round-trips the UTF-8 device name and returns a protocol-valid id.
        var st = _fx.Core.Call("status");
        Assert.Equal(NativeCoreFixture.DeviceName, st.GetProperty("device_name").GetString());
        var deviceId = st.GetProperty("device_id").GetString()!;
        Assert.Matches("^dev_[a-z2-7]{26}$", deviceId);

        // The pump thread delivers core.started, and its device_id matches status.
        Assert.True(_fx.GotStarted.Wait(TimeSpan.FromSeconds(5)), "core.started was not delivered by the event pump");
        lock (_fx.RawEvents)
            Assert.Equal(deviceId, _fx.RawEvents.First(e => e.GetProperty("type").GetString() == "core.started")
                .GetProperty("device_id").GetString());
    }

    [Fact]
    public void Errors_SurfaceAsCoreException_WithTheStableCode()
    {
        var ex = Assert.Throws<CoreException>(() => _fx.Core.Call("does.not.exist"));
        Assert.Equal("METHOD_NOT_FOUND", ex.Code);
        Assert.Equal(0, ex.RetryAfterSeconds);
    }

    [Fact]
    public void ASecondStart_OnTheSameHost_IsRejectedClientSide()
    {
        Assert.Throws<InvalidOperationException>(() =>
            _fx.Core.Start(new { data_dir = _fx.DataDir, db_key_b64 = _fx.Key, device_name = "x", platform = "windows" }));
    }

    [Fact]
    public void DevKey_IsStable_And_RefusesToReplaceACorruptFile()
    {
        var dir = Path.Combine(Path.GetTempPath(), "clipbridge-devkey-" + Guid.NewGuid().ToString("N"));
        try
        {
            var first = DevKey.LoadOrCreate(dir);
            Assert.Equal(DevKey.KeyBytes, Convert.FromBase64String(first).Length);
            Assert.Equal(first, DevKey.LoadOrCreate(dir));

            var other = Path.Combine(dir, "other");
            Assert.NotEqual(first, DevKey.LoadOrCreate(other));

            File.WriteAllText(Path.Combine(dir, DevKey.FileName), "not base64!!");
            Assert.Throws<DevKeyException>(() => DevKey.LoadOrCreate(dir));
            File.WriteAllText(Path.Combine(dir, DevKey.FileName), Convert.ToBase64String(new byte[16]));
            Assert.Throws<DevKeyException>(() => DevKey.LoadOrCreate(dir));
        }
        finally
        {
            try { Directory.Delete(dir, recursive: true); } catch (IOException) { }
        }
    }
}
