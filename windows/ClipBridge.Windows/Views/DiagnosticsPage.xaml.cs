using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using ClipBridge.Windows.Update;

namespace ClipBridge.Windows.Views;

public partial class DiagnosticsPage : UserControl
{
    private ShellController _shell = null!;
    private string _text = "";

    public DiagnosticsPage() => InitializeComponent();

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        shell.StateChanged += UpdateBanner;
    }

    public async void OnShown() => await ReloadAsync();

    private void UpdateBanner() =>
        NoTailnetBanner.Visibility = _shell.Warnings.Has("NO_TAILNET") ? Visibility.Visible : Visibility.Collapsed;

    private async Task ReloadAsync()
    {
        UpdateBanner();
        var (json, error) = await _shell.CallAsync(a => a.Diagnostics());
        if (error is not null)
        {
            _text = Strings.DiagUnavailable + "\n" + error;
        }
        else
        {
            _text = DiagnosticsText.Build(json, BuildInfo.VersionName, DateTimeOffset.Now, _shell.Warnings.Active.ToList());
        }
        DiagText.Text = _text;
    }

    private async void Refresh_Click(object sender, RoutedEventArgs e) => await ReloadAsync();

    private async void Copy_Click(object sender, RoutedEventArgs e)
    {
        if (_text.Length == 0) return;
        var ok = await _shell.CopyTextAsync(_text);
        _shell.ShowStatus(ok ? Strings.DiagCopied : Strings.ApplyFailed);
    }

    private void Logs_Click(object sender, RoutedEventArgs e)
    {
        if (_shell.OpenFolder(AppLog.LogDirectory) is { } error) _shell.ShowStatus(error);
    }

    private void Data_Click(object sender, RoutedEventArgs e)
    {
        if (_shell.OpenFolder(App.DataDir) is { } error) _shell.ShowStatus(error);
    }
}
