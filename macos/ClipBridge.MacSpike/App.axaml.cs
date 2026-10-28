using System;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Markup.Xaml;

namespace ClipBridge.MacSpike;

public partial class App : Application
{
    private MainWindow? _window;

    public override void Initialize() => AvaloniaXamlLoader.Load(this);

    public override void OnFrameworkInitializationCompleted()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            // Closing the window keeps the app alive in the menu bar, like the Windows tray app.
            desktop.ShutdownMode = ShutdownMode.OnExplicitShutdown;
            _window = new MainWindow();
            desktop.MainWindow = _window;
        }
        base.OnFrameworkInitializationCompleted();
    }

    private void OnShow(object? sender, EventArgs e)
    {
        _window?.Show();
        _window?.Activate();
    }

    private void OnSendClipboard(object? sender, EventArgs e) => _window?.SimulateSend();

    private void OnQuit(object? sender, EventArgs e) =>
        (ApplicationLifetime as IClassicDesktopStyleApplicationLifetime)?.Shutdown();
}
