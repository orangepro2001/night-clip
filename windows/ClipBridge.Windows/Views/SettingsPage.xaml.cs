using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using ClipBridge.Windows.Update;
using Microsoft.Win32;

namespace ClipBridge.Windows.Views;

public partial class SettingsPage : UserControl
{
    private ShellController _shell = null!;

    public SettingsPage() => InitializeComponent();

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        shell.StateChanged += RefreshLive;
        LoadFields();
        RefreshLive();
    }

    /// <summary>Re-reads the stored values into the editable fields (discarding unsaved edits).</summary>
    public async void OnShown()
    {
        await _shell.ReloadSettingsAsync();
        LoadFields();
        SaveResult.Text = "";
        RefreshLive();
    }

    private void LoadFields()
    {
        var s = _shell.Settings;
        ModeBox.SelectedValue = s.DefaultApplyMode;
        TtlBox.Text = s.ClipTtlSeconds.ToString(System.Globalization.CultureInfo.InvariantCulture);
        HistoryBox.Text = s.HistoryLimit.ToString(System.Globalization.CultureInfo.InvariantCulture);
        SensitiveDetectionBox.IsChecked = s.SensitiveDetection;
        SensitiveBox.IsChecked = s.SendSensitive;
        DirBox.Text = s.DownloadDir;
        AutoStartBox.IsChecked = _shell.AutoStartEnabled; // registry read: only when the fields are (re)loaded
    }

    /// <summary>Parts of the page that follow live state: sync switch, autostart, update section, about.</summary>
    private void RefreshLive()
    {
        SyncBox.IsChecked = _shell.Settings.Capture;

        var u = _shell.Update;
        UpdateText.Text = UpdateView.Text(u);
        UpdateText.Foreground = u.Mandatory && u.State is "available" or "ready"
            ? System.Windows.Media.Brushes.Firebrick : System.Windows.SystemColors.ControlTextBrush;
        UpdateButton.Content = UpdateView.ButtonText(u);
        UpdateButton.IsEnabled = u.ButtonAction != UpdateInfo.Action.None;
        UpdateButton.Visibility = u.Enabled ? Visibility.Visible : Visibility.Collapsed;
        UpdateProgress.Visibility = u.State == "downloading" ? Visibility.Visible : Visibility.Collapsed;
        UpdateProgress.Value = u.ProgressFraction;
        AutoUpdateBox.IsEnabled = u.Enabled;
        AutoUpdateBox.IsChecked = u.Auto;

        AboutVersion.Text = Strings.AboutVersion(BuildInfo.VersionName);
        AboutDevice.Text = Strings.AboutDeviceId(_shell.DeviceId);
        AboutFingerprint.Text = Strings.AboutFingerprint(_shell.FingerprintPrefix);
    }

    private void Tell(string? message)
    {
        if (!string.IsNullOrEmpty(message)) _shell.ShowStatus(message);
    }

    // ---- editable settings ------------------------------------------------------------------

    private void TtlBox_TextChanged(object sender, TextChangedEventArgs e)
    {
        TtlHint.Text = int.TryParse(TtlBox.Text, out var secs) && secs >= SettingsModel.TtlMin && secs <= SettingsModel.TtlMax
            ? Formatting.SecondsHint(secs) : "";
    }

    private void Browse_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new OpenFolderDialog { Title = Strings.SettingsDownloadDir, InitialDirectory = _shell.Settings.DownloadDir };
        if (dialog.ShowDialog(Window.GetWindow(this)) == true) DirBox.Text = dialog.FolderName;
    }

    private async void Save_Click(object sender, RoutedEventArgs e)
    {
        var input = new SettingsInput(
            Capture: SyncBox.IsChecked == true,
            DefaultApplyMode: ModeBox.SelectedValue as string ?? "notify",
            TtlText: TtlBox.Text, HistoryText: HistoryBox.Text,
            SendSensitive: SensitiveBox.IsChecked == true, DownloadDir: DirBox.Text,
            SensitiveDetection: SensitiveDetectionBox.IsChecked == true);
        var validation = SettingsModel.Validate(input);
        if (!validation.Ok)
        {
            SaveResult.Foreground = System.Windows.Media.Brushes.Firebrick;
            SaveResult.Text = string.Join("\n", validation.Errors);
            return;
        }
        var error = await _shell.SaveSettingsAsync(validation.Values!);
        SaveResult.Foreground = error is null ? System.Windows.Media.Brushes.SeaGreen : System.Windows.Media.Brushes.Firebrick;
        SaveResult.Text = error ?? Strings.SettingsSaved;
        LoadFields();
    }

    // ---- immediate switches --------------------------------------------------------------------

    private async void SyncBox_Click(object sender, RoutedEventArgs e)
    {
        Tell(await _shell.SetCaptureAsync(SyncBox.IsChecked == true));
        RefreshLive();
    }

    private async void AutoStartBox_Click(object sender, RoutedEventArgs e)
    {
        Tell(await _shell.SetAutoStartAsync(AutoStartBox.IsChecked == true));
        AutoStartBox.IsChecked = _shell.AutoStartEnabled;
    }

    // ---- updates --------------------------------------------------------------------------------

    private async void UpdateButton_Click(object sender, RoutedEventArgs e)
    {
        var action = _shell.Update.ButtonAction;
        UpdateButton.IsEnabled = false;
        if (action == UpdateInfo.Action.Check) UpdateText.Text = Strings.UpdateChecking;
        var error = await _shell.UpdateActionAsync(action);
        if (error is not null)
        {
            if (action == UpdateInfo.Action.Install)
                MessageBox.Show(Window.GetWindow(this)!, error, Strings.UpdatePrepareFailedTitle, MessageBoxButton.OK, MessageBoxImage.Warning);
            else Tell(error);
        }
        RefreshLive();
    }

    private async void AutoUpdateBox_Click(object sender, RoutedEventArgs e)
    {
        Tell(await _shell.SetAutoUpdateAsync(AutoUpdateBox.IsChecked == true));
        RefreshLive();
    }
}
