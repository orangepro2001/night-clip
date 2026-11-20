using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Shapes;
using Avalonia.Input;
using Avalonia.Layout;
using Avalonia.Media;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using MoonPassword.Windows.Update;
using Lumen.UI.Controls;
using Lumen.UI.Icons;
using Lumen.UI.Services;

namespace MoonPassword.Windows.Views;

/// <summary>
/// Settings, saved as they change (no separate save button): appearance, notifications and start-up, the SSH
/// agent, updates, and the about block.
/// </summary>
internal sealed class SettingsPage : UserControl
{
    private readonly ShellController _shell;
    private bool _loading;

    // appearance
    private readonly SegmentedControl _theme = new();
    private readonly WrapPanel _accents = new() { Orientation = Orientation.Horizontal };

    // notifications / start-up
    private readonly ToggleSwitch _notifications = new();
    private readonly ToggleSwitch _autoStart = new();

    // updates
    private readonly TextBlock _updateText = Ui.Text("", "body");
    private readonly Button _updateButton = new() { MinWidth = 120 };
    private readonly ProgressBar _updateProgress = new() { Minimum = 0, Maximum = 1, Height = 6, MinHeight = 6, IsVisible = false };
    private readonly ToggleSwitch _autoUpdate = new();

    // about
    private readonly TextBlock _aboutVersion = Ui.Text("", "body");
    private readonly TextBlock _aboutDevice = Ui.Text("", "body");

    public SettingsPage(ShellController shell)
    {
        _shell = shell;

        // ---- appearance ------------------------------------------------------------------
        foreach (var label in new[] { Strings.ThemeSystem, Strings.ThemeLight, Strings.ThemeDark })
            _theme.Items.Add(new SegmentedItem { Content = label });
        _theme.SelectionChanged += (_, _) => OnThemeChanged();
        var appearance = Section(Strings.SettingsAppearanceTitle, null,
            SettingRows.SettingRow(Strings.SettingsTheme, Strings.SettingsThemeHint, _theme),
            new Separator(),
            new StackPanel { Spacing = 8, Children = { Ui.Text(Strings.SettingsAccent, "medium"), _accents } });

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
            SettingRows.SettingRow(Strings.SettingsNotifications, Strings.SettingsNotificationsHint, _notifications),
            SettingRows.SettingRow(Strings.SettingsAutoStart, Strings.SettingsAutoStartHint, _autoStart));

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
            SettingRows.SettingRow(Strings.UpdateAuto, Strings.UpdateAutoHint, _autoUpdate));

        // ---- about ----------------------------------------------------------------------------
        var about = Section(Strings.SettingsAboutTitle, null,
            AboutRow(Strings.AboutVersionLabel, _aboutVersion, null),
            AboutRow(Strings.AboutDeviceLabel, _aboutDevice, null));

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
                Children = { intro, appearance, general, new SshSettingsCard(shell), updates, about },
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

    private void Load(Action set)
    {
        _loading = true;
        try { set(); }
        finally { _loading = false; }
    }

    private void Fail(string error) => _shell.ShowStatus(error, StatusKind.Error);

    // ---- loading ----------------------------------------------------------------------------

    /// <summary>Re-reads the stored values.</summary>
    public void OnShown()
    {
        LoadFields();
        RefreshLive();
    }

    private void LoadFields() => Load(() =>
    {
        _theme.SelectedIndex = _shell.Prefs.Theme switch { "light" => 1, "dark" => 2, _ => 0 };
        _autoStart.IsChecked = _shell.AutoStartEnabled; // registry read: only when the fields are (re)loaded
        _notifications.IsChecked = _shell.Prefs.Notifications;
        RebuildAccents();
    });

    /// <summary>Parts that follow live state: the update section and the about block.</summary>
    private void RefreshLive() => Load(() =>
    {
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
        _aboutDevice.Text = _shell.DeviceName;
    });

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
