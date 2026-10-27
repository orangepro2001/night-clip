using ClipBridge.Windows.Interop;

namespace ClipBridge.Windows.Logic;

/// <summary>
/// Export and import of notes. The core wants absolute paths and refuses to overwrite an export, while a save dialog has
/// already asked the user whether an existing file may be replaced: the ZIP is therefore written next to the chosen name first
/// and only then moved over it, so an export that fails never destroys the older file. Blocks; run it off the UI thread.
/// </summary>
public static class NotesTransfer
{
    public static string DefaultExportName(DateTimeOffset now) =>
        Strings.NotesExportFileName(now.ToString("yyyy-MM-dd", System.Globalization.CultureInfo.InvariantCulture));

    private static void RequireAbsolute(string path)
    {
        if (string.IsNullOrWhiteSpace(path) || !Path.IsPathFullyQualified(path)) throw new CoreException("INVALID", "the path has to be absolute");
    }

    /// <summary>Writes the ZIP to <paramref name="path"/> (replacing a file that is there). Throws <see cref="CoreException"/> or an IO exception.</summary>
    public static NoteExportResult ExportTo(NotesApi api, string path, bool includeTrash)
    {
        RequireAbsolute(path);
        var temp = path + "." + Guid.NewGuid().ToString("N")[..8] + ".part";
        try
        {
            var result = api.Export(temp, includeTrash);
            File.Move(temp, path, overwrite: true);
            return result with { Path = path };
        }
        finally
        {
            try { if (File.Exists(temp)) File.Delete(temp); } catch (IOException) { /* a stray .part file does no harm */ }
        }
    }

    /// <summary>Imports a .zip, a text file or a folder as new notes.</summary>
    public static NoteImportResult Import(NotesApi api, string path)
    {
        RequireAbsolute(path);
        return api.Import(path);
    }
}

/// <summary>The words that report what an export, an import or a cleanup did.</summary>
public static class NotesTransferText
{
    public static string ExportDone(NoteExportResult r) => Strings.NotesExportDone(r.Count, Formatting.Bytes(r.Bytes));

    /// <summary>"导入了 3 篇" or "导入了 3 篇，跳过 2 个".</summary>
    public static string ImportSummary(NoteImportResult r) =>
        r.Skipped.Count == 0 ? Strings.NotesImportDone(r.Imported) : Strings.NotesImportDoneSkipped(r.Imported, r.Skipped.Count);

    /// <summary>The core's reason for skipping a file (English text) in Chinese; a reason of a newer core is shown as it is.</summary>
    public static string SkipReason(string reason) => reason switch
    {
        "not a regular file" => Strings.NotesSkipNotRegular,
        "larger than the 1 MiB limit of a note" => Strings.NotesSkipTooLarge,
        "cannot be read" => Strings.NotesSkipUnreadable,
        "not a text file" => Strings.NotesSkipNotText,
        "" => Strings.Unknown,
        _ => reason,
    };

    /// <summary>One line per skipped file: its name, then why.</summary>
    public static string ImportDetails(NoteImportResult r) =>
        string.Join("\n", r.Skipped.Select(s => FileDisplay.Sanitize(s.Name) + "  —  " + SkipReason(s.Reason)));

    /// <summary>"释放了 3.2 MB", or that there was nothing to remove.</summary>
    public static string CleanupDone(AttachmentCleanupResult r) =>
        r.Removed == 0 ? Strings.AttachmentCleanupNothing : Strings.AttachmentCleanupDone(r.Removed, Formatting.Bytes(r.FreedBytes));
}
