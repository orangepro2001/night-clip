using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The "密码库" page: decides between the create, locked and unlocked screens from <c>vault.status</c> and the
/// <c>vault.locked</c> event, and makes sure a lock drops everything (open windows, list, detail, typed passwords).
/// One instance lives as long as the main window; it is not rebuilt on state changes.
/// </summary>
internal sealed class VaultPage : UserControl, ISearchablePage
{
    private readonly ShellController _shell;
    private readonly StackPanel _loading;
    private readonly TextBlock _loadingText = Ui.Text(Strings.VaultLoading, "secondary");
    private readonly VaultLockPanel _lockPanel;
    private readonly VaultMainPanel _mainPanel;
    private VaultScreen _screen = VaultScreen.Loading;
    private bool _mainLoaded;

    public VaultPage(ShellController shell)
    {
        _shell = shell;
        _lockPanel = new VaultLockPanel(shell);
        _mainPanel = new VaultMainPanel(shell);
        _loading = new StackPanel
        {
            Spacing = 10,
            HorizontalAlignment = HorizontalAlignment.Center,
            VerticalAlignment = VerticalAlignment.Center,
            Children = { new Lumen.UI.Controls.Spinner { Size = 24, HorizontalAlignment = HorizontalAlignment.Center }, _loadingText },
        };
        Content = new Panel { Children = { _loading, _lockPanel, _mainPanel } };
        SetScreen(VaultScreen.Loading);
        shell.VaultLocked += OnVaultLocked;
        shell.VaultChanged += () => { if (_screen == VaultScreen.Unlocked && _mainLoaded) _ = _mainPanel.ReloadAsync(); };
        _lockPanel.Succeeded += async created => await OnSucceededAsync(created);
        _mainPanel.LockedDetected += () => ShowLocked("", 0);
    }

    /// <summary>The page became the visible one: exclude the window from captures and read the state.</summary>
    public async void OnShown()
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

    public void FocusSearch()
    {
        if (_screen == VaultScreen.Unlocked) _mainPanel.FocusSearch();
    }

    private async Task RefreshAsync()
    {
        if (!_shell.CoreRunning) { _loadingText.Text = Strings.VaultNotRunning; return; }
        var r = await _shell.VaultCallAsync(a => a.Status());
        if (!r.Ok)
        {
            _loadingText.Text = r.Error ?? Strings.ErrVaultUnexpected;
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
        _shell.SetVaultUnlocked(screen == VaultScreen.Unlocked);
    }

    private async Task OnSucceededAsync(bool created)
    {
        var r = await _shell.VaultCallAsync(a => a.Status());
        if (!r.Ok) { _shell.ShowStatus(r.Error ?? Strings.ErrVaultUnexpected, StatusKind.Error); return; }
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

    /// <summary>The vault was locked (idle, tray menu, session lock, ...): show the locked screen with everything dropped.</summary>
    private void OnVaultLocked(VaultLockedEvent ev)
    {
        ShowLocked(VaultScreens.LockedNote(ev.Reason), 0);
        _shell.ShowStatus(Strings.VaultLockedToast, StatusKind.Info);
    }
}

/// <summary>The locked screen (unlock) and the first-run screen (create), centred cards. Every decision is in Logic/VaultAuth.cs.</summary>
internal sealed class VaultLockPanel : ScrollViewer
{
    private readonly ShellController _shell;
    private readonly RateLimitCountdown _rate = new();
    private readonly Avalonia.Threading.DispatcherTimer _tick = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly Avalonia.Threading.DispatcherTimer _strengthTimer = new() { Interval = TimeSpan.FromMilliseconds(250) };
    private int _strengthGeneration;
    private string? _strengthLabel;
    private bool _busy;

    // unlock
    private readonly Control _unlockPane;
    private readonly InfoBar _lockedNote = new() { Severity = Severity.Info, IsOpen = false };
    private readonly CredsBox _unlockCreds = new();
    private readonly Button _unlockButton = new() { Content = Strings.BtnUnlock, MinWidth = 120, IsDefault = true, Classes = { "primary", "block" } };
    private readonly TextBlock _unlockStatus = Ui.Text("", "caption", "secondary");

    // create
    private readonly Control _createPane;
    private readonly CredsBox _createCreds = new();
    private readonly CredsBox _createConfirm = new() { Caption = Strings.LabelConfirmPassword, ShowKeyFile = false };
    private readonly StrengthBar _createStrength = new() { Margin = new Thickness(0, 2, 0, 0) };
    private readonly TextBlock _createWeak = Ui.Text(Strings.CreateWeakWarning, "caption", "warning");
    private readonly ComboBox _kdfBox = new() { MinWidth = 320, ItemsSource = CreateVaultForm.KdfChoices, SelectedIndex = 1 };
    private readonly InfoBar _createError = new() { Severity = Severity.Danger, IsOpen = false };
    private readonly Button _createButton = new() { Content = Strings.BtnCreateVault, MinWidth = 140, Classes = { "primary" } };
    private readonly TextBlock _createStatus = Ui.Text("", "caption", "secondary").With(t => t.VerticalAlignment = VerticalAlignment.Center);

    /// <summary>Unlocked or created: the page should show the vault. The argument tells whether it was just created.</summary>
    public event Action<bool>? Succeeded;

    public VaultLockPanel(ShellController shell)
    {
        _shell = shell;

        var lockTile = new Border { Width = 52, Height = 52, CornerRadius = new CornerRadius(14), HorizontalAlignment = HorizontalAlignment.Center, Child = new Icon { Kind = IconKind.Lock, Size = 24, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center } };
        lockTile.Classes.Add("tile");
        lockTile.Classes.Add("accent");
        var unlockCard = new Card
        {
            MaxWidth = 440,
            HorizontalAlignment = HorizontalAlignment.Center,
            Content = new StackPanel
            {
                Spacing = 14,
                Children =
                {
                    lockTile,
                    Ui.Text(Strings.LockedTitle, "title").With(t => { t.HorizontalAlignment = HorizontalAlignment.Center; t.TextAlignment = TextAlignment.Center; }),
                    Ui.Text(Strings.LockedHint, "secondary").With(t => { t.HorizontalAlignment = HorizontalAlignment.Center; t.TextAlignment = TextAlignment.Center; }),
                    _lockedNote,
                    _unlockCreds,
                    _unlockButton,
                    _unlockStatus.With(t => t.HorizontalAlignment = HorizontalAlignment.Center),
                },
            },
        };
        _unlockPane = new Border { Padding = new Thickness(28, 48, 28, 28), Child = unlockCard };

        _kdfBox.ItemTemplate = Ui.Template<string>(k => new TextBlock { Text = CreateVaultForm.KdfLabel(k) });
        var warning = new InfoBar { Severity = Severity.Danger, Title = Strings.CreateWarningTitle, Message = Strings.CreateWarningText };
        var advanced = new Expander
        {
            Header = Strings.CreateAdvanced,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            Content = new FormField { Label = Strings.LabelKdf, Content = _kdfBox },
        };
        var createCard = new Card
        {
            MaxWidth = 560,
            HorizontalAlignment = HorizontalAlignment.Center,
            Header = Strings.CreateTitle,
            Description = Strings.CreateIntro,
            Content = new StackPanel
            {
                Spacing = 12,
                Margin = new Thickness(0, 8, 0, 0),
                Children =
                {
                    warning,
                    _createCreds,
                    _createStrength,
                    _createWeak,
                    _createConfirm,
                    Ui.Text(Strings.KeyFileRequiredNote, "caption", "tertiary"),
                    advanced,
                    _createError,
                    new StackPanel { Orientation = Orientation.Horizontal, Spacing = 12, Children = { _createButton, _createStatus } },
                },
            },
        };
        _createPane = new Border { Padding = new Thickness(28, 28, 28, 28), Child = createCard };

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
        _lockedNote.Message = note;
        _lockedNote.IsOpen = note.Length > 0;
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
        _createError.IsOpen = false;
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
            if (slow) _shell.ShowStatus(Strings.UnlockSlowDevice, StatusKind.Info);
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
            _createError.Message = error;
            _createError.IsOpen = true;
            return;
        }
        _createError.IsOpen = false;
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
            _createError.Message = result.Error ?? Strings.ErrVaultUnexpected;
            _createError.IsOpen = true;
            return;
        }
        _createCreds.Clear(keyFileToo: true);
        _createConfirm.Clear();
        Succeeded?.Invoke(true);
    }
}
