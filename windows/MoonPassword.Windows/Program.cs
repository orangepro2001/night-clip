using System.Diagnostics;
using Avalonia;
using Avalonia.Controls;
using MoonPassword.Windows.Services;
using MoonPassword.Windows.Update;
using Lumen.UI;

namespace MoonPassword.Windows;

internal static class Program
{
    /// <summary>Held for the whole run of the first instance (released on exit).</summary>
    internal static SingleInstance? Single { get; private set; }

    [STAThread]
    public static int Main(string[] args)
    {
        // Hidden mode: this is the staged new version, started by the old one to swap the files. No window,
        // no core and no single-instance check (the old instance still holds the mutex until it has exited).
        if (UpdateApplier.TryParseApplyArgs(args, out var apply))
        {
            RunApply(apply!);
            return 0;
        }

        Single = new SingleInstance(App.InstanceSuffix);
        if (!Single.TryAcquire())
        {
            Single.Dispose();
            return 0; // the running instance has been asked to show its window
        }

        try
        {
            return BuildAvaloniaApp().StartWithClassicDesktopLifetime(args, ShutdownMode.OnExplicitShutdown);
        }
        finally
        {
            Single.Dispose();
        }
    }

    // Also used by the Avalonia previewer.
    public static AppBuilder BuildAvaloniaApp() =>
        AppBuilder.Configure<App>()
            .UseWin32()
            .UseSkia()
            .UseHarfBuzz()
            .UseLumen()
            .LogToTrace();

    private static void RunApply(ApplyArgs apply)
    {
        var exe = Path.Combine(apply.Target, UpdateApplier.ExeName);
        try
        {
            UpdateApplier.Apply(AppContext.BaseDirectory, apply.Target, apply.Pid, TimeSpan.FromSeconds(30));
        }
        catch (Exception ex)
        {
            NativeDialogs.Warning(Strings.UpdateApplyFailedTitle, Strings.UpdateApplyFailed(ex.Message));
        }
        // Start whatever is installed now: the new version, or the old one after a rollback.
        if (File.Exists(exe))
        {
            var start = new ProcessStartInfo(exe) { WorkingDirectory = apply.Target, UseShellExecute = false };
            if (apply.StartMinimized) start.ArgumentList.Add(Logic.StartupOptions.MinimizedFlag);
            using var restarted = Process.Start(start);
        }
    }
}
