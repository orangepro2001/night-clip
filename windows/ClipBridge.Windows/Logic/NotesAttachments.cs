using System.Text;
using ClipBridge.Windows.Interop;

namespace ClipBridge.Windows.Logic;

/// <summary>
/// The address of an attachment in a note: <c>blob:</c> and 64 hexadecimal digits (the SHA-256 of the file). It is NOT a web
/// address and never goes through the link guard of <see cref="NoteLinks"/>: the preview recognises it here and asks the core
/// for the file.
/// </summary>
public static class BlobRef
{
    public const string Scheme = "blob:";
    public const int IdLength = 64;

    public static bool IsBlob(string? href) => href is not null && href.TrimStart().StartsWith(Scheme, StringComparison.OrdinalIgnoreCase);

    /// <summary>The blob id (lower case) of a well-formed <c>blob:</c> address; false for anything else, such as a short or non-hex id.</summary>
    public static bool TryParse(string? href, out string blobId)
    {
        blobId = "";
        if (!IsBlob(href)) return false;
        var id = href!.Trim()[Scheme.Length..];
        if (!IsValidId(id)) return false;
        blobId = id.ToLowerInvariant();
        return true;
    }

    public static bool IsValidId(string? id) =>
        id is { Length: IdLength } && id.All(c => c is >= '0' and <= '9' or >= 'a' and <= 'f' or >= 'A' and <= 'F');
}

/// <summary>Which attachments the preview can draw as pictures.</summary>
public static class AttachmentTypes
{
    /// <summary>The types the core writes as image syntax (SVG is deliberately not among them: it can carry scripts).</summary>
    public static bool IsRasterImage(string? mime) =>
        mime is not null && ImageExtensions.ContainsKey(mime.Trim());

    private static readonly Dictionary<string, string> ImageExtensions = new(StringComparer.OrdinalIgnoreCase)
    {
        ["image/png"] = ".png", ["image/jpeg"] = ".jpg", ["image/gif"] = ".gif", ["image/webp"] = ".webp", ["image/bmp"] = ".bmp",
    };

    /// <summary>The extension of the cached copy of a picture, so that the decoder and the system agree on its type.</summary>
    public static string CacheExtension(string? mime) => mime is not null && ImageExtensions.TryGetValue(mime.Trim(), out var ext) ? ext : ".bin";
}

/// <summary>What may be opened with the default program, and the names attachments get on disk. File names come from other devices: untrusted text.</summary>
public static class AttachmentPolicy
{
    private const int MaxNameLength = 120;

    /// <summary>The largest attachment the core accepts (32 MiB).</summary>
    public const long MaxBytes = 32L << 20;

    /// <summary>Types Windows runs, loads or lets run something when they are opened, on top of <see cref="FileDisplay.IsExecutable"/>.</summary>
    private static readonly HashSet<string> MoreRisky = new(StringComparer.OrdinalIgnoreCase)
    {
        ".url", ".application", ".appinstaller", ".gadget", ".inf", ".scf", ".chm", ".sys", ".drv", ".ocx", ".ps1xml", ".psd1",
        ".sh", ".py", ".pyw", ".vb", ".ws", ".msp", ".mst", ".xll", ".wll", ".jnlp", ".diagcab", ".settingcontent-ms", ".library-ms",
        ".msixbundle", ".appxbundle", ".ade", ".adp", ".mde", ".mdb", ".accde",
    };

    private static readonly HashSet<string> Reserved = new(StringComparer.OrdinalIgnoreCase)
    {
        "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
        "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
    };

    private static readonly char[] BadNameChars = { '\\', '/', ':', '*', '?', '"', '<', '>', '|', '\0' };

    /// <summary>
    /// A name that is safe to create as a file: no control or direction-changing characters, no path separators, no trailing dots
    /// or spaces (Windows drops them, which could turn "a.exe " into something that looks different), no device names such as
    /// CON, at most 120 characters with the extension kept. <paramref name="fallback"/> is used when nothing is left.
    /// </summary>
    public static string SafeFileName(string? name, string fallback)
    {
        var s = FileDisplay.Sanitize(name);
        var sb = new StringBuilder(s.Length);
        foreach (var ch in s) sb.Append(Array.IndexOf(BadNameChars, ch) >= 0 ? '_' : ch);
        s = sb.ToString().Trim().TrimEnd('.', ' ');
        if (s.Length == 0) return fallback;
        var dot = s.IndexOf('.');
        var stem = dot < 0 ? s : s[..dot];
        if (Reserved.Contains(stem.TrimEnd())) s = "_" + s;
        if (s.Length > MaxNameLength)
        {
            var ext = FileDisplay.Extension(s);
            if (ext.Length > 16) ext = "";
            var keep = MaxNameLength - ext.Length;
            var head = s[..keep];
            if (char.IsHighSurrogate(head[^1])) head = head[..^1];
            s = head + ext;
        }
        return s;
    }

    /// <summary>True for programs and scripts (by the last extension of the safe name): they are never opened without the user saying so.</summary>
    public static bool IsRisky(string? fileName)
    {
        var ext = Path.GetExtension(SafeFileName(fileName, "attachment")); // also for a file that is nothing but ".exe"
        return ext.Length > 1 && (FileDisplay.IsExecutable("a" + ext) || MoreRisky.Contains(ext));
    }

    /// <summary>The name of a picture pasted from the clipboard, as it will appear in the note: "image-20261027-153000.png".</summary>
    public static string PastedImageName(DateTimeOffset now) =>
        "image-" + now.ToString("yyyyMMdd-HHmmss", System.Globalization.CultureInfo.InvariantCulture) + ".png";
}

public enum AttachmentState
{
    /// <summary>The file is on this device.</summary>
    Present,

    /// <summary>A note refers to it but it has not been synced here yet: a placeholder, and a new look when <c>notes.attachment</c> arrives.</summary>
    Missing,

    /// <summary>The core or the disk failed; see <see cref="AttachmentView.ErrorCode"/>.</summary>
    Failed,
}

/// <summary>What the preview needs to know about one attachment. <see cref="CachePath"/> is the cached copy of a picture (empty when none was asked for).</summary>
public sealed record AttachmentView(
    string BlobId, AttachmentState State, string Name, string Mime, long Size, string CachePath, string ErrorCode = "")
{
    public bool IsPresent => State == AttachmentState.Present;
    public bool IsImage => IsPresent && AttachmentTypes.IsRasterImage(Mime);

    /// <summary>"report.pdf (1.2 MB)", the words of the chip.</summary>
    public string Label => Size > 0 ? Strings.AttachmentLabel(Name, Formatting.Bytes(Size)) : Name;

    public static AttachmentView Missing(string blobId) => new(blobId, AttachmentState.Missing, "", "", 0, "");

    public static AttachmentView Failure(string blobId, string code) => new(blobId, AttachmentState.Failed, "", "", 0, "", code);
}

/// <summary>
/// Gets attachments for the preview and for opening: asks the core about a blob, writes pictures into a cache folder (once; the
/// file name is the blob id, so the same picture is never written twice) and exports other files to a temporary file with their
/// real name. Everything here blocks; run it off the UI thread. It never throws for a missing or broken attachment: the result
/// says what happened.
/// </summary>
public sealed class AttachmentResolver
{
    private readonly NotesApi _api;
    private readonly string _cacheDir;
    private readonly string _openDir;
    private readonly object _gate = new();
    private readonly Dictionary<string, NoteAttachment> _known = new();

    /// <param name="cacheDir">Where pictures are kept for the preview (created when needed).</param>
    /// <param name="openDir">Where files are exported to before they are opened (created when needed).</param>
    public AttachmentResolver(NotesApi api, string cacheDir, string openDir)
    {
        _api = api;
        _cacheDir = cacheDir;
        _openDir = openDir;
    }

    /// <summary>Forgets what was learnt about attachments (after a cleanup).</summary>
    public void Forget()
    {
        lock (_gate) _known.Clear();
    }

    private static bool IsFailure(Exception ex) => NoteEditorSession.IsCoreFailure(ex) || ex is IOException or UnauthorizedAccessException;

    private NoteAttachment? Info(string blobId)
    {
        lock (_gate) if (_known.TryGetValue(blobId, out var known)) return known;
        var info = _api.AttachmentGet(blobId);
        if (!info.Present) return null;
        lock (_gate) _known[blobId] = info;
        return info;
    }

    /// <summary>
    /// Looks the attachment up. With <paramref name="wantImageFile"/> a picture is also written to the cache folder and
    /// <see cref="AttachmentView.CachePath"/> names the file.
    /// </summary>
    public AttachmentView Resolve(string blobId, bool wantImageFile)
    {
        if (!BlobRef.IsValidId(blobId)) return AttachmentView.Failure(blobId, "INVALID");
        try
        {
            var info = Info(blobId);
            if (info is null) return AttachmentView.Missing(blobId);
            var view = new AttachmentView(blobId, AttachmentState.Present, AttachmentPolicy.SafeFileName(info.Name, blobId[..8]), info.Mime, info.Size, "");
            return wantImageFile && view.IsImage ? view with { CachePath = EnsureCached(blobId, info.Mime) } : view;
        }
        catch (CoreException ex) when (ex.Code == "NOT_FOUND")
        {
            return AttachmentView.Missing(blobId);
        }
        catch (Exception ex) when (IsFailure(ex))
        {
            return AttachmentView.Failure(blobId, NoteEditorSession.CodeOf(ex));
        }
    }

    /// <summary>The cached copy of a picture; written through a temporary name so that a half-written file is never taken for the picture.</summary>
    private string EnsureCached(string blobId, string mime)
    {
        var path = Path.Combine(_cacheDir, blobId + AttachmentTypes.CacheExtension(mime));
        if (File.Exists(path) && new FileInfo(path).Length > 0) return path;
        Directory.CreateDirectory(_cacheDir);
        var temp = path + "." + Guid.NewGuid().ToString("N")[..8] + ".part";
        try
        {
            _api.AttachmentExport(blobId, temp);
            try { File.Move(temp, path, overwrite: false); }
            catch (IOException) when (File.Exists(path)) { /* another look got there first */ }
        }
        finally
        {
            try { if (File.Exists(temp)) File.Delete(temp); } catch (IOException) { /* a stray .part file does no harm */ }
        }
        return path;
    }

    /// <summary>
    /// Writes the attachment to a temporary file under its real (sanitised) name so that the default program can open it. Throws
    /// <see cref="CoreException"/> NOT_FOUND when the file is not on this device.
    /// </summary>
    public (string Path, string Name) ExportForOpening(string blobId)
    {
        if (!BlobRef.IsValidId(blobId)) throw new CoreException("INVALID", "not an attachment address");
        var info = Info(blobId) ?? throw new CoreException("NOT_FOUND", "the attachment is not on this device yet");
        var name = AttachmentPolicy.SafeFileName(info.Name, blobId[..8]);
        var dir = Path.Combine(_openDir, blobId[..16]);
        Directory.CreateDirectory(dir);
        var path = Path.Combine(dir, name);
        _api.AttachmentExport(blobId, path);
        return (path, name);
    }
}
