using System.Windows;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// Confirmation of an incoming pairing: the peer's name and platform plus the same 6-digit number the peer shows.
/// Allow / deny go to <c>pair.confirm</c>; closing the window without choosing counts as a denial.
/// </summary>
public partial class PairRequestWindow : Window
{
    private readonly ShellController _shell = ShellController.Current;
    private readonly PairRequestEvent _request;
    private readonly DateTimeOffset? _expires;
    private readonly DispatcherTimer _timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private bool _decided;
    private bool _isClosed;

    internal PairRequestWindow(PairRequestEvent request)
    {
        _request = request;
        _expires = Countdown.ParseRfc3339(request.ExpiresAt);
        InitializeComponent();
        Title = Strings.PairRequestTitle;
        var platform = request.Platform switch { "windows" => Strings.PlatformWindows, "android" => Strings.PlatformAndroid, "macos" => Strings.PlatformMacOS, "" => Strings.Unknown, var p => p };
        RequestText.Text = Strings.PairRequestText(request.Name.Length > 0 ? request.Name : request.DeviceId, platform);
        SasPrompt.Text = Strings.PairRequestSasPrompt;
        SasText.Text = request.Sas;
        AllowButton.Content = Strings.PairAllow;
        DenyButton.Content = Strings.PairDeny;

        _timer.Tick += (_, _) => UpdateCountdown();
        _shell.EventReceived += OnCoreEvent;
        Loaded += (_, _) => { UpdateCountdown(); _timer.Start(); };
        Closing += OnClosing;
        PreviewKeyDown += (_, e) =>
        {
            if (e.Key == System.Windows.Input.Key.Escape) CloseSafe();   // closing without a decision denies
        };
        Closed += (_, _) =>
        {
            _isClosed = true;
            _timer.Stop();
            _shell.EventReceived -= OnCoreEvent;
        };
    }

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
            CountdownText.Text = Strings.PairExpired;
            AllowButton.IsEnabled = false;
            DenyButton.Content = Strings.PairClose;
            return;
        }
        CountdownText.Text = Strings.PairExpiresIn(left);
    }

    private void OnCoreEvent(CoreEvent ev)
    {
        if (ev is PairResultEvent r && r.PairId == _request.PairId)
        {
            _decided = true;
            CloseSafe();
        }
    }

    private async void Allow_Click(object sender, RoutedEventArgs e) => await DecideAsync(accept: true);

    private async void Deny_Click(object sender, RoutedEventArgs e) => await DecideAsync(accept: false);

    private async Task DecideAsync(bool accept)
    {
        if (_decided) { CloseSafe(); return; }
        _decided = true;
        AllowButton.IsEnabled = false;
        DenyButton.IsEnabled = false;
        var error = await _shell.CallAsync(a => a.PairConfirm(_request.PairId, accept));
        if (_isClosed) return;
        if (error is not null)
        {
            StatusText.Text = error;                 // e.g. the request expired meanwhile
            DenyButton.IsEnabled = true;
            DenyButton.Content = Strings.PairClose;
            return;
        }
        CloseSafe();
    }

    private void OnClosing(object? sender, System.ComponentModel.CancelEventArgs e)
    {
        if (_decided) return;
        _decided = true;                            // the X button: deny, never leave a request dangling
        _ = Task.Run(() => { try { _shell.Api.PairConfirm(_request.PairId, false); } catch (Interop.CoreException) { } });
    }
}
