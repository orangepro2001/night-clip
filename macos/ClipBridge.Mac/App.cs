using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Media.Imaging;
using Avalonia.Platform;
using Avalonia.Threading;
using ClipBridge.Mac.Platform;
using ClipBridge.Mac.Views;
using ClipBridge.Windows;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Update;

namespace ClipBridge.Mac;

/// <summary>
/// Menu-bar application: the status item's menu is the main entry, the window opens on demand and only
/// hides when closed. `--minimized` (login item) starts without the window.
/// </summary>
public sealed partial class App : Application
{
    private MacController? _shell;
    private MainWindow? _window;
    private NativeMenuItem? _syncItem;
    private IClassicDesktopStyleApplicationLifetime? _desktop;

    // The theme is declared in App.axaml: created with `new` in code it adds no control templates
    // (buttons render as bare text), so it must go through the XAML loader.
    public override void Initialize() => Avalonia.Markup.Xaml.AvaloniaXamlLoader.Load(this);

    public override void OnFrameworkInitializationCompleted()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            _desktop = desktop;
            desktop.ShutdownMode = ShutdownMode.OnExplicitShutdown;
            // ⌘Q, the Dock's Quit and logout ask the lifetime to shut down: let the main window really close
            // (it otherwise only hides, which would cancel the quit) and stop the core first.
            desktop.ShutdownRequested += (_, _) =>
            {
                _window?.AllowClose();
                _shell?.Dispose();
            };
            Dispatcher.UIThread.Post(async () => await StartAsync(desktop.Args ?? Array.Empty<string>()));
        }
        base.OnFrameworkInitializationCompleted();
    }

    private async Task StartAsync(string[] args)
    {
        MacLog.Init();
        MacLog.Write($"start {BuildInfo.VersionName} ({BuildInfo.VersionCode})");
        MacUpdateApplier.Cleanup(MacPaths.StagingDir);
        AppDomain.CurrentDomain.UnhandledException += (_, e) => MacLog.Write("unhandled: " + (e.ExceptionObject as Exception)?.GetType().Name);
        TaskScheduler.UnobservedTaskException += (_, e) => { MacLog.Error("unobserved task", e.Exception); e.SetObserved(); };

        _shell = new MacController();
        _window = new MainWindow();
        _shell.ShowRequested += ShowMain;
        _shell.ExitRequested += Quit;
        _shell.StateChanged += UpdateMenu;
        SetupTray();
        if (this.TryGetFeature<IActivatableLifetime>() is { } activatable)
            activatable.Activated += (_, e) => { if (e.Kind == ActivationKind.Reopen) ShowMain(null); };

        try
        {
            await _shell.StartAsync();
        }
        catch (KeyStoreException ex)
        {
            await FatalAsync(MacStrings.KeyStoreFailedTitle, ex.Message);
            return;
        }
        catch (Exception ex) when (ex is DllNotFoundException or BadImageFormatException or EntryPointNotFoundException)
        {
            MacLog.Error("core load", ex);
            await FatalAsync(Strings.StartupFailedTitle, Strings.CoreMissingText);
            return;
        }
        catch (CoreException ex)
        {
            MacLog.Write("core start failed: " + ex.Code);
            await FatalAsync(Strings.StartupFailedTitle, ErrorMessages.ForCode(ex.Code) + "\n\n" + ex.Message);
            return;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            MacLog.Error("startup", ex);
            await FatalAsync(Strings.StartupFailedTitle, ex.Message);
            return;
        }
        if (!args.Contains("--minimized")) ShowMain(null);
        UpdateMenu();
        if (args.Contains("--snapshot")) DispatcherTimer.RunOnce(() => _ = SnapshotPagesAsync(), TimeSpan.FromSeconds(3));
    }

    /// <summary>
    /// Diagnostics (`open -a ClipBridge --args --snapshot`): renders every main-window page to
    /// ~/Library/Logs/ClipBridge/page-*.png, so the look can be checked remotely without screen-recording rights.
    /// </summary>
    private async Task SnapshotPagesAsync()
    {
        if (_window is null) return;
        foreach (var page in Enum.GetValues<MainPage>())
        {
            _window.Show(page);
            await Task.Delay(500);
            try
            {
                var scale = _window.RenderScaling;
                var size = new PixelSize((int)(_window.Bounds.Width * scale), (int)(_window.Bounds.Height * scale));
                using var bmp = new RenderTargetBitmap(size, new Vector(96 * scale, 96 * scale));
                bmp.Render(_window);
                bmp.Save(Path.Combine(MacPaths.LogDir, $"page-{page}.png"));
            }
            catch (Exception ex) { MacLog.Error("snapshot", ex); }
        }
        _window.Show(MainPage.Devices);
        MacLog.Write("snapshot done");
    }

    private void ShowMain(MainPage? page)
    {
        if (_window is null) return;
        _window.Show();
        if (page is { } p) _window.Show(p);
        _window.Activate();
    }

    private void Quit()
    {
        _window?.AllowClose();
        _shell?.Dispose();
        _desktop?.Shutdown();
    }

    private async Task FatalAsync(string title, string text)
    {
        var w = new Window { Title = title, Width = 420, SizeToContent = SizeToContent.Height, WindowStartupLocation = WindowStartupLocation.CenterScreen };
        w.Content = Ui.Column(16, Ui.Text(text), Ui.Button(Strings.BtnOk, w.Close)).With(p => p.Margin = new Thickness(20));
        var done = new TaskCompletionSource();
        w.Closed += (_, _) => done.TrySetResult();
        w.Show();
        await done.Task;
        Quit();
    }

    private void SetupTray()
    {
        _syncItem = new NativeMenuItem(MacStrings.MenuSync) { ToggleType = MenuItemToggleType.CheckBox, IsChecked = true };
        _syncItem.Click += async (_, _) => { if (_shell is { } s) await s.SetCaptureAsync(!s.Settings.Capture); };
        var menu = new NativeMenu
        {
            Item(MacStrings.MenuShow, () => ShowMain(null)),
            Item(MacStrings.MenuSendClipboard, () => _shell?.SendCurrentClipboard()),
            _syncItem,
            new NativeMenuItemSeparator(),
            Item(MacStrings.MenuPair, PairListenWindow.ShowOrActivate),
            Item(MacStrings.MenuJoin, PairJoinWindow.ShowOrActivate),
            Item(MacStrings.MenuCheckUpdate, async () =>
            {
                ShowMain(MainPage.Settings);
                if (_shell is { } s && await s.UpdateActionAsync(UpdateInfo.Action.Check) is { } error) s.ShowStatus(error);
            }),
            new NativeMenuItemSeparator(),
            Item(MacStrings.MenuQuit, Quit),
        };
        using var icon = AssetLoader.Open(new Uri("avares://ClipBridge/Assets/tray.png"));
        var tray = new TrayIcon { Icon = new WindowIcon(new Bitmap(icon)), ToolTipText = Strings.AppName, Menu = menu };
        tray.Clicked += (_, _) => ShowMain(null);
        TrayIcon.SetIcons(this, new TrayIcons { tray });
    }

    private static NativeMenuItem Item(string header, Action onClick)
    {
        var item = new NativeMenuItem(header);
        item.Click += (_, _) => onClick();
        return item;
    }

    private void UpdateMenu()
    {
        if (_syncItem is not null && _shell is not null) _syncItem.IsChecked = _shell.Settings.Capture;
    }
}
