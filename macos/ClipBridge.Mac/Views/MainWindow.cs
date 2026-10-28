using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Platform.Storage;
using Avalonia.Threading;
using ClipBridge.Mac.Platform;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Update;

namespace ClipBridge.Mac.Views;

/// <summary>
/// Sidebar (devices, inbox, transfers, vault, settings) and the selected page. Closing hides the window; the app
/// stays in the menu bar. Pages are rebuilt from the controller's snapshots when they change.
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
    private readonly DispatcherTimer _transfersTick = new() { Interval = TimeSpan.FromMilliseconds(300) };
    private bool _transfersDirty;
    private bool _allowClose;
    private MainPage _current = MainPage.Devices;

    public MainWindow()
    {
        Title = MacStrings.WindowTitle;
        Width = 960;
        Height = 580;
        MinWidth = 640;
        MinHeight = 440;
        // Same order as MainPage.
        _nav.ItemsSource = new[] { Strings.NavDevices, Strings.NavInbox, Strings.NavTransfers, Strings.NavVault, Strings.NavSettings };
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
        _transfersTick.Tick += (_, _) =>
        {
            if (!_transfersDirty) return;
            _transfersDirty = false;
            if (_current == MainPage.Transfers && IsVisible) Render();
        };
        _transfersTick.Start();
        _shell.StateChanged += () => { if (IsVisible) Render(); };
        _shell.Inbox.CollectionChanged += (_, _) => { if (_current == MainPage.Inbox && IsVisible) Render(); };
        _shell.TransfersChanged += () => _transfersDirty = true;
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
        _page.Content = _current switch
        {
            MainPage.Inbox => InboxPage(),
            MainPage.Transfers => TransfersPage(),
            MainPage.Settings => SettingsPage(),
            _ => DevicesPage(),
        };
    }

    // ---- devices -------------------------------------------------------------------------

    private Control DevicesPage()
    {
        var page = Ui.Column(12,
            Ui.Heading(Strings.DevicesTitle),
            Ui.Row(
                Ui.Button(Strings.BtnPairNew, PairListenWindow.ShowOrActivate),
                Ui.Button(Strings.BtnJoinPair, PairJoinWindow.ShowOrActivate),
                Ui.Button(MacStrings.MenuSendClipboard, _shell.SendCurrentClipboard),
                Ui.Button(Strings.BtnSendText, SendTextAsync)));
        if (!_shell.CoreRunning) page.Children.Add(Ui.Text(Strings.StatusStarting));
        foreach (var w in WarningTexts()) page.Children.Add(Ui.Text(w).With(t => t.Foreground = Ui.ErrorBrush));
        if (_shell.Peers.Count == 0) page.Children.Add(Ui.Dim(Strings.DevicesEmpty));
        else page.Children.Add(Ui.Dim(MacStrings.DropHere));
        foreach (var peer in _shell.Peers) page.Children.Add(PeerCard(peer));
        return page;
    }

    private IEnumerable<string> WarningTexts()
    {
        if (_shell.Warnings.Has("NO_TAILNET")) yield return Strings.NoTailnetBanner;
        if (_shell.Warnings.Has("LISTEN_FAILED")) yield return Strings.ListenFailedBanner;
    }

    private Control PeerCard(PeerItem peer)
    {
        // Radio buttons rather than a ComboBox: three choices, all visible at once.
        var mode = Ui.Row();
        var labels = new[] { Strings.ModeNotify, Strings.ModeManual, Strings.ModeAuto };
        for (var i = 0; i < SettingsModel.Modes.Length; i++)
        {
            var value = SettingsModel.Modes[i];
            var radio = new RadioButton
            {
                Content = labels[i], GroupName = "mode-" + peer.DeviceId, IsChecked = value == peer.ApplyMode,
                IsEnabled = peer.CanChangeSettings, VerticalAlignment = VerticalAlignment.Center,
            };
            radio.IsCheckedChanged += async (_, _) =>
            {
                if (radio.IsChecked == true && value != peer.ApplyMode) Report(await _shell.SetPeerModeAsync(peer, value));
            };
            mode.Children.Add(radio);
        }
        var send = new CheckBox { Content = Strings.DeviceAutoSend, IsChecked = peer.SendEnabled, IsEnabled = peer.CanChangeSettings };
        send.IsCheckedChanged += async (_, _) =>
        {
            if ((send.IsChecked ?? false) != peer.SendEnabled) Report(await _shell.SetPeerSendAsync(peer, send.IsChecked ?? false));
        };

        var buttons = Ui.Row(
            Ui.Button(Strings.BtnSendText, async () =>
            {
                if (await Ui.PromptAsync(this, Strings.SendTextOneTitle(peer.DisplayName), Strings.SendTextOnePrompt) is { } text)
                    Report(await _shell.SendTextAsync(text, new[] { peer.DeviceId }));
            }, peer.CanSendFile),
            Ui.Button(Strings.BtnSendClipboardOne, () => _shell.SendClipboardTo(peer), peer.CanSendFile),
            Ui.Button(Strings.BtnSendFiles, async () =>
            {
                var files = await Ui.PickFilesAsync(this, Strings.FilePickTitle);
                if (files.Count > 0) Report(await _shell.SendFilesAsync(peer, files));
            }, peer.CanSendFile),
            Ui.Button(Strings.BtnRename, async () =>
            {
                if (await Ui.PromptAsync(this, Strings.RenameTitle, Strings.RenamePrompt, peer.DisplayName) is { } name)
                    Report(await _shell.RenamePeerAsync(peer, name));
            }),
            Ui.Button(Strings.BtnReconnect, async () => Report(await _shell.ReconnectPeerAsync(peer)), peer.CanReconnect),
            Ui.Button(Strings.BtnUnpair, async () =>
            {
                if (await Ui.ConfirmAsync(this, Strings.ConfirmTitle, Strings.ConfirmUnpair(peer.DisplayName), Strings.BtnUnpair))
                    Report(await _shell.RemovePeerAsync(peer));
            }));

        var state = peer.State == "offline" && peer.Pending > 0 ? Strings.OfflineWithPending(peer.Pending) : peer.StateText;
        var card = Ui.Card(Ui.Column(6,
            Ui.Text(peer.DisplayName, 15, FontWeight.SemiBold),
            Ui.Dim($"{peer.PlatformText} · {state}" + (peer.IsConnected ? "" : " · " + peer.LastSeenText)),
            Ui.Row(Ui.Text(MacStrings.ModeLabel).With(t => t.VerticalAlignment = VerticalAlignment.Center), mode),
            send,
            buttons));

        // Drop files on a device to send them.
        DragDrop.SetAllowDrop(card, peer.CanSendFile);
        card.AddHandler(DragDrop.DropEvent, async (_, e) =>
        {
            var paths = DroppedPaths(e);
            if (paths.Count > 0) Report(await _shell.SendFilesAsync(peer, paths));
        });
        return card;
    }

    private static List<string> DroppedPaths(DragEventArgs e) =>
        e.DataTransfer.TryGetFiles()?.Select(f => f.TryGetLocalPath()).OfType<string>().ToList() ?? new List<string>();

    /// <summary>Top "发送文字…": typed text to every device online now.</summary>
    private async Task SendTextAsync()
    {
        if (await Ui.PromptAsync(this, Strings.SendTextAllTitle, Strings.SendTextAllPrompt) is { } text)
            Report(await _shell.SendTextAsync(text, null));
    }

    // ---- inbox ---------------------------------------------------------------------------

    private Control InboxPage()
    {
        var page = Ui.Column(10, Ui.Row(Ui.Heading(Strings.InboxTitle), Ui.Button(Strings.BtnClear, async () =>
        {
            if (await Ui.ConfirmAsync(this, Strings.ConfirmTitle, Strings.ConfirmClearInbox, Strings.BtnClear)) Report(await _shell.ClearInboxAsync());
        }, _shell.Inbox.Count > 0)));
        if (_shell.Inbox.Count == 0) page.Children.Add(Ui.Dim(Strings.InboxEmpty));
        foreach (var row in _shell.Inbox)
        {
            var preview = Ui.Text(row.Preview, 13);
            var buttons = Ui.Row(Ui.Button(Strings.BtnCopy, async () => Report(await _shell.CopyInboxItemAsync(row)), row.CanCopy));
            if (row.CanReveal) buttons.Children.Add(Ui.Button(row.RevealText, () => { row.ToggleReveal(); Render(); }));
            if (row.CanDismiss) buttons.Children.Add(Ui.Button(Strings.BtnIgnore, async () => { Report(await _shell.DismissInboxItemAsync(row)); Render(); }));
            page.Children.Add(Ui.Card(Ui.Column(6, Ui.Dim($"{row.FromText} · {row.TimeText} · {row.StateText}"), preview, buttons)));
        }
        return page;
    }

    // ---- transfers -----------------------------------------------------------------------

    private Control TransfersPage()
    {
        var page = Ui.Column(10, Ui.Row(Ui.Heading(Strings.TransfersTitle),
            Ui.Button(Strings.BtnOpenFolder, () => Report(MacOpen.Folder(_shell.Settings.DownloadDir)))));
        var rows = _shell.Transfers.Rows;
        if (rows.Count == 0) page.Children.Add(Ui.Dim(Strings.TransfersEmpty));
        foreach (var r in rows)
        {
            var col = Ui.Column(6,
                Ui.Text($"{r.DirectionGlyph} {r.DisplayName}", 14, FontWeight.SemiBold),
                Ui.Dim(string.Join(" · ", new[] { r.DirectionText + " " + r.PeerName, r.SizeText, r.StateText, r.ProgressText, r.SpeedText, r.EtaText }.Where(s => s.Length > 0))));
            if (r.ShowProgress) col.Children.Add(new ProgressBar { Minimum = 0, Maximum = 1, Value = r.Progress, Height = 6 });
            var buttons = Ui.Row();
            if (r.CanAccept) buttons.Children.Add(Ui.Button(Strings.BtnAccept, async () => Report(await _shell.AcceptTransferAsync(r.TransferId, null))));
            if (r.CanReject) buttons.Children.Add(Ui.Button(Strings.BtnReject, async () => Report(await _shell.RejectTransferAsync(r.TransferId))));
            if (r.CanCancel) buttons.Children.Add(Ui.Button(Strings.BtnCancel, async () => Report(await _shell.CancelTransferAsync(r.TransferId))));
            if (r.CanOpen)
            {
                buttons.Children.Add(Ui.Button(Strings.BtnOpenFile, () => Report(MacOpen.File(r.Path))));
                buttons.Children.Add(Ui.Button(Strings.BtnOpenFolder, () => Report(MacOpen.Reveal(r.Path))));
            }
            if (buttons.Children.Count > 0) col.Children.Add(buttons);
            page.Children.Add(Ui.Card(col));
        }
        return page;
    }

    // ---- settings ------------------------------------------------------------------------

    private Control SettingsPage()
    {
        var s = _shell.Settings;
        var sync = new ToggleSwitch { IsChecked = s.Capture, OnContent = "", OffContent = "" };
        sync.IsCheckedChanged += async (_, _) =>
        {
            if ((sync.IsChecked ?? false) != _shell.Settings.Capture) Report(await _shell.SetCaptureAsync(sync.IsChecked ?? false));
        };
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
        var download = Ui.Dim(s.DownloadDir);
        var chooseDir = Ui.Button(MacStrings.SettingsChooseFolder, async () =>
        {
            if (await Ui.PickFolderAsync(this, Strings.SettingsDownloadDir) is { } dir)
                Report(await _shell.SaveSettingsAsync(_shell.Settings with { DownloadDir = dir }));
        });

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
                Ui.Row(sync, Ui.Column(2, Ui.Text(Strings.SettingsSync), Ui.Dim(Strings.SettingsSyncHint))),
                Ui.Row(autoStart, Ui.Text(MacStrings.SettingsAutoStart).With(t => t.VerticalAlignment = VerticalAlignment.Center)),
                Ui.Row(notifications, Ui.Column(2, Ui.Text(Strings.SettingsNotifications), Ui.Dim(Strings.SettingsNotificationsHint))),
                Ui.Column(4, Ui.Text(Strings.SettingsDownloadDir), Ui.Row(download, chooseDir)))),
            Ui.Card(Ui.Column(8,
                Ui.Text(Strings.SettingsUpdateTitle, 14, FontWeight.SemiBold),
                Ui.Text(UpdateView.Text(u)),
                Ui.Row(updateButton, autoUpdate))),
            Ui.Card(Ui.Column(4,
                Ui.Text(Strings.SettingsAboutTitle, 14, FontWeight.SemiBold),
                Ui.Dim(Strings.AboutVersion(BuildInfo.VersionName)),
                Ui.Dim(_shell.DeviceName),
                Ui.Dim(Strings.AboutDeviceId(_shell.DeviceId)),
                Ui.Dim(Strings.AboutFingerprint(_shell.FingerprintPrefix)),
                Ui.Button(MacStrings.SettingsOpenLogs, () => Report(MacOpen.Folder(MacPaths.LogDir))))));
    }
}
