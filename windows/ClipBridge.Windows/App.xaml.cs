using System.Diagnostics;
using System.IO;
using System.Windows;
using System.Windows.Threading;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using ClipBridge.Windows.Update;

namespace ClipBridge.Windows;

public partial class App : Application
{
    /// <summary>Per-user data folder (database, key file, downloaded updates, staging, logs).</summary>
    public static string DataDir => Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "ClipBridge");

    public static string StagingDir => Path.Combine(DataDir, "staging");

    private SingleInstance? _single;
    private ShellController? _shell;

    protected override async void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);
        ShutdownMode = ShutdownMode.OnExplicitShutdown; // closing the window only hides it; exit goes through the tray menu

        // Hidden mode: this is the staged new version, started by the old one to
        // swap the files. No window, no core, and no single-instance check (the old
        // instance still holds the mutex until it has exited).
        if (UpdateApplier.TryParseApplyArgs(e.Args, out var apply))
        {
            RunApply(apply!);
            Shutdown();
            return;
        }

        _single = new SingleInstance();
        if (!_single.TryAcquire())
        {
            _single.Dispose();
            _single = null;
            Shutdown();
            return;
        }

        DispatcherUnhandledException += OnDispatcherUnhandledException;
        AppDomain.CurrentDomain.UnhandledException += (_, args) =>
            AppLog.Write("unhandled: " + (args.ExceptionObject as Exception)?.GetType().Name);
        TaskScheduler.UnobservedTaskException += (_, args) =>
        {
            AppLog.Error("unobserved task", args.Exception);
            args.SetObserved();
        };
        SessionEnding += (_, _) => _shell?.MarkExiting();

        UpdateApplier.Cleanup(StagingDir, AppContext.BaseDirectory);
        var options = StartupOptions.Parse(e.Args);

        _shell = new ShellController(Dispatcher);
        _single.ActivationRequested += () =>
            Dispatcher.BeginInvoke(new Action(() => _shell?.ShowMainWindow()));

        // Created before anything else so that it is the application's main window; shown only when appropriate.
        var main = new MainWindow();
        MainWindow = main;

        try
        {
            await _shell.StartAsync();
        }
        catch (DevKeyException ex)
        {
            Fatal(Strings.KeyCorruptTitle, Strings.KeyCorruptText(ex.Message));
            return;
        }
        catch (Exception ex) when (ex is DllNotFoundException or BadImageFormatException)
        {
            AppLog.Error("core load", ex);
            Fatal(Strings.StartupFailedTitle, Strings.CoreMissingText);
            return;
        }
        catch (CoreException ex)
        {
            AppLog.Write("core start failed: " + ex.Code);
            Fatal(Strings.StartupFailedTitle, ErrorMessages.ForCode(ex.Code) + "\n\n" + ex.Message);
            return;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            AppLog.Error("startup", ex);
            Fatal(Strings.StartupFailedTitle, ex.Message);
            return;
        }

        if (!options.Minimized) _shell.ShowMainWindow();
        if (_shell.StartupWarning is { } warning) _shell.Balloon(Strings.AppName, warning, () => _shell.ShowMainWindow());

        await _shell.RunFirstRunIfNeededAsync(options.Minimized);
    }

    private void Fatal(string title, string text)
    {
        MessageBox.Show(text, title, MessageBoxButton.OK, MessageBoxImage.Error);
        _shell?.MarkExiting();
        Shutdown(1);
    }

    private static void OnDispatcherUnhandledException(object sender, DispatcherUnhandledExceptionEventArgs e)
    {
        // A tray application should survive a faulty page or callback; the log has the type and message.
        AppLog.Error("ui", e.Exception);
        e.Handled = true;
    }

    protected override void OnExit(ExitEventArgs e)
    {
        try { _shell?.Dispose(); }   // tray icon, clipboard listener/hook, clipboard worker, core
        finally
        {
            _single?.Dispose();
            base.OnExit(e);
        }
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
            MessageBox.Show(Strings.UpdateApplyFailed(ex.Message), Strings.UpdateApplyFailedTitle,
                MessageBoxButton.OK, MessageBoxImage.Warning);
        }
        // Start whatever is installed now: the new version, or the old one after a rollback.
        if (File.Exists(exe))
            Process.Start(new ProcessStartInfo(exe) { WorkingDirectory = apply.Target, UseShellExecute = false });
    }
}
