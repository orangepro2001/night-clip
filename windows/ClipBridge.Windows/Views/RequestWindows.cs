using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace ClipBridge.Windows.Views;

/// <summary>
/// Confirmation of an incoming pairing: the peer's name and platform plus the same 6-digit number the peer shows.
/// Allow / deny go to <c>pair.confirm</c>; closing the window without choosing counts as a denial.
/// </summary>
internal sealed class PairRequestWindow : SmallWindow
{
    private readonly ShellController _shell = ShellController.Current;
    private readonly PairRequestEvent _request;
    private readonly DateTimeOffset? _expires;
    private readonly DispatcherTimer _timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly TextBlock _countdown = Ui.Text("", "caption", "tertiary").With(t => t.HorizontalAlignment = HorizontalAlignment.Center);
    private readonly PairDialogs.StatusLine _status = new();
    private readonly Button _allow;
    private readonly Button _deny;
    private bool _decided;
    private bool _isClosed;

    internal PairRequestWindow(PairRequestEvent request) : base(420)
    {
        _request = request;
        _expires = Countdown.ParseRfc3339(request.ExpiresAt);
        Title = Strings.PairRequestTitle;
        Topmost = true;
        var platform = request.Platform switch
        {
            "windows" => Strings.PlatformWindows, "android" => Strings.PlatformAndroid, "macos" => Strings.PlatformMacOS,
            "" => Strings.Unknown, var p => p,
        };
        var name = request.Name.Length > 0 ? request.Name : request.DeviceId;

        _allow = new Button { Content = Strings.PairAllow, IsDefault = true, MinWidth = 96 };
        _allow.Classes.Add("primary");
        _allow.Click += async (_, _) => await DecideAsync(accept: true);
        _deny = new Button { Content = Strings.PairDeny, MinWidth = 96 };
        _deny.Click += async (_, _) => await DecideAsync(accept: false);

        var tile = new Border { Child = new Icon { NameKey = Ui.PlatformIcon(request.Platform), Size = 20, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center }, Width = 44, Height = 44 };
        tile.Classes.Add("tile");
        tile.Classes.Add("accent");
        var sasCard = new Border
        {
            Padding = new Thickness(16, 14),
            CornerRadius = new CornerRadius(12),
            Child = new StackPanel
            {
                Spacing = 4,
                Children = { Ui.Text(Strings.PairRequestSasPrompt, "medium").With(t => { t.HorizontalAlignment = HorizontalAlignment.Center; t.TextAlignment = TextAlignment.Center; }), new TextBlock { Text = request.Sas, Classes = { "sas" } }, _countdown },
            },
        };
        sasCard[!Border.BackgroundProperty] = sasCard.GetResourceObservable("Lumen.Brush.AccentSubtle").ToBinding();

        SetBody(new StackPanel
        {
            Spacing = 16,
            Children =
            {
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 14, Children = { tile, new StackPanel { VerticalAlignment = VerticalAlignment.Center, Spacing = 2, Children = { Ui.Text(Strings.PairRequestTitle, "subtitle"), Ui.Text(Strings.PairRequestText(name, platform), "secondary") } } } },
                sasCard,
                _status,
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Children = { _deny, _allow } },
            },
        });
        _status.Clear();

        _timer.Tick += (_, _) => UpdateCountdown();
        _shell.EventReceived += OnCoreEvent;
        Opened += (_, _) => { UpdateCountdown(); _timer.Start(); };
        Closing += (_, _) =>
        {
            if (_decided) return;
            _decided = true;                            // the X button / Esc: deny, never leave a request dangling
            _ = Task.Run(() => { try { _shell.Api.PairConfirm(_request.PairId, false); } catch (Interop.CoreException) { } });
        };
        Closed += (_, _) =>
        {
            _isClosed = true;
            _timer.Stop();
            _shell.EventReceived -= OnCoreEvent;
        };
    }

    public void ShowRequest() => ShowNearMain();

    private void CloseSafe()
    {
        if (!_isClosed) Close();
    }

    private void UpdateCountdown()
    {
        if (_expires is not { } expires) return;
        var left = Countdown.Remaining(expires, DateTimeOffset.UtcNow);
        if (left is null)
        {
            _timer.Stop();
            _decided = true;                        // nothing left to confirm
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
            CloseSafe();
        }
    }

    private async Task DecideAsync(bool accept)
    {
        if (_decided) { CloseSafe(); return; }
        _decided = true;
        _allow.IsEnabled = false;
        _deny.IsEnabled = false;
        if (accept) _status.Busy(Strings.PairConfirming);
        var error = await _shell.CallAsync(a => a.PairConfirm(_request.PairId, accept));
        if (_isClosed) return;
        if (error is not null)
        {
            _status.Error(error);                    // e.g. the request expired meanwhile
            _deny.IsEnabled = true;
            _deny.Content = Strings.PairClose;
            return;
        }
        CloseSafe();
    }
}

/// <summary>An incoming file offer: who, what, how big, where to save; accept or reject (nothing is ever auto-accepted).</summary>
internal sealed class FileOfferWindow : SmallWindow
{
    private readonly ShellController _shell = ShellController.Current;
    private readonly FileOfferedEvent _offer;
    private readonly TextBox _dir = new() { IsReadOnly = true };
    private readonly FormField _dirField;
    private readonly Button _accept;
    private readonly Button _reject;
    private readonly Button _change;
    private bool _isClosed;

    internal FileOfferWindow(FileOfferedEvent offer) : base(460)
    {
        _offer = offer;
        Title = Strings.FileOfferTitle;
        var name = FileDisplay.Sanitize(offer.Name);
        var from = offer.FromName.Length > 0 ? offer.FromName : offer.From;
        var image = FileDisplay.IsPreviewableImage(name, offer.Size);

        var tile = new Border { Child = new Icon { NameKey = image ? AppIcons.Image : AppIcons.Of(IconKind.FileText), Size = 20, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center }, Width = 44, Height = 44 };
        tile.Classes.Add("tile");
        tile.Classes.Add("accent");
        var nameText = Ui.Text(FileDisplay.Shorten(name, 70), "subtitle");
        ToolTip.SetTip(nameText, name);

        _dir.Text = _shell.Settings.DownloadDir;
        _change = Ui.Button(AppIcons.Of(IconKind.Folder), Strings.BtnChangeFolder, () => _ = ChangeAsync());
        _change.Margin = new Thickness(8, 0, 0, 0);
        var dirRow = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
        dirRow.Children.Add(_dir);
        Grid.SetColumn(_change, 1);
        dirRow.Children.Add(_change);
        _dirField = new FormField { Label = Strings.FileOfferSaveToLabel, Content = dirRow };

        _accept = new Button { Content = Strings.BtnAccept, IsDefault = true, MinWidth = 96 };
        _accept.Classes.Add("primary");
        _accept.Click += async (_, _) => await AcceptAsync();
        _reject = new Button { Content = Strings.BtnReject, MinWidth = 88 };
        _reject.Click += async (_, _) => await RejectAsync();
        var later = new Button { Content = Strings.FileOfferDecideLater };
        later.Classes.Add("ghost");
        later.Click += (_, _) => CloseSafe();

        var body = new StackPanel
        {
            Spacing = 16,
            Children =
            {
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 14, Children = { tile, new StackPanel { VerticalAlignment = VerticalAlignment.Center, Spacing = 2, MaxWidth = 340, Children = { nameText, Ui.Text(Strings.FileOfferFrom(from) + "  ·  " + Formatting.Bytes(offer.Size), "caption", "tertiary") } } } },
            },
        };
        if (FileDisplay.IsExecutable(name)) body.Children.Add(new InfoBar { Severity = Severity.Warning, Message = Strings.FileOfferDanger });
        if (image && string.Equals(_dir.Text, _shell.Settings.DownloadDir, StringComparison.OrdinalIgnoreCase))
            body.Children.Add(Ui.Text(Strings.FileOfferImageHint, "caption", "tertiary"));
        body.Children.Add(_dirField);
        var footer = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto,Auto") };
        footer.Children.Add(later);
        Grid.SetColumn(_reject, 2);
        footer.Children.Add(_reject);
        _accept.Margin = new Thickness(8, 0, 0, 0);
        Grid.SetColumn(_accept, 3);
        footer.Children.Add(_accept);
        body.Children.Add(footer);
        SetBody(body);

        _shell.EventReceived += OnCoreEvent;
        Closed += (_, _) =>
        {
            _isClosed = true;
            _shell.EventReceived -= OnCoreEvent;
        };
    }

    public void ShowOffer() => ShowNearMain();

    private void CloseSafe()
    {
        if (!_isClosed) Close();
    }

    /// <summary>The offer was decided elsewhere (transfer list, the sender cancelled, it expired): nothing left to ask.</summary>
    private void OnCoreEvent(CoreEvent ev)
    {
        var gone = ev switch
        {
            FileStateEvent s when s.TransferId == _offer.TransferId => s.State != "offered",
            FileFailedEvent f when f.TransferId == _offer.TransferId => true,
            FileDoneEvent d when d.TransferId == _offer.TransferId => true,
            _ => false,
        };
        if (gone) CloseSafe();
    }

    private async Task ChangeAsync()
    {
        var folder = await Ui.PickFolderAsync(this, Strings.FileOfferSaveToLabel, _dir.Text);
        if (folder is not null)
        {
            _dir.Text = folder;
            _dirField.Error = null;
        }
    }

    private async Task AcceptAsync()
    {
        if (!SettingsModel.IsAbsoluteWindowsPath(_dir.Text))
        {
            _dirField.Error = Strings.ValidationDownloadDir;
            return;
        }
        SetBusy(true);
        // The default folder is passed as "no choice" so that an image can be held for the preview (ReceivedImages).
        var chosen = string.Equals(_dir.Text, _shell.Settings.DownloadDir, StringComparison.OrdinalIgnoreCase) ? null : _dir.Text;
        var error = await _shell.AcceptTransferAsync(_offer.TransferId, chosen);
        if (_isClosed) return;
        if (error is null) { CloseSafe(); return; }
        _dirField.Error = error;
        SetBusy(false);
    }

    private async Task RejectAsync()
    {
        SetBusy(true);
        var error = await _shell.RejectTransferAsync(_offer.TransferId);
        if (_isClosed) return;
        if (error is null) { CloseSafe(); return; }
        _dirField.Error = error;
        SetBusy(false);
    }

    private void SetBusy(bool busy)
    {
        _accept.IsEnabled = !busy;
        _reject.IsEnabled = !busy;
        _change.IsEnabled = !busy;
    }
}
