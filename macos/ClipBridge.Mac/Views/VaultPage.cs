using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

/// <summary>
/// The "密码库" page (port of the Windows VaultPage): decides between the create, locked and unlocked screens from
/// <c>vault.status</c> and the <c>vault.locked</c> event, and makes sure a lock drops everything (open windows, list,
/// detail, typed passwords). One instance lives as long as the main window; it is not rebuilt on state changes.
/// </summary>
internal sealed class VaultPage : Grid
{
    private readonly MacController _shell = MacController.Current;
    private readonly TextBlock _loading = Ui.Dim(Strings.VaultLoading).With(t => t.Margin = new Thickness(0, 8));
    private readonly VaultLockPanel _lockPanel = new();
    private readonly VaultMainPanel _mainPanel = new();
    private VaultScreen _screen = VaultScreen.Loading;
    private bool _mainLoaded;

    public VaultPage()
    {
        Children.Add(_loading);
        Children.Add(_lockPanel);
        Children.Add(_mainPanel);
        SetScreen(VaultScreen.Loading);
        _shell.VaultLocked += OnVaultLocked;
        _shell.VaultChanged += () => { if (_screen == VaultScreen.Unlocked && _mainLoaded) _ = _mainPanel.ReloadAsync(); };
        _lockPanel.Succeeded += async created => await OnSucceededAsync(created);
        _mainPanel.LockedDetected += () => ShowLocked("", 0);
    }

    /// <summary>The page became the visible one: exclude the window from captures and read the state.</summary>
    public async Task OnShownAsync()
    {
        _shell.SetVaultPageVisible(true);
        _mainPanel.OnShown();
        await RefreshAsync();
    }

    /// <summary>Another page was selected or the window was hidden: captures are allowed again, secrets are masked.</summary>
    public void OnHidden()
    {
        _shell.SetVaultPageVisible(false);
        _mainPanel.OnHidden();
    }

    private async Task RefreshAsync()
    {
        if (!_shell.CoreRunning) { _loading.Text = Strings.VaultNotRunning; return; }
        var r = await _shell.VaultCallAsync(a => a.Status());
        if (!r.Ok)
        {
            _loading.Text = r.Error ?? Strings.ErrVaultUnexpected;
            return;
        }
        var st = r.Value!;
        switch (VaultScreens.From(st))
        {
            case VaultScreen.Create:
                if (_screen != VaultScreen.Create) { _mainPanel.Reset(); _mainLoaded = false; _lockPanel.ShowCreate(); }
                SetScreen(VaultScreen.Create);
                break;
            case VaultScreen.Locked:
                if (_screen != VaultScreen.Locked)
                {
                    _mainPanel.Reset();
                    _mainLoaded = false;
                    _lockPanel.ShowLocked("", st.RetryAfterS);
                }
                SetScreen(VaultScreen.Locked);
                break;
            default:
                SetScreen(VaultScreen.Unlocked);
                if (_mainLoaded) await _mainPanel.ReloadAsync();
                else { _mainLoaded = true; await _mainPanel.OnUnlockedAsync(st); }
                break;
        }
    }

    private void SetScreen(VaultScreen screen)
    {
        _screen = screen;
        _loading.IsVisible = screen == VaultScreen.Loading;
        _lockPanel.IsVisible = screen is VaultScreen.Create or VaultScreen.Locked;
        _mainPanel.IsVisible = screen == VaultScreen.Unlocked;
    }

    private async Task OnSucceededAsync(bool created)
    {
        var r = await _shell.VaultCallAsync(a => a.Status());
        if (!r.Ok) { _shell.ShowStatus(r.Error ?? Strings.ErrVaultUnexpected); return; }
        SetScreen(VaultScreen.Unlocked);
        _mainLoaded = true;
        await _mainPanel.OnUnlockedAsync(r.Value!);
        if (created) await CreatedPrompt.ShowAsync(VaultUi.OwnerOf(this), _shell);
    }

    private void ShowLocked(string note, int retryAfterSeconds)
    {
        VaultUi.CloseAll();
        _mainPanel.Reset();
        _mainLoaded = false;
        _lockPanel.ShowLocked(note, retryAfterSeconds);
        SetScreen(VaultScreen.Locked);
    }

    /// <summary>The vault was locked (idle, menu bar, screen lock, ...): show the locked screen with everything dropped.</summary>
    private void OnVaultLocked(VaultLockedEvent ev)
    {
        ShowLocked(VaultScreens.LockedNote(ev.Reason), 0);
        _shell.ShowStatus(Strings.VaultLockedToast);
    }
}

/// <summary>The locked screen (unlock) and the first-run screen (create). Every decision is in Logic/VaultAuth.cs.</summary>
internal sealed class VaultLockPanel : ScrollViewer
{
    private readonly MacController _shell = MacController.Current;
    private readonly RateLimitCountdown _rate = new();
    private readonly Avalonia.Threading.DispatcherTimer _tick = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly Avalonia.Threading.DispatcherTimer _strengthTimer = new() { Interval = TimeSpan.FromMilliseconds(250) };
    private int _strengthGeneration;
    private string? _strengthLabel;
    private bool _busy;

    // unlock
    private readonly StackPanel _unlockPane;
    private readonly TextBlock _lockedNote = DialogKit.Text("", VaultUi.WarnBrush, bottom: 10);
    private readonly CredsBox _unlockCreds = new();
    private readonly Button _unlockButton = new() { Content = Strings.BtnUnlock, MinWidth = 110 };
    private readonly TextBlock _unlockStatus = new() { VerticalAlignment = VerticalAlignment.Center, TextWrapping = Avalonia.Media.TextWrapping.Wrap, MaxWidth = 320 };

    // create
    private readonly StackPanel _createPane;
    private readonly CredsBox _createCreds = new();
    private readonly CredsBox _createConfirm = new() { Caption = Strings.LabelConfirmPassword, ShowKeyFile = false, Margin = new Thickness(0, 12, 0, 0) };
    private readonly StrengthBar _createStrength = new() { Margin = new Thickness(0, 6, 0, 0) };
    private readonly TextBlock _createWeak = DialogKit.Text(Strings.CreateWeakWarning, VaultUi.WarnBrush, top: 4);
    private readonly ComboBox _kdfBox = new() { Width = 320, ItemsSource = CreateVaultForm.KdfChoices, SelectedIndex = 1 };
    private readonly TextBlock _createError = DialogKit.Text("", VaultUi.ErrorBrush, top: 10);
    private readonly Button _createButton = new() { Content = Strings.BtnCreateVault, MinWidth = 130 };
    private readonly TextBlock _createStatus = new() { VerticalAlignment = VerticalAlignment.Center };

    /// <summary>Unlocked or created: the page should show the vault. The argument tells whether it was just created.</summary>
    public event Action<bool>? Succeeded;

    public VaultLockPanel()
    {
        _unlockPane = new StackPanel { MaxWidth = 480, HorizontalAlignment = HorizontalAlignment.Left, Margin = new Thickness(0, 8, 0, 16) };
        _unlockPane.Children.Add(DialogKit.Text(Strings.LockedTitle, size: 22, bold: true));
        _unlockPane.Children.Add(DialogKit.Text(Strings.LockedHint, VaultUi.MutedBrush, top: 6, bottom: 14));
        _unlockPane.Children.Add(_lockedNote);
        _unlockPane.Children.Add(_unlockCreds);
        _unlockPane.Children.Add(Ui.Row(_unlockButton, _unlockStatus).With(r => r.Margin = new Thickness(0, 14, 0, 0)));

        _kdfBox.ItemTemplate = new Avalonia.Controls.Templates.FuncDataTemplate<string>((k, _) => new TextBlock { Text = CreateVaultForm.KdfLabel(k) });
        _createPane = new StackPanel { MaxWidth = 520, HorizontalAlignment = HorizontalAlignment.Left, Margin = new Thickness(0, 8, 0, 16) };
        _createPane.Children.Add(DialogKit.Text(Strings.CreateTitle, size: 22, bold: true));
        _createPane.Children.Add(DialogKit.Text(Strings.CreateIntro, VaultUi.MutedBrush, top: 6, bottom: 12));
        _createPane.Children.Add(new Border
        {
            BorderBrush = VaultUi.ErrorBrush, BorderThickness = new Thickness(2), CornerRadius = new CornerRadius(6),
            Padding = new Thickness(14, 10), Margin = new Thickness(0, 0, 0, 14), Background = VaultUi.Brush(0x1AC62828),
            Child = Ui.Column(6,
                DialogKit.Text(Strings.CreateWarningTitle, VaultUi.ErrorBrush, 16, bold: true),
                DialogKit.Text(Strings.CreateWarningText, VaultUi.ErrorBrush)),
        });
        _createPane.Children.Add(_createCreds);
        _createPane.Children.Add(_createStrength);
        _createPane.Children.Add(_createWeak);
        _createPane.Children.Add(_createConfirm);
        _createPane.Children.Add(DialogKit.Text(Strings.KeyFileRequiredNote, VaultUi.MutedBrush, 12, top: 6));
        _createPane.Children.Add(new Expander
        {
            Header = Strings.CreateAdvanced, Margin = new Thickness(0, 12, 0, 0), HorizontalAlignment = HorizontalAlignment.Stretch,
            Content = Ui.Column(4, Ui.Text(Strings.LabelKdf), _kdfBox),
        });
        _createPane.Children.Add(_createError);
        _createPane.Children.Add(Ui.Row(_createButton, _createStatus).With(r => r.Margin = new Thickness(0, 14, 0, 0)));

        Content = new Panel { Children = { _unlockPane, _createPane } };
        _unlockPane.IsVisible = false;
        _createPane.IsVisible = false;

        _tick.Tick += (_, _) => OnTick();
        _strengthTimer.Tick += async (_, _) => { _strengthTimer.Stop(); await UpdateStrengthAsync(); };
        _unlockButton.Click += async (_, _) => await UnlockAsync();
        _unlockCreds.Submit += () => _ = UnlockAsync();
        _createButton.Click += async (_, _) => await CreateAsync();
        _createConfirm.Submit += () => _ = CreateAsync();
        _createCreds.Submit += () => _createConfirm.FocusPassword();
        _createCreds.PasswordChanged += () => { _strengthTimer.Stop(); _strengthTimer.Start(); };
    }

    // ---- screens ---------------------------------------------------------------------------

    /// <summary>Shows the unlock screen. <paramref name="note"/> explains an automatic lock; <paramref name="retryAfterSeconds"/> starts the wait.</summary>
    public void ShowLocked(string note = "", int retryAfterSeconds = 0)
    {
        _createPane.IsVisible = false;
        _unlockPane.IsVisible = true;
        ClearAll();
        _lockedNote.Text = note;
        _lockedNote.IsVisible = note.Length > 0;
        _unlockStatus.Text = "";
        _unlockCreds.ShowError(null);
        SetBusy(false);
        _unlockButton.Content = Strings.BtnUnlock;
        if (retryAfterSeconds > 0) StartWait(retryAfterSeconds);
        _unlockCreds.FocusPassword();
    }

    public void ShowCreate()
    {
        _unlockPane.IsVisible = false;
        _createPane.IsVisible = true;
        ClearAll();
        _createError.IsVisible = false;
        _createWeak.IsVisible = false;
        _createStatus.Text = "";
        _createStrength.Clear();
        _createButton.IsEnabled = true;
        _createCreds.FocusPassword();
    }

    /// <summary>Drops every password and key file held by the boxes (called on lock, on success and when leaving).</summary>
    public void ClearAll()
    {
        _unlockCreds.Clear(keyFileToo: true);
        _createCreds.Clear(keyFileToo: true);
        _createConfirm.Clear();
        _tick.Stop();
        _rate.Clear();
    }

    // ---- unlock ----------------------------------------------------------------------------

    private async Task UnlockAsync()
    {
        if (_busy || _rate.IsBlocked(DateTimeOffset.UtcNow)) return;
        if (_unlockCreds.Password.Length == 0) { _unlockCreds.FocusPassword(); return; }
        var creds = _unlockCreds.Creds;
        SetBusy(true);
        _unlockStatus.Text = Strings.Unlocking;
        _unlockCreds.ShowError(null);
        var result = await _shell.VaultCallAsync(a => a.Unlock(creds));
        _unlockCreds.Clear(); // the password is never kept after use
        SetBusy(false);
        if (result.Ok)
        {
            _unlockStatus.Text = "";
            var slow = result.Value > 3000;
            _unlockCreds.Clear(keyFileToo: true);
            Succeeded?.Invoke(false);
            if (slow) _shell.ShowStatus(Strings.UnlockSlowDevice);
            return;
        }
        var outcome = UnlockOutcome.FromError(result.ErrorCode ?? "", result.RetryAfterSeconds);
        _unlockStatus.Text = "";
        _unlockCreds.ShowError(outcome.Message);
        if (outcome.RetryAfterSeconds > 0) StartWait(outcome.RetryAfterSeconds);
        else _unlockCreds.FocusPassword();
    }

    private void SetBusy(bool busy)
    {
        _busy = busy;
        _unlockButton.IsEnabled = !busy && !_rate.IsBlocked(DateTimeOffset.UtcNow);
    }

    private void StartWait(int seconds)
    {
        _rate.Start(seconds, DateTimeOffset.UtcNow);
        _unlockButton.IsEnabled = false;
        _unlockButton.Content = Strings.UnlockWait(seconds);
        _tick.Start();
    }

    private void OnTick()
    {
        var left = _rate.SecondsLeft(DateTimeOffset.UtcNow);
        if (left > 0)
        {
            _unlockButton.Content = Strings.UnlockWait(left);
            _unlockCreds.ShowError(Strings.ErrVaultRateLimited(left));
            return;
        }
        _tick.Stop();
        _unlockButton.Content = Strings.BtnUnlock;
        _unlockButton.IsEnabled = !_busy;
        _unlockCreds.ShowError(null);
        _unlockCreds.FocusPassword();
    }

    // ---- create ----------------------------------------------------------------------------

    private async Task UpdateStrengthAsync()
    {
        var gen = ++_strengthGeneration;
        var password = _createCreds.Password;
        if (password.Length == 0)
        {
            _strengthLabel = null;
            _createStrength.Clear();
            _createWeak.IsVisible = false;
            return;
        }
        var r = await _shell.VaultCallAsync(a => a.Strength(password));
        if (gen != _strengthGeneration || !r.Ok) return;
        _strengthLabel = r.Value!.Label;
        _createStrength.Set(r.Value.Label, r.Value.Bits);
        _createWeak.IsVisible = StrengthDisplay.IsWeak(r.Value.Label);
    }

    private async Task CreateAsync()
    {
        if (_busy) return;
        var (error, _) = CreateVaultForm.Check(_createCreds.Password, _createConfirm.Password, _strengthLabel);
        if (error is not null)
        {
            _createError.Text = error;
            _createError.IsVisible = true;
            return;
        }
        _createError.IsVisible = false;
        var creds = _createCreds.Creds;
        var kdf = _kdfBox.SelectedItem as string ?? "standard";
        _busy = true;
        _createButton.IsEnabled = false;
        _createStatus.Text = Strings.Creating;
        var result = await _shell.VaultCallAsync(a => a.Create(creds, kdf));
        _busy = false;
        _createStatus.Text = "";
        _createButton.IsEnabled = true;
        if (!result.Ok)
        {
            _createError.Text = result.Error ?? Strings.ErrVaultUnexpected;
            _createError.IsVisible = true;
            return;
        }
        _createCreds.Clear(keyFileToo: true);
        _createConfirm.Clear();
        Succeeded?.Invoke(true);
    }
}
