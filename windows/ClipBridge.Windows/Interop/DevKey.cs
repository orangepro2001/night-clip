using System.IO;
using System.Security.Cryptography;

namespace ClipBridge.Windows.Interop;

/// <summary>
/// Temporary database-key provider: a random 32-byte key kept as base64 in
/// <c>devkey.b64</c> inside the per-user data directory. The key is NOT
/// encrypted at rest and is protected only by the OS file ACL of the user
/// profile. TODO(WP9: DPAPI): wrap the key with DPAPI instead.
/// </summary>
public static class DevKey
{
    public const string FileName = "devkey.b64";
    public const int KeyBytes = 32;

    /// <summary>
    /// Returns the base64 key for <paramref name="dataDir"/>, creating it on first run.
    /// An unreadable or wrong-sized key file throws instead of being regenerated:
    /// a new key would make the existing encrypted database permanently unreadable.
    /// </summary>
    public static string LoadOrCreate(string dataDir)
    {
        Directory.CreateDirectory(dataDir);
        var path = Path.Combine(dataDir, FileName);

        if (!File.Exists(path))
        {
            var fresh = Convert.ToBase64String(RandomNumberGenerator.GetBytes(KeyBytes));
            try
            {
                // CreateNew: if another instance won the race, fall through and read its key.
                using var fs = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None);
                using var w = new StreamWriter(fs);
                w.Write(fresh);
            }
            catch (IOException) when (File.Exists(path)) { }
        }

        var text = File.ReadAllText(path).Trim();
        byte[] key;
        try { key = Convert.FromBase64String(text); }
        catch (FormatException) { throw Corrupt(path); }
        if (key.Length != KeyBytes) throw Corrupt(path);
        return text;
    }

    private static DevKeyException Corrupt(string path) =>
        new($"{path} is not a valid {KeyBytes}-byte base64 key; refusing to replace it " +
            "because the existing database could no longer be opened.");
}

/// <summary>The key file exists but cannot be used; the caller must not regenerate it.</summary>
public sealed class DevKeyException(string message) : Exception(message);
