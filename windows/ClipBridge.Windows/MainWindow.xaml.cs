using System.IO;
using System.Text.Json;
using System.Windows;
using ClipBridge.Windows.Interop;

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
            var dataDir = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "ClipBridge");
            Directory.CreateDirectory(dataDir);
            var dbKey = DevKey.LoadOrCreate(dataDir); // TODO(WP9: DPAPI): temporary, unencrypted key file

            _core.EventReceived += ev => Dispatcher.BeginInvoke(() => Append("event  " + ev));
            _core.Start(new
            {
                data_dir = dataDir,
                db_key_b64 = dbKey,
                device_name = Environment.MachineName,
                platform = "windows",
                listen = true,
                port = 47821,
            });
            ShowStatus();
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

    private void RefreshButton_Click(object sender, RoutedEventArgs e)
    {
        try { ShowStatus(); }
        catch (CoreException ex) { Append(ex.Message); }
    }

    private void Append(string line)
    {
        Log.AppendText(line + Environment.NewLine);
        Log.ScrollToEnd();
    }
}
