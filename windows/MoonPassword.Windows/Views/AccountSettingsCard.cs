using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Lumen.UI.Controls;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;

namespace MoonPassword.Windows.Views;

internal sealed class AccountSettingsCard : Card
{
    private readonly ShellController _shell;
    private readonly TextBlock _state = Ui.Text("正在读取账号状态…", "body");
    private readonly Button _action;
    private string _status = "";
    private bool _busy;
    private int _refresh;

    public AccountSettingsCard(ShellController shell)
    {
        _shell = shell;
        Header = "Moon SSO 账号";
        Description = "在系统浏览器中授权 MoonPassword。密码库仍用主密码解锁。";
        Classes.Add("flat");
        _state.TextWrapping = TextWrapping.Wrap;
        _action = Ui.TextButton("使用 Moon SSO 登录", () => _ = ActAsync(), "primary");
        _action.IsEnabled = false;
        var row = new Grid
        {
            ColumnDefinitions = new ColumnDefinitions("*,Auto"),
            ColumnSpacing = 12,
            Margin = new Thickness(0, 6, 0, 0),
        };
        _state.VerticalAlignment = VerticalAlignment.Center;
        row.Children.Add(_state);
        Grid.SetColumn(_action, 1);
        row.Children.Add(_action);
        Content = row;
        AttachedToVisualTree += (_, _) =>
        {
            _shell.EventReceived += AccountChanged;
            _ = RefreshAsync();
        };
        DetachedFromVisualTree += (_, _) => _shell.EventReceived -= AccountChanged;
    }

    private void AccountChanged(CoreEvent ev)
    {
        if (ev.Type is "core.started" or "account.changed") _ = RefreshAsync();
    }

    private async Task RefreshAsync()
    {
        int request = ++_refresh;
        try
        {
            var result = await _shell.SessionCallAsync("account.status");
            if (request != _refresh) return;
            _status = JsonRead.Str(result, "state");
            var name = JsonRead.Str(result, "name");
            var username = JsonRead.Str(result, "username");
            var error = JsonRead.Str(result, "error");
            _state.Text = _status switch
            {
                "signed_in" when name.Length > 0 && username.Length > 0 && name != username => $"已登录 {name}（{username}）",
                "signed_in" => "已登录 " + (name.Length > 0 ? name : username),
                "authorizing" => "请在浏览器内完成授权。",
                "unavailable" => "此版本尚未配置 Moon SSO。",
                _ when error == "session_storage" => "无法读取本机登录会话，可清除它后重试。",
                _ when error == "authorization_failed" => "授权未完成，可以重新登录。",
                _ => "未登录",
            };
            if (error == "session_storage") _status = "clear_session";
            _action.Content = _status switch
            {
                "signed_in" => "退出登录",
                "authorizing" => "取消授权",
                "clear_session" => "清除登录会话",
                _ => "使用 Moon SSO 登录",
            };
        }
        catch (CoreException)
        {
            if (request != _refresh) return;
            _status = "unavailable";
            _state.Text = "无法读取账号状态。";
        }
        _action.IsEnabled = !_busy && _status is ("signed_out" or "authorizing" or "signed_in" or "clear_session");
    }

    private async Task ActAsync()
    {
        if (_busy) return;
        _busy = true;
        _action.IsEnabled = false;
        try
        {
            switch (_status)
            {
                case "signed_in":
                case "clear_session":
                    await _shell.SessionCallAsync("account.logout");
                    break;
                case "authorizing":
                    await _shell.SessionCallAsync("account.cancel");
                    break;
                case "signed_out":
                    var result = await _shell.SessionCallAsync("account.login");
                    var url = JsonRead.Str(result, "authorization_url");
                    if (!Uri.TryCreate(url, UriKind.Absolute, out var uri) || uri.Scheme != "https" ||
                        !uri.IdnHost.Equals("sso.moonl.cloud", StringComparison.OrdinalIgnoreCase) || !uri.IsDefaultPort ||
                        uri.UserInfo.Length != 0 || _shell.OpenWebUrl(uri.AbsoluteUri) is not null)
                    {
                        await _shell.SessionCallAsync("account.cancel");
                        _shell.ShowStatus("无法打开 Moon SSO 授权页。", StatusKind.Error);
                    }
                    break;
            }
        }
        catch (CoreException)
        {
            _shell.ShowStatus("Moon SSO 操作失败，请重试。", StatusKind.Error);
        }
        finally
        {
            _busy = false;
            await RefreshAsync();
        }
    }
}
