using System.Text;

namespace ClipBridge.Windows.Logic;

/// <summary>Display helpers for file names that came from another device (untrusted text).</summary>
public static class FileDisplay
{
    private static readonly HashSet<string> Executable = new(StringComparer.OrdinalIgnoreCase)
    {
        ".exe", ".msi", ".bat", ".cmd", ".com", ".scr", ".ps1", ".psm1", ".vbs", ".vbe", ".js", ".jse",
        ".wsf", ".wsh", ".hta", ".lnk", ".pif", ".cpl", ".jar", ".reg", ".dll", ".msc", ".appx", ".msix",
    };

    /// <summary>
    /// Removes control characters and bidirectional overrides (which can make "evil‮fdp.exe" look like
    /// a PDF) so the shown name is the real name.
    /// </summary>
    public static string Sanitize(string? name)
    {
        if (string.IsNullOrEmpty(name)) return "";
        var sb = new StringBuilder(name.Length);
        foreach (var ch in name)
        {
            if (char.IsControl(ch)) continue;
            if (ch is >= '​' and <= '‏') continue;   // zero-width / LRM / RLM
            if (ch is >= '‪' and <= '‮') continue;   // embeddings and overrides
            if (ch is >= '⁦' and <= '⁩') continue;   // isolates
            if (ch == '﻿') continue;
            sb.Append(ch);
        }
        return sb.ToString().Trim();
    }

    /// <summary>Shortens in the middle but keeps the extension: "very-long-na….pdf".</summary>
    public static string Shorten(string? name, int max = 48)
    {
        var s = Sanitize(name);
        if (s.Length <= max || max < 8) return s;
        var dot = s.LastIndexOf('.');
        var ext = dot > 0 && s.Length - dot <= 8 ? s[dot..] : "";
        var keep = max - ext.Length - 1;
        if (keep < 3) { ext = ""; keep = max - 1; }
        var head = s[..keep];
        if (char.IsHighSurrogate(head[^1])) head = head[..^1];
        return head + "…" + ext;
    }

    public static string Extension(string? name)
    {
        var s = Sanitize(name);
        var dot = s.LastIndexOf('.');
        return dot > 0 && dot < s.Length - 1 ? s[dot..].ToLowerInvariant() : "";
    }

    private static readonly HashSet<string> PreviewImage = new(StringComparer.OrdinalIgnoreCase)
    {
        ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp",
    };

    /// <summary>Received images above this size get no preview window (decoding them costs too much memory).</summary>
    public const long MaxPreviewBytes = 64L << 20;

    /// <summary>
    /// A received file that opens the image preview (copy to clipboard / show in folder): an image type
    /// both desktops decode, and not empty or huge.
    /// </summary>
    public static bool IsPreviewableImage(string? name, long size) =>
        PreviewImage.Contains(Extension(name)) && size > 0 && size <= MaxPreviewBytes;

    /// <summary>True for file types Windows runs when opened; the UI asks before opening them.</summary>
    public static bool IsExecutable(string? name) => Executable.Contains(Extension(name));

    /// <summary>The last path segment of a Windows or POSIX path, whatever the host OS.</summary>
    public static string FileNameOf(string? path)
    {
        if (string.IsNullOrEmpty(path)) return "";
        var i = path.LastIndexOfAny(new[] { '\\', '/' });
        return i < 0 ? path : path[(i + 1)..];
    }

    /// <summary>The folder part of a Windows or POSIX path; empty if there is none.</summary>
    public static string DirectoryOf(string? path)
    {
        if (string.IsNullOrEmpty(path)) return "";
        var i = path.LastIndexOfAny(new[] { '\\', '/' });
        return i <= 0 ? "" : path[..i];
    }
}
