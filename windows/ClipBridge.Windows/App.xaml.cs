using System.Diagnostics;
using System.IO;
using System.Windows;
using ClipBridge.Windows.Update;

namespace ClipBridge.Windows;

public partial class App : Application
{
    // TODO(phase0): single-instance mutex, tray icon, start minimised.

    /// <summary>Per-user data folder (database, key file, downloaded updates, staging).</summary>
    public static string DataDir => Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "ClipBridge");

    public static string StagingDir => Path.Combine(DataDir, "staging");

    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);

        // Hidden mode: this is the staged new version, started by the old one to
        // swap the files. No window, no core.
        if (UpdateApplier.TryParseApplyArgs(e.Args, out var apply))
        {
            ShutdownMode = ShutdownMode.OnExplicitShutdown;
            RunApply(apply!);
            Shutdown();
            return;
        }

        UpdateApplier.Cleanup(StagingDir, AppContext.BaseDirectory);
        new MainWindow().Show();
    }

    private static void RunApply(ApplyArgs apply)
    {
        var exe = Path.Combine(apply.Target, UpdateApplier.ExeName);
        try
        {
            UpdateApplier.Apply(AppContext.BaseDirectory, apply.Target, apply.Pid, TimeSpan.FromSeconds(30));
        }
        catch (Exception ex)
        {
            MessageBox.Show(
                $"ClipBridge could not install the update and kept the current version.\n\n{ex.Message}",
                "ClipBridge update", MessageBoxButton.OK, MessageBoxImage.Warning);
        }
        // Start whatever is installed now: the new version, or the old one after a rollback.
        if (File.Exists(exe))
            Process.Start(new ProcessStartInfo(exe) { WorkingDirectory = apply.Target, UseShellExecute = false });
    }
}
