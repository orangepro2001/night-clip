using System.IO.Compression;
using ClipBridge.Windows.Update;
using Xunit;

namespace ClipBridge.Windows.Tests;

public sealed class UpdateApplierTests : IDisposable
{
    private readonly string _root = Path.Combine(Path.GetTempPath(), "clipbridge-apply-" + Guid.NewGuid().ToString("N"));

    public UpdateApplierTests() => Directory.CreateDirectory(_root);
    public void Dispose() { try { Directory.Delete(_root, recursive: true); } catch (IOException) { } }

    private string MakeZip(params (string Name, string Content)[] entries)
    {
        var path = Path.Combine(_root, Guid.NewGuid().ToString("N") + ".zip");
        using var fs = File.Create(path);
        using var zip = new ZipArchive(fs, ZipArchiveMode.Create);
        foreach (var (name, content) in entries)
        {
            var e = zip.CreateEntry(name);
            using var w = new StreamWriter(e.Open());
            w.Write(content);
        }
        return path;
    }

    private string Dir(string name)
    {
        var d = Path.Combine(_root, name);
        Directory.CreateDirectory(d);
        return d;
    }

    [Fact]
    public void Stage_ExtractsPackageIncludingSubfolders()
    {
        var zip = MakeZip(("ClipBridge.exe", "new-exe"), ("clipbridge_core.dll", "new-dll"), ("runtimes/win-x64/x.dll", "x"));
        var staged = UpdateApplier.Stage(zip, Dir("staging"));
        Assert.Equal("new-exe", File.ReadAllText(Path.Combine(staged, "ClipBridge.exe")));
        Assert.Equal("x", File.ReadAllText(Path.Combine(staged, "runtimes", "win-x64", "x.dll")));
        Assert.StartsWith(Path.Combine(_root, "staging"), staged);
    }

    [Theory]
    [InlineData("../evil.txt")]
    [InlineData("sub/../../evil.txt")]
    [InlineData("/abs/evil.txt")]
    public void Stage_RefusesEntriesThatEscapeTheFolder(string entry)
    {
        var zip = MakeZip(("ClipBridge.exe", "x"), (entry, "evil"));
        var staging = Dir("staging");
        Assert.Throws<InvalidDataException>(() => UpdateApplier.Stage(zip, staging));
        Assert.False(File.Exists(Path.Combine(_root, "evil.txt")));
        Assert.Empty(Directory.GetDirectories(staging)); // a failed stage leaves nothing behind
    }

    [Fact]
    public void Stage_RefusesPackageWithoutTheExe()
    {
        var staging = Dir("staging");
        Assert.Throws<InvalidDataException>(() => UpdateApplier.Stage(MakeZip(("readme.txt", "x")), staging));
        Assert.Empty(Directory.GetDirectories(staging));
    }

    [Fact]
    public void Stage_RefusesCorruptZip()
    {
        var path = Path.Combine(_root, "bad.zip");
        File.WriteAllText(path, "this is not a zip");
        Assert.ThrowsAny<Exception>(() => UpdateApplier.Stage(path, Dir("staging")));
    }

    [Fact]
    public void Apply_ReplacesPackageFilesAndKeepsEverythingElse()
    {
        var target = Dir("install");
        File.WriteAllText(Path.Combine(target, "ClipBridge.exe"), "old-exe");
        File.WriteAllText(Path.Combine(target, "clipbridge_core.dll"), "old-dll");
        File.WriteAllText(Path.Combine(target, "user-notes.txt"), "keep me");
        var staged = UpdateApplier.Stage(
            MakeZip(("ClipBridge.exe", "new-exe"), ("clipbridge_core.dll", "new-dll"), ("added/new.txt", "n")), Dir("staging"));

        UpdateApplier.Apply(staged, target, waitForPid: int.MaxValue, TimeSpan.FromSeconds(1));

        Assert.Equal("new-exe", File.ReadAllText(Path.Combine(target, "ClipBridge.exe")));
        Assert.Equal("new-dll", File.ReadAllText(Path.Combine(target, "clipbridge_core.dll")));
        Assert.Equal("n", File.ReadAllText(Path.Combine(target, "added", "new.txt")));
        Assert.Equal("keep me", File.ReadAllText(Path.Combine(target, "user-notes.txt")));
        Assert.Empty(Directory.GetFiles(target, "*.old", SearchOption.AllDirectories));
    }

    [Fact]
    public void Apply_IntoAnEmptyTargetInstallsEverything()
    {
        var staged = UpdateApplier.Stage(MakeZip(("ClipBridge.exe", "e"), ("a.dll", "a")), Dir("staging"));
        var target = Path.Combine(_root, "fresh", "install");
        UpdateApplier.Apply(staged, target, int.MaxValue, TimeSpan.FromSeconds(1));
        Assert.Equal("e", File.ReadAllText(Path.Combine(target, "ClipBridge.exe")));
    }

    [Fact]
    public void Apply_RollsBackWhenACopyFails()
    {
        var target = Dir("install");
        File.WriteAllText(Path.Combine(target, "ClipBridge.exe"), "old-exe");
        File.WriteAllText(Path.Combine(target, "a.dll"), "old-a");
        // The package wants a folder where the install has a plain file named "z": that copy fails.
        File.WriteAllText(Path.Combine(target, "z"), "i am a file");
        var staged = UpdateApplier.Stage(
            MakeZip(("ClipBridge.exe", "new-exe"), ("a.dll", "new-a"), ("z/inner.txt", "boom")), Dir("staging"));

        Assert.ThrowsAny<IOException>(() => UpdateApplier.Apply(staged, target, int.MaxValue, TimeSpan.FromSeconds(1)));

        Assert.Equal("old-exe", File.ReadAllText(Path.Combine(target, "ClipBridge.exe")));
        Assert.Equal("old-a", File.ReadAllText(Path.Combine(target, "a.dll")));
        Assert.Equal("i am a file", File.ReadAllText(Path.Combine(target, "z")));
        Assert.Empty(Directory.GetFiles(target, "*.old", SearchOption.AllDirectories));
        Assert.False(File.Exists(Path.Combine(target, "z", "inner.txt")));
    }

    [Fact]
    public void Apply_RefusesSameFolderAndMissingExe()
    {
        var staged = UpdateApplier.Stage(MakeZip(("ClipBridge.exe", "e")), Dir("staging"));
        Assert.Throws<InvalidOperationException>(() => UpdateApplier.Apply(staged, staged, int.MaxValue, TimeSpan.FromSeconds(1)));
        var empty = Dir("empty");
        Assert.Throws<InvalidDataException>(() => UpdateApplier.Apply(empty, Dir("install"), int.MaxValue, TimeSpan.FromSeconds(1)));
    }

    [Fact]
    public void Apply_TimesOutWhileTheOldProcessIsStillRunning()
    {
        var staged = UpdateApplier.Stage(MakeZip(("ClipBridge.exe", "e")), Dir("staging"));
        var target = Dir("install");
        File.WriteAllText(Path.Combine(target, "ClipBridge.exe"), "old");
        // Our own process certainly does not exit during the test.
        Assert.Throws<TimeoutException>(() =>
            UpdateApplier.Apply(staged, target, Environment.ProcessId, TimeSpan.FromMilliseconds(200)));
        Assert.Equal("old", File.ReadAllText(Path.Combine(target, "ClipBridge.exe")));
    }

    [Fact]
    public void Cleanup_RemovesStagingAndBackups()
    {
        var staging = Dir("staging");
        File.WriteAllText(Path.Combine(staging, "leftover"), "x");
        var target = Dir("install");
        Directory.CreateDirectory(Path.Combine(target, "sub"));
        File.WriteAllText(Path.Combine(target, "a.dll.old"), "x");
        File.WriteAllText(Path.Combine(target, "sub", "b.dll.old"), "x");
        File.WriteAllText(Path.Combine(target, "a.dll"), "keep");

        UpdateApplier.Cleanup(staging, target);

        Assert.False(Directory.Exists(staging));
        Assert.Empty(Directory.GetFiles(target, "*.old", SearchOption.AllDirectories));
        Assert.Equal("keep", File.ReadAllText(Path.Combine(target, "a.dll")));
    }

    [Fact]
    public void ParseApplyArgs()
    {
        Assert.True(UpdateApplier.TryParseApplyArgs(["--apply-update", "--pid", "123", "--target", "/opt/app dir"], out var a));
        Assert.Equal(new ApplyArgs(123, "/opt/app dir"), a);

        Assert.False(UpdateApplier.TryParseApplyArgs([], out _));
        Assert.False(UpdateApplier.TryParseApplyArgs(["--pid", "1", "--target", "x"], out _));          // flag missing
        Assert.False(UpdateApplier.TryParseApplyArgs(["--apply-update", "--target", "x"], out _));      // pid missing
        Assert.False(UpdateApplier.TryParseApplyArgs(["--apply-update", "--pid", "abc", "--target", "x"], out _));
        Assert.False(UpdateApplier.TryParseApplyArgs(["--apply-update", "--pid", "-5", "--target", "x"], out _));
        Assert.False(UpdateApplier.TryParseApplyArgs(["--apply-update", "--pid", "5"], out _));         // target missing
    }
}
