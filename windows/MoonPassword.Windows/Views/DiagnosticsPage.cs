using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using MoonPassword.Windows.Update;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace MoonPassword.Windows.Views;

/// <summary>Runtime facts: a summary on top, the full report (copyable) below, links to the log and data folders.</summary>
internal sealed class DiagnosticsPage : UserControl
{
    private readonly ShellController _shell;
    private readonly TextBox _report;
    private readonly StatCard _core;
    private readonly StatCard _vault;
    private readonly Button _copy;
    private string _text = "";

    public DiagnosticsPage(ShellController shell)
    {
        _shell = shell;

        var toolbar = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
        toolbar.Children.Add(Ui.Text(Strings.DiagIntro, "secondary").With(t => t.VerticalAlignment = VerticalAlignment.Center));
        _copy = Ui.Button(AppIcons.Of(IconKind.Copy), Strings.DiagCopy, () => _ = CopyAsync(), "primary");
        var actions = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8,
            Children =
            {
                Ui.Button(AppIcons.Of(IconKind.RefreshCw), Strings.BtnRefresh, () => _ = ReloadAsync()),
                Ui.Button(AppIcons.FolderOpen, Strings.DiagOpenLogs, () => Tell(_shell.OpenFolder(AppLog.LogDirectory))),
                Ui.Button(AppIcons.Of(IconKind.Folder), Strings.DiagOpenData, () => Tell(_shell.OpenFolder(App.DataDir))),
                _copy,
            },
        };
        Grid.SetColumn(actions, 1);
        toolbar.Children.Add(actions);

        _core = new StatCard { Label = Strings.DiagCoreLabel, Icon = IconKind.Zap };
        _vault = new StatCard { Label = Strings.DiagVaultLabel, Icon = IconKind.Lock };
        var stats = new Grid { ColumnDefinitions = new ColumnDefinitions("*,16,*") };
        stats.Children.Add(_core);
        Grid.SetColumn(_vault, 2);
        stats.Children.Add(_vault);

        _report = new TextBox
        {
            IsReadOnly = true,
            AcceptsReturn = true,
            TextWrapping = TextWrapping.NoWrap,
            MinHeight = 300,
            BorderThickness = new Thickness(0),
            Background = Brushes.Transparent,
        };
        _report.Classes.Add("code");
        var reportCard = new Card { Header = Strings.DiagReportTitle, Description = Strings.DiagReportHint, Content = _report };
        reportCard.Classes.Add("flat");

        Content = new ScrollViewer
        {
            Content = new StackPanel { Classes = { "page" }, Children = { toolbar, stats, reportCard } },
        };
        shell.StateChanged += UpdateSummary;
        UpdateSummary();
    }

    public async void OnShown() => await ReloadAsync();

    private void Tell(string? error)
    {
        if (error is not null) _shell.ShowStatus(error, StatusKind.Error);
    }

    private void UpdateSummary()
    {
        _core.Value = _shell.CoreRunning ? Strings.DiagCoreRunning : Strings.DiagCoreStopped;
        _core.Caption = Strings.AboutVersion(BuildInfo.VersionName);
        _vault.Value = _shell.VaultUnlocked ? Strings.DiagVaultUnlocked : Strings.DiagVaultLocked;
        _vault.Caption = _shell.DeviceName;
    }

    private async Task ReloadAsync()
    {
        UpdateSummary();
        var (json, error) = await _shell.CallAsync(a => a.Diagnostics());
        _text = error is not null
            ? Strings.DiagUnavailable + "\n" + error
            : DiagnosticsText.Build(json, BuildInfo.VersionName, DateTimeOffset.Now);
        _report.Text = _text;
    }

    private async Task CopyAsync()
    {
        if (_text.Length == 0) await ReloadAsync();
        if (_text.Length == 0) return;
        var ok = await _shell.CopyTextAsync(_text);
        _shell.ShowStatus(ok ? Strings.DiagCopied : Strings.ApplyFailed, ok ? StatusKind.Success : StatusKind.Error);
    }
}
