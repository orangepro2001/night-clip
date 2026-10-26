using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The "密码库" page: decides between the create, locked and unlocked screens from <c>vault.status</c> and the
/// <c>vault.locked</c> event, and makes sure a lock drops everything (open dialogs, list, detail, typed passwords).
/// </summary>
public partial class VaultPage : UserControl
{
    private ShellController _shell = null!;
    private VaultScreen _screen = VaultScreen.Loading;
    private bool _mainLoaded;

    public VaultPage() => InitializeComponent();

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        LockPanel.Attach(shell);
        MainPanel.Attach(shell);
        shell.VaultLocked += OnVaultLocked;
        LockPanel.Succeeded += async created => await OnSucceededAsync(created);
        MainPanel.LockedDetected += () => ShowLocked("", 0);
    }

    /// <summary>The page became the visible one: exclude the window from captures and read the state.</summary>
    public async void OnShown()
    {
        _shell.SetVaultPageVisible(true);
        await RefreshAsync();
    }

    /// <summary>Another page was selected: captures are allowed again; the detail pane masks its secrets by itself.</summary>
    public void OnHidden() => _shell.SetVaultPageVisible(false);

    private async Task RefreshAsync()
    {
        if (!_shell.CoreRunning) { LoadingText.Text = Strings.VaultNotRunning; return; }
        var r = await _shell.VaultCallAsync(a => a.Status());
        if (!r.Ok)
        {
            LoadingText.Text = r.Error ?? Strings.ErrVaultUnexpected;
            return;
        }
        var st = r.Value!;
        switch (VaultScreens.From(st))
        {
            case VaultScreen.Create:
                if (_screen != VaultScreen.Create) { MainPanel.Reset(); _mainLoaded = false; LockPanel.ShowCreate(); }
                SetScreen(VaultScreen.Create);
                break;
            case VaultScreen.Locked:
                if (_screen != VaultScreen.Locked)
                {
                    MainPanel.Reset();
                    _mainLoaded = false;
                    LockPanel.ShowLocked("", st.RetryAfterS);
                }
                SetScreen(VaultScreen.Locked);
                break;
            default:
                SetScreen(VaultScreen.Unlocked);
                if (_mainLoaded) await MainPanel.ReloadAsync();
                else { _mainLoaded = true; await MainPanel.OnUnlockedAsync(st); }
                break;
        }
    }

    private void SetScreen(VaultScreen screen)
    {
        _screen = screen;
        LoadingText.Visibility = screen == VaultScreen.Loading ? Visibility.Visible : Visibility.Collapsed;
        LockPanel.Visibility = screen is VaultScreen.Create or VaultScreen.Locked ? Visibility.Visible : Visibility.Collapsed;
        MainPanel.Visibility = screen == VaultScreen.Unlocked ? Visibility.Visible : Visibility.Collapsed;
    }

    private async Task OnSucceededAsync(bool created)
    {
        var r = await _shell.VaultCallAsync(a => a.Status());
        if (!r.Ok) { _shell.ShowStatus(r.Error ?? Strings.ErrVaultUnexpected); return; }
        SetScreen(VaultScreen.Unlocked);
        _mainLoaded = true;
        await MainPanel.OnUnlockedAsync(r.Value!);
        if (created) CreatedPrompt.Show(Window.GetWindow(this), _shell);
    }

    private void ShowLocked(string note, int retryAfterSeconds)
    {
        VaultUi.CloseAll();
        MainPanel.Reset();
        _mainLoaded = false;
        LockPanel.ShowLocked(note, retryAfterSeconds);
        SetScreen(VaultScreen.Locked);
    }

    /// <summary>The vault was locked (idle, tray menu, session lock, ...): show the locked screen with everything dropped.</summary>
    private void OnVaultLocked(VaultLockedEvent ev)
    {
        ShowLocked(VaultScreens.LockedNote(ev.Reason), 0);
        _shell.ShowStatus(Strings.VaultLockedToast);
    }
}
