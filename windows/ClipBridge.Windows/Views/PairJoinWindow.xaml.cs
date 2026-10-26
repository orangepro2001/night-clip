using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Media;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>Joining side: paste a link or type host/port/key, then show the large 6-digit SAS for comparison.</summary>
public partial class PairJoinWindow : Window
{
    private static PairJoinWindow? _instance;

    internal static void ShowOrActivate()
    {
        if (_instance is { } open)
        {
            open.Activate();
            return;
        }
        _instance = new PairJoinWindow();
        _instance.Show();
    }

    private readonly ShellController _shell = ShellController.Current;
    private readonly PairJoinSession _session = new();
    private bool _isClosed;

    private PairJoinWindow()
    {
        InitializeComponent();
        Title = Strings.PairJoinTitle;
        IntroText.Text = Strings.PairJoinIntro;
        LinkTab.Header = Strings.PairTabLink;
        ManualTab.Header = Strings.PairTabManual;
        LinkLabel.Text = Strings.PairLinkLabel;
        PasteButton.Content = Strings.PairPasteFromClipboard;
        HostLabel.Text = Strings.PairHostLabel;
        PortLabel.Text = Strings.PairPortLabel;
        PskLabel.Text = Strings.PairPskLabel;
        FpLabel.Text = Strings.PairFpLabel;
        StartButton.Content = Strings.PairStartJoin;
        CloseButton.Content = Strings.PairClose;
        SasPrompt.Text = Strings.PairSasPrompt;
        SasHint.Text = Strings.PairSasWaiting;
        if (MainWindow.Instance is { IsVisible: true } owner) { Owner = owner; WindowStartupLocation = WindowStartupLocation.CenterOwner; }

        _shell.EventReceived += OnCoreEvent;
        Closed += (_, _) =>
        {
            _isClosed = true;
            _shell.EventReceived -= OnCoreEvent;
            _instance = null;
        };
        PreviewKeyDown += (_, e) => { if (e.Key == System.Windows.Input.Key.Escape) CloseSafe(); };
    }

    private void CloseSafe()
    {
        if (!_isClosed) Close();
    }

    private void Paste_Click(object sender, RoutedEventArgs e)
    {
        try
        {
            if (Clipboard.ContainsText()) LinkBox.Text = Clipboard.GetText();
        }
        catch (COMException)
        {
            StatusText.Text = Strings.ApplyFailed; // the clipboard is held by another program
        }
    }

    private async void Start_Click(object sender, RoutedEventArgs e)
    {
        var input = Tabs.SelectedItem == LinkTab
            ? PairingInput.FromLink(LinkBox.Text, DateTimeOffset.UtcNow)
            : PairingInput.FromManual(HostBox.Text, PortBox.Text, PskBox.Text, FpBox.Text);
        if (!input.Ok)
        {
            ShowError(input.Error ?? Strings.PairFailed);
            return;
        }

        _session.Begin();
        Render();
        var (joinId, error) = await _shell.CallAsync(a => a.PairJoin(input.Request!));
        if (_isClosed) return;
        if (error is not null || joinId is null) _session.OnJoinFailed(error ?? Strings.PairFailed);
        else _session.OnJoinStarted(joinId);
        Render();
    }

    private void OnCoreEvent(CoreEvent ev)
    {
        if (_session.OnEvent(ev)) Render();
    }

    private void ShowError(string message)
    {
        StatusText.Foreground = Brushes.Firebrick;
        StatusText.Text = message;
    }

    private void Render()
    {
        var busy = _session.Phase is PairJoinPhase.Joining or PairJoinPhase.AwaitingSas;
        Tabs.IsEnabled = !busy && _session.Phase != PairJoinPhase.Accepted;
        StartButton.IsEnabled = !busy && _session.Phase != PairJoinPhase.Accepted;
        SasPanel.Visibility = _session.Phase == PairJoinPhase.AwaitingSas ? Visibility.Visible : Visibility.Collapsed;

        switch (_session.Phase)
        {
            case PairJoinPhase.Joining:
                StatusText.Foreground = SystemColors.ControlTextBrush;
                StatusText.Text = Strings.PairJoining;
                break;
            case PairJoinPhase.AwaitingSas:
                SasText.Text = _session.Sas;
                StatusText.Text = "";
                break;
            case PairJoinPhase.Accepted:
                StatusText.Foreground = Brushes.SeaGreen;
                StatusText.Text = Strings.PairSuccessWith(_session.PeerName.Length > 0 ? _session.PeerName : Strings.Unknown);
                var close = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1.5) };
                close.Tick += (_, _) => { close.Stop(); CloseSafe(); };
                close.Start();
                break;
            case PairJoinPhase.Failed:
                ShowError(_session.ErrorMessage);
                break;
        }
    }

    private void Close_Click(object sender, RoutedEventArgs e) => CloseSafe();
}
