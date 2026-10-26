using System.Diagnostics;
using System.IO;
using System.IO.Compression;

namespace ClipBridge.Windows.Update;

/// <summary>Arguments of the hidden "apply update" start mode.</summary>
public sealed record ApplyArgs(int Pid, string Target);

/// <summary>
/// Installs a downloaded Windows update. The running app cannot overwrite its
/// own files, so it extracts the package to a staging folder and starts the
/// staged (new) ClipBridge.exe with <c>--apply-update --pid N --target DIR</c>;
/// that process waits for the old one to exit, replaces the files in DIR
/// (rolling back if anything fails), starts the installed exe and quits.
/// Contains no WPF so it can be unit tested.
/// </summary>
public static class UpdateApplier
{
    public const string ExeName = "ClipBridge.exe";
    public const string ApplyFlag = "--apply-update";
    private const string BackupSuffix = ".old";
    private const long MaxExtractedBytes = 1L << 30; // 1 GiB
    private const int MaxEntries = 5000;

    /// <summary>
    /// Extracts <paramref name="zipPath"/> into a new folder under
    /// <paramref name="stagingRoot"/> and returns it. Refuses entries that
    /// escape the folder, oversized packages, and packages without the exe.
    /// </summary>
    public static string Stage(string zipPath, string stagingRoot)
    {
        var stage = Path.Combine(stagingRoot, Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(stage);
        try
        {
            using var zip = ZipFile.OpenRead(zipPath);
            if (zip.Entries.Count > MaxEntries) throw new InvalidDataException("update package has too many files");
            long total = 0;
            var root = Path.GetFullPath(stage) + Path.DirectorySeparatorChar;
            foreach (var entry in zip.Entries)
            {
                total += entry.Length;
                if (total > MaxExtractedBytes) throw new InvalidDataException("update package is too large");
                var dest = Path.GetFullPath(Path.Combine(stage, entry.FullName));
                if (!dest.StartsWith(root, StringComparison.Ordinal))
                    throw new InvalidDataException($"update package entry escapes the folder: {entry.FullName}");
                if (entry.FullName.EndsWith('/') || entry.FullName.EndsWith('\\')) // directory entry
                {
                    Directory.CreateDirectory(dest);
                    continue;
                }
                Directory.CreateDirectory(Path.GetDirectoryName(dest)!);
                entry.ExtractToFile(dest, overwrite: false);
            }
            if (!File.Exists(Path.Combine(stage, ExeName)))
                throw new InvalidDataException($"update package does not contain {ExeName}");
            return stage;
        }
        catch
        {
            TryDeleteDirectory(stage);
            throw;
        }
    }

    /// <summary>Starts the staged exe in apply mode.</summary>
    public static Process LaunchApplier(string stagedDir, string targetDir, int pid)
    {
        var psi = new ProcessStartInfo(Path.Combine(stagedDir, ExeName))
        {
            WorkingDirectory = stagedDir,
            UseShellExecute = false,
        };
        psi.ArgumentList.Add(ApplyFlag);
        psi.ArgumentList.Add("--pid");
        psi.ArgumentList.Add(pid.ToString());
        psi.ArgumentList.Add("--target");
        psi.ArgumentList.Add(targetDir);
        return Process.Start(psi) ?? throw new InvalidOperationException("could not start the updater");
    }

    public static bool TryParseApplyArgs(string[] args, out ApplyArgs? parsed)
    {
        parsed = null;
        if (!args.Contains(ApplyFlag)) return false;
        string? pid = null, target = null;
        for (var i = 0; i < args.Length - 1; i++)
        {
            if (args[i] == "--pid") pid = args[i + 1];
            else if (args[i] == "--target") target = args[i + 1];
        }
        if (!int.TryParse(pid, out var p) || p <= 0 || string.IsNullOrWhiteSpace(target)) return false;
        parsed = new ApplyArgs(p, target);
        return true;
    }

    /// <summary>
    /// Waits for <paramref name="waitForPid"/> to exit, then copies every file of
    /// <paramref name="stagedDir"/> over <paramref name="targetDir"/>. Existing files are
    /// moved aside first, so a failure restores the previous version. Files that
    /// are not part of the package are left alone.
    /// </summary>
    public static void Apply(string stagedDir, string targetDir, int waitForPid, TimeSpan timeout)
    {
        if (Path.GetFullPath(stagedDir).TrimEnd(Path.DirectorySeparatorChar) ==
            Path.GetFullPath(targetDir).TrimEnd(Path.DirectorySeparatorChar))
            throw new InvalidOperationException("staging and target folders are the same");
        if (!File.Exists(Path.Combine(stagedDir, ExeName)))
            throw new InvalidDataException($"{ExeName} missing from the staged update");

        WaitForExit(waitForPid, timeout);
        Directory.CreateDirectory(targetDir);

        var movedAside = new List<(string Dest, string Backup)>();
        var created = new List<string>();
        try
        {
            foreach (var src in Directory.EnumerateFiles(stagedDir, "*", SearchOption.AllDirectories))
            {
                var rel = Path.GetRelativePath(stagedDir, src);
                var dest = Path.Combine(targetDir, rel);
                Directory.CreateDirectory(Path.GetDirectoryName(dest)!);
                if (File.Exists(dest))
                {
                    var backup = dest + BackupSuffix;
                    Retry(() => File.Move(dest, backup, overwrite: true));
                    movedAside.Add((dest, backup));
                }
                Retry(() => File.Copy(src, dest, overwrite: false));
                created.Add(dest);
            }
        }
        catch
        {
            foreach (var path in created) TryDelete(path);
            foreach (var (dest, backup) in movedAside)
            {
                TryDelete(dest);
                try { File.Move(backup, dest, overwrite: true); } catch (IOException) { }
            }
            throw;
        }
        foreach (var (_, backup) in movedAside) TryDelete(backup);
    }

    /// <summary>Removes leftovers of earlier updates; call on a normal start.</summary>
    public static void Cleanup(string stagingRoot, string targetDir)
    {
        TryDeleteDirectory(stagingRoot);
        try
        {
            foreach (var f in Directory.EnumerateFiles(targetDir, "*" + BackupSuffix, SearchOption.AllDirectories))
                TryDelete(f);
        }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }

    private static void WaitForExit(int pid, TimeSpan timeout)
    {
        try
        {
            using var p = Process.GetProcessById(pid);
            if (!p.WaitForExit(timeout)) throw new TimeoutException("the running ClipBridge did not exit in time");
        }
        catch (ArgumentException) { /* already gone */ }
    }

    // Antivirus and the Windows loader hold files briefly after a process exits.
    private static void Retry(Action action)
    {
        for (var attempt = 1; ; attempt++)
        {
            try { action(); return; }
            catch (IOException) when (attempt < 10) { Thread.Sleep(200); }
            catch (UnauthorizedAccessException) when (attempt < 10) { Thread.Sleep(200); }
        }
    }

    private static void TryDelete(string path)
    {
        try { File.Delete(path); } catch (IOException) { } catch (UnauthorizedAccessException) { }
    }

    private static void TryDeleteDirectory(string path)
    {
        try { if (Directory.Exists(path)) Directory.Delete(path, recursive: true); }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }
}
