using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>The locked screen (unlock) and the first-run screen (create). Thin: every decision is in Logic/VaultAuth.cs.</summary>
public partial class VaultLockPanel : UserControl
{
    private ShellController _shell = null!;
    private readonly RateLimitCountdown _rate = new();
    private readonly DispatcherTimer _tick = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly DispatcherTimer _strengthTimer = new() { Interval = TimeSpan.FromMilliseconds(250) };
    private int _strengthGeneration;
    private string? _strengthLabel;
    private bool _busy;

    /// <summary>Unlocked or created: the page should show the vault. The argument tells whether it was just created.</summary>
    public event Action<bool>? Succeeded;

    public VaultLockPanel()
    {
        InitializeComponent();
        _tick.Tick += (_, _) => OnTick();
        _strengthTimer.Tick += async (_, _) => { _strengthTimer.Stop(); await UpdateStrengthAsync(); };
        UnlockCreds.Submit += () => Unlock_Click(this, new RoutedEventArgs());
        CreateConfirm.Caption = Strings.LabelConfirmPassword;
        CreateConfirm.ShowKeyFile = false;
        CreateConfirm.Submit += () => Create_Click(this, new RoutedEventArgs());
        CreateCreds.Submit += () => CreateConfirm.FocusPassword();
        CreateCreds.PasswordChanged += () => { _strengthTimer.Stop(); _strengthTimer.Start(); };
    }

    internal void Attach(ShellController shell) => _shell = shell;

    // ---- screens ---------------------------------------------------------------------------

    /// <summary>Shows the unlock screen. <paramref name="note"/> explains an automatic lock; <paramref name="retryAfterSeconds"/> starts the wait.</summary>
    public void ShowLocked(string note = "", int retryAfterSeconds = 0)
    {
        CreatePane.Visibility = Visibility.Collapsed;
        UnlockPane.Visibility = Visibility.Visible;
        ClearAll();
        LockedNote.Text = note;
        LockedNote.Visibility = note.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        UnlockStatus.Text = "";
        UnlockCreds.ShowError(null);
        SetBusy(false);
        if (retryAfterSeconds > 0) StartWait(retryAfterSeconds);
        UnlockCreds.FocusPassword();
    }

    public void ShowCreate()
    {
        UnlockPane.Visibility = Visibility.Collapsed;
        CreatePane.Visibility = Visibility.Visible;
        ClearAll();
        CreateError.Visibility = Visibility.Collapsed;
        CreateWeak.Visibility = Visibility.Collapsed;
        CreateStatus.Text = "";
        CreateStrength.Clear();
        CreateButton.IsEnabled = true;
        CreateCreds.FocusPassword();
    }

    /// <summary>Drops every password and key file held by the boxes (called on lock, on success and when leaving).</summary>
    public void ClearAll()
    {
        UnlockCreds.Clear(keyFileToo: true);
        CreateCreds.Clear(keyFileToo: true);
        CreateConfirm.Clear();
        _tick.Stop();
        _rate.Clear();
    }

    // ---- unlock ----------------------------------------------------------------------------

    private async void Unlock_Click(object sender, RoutedEventArgs e)
    {
        if (_busy || _rate.IsBlocked(DateTimeOffset.UtcNow)) return;
        if (UnlockCreds.Password.Length == 0) { UnlockCreds.FocusPassword(); return; }
        var creds = UnlockCreds.Creds;
        SetBusy(true);
        UnlockStatus.Foreground = System.Windows.SystemColors.ControlTextBrush;
        UnlockStatus.Text = Strings.Unlocking;
        UnlockCreds.ShowError(null);
        var result = await _shell.VaultCallAsync(a => a.Unlock(creds));
        UnlockCreds.Clear(); // the password is never kept after use
        SetBusy(false);
        if (result.Ok)
        {
            UnlockStatus.Text = "";
            var slow = result.Value > 3000;
            UnlockCreds.Clear(keyFileToo: true);
            Succeeded?.Invoke(false);
            if (slow) _shell.ShowStatus(Strings.UnlockSlowDevice);
            return;
        }
        var outcome = UnlockOutcome.FromError(result.ErrorCode ?? "", result.RetryAfterSeconds);
        UnlockStatus.Text = "";
        UnlockCreds.ShowError(outcome.Message);
        if (outcome.RetryAfterSeconds > 0) StartWait(outcome.RetryAfterSeconds);
        else UnlockCreds.FocusPassword();
    }

    private void SetBusy(bool busy)
    {
        _busy = busy;
        UnlockButton.IsEnabled = !busy && !_rate.IsBlocked(DateTimeOffset.UtcNow);
    }

    private void StartWait(int seconds)
    {
        _rate.Start(seconds, DateTimeOffset.UtcNow);
        UnlockButton.IsEnabled = false;
        UnlockButton.Content = Strings.UnlockWait(seconds);
        _tick.Start();
    }

    private void OnTick()
    {
        var left = _rate.SecondsLeft(DateTimeOffset.UtcNow);
        if (left > 0)
        {
            UnlockButton.Content = Strings.UnlockWait(left);
            UnlockCreds.ShowError(Strings.ErrVaultRateLimited(left));
            return;
        }
        _tick.Stop();
        UnlockButton.Content = Strings.BtnUnlock;
        UnlockButton.IsEnabled = !_busy;
        UnlockCreds.ShowError(null);
        UnlockCreds.FocusPassword();
    }

    // ---- create ----------------------------------------------------------------------------

    private async Task UpdateStrengthAsync()
    {
        var gen = ++_strengthGeneration;
        var password = CreateCreds.Password;
        if (password.Length == 0)
        {
            _strengthLabel = null;
            CreateStrength.Clear();
            CreateWeak.Visibility = Visibility.Collapsed;
            return;
        }
        var r = await _shell.VaultCallAsync(a => a.Strength(password));
        if (gen != _strengthGeneration || !r.Ok) return;
        _strengthLabel = r.Value!.Label;
        CreateStrength.Set(r.Value.Label, r.Value.Bits);
        CreateWeak.Visibility = StrengthDisplay.IsWeak(r.Value.Label) ? Visibility.Visible : Visibility.Collapsed;
    }

    private async void Create_Click(object sender, RoutedEventArgs e)
    {
        if (_busy) return;
        var (error, _) = CreateVaultForm.Check(CreateCreds.Password, CreateConfirm.Password, _strengthLabel);
        if (error is not null)
        {
            CreateError.Text = error;
            CreateError.Visibility = Visibility.Visible;
            return;
        }
        CreateError.Visibility = Visibility.Collapsed;
        var creds = CreateCreds.Creds;
        var kdf = KdfBox.SelectedValue as string ?? "standard";
        _busy = true;
        CreateButton.IsEnabled = false;
        CreateStatus.Text = Strings.Creating;
        var result = await _shell.VaultCallAsync(a => a.Create(creds, kdf));
        _busy = false;
        CreateStatus.Text = "";
        CreateButton.IsEnabled = true;
        if (!result.Ok)
        {
            CreateError.Text = result.Error ?? Strings.ErrVaultUnexpected;
            CreateError.Visibility = Visibility.Visible;
            return;
        }
        CreateCreds.Clear(keyFileToo: true);
        CreateConfirm.Clear();
        Succeeded?.Invoke(true);
    }
}
