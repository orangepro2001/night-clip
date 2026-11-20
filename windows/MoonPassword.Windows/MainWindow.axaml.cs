using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Shapes;
using Avalonia.Input;
using Avalonia.Interactivity;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using MoonPassword.Windows.Views;
using Lumen.UI.Controls;

namespace MoonPassword.Windows;

/// <summary>
/// The navigation shell: Lumen's AppShell with the vault, settings and diagnostics, a lock button in the top bar,
/// warnings and updates as banners above every page, and toasts for short messages. Closing only hides it to the tray.
/// </summary>
public partial class MainWindow : Window
{
    internal static MainWindow? Instance { get; private set; }

    private readonly ShellController _shell;
    private readonly Dictionary<MainPage, NavPage> _pages = new();
    private readonly BannerModel _banners;
    private readonly VaultPage _vault;
    private readonly SettingsPage _settings;
    private readonly DiagnosticsPage _diagnostics;
    private MainPage _current = MainPage.Vault;
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
        // the moon mark in the shell's logo tile (drawn from Logic/BrandMark.cs, like the window and tray icons)
        Shell.Logo = new Avalonia.Controls.Shapes.Path
        {
            Data = AppIconImage.Mark(18 / 24.0, 0),
            Fill = Avalonia.Media.Brushes.White,
            Width = 18,
            Height = 18,
        };
        _banners = new BannerModel(shell);

        _vault = new VaultPage(shell);
        _settings = new SettingsPage(shell);
        _diagnostics = new DiagnosticsPage(shell);

        var items = new List<NavPage>
        {
            Add(MainPage.Vault, Strings.NavVault, AppIcons.Of(Lumen.UI.Icons.IconKind.Lock), _vault),
            NavPage.Group(Strings.NavGroupSystem),
            Add(MainPage.Settings, Strings.NavSettings, AppIcons.Of(Lumen.UI.Icons.IconKind.Settings), _settings),
            Add(MainPage.Diagnostics, Strings.NavDiagnostics, AppIcons.Of(Lumen.UI.Icons.IconKind.Activity), _diagnostics),
        };
        Shell.NavItems = items;
        Shell.SelectedNavItem = _pages[MainPage.Vault];
        Shell.IsPaneOpen = shell.Prefs.SidebarOpen;
        Shell.PropertyChanged += OnShellPropertyChanged;

        LockButtonText.Text = Strings.BtnLockVault;
        ToolTip.SetTip(LockButton, Strings.LockButtonTip);
        ToolTip.SetTip(ThemeButton, Strings.ThemeToggleTip);

        shell.StateChanged += RefreshChrome;
        shell.StatusMessage += ShowToast;
        Opened += OnOpened;
        Closing += OnClosing;
        PropertyChanged += (_, e) =>
        {
            if (e.Property != IsVisibleProperty || _current != MainPage.Vault) return;
            // Hidden to the tray: vault secrets are masked; shown again: the page refreshes.
            if (e.NewValue is false) _vault.OnHidden();
            else _vault.OnShown();
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

        switch (page)
        {
            case MainPage.Vault: _vault.OnShown(); break;
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
            Key.D1 => MainPage.Vault,
            Key.OemComma => MainPage.Settings,
            _ => null,
        };
        if (target is { } page)
        {
            ShowPage(page);
            e.Handled = true;
        }
        else if (e.Key == Key.F && _current == MainPage.Vault)
        {
            ((ISearchablePage)_vault).FocusSearch();
            e.Handled = true;
        }
        else if (e.Key == Key.L)
        {
            _ = _shell.LockVaultAsync();
            e.Handled = true;
        }
    }

    // ---- chrome: top bar, sidebar footer, banners -------------------------------------------

    /// <summary>Banners, the lock button and the device block follow the shell state.</summary>
    private void RefreshChrome()
    {
        _banners.Refresh();
        LockButton.IsVisible = _shell.VaultUnlocked;
        DeviceText.Text = _shell.CoreRunning ? _shell.DeviceName : Strings.StatusStarting;
        DeviceDetail.Text = !_shell.CoreRunning ? "" : _shell.VaultUnlocked ? Strings.FooterUnlocked : Strings.FooterLocked;
        SetBrush(DeviceDot, Ellipse.FillProperty, !_shell.CoreRunning ? "Lumen.Brush.TextTertiary"
            : _shell.VaultUnlocked ? "Lumen.Brush.Success" : "Lumen.Brush.TextTertiary");
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

    private void LockButton_Click(object? sender, RoutedEventArgs e) => _ = _shell.LockVaultAsync();

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
