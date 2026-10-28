using System.Diagnostics;

namespace ClipBridge.Mac.Platform;

/// <summary>
/// Installs a verified macOS package (a zip holding ClipBridge.app, made with `ditto -c -k --keepParent`).
/// The core has already checked the signed manifest, size and SHA-256 of <c>ready_path</c>. Here:
/// unpack with ditto (keeps symlinks, modes and signatures), check it is our bundle, then hand over to
/// a small shell script that waits for this process to exit, swaps the bundles (rolling back on
/// failure) and starts the installed one. No admin rights: a bundle the user cannot write is refused.
/// </summary>
public static class MacUpdateApplier
{
    public const string BundleName = "ClipBridge.app";
    public const string BundleId = "com.nightclip.clipbridge";

    /// <summary>Unpacks the package into a fresh staging folder; returns the staged bundle.</summary>
    public static string Stage(string zipPath, string stagingDir)
    {
        if (!File.Exists(zipPath)) throw new IOException("the downloaded package is missing");
        if (Directory.Exists(stagingDir)) Directory.Delete(stagingDir, recursive: true);
        Directory.CreateDirectory(stagingDir);
        Run("/usr/bin/ditto", "-x", "-k", zipPath, stagingDir);
        var staged = Path.Combine(stagingDir, BundleName);
        var plist = Path.Combine(staged, "Contents", "Info.plist");
        if (!File.Exists(plist) || !File.ReadAllText(plist).Contains("<string>" + BundleId + "</string>"))
            throw new InvalidDataException("the package does not contain " + BundleName);
        if (!File.Exists(Path.Combine(staged, "Contents", "MacOS", "ClipBridge")))
            throw new InvalidDataException("the package's bundle has no executable");
        return staged;
    }

    /// <summary>Starts the detached swap script; the caller exits right after.</summary>
    public static void LaunchSwap(string staged, string target, int pid)
    {
        var parent = Path.GetDirectoryName(target) ?? throw new IOException("bad bundle path");
        if (!CanWrite(parent)) throw new UnauthorizedAccessException("no write permission for " + parent);
        var script = Path.Combine(Path.GetDirectoryName(staged)!, "swap.sh");
        File.WriteAllText(script, """
            #!/bin/sh
            # args: pid staged target
            pid="$1"; staged="$2"; target="$3"; old="$target.old"
            i=0; while kill -0 "$pid" 2>/dev/null && [ $i -lt 300 ]; do sleep 0.1; i=$((i+1)); done
            rm -rf "$old"
            if mv "$target" "$old" && mv "$staged" "$target"; then
              rm -rf "$old"
            else
              [ -d "$old" ] && [ ! -d "$target" ] && mv "$old" "$target"
            fi
            open "$target"
            """.Replace("\r\n", "\n"));
        var psi = new ProcessStartInfo("/bin/sh") { UseShellExecute = false };
        foreach (var a in new[] { script, pid.ToString(System.Globalization.CultureInfo.InvariantCulture), staged, target }) psi.ArgumentList.Add(a);
        using var _ = Process.Start(psi) ?? throw new IOException("cannot start the update script");
    }

    /// <summary>Removes leftovers of an earlier update (the staging folder).</summary>
    public static void Cleanup(string stagingDir)
    {
        try { if (Directory.Exists(stagingDir)) Directory.Delete(stagingDir, recursive: true); }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { MacLog.Error("update cleanup", ex); }
    }

    private static bool CanWrite(string dir)
    {
        var probe = Path.Combine(dir, ".clipbridge-write-test-" + Environment.ProcessId);
        try
        {
            File.WriteAllText(probe, "");
            File.Delete(probe);
            return true;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { return false; }
    }

    private static void Run(string exe, params string[] args)
    {
        var psi = new ProcessStartInfo(exe) { UseShellExecute = false, RedirectStandardError = true };
        foreach (var a in args) psi.ArgumentList.Add(a);
        using var p = Process.Start(psi) ?? throw new IOException("cannot run " + exe);
        var err = p.StandardError.ReadToEnd();
        p.WaitForExit();
        if (p.ExitCode != 0) throw new IOException(Path.GetFileName(exe) + " failed: " + err.Trim());
    }
}
