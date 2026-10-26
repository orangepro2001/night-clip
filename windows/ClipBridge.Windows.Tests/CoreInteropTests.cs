using System.Diagnostics;
using System.Text.Json;
using ClipBridge.Windows.Interop;
using Xunit;

namespace ClipBridge.Windows.Tests;

/// <summary>
/// The Go core is a process-wide singleton (it lives in a native library), so
/// the whole lifecycle is one ordered test instead of several independent ones.
/// </summary>
public class CoreInteropTests
{
    // Non-ASCII on purpose: proves UTF-8 survives P/Invoke in both directions.
    private const string DeviceName = "测试-PC-é";

    [Fact]
    public void Lifecycle_StartCallEventsUnknownMethodShutdown()
    {
        var dataDir = Path.Combine(Path.GetTempPath(), "clipbridge-interop-" + Guid.NewGuid().ToString("N"));
        var key = DevKey.LoadOrCreate(dataDir);
        try
        {
            RunLifecycle(dataDir, key);
        }
        finally
        {
            // The core has shut down and closed the database by now.
            try { Directory.Delete(dataDir, recursive: true); } catch (IOException) { }
        }
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

    private static void RunLifecycle(string dataDir, string key)
    {
        // A rejected config surfaces as INIT_FAILED and leaves the process-wide core usable.
        using (var rejected = new CoreHost())
        {
            var bad = Assert.Throws<CoreException>(() => rejected.Start(new
            {
                data_dir = "relative/dir", db_key_b64 = key, device_name = "x", platform = "windows",
            }));
            Assert.Equal("INIT_FAILED", bad.Code);
            Assert.Contains("data_dir", bad.Message);
        }

        using var core = new CoreHost();
        var events = new List<JsonElement>();
        var gotStarted = new ManualResetEventSlim();
        core.EventReceived += ev =>
        {
            lock (events) events.Add(ev);
            if (ev.GetProperty("type").GetString() == "core.started") gotStarted.Set();
        };

        core.Start(new
        {
            data_dir = dataDir, db_key_b64 = key,
            device_name = DeviceName, platform = "windows", listen = false, port = 47821,
        });
        Assert.True(File.Exists(Path.Combine(dataDir, "clipbridge.db")), "the core did not create its database");

        // status round-trips the UTF-8 device name and returns a protocol-valid id.
        var st = core.Call("status");
        Assert.Equal(DeviceName, st.GetProperty("device_name").GetString());
        var deviceId = st.GetProperty("device_id").GetString()!;
        Assert.Matches("^dev_[a-z2-7]{26}$", deviceId);

        // The pump thread delivers core.started, and its device_id matches status.
        Assert.True(gotStarted.Wait(TimeSpan.FromSeconds(5)), "core.started was not delivered by the event pump");
        lock (events)
            Assert.Equal(deviceId, events.First(e => e.GetProperty("type").GetString() == "core.started")
                .GetProperty("device_id").GetString());

        // Errors surface as CoreException with the stable code.
        var ex = Assert.Throws<CoreException>(() => core.Call("does.not.exist"));
        Assert.Equal("METHOD_NOT_FOUND", ex.Code);

        // A second Start on the same CoreHost is rejected client-side.
        Assert.Throws<InvalidOperationException>(() => core.Start(new { data_dir = dataDir, db_key_b64 = key, device_name = "x", platform = "windows" }));

        // Dispose must stop the pump promptly (cb_shutdown wakes cb_next_event).
        var sw = Stopwatch.StartNew();
        core.Dispose();
        Assert.True(sw.Elapsed < TimeSpan.FromSeconds(2), $"Dispose took {sw.Elapsed}");
    }
}
