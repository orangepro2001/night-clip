using System.IO;
using System.Security.Cryptography;

namespace ClipBridge.Windows.Interop;

/// <summary>
/// The legacy plaintext database-key provider: a random 32-byte key kept as base64 in
/// <c>devkey.b64</c>. The app no longer uses it for new installs: <c>DpapiKeyStore</c> keeps the key
/// protected by Windows DPAPI in <c>devkey.dpapi</c> and migrates an existing <c>devkey.b64</c> into it.
/// This class remains for the migration (file name and size constants) and for the interop tests.
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
