using System.Windows;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// Listening side of pairing: <c>pair.begin</c>, draw the QR code from <c>qr.rows</c> (at least 6 px per
/// module, quiet zone included in the matrix), show the link with a copy button and the countdown, and
/// <c>pair.cancel</c> when the window goes away.
/// </summary>
public partial class PairListenWindow : Window
{
    private static PairListenWindow? _instance;

    internal static void ShowOrActivate()
    {
        if (_instance is { } open)
        {
            open.Activate();
            return;
        }
        _instance = new PairListenWindow();
        _instance.Show();
    }

    private readonly ShellController _shell = ShellController.Current;
    private readonly DispatcherTimer _timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private string? _sessionId;
    private string _link = "";
    private DateTimeOffset? _expires;
    private bool _finished;
    private bool _isClosed;

    private PairListenWindow()
    {
        InitializeComponent();
        Title = Strings.PairListenTitle;
        IntroText.Text = Strings.PairListenIntro;
        CopyButton.Content = Strings.PairCopyLink;
        CloseButton.Content = Strings.PairClose;
        StatusText.Text = Strings.PairStarting;
        QrMissing.Text = Strings.PairQrUnavailable;
        if (MainWindow.Instance is { IsVisible: true } owner) { Owner = owner; WindowStartupLocation = WindowStartupLocation.CenterOwner; }

        _timer.Tick += (_, _) => UpdateCountdown();
        _shell.EventReceived += OnCoreEvent;
        Loaded += async (_, _) => await BeginAsync();
        Closed += OnClosed;
        PreviewKeyDown += (_, e) => { if (e.Key == System.Windows.Input.Key.Escape) CloseSafe(); };
    }

    private void CloseSafe()
    {
        if (!_isClosed) Close();
    }

    private async Task BeginAsync()
    {
        var (begin, error) = await _shell.CallAsync(a => a.PairBegin());
        if (error is not null || begin is null)
        {
            StatusText.Text = error ?? Strings.PairFailed;
            QrMissing.Visibility = Visibility.Visible;
            QrMissing.Text = "";
            CountdownText.Text = "";
            return;
        }

        _sessionId = begin.SessionId;
        _link = begin.Link;
        _expires = begin.Expires;
        LinkBox.Text = begin.Link;
        StatusText.Text = Strings.PairWaitingForPeer;

        var bitmap = QrRenderer.Render(begin.QrRows, begin.QrSize, 300);
        if (bitmap is null)
        {
            QrMissing.Visibility = Visibility.Visible;
        }
        else
        {
            var wb = new WriteableBitmap(bitmap.Width, bitmap.Height, 96, 96, PixelFormats.Bgra32, null);
            wb.WritePixels(new Int32Rect(0, 0, bitmap.Width, bitmap.Height), bitmap.Bgra32, bitmap.Width * 4, 0);
            wb.Freeze();
            QrImage.Source = wb;
            QrMissing.Visibility = Visibility.Collapsed;
        }
        UpdateCountdown();
        _timer.Start();
    }

    private void UpdateCountdown()
    {
        if (_expires is not { } expires) { CountdownText.Text = ""; return; }
        var left = Countdown.Remaining(expires, DateTimeOffset.UtcNow);
        if (left is null)
        {
            _timer.Stop();
            CountdownText.Text = Strings.PairExpired;
            QrImage.Opacity = 0.2;
            StatusText.Text = Strings.ErrPairingExpired;
            return;
        }
        CountdownText.Text = Strings.PairExpiresIn(left);
    }

    private void OnCoreEvent(CoreEvent ev)
    {
        if (ev is not PairResultEvent { Role: "listener" } result) return;
        switch (result.State)
        {
            case "accepted":
                _finished = true;
                _timer.Stop();
                StatusText.Text = Strings.PairSuccessWith(result.Name.Length > 0 ? result.Name : Strings.Unknown);
                var close = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1.5) };
                close.Tick += (_, _) => { close.Stop(); CloseSafe(); };
                close.Start();
                break;
            default:
                StatusText.Text = ErrorMessages.ForPairResult(result.State, result.Code);
                break;
        }
    }

    private async void Copy_Click(object sender, RoutedEventArgs e)
    {
        if (_link.Length == 0) return;
        var ok = await _shell.CopyTextAsync(_link);   // not synced to other devices as if the user had copied it
        StatusText.Text = ok ? Strings.PairLinkCopied : Strings.ApplyFailed;
    }

    private void Close_Click(object sender, RoutedEventArgs e) => CloseSafe();

    private void OnClosed(object? sender, EventArgs e)
    {
        _isClosed = true;
        _timer.Stop();
        _shell.EventReceived -= OnCoreEvent;
        _instance = null;
        if (_sessionId is { } id && !_finished)
            _ = Task.Run(() => { try { _shell.Api.PairCancel(id); } catch (Interop.CoreException) { } });
    }
}
