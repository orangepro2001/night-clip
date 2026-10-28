using System.Runtime.InteropServices;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Media.Imaging;
using Avalonia.Platform;
using Avalonia.Threading;
using ClipBridge.Mac.Platform;
using ClipBridge.Windows;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

/// <summary>
/// Listening side of pairing (same flow as the Windows PairListenWindow): pair.begin, QR code from qr.rows,
/// the link with a copy button and a countdown, pair.cancel when the window goes away unfinished.
/// </summary>
internal sealed class PairListenWindow : Window
{
    private static PairListenWindow? _instance;

    public static void ShowOrActivate()
    {
        if (_instance is { } open) { open.Activate(); return; }
        _instance = new PairListenWindow();
        _instance.Show();
    }

    private readonly MacController _shell = MacController.Current;
    private readonly DispatcherTimer _timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly Image _qr = new() { Width = 280, Height = 280 };
    private readonly TextBlock _status = Ui.Text(Strings.PairStarting);
    private readonly TextBlock _countdown = Ui.Dim("");
    private readonly TextBox _linkBox = new() { IsReadOnly = true, TextWrapping = TextWrapping.Wrap, FontSize = 11 };
    private string? _sessionId;
    private string _link = "";
    private DateTimeOffset? _expires;
    private bool _finished;
    private bool _closed;

    private PairListenWindow()
    {
        Title = Strings.PairListenTitle;
        Width = 420;
        SizeToContent = SizeToContent.Height;
        CanResize = false;
        Content = Ui.Column(12,
            Ui.Text(Strings.PairListenIntro),
            new Border { Child = _qr, HorizontalAlignment = HorizontalAlignment.Center, Background = Brushes.White, Padding = new Thickness(8), CornerRadius = new CornerRadius(6) },
            _countdown,
            _linkBox,
            Ui.Row(Ui.Button(Strings.PairCopyLink, CopyAsync), Ui.Button(Strings.PairClose, Close)),
            _status).With(p => p.Margin = new Thickness(20));
        _timer.Tick += (_, _) => UpdateCountdown();
        _shell.EventReceived += OnCoreEvent;
        Opened += async (_, _) => await BeginAsync();
        Closed += OnClosed;
    }

    private async Task BeginAsync()
    {
        var (begin, error) = await _shell.CallAsync(a => a.PairBegin());
        if (error is not null || begin is null)
        {
            _status.Text = error ?? Strings.PairFailed;
            return;
        }
        _sessionId = begin.SessionId;
        _link = begin.Link;
        _expires = begin.Expires;
        _linkBox.Text = begin.Link;
        _status.Text = Strings.PairWaitingForPeer;
        _qr.Source = ToBitmap(QrRenderer.Render(begin.QrRows, begin.QrSize, 560));
        if (_qr.Source is null) _status.Text = Strings.PairQrUnavailable;
        UpdateCountdown();
        _timer.Start();
    }

    internal static WriteableBitmap? ToBitmap(QrRenderer.Bitmap? bitmap)
    {
        if (bitmap is null) return null;
        var wb = new WriteableBitmap(new PixelSize(bitmap.Width, bitmap.Height), new Vector(96, 96), PixelFormat.Bgra8888, AlphaFormat.Premul);
        using var fb = wb.Lock();
        for (var y = 0; y < bitmap.Height; y++)
            Marshal.Copy(bitmap.Bgra32, y * bitmap.Width * 4, fb.Address + y * fb.RowBytes, bitmap.Width * 4);
        return wb;
    }

    private void UpdateCountdown()
    {
        if (_expires is not { } expires) { _countdown.Text = ""; return; }
        var left = Countdown.Remaining(expires, DateTimeOffset.UtcNow);
        if (left is null)
        {
            _timer.Stop();
            _countdown.Text = Strings.PairExpired;
            _qr.Opacity = 0.2;
            _status.Text = Strings.ErrPairingExpired;
            return;
        }
        _countdown.Text = Strings.PairExpiresIn(left);
    }

    private void OnCoreEvent(CoreEvent ev)
    {
        if (ev is not PairResultEvent { Role: "listener" } result) return;
        if (result.State == "accepted")
        {
            _finished = true;
            _timer.Stop();
            _status.Foreground = Ui.OkBrush;
            _status.Text = Strings.PairSuccessWith(result.Name.Length > 0 ? result.Name : Strings.Unknown);
            DispatcherTimer.RunOnce(() => { if (!_closed) Close(); }, TimeSpan.FromSeconds(1.5));
        }
        else _status.Text = ErrorMessages.ForPairResult(result.State, result.Code);
    }

    private async Task CopyAsync()
    {
        if (_link.Length == 0) return;
        _status.Text = await _shell.CopyTextAsync(_link) ? Strings.PairLinkCopied : Strings.ApplyFailed;
    }

    private void OnClosed(object? sender, EventArgs e)
    {
        _closed = true;
        _timer.Stop();
        _shell.EventReceived -= OnCoreEvent;
        _instance = null;
        if (_sessionId is { } id && !_finished)
            _ = Task.Run(() => { try { _shell.Api.PairCancel(id); } catch (CoreException) { } });
    }
}

/// <summary>Joining side: paste the other device's link (or type host/port/secret), compare the digits, wait.</summary>
internal sealed class PairJoinWindow : Window
{
    private static PairJoinWindow? _instance;

    public static void ShowOrActivate()
    {
        if (_instance is { } open) { open.Activate(); return; }
        _instance = new PairJoinWindow();
        _instance.Show();
    }

    private readonly MacController _shell = MacController.Current;
    private readonly PairJoinSession _session = new();
    private readonly TextBox _link = new() { PlaceholderText = "clipbridge://pair?…", AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, MinHeight = 60 };
    private readonly TextBox _host = new() { PlaceholderText = Strings.PairHostLabel };
    private readonly TextBox _port = new() { PlaceholderText = Strings.PairPortLabel, Text = "47821" };
    private readonly TextBox _psk = new() { PlaceholderText = Strings.PairPskLabel };
    private readonly TextBox _fp = new() { PlaceholderText = Strings.PairFpLabel };
    private readonly TabControl _tabs = new();
    private readonly Button _start;
    private readonly TextBlock _status = Ui.Text("");
    private readonly TextBlock _sas = Ui.Text("", 32, FontWeight.Bold);
    private readonly StackPanel _sasPanel;
    private bool _closed;

    private PairJoinWindow()
    {
        Title = Strings.PairJoinTitle;
        Width = 460;
        SizeToContent = SizeToContent.Height;
        CanResize = false;
        _tabs.ItemsSource = new[]
        {
            new TabItem { Header = Strings.PairTabLink, Content = Ui.Column(8, Ui.Text(Strings.PairLinkLabel), _link, Ui.Button(Strings.PairPasteFromClipboard, Paste)) },
            new TabItem { Header = Strings.PairTabManual, Content = Ui.Column(8, _host, _port, _psk, _fp) },
        };
        _tabs.SelectedIndex = 0;
        _start = Ui.Button(Strings.PairStartJoin, StartAsync);
        _sasPanel = Ui.Column(6, Ui.Text(Strings.PairSasPrompt), _sas, Ui.Dim(Strings.PairSasWaiting));
        _sasPanel.IsVisible = false;
        Content = Ui.Column(12, Ui.Text(Strings.PairJoinIntro), _tabs, Ui.Row(_start, Ui.Button(Strings.PairClose, Close)), _sasPanel, _status)
            .With(p => p.Margin = new Thickness(20));
        _shell.EventReceived += OnCoreEvent;
        Closed += (_, _) => { _closed = true; _shell.EventReceived -= OnCoreEvent; _instance = null; };
    }

    private void Paste()
    {
        var read = MacPasteboard.ReadText();
        if (read.Status == PasteReadStatus.Ok) _link.Text = read.Text;
    }

    private async Task StartAsync()
    {
        var input = _tabs.SelectedIndex == 0
            ? PairingInput.FromLink(_link.Text, DateTimeOffset.UtcNow)
            : PairingInput.FromManual(_host.Text, _port.Text, _psk.Text, _fp.Text);
        if (!input.Ok)
        {
            ShowError(input.Error ?? Strings.PairFailed);
            return;
        }
        _session.Begin();
        Render();
        var (joinId, error) = await _shell.CallAsync(a => a.PairJoin(input.Request!));
        if (_closed) return;
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
        _status.Foreground = Ui.ErrorBrush;
        _status.Text = message;
    }

    private void Render()
    {
        var busy = _session.Phase is PairJoinPhase.Joining or PairJoinPhase.AwaitingSas;
        _tabs.IsEnabled = !busy && _session.Phase != PairJoinPhase.Accepted;
        _start.IsEnabled = !busy && _session.Phase != PairJoinPhase.Accepted;
        _sasPanel.IsVisible = _session.Phase == PairJoinPhase.AwaitingSas;
        switch (_session.Phase)
        {
            case PairJoinPhase.Joining:
                _status.ClearValue(TextBlock.ForegroundProperty);
                _status.Text = Strings.PairJoining;
                break;
            case PairJoinPhase.AwaitingSas:
                _sas.Text = _session.Sas;
                _status.Text = "";
                break;
            case PairJoinPhase.Accepted:
                _status.Foreground = Ui.OkBrush;
                _status.Text = Strings.PairSuccessWith(_session.PeerName.Length > 0 ? _session.PeerName : Strings.Unknown);
                DispatcherTimer.RunOnce(() => { if (!_closed) Close(); }, TimeSpan.FromSeconds(1.5));
                break;
            case PairJoinPhase.Failed:
                ShowError(_session.ErrorMessage);
                break;
        }
    }
}

/// <summary>Someone asks to pair with this Mac: show the digits, allow or deny. Closing without a decision denies.</summary>
internal sealed class PairRequestWindow : Window
{
    private static readonly Dictionary<string, PairRequestWindow> Open_ = new();

    public static void Open(PairRequestEvent p)
    {
        if (p.PairId.Length == 0 || Open_.ContainsKey(p.PairId)) return;
        var w = new PairRequestWindow(p);
        Open_[p.PairId] = w;
        w.Closed += (_, _) => Open_.Remove(p.PairId);
        w.Show();
        w.Activate();
    }

    private readonly MacController _shell = MacController.Current;
    private readonly PairRequestEvent _request;
    private readonly DateTimeOffset? _expires;
    private readonly DispatcherTimer _timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly TextBlock _countdown = Ui.Dim("");
    private readonly TextBlock _status = Ui.Text("");
    private readonly Button _allow;
    private readonly Button _deny;
    private bool _decided;
    private bool _closed;

    private PairRequestWindow(PairRequestEvent request)
    {
        _request = request;
        _expires = Countdown.ParseRfc3339(request.ExpiresAt);
        Title = Strings.PairRequestTitle;
        Width = 400;
        SizeToContent = SizeToContent.Height;
        CanResize = false;
        Topmost = true;
        var platform = request.Platform switch
        {
            "windows" => Strings.PlatformWindows, "android" => Strings.PlatformAndroid, "macos" => Strings.PlatformMacOS,
            "" => Strings.Unknown, var p => p,
        };
        _allow = Ui.Button(Strings.PairAllow, () => DecideAsync(true));
        _deny = Ui.Button(Strings.PairDeny, () => DecideAsync(false));
        Content = Ui.Column(12,
            Ui.Text(Strings.PairRequestText(request.Name.Length > 0 ? request.Name : request.DeviceId, platform)),
            Ui.Text(Strings.PairRequestSasPrompt),
            Ui.Text(request.Sas, 32, FontWeight.Bold),
            _countdown,
            Ui.Row(_deny, _allow),
            _status).With(p => p.Margin = new Thickness(20));
        _timer.Tick += (_, _) => UpdateCountdown();
        _shell.EventReceived += OnCoreEvent;
        Opened += (_, _) => { UpdateCountdown(); _timer.Start(); };
        Closing += (_, _) =>
        {
            if (_decided) return;
            _decided = true;
            _ = Task.Run(() => { try { _shell.Api.PairConfirm(_request.PairId, false); } catch (CoreException) { } });
        };
        Closed += (_, _) => { _closed = true; _timer.Stop(); _shell.EventReceived -= OnCoreEvent; };
    }

    private void UpdateCountdown()
    {
        if (_expires is not { } expires) return;
        var left = Countdown.Remaining(expires, DateTimeOffset.UtcNow);
        if (left is null)
        {
            _timer.Stop();
            _decided = true;
            _countdown.Text = Strings.PairExpired;
            _allow.IsEnabled = false;
            _deny.Content = Strings.PairClose;
            return;
        }
        _countdown.Text = Strings.PairExpiresIn(left);
    }

    private void OnCoreEvent(CoreEvent ev)
    {
        if (ev is PairResultEvent r && r.PairId == _request.PairId)
        {
            _decided = true;
            if (!_closed) Close();
        }
    }

    private async Task DecideAsync(bool accept)
    {
        if (_decided) { if (!_closed) Close(); return; }
        _decided = true;
        var error = await _shell.CallAsync(a => a.PairConfirm(_request.PairId, accept));
        if (_closed) return;
        if (error is not null)
        {
            _status.Text = error;
            _deny.Content = Strings.PairClose;
            return;
        }
        Close();
    }
}

/// <summary>An incoming file: accept (into the download folder, or a chosen one) or decline; closing decides later.</summary>
internal sealed class FileOfferWindow : Window
{
    public static void Open(FileOfferedEvent offer, Action onClosed)
    {
        var w = new FileOfferWindow(offer);
        w.Closed += (_, _) => onClosed();
        w.Show();
        w.Activate();
    }

    private readonly MacController _shell = MacController.Current;
    private readonly FileOfferedEvent _offer;
    private readonly TextBlock _status = Ui.Text("");
    private string? _saveDir;

    private FileOfferWindow(FileOfferedEvent offer)
    {
        _offer = offer;
        Title = Strings.FileOfferTitle;
        Width = 440;
        SizeToContent = SizeToContent.Height;
        CanResize = false;
        var from = offer.FromName.Length > 0 ? offer.FromName : offer.From;
        var dir = Ui.Dim(Strings.FileOfferSaveTo + _shell.Settings.DownloadDir);
        var choose = Ui.Button(MacStrings.SettingsChooseFolder, async () =>
        {
            if (await Ui.PickFolderAsync(this, Strings.FileOfferSaveTo) is { } picked)
            {
                _saveDir = picked;
                dir.Text = Strings.FileOfferSaveTo + picked;
            }
        });
        var panel = Ui.Column(10,
            Ui.Text(Strings.FileOfferFrom(from)),
            Ui.Text(FileDisplay.Shorten(offer.Name, 80) + "  (" + Formatting.Bytes(offer.Size) + ")", 14, FontWeight.SemiBold),
            Ui.Row(dir, choose));
        if (FileDisplay.IsExecutable(offer.Name)) panel.Children.Add(Ui.Text(Strings.FileOfferDanger).With(t => t.Foreground = Ui.ErrorBrush));
        panel.Children.Add(Ui.Row(
            Ui.Button(Strings.BtnReject, async () => { if (await Report(_shell.RejectTransferAsync(_offer.TransferId))) Close(); }),
            Ui.Button(Strings.FileOfferDecideLater, Close),
            Ui.Button(Strings.BtnAccept, async () => { if (await Report(_shell.AcceptTransferAsync(_offer.TransferId, _saveDir))) Close(); })));
        panel.Children.Add(_status);
        Content = panel.With(p => p.Margin = new Thickness(20));
        Topmost = true;
    }

    private async Task<bool> Report(Task<string?> action)
    {
        var error = await action;
        if (error is null) return true;
        _status.Text = error;
        return false;
    }
}
