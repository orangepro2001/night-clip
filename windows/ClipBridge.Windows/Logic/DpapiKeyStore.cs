using System.Security.Cryptography;
using ClipBridge.Windows.Interop;

namespace ClipBridge.Windows.Logic;

/// <summary>Wraps and unwraps the 32-byte database key. The real implementation is Windows DPAPI (current user).</summary>
public interface IKeyProtector
{
    byte[] Protect(byte[] plain);
    byte[] Unprotect(byte[] protectedBlob);
}

public enum KeySource
{
    /// <summary>devkey.dpapi existed and decrypted.</summary>
    Dpapi,
    /// <summary>First run: a fresh key was generated and stored protected.</summary>
    Created,
    /// <summary>The old devkey.b64 was converted to devkey.dpapi and the plaintext deleted.</summary>
    Migrated,
    /// <summary>Migration failed; the old plaintext file was kept and is still in use.</summary>
    PlaintextFallback,
}

public sealed record KeyLoadResult(string KeyBase64, KeySource Source, string? Warning);

/// <summary>
/// The database key at rest (docs/shell-guide.md §3 item 2):
/// <c>devkey.dpapi</c> holds the key protected by <see cref="IKeyProtector"/>;
/// an old plaintext <c>devkey.b64</c> is migrated in the order read, protect, write, verify, delete;
/// and an unreadable or damaged key file is an error, never a reason to make a new key
/// (that would orphan the encrypted database).
/// </summary>
public sealed class DpapiKeyStore
{
    public const string FileName = "devkey.dpapi";
    private const string TempSuffix = ".tmp";

    private readonly IKeyProtector _protector;
    private readonly Func<byte[]> _random;

    public DpapiKeyStore(IKeyProtector protector, Func<byte[]>? random = null)
    {
        _protector = protector;
        _random = random ?? (() => RandomNumberGenerator.GetBytes(DevKey.KeyBytes));
    }

    public KeyLoadResult LoadOrCreate(string dataDir)
    {
        Directory.CreateDirectory(dataDir);
        var dpapiPath = Path.Combine(dataDir, FileName);
        var plainPath = Path.Combine(dataDir, DevKey.FileName);

        if (File.Exists(dpapiPath))
        {
            var key = ReadProtected(dpapiPath);
            try
            {
                // An earlier migration may have been interrupted after the new file was written but
                // before the plaintext was removed. Only delete it when it is provably the same key.
                if (File.Exists(plainPath) && TryReadPlain(plainPath) is { } old)
                {
                    if (CryptographicOperations.FixedTimeEquals(old, key)) File.Delete(plainPath);
                    CryptographicOperations.ZeroMemory(old);
                }
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { }
            return Done(key, KeySource.Dpapi, null);
        }

        if (File.Exists(plainPath))
        {
            var plain = ReadPlain(plainPath); // throws DevKeyException when damaged
            try
            {
                WriteProtected(dpapiPath, plain);
                try { File.Delete(plainPath); }
                catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { /* cleaned up on the next start */ }
                return Done(plain, KeySource.Migrated, null);
            }
            catch (Exception ex) when (ex is not DevKeyException)
            {
                // Could not protect/write/verify: keep the old file and keep working with it.
                SafeDelete(dpapiPath + TempSuffix);
                return Done(plain, KeySource.PlaintextFallback, Strings.KeyMigrationWarning + " (" + ex.GetType().Name + ")");
            }
        }

        var fresh = _random();
        try
        {
            if (fresh.Length != DevKey.KeyBytes) throw new DevKeyException("internal: wrong key size");
            try
            {
                WriteProtected(dpapiPath, fresh);
            }
            catch (IOException) when (File.Exists(dpapiPath))
            {
                // Another process created it first; use its key.
                return Done(ReadProtected(dpapiPath), KeySource.Dpapi, null);
            }
            return Done(fresh, KeySource.Created, null);
        }
        catch (Exception ex) when (ex is not DevKeyException)
        {
            SafeDelete(dpapiPath + TempSuffix);
            throw new DevKeyException($"Cannot protect the new database key with Windows DPAPI: {ex.Message}");
        }
    }

    // ---- file helpers ----------------------------------------------------------------

    private static KeyLoadResult Done(byte[] key, KeySource source, string? warning)
    {
        var b64 = Convert.ToBase64String(key);
        CryptographicOperations.ZeroMemory(key);
        return new KeyLoadResult(b64, source, warning);
    }

    private byte[] ReadProtected(string path)
    {
        byte[] blob;
        try { blob = File.ReadAllBytes(path); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            throw new DevKeyException($"{path} cannot be read ({ex.Message}); refusing to replace it.");
        }
        byte[] key;
        try { key = _protector.Unprotect(blob); }
        catch (Exception ex)
        {
            throw new DevKeyException($"{path} cannot be decrypted for this Windows user ({ex.GetType().Name}); " +
                "refusing to replace it because the existing database could no longer be opened.");
        }
        if (key.Length != DevKey.KeyBytes)
        {
            CryptographicOperations.ZeroMemory(key);
            throw new DevKeyException($"{path} does not contain a {DevKey.KeyBytes}-byte key; refusing to replace it.");
        }
        return key;
    }

    /// <summary>Protects <paramref name="key"/>, writes it beside the target, verifies it decrypts back, then moves it into place.</summary>
    private void WriteProtected(string path, byte[] key)
    {
        var tmp = path + TempSuffix;
        var blob = _protector.Protect(key);
        File.WriteAllBytes(tmp, blob);

        var back = _protector.Unprotect(File.ReadAllBytes(tmp));
        var same = CryptographicOperations.FixedTimeEquals(back, key);
        CryptographicOperations.ZeroMemory(back);
        if (!same) throw new CryptographicException("DPAPI round trip returned a different key");

        File.Move(tmp, path, overwrite: false);
    }

    private static byte[] ReadPlain(string path)
    {
        var text = File.ReadAllText(path).Trim();
        byte[] key;
        try { key = Convert.FromBase64String(text); }
        catch (FormatException) { throw Corrupt(path); }
        if (key.Length != DevKey.KeyBytes) throw Corrupt(path);
        return key;
    }

    private static byte[]? TryReadPlain(string path)
    {
        try { return ReadPlain(path); }
        catch (DevKeyException) { return null; }
    }

    private static void SafeDelete(string path)
    {
        try { File.Delete(path); } catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { }
    }

    private static DevKeyException Corrupt(string path) =>
        new($"{path} is not a valid {DevKey.KeyBytes}-byte base64 key; refusing to replace it " +
            "because the existing database could no longer be opened.");
}
