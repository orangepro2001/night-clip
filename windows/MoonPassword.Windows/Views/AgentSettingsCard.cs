using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Lumen.UI.Controls;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;

namespace MoonPassword.Windows.Views;

internal sealed class AgentSettingsCard : Card
{
    private readonly ShellController _shell;
    private readonly StackPanel _actions = new() { Spacing = 10 };

    public AgentSettingsCard(ShellController shell)
    {
        _shell = shell;
        Header = "AI 连接器";
        Classes.Add("flat");
        var toggle = new ToggleSwitch { IsChecked = shell.Prefs.AgentRequests };
        Avalonia.Automation.AutomationProperties.SetName(toggle, "接收 AI 请求");
        toggle.IsCheckedChanged += (_, _) => shell.SetAgentRequests(toggle.IsChecked == true);
        var address = new SelectableTextBlock { Text = ShellController.AgentEndpoint, TextWrapping = Avalonia.Media.TextWrapping.Wrap };
        Content = new StackPanel { Spacing = 10, Margin = new Thickness(0, 6, 0, 0), Children = {
            SettingRows.SettingRow("接收 AI 请求", "在密码库解锁期间接收 Codex 和 Grok Bot 的请求，每次由你决定是否批准。", toggle),
            address,
            _actions,
        } };
        foreach (var client in new[] { "codex", "grok" })
        {
            var name = ShellController.AgentClientName(client);
            var connect = DialogKit.Btn("生成连接令牌");
            var revoke = DialogKit.Btn("撤销连接");
            connect.Click += async (_, _) => await ChangeAsync(client, true);
            revoke.Click += async (_, _) => await ChangeAsync(client, false);
            var buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, Children = { connect, revoke } };
            _actions.Children.Add(SettingRows.SettingRow(name, null, buttons));
        }
    }

    private async Task ChangeAsync(string client, bool make)
    {
        var name = ShellController.AgentClientName(client);
        if (make && !_shell.VaultUnlocked)
        {
            _shell.ShowStatus("请先解锁密码库。", StatusKind.Error);
            return;
        }
        var title = make ? $"生成 {name} 连接令牌" : $"撤销 {name} 连接";
        if (!await VaultUi.ConfirmAsync(this, make
                ? "新令牌有效期为 30 天。生成时会替换此设备上该客户端的旧令牌。"
                : "撤销后，此客户端的旧令牌和待批准请求立即失效。",
            title, make ? "生成" : "撤销")) return;
        _actions.IsEnabled = false;
        try
        {
            var result = await _shell.SessionCallAsync(make ? "agent.connect" : "agent.revoke", new { client });
            if (!make)
            {
                _shell.ShowStatus($"{name} 已撤销。", StatusKind.Success);
                return;
            }
            var value = JsonRead.Str(result, "token");
            if (value.Length == 0 || !_shell.VaultUnlocked) return;
            await ShowConnectionAsync(name, value, client == "grok");
        }
        catch (CoreException e)
        {
            _shell.ShowStatus(e.Code switch
            {
                "SYNC_SIGN_IN" or "SYNC_ACCOUNT" => "请先登录 Moon SSO。",
                "VAULT_LOCKED" => "请先解锁密码库。",
                _ => "暂时无法连接 MoonPassword 服务。",
            }, StatusKind.Error);
        }
        finally { _actions.IsEnabled = true; }
    }

    private async Task ShowConnectionAsync(string name, string value, bool grok)
    {
        var copy = DialogKit.Btn(grok ? "复制 HTTP 标头值" : "复制连接令牌", 130, "primary");
        var done = DialogKit.Btn("完成");
        var hint = grok
            ? "添加远程 HTTPS 连接器，服务器 URL 使用上面的地址。HTTP 标头名称填 Authorization，再粘贴复制的值。客户端 ID 留空。"
            : "添加远程 MCP 服务器，服务器 URL 使用上面的地址。把复制的令牌用作 Bearer token。";
        var status = Ui.Text("", "caption", "secondary");
        var body = new StackPanel { Spacing = 10, Children = {
            Ui.Text($"{name} 连接令牌", "title"), Ui.Text(hint, "secondary"), status, DialogKit.ButtonRow(done, copy),
        } };
        var dialog = DialogKit.Create(_shell, $"{name} 连接令牌", 510, body);
        copy.Click += async (_, _) =>
        {
            if (!_shell.VaultUnlocked) return;
            status.Text = await _shell.CopySensitiveTextAsync(grok ? "Bearer " + value : value, $"{name} 连接令牌", 30)
                ?? "已复制，30 秒后自动清除。";
        };
        done.Click += (_, _) => dialog.Close();
        await dialog.ShowModalAsync(VaultUi.OwnerOf(this));
    }
}
