using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Shapes;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Media;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using ClipBridge.Windows.Views;
using Lumen.UI.Controls;
using NotesPage = ClipBridge.Windows.Views.NotesPage;

namespace ClipBridge.Windows;

/// <summary>
/// The navigation shell: Lumen's AppShell with a live sidebar (badges for the inbox, transfers and warnings),
/// the sync state and pairing in the top bar, warnings and updates as banners above every page, and toasts
/// for short messages. Closing only hides it to the tray.
/// </summary>
public partial class MainWindow : Window
{
    internal static MainWindow? Instance { get; private set; }

    private readonly ShellController _shell;
    private readonly Dictionary<MainPage, NavPage> _pages = new();
    private readonly BannerModel _banners;
    private readonly DevicesPage _devices;
    private readonly InboxPage _inbox;
    private readonly TransfersPage _transfers;
    private readonly VaultPage _vault;
    private readonly NotesPage _notes;
    private readonly SettingsPage _settings;
    private readonly DiagnosticsPage _diagnostics;
    private MainPage _current = MainPage.Devices;
    private bool _shownHideHint;
    private bool _handleReady;

    // The XAML loader needs a parameterless constructor (designer); the app always passes the controller.
    public MainWindow() : this(ShellController.Current) { }

    internal MainWindow(ShellController shell)
    {
        _shell = shell;
        InitializeComponent();
        Instance = this;
        Title = Strings.AppName;
        Icon = AppIconImage.Create();
        _banners = new BannerModel(shell);

        _devices = new DevicesPage(shell);
        _inbox = new InboxPage(shell);
        _transfers = new TransfersPage(shell);
        _vault = new VaultPage(shell);
        _notes = new NotesPage(shell);
        _settings = new SettingsPage(shell);
        _diagnostics = new DiagnosticsPage(shell);

        var items = new List<NavPage>
        {
            NavPage.Group(Strings.NavGroupSync),
            Add(MainPage.Devices, Strings.NavDevices, AppIcons.Monitor, _devices),
            Add(MainPage.Inbox, Strings.NavInbox, AppIcons.Of(Lumen.UI.Icons.IconKind.Inbox), _inbox),
            Add(MainPage.Transfers, Strings.NavTransfers, AppIcons.ArrowLeftRight, _transfers),
            NavPage.Group(Strings.NavGroupTools),
            Add(MainPage.Vault, Strings.NavVault, AppIcons.Of(Lumen.UI.Icons.IconKind.Lock), _vault),
            Add(MainPage.Notes, Strings.NavNotes, AppIcons.BookOpen, _notes),
            NavPage.Group(Strings.NavGroupSystem),
            Add(MainPage.Settings, Strings.NavSettings, AppIcons.Of(Lumen.UI.Icons.IconKind.Settings), _settings),
            Add(MainPage.Diagnostics, Strings.NavDiagnostics, AppIcons.Of(Lumen.UI.Icons.IconKind.Activity), _diagnostics),
        };
        Shell.NavItems = items;
        Shell.SelectedNavItem = _pages[MainPage.Devices];
        Shell.IsPaneOpen = shell.Prefs.SidebarOpen;
        Shell.PropertyChanged += OnShellPropertyChanged;

        PairButtonText.Text = Strings.BtnPairNewShort;
        ToolTip.SetTip(PairButton, Strings.PairButtonTip);
        ToolTip.SetTip(ThemeButton, Strings.ThemeToggleTip);
        ToolTip.SetTip(SyncChip, Strings.SyncChipTip);

        shell.StateChanged += RefreshChrome;
        shell.StatusMessage += ShowToast;
        shell.Inbox.CollectionChanged += (_, _) => RefreshCounts();
        shell.Transfers.Rows.CollectionChanged += (_, _) => RefreshCounts();
        Opened += OnOpened;
        Closing += OnClosing;
        PropertyChanged += (_, e) =>
        {
            if (e.Property != IsVisibleProperty) return;
            // Hidden to the tray: typed note text is saved, vault secrets are masked; shown again: the page refreshes.
            if (e.NewValue is false)
            {
                if (_current == MainPage.Notes) _notes.OnHidden();
                if (_current == MainPage.Vault) _vault.OnHidden();
            }
            else if (_current == MainPage.Vault) _vault.OnShown();
        };
        RefreshChrome();
    }

    private NavPage Add(MainPage page, string title, string icon, Control view)
    {
        var nav = new NavPage(page, title, icon, new PageFrame(view, _banners));
        _pages[page] = nav;
        return nav;
    }

    internal MainPage CurrentPage => _current;

    internal void ShowPage(MainPage page)
    {
        if (_pages.TryGetValue(page, out var nav)) Shell.SelectedNavItem = nav;
    }

    // ---- page switching -----------------------------------------------------------------

    private void OnShellPropertyChanged(object? sender, AvaloniaPropertyChangedEventArgs e)
    {
        if (e.Property == AppShell.SelectedNavItemProperty && Shell.SelectedNavItem is NavPage { IsHeader: false } nav)
            OnPageChanged(nav.Page);
        else if (e.Property == AppShell.IsPaneOpenProperty && _shell.Prefs.SidebarOpen != Shell.IsPaneOpen)
        {
            _shell.Prefs.SidebarOpen = Shell.IsPaneOpen;
            _shell.Prefs.Save(App.DataDir);
        }
    }

    private void OnPageChanged(MainPage page)
    {
        var previous = _current;
        _current = page;
        if (previous == MainPage.Vault && page != MainPage.Vault) _vault.OnHidden(); // capture exclusion only while it shows
        if (previous == MainPage.Notes && page != MainPage.Notes) _notes.OnHidden();

        switch (page)
        {
            case MainPage.Vault: _vault.OnShown(); break;
            case MainPage.Notes: _notes.OnShown(); break;
            case MainPage.Inbox: _inbox.OnShown(); break;
            case MainPage.Transfers: _transfers.OnShown(); break;
            case MainPage.Settings: _settings.OnShown(); break;
            case MainPage.Diagnostics: _diagnostics.OnShown(); break;
        }
    }

    protected override void OnKeyDown(KeyEventArgs e)
    {
        base.OnKeyDown(e);
        if (e.Handled || e.KeyModifiers != KeyModifiers.Control) return;
        MainPage? target = e.Key switch
        {
            Key.D1 => MainPage.Devices,
            Key.D2 => MainPage.Inbox,
            Key.D3 => MainPage.Transfers,
            Key.D4 => MainPage.Vault,
            Key.D5 => MainPage.Notes,
            Key.OemComma => MainPage.Settings,
            _ => null,
        };
        if (target is { } page)
        {
            ShowPage(page);
            e.Handled = true;
        }
        else if (e.Key == Key.F && CurrentView() is ISearchablePage searchable)
        {
            searchable.FocusSearch();
            e.Handled = true;
        }
    }

    private Control? CurrentView() => _current switch
    {
        MainPage.Inbox => _inbox,
        MainPage.Vault => _vault,
        MainPage.Notes => _notes,
        MainPage.Transfers => _transfers,
        _ => null,
    };

    // ---- chrome: top bar, sidebar footer, badges, banners -------------------------------------

    /// <summary>Banners, the sync chip and the device block follow the shell state.</summary>
    private void RefreshChrome()
    {
        _banners.Refresh();

        var s = _shell.Settings;
        var pausedUntil = s.PausedUntil > 0 ? DateTimeOffset.FromUnixTimeSeconds(Math.Clamp(s.PausedUntil, 0, 253402300799)) : (DateTimeOffset?)null;
        var paused = pausedUntil is { } p && p > DateTimeOffset.UtcNow;
        if (!_shell.CoreRunning)
        {
            SyncText.Text = Strings.StatusStarting;
            SetBrush(SyncDot, Ellipse.FillProperty, "Lumen.Brush.TextTertiary");
        }
        else if (paused)
        {
            SyncText.Text = Strings.SyncChipPaused(pausedUntil!.Value.LocalDateTime.ToString("HH:mm"));
            SetBrush(SyncDot, Ellipse.FillProperty, "Lumen.Brush.Warning");
        }
        else if (s.Capture)
        {
            SyncText.Text = Strings.SyncChipOn;
            SetBrush(SyncDot, Ellipse.FillProperty, "Lumen.Brush.Success");
        }
        else
        {
            SyncText.Text = Strings.SyncChipOff;
            SetBrush(SyncDot, Ellipse.FillProperty, "Lumen.Brush.TextTertiary");
        }

        var online = _shell.Peers.Count(p => p.IsConnected);
        var live = _shell.Peers.Count(p => !p.IsRevoked);
        DeviceText.Text = _shell.CoreRunning ? _shell.DeviceName : Strings.StatusStarting;
        DeviceDetail.Text = !_shell.CoreRunning ? "" : _shell.Warnings.Any ? Strings.FooterWarning
            : live == 0 ? Strings.FooterNoPeers : Strings.FooterOnline(online, live);
        SetBrush(DeviceDot, Ellipse.FillProperty, !_shell.CoreRunning ? "Lumen.Brush.TextTertiary"
            : _shell.Warnings.Any ? "Lumen.Brush.Warning" : online > 0 ? "Lumen.Brush.Success" : "Lumen.Brush.TextTertiary");
        Shell.Subtitle = _shell.CoreRunning && _shell.FingerprintPrefix.Length > 0 ? Strings.ShellSubtitle(_shell.FingerprintPrefix) : null;

        RefreshCounts();
    }

    private void RefreshCounts()
    {
        _pages[MainPage.Inbox].Count = _shell.Inbox.Count(r => r.CanDismiss);
        _pages[MainPage.Transfers].Count = _shell.Transfers.Rows.Count(r => !r.IsTerminal);
        _pages[MainPage.Diagnostics].Count = _shell.Warnings.Active.Count;
    }

    private static void SetBrush(StyledElement target, AvaloniaProperty property, string key) =>
        target[!property] = target.GetResourceObservable(key).ToBinding();

    private void ShowToast(string text, StatusKind kind)
    {
        if (!IsVisible) return;
        var severity = kind switch
        {
            StatusKind.Success => Severity.Success,
            StatusKind.Warning => Severity.Warning,
            StatusKind.Error => Severity.Danger,
            _ => Severity.Info,
        };
        Toast.Show(text, null, severity, kind == StatusKind.Error ? TimeSpan.FromSeconds(7) : null, anchor: this);
    }

    // ---- top bar actions ------------------------------------------------------------------

    private void PairButton_Click(object? sender, RoutedEventArgs e) => _shell.ShowPairListen();

    private async void SyncChip_Click(object? sender, RoutedEventArgs e)
    {
        var s = _shell.Settings;
        if (s.PausedUntil > DateTimeOffset.UtcNow.ToUnixTimeSeconds())
        {
            if (await _shell.SetPausedAsync(0) is null) ShowToast(Strings.SyncResumed, StatusKind.Success);
            return;
        }

        var menu = new MenuFlyout { Placement = PlacementMode.BottomEdgeAlignedRight };
        void Item(string text, Func<Task> run)
        {
            var item = new MenuItem { Header = text };
            item.Click += async (_, _) => await run();
            menu.Items.Add(item);
        }
        Item(s.Capture ? Strings.SyncTurnOff : Strings.SyncTurnOn, async () =>
        {
            if (await _shell.SetCaptureAsync(!s.Capture) is { } error) ShowToast(error, StatusKind.Error);
        });
        menu.Items.Add(new Separator());
        foreach (var (minutes, label) in new[] { (15, Strings.TrayPause15), (60, Strings.TrayPause60), (240, Strings.TrayPause240) })
            Item(Strings.SyncPauseFor(label), async () =>
            {
                if (await _shell.SetPausedAsync(minutes) is null) ShowToast(Strings.SyncPausedToast(label), StatusKind.Info);
            });
        menu.ShowAt(SyncChip);
    }

    // ---- window lifetime --------------------------------------------------------------------

    private void OnOpened(object? sender, EventArgs e)
    {
        if (_handleReady) return;
        _handleReady = true;
        _shell.ApplyCaptureExclusion(); // the vault page may already be selected when the window first appears
    }

    private void OnClosing(object? sender, WindowClosingEventArgs e)
    {
        if (_shell.IsExiting || e.CloseReason is WindowCloseReason.ApplicationShutdown or WindowCloseReason.OSShutdown) return;
        e.Cancel = true;   // the X button hides to the tray; "退出" in the tray menu really exits
        Hide();
        if (!_shownHideHint)
        {
            _shownHideHint = true;
            _shell.Balloon(Strings.StillRunningTitle, Strings.StillRunningText);
        }
    }
}
