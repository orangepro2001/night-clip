using System.Security.Cryptography;
using System.Text;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Markup.Xaml;
using Avalonia.Threading;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using MoonPassword.Windows.Update;
using MoonPassword.Windows.Views;

namespace MoonPassword.Windows;

public partial class App : Application
{
    /// <summary>
    /// Development override of the data folder (environment variable). A second, isolated MoonPassword can then run
    /// next to the installed one: its own database, identity, logs and single-instance names.
    /// </summary>
    public const string DataDirVariable = "MOONPASSWORD_DATA_DIR";

    /// <summary>Per-user data folder (database, key file, downloaded updates, staging, logs).</summary>
    public static string DataDir { get; } = ResolveDataDir();

    public static string StagingDir => Path.Combine(DataDir, "staging");

    /// <summary>"" for the installed app; ".dev-xxxxxxxx" for an instance started with <see cref="DataDirVariable"/>.</summary>
    public static string InstanceSuffix { get; } = ComputeSuffix();

    public static bool IsDevInstance => InstanceSuffix.Length > 0;

    private ShellController? _shell;
    private IClassicDesktopStyleApplicationLifetime? _desktop;
    private string? _snapshotDir;

    public override void Initialize() => AvaloniaXamlLoader.Load(this);

    public override void OnFrameworkInitializationCompleted()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            _desktop = desktop;
            // Closing the window only hides it; "退出" in the tray menu (or the session ending) really exits.
            desktop.ShutdownMode = ShutdownMode.OnExplicitShutdown;
            Setup(desktop);
        }
        base.OnFrameworkInitializationCompleted();
    }

    private void Setup(IClassicDesktopStyleApplicationLifetime desktop)
    {
        Dispatcher.UIThread.UnhandledException += (_, e) =>
        {
            // A tray application should survive a faulty page or callback; the log has the type and message.
            AppLog.Error("ui", e.Exception);
            e.Handled = true;
        };
        AppDomain.CurrentDomain.UnhandledException += (_, args) =>
            AppLog.Write("unhandled: " + (args.ExceptionObject as Exception)?.GetType().Name);
        TaskScheduler.UnobservedTaskException += (_, args) =>
        {
            AppLog.Error("unobserved task", args.Exception);
            args.SetObserved();
        };

        UpdateApplier.Cleanup(StagingDir, AppContext.BaseDirectory);
        var options = StartupOptions.Parse(desktop.Args ?? []);
        _snapshotDir = IsDevInstance ? Snapshots.ParseDir(desktop.Args ?? []) : null;
        AppIcons.Register();

        _shell = new ShellController(Dispatcher.UIThread);
        Appearance.Apply(_shell.Prefs);
        if (Program.Single is { } single)
            single.ActivationRequested += () => Dispatcher.UIThread.Post(() => _shell?.ShowMainWindow());
        desktop.ShutdownRequested += (_, _) => _shell?.MarkExiting(); // Windows is ending the session
        desktop.Exit += (_, _) => _shell?.Dispose();                  // tray icon, clipboard listener, worker, core

        // Created before anything else so that it is the window balloons and dialogs refer to; shown only when appropriate.
        _ = new MainWindow(_shell);

        Dispatcher.UIThread.Post(() => _ = StartAsync(options));
    }

    private async Task StartAsync(StartupOptions options)
    {
        var shell = _shell!;
        try
        {
            await shell.StartAsync();
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

        if (_snapshotDir is { } dir)
        {
            await Snapshots.RunAsync(shell, dir);
            return;
        }
        if (!options.Minimized) shell.ShowMainWindow();
        if (shell.StartupWarning is { } warning) shell.Balloon(Strings.AppName, warning, () => shell.ShowMainWindow());
        await shell.RunFirstRunIfNeededAsync(options.Minimized);
    }

    private void Fatal(string title, string text)
    {
        NativeDialogs.Error(title, text);
        _shell?.MarkExiting();
        _desktop?.Shutdown(1);
    }

    private static string ResolveDataDir()
    {
        var overridden = Environment.GetEnvironmentVariable(DataDirVariable);
        if (!string.IsNullOrWhiteSpace(overridden) && Path.IsPathFullyQualified(overridden)) return overridden.TrimEnd('\\', '/');
        return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "MoonPassword");
    }

    private static string ComputeSuffix()
    {
        var overridden = Environment.GetEnvironmentVariable(DataDirVariable);
        if (string.IsNullOrWhiteSpace(overridden) || !Path.IsPathFullyQualified(overridden)) return "";
        var hash = SHA256.HashData(Encoding.UTF8.GetBytes(DataDir.ToUpperInvariant()));
        return ".dev-" + Convert.ToHexString(hash, 0, 4).ToLowerInvariant();
    }
}
