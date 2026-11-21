using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using Lumen.UI.Controls;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;

namespace MoonPassword.Windows.Views;

internal sealed class VaultSyncSettingsCard : Card
{
    private readonly ShellController _shell;
    private readonly TextBlock _state = Ui.Text("", "body");
    private readonly Button _create, _join, _approve, _restore, _sync, _key, _disconnect;
    private bool _busy;
    private bool _enabled;
    private bool _signedIn;
    private int _refresh;

    public VaultSyncSettingsCard(ShellController shell)
    {
        _shell = shell;
        Header = "密码库自动同步";
        Description = "解锁时自动同步加密库。添加设备时，用同一 Moon SSO 账号登录并核对 6 位码。";
        Classes.Add("flat");
        _state.TextWrapping = TextWrapping.Wrap;
        _create = Ui.TextButton("创建云端密码库", () => _ = SetUpAsync(join: false), "primary");
        _join = Ui.TextButton("用 6 位码连接", () => _ = PairAsync(requester: true), "soft");
        _approve = Ui.TextButton("核对并批准新设备", () => _ = PairAsync(requester: false), "soft");
        _restore = Ui.TextButton("使用应急恢复密钥", () => _ = SetUpAsync(join: true), "ghost");
        _sync = Ui.TextButton("立即同步", () => _ = RunAsync("sync.now"), "primary");
        _key = Ui.TextButton("复制应急恢复密钥", () => _ = RunAsync("sync.recovery_key"), "ghost");
        _disconnect = Ui.TextButton("断开此设备", () => _ = DisconnectAsync(), "ghost");
        var actions = new WrapPanel { Orientation = Orientation.Horizontal };
        foreach (var button in new[] { _create, _join, _approve, _sync, _restore, _key, _disconnect })
        {
            button.Margin = new Thickness(0, 0, 8, 6);
            actions.Children.Add(button);
        }
        Content = new StackPanel { Spacing = 10, Margin = new Thickness(0, 6, 0, 0), Children = { _state, actions } };
        RefreshButtons();
        AttachedToVisualTree += (_, _) =>
        {
            _shell.EventReceived += Changed;
            _shell.StateChanged += Refresh;
            Refresh();
        };
        DetachedFromVisualTree += (_, _) =>
        {
            _shell.EventReceived -= Changed;
            _shell.StateChanged -= Refresh;
        };
    }

    private void Changed(CoreEvent ev)
    {
        if (ev.Type is "core.started" or "account.changed" or "sync.changed") Refresh();
    }

    private void Refresh() => _ = RefreshAsync();

    private async Task RefreshAsync()
    {
        int request = ++_refresh;
        try
        {
            var account = await _shell.SessionCallAsync("account.status");
            var sync = await _shell.SessionCallAsync("sync.status");
            if (request != _refresh) return;
            _signedIn = JsonRead.Str(account, "state") == "signed_in";
            _enabled = JsonRead.Bool(sync, "enabled");
            long last = JsonRead.Long(sync, "last_sync");
            string error = JsonRead.Str(sync, "error");
            _state.Text = !_signedIn ? "请先登录 Moon SSO。"
                : !_shell.VaultUnlocked ? "解锁密码库后同步。"
                : error.Length > 0 ? ErrorText(error)
                : !_enabled ? "还未连接云端密码库。"
                : last == 0 ? "已连接，等待首次同步。"
                : $"上次同步 {DateTimeOffset.FromUnixTimeSeconds(last).ToLocalTime():g}";
        }
        catch (CoreException)
        {
            if (request != _refresh) return;
            _state.Text = "无法读取同步状态。";
        }
        RefreshButtons();
    }

    private void RefreshButtons()
    {
        _create.IsVisible = _join.IsVisible = _restore.IsVisible = !_enabled;
        _approve.IsVisible = _sync.IsVisible = _key.IsVisible = _disconnect.IsVisible = _enabled;
        _create.IsEnabled = _join.IsEnabled = _restore.IsEnabled = _approve.IsEnabled = _sync.IsEnabled =
            !_busy && _signedIn && _shell.VaultUnlocked;
        _key.IsEnabled = !_busy && _shell.VaultUnlocked;
        _disconnect.IsEnabled = !_busy;
    }

    private async Task SetUpAsync(bool join)
    {
        var key = "";
        if (join)
        {
            var password = new TextBox { PasswordChar = '•', IsUndoEnabled = false, PlaceholderText = "应急恢复密钥" };
            var connect = DialogKit.Btn("连接并合并", 100, "primary");
            var close = DialogKit.Btn(Strings.BtnCancel, 88);
            var description = Ui.Text("无法使用旧设备时，粘贴备份的应急恢复密钥。云端库会与本机库合并。", "caption");
            description.TextWrapping = TextWrapping.Wrap;
            var body = new StackPanel { Spacing = 8, Children = { description, password, DialogKit.ButtonRow(close, connect) } };
            var window = DialogKit.Create(_shell, "使用应急恢复密钥", 465, body);
            close.Click += (_, _) => window.Close();
            connect.Click += (_, _) => { key = password.Text?.Trim() ?? ""; window.Close(); };
            await window.ShowModalAsync(VaultUi.OwnerOf(this));
            password.Text = "";
            if (key.Length == 0) return;
        }
        await RunAsync("sync.enable", new { mode = join ? "join" : "create", key });
    }

    private async Task PairAsync(bool requester)
    {
        if (_busy) return;
        _busy = true;
        RefreshButtons();
        var note = Ui.Text("正在连接…", "body");
        note.TextWrapping = TextWrapping.Wrap;
        var code = Ui.Text("", "title");
        var approve = DialogKit.Btn("两台设备上的码一致，批准", 185, "primary");
        approve.IsVisible = !requester;
        approve.IsEnabled = false;
        var cancel = DialogKit.Btn("取消配对", 90);
        var panel = new StackPanel { Spacing = 10, Children = { note, code, DialogKit.ButtonRow(cancel, approve) } };
        var window = DialogKit.Create(_shell, requester ? "用 6 位码连接" : "核对并批准新设备", 505, panel);
        var timer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(2) };
        bool closed = false, complete = false, inFlight = false;
        string compare = "";
        async Task Step(string method)
        {
            if (closed || complete || inFlight) return;
            inFlight = true;
            approve.IsEnabled = false;
            try
            {
                var result = await _shell.SessionCallAsync(method, method == "sync.pair_approve" ? new { code = compare } : null);
                if (closed) return;
                string state = JsonRead.Str(result, "state");
                compare = JsonRead.Str(result, "code");
                code.Text = compare;
                string device = JsonRead.Str(result, "device");
                if (state is "connected" or "sent")
                {
                    complete = true;
                    _shell.ShowStatus(state == "connected" ? "已连接云端密码库。" : "已批准新设备。", StatusKind.Success);
                    window.Close();
                    return;
                }
                if (state is "expired" or "cancelled" or "idle")
                {
                    timer.Stop();
                    note.Text = "配对已取消或过期。关闭后可重新发起。";
                    return;
                }
                note.Text = state == "compare"
                    ? requester ? "请在旧设备上核对这 6 位码并批准。" : $"核对新设备「{device}」上显示的 6 位码。只有完全一致才批准。"
                    : requester ? "请在旧设备打开同步设置，点击「核对并批准新设备」。" : $"正在等待新设备「{device}」显示 6 位码。";
                approve.IsEnabled = !requester && state == "compare";
                timer.Start();
            }
            catch (CoreException ex)
            {
                if (closed) return;
                code.Text = "";
                note.Text = ErrorText(ex.Code);
                if (method != "sync.pair_status" || ex.Code != "SYNC_NETWORK") timer.Stop();
            }
            finally { inFlight = false; }
        }
        timer.Tick += (_, _) => _ = Step("sync.pair_status");
        approve.Click += (_, _) => _ = Step("sync.pair_approve");
        cancel.Click += (_, _) => window.Close();
        window.Opened += (_, _) => _ = Step(requester ? "sync.pair_start" : "sync.pair_prepare");
        window.Closed += (_, _) => { closed = true; timer.Stop(); code.Text = ""; };
        await window.ShowModalAsync(VaultUi.OwnerOf(this));
        if (!complete)
        {
            try { await _shell.SessionCallAsync("sync.pair_cancel"); }
            catch (CoreException) { }
        }
        _busy = false;
        await RefreshAsync();
    }

    private async Task DisconnectAsync()
    {
        if (await VaultUi.ConfirmAsync(this, "停止这台设备的自动同步。云端和本机密码库都会保留。", "断开此设备", "断开"))
            await RunAsync("sync.disconnect");
    }

    private async Task RunAsync(string method, object? parameters = null)
    {
        if (_busy) return;
        _busy = true;
        RefreshButtons();
        try
        {
            var result = await _shell.SessionCallAsync(method, parameters);
            if (method == "sync.recovery_key" && _shell.VaultUnlocked)
            {
                var error = await _shell.CopySensitiveTextAsync(JsonRead.Str(result, "key"), "应急恢复密钥", 30);
                if (error is not null) _shell.ShowStatus(error, StatusKind.Error);
            }
            if (method == "sync.enable" && JsonRead.Str(result, "error").Length == 0)
                _shell.ShowStatus("已连接。可备份应急恢复密钥。", StatusKind.Success);
        }
        catch (CoreException ex) { _shell.ShowStatus(ErrorText(ex.Code), StatusKind.Error); }
        finally
        {
            _busy = false;
            await RefreshAsync();
        }
    }

    private static string ErrorText(string code) => code switch
    {
        "SYNC_SIGN_IN" => "请重新登录 Moon SSO。",
        "SYNC_ACCOUNT" => "此密码库已连接到另一 Moon SSO 账号。",
        "SYNC_EXISTS" => "该账号已有云端库，请使用 6 位码连接。",
        "SYNC_KEY" => "同步密钥无法解密云端密码库。",
        "SYNC_MISSING" => "没有找到此账号的云端密码库。",
        "SYNC_CONFLICT" => "其他设备刚更新了密码库，将自动重试。",
        "SYNC_DATA" => "云端密码库格式错误或超出大小限制。",
        "VAULT_LOCKED" => "解锁密码库后同步。",
        "PAIR_MISSING" => "没有等待中的配对。请先在新设备发起连接。",
        "PAIR_BUSY" => "近期已有配对请求，请等它的有效期结束后再试。",
        "PAIR_INVALID" or "PAIR_STATE" => "配对已失效，请重新发起。",
        "PAIR_CONFIRM" => "核对当前两台设备的 6 位码后再批准。",
        _ => "同步暂时失败，连接后将自动重试。",
    };
}
