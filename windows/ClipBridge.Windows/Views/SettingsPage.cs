using System.Globalization;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Shapes;
using Avalonia.Input;
using Avalonia.Layout;
using Avalonia.Media;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using ClipBridge.Windows.Update;
using Lumen.UI.Controls;
using Lumen.UI.Icons;
using Lumen.UI.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// Settings, saved as they change (no separate save button): appearance, sync, notifications and start-up,
/// received files, updates, and the about block. Text fields save when they lose focus or on Enter.
/// </summary>
internal sealed class SettingsPage : UserControl
{
    private readonly ShellController _shell;
    private bool _loading;

    // appearance
    private readonly SegmentedControl _theme = new();
    private readonly WrapPanel _accents = new() { Orientation = Orientation.Horizontal };

    // sync
    private readonly ToggleSwitch _capture = new();
    private readonly SegmentedControl _mode = new();
    private readonly TextBlock _modeHint = Ui.Text("", "caption", "tertiary");
    private readonly ToggleSwitch _sensitiveDetection = new();
    private readonly ToggleSwitch _sendSensitive = new();
    private readonly TextBox _ttl = new() { Width = 120 };
    private readonly FormField _ttlField;
    private readonly TextBox _history = new() { Width = 120 };
    private readonly FormField _historyField;

    // notifications / start-up
    private readonly ToggleSwitch _notifications = new();
    private readonly ToggleSwitch _autoStart = new();

    // files
    private readonly TextBox _dir = new() { IsReadOnly = true };
    private readonly FormField _dirField;

    // updates
    private readonly TextBlock _updateText = Ui.Text("", "body");
    private readonly Button _updateButton = new() { MinWidth = 120 };
    private readonly ProgressBar _updateProgress = new() { Minimum = 0, Maximum = 1, Height = 6, MinHeight = 6, IsVisible = false };
    private readonly ToggleSwitch _autoUpdate = new();

    // about
    private readonly TextBlock _aboutVersion = Ui.Text("", "body");
    private readonly TextBlock _aboutDevice = Ui.Text("", "body", "mono");
    private readonly TextBlock _aboutFingerprint = Ui.Text("", "body", "mono");

    public SettingsPage(ShellController shell)
    {
        _shell = shell;

        // ---- appearance ------------------------------------------------------------------
        foreach (var label in new[] { Strings.ThemeSystem, Strings.ThemeLight, Strings.ThemeDark })
            _theme.Items.Add(new SegmentedItem { Content = label });
        _theme.SelectionChanged += (_, _) => OnThemeChanged();
        var appearance = Section(Strings.SettingsAppearanceTitle, null,
            DevicesPage.SettingRow(Strings.SettingsTheme, Strings.SettingsThemeHint, _theme),
            new Separator(),
            new StackPanel { Spacing = 8, Children = { Ui.Text(Strings.SettingsAccent, "medium"), _accents } });

        // ---- sync --------------------------------------------------------------------------
        foreach (var label in new[] { Strings.ModeNotify, Strings.ModeManual, Strings.ModeAuto })
            _mode.Items.Add(new SegmentedItem { Content = label });
        _mode.SelectionChanged += (_, _) =>
        {
            UpdateModeHint();
            if (!_loading) _ = SaveAsync();
        };
        _capture.IsCheckedChanged += async (_, _) =>
        {
            if (_loading) return;
            if (await _shell.SetCaptureAsync(_capture.IsChecked == true) is { } error) Fail(error);
        };
        _sensitiveDetection.IsCheckedChanged += (_, _) => { if (!_loading) _ = SaveAsync(); };
        _sendSensitive.IsCheckedChanged += (_, _) => { if (!_loading) _ = SaveAsync(); };

        _ttlField = new FormField { Label = Strings.SettingsTtlLabel, Content = _ttl };
        _historyField = new FormField { Label = Strings.SettingsHistoryLabel, Hint = Strings.SettingsHistoryHint, Content = _history };
        _ttl.TextChanged += (_, _) => { _ttlField.Error = null; UpdateTtlHint(); };
        _history.TextChanged += (_, _) => _historyField.Error = null;
        CommitOnBlurOrEnter(_ttl);
        CommitOnBlurOrEnter(_history);

        var numbers = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 24, Children = { _ttlField, _historyField } };
        var sync = Section(Strings.SettingsSyncTitle, null,
            DevicesPage.SettingRow(Strings.SettingsSync, Strings.SettingsSyncHint, _capture),
            new Separator(),
            new StackPanel { Spacing = 8, Children = { Ui.Text(Strings.SettingsDefaultMode, "medium"), _mode, _modeHint } },
            new Separator(),
            DevicesPage.SettingRow(Strings.SettingsSensitiveDetection, Strings.SettingsSensitiveDetectionHint, _sensitiveDetection),
            DevicesPage.SettingRow(Strings.SettingsSendSensitive, Strings.SettingsSendSensitiveHint, _sendSensitive),
            new Separator(),
            numbers);

        // ---- notifications / start-up -----------------------------------------------------
        _notifications.IsCheckedChanged += (_, _) =>
        {
            if (!_loading) _shell.SetNotifications(_notifications.IsChecked == true);
        };
        _autoStart.IsCheckedChanged += async (_, _) =>
        {
            if (_loading) return;
            if (await _shell.SetAutoStartAsync(_autoStart.IsChecked == true) is { } error) Fail(error);
            Load(() => _autoStart.IsChecked = _shell.AutoStartEnabled);
        };
        var general = Section(Strings.SettingsGeneralTitle, null,
            DevicesPage.SettingRow(Strings.SettingsNotifications, Strings.SettingsNotificationsHint, _notifications),
            DevicesPage.SettingRow(Strings.SettingsAutoStart, Strings.SettingsAutoStartHint, _autoStart));

        // ---- files ---------------------------------------------------------------------------
        var dirRow = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto,Auto") };
        dirRow.Children.Add(_dir);
        var change = Ui.Button(AppIcons.Of(IconKind.Folder), Strings.BtnChangeFolder, () => _ = BrowseAsync());
        change.Margin = new Thickness(8, 0, 0, 0);
        Grid.SetColumn(change, 1);
        dirRow.Children.Add(change);
        var open = Ui.IconButton(AppIcons.FolderOpen, Strings.BtnOpenFolder, () =>
        {
            if (_shell.OpenFolder(_shell.Settings.DownloadDir) is { } error) Fail(error);
        });
        open.Margin = new Thickness(4, 0, 0, 0);
        Grid.SetColumn(open, 2);
        dirRow.Children.Add(open);
        _dirField = new FormField { Label = Strings.SettingsDownloadDir, Hint = Strings.SettingsDownloadDirHint, Content = dirRow };
        var files = Section(Strings.SettingsFilesTitle, null, _dirField,
            new InfoBar { Severity = Severity.Neutral, Title = Strings.SettingsFirewallTitle, Message = Strings.SettingsFirewallText });

        // ---- updates -------------------------------------------------------------------------
        _updateButton.Classes.Add("primary");
        _updateButton.Click += async (_, _) => await UpdateActionAsync();
        _autoUpdate.IsCheckedChanged += async (_, _) =>
        {
            if (_loading) return;
            if (await _shell.SetAutoUpdateAsync(_autoUpdate.IsChecked == true) is { } error) Fail(error);
            RefreshLive();
        };
        var updateRow = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
        updateRow.Children.Add(new StackPanel { Spacing = 8, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(0, 0, 16, 0), Children = { _updateText, _updateProgress } });
        Grid.SetColumn(_updateButton, 1);
        updateRow.Children.Add(_updateButton);
        var updates = Section(Strings.SettingsUpdateTitle, null, updateRow, new Separator(),
            DevicesPage.SettingRow(Strings.UpdateAuto, Strings.UpdateAutoHint, _autoUpdate));

        // ---- about ----------------------------------------------------------------------------
        var copyId = Ui.IconButton(AppIcons.Of(IconKind.Copy), Strings.BtnCopyDeviceId, () => _ = CopyAsync(_shell.DeviceId), "sm");
        var about = Section(Strings.SettingsAboutTitle, null,
            AboutRow(Strings.AboutVersionLabel, _aboutVersion, null),
            AboutRow(Strings.AboutDeviceIdLabel, _aboutDevice, copyId),
            AboutRow(Strings.AboutFingerprintLabel, _aboutFingerprint, null));

        var intro = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 6,
            Children = { Ui.Icon(AppIcons.Of(IconKind.CircleCheck), 14).With(i => i[!Icon.ForegroundProperty] = i.GetResourceObservable("Lumen.Brush.Success").ToBinding()), Ui.Text(Strings.SettingsAutoSaved, "caption", "tertiary") },
        };

        Content = new ScrollViewer
        {
            Content = new StackPanel
            {
                Classes = { "page" },
                MaxWidth = 820,
                HorizontalAlignment = HorizontalAlignment.Left,
                Children = { intro, appearance, sync, general, files, updates, about },
            },
        };

        shell.StateChanged += RefreshLive;
        ThemeManager.Changed += (_, _) => RebuildAccents();
        LoadFields();
        RefreshLive();
    }

    private static Card Section(string title, string? description, params Control[] rows)
    {
        var card = new Card { Header = title, Description = description, Content = new StackPanel { Spacing = 10, Margin = new Thickness(0, 6, 0, 0) } };
        card.Classes.Add("flat");
        foreach (var r in rows) ((StackPanel)card.Content!).Children.Add(r);
        return card;
    }

    private static Grid AboutRow(string label, TextBlock value, Control? action)
    {
        var grid = new Grid { ColumnDefinitions = new ColumnDefinitions("140,*,Auto"), MinHeight = 30 };
        grid.Children.Add(Ui.Text(label, "secondary").With(t => t.VerticalAlignment = VerticalAlignment.Center));
        value.VerticalAlignment = VerticalAlignment.Center;
        value.TextWrapping = TextWrapping.NoWrap;
        value.TextTrimming = TextTrimming.CharacterEllipsis;
        Grid.SetColumn(value, 1);
        grid.Children.Add(value);
        if (action is not null)
        {
            Grid.SetColumn(action, 2);
            grid.Children.Add(action);
        }
        return grid;
    }

    private void CommitOnBlurOrEnter(TextBox box)
    {
        box.LostFocus += (_, _) => { if (!_loading) _ = SaveAsync(); };
        box.KeyDown += (_, e) =>
        {
            if (e.Key != Key.Enter) return;
            e.Handled = true;
            _ = SaveAsync();
        };
    }

    private void Load(Action set)
    {
        _loading = true;
        try { set(); }
        finally { _loading = false; }
    }

    private void Fail(string error) => _shell.ShowStatus(error, StatusKind.Error);

    // ---- loading ----------------------------------------------------------------------------

    /// <summary>Re-reads the stored values (discarding unsaved typing in the number fields).</summary>
    public async void OnShown()
    {
        await _shell.ReloadSettingsAsync();
        LoadFields();
        RefreshLive();
    }

    private void LoadFields() => Load(() =>
    {
        var s = _shell.Settings;
        _theme.SelectedIndex = _shell.Prefs.Theme switch { "light" => 1, "dark" => 2, _ => 0 };
        _mode.SelectedIndex = Math.Max(0, Array.IndexOf(SettingsModel.Modes, s.DefaultApplyMode));
        // A save finishing while the user already types in the other number box must not overwrite that box.
        if (!_ttl.IsKeyboardFocusWithin) _ttl.Text = s.ClipTtlSeconds.ToString(CultureInfo.InvariantCulture);
        if (!_history.IsKeyboardFocusWithin) _history.Text = s.HistoryLimit.ToString(CultureInfo.InvariantCulture);
        _sensitiveDetection.IsChecked = s.SensitiveDetection;
        _sendSensitive.IsChecked = s.SendSensitive;
        _dir.Text = s.DownloadDir;
        _autoStart.IsChecked = _shell.AutoStartEnabled; // registry read: only when the fields are (re)loaded
        _notifications.IsChecked = _shell.Prefs.Notifications;
        _ttlField.Error = null;
        _historyField.Error = null;
        _dirField.Error = null;
        UpdateModeHint();
        UpdateTtlHint();
        RebuildAccents();
    });

    /// <summary>Parts that follow live state: the sync switch, the update section and the about block.</summary>
    private void RefreshLive() => Load(() =>
    {
        _capture.IsChecked = _shell.Settings.Capture;

        var u = _shell.Update;
        _updateText.Text = UpdateView.Text(u);
        _updateText.Classes.Set("danger", u.Mandatory && u.State is "available" or "ready");
        _updateButton.Content = UpdateView.ButtonText(u);
        _updateButton.IsEnabled = u.ButtonAction != UpdateInfo.Action.None;
        _updateButton.IsVisible = u.Enabled;
        _updateProgress.IsVisible = u.State == "downloading";
        _updateProgress.Value = u.ProgressFraction;
        _autoUpdate.IsEnabled = u.Enabled;
        _autoUpdate.IsChecked = u.Auto;

        _aboutVersion.Text = BuildInfo.VersionName;
        _aboutDevice.Text = _shell.DeviceId.Length > 0 ? _shell.DeviceId : "—";
        _aboutFingerprint.Text = _shell.FingerprintPrefix.Length > 0 ? _shell.FingerprintPrefix : "—";
    });

    private void UpdateModeHint() => _modeHint.Text = Strings.ModeHint(SettingsModel.Modes[Math.Max(0, _mode.SelectedIndex)]);

    private void UpdateTtlHint() =>
        _ttlField.Hint = int.TryParse(_ttl.Text, out var secs) && secs >= SettingsModel.TtlMin && secs <= SettingsModel.TtlMax
            ? Strings.SettingsTtlHint + "（" + Formatting.SecondsHint(secs) + "）" : Strings.SettingsTtlHint;

    // ---- appearance ---------------------------------------------------------------------------

    private void OnThemeChanged()
    {
        if (_loading) return;
        var theme = _theme.SelectedIndex switch { 1 => "light", 2 => "dark", _ => "system" };
        _shell.Prefs.Theme = theme;
        _shell.Prefs.Save(App.DataDir);
        ThemeManager.SetMode(Appearance.ToMode(theme));
    }

    private void RebuildAccents()
    {
        _accents.Children.Clear();
        var current = Appearance.CurrentAccentHex(_shell.Prefs);
        foreach (var (name, hex) in Appearance.Accents)
        {
            var selected = string.Equals(hex, current, StringComparison.OrdinalIgnoreCase);
            var swatch = new Ellipse { Width = 22, Height = 22, Fill = new SolidColorBrush(Color.Parse(hex)) };
            var ring = new Border
            {
                Width = 32,
                Height = 32,
                CornerRadius = new CornerRadius(16),
                BorderThickness = new Thickness(selected ? 2 : 0),
                Child = swatch,
            };
            ring[!Border.BorderBrushProperty] = ring.GetResourceObservable("Lumen.Brush.TextPrimary").ToBinding();
            var button = new Button { Content = ring, Padding = new Thickness(2), Margin = new Thickness(0, 0, 6, 6) };
            button.Classes.Add("ghost");
            ToolTip.SetTip(button, name);
            var chosen = hex;
            button.Click += (_, _) =>
            {
                _shell.Prefs.Accent = chosen;
                _shell.Prefs.Save(App.DataDir);
                ThemeManager.SetAccent(Color.Parse(chosen)); // raises Changed → rebuilt with the new selection
            };
            _accents.Children.Add(button);
        }
    }

    // ---- saving ---------------------------------------------------------------------------------

    /// <summary>Validates every core-side field and saves what changed. Field errors stay next to the field.</summary>
    private async Task SaveAsync()
    {
        var ttlOk = int.TryParse(_ttl.Text, out var ttl) && ttl >= SettingsModel.TtlMin && ttl <= SettingsModel.TtlMax;
        var historyOk = int.TryParse(_history.Text, out var history) && history >= SettingsModel.HistoryMin && history <= SettingsModel.HistoryMax;
        _ttlField.Error = ttlOk ? null : Strings.ValidationTtlShort;
        _historyField.Error = historyOk ? null : Strings.ValidationHistoryShort;
        if (!ttlOk || !historyOk) return;

        var input = new SettingsInput(
            Capture: _shell.Settings.Capture,
            DefaultApplyMode: SettingsModel.Modes[Math.Max(0, _mode.SelectedIndex)],
            TtlText: _ttl.Text ?? "", HistoryText: _history.Text ?? "",
            SendSensitive: _sendSensitive.IsChecked == true, DownloadDir: _dir.Text ?? "",
            SensitiveDetection: _sensitiveDetection.IsChecked == true);
        var validation = SettingsModel.Validate(input);
        if (!validation.Ok)
        {
            Fail(string.Join("\n", validation.Errors));
            return;
        }
        if (SettingsModel.Diff(_shell.Settings, validation.Values!).Count == 0) return;
        if (await _shell.SaveSettingsAsync(validation.Values!) is { } error) Fail(error);
        LoadFields();
    }

    private async Task BrowseAsync()
    {
        var folder = await Ui.PickFolderAsync(this, Strings.SettingsDownloadDir, _dir.Text);
        if (folder is null) return;
        if (!SettingsModel.IsAbsoluteWindowsPath(folder))
        {
            _dirField.Error = Strings.ValidationDownloadDir;
            return;
        }
        _dir.Text = folder;
        await SaveAsync();
    }

    private async Task CopyAsync(string text)
    {
        if (text.Length == 0) return;
        var ok = await _shell.CopyTextAsync(text);
        _shell.ShowStatus(ok ? Strings.CopiedPlain : Strings.ApplyFailed, ok ? StatusKind.Success : StatusKind.Error);
    }

    private async Task UpdateActionAsync()
    {
        var action = _shell.Update.ButtonAction;
        _updateButton.IsEnabled = false;
        if (action == UpdateInfo.Action.Check) _updateText.Text = Strings.UpdateChecking;
        var error = await _shell.UpdateActionAsync(action);
        if (error is not null)
        {
            if (action == UpdateInfo.Action.Install) await Dialog.AlertAsync(Strings.UpdatePrepareFailedTitle, error, Strings.BtnOk, this);
            else Fail(error);
        }
        RefreshLive();
    }
}
