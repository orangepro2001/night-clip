using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using MoonPassword.Mac.Platform;
using MoonPassword.Windows;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Update;

namespace MoonPassword.Mac.Views;

/// <summary>
/// Sidebar (vault, settings) and the selected page. Closing hides the window; the app stays in the menu bar.
/// The settings page is rebuilt from the controller's snapshot when it changes.
/// </summary>
internal sealed class MainWindow : Window
{
    private readonly MacController _shell = MacController.Current;
    private readonly ListBox _nav = new() { Width = 170 };
    private readonly ContentControl _page = new();
    private readonly ContentControl _host = new();
    private readonly ScrollViewer _scroll;
    private readonly VaultPage _vaultPage = new() { Margin = new Thickness(16) };
    private bool _vaultShown;
    private readonly TextBlock _status = Ui.Dim("");
    private readonly DispatcherTimer _statusClear = new() { Interval = TimeSpan.FromSeconds(6) };
    private bool _allowClose;
    private MainPage _current = MainPage.Vault;

    public MainWindow()
    {
        Title = MacStrings.WindowTitle;
        Width = 960;
        Height = 580;
        MinWidth = 640;
        MinHeight = 440;
        // Same order as MainPage.
        _nav.ItemsSource = new[] { Strings.NavVault, Strings.NavSettings };
        _nav.SelectedIndex = 0;
        _nav.SelectionChanged += (_, _) => { if (_nav.SelectedIndex >= 0) Show((MainPage)_nav.SelectedIndex); };

        var grid = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*"), RowDefinitions = new RowDefinitions("*,Auto") };
        grid.Children.Add(new Border { Child = _nav, Padding = new Thickness(8), [Grid.RowSpanProperty] = 2 });
        // Ordinary pages scroll; the vault page fills the height (its lists scroll by themselves).
        _scroll = new ScrollViewer { Content = _page, Padding = new Thickness(16) };
        _host.Content = _scroll;
        _host[Grid.ColumnProperty] = 1;
        grid.Children.Add(_host);
        grid.Children.Add(new Border { Child = _status, Padding = new Thickness(16, 6), [Grid.ColumnProperty] = 1, [Grid.RowProperty] = 1 });
        Content = grid;

        _statusClear.Tick += (_, _) => { _statusClear.Stop(); _status.Text = ""; };
        _shell.StateChanged += () => { if (IsVisible) Render(); };
        _shell.StatusMessage += ShowStatus;
        Closing += (_, e) =>
        {
            if (_allowClose) return;
            e.Cancel = true; // hide only: the menu bar item keeps running
            Hide();
        };
        // Shown / hidden / minimised: the vault page masks its secrets and drops the capture exclusion when not on screen.
        PropertyChanged += (_, e) => { if (e.Property == IsVisibleProperty || e.Property == WindowStateProperty) UpdateVaultShown(); };
        _shell.Main = this;
        Render();
    }

    public void AllowClose() => _allowClose = true;

    public void Show(MainPage page)
    {
        _current = page;
        if (_nav.SelectedIndex != (int)page) _nav.SelectedIndex = (int)page;
        Render();
        UpdateVaultShown();
    }

    /// <summary>Tells the vault page when it comes on screen or goes away (page switch, window hidden or minimised).</summary>
    private void UpdateVaultShown()
    {
        var shown = IsVisible && WindowState != WindowState.Minimized && _current == MainPage.Vault;
        if (shown == _vaultShown) return;
        _vaultShown = shown;
        if (shown) _ = _vaultPage.OnShownAsync();
        else _vaultPage.OnHidden();
    }

    public void ShowStatus(string text)
    {
        _status.Text = text;
        _statusClear.Stop();
        _statusClear.Start();
    }

    private void Report(string? error)
    {
        if (error is not null) ShowStatus(error);
    }

    private void Render()
    {
        if (_current == MainPage.Vault)
        {
            _host.Content = _vaultPage; // one instance: it keeps its own state and is never rebuilt
            return;
        }
        _host.Content = _scroll;
        _page.Content = SettingsPage();
    }

    // ---- settings ------------------------------------------------------------------------

    private Control SettingsPage()
    {
        var autoStart = new ToggleSwitch { IsChecked = _shell.AutoStartEnabled, OnContent = "", OffContent = "", IsEnabled = MacPaths.BundlePath is not null };
        autoStart.IsCheckedChanged += async (_, _) =>
        {
            if ((autoStart.IsChecked ?? false) != _shell.AutoStartEnabled) Report(await _shell.SetAutoStartAsync(autoStart.IsChecked ?? false));
        };
        var notifications = new ToggleSwitch { IsChecked = _shell.Prefs.Notifications, OnContent = "", OffContent = "" };
        notifications.IsCheckedChanged += (_, _) =>
        {
            if ((notifications.IsChecked ?? false) != _shell.Prefs.Notifications) _shell.SetNotifications(notifications.IsChecked ?? false);
        };

        var u = _shell.Update;
        var updateButton = Ui.Button(UpdateView.ButtonText(u), async () => Report(await _shell.UpdateActionAsync(u.ButtonAction)), u.ButtonAction != UpdateInfo.Action.None);
        var autoUpdate = new CheckBox { Content = Strings.UpdateAuto, IsChecked = u.Auto, IsEnabled = u.Enabled };
        autoUpdate.IsCheckedChanged += async (_, _) =>
        {
            if ((autoUpdate.IsChecked ?? false) != _shell.Update.Auto) Report(await _shell.SetAutoUpdateAsync(autoUpdate.IsChecked ?? false));
        };

        return Ui.Column(14,
            Ui.Heading(Strings.SettingsTitle),
            Ui.Card(Ui.Column(10,
                Ui.Row(autoStart, Ui.Text(MacStrings.SettingsAutoStart).With(t => t.VerticalAlignment = VerticalAlignment.Center)),
                Ui.Row(notifications, Ui.Column(2, Ui.Text(Strings.SettingsNotifications), Ui.Dim(Strings.SettingsNotificationsHint))))),
            Ui.Card(Ui.Column(8,
                Ui.Text(Strings.SettingsUpdateTitle, 14, FontWeight.SemiBold),
                Ui.Text(UpdateView.Text(u)),
                Ui.Row(updateButton, autoUpdate))),
            Ui.Card(Ui.Column(4,
                Ui.Text(Strings.SettingsAboutTitle, 14, FontWeight.SemiBold),
                Ui.Dim(Strings.AboutVersion(BuildInfo.VersionName)),
                Ui.Dim(_shell.DeviceName),
                Ui.Button(MacStrings.SettingsOpenLogs, () => Report(MacOpen.Folder(MacPaths.LogDir))))));
    }
}
