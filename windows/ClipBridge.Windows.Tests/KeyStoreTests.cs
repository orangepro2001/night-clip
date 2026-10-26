using System.Security.Cryptography;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

/// <summary>A reversible, recognisable "protector" (not secure; it only needs to be distinguishable from plaintext).</summary>
internal sealed class FakeProtector : IKeyProtector
{
    public static readonly byte[] Magic = { 0xD0, 0xA9, 0x1A };
    public bool FailProtect, FailUnprotect, CorruptRoundTrip;
    public int ProtectCalls, UnprotectCalls;

    public byte[] Protect(byte[] plain)
    {
        ProtectCalls++;
        if (FailProtect) throw new CryptographicException("protect failed");
        var blob = Magic.Concat(plain.Select(b => (byte)(b ^ 0x5A))).ToArray();
        if (CorruptRoundTrip) blob[^1] ^= 0xFF;
        return blob;
    }

    public byte[] Unprotect(byte[] blob)
    {
        UnprotectCalls++;
        if (FailUnprotect) throw new CryptographicException("unprotect failed");
        if (blob.Length < Magic.Length || !blob.Take(Magic.Length).SequenceEqual(Magic))
            throw new CryptographicException("not a protected blob");
        return blob.Skip(Magic.Length).Select(b => (byte)(b ^ 0x5A)).ToArray();
    }
}

public sealed class DpapiKeyStoreTests : IDisposable
{
    private readonly string _dir = Path.Combine(Path.GetTempPath(), "clipbridge-keystore-" + Guid.NewGuid().ToString("N"));
    private readonly FakeProtector _p = new();
    private string Dpapi => Path.Combine(_dir, DpapiKeyStore.FileName);
    private string Plain => Path.Combine(_dir, DevKey.FileName);

    private DpapiKeyStore Store(Func<byte[]>? rng = null) => new(_p, rng);

    public void Dispose()
    {
        try { Directory.Delete(_dir, true); } catch (IOException) { }
    }

    [Fact]
    public void FirstRun_CreatesAProtectedKey_AndNoPlaintext()
    {
        var r = Store().LoadOrCreate(_dir);
        Assert.Equal(KeySource.Created, r.Source);
        var key = Convert.FromBase64String(r.KeyBase64);
        Assert.Equal(32, key.Length);
        Assert.True(File.Exists(Dpapi));
        Assert.False(File.Exists(Plain));
        Assert.False(File.Exists(Dpapi + ".tmp"));
        // The key is not readable from the file as is.
        Assert.DoesNotContain(r.KeyBase64, File.ReadAllText(Dpapi, System.Text.Encoding.Latin1));
        Assert.False(File.ReadAllBytes(Dpapi).AsSpan().IndexOf(key) >= 0);
    }

    [Fact]
    public void SecondRun_ReturnsTheSameKey()
    {
        var a = Store().LoadOrCreate(_dir);
        var b = Store().LoadOrCreate(_dir);
        Assert.Equal(a.KeyBase64, b.KeyBase64);
        Assert.Equal(KeySource.Dpapi, b.Source);
    }

    [Fact]
    public void Migration_ConvertsPlaintext_VerifiesAndDeletesIt()
    {
        var legacy = DevKey.LoadOrCreate(_dir);            // the old plaintext provider writes devkey.b64
        Assert.True(File.Exists(Plain));
        var r = Store().LoadOrCreate(_dir);
        Assert.Equal(KeySource.Migrated, r.Source);
        Assert.Equal(legacy, r.KeyBase64);                  // same key: the database stays readable
        Assert.True(File.Exists(Dpapi));
        Assert.False(File.Exists(Plain));
        Assert.True(_p.UnprotectCalls >= 1);                // it was verified by decrypting
        Assert.Equal(legacy, Store().LoadOrCreate(_dir).KeyBase64);
    }

    [Fact]
    public void Migration_ProtectFailure_KeepsPlaintextAndKeepsWorking()
    {
        var legacy = DevKey.LoadOrCreate(_dir);
        _p.FailProtect = true;
        var r = Store().LoadOrCreate(_dir);
        Assert.Equal(KeySource.PlaintextFallback, r.Source);
        Assert.Equal(legacy, r.KeyBase64);
        Assert.NotNull(r.Warning);
        Assert.True(File.Exists(Plain));
        Assert.False(File.Exists(Dpapi));
        Assert.False(File.Exists(Dpapi + ".tmp"));
    }

    [Fact]
    public void Migration_VerificationMismatch_KeepsPlaintext_AndLeavesNoDpapiFile()
    {
        var legacy = DevKey.LoadOrCreate(_dir);
        _p.CorruptRoundTrip = true;
        var r = Store().LoadOrCreate(_dir);
        Assert.Equal(KeySource.PlaintextFallback, r.Source);
        Assert.Equal(legacy, r.KeyBase64);
        Assert.True(File.Exists(Plain));
        Assert.False(File.Exists(Dpapi));
    }

    [Fact]
    public void Migration_OfACorruptPlaintext_IsAnError()
    {
        Directory.CreateDirectory(_dir);
        File.WriteAllText(Plain, "not base64!!");
        Assert.Throws<DevKeyException>(() => Store().LoadOrCreate(_dir));
        Assert.True(File.Exists(Plain));
        Assert.False(File.Exists(Dpapi));
        File.WriteAllText(Plain, Convert.ToBase64String(new byte[16]));
        Assert.Throws<DevKeyException>(() => Store().LoadOrCreate(_dir));
    }

    [Fact]
    public void CorruptProtectedFile_IsNeverRegenerated()
    {
        Store().LoadOrCreate(_dir);
        var before = File.ReadAllBytes(Dpapi);
        File.WriteAllBytes(Dpapi, new byte[] { 1, 2, 3, 4 });
        Assert.Throws<DevKeyException>(() => Store().LoadOrCreate(_dir));
        Assert.Equal(new byte[] { 1, 2, 3, 4 }, File.ReadAllBytes(Dpapi));   // untouched
        Assert.NotEqual(before, File.ReadAllBytes(Dpapi));
    }

    [Fact]
    public void UndecryptableFile_IsAnError_EvenIfAPlaintextCopyExists()
    {
        DevKey.LoadOrCreate(_dir);
        Store().LoadOrCreate(_dir);                       // migrates; plaintext gone
        File.WriteAllText(Plain, Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)));
        _p.FailUnprotect = true;                          // e.g. a different Windows user or a reset profile
        Assert.Throws<DevKeyException>(() => Store().LoadOrCreate(_dir));
        Assert.True(File.Exists(Plain));                  // nothing was deleted
    }

    [Fact]
    public void WrongSizedKeyInsideTheBlob_IsAnError()
    {
        Directory.CreateDirectory(_dir);
        File.WriteAllBytes(Dpapi, _p.Protect(new byte[16]));
        Assert.Throws<DevKeyException>(() => Store().LoadOrCreate(_dir));
    }

    [Fact]
    public void LeftoverPlaintext_OfTheSameKey_IsCleanedUp()
    {
        var legacy = DevKey.LoadOrCreate(_dir);
        Store().LoadOrCreate(_dir);
        File.WriteAllText(Plain, legacy);                 // as if the delete had failed last time
        var r = Store().LoadOrCreate(_dir);
        Assert.Equal(KeySource.Dpapi, r.Source);
        Assert.False(File.Exists(Plain));
    }

    [Fact]
    public void LeftoverPlaintext_OfADifferentKey_IsKeptAndNotUsed()
    {
        var first = Store().LoadOrCreate(_dir);
        File.WriteAllText(Plain, Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)));
        var r = Store().LoadOrCreate(_dir);
        Assert.Equal(first.KeyBase64, r.KeyBase64);       // the protected file wins
        Assert.True(File.Exists(Plain));
    }

    [Fact]
    public void ProtectFailureOnFirstRun_IsAnError_NotASilentPlaintextKey()
    {
        _p.FailProtect = true;
        Assert.Throws<DevKeyException>(() => Store().LoadOrCreate(_dir));
        Assert.False(File.Exists(Plain));
        Assert.False(File.Exists(Dpapi));
        Assert.False(File.Exists(Dpapi + ".tmp"));
    }

    [Fact]
    public void InjectedRandom_IsUsed()
    {
        var fixedKey = Enumerable.Range(1, 32).Select(i => (byte)i).ToArray();
        var r = Store(() => (byte[])fixedKey.Clone()).LoadOrCreate(_dir);
        Assert.Equal(Convert.ToBase64String(fixedKey), r.KeyBase64);
    }
}
