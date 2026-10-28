namespace ClipBridge.Windows.Logic;

/// <summary>
/// Received images are not saved straight away (desktop shells; Android: logic/ReceivedImages.kt): an image
/// accepted into the default folder is held in a private folder, previewed, and only written where the user
/// picks with "保存到…". Closing the preview discards it. Choosing a folder in the offer dialog still saves there.
/// </summary>
public static class ReceivedImages
{
    /// <summary>Sub-folder of the shell's data folder; one sub-folder per transfer under it.</summary>
    public const string HoldDirName = "received-images";

    /// <summary>
    /// Whether an accepted transfer goes to the hold folder: a previewable image the user did not direct to a
    /// folder of their own (no folder given, or the default download folder).
    /// </summary>
    public static bool ShouldHold(string? name, long size, string? chosenDir, string defaultDir) =>
        FileDisplay.IsPreviewableImage(name, size)
        && (string.IsNullOrWhiteSpace(chosenDir) || SameDir(chosenDir, defaultDir));

    /// <summary>Where the core writes a held transfer: &lt;holdRoot&gt;/&lt;transferId&gt;.</summary>
    public static string HoldDirFor(string holdRoot, string transferId) => Path.Combine(holdRoot, Safe(transferId));

    /// <summary>Whether a finished file lies in the hold folder (so the preview offers "保存到…" and discards on close).</summary>
    public static bool IsHeld(string? path, string holdRoot)
    {
        if (string.IsNullOrEmpty(path) || string.IsNullOrEmpty(holdRoot)) return false;
        var root = Path.TrimEndingDirectorySeparator(holdRoot);
        return path.StartsWith(root + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)
            || path.StartsWith(root + "/", StringComparison.Ordinal);
    }

    /// <summary>"name.png", or "name (2).png" … when taken, for saving without overwriting.</summary>
    public static string UniqueName(string name, Func<string, bool> exists)
    {
        var clean = FileDisplay.Sanitize(name);
        if (clean.Length == 0) clean = "image";
        if (!exists(clean)) return clean;
        var ext = FileDisplay.Extension(clean);
        var stem = ext.Length > 0 ? clean[..^ext.Length] : clean;
        for (var i = 2; i < 10_000; i++)
        {
            var candidate = $"{stem} ({i}){ext}";
            if (!exists(candidate)) return candidate;
        }
        return $"{stem} ({Guid.NewGuid():N}){ext}";
    }

    private static bool SameDir(string a, string b) =>
        string.Equals(a.Trim().TrimEnd('\\', '/'), b.Trim().TrimEnd('\\', '/'), StringComparison.OrdinalIgnoreCase);

    private static string Safe(string transferId)
    {
        var chars = transferId.Where(c => char.IsAsciiLetterOrDigit(c) || c is '-' or '_').ToArray();
        return chars.Length > 0 ? new string(chars) : "transfer";
    }
}
