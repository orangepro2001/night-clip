using System.Text.Json;
using Avalonia.Controls;
using Avalonia.Threading;
using Lumen.UI.Controls;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Views;

namespace MoonPassword.Windows.Services;

internal sealed partial class ShellController
{
    internal const string AgentEndpoint = "https://release.moonl.cloud/vault-agent/mcp";
    private DispatcherTimer? _agentPoll;
    private bool _agentProcessing;

    public void SetAgentRequests(bool enabled)
    {
        Prefs.AgentRequests = enabled;
        Prefs.Save(App.DataDir);
        StateChanged?.Invoke();
    }

    private void StartAgentPoll() =>
        _agentPoll = UiTimer.Start(TimeSpan.FromSeconds(8), () => _ = PollAgentAsync());

    private async Task PollAgentAsync()
    {
        if (_agentProcessing || !CoreRunning || !Prefs.AgentRequests || !VaultUnlocked) return;
        _agentProcessing = true;
        try
        {
            var pending = JsonRead.Obj(await SessionCallAsync("agent.next"), "request");
            if (pending.ValueKind != JsonValueKind.Object || _disposed || !VaultUnlocked || !Prefs.AgentRequests) return;
            var credential = await ApproveAgentRequestAsync(pending);
            if (!_disposed)
            {
                if (credential is null || !VaultUnlocked || !Prefs.AgentRequests)
                {
                    await SessionCallAsync("agent.deny", new { request = pending });
                }
                else
                {
                    var result = await SessionCallAsync("agent.approve", new { request = pending, entry_id = credential });
                    if (JsonRead.Str(result, "state") == "done") ShowStatus("已将仓库概要返回给连接器。", StatusKind.Success);
                    else ShowStatus("读取失败，请检查 GitHub 令牌的仓库访问权限。", StatusKind.Error);
                }
            }
        }
        catch (CoreException e)
        {
            // An expired or cancelled request is normal when the vault locks or a connection is revoked.
            if (e.Code is "AGENT_EXPIRED" or "SYNC_SIGN_IN" or "VAULT_LOCKED") return;
            AppLog.Write("agent request failed: " + e.Code);
        }
        catch (InvalidOperationException) when (_disposed) { }
        finally { _agentProcessing = false; }
    }

    private sealed record AgentCredential(string Id, string Title)
    {
        public override string ToString() => Title;
    }

    private async Task<string?> ApproveAgentRequestAsync(JsonElement request)
    {
        var client = AgentClientName(JsonRead.Str(request, "client"));
        var repo = JsonRead.Str(request, "repository");
        var life = DateTimeOffset.FromUnixTimeSeconds(JsonRead.Long(request, "expires")) - DateTimeOffset.UtcNow;
        if (client.Length == 0 || life <= TimeSpan.Zero || life > TimeSpan.FromMinutes(3)) return null;
        var response = await SessionCallAsync("agent.credentials");
        if (_disposed || !VaultUnlocked || !Prefs.AgentRequests) return null;
        var entries = JsonRead.Arr(response, "entries")
            .Select(e => new AgentCredential(JsonRead.Str(e, "id"), JsonRead.Str(e, "title")))
            .ToArray();
        var pick = new ComboBox { ItemsSource = entries, PlaceholderText = "选择用于此次请求的 GitHub API 密钥" };
        var cancel = DialogKit.Btn("拒绝");
        var allow = DialogKit.Btn("允许一次", 105, "primary");
        allow.IsEnabled = false;
        pick.SelectionChanged += (_, _) => allow.IsEnabled = pick.SelectedItem is AgentCredential;
        var body = new StackPanel { Spacing = 10, Children = {
            Ui.Text($"{client} 请求读取 GitHub 仓库概要", "title"),
            Ui.Text(repo, "mono"),
            Ui.Text(entries.Length == 0
                ? "请先在密码库添加一项 API 密钥，将网址设为 https://github.com 或 https://api.github.com。"
                : "完成后只返回仓库概要。此处选择的令牌仅用于本次 GitHub 请求。", "secondary"),
            pick, DialogKit.ButtonRow(cancel, allow),
        } };
        var dialog = DialogKit.Create(this, "批准 AI 连接器请求", 530, body);
        dialog.Topmost = true;
        string? selected = null;
        cancel.Click += (_, _) => dialog.Close();
        allow.Click += (_, _) =>
        {
            if (VaultUnlocked && Prefs.AgentRequests && pick.SelectedItem is AgentCredential item) selected = item.Id;
            dialog.Close();
        };
        using var expires = new CancellationTokenSource(life);
        using var expiry = expires.Token.Register(() => Dispatcher.UIThread.Post(() =>
        {
            if (dialog.IsVisible) dialog.Close();
        }));
        await dialog.ShowModalAsync(Main);
        return expires.IsCancellationRequested ? null : selected;
    }

    internal static string AgentClientName(string client) => client switch
    {
        "codex" => "Codex",
        "grok" => "Grok Bot",
        _ => "",
    };
}
