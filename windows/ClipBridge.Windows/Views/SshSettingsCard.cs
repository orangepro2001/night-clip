using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The "SSH 代理" card of the settings page: the agent switch and its state, the environment checks with the
/// fixes the app can apply (each shown and confirmed first), how long keys outlive an idle vault lock,
/// forwarding, notifications, and the recent uses.
/// </summary>
internal sealed class SshSettingsCard : Card
{
    private static readonly int[] TtlChoices = { 0, 900, 3600, 4 * 3600, 8 * 3600, 24 * 3600 };

    private readonly ShellController _shell;
    private readonly ToggleSwitch _enable = new();
    private readonly ToggleSwitch _forwarding = new();
    private readonly ComboBox _ttl = new() { MinWidth = 160 };
    private readonly SegmentedControl _notify = new();
    private readonly Icon _stateIcon = new() { Size = 16, VerticalAlignment = VerticalAlignment.Center };
    private readonly TextBlock _stateText = Ui.Text("", "medium");
    private readonly TextBlock _keysText = Ui.Text("", "caption", "tertiary");
    private readonly StackPanel _issues = new() { Spacing = 8 };
    private readonly StackPanel _recent = new() { Spacing = 4 };
    private readonly Expander _recentBox;
    private SshEnvFacts _facts = SshEnvFacts.Unknown;
    private bool _loading;

    /// <summary>A subclass does not get Card's control theme by itself.</summary>
    protected override Type StyleKeyOverride => typeof(Card);

    public SshSettingsCard(ShellController shell)
    {
        _shell = shell;
        Header = Strings.SshSettingsTitle;
        Description = Strings.SshSettingsDesc;
        Classes.Add("flat");

        _enable.IsCheckedChanged += async (_, _) =>
        {
            if (_loading) return;
            if (await _shell.SetSshAgentAsync(_enable.IsChecked == true) is { } error) _shell.ShowStatus(error, StatusKind.Error);
        };
        _forwarding.IsCheckedChanged += async (_, _) =>
        {
            if (_loading) return;
            var on = _forwarding.IsChecked == true ? "on" : "off";
            if (await _shell.CallAsync(a => a.SettingsSet("ssh_allow_forwarding", on)) is { } error) _shell.ShowStatus(error, StatusKind.Error);
            await _shell.RefreshSshStatusAsync();
        };
        foreach (var s in TtlChoices) _ttl.Items.Add(new ComboBoxItem { Content = TtlText(s), Tag = s });
        _ttl.SelectionChanged += async (_, _) =>
        {
            if (_loading || _ttl.SelectedItem is not ComboBoxItem { Tag: int s }) return;
            if (await _shell.CallAsync(a => a.SettingsSet("ssh_key_ttl_s", s.ToString())) is { } error) _shell.ShowStatus(error, StatusKind.Error);
            await _shell.RefreshSshStatusAsync();
        };
        foreach (var label in new[] { Strings.SshNotifyFirst, Strings.SshNotifyAll, Strings.SshNotifyOff })
            _notify.Items.Add(new SegmentedItem { Content = label });
        _notify.SelectionChanged += (_, _) =>
        {
            if (_loading) return;
            _shell.Prefs.SshNotify = _notify.SelectedIndex switch { 1 => "all", 2 => "off", _ => "first" };
            _shell.Prefs.Save(App.DataDir);
        };

        var selfTest = Ui.Button(AppIcons.Of(IconKind.CircleCheck), Strings.SshBtnSelfTest, () => { }, "soft", "sm");
        selfTest.Click += (_, _) => _ = Ui.Busy(selfTest, SelfTestAsync);
        var state = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto") };
        state.Children.Add(_stateIcon);
        var stateTexts = new StackPanel { Spacing = 2, Margin = new Thickness(10, 0, 12, 0), VerticalAlignment = VerticalAlignment.Center, Children = { _stateText, _keysText } };
        Grid.SetColumn(stateTexts, 1);
        state.Children.Add(stateTexts);
        Grid.SetColumn(selfTest, 2);
        state.Children.Add(selfTest);

        _recentBox = new Expander { Header = Strings.SshRecentTitle, Content = _recent, HorizontalAlignment = HorizontalAlignment.Stretch };

        Content = new StackPanel
        {
            Spacing = 10,
            Margin = new Thickness(0, 6, 0, 0),
            Children =
            {
                DevicesPage.SettingRow(Strings.SshSettingEnable, Strings.SshSettingEnableHint, _enable),
                state,
                _issues,
                new Separator(),
                DevicesPage.SettingRow(Strings.SshSettingTtl, Strings.SshSettingTtlHint, _ttl),
                DevicesPage.SettingRow(Strings.SshSettingForwarding, Strings.SshSettingForwardingHint, _forwarding),
                DevicesPage.SettingRow(Strings.SshSettingNotify, null, _notify),
                _recentBox,
            },
        };

        shell.SshChanged += () => { if (IsAttachedToVisualTree()) { Render(); _ = LoadRecentAsync(); } };
        AttachedToVisualTree += (_, _) => _ = RefreshAllAsync();
        Render();
    }

    private bool IsAttachedToVisualTree() => TopLevel.GetTopLevel(this) is not null;

    private static string TtlText(int seconds) => seconds switch
    {
        0 => Strings.SshTtlFollow,
        < 3600 => Strings.SshTtlMinutes(seconds / 60),
        _ => Strings.SshTtlHours(seconds / 3600),
    };

    private async Task RefreshAllAsync()
    {
        await _shell.RefreshSshStatusAsync();
        _facts = await SshEnvProbe.ProbeAsync();
        Render();
        await LoadRecentAsync();
    }

    private void Render()
    {
        var st = _shell.SshState;
        _loading = true;
        _enable.IsChecked = st.Enabled;
        _enable.IsEnabled = st.Supported;
        _forwarding.IsChecked = st.AllowForwarding;
        if (!_ttl.Items.OfType<ComboBoxItem>().Any(i => i.Tag is int t && t == st.TtlSeconds))
            _ttl.Items.Add(new ComboBoxItem { Content = TtlText(st.TtlSeconds), Tag = st.TtlSeconds });
        _ttl.SelectedItem = _ttl.Items.OfType<ComboBoxItem>().First(i => i.Tag is int t && t == st.TtlSeconds);
        _notify.SelectedIndex = _shell.Prefs.SshNotify switch { "all" => 1, "off" => 2, _ => 0 };
        _loading = false;

        var (icon, text, brush) = st.State switch
        {
            "listening" => (IconKind.CircleCheck, Strings.SshStatusListening, "Lumen.Brush.Success"),
            "conflict" => (IconKind.AlertTriangle, Strings.SshStatusConflict, "Lumen.Brush.Warning"),
            "error" => (IconKind.AlertCircle, Strings.SshStatusError, "Lumen.Brush.Danger"),
            "unsupported" => (IconKind.Info, Strings.SshStatusUnsupported, "Lumen.Brush.TextTertiary"),
            _ => (IconKind.Minus, Strings.SshStatusStopped, "Lumen.Brush.TextTertiary"),
        };
        _stateIcon.Kind = icon;
        _stateIcon[!Icon.ForegroundProperty] = _stateIcon.GetResourceObservable(brush).ToBinding();
        _stateText.Text = text + (st.Pipe.Length > 0 ? "  ·  " + st.Pipe : "");
        _keysText.Text = st.Listening ? Strings.SshStatusKeys(st.KeysLoaded, st.InGrace, DateTimeOffset.FromUnixTimeSeconds(st.ExpiresAt))
            : st.Error.Length > 0 ? st.Error : "";
        _keysText.IsVisible = _keysText.Text.Length > 0;

        _issues.Children.Clear();
        foreach (var issue in SshEnv.Check(_facts, st))
        {
            var bar = new InfoBar { Severity = issue.IsWarning ? Severity.Warning : Severity.Info, IsOpen = true, IsClosable = false, Title = issue.Title, Message = issue.Text };
            if (issue.Fix == SshEnv.FixGitSsh)
                bar.Actions = Ui.TextButton(Strings.SshBtnFixGit, () => _ = FixGitAsync(), "soft", "sm");
            else if (issue.Id == "service")
                bar.Actions = Ui.TextButton(Strings.SshServiceFixButton, () => _ = FixServiceAsync(), "soft", "sm");
            _issues.Children.Add(bar);
        }
        if (_shell.GitSshIsOurs(_facts))
            _issues.Children.Add(new StackPanel
            {
                Orientation = Orientation.Horizontal, Spacing = 8,
                Children =
                {
                    Ui.Text(Strings.SshGitFixed, "caption", "tertiary").With(t => t.VerticalAlignment = VerticalAlignment.Center),
                    Ui.TextButton(Strings.SshBtnUndoGit, () => _ = UndoGitAsync(), "ghost", "sm"),
                },
            });
    }

    private async Task LoadRecentAsync()
    {
        var r = await _shell.SshCallAsync(a => a.Recent(15));
        _recent.Children.Clear();
        if (!r.Ok || r.Value!.Count == 0)
        {
            _recent.Children.Add(Ui.Text(Strings.SshRecentEmpty, "caption", "tertiary"));
            return;
        }
        foreach (var u in r.Value!)
        {
            var line = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*") };
            var dot = new Ellipse(u.Signed);
            line.Children.Add(dot);
            var text = Ui.Text($"{StampText.Local(u.At)}  ·  {(u.ProgramName.Length > 0 ? u.ProgramName : "?")}  ·  {(u.Signed ? Strings.SshUsedText(u) : Strings.SshRefusedText(u))}", "caption", "secondary");
            text.TextTrimming = TextTrimming.CharacterEllipsis;
            text.Margin = new Thickness(8, 0, 0, 0);
            Grid.SetColumn(text, 1);
            line.Children.Add(text);
            _recent.Children.Add(line);
        }
    }

    private async Task SelfTestAsync()
    {
        var r = await _shell.SshCallAsync(a => a.SelfTest());
        if (!r.Ok) { _shell.ShowStatus(r.Error, StatusKind.Error); return; }
        var t = r.Value!;
        _shell.ShowStatus(t.Ok ? Strings.SshSelfTestOk(t.Listed) : Strings.SshSelfTestFailed(t.Error), t.Ok ? StatusKind.Success : StatusKind.Error);
    }

    private async Task FixGitAsync()
    {
        var command = "git config --global core.sshCommand " + SshEnv.WindowsSshCommand;
        if (!await VaultUi.ConfirmAsync(this, Strings.SshFixGitText(command), Strings.SshFixGitTitle, Strings.SshBtnFixGit)) return;
        if (await _shell.FixGitSshAsync() is { } error) { _shell.ShowStatus(error, StatusKind.Error); return; }
        _shell.ShowStatus(Strings.SshGitFixed, StatusKind.Success);
        await RefreshAllAsync();
    }

    private async Task UndoGitAsync()
    {
        if (await _shell.UndoGitSshAsync() is { } error) { _shell.ShowStatus(error, StatusKind.Error); return; }
        _shell.ShowStatus(Strings.SshGitUndone, StatusKind.Success);
        await RefreshAllAsync();
    }

    private async Task FixServiceAsync()
    {
        if (!await VaultUi.ConfirmAsync(this, Strings.SshServiceFixText, Strings.SshServiceFixTitle, Strings.SshServiceFixButton, warning: true)) return;
        if (await _shell.DisableWindowsSshAgentServiceAsync() is { } error) { _shell.ShowStatus(error, StatusKind.Error); return; }
        _shell.ShowStatus(Strings.SshServiceFixed, StatusKind.Success);
        await RefreshAllAsync();
    }

    /// <summary>A small green (signed) or red (refused) dot.</summary>
    private sealed class Ellipse : Border
    {
        public Ellipse(bool ok)
        {
            Width = 7;
            Height = 7;
            CornerRadius = new CornerRadius(4);
            VerticalAlignment = VerticalAlignment.Center;
            this[!BackgroundProperty] = this.GetResourceObservable(ok ? "Lumen.Brush.Success" : "Lumen.Brush.Danger").ToBinding();
        }
    }
}
