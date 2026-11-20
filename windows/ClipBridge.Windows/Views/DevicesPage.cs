using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Platform.Storage;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace ClipBridge.Windows.Views;

/// <summary>
/// Paired devices: a quick-send box on top (typed text to every online device or one of them), then one row per
/// device with its state, the send actions and a settings dialog (name, receive policy, automatic sending, unpair).
/// Files dropped on a row are offered to that device.
/// </summary>
internal sealed class DevicesPage : UserControl
{
    private readonly ShellController _shell;
    private readonly StackPanel _rows = new() { Margin = new Thickness(0, 10, 0, -1) };
    private readonly Card _listCard;
    private readonly EmptyState _empty;
    private readonly TextBox _composer;
    private readonly ComboBox _target;
    private readonly Button _sendButton;
    private readonly TextBlock _composerHint;

    public DevicesPage(ShellController shell)
    {
        _shell = shell;

        // ---- header ---------------------------------------------------------------------
        var header = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
        header.Children.Add(Ui.Text(Strings.DevicesIntro, "secondary").With(t => t.VerticalAlignment = VerticalAlignment.Center));
        var actions = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        actions.Children.Add(Ui.Button(AppIcons.Paperclip, Strings.BtnSendFilesShort, () => _ = SendFlowAsync(null)));
        actions.Children.Add(Ui.Button(AppIcons.Scan, Strings.BtnJoinPairShort, () => _shell.ShowPairJoin()));
        Grid.SetColumn(actions, 1);
        header.Children.Add(actions);

        // ---- quick send -------------------------------------------------------------------
        _composer = new TextBox
        {
            AcceptsReturn = true,
            TextWrapping = TextWrapping.Wrap,
            MinHeight = 64,
            MaxHeight = 160,
            PlaceholderText = Strings.ComposerPlaceholder,
        };
        _composer.Classes.Add("filled");
        _composer.KeyDown += async (_, e) =>
        {
            if (e.Key == Key.Enter && e.KeyModifiers == KeyModifiers.Control)
            {
                e.Handled = true;
                await SendComposedAsync();
            }
        };
        _composer.TextChanged += (_, _) => UpdateComposer();
        _target = new ComboBox { MinWidth = 180, VerticalAlignment = VerticalAlignment.Center };
        _target.SelectionChanged += (_, _) => UpdateComposer();
        _sendButton = Ui.Button(AppIcons.Send, Strings.ComposerSend, () => _ = SendComposedAsync(), "primary");
        _composerHint = Ui.Text("", "caption", "tertiary").With(t => t.VerticalAlignment = VerticalAlignment.Center);
        var composerBar = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto"), Margin = new Thickness(0, 10, 0, 0) };
        composerBar.Children.Add(_target);
        var hintHost = new Border { Child = _composerHint, Margin = new Thickness(12, 0) };
        Grid.SetColumn(hintHost, 1);
        composerBar.Children.Add(hintHost);
        Grid.SetColumn(_sendButton, 2);
        composerBar.Children.Add(_sendButton);
        var composerCard = new Card
        {
            Header = Strings.ComposerTitle,
            Content = new StackPanel { Children = { _composer, composerBar } },
        };
        composerCard.Classes.Add("flat");

        // ---- device list -------------------------------------------------------------------
        _empty = new EmptyState
        {
            Icon = IconKind.Users,
            Title = Strings.DevicesEmptyTitle,
            Description = Strings.DevicesEmptyText,
            Action = new StackPanel
            {
                Orientation = Orientation.Horizontal,
                Spacing = 8,
                Children =
                {
                    Ui.Button(AppIcons.QrCode, Strings.BtnPairNewShort, () => _shell.ShowPairListen(), "primary"),
                    Ui.Button(AppIcons.Scan, Strings.BtnJoinPairShort, () => _shell.ShowPairJoin()),
                },
            },
        };
        _listCard = new Card { Header = Strings.DevicesTitle, Padding = new Thickness(0), Content = _rows, ClipToBounds = true };
        _listCard.Classes.Add("flat");

        var dropHint = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 6,
            Margin = new Thickness(4, -6, 0, 0),
            Children = { Ui.Icon(AppIcons.Paperclip, 13).With(i => i[!Icon.ForegroundProperty] = i.GetResourceObservable("Lumen.Brush.TextTertiary").ToBinding()), Ui.Text(Strings.DropFilesHint, "caption", "tertiary") },
        };

        Content = new ScrollViewer
        {
            Content = new StackPanel { Classes = { "page" }, Children = { header, composerCard, _listCard, _empty, dropHint } },
        };

        shell.Peers.CollectionChanged += (_, _) => Rebuild();
        shell.StateChanged += UpdateComposer;
        Rebuild();
    }

    // ---- list -------------------------------------------------------------------------------

    private void Rebuild()
    {
        _rows.Children.Clear();
        var peers = _shell.Peers.ToList();
        foreach (var peer in peers) _rows.Children.Add(BuildRow(peer));
        _listCard.IsVisible = peers.Count > 0;
        _empty.IsVisible = peers.Count == 0;
        _listCard.Description = Strings.DeviceCount(peers.Count(p => !p.IsRevoked), peers.Count(p => p.IsConnected));
        RebuildTargets();
        UpdateComposer();
    }

    private Control BuildRow(PeerItem peer)
    {
        var grid = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto") };
        grid.Children.Add(Ui.DeviceTile(peer));

        var title = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        title.Children.Add(new TextBlock { Text = peer.DisplayName, FontWeight = FontWeight.SemiBold, TextTrimming = TextTrimming.CharacterEllipsis, VerticalAlignment = VerticalAlignment.Center });
        title.Children.Add(Ui.StateBadge(peer));
        var info = new StackPanel { Spacing = 3, Margin = new Thickness(14, 0, 12, 0), VerticalAlignment = VerticalAlignment.Center, Children = { title } };
        info.Children.Add(Ui.Text(DetailLine(peer), "caption", "tertiary"));
        if (peer.PendingText.Length > 0) info.Children.Add(Ui.Text(peer.PendingText, "caption", "warning"));
        Grid.SetColumn(info, 1);
        grid.Children.Add(info);

        var buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 2, VerticalAlignment = VerticalAlignment.Center };
        if (peer.CanSendFile)
        {
            buttons.Children.Add(Ui.IconButton(AppIcons.Send, Strings.TipSendText, () => _ = SendTextToAsync(peer)));
            buttons.Children.Add(Ui.IconButton(AppIcons.Clipboard, Strings.BtnSendClipboardOne, () => _shell.SendClipboardTo(peer)));
            buttons.Children.Add(Ui.IconButton(AppIcons.Paperclip, Strings.TipSendFiles, () => _ = SendFlowAsync(peer)));
        }
        if (peer.IsUnpaired)
            buttons.Children.Add(Ui.Button(AppIcons.RotateCcw, Strings.RepairNowShort, () => _ = RepairAsync(peer), "soft", "sm").With(b => b.Margin = new Thickness(6, 0, 0, 0)));
        else if (peer.CanReconnect)
            buttons.Children.Add(Ui.IconButton(AppIcons.Of(IconKind.RefreshCw), Strings.BtnReconnect, () => _ = ReconnectAsync(peer)));
        if (peer.IsRevoked)
            buttons.Children.Add(Ui.Button(AppIcons.Of(IconKind.Trash), Strings.BtnForget, () => _ = ForgetAsync(peer), "danger-soft", "sm").With(b => b.Margin = new Thickness(6, 0, 0, 0)));
        if (peer.CanChangeSettings)
            buttons.Children.Add(Ui.IconButton(AppIcons.Of(IconKind.Settings), Strings.DeviceSettingsTip, () => _ = EditAsync(peer)));
        Grid.SetColumn(buttons, 2);
        grid.Children.Add(buttons);

        var row = new Border { Child = grid };
        row.Classes.Add("row");
        if (peer.CanSendFile) EnableDrop(row, peer);
        return row;
    }

    private static string DetailLine(PeerItem peer)
    {
        var parts = new List<string> { peer.PlatformText };
        if (!peer.IsConnected && !peer.IsRevoked) parts.Add(peer.LastSeenText);
        if (peer.CanChangeSettings)
        {
            parts.Add(Strings.ReceivePolicyShort(peer.ApplyModeText));
            if (!peer.SendEnabled) parts.Add(Strings.AutoSendOffShort);
        }
        return string.Join("  ·  ", parts.Where(p => p.Length > 0));
    }

    private void EnableDrop(Border row, PeerItem peer)
    {
        DragDrop.SetAllowDrop(row, true);
        row.AddHandler(DragDrop.DragEnterEvent, (_, e) =>
        {
            if (HasFiles(e)) row.Classes.Add("droptarget");
        });
        row.AddHandler(DragDrop.DragLeaveEvent, (_, _) => row.Classes.Remove("droptarget"));
        row.AddHandler(DragDrop.DragOverEvent, (_, e) =>
        {
            e.DragEffects = HasFiles(e) ? DragDropEffects.Copy : DragDropEffects.None;
            e.Handled = true;
        });
        row.AddHandler(DragDrop.DropEvent, async (_, e) =>
        {
            row.Classes.Remove("droptarget");
            e.Handled = true;
            var paths = e.DataTransfer.TryGetFiles()?.Select(f => f.TryGetLocalPath()).OfType<string>().ToList();
            if (paths is { Count: > 0 }) await SendPathsAsync(peer, paths);
        });
    }

    private static bool HasFiles(DragEventArgs e) => e.DataTransfer.Contains(DataFormat.File);

    // ---- quick send ---------------------------------------------------------------------------

    private sealed record Target(string Label, string? DeviceId)
    {
        public override string ToString() => Label;
    }

    private void RebuildTargets()
    {
        var selected = (_target.SelectedItem as Target)?.DeviceId;
        var targets = new List<Target> { new(Strings.ComposerAllOnline, null) };
        targets.AddRange(_shell.Peers.Where(p => p.CanSendFile).Select(p => new Target(p.DisplayName, p.DeviceId)));
        _target.ItemsSource = targets;
        _target.SelectedItem = targets.FirstOrDefault(t => t.DeviceId == selected) ?? targets[0];
    }

    private void UpdateComposer()
    {
        var online = _shell.Peers.Count(p => p.IsConnected);
        _composerHint.Text = (_target.SelectedItem as Target)?.DeviceId is null ? Strings.ComposerHintAll(online) : Strings.ComposerHintOne;
        _sendButton.IsEnabled = !string.IsNullOrWhiteSpace(_composer.Text);
    }

    private async Task SendComposedAsync()
    {
        var text = _composer.Text;
        if (string.IsNullOrWhiteSpace(text)) return;
        var deviceId = (_target.SelectedItem as Target)?.DeviceId;
        _sendButton.IsEnabled = false;
        var result = await _shell.SendTextAsync(text, deviceId is null ? null : new[] { deviceId });
        var failed = result is null || !result.StartsWith(Strings.SentPrefix, StringComparison.Ordinal);
        if (!failed) _composer.Text = "";
        if (result is not null) _shell.ShowStatus(result, failed ? StatusKind.Warning : StatusKind.Success);
        UpdateComposer();
    }

    private async Task SendTextToAsync(PeerItem peer)
    {
        var text = await Ui.PromptAsync(this, Strings.SendTextOneTitle(peer.DisplayName), Strings.SendTextOnePrompt, "",
            s => string.IsNullOrWhiteSpace(s) ? null : s, Strings.SendTextEmpty, Strings.ComposerSend, multiline: true);
        if (text is null) return;
        var result = await _shell.SendTextAsync(text, new[] { peer.DeviceId });
        if (result is not null)
            _shell.ShowStatus(result, result.StartsWith(Strings.SentPrefix, StringComparison.Ordinal) ? StatusKind.Success : StatusKind.Warning);
    }

    // ---- files -------------------------------------------------------------------------------

    /// <summary>Choose a device (unless given), choose files, offer them one by one.</summary>
    private async Task SendFlowAsync(PeerItem? preselected)
    {
        var peer = preselected;
        if (peer is null)
        {
            var candidates = _shell.Peers.Where(p => p.CanSendFile).ToList();
            if (candidates.Count == 0)
            {
                await Dialog.AlertAsync(Strings.FilePickPeerTitle, Strings.FileNoPeers, Strings.BtnOk, this);
                return;
            }
            peer = candidates.Count == 1 ? candidates[0] : await Ui.PickPeerAsync(this, candidates);
            if (peer is null) return;
        }

        var files = await Ui.PickFilesAsync(this, Strings.FilePickTitle);
        if (files.Count == 0) return;
        await SendPathsAsync(peer, files);
    }

    private async Task SendPathsAsync(PeerItem peer, IEnumerable<string> paths)
    {
        var error = await _shell.SendFilesAsync(peer, paths);
        if (error is not null) await Dialog.AlertAsync(Strings.FileFailedTitle, error, Strings.BtnOk, this);
        MainWindow.Instance?.ShowPage(MainPage.Transfers);
    }

    // ---- device actions -----------------------------------------------------------------------

    private async Task ReconnectAsync(PeerItem peer)
    {
        var message = await _shell.ReconnectPeerAsync(peer);
        _shell.ShowStatus(message ?? Strings.Reconnecting, message is null || message == Strings.ReconnectRefreshed ? StatusKind.Info : StatusKind.Error);
    }

    private async Task RepairAsync(PeerItem peer)
    {
        if (!await Dialog.ConfirmAsync(Strings.RepairNow, Strings.RepairHint, Strings.RepairNowShort, Strings.BtnCancel, anchor: this)) return;
        var error = await _shell.RemovePeerAsync(peer, forget: true);
        if (error is not null) { _shell.ShowStatus(error, StatusKind.Error); return; }
        _shell.ShowPairListen();
    }

    private async Task ForgetAsync(PeerItem peer)
    {
        if (!await Dialog.ConfirmAsync(Strings.ConfirmTitle, Strings.ConfirmForget(peer.DisplayName), Strings.BtnForget, Strings.BtnCancel, isDanger: true, anchor: this)) return;
        var error = await _shell.RemovePeerAsync(peer, forget: true);
        _shell.ShowStatus(error ?? Strings.ForgotDone, error is null ? StatusKind.Success : StatusKind.Error);
    }

    /// <summary>Name, receive policy and automatic sending in one dialog; unpairing at the bottom.</summary>
    private async Task EditAsync(PeerItem peer)
    {
        var name = new TextBox { Text = peer.DisplayName };
        var nameField = new FormField { Label = Strings.LabelName, Content = name };
        var modes = new SegmentedControl { Classes = { "block" } };
        foreach (var label in new[] { Strings.ModeNotify, Strings.ModeManual, Strings.ModeAuto }) modes.Items.Add(new SegmentedItem { Content = label });
        modes.SelectedIndex = Math.Max(0, Array.IndexOf(SettingsModel.Modes, peer.ApplyMode));
        var modeHint = Ui.Text("", "caption", "tertiary");
        void UpdateModeHint() => modeHint.Text = Strings.ModeHint(SettingsModel.Modes[Math.Max(0, modes.SelectedIndex)]);
        modes.SelectionChanged += (_, _) => UpdateModeHint();
        UpdateModeHint();
        var modeField = new FormField { Label = Strings.LabelReceivePolicy, Content = new StackPanel { Spacing = 6, Children = { modes, modeHint } } };
        var autoSend = new ToggleSwitch { IsChecked = peer.SendEnabled };
        var autoRow = SettingRow(Strings.DeviceAutoSend, Strings.DeviceAutoSendTip, autoSend);

        var save = new Button { Content = Strings.BtnSave, IsDefault = true, MinWidth = 88 };
        save.Classes.Add("primary");
        var cancel = new Button { Content = Strings.BtnCancel, IsCancel = true, MinWidth = 88 };
        var unpair = Ui.Button(AppIcons.Of(IconKind.Trash), Strings.BtnUnpair, () => { }, "danger-soft");
        var footer = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto,Auto"), Margin = new Thickness(0, 8, 0, 0) };
        footer.Children.Add(unpair);
        Grid.SetColumn(cancel, 2);
        footer.Children.Add(cancel);
        Grid.SetColumn(save, 3);
        save.Margin = new Thickness(8, 0, 0, 0);
        footer.Children.Add(save);

        var panel = new StackPanel
        {
            Spacing = 16,
            Width = 420,
            Children =
            {
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 12, Children = { Ui.DeviceTile(peer), new StackPanel { VerticalAlignment = VerticalAlignment.Center, Children = { Ui.Text(Strings.DeviceSettingsTitle, "subtitle"), Ui.Text(peer.PlatformText + "  ·  " + peer.StateText, "caption", "tertiary") } } } },
                nameField, modeField, autoRow, footer,
            },
        };

        save.Click += (_, _) =>
        {
            if (PeerItem.NormalizeName(name.Text) is null) { nameField.Error = Strings.NameInvalid; return; }
            Dialog.Close(panel, "save");
        };
        cancel.Click += (_, _) => Dialog.Close(panel);
        unpair.Click += (_, _) => Dialog.Close(panel, "unpair");
        name.TextChanged += (_, _) => nameField.Error = null;

        var result = await Dialog.ShowAsync(panel, this) as string;
        if (result == "unpair")
        {
            if (!await Dialog.ConfirmAsync(Strings.BtnUnpair, Strings.ConfirmUnpair(peer.DisplayName), Strings.BtnUnpair, Strings.BtnCancel, isDanger: true, anchor: this)) return;
            var error = await _shell.RemovePeerAsync(peer, forget: false);
            _shell.ShowStatus(error ?? Strings.UnpairedDone, error is null ? StatusKind.Success : StatusKind.Error);
            return;
        }
        if (result != "save") return;

        var errors = new List<string>();
        var newName = PeerItem.NormalizeName(name.Text)!;
        if (newName != peer.DisplayName && await _shell.RenamePeerAsync(peer, newName) is { } e1) errors.Add(e1);
        var mode = SettingsModel.Modes[Math.Max(0, modes.SelectedIndex)];
        if (mode != peer.ApplyMode && await _shell.SetPeerModeAsync(peer, mode) is { } e2) errors.Add(e2);
        if ((autoSend.IsChecked == true) != peer.SendEnabled && await _shell.SetPeerSendAsync(peer, autoSend.IsChecked == true) is { } e3) errors.Add(e3);
        _shell.ShowStatus(errors.Count == 0 ? Strings.DeviceSaved : string.Join("\n", errors.Distinct()), errors.Count == 0 ? StatusKind.Success : StatusKind.Error);
    }

    /// <summary>Title + description on the left, a control on the right.</summary>
    internal static Grid SettingRow(string title, string? description, Control control)
    {
        var grid = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto"), Classes = { "setting" } };
        var text = new StackPanel { Spacing = 2, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(0, 0, 16, 0) };
        text.Children.Add(Ui.Text(title, "medium"));
        if (!string.IsNullOrEmpty(description)) text.Children.Add(Ui.Text(description, "caption", "tertiary"));
        grid.Children.Add(text);
        control.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(control, 1);
        grid.Children.Add(control);
        return grid;
    }
}

internal static class ControlExtensions
{
    /// <summary>Inline configuration for object initialisers: <c>new X().With(x =&gt; ...)</c>.</summary>
    public static T With<T>(this T control, Action<T> set)
    {
        set(control);
        return control;
    }
}
