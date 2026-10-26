using System.ComponentModel;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows;

/// <summary>Navigation shell: banners on top, one page at a time, a status line at the bottom. Closing only hides it.</summary>
public partial class MainWindow : Window
{
    internal static MainWindow? Instance { get; private set; }

    private readonly ShellController _shell = ShellController.Current;
    private readonly DispatcherTimer _statusTimer = new() { Interval = TimeSpan.FromSeconds(8) };
    private bool _shownHideHint;

    public MainWindow()
    {
        InitializeComponent();
        Instance = this;
        Title = Strings.AppName;

        foreach (var label in new[] { Strings.NavDevices, Strings.NavInbox, Strings.NavTransfers, Strings.NavVault, Strings.NavSettings, Strings.NavDiagnostics })
            Nav.Items.Add(new ListBoxItem { Content = label, Padding = new Thickness(16, 10, 8, 10) });

        DevicesPage.Attach(_shell);
        InboxPage.Attach(_shell);
        TransfersPage.Attach(_shell);
        VaultPage.Attach(_shell);
        SettingsPage.Attach(_shell);
        DiagnosticsPage.Attach(_shell);

        _statusTimer.Tick += (_, _) => { _statusTimer.Stop(); StatusText.Text = ""; };
        _shell.StateChanged += RefreshChrome;
        _shell.StatusMessage += ShowStatus;
        Nav.SelectedIndex = (int)MainPage.Devices;
        RefreshChrome();
    }

    internal void ShowPage(MainPage page) => Nav.SelectedIndex = (int)page;

    private void ShowStatus(string text)
    {
        StatusText.Text = text;
        _statusTimer.Stop();
        _statusTimer.Start();
    }

    /// <summary>Banners and the device line follow the shell state.</summary>
    private void RefreshChrome()
    {
        var warning = _shell.Warnings.Banner;
        WarningBanner.Visibility = warning is null ? Visibility.Collapsed : Visibility.Visible;
        WarningText.Text = warning ?? "";

        var update = _shell.Update;
        var actionable = UpdateView.IsActionable(update);
        UpdateBanner.Visibility = actionable ? Visibility.Visible : Visibility.Collapsed;
        if (actionable)
        {
            UpdateBannerText.Text = update.State == "ready" ? Strings.UpdateBannerReady(update.LatestName) : Strings.UpdateBannerAvailable(update.LatestName);
            UpdateBannerButton.Content = Strings.NavSettings;
        }

        DeviceText.Text = _shell.CoreRunning
            ? $"{_shell.DeviceName}  ·  {_shell.DeviceId}"
            : Strings.StatusStarting;
    }

    private void UpdateBannerButton_Click(object sender, RoutedEventArgs e) => ShowPage(MainPage.Settings);

    private void Nav_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        var page = (MainPage)Math.Max(0, Nav.SelectedIndex);
        DevicesPage.Visibility = page == MainPage.Devices ? Visibility.Visible : Visibility.Collapsed;
        InboxPage.Visibility = page == MainPage.Inbox ? Visibility.Visible : Visibility.Collapsed;
        TransfersPage.Visibility = page == MainPage.Transfers ? Visibility.Visible : Visibility.Collapsed;
        VaultPage.Visibility = page == MainPage.Vault ? Visibility.Visible : Visibility.Collapsed;
        SettingsPage.Visibility = page == MainPage.Settings ? Visibility.Visible : Visibility.Collapsed;
        DiagnosticsPage.Visibility = page == MainPage.Diagnostics ? Visibility.Visible : Visibility.Collapsed;

        if (page != MainPage.Vault) VaultPage.OnHidden(); // screen-capture exclusion only while the vault page shows

        switch (page)
        {
            case MainPage.Vault: VaultPage.OnShown(); break;
            case MainPage.Inbox: InboxPage.OnShown(); break;
            case MainPage.Transfers: TransfersPage.OnShown(); break;
            case MainPage.Settings: SettingsPage.OnShown(); break;
            case MainPage.Diagnostics: DiagnosticsPage.OnShown(); break;
        }
    }

    private void Window_Closing(object? sender, CancelEventArgs e)
    {
        if (_shell.IsExiting) return;
        e.Cancel = true;   // the X button hides to the tray; "退出" in the tray menu really exits
        Hide();
        if (!_shownHideHint)
        {
            _shownHideHint = true;
            _shell.Balloon(Strings.StillRunningTitle, Strings.StillRunningText);
        }
    }
}
