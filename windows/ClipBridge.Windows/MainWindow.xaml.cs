using System.IO;
using System.Text.Json;
using System.Windows;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Update;

namespace ClipBridge.Windows;

/// <summary>
/// Scaffold window that proves the DLL boundary works: it starts the core,
/// prints status and shows every event from the pump thread.
/// </summary>
public partial class MainWindow : Window
{
    private readonly CoreHost _core = new();

    public MainWindow()
    {
        InitializeComponent();
        Loaded += OnLoaded;
        Closed += (_, _) => _core.Dispose();
    }

    private void OnLoaded(object sender, RoutedEventArgs e)
    {
        try
        {
            var dataDir = App.DataDir;
            Directory.CreateDirectory(dataDir);
            var dbKey = DevKey.LoadOrCreate(dataDir); // TODO(WP9: DPAPI): temporary, unencrypted key file

            _core.EventReceived += ev => Dispatcher.BeginInvoke(() =>
            {
                Append("event  " + ev);
                if (ev.TryGetProperty("type", out var t) && (t.GetString() ?? "").StartsWith("update."))
                {
                    RefreshUpdate();
                    // A new version (or a finished download) deserves attention even when minimised.
                    if (t.GetString() is "update.available" or "update.ready") TaskbarFlash.Flash(this);
                }
            });
            _core.Start(new
            {
                data_dir = dataDir,
                db_key_b64 = dbKey,
                device_name = Environment.MachineName,
                platform = "windows",
                listen = true,
                port = 47821,
                app_version_code = BuildInfo.VersionCode,
                app_version_name = BuildInfo.VersionName,
                update_url = BuildInfo.UpdateUrl,
            });
            ShowStatus();
            RefreshUpdate();
        }
        catch (Exception ex) when (ex is DllNotFoundException or BadImageFormatException or CoreException
                                    or DevKeyException or IOException or UnauthorizedAccessException)
        {
            StatusLine.Text = "Core failed to start";
            Append(ex.Message);
        }
    }

    private void ShowStatus()
    {
        var st = _core.Call("status");
        StatusLine.Text = $"{st.GetProperty("device_name").GetString()}  ·  {st.GetProperty("device_id").GetString()}";
        Append("status " + st);
    }

    // ---- self-update -------------------------------------------------------------

    private UpdateInfo _update = UpdateInfo.FromJson(default);

    private void RefreshUpdate()
    {
        try { _update = UpdateInfo.FromJson(_core.Call("update.status")); }
        catch (CoreException ex) { UpdateText.Text = "Update status unavailable: " + ex.Message; return; }

        UpdateText.Text = _update.Text;
        UpdateText.Foreground = _update.Mandatory && _update.State is "available" or "ready"
            ? System.Windows.Media.Brushes.Firebrick : System.Windows.SystemColors.ControlTextBrush;
        UpdateButton.Content = _update.ButtonText;
        UpdateButton.IsEnabled = _update.ButtonAction != UpdateInfo.Action.None;
        UpdateButton.Visibility = _update.Enabled ? Visibility.Visible : Visibility.Collapsed;
        UpdateProgress.Visibility = _update.State == "downloading" ? Visibility.Visible : Visibility.Collapsed;
        UpdateProgress.Value = _update.ProgressFraction;
        AutoUpdateBox.IsEnabled = _update.Enabled;
        AutoUpdateBox.IsChecked = _update.Auto;
    }

    private async void UpdateButton_Click(object sender, RoutedEventArgs e)
    {
        UpdateButton.IsEnabled = false;
        try
        {
            switch (_update.ButtonAction)
            {
                case UpdateInfo.Action.Check:
                    UpdateText.Text = "Checking for updates…";
                    await Task.Run(() => _core.Call("update.check")); // up to 30 s: keep the UI free
                    break;
                case UpdateInfo.Action.Download:
                    _core.Call("update.download");
                    break;
                case UpdateInfo.Action.Install:
                    InstallUpdate();
                    return;
            }
        }
        catch (CoreException ex) { Append("update: " + ex.Message); }
        finally { RefreshUpdate(); }
    }

    private void AutoUpdateBox_Click(object sender, RoutedEventArgs e)
    {
        try { _core.Call("update.config", new { auto = AutoUpdateBox.IsChecked == true }); }
        catch (CoreException ex) { Append("update: " + ex.Message); }
        RefreshUpdate();
    }

    /// <summary>Stages the verified package, starts the staged exe to swap the files and exits.</summary>
    private void InstallUpdate()
    {
        try
        {
            var staged = UpdateApplier.Stage(_update.ReadyPath, App.StagingDir);
            UpdateApplier.LaunchApplier(staged, AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar), Environment.ProcessId);
            Close(); // Closed disposes the core; the updater waits for this process to exit
        }
        catch (Exception ex) when (ex is IOException or InvalidDataException or InvalidOperationException
                                       or UnauthorizedAccessException or System.ComponentModel.Win32Exception)
        {
            Append("update: " + ex.Message);
            MessageBox.Show(this, "The update could not be prepared:\n\n" + ex.Message, "ClipBridge update",
                MessageBoxButton.OK, MessageBoxImage.Warning);
            RefreshUpdate();
        }
    }

    private void RefreshButton_Click(object sender, RoutedEventArgs e)
    {
        try { ShowStatus(); RefreshUpdate(); }
        catch (CoreException ex) { Append(ex.Message); }
    }

    private void Append(string line)
    {
        Log.AppendText(line + Environment.NewLine);
        Log.ScrollToEnd();
    }
}
