using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Media.Imaging;
using Avalonia.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;
using Spinner = Lumen.UI.Controls.Spinner;

namespace ClipBridge.Windows.Views;

/// <summary>
/// "配对新设备" and "加入配对" open as dialogs over the main window (shown first if it is hidden in the tray).
/// One of each at a time; asking again just brings the window forward.
/// </summary>
internal static class PairDialogs
{
    private static bool _listenOpen;
    private static bool _joinOpen;

    public static async void ShowListen(ShellController shell)
    {
        shell.ShowMainWindow();
        if (_listenOpen || MainWindow.Instance is not { } main) return;
        _listenOpen = true;
        var view = new PairListenView(shell);
        try { await Dialog.ShowAsync(view, main); }
        finally
        {
            _listenOpen = false;
            view.OnClosed();
        }
    }

    public static async void ShowJoin(ShellController shell)
    {
        shell.ShowMainWindow();
        if (_joinOpen || MainWindow.Instance is not { } main) return;
        _joinOpen = true;
        var view = new PairJoinView(shell);
        try { await Dialog.ShowAsync(view, main); }
        finally
        {
            _joinOpen = false;
            view.OnClosed();
        }
    }

    /// <summary>A status line: an icon (or spinner) and a text, recoloured per state.</summary>
    internal sealed class StatusLine : StackPanel
    {
        private readonly Spinner _spinner = new() { Size = 14, Thickness = 2 };
        private readonly Icon _icon = new() { Size = 15 };
        private readonly TextBlock _text = new() { TextWrapping = TextWrapping.Wrap, VerticalAlignment = VerticalAlignment.Center };

        public StatusLine()
        {
            Orientation = Orientation.Horizontal;
            Spacing = 8;
            Children.Add(_spinner);
            Children.Add(_icon);
            Children.Add(_text);
        }

        public void Busy(string text) => Set(text, spinner: true, null, null);
        public void Ok(string text) => Set(text, spinner: false, IconKind.CircleCheck, "success");
        public void Error(string text) => Set(text, spinner: false, IconKind.AlertCircle, "danger");
        public void Info(string text) => Set(text, spinner: false, IconKind.Info, "secondary");
        public void Clear() => Set("", spinner: false, null, null);

        private void Set(string text, bool spinner, IconKind? icon, string? tone)
        {
            _text.Text = text;
            _spinner.IsVisible = spinner;
            _icon.IsVisible = icon is not null;
            if (icon is { } k) _icon.Kind = k;
            _text.Classes.Clear();
            if (tone is not null) _text.Classes.Add(tone);
            var key = tone switch { "success" => "Lumen.Brush.Success", "danger" => "Lumen.Brush.Danger", _ => "Lumen.Brush.TextSecondary" };
            _icon[!Icon.ForegroundProperty] = _icon.GetResourceObservable(key).ToBinding();
            IsVisible = text.Length > 0;
        }
    }
}

/// <summary>
/// Listening side: <c>pair.begin</c>, the QR code from <c>qr.rows</c> (dark on white in both themes, at least 6 px per
/// module, quiet zone included), the link with a copy button and the countdown; <c>pair.cancel</c> when closed early.
/// </summary>
internal sealed class PairListenView : StackPanel
{
    private readonly ShellController _shell;
    private readonly DispatcherTimer _timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly Image _qr = new() { Stretch = Stretch.None, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center };
    private readonly Spinner _qrSpinner = new() { Size = 28, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center };
    private readonly TextBlock _qrMissing = new() { TextWrapping = TextWrapping.Wrap, Foreground = Brushes.DimGray, Margin = new Thickness(16), TextAlignment = TextAlignment.Center, VerticalAlignment = VerticalAlignment.Center, IsVisible = false };
    private readonly TextBlock _countdown = Ui.Text("", "caption", "secondary");
    private readonly TextBox _link = new() { IsReadOnly = true, TextWrapping = TextWrapping.NoWrap };
    private readonly Button _copy;
    private readonly PairDialogs.StatusLine _status = new();
    private string? _sessionId;
    private string _link_ = "";
    private DateTimeOffset? _expires;
    private bool _finished;
    private bool _closed;

    public PairListenView(ShellController shell)
    {
        _shell = shell;
        Spacing = 14;
        Width = 420;
        RenderOptions.SetBitmapInterpolationMode(_qr, BitmapInterpolationMode.None);

        _link.Classes.Add("code");
        _copy = Ui.Button(AppIcons.Of(IconKind.Copy), Strings.PairCopyLink, () => _ = CopyAsync());
        _copy.IsEnabled = false;
        var linkRow = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
        linkRow.Children.Add(_link);
        _copy.Margin = new Thickness(8, 0, 0, 0);
        Grid.SetColumn(_copy, 1);
        linkRow.Children.Add(_copy);

        // White plate in both themes: phone cameras read dark-on-light codes most reliably.
        var plate = new Border
        {
            MinWidth = 236,
            MinHeight = 236,
            Padding = new Thickness(6),
            Background = Brushes.White,
            CornerRadius = new CornerRadius(14),
            HorizontalAlignment = HorizontalAlignment.Center,
            Child = new Panel { Children = { _qr, _qrSpinner, _qrMissing } },
        };
        plate[!Border.BorderBrushProperty] = plate.GetResourceObservable("Lumen.Brush.Border").ToBinding();
        plate.BorderThickness = new Thickness(1);

        var close = new Button { Content = Strings.PairClose, IsCancel = true, MinWidth = 88 };
        close.Click += (_, _) => Dialog.Close(this);
        var join = Ui.Button(AppIcons.Scan, Strings.PairJoinInstead, () => { Dialog.Close(this); PairDialogs.ShowJoin(_shell); }, "ghost");
        var footer = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto") };
        footer.Children.Add(join);
        Grid.SetColumn(close, 2);
        footer.Children.Add(close);

        Children.Add(new StackPanel { Spacing = 4, Children = { Ui.Text(Strings.PairListenTitle, "title"), Ui.Text(Strings.PairListenIntro, "secondary") } });
        Children.Add(plate);
        Children.Add(new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 6,
            HorizontalAlignment = HorizontalAlignment.Center,
            Children = { Ui.Icon(AppIcons.Of(IconKind.Clock), 13), _countdown },
        });
        Children.Add(new FormField { Label = Strings.PairLinkLabel, Content = linkRow });
        Children.Add(_status);
        Children.Add(footer);

        _status.Busy(Strings.PairStarting);
        _timer.Tick += (_, _) => UpdateCountdown();
        _shell.EventReceived += OnCoreEvent;
        AttachedToVisualTree += async (_, _) =>
        {
            if (_sessionId is null && !_closed) await BeginAsync();
        };
    }

    private async Task BeginAsync()
    {
        _sessionId = "";
        var (begin, error) = await _shell.CallAsync(a => a.PairBegin());
        if (_closed) return;
        _qrSpinner.IsVisible = false;
        if (error is not null || begin is null)
        {
            _status.Error(error ?? Strings.PairFailed);
            _qrMissing.Text = Strings.PairQrUnavailable;
            _qrMissing.IsVisible = true;
            _countdown.Text = "";
            return;
        }

        _sessionId = begin.SessionId;
        _link_ = begin.Link;
        _expires = begin.Expires;
        _link.Text = begin.Link;
        _copy.IsEnabled = true;
        _status.Busy(Strings.PairWaitingForPeer);

        var bitmap = QrRenderer.Render(begin.QrRows, begin.QrSize, 228);
        if (bitmap is null)
        {
            _qrMissing.Text = Strings.PairQrUnavailable;
            _qrMissing.IsVisible = true;
        }
        else _qr.Source = bitmap.ToAvalonia();
        UpdateCountdown();
        _timer.Start();
    }

    private void UpdateCountdown()
    {
        if (_expires is not { } expires) { _countdown.Text = ""; return; }
        var left = Countdown.Remaining(expires, DateTimeOffset.UtcNow);
        if (left is null)
        {
            _timer.Stop();
            _countdown.Text = Strings.PairExpired;
            _qr.Opacity = 0.15;
            if (!_finished) _status.Error(Strings.ErrPairingExpired);
            return;
        }
        _countdown.Text = Strings.PairExpiresIn(left);
    }

    private void OnCoreEvent(CoreEvent ev)
    {
        if (_closed || ev is not PairResultEvent { Role: "listener" } result) return;
        if (result.State == "accepted")
        {
            _finished = true;
            _timer.Stop();
            _status.Ok(Strings.PairSuccessWith(result.Name.Length > 0 ? result.Name : Strings.Unknown));
            DispatcherTimer.RunOnce(() => { if (!_closed) Dialog.Close(this); }, TimeSpan.FromSeconds(1.6));
        }
        else _status.Error(ErrorMessages.ForPairResult(result.State, result.Code));
    }

    private async Task CopyAsync()
    {
        if (_link_.Length == 0) return;
        var ok = await _shell.CopyTextAsync(_link_);   // not synced to other devices as if the user had copied it
        if (ok) Toast.Success(Strings.PairLinkCopied, anchor: this);
        else _status.Error(Strings.ApplyFailed);
    }

    /// <summary>The dialog went away (closed, Esc, replaced): stop and cancel the session unless it finished.</summary>
    public void OnClosed()
    {
        if (_closed) return;
        _closed = true;
        _timer.Stop();
        _shell.EventReceived -= OnCoreEvent;
        if (_sessionId is { Length: > 0 } id && !_finished)
            _ = Task.Run(() => { try { _shell.Api.PairCancel(id); } catch (Interop.CoreException) { } });
    }
}

/// <summary>Joining side: paste a link or type host / port / key, then compare the large 6-digit number.</summary>
internal sealed class PairJoinView : StackPanel
{
    private readonly ShellController _shell;
    private readonly PairJoinSession _session = new();
    private readonly SegmentedControl _mode = new() { Classes = { "block" } };
    private readonly TextBox _linkBox = new() { AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, MinHeight = 72, PlaceholderText = "clipbridge://pair?…" };
    private readonly FormField _linkField;
    private readonly StackPanel _linkPanel;
    private readonly Grid _manualPanel;
    private readonly TextBox _host = new() { PlaceholderText = "100.x.y.z" };
    private readonly TextBox _port = new() { Text = "47821" };
    private readonly TextBox _psk = new();
    private readonly TextBox _fp = new();
    private readonly StackPanel _sasPanel;
    private readonly TextBlock _sas = new() { Classes = { "sas" } };
    private readonly PairDialogs.StatusLine _status = new();
    private readonly Button _start;
    private bool _closed;

    public PairJoinView(ShellController shell)
    {
        _shell = shell;
        Spacing = 14;
        Width = 440;

        _mode.Items.Add(new SegmentedItem { Content = Strings.PairTabLink });
        _mode.Items.Add(new SegmentedItem { Content = Strings.PairTabManual });
        _mode.SelectedIndex = 0;
        _mode.SelectionChanged += (_, _) => ApplyMode();

        _linkBox.Classes.Add("code");
        _linkBox.TextChanged += (_, _) => _linkField!.Error = null;
        _linkField = new FormField { Label = Strings.PairLinkLabel, Content = _linkBox };
        var paste = Ui.Button(AppIcons.Clipboard, Strings.PairPasteFromClipboard, Paste, "ghost", "sm");
        _linkPanel = new StackPanel { Spacing = 6, Children = { _linkField, paste } };

        _manualPanel = new Grid { ColumnDefinitions = new ColumnDefinitions("*,12,110"), RowDefinitions = new RowDefinitions("Auto,12,Auto,12,Auto"), IsVisible = false };
        Place(_manualPanel, new FormField { Label = Strings.PairHostLabel, Content = _host }, 0, 0);
        Place(_manualPanel, new FormField { Label = Strings.PairPortLabel, Content = _port }, 0, 2);
        var psk = new FormField { Label = Strings.PairPskLabel, Content = _psk };
        Place(_manualPanel, psk, 2, 0);
        Grid.SetColumnSpan(psk, 3);
        var fp = new FormField { Label = Strings.PairFpLabel, Content = _fp };
        Place(_manualPanel, fp, 4, 0);
        Grid.SetColumnSpan(fp, 3);

        _sasPanel = new StackPanel
        {
            Spacing = 6,
            IsVisible = false,
            Children = { Ui.Text(Strings.PairSasPrompt, "medium").With(t => t.HorizontalAlignment = HorizontalAlignment.Center), _sas, Ui.Text(Strings.PairSasWaiting, "caption", "tertiary").With(t => { t.HorizontalAlignment = HorizontalAlignment.Center; t.TextAlignment = TextAlignment.Center; }) },
        };
        var sasCard = new Border { Padding = new Thickness(16, 14), CornerRadius = new CornerRadius(12), Child = _sasPanel };
        sasCard[!Border.BackgroundProperty] = sasCard.GetResourceObservable("Lumen.Brush.AccentSubtle").ToBinding();
        sasCard.Bind(IsVisibleProperty, _sasPanel.GetObservable(IsVisibleProperty));

        _start = new Button { Content = Strings.PairStartJoin, IsDefault = true, MinWidth = 100 };
        _start.Classes.Add("primary");
        _start.Click += async (_, _) => await StartAsync();
        var close = new Button { Content = Strings.PairClose, IsCancel = true, MinWidth = 88 };
        close.Click += (_, _) => Dialog.Close(this);

        Children.Add(new StackPanel { Spacing = 4, Children = { Ui.Text(Strings.PairJoinTitle, "title"), Ui.Text(Strings.PairJoinIntro, "secondary") } });
        Children.Add(_mode);
        Children.Add(_linkPanel);
        Children.Add(_manualPanel);
        Children.Add(sasCard);
        Children.Add(_status);
        Children.Add(new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Children = { close, _start } });

        _status.Clear();
        _shell.EventReceived += OnCoreEvent;
        AttachedToVisualTree += (_, _) => _linkBox.Focus();
    }

    private static void Place(Grid grid, Control child, int row, int column)
    {
        Grid.SetRow(child, row);
        Grid.SetColumn(child, column);
        grid.Children.Add(child);
    }

    private void ApplyMode()
    {
        _linkPanel.IsVisible = _mode.SelectedIndex == 0;
        _manualPanel.IsVisible = _mode.SelectedIndex == 1;
    }

    private void Paste()
    {
        var read = new ClipboardAccess().ReadText();
        if (read.Status == ClipReadStatus.Ok) _linkBox.Text = read.Text.Trim();
        else _status.Error(read.Status == ClipReadStatus.Busy ? Strings.ApplyFailed : Strings.PairClipboardNoText);
    }

    private async Task StartAsync()
    {
        var input = _mode.SelectedIndex == 0
            ? PairingInput.FromLink(_linkBox.Text, DateTimeOffset.UtcNow)
            : PairingInput.FromManual(_host.Text, _port.Text, _psk.Text, _fp.Text);
        if (!input.Ok)
        {
            if (_mode.SelectedIndex == 0) _linkField.Error = input.Error ?? Strings.PairFailed;
            else _status.Error(input.Error ?? Strings.PairFailed);
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
        if (!_closed && _session.OnEvent(ev)) Render();
    }

    private void Render()
    {
        var busy = _session.Phase is PairJoinPhase.Joining or PairJoinPhase.AwaitingSas;
        var done = _session.Phase == PairJoinPhase.Accepted;
        _mode.IsEnabled = _linkPanel.IsEnabled = _manualPanel.IsEnabled = !busy && !done;
        _start.IsEnabled = !busy && !done;
        _sasPanel.IsVisible = _session.Phase == PairJoinPhase.AwaitingSas;

        switch (_session.Phase)
        {
            case PairJoinPhase.Joining:
                _status.Busy(Strings.PairJoining);
                break;
            case PairJoinPhase.AwaitingSas:
                _sas.Text = _session.Sas;
                _status.Busy(Strings.PairSasWaitingShort);
                break;
            case PairJoinPhase.Accepted:
                _status.Ok(Strings.PairSuccessWith(_session.PeerName.Length > 0 ? _session.PeerName : Strings.Unknown));
                DispatcherTimer.RunOnce(() => { if (!_closed) Dialog.Close(this); }, TimeSpan.FromSeconds(1.6));
                break;
            case PairJoinPhase.Failed:
                _status.Error(_session.ErrorMessage);
                break;
        }
    }

    public void OnClosed()
    {
        _closed = true;
        _shell.EventReceived -= OnCoreEvent;
    }
}
