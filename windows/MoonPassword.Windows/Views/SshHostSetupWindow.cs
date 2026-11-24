using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using Lumen.UI.Controls;

namespace MoonPassword.Windows.Views;

internal sealed class SshHostSetupWindow
{
    private sealed record HostChoice(string Alias, bool Managed)
    {
        public override string ToString() => Alias.Length == 0 ? "新建或直接登录" : Managed ? Alias : Alias + "（作为登录来源）";
    }

    private sealed record KeyChoice(SshKeyInfo? Key)
    {
        public override string ToString() => Key is null ? "保留当前设置" : $"{Key.Title} · {Key.Fingerprint}";
    }

    private readonly SshHostSetupService _setup;
    private readonly VaultWindow _window;
    private readonly TextBox _alias = new() { PlaceholderText = "例如 moon-admin" };
    private readonly TextBox _host = new() { PlaceholderText = "域名或 IP 地址" };
    private readonly TextBox _user = new() { PlaceholderText = "SSH 登录用户名" };
    private readonly TextBox _port = new() { Text = "22" };
    private readonly TextBox _jump = new() { PlaceholderText = "可留空，也可以填写已有跳板机别名" };
    private readonly CheckBox _keepAlias = new() { Content = "迁移后沿用原别名", IsVisible = false };
    private readonly ComboBox _existing = new() { HorizontalAlignment = HorizontalAlignment.Stretch };
    private readonly ComboBox _keys = new() { HorizontalAlignment = HorizontalAlignment.Stretch };
    private readonly InfoBar _info = new() { IsOpen = false, IsClosable = false };
    private readonly Button _save = DialogKit.Btn("保存别名", 112);
    private readonly Button _deploy = DialogKit.Btn("部署专属密钥", 134, "primary");
    private readonly Button _revoke = DialogKit.Btn("移除旧授权", 105);
    private readonly Button _close = DialogKit.Btn(Strings.BtnCancel, 88);
    private readonly StackPanel _inputs;
    private string _bootstrap = "";
    private string _hostKeyAlias = "";
    private bool _busy;
    private SshProvisionedHost? _provisioned;

    private SshHostSetupWindow(ShellController shell)
    {
        _setup = SshHostSetupService.ForUser(shell.Ssh);
        var types = new StackPanel
        {
            Orientation = Orientation.Horizontal, Spacing = 8,
            Children = {
                Ui.TextButton("GitHub", () => FillGit("github.com"), "soft", "sm"),
                Ui.TextButton("Codeberg", () => FillGit("codeberg.org"), "soft", "sm"),
            },
        };
        _inputs = new StackPanel
        {
            Spacing = 10,
            Children = {
                Field("已有别名", _existing),
                _keepAlias,
                Field("SSH 别名", _alias),
                Field("服务器地址", _host),
                Field("SSH 用户", _user),
                Field("端口", _port),
                Field("跳板机", _jump),
                Field("关联 Agent 密钥（可选）", _keys),
                types,
                DialogKit.Text("普通服务器可自动部署公钥。GitHub / Codeberg 请在网页设置中添加公钥，再保存本机别名。", "caption", "secondary")
                    .With(t => t.TextWrapping = TextWrapping.Wrap),
            },
        };
        var form = new ScrollViewer
        {
            Content = new StackPanel { Spacing = 12, Children = { _inputs, _info } },
            VerticalScrollBarVisibility = Avalonia.Controls.Primitives.ScrollBarVisibility.Auto,
        };
        var layout = new Grid { RowDefinitions = new RowDefinitions("*,Auto") };
        layout.Children.Add(form);
        _revoke.IsVisible = false;
        var buttons = DialogKit.ButtonRow(_close, _revoke, _save, _deploy);
        Grid.SetRow(buttons, 1);
        layout.Children.Add(buttons);
        _window = DialogKit.Create(shell, Strings.SshHostSetupTitle, 620, layout, height: 680);
        _window.Closing += (_, e) => e.Cancel = _busy;
        _window.Opened += async (_, _) => await RefreshAsync();
        _close.Click += (_, _) => _window.Close();
        _revoke.Click += async (_, _) => await RevokeAsync();
        _save.Click += async (_, _) => await RunAsync(deploy: false);
        _deploy.Click += async (_, _) => await RunAsync(deploy: true);
        _existing.SelectionChanged += async (_, _) => await ChooseAsync();
        _keepAlias.IsCheckedChanged += (_, _) =>
        {
            if (_existing.SelectedItem is HostChoice { Alias.Length: > 0, Managed: false } choice)
            {
                _alias.IsReadOnly = _keepAlias.IsChecked == true;
                _alias.Text = _alias.IsReadOnly ? choice.Alias : NewName(choice.Alias);
            }
            RefreshButtons();
        };
        _host.TextChanged += (_, _) => RefreshButtons();
    }

    public static async Task ShowAsync(Window? owner, ShellController shell) =>
        await new SshHostSetupWindow(shell)._window.ShowModalAsync(owner);

    private static FormField Field(string label, Control control) => new() { Label = label, Content = control };

    private async Task RefreshAsync()
    {
        SetBusy(true);
        try { await LoadChoicesAsync(); }
        catch (Exception ex) when (Expected(ex)) { Say(ex.Message, Severity.Danger); }
        finally { SetBusy(false); }
    }

    private async Task LoadChoicesAsync(string selected = "")
    {
        var aliases = await _setup.HostsAsync();
        var choices = new[] { new HostChoice("", false) }
            .Concat(aliases.Select(a => new HostChoice(a.Alias, a.Managed))).ToArray();
        _existing.ItemsSource = choices;
        _existing.SelectedItem = choices.FirstOrDefault(a => a.Alias.Equals(selected, StringComparison.OrdinalIgnoreCase)) ?? choices[0];
        var keys = await _setup.KeysAsync();
        _keys.ItemsSource = new[] { new KeyChoice(null) }
            .Concat(keys.Keys.Where(k => k.Agent && k.Usable).Select(k => new KeyChoice(k))).ToArray();
        _keys.SelectedIndex = 0;
    }

    private async Task ChooseAsync()
    {
        if (_busy || _existing.SelectedItem is not HostChoice choice) return;
        _keepAlias.IsChecked = false;
        _keepAlias.IsVisible = false;
        if (choice.Alias.Length == 0)
        {
            _bootstrap = "";
            _hostKeyAlias = "";
            _alias.IsReadOnly = false;
            return;
        }
        SetBusy(true);
        try
        {
            var form = await _setup.ReadAliasAsync(choice.Alias);
            _bootstrap = choice.Alias;
            _hostKeyAlias = form.HostKeyAlias;
            _alias.IsReadOnly = choice.Managed;
            _alias.Text = choice.Managed ? choice.Alias : NewName(choice.Alias);
            _keepAlias.IsVisible = !choice.Managed;
            _host.Text = form.Host;
            _user.Text = form.User;
            _port.Text = form.Port.ToString();
            _jump.Text = form.Jump;
            _keys.SelectedIndex = 0;
        }
        catch (Exception ex) when (Expected(ex)) { Say(ex.Message, Severity.Danger); }
        finally { SetBusy(false); }
    }

    private static string NewName(string alias) => alias[..Math.Min(alias.Length, 60)] + "-mp";

    private void FillGit(string domain)
    {
        _existing.SelectedIndex = 0;
        _keepAlias.IsChecked = false;
        _keepAlias.IsVisible = false;
        _bootstrap = _hostKeyAlias = "";
        _alias.IsReadOnly = false;
        _alias.Text = domain;
        _host.Text = domain;
        _user.Text = "git";
        _port.Text = "22";
        _jump.Text = "";
        Say("每个平台使用一把密钥。把公钥加到账户的 SSH 密钥设置里，然后在这里关联对应的 Agent 密钥。", Severity.Info);
    }

    private SshHostForm Form() => new(_alias.Text?.Trim() ?? "", _host.Text?.Trim() ?? "", _user.Text?.Trim() ?? "",
        int.TryParse(_port.Text, out var port) ? port : 0, _jump.Text?.Trim() ?? "", _hostKeyAlias);

    private async Task RunAsync(bool deploy)
    {
        if (_busy) return;
        _revoke.IsVisible = false;
        SetBusy(true);
        try
        {
            var form = Form();
            if (deploy)
            {
                _provisioned = await _setup.ProvisionAsync(form, _bootstrap, text => Say(text, Severity.Info), _keepAlias.IsChecked == true);
                _revoke.IsVisible = _provisioned.OldKey.Length > 0;
                var backup = _provisioned.BackupFolder.Length > 0 ? $"\n配置备份：{_provisioned.BackupFolder}" : "";
                Say($"部署并验证成功。可在终端运行 ssh {form.Alias}。\n新密钥指纹 {_provisioned.NewKey}\n原登录授权已保留。{backup}", Severity.Success);
            }
            else
            {
                await _setup.SaveAliasAsync(form, (_keys.SelectedItem as KeyChoice)?.Key);
                Say($"已保存，可在终端运行 ssh {form.Alias}。", Severity.Success);
            }
            _bootstrap = form.Alias;
            await LoadChoicesAsync(form.Alias);
            _keepAlias.IsVisible = false;
            _keepAlias.IsChecked = false;
            _alias.IsReadOnly = true;
        }
        catch (Exception ex) when (Expected(ex)) { Say(ex.Message, Severity.Danger); }
        finally { SetBusy(false); }
    }

    private async Task RevokeAsync()
    {
        if (_busy || _provisioned is not { } setup) return;
        var target = setup.Form;
        if (!await VaultUi.ConfirmAsync(_window,
            $"从 {target.User}@{target.Host}:{target.Port} 移除旧公钥 {setup.OldKey} 的授权？\n仍使用旧公钥连接这台机器的客户端将需要改用新密钥。",
            "移除旧 SSH 授权", "移除这把旧公钥", warning: true)) return;
        SetBusy(true);
        try
        {
            await _setup.RevokePreviousAsync(setup);
            _revoke.IsVisible = false;
            Say($"已从此账号的授权文件移除旧公钥。仍可运行 ssh {target.Alias}。", Severity.Success);
        }
        catch (Exception ex) when (Expected(ex)) { Say(ex.Message, Severity.Danger); }
        finally { SetBusy(false); }
    }

    private static bool Expected(Exception ex) => ex is ArgumentException or IOException or UnauthorizedAccessException or
        InvalidOperationException or CoreException or System.ComponentModel.Win32Exception;

    private void SetBusy(bool value)
    {
        _busy = value;
        _inputs.IsEnabled = !value;
        _close.IsEnabled = _revoke.IsEnabled = !value;
        RefreshButtons();
    }

    private void RefreshButtons()
    {
        _save.IsEnabled = !_busy && _keepAlias.IsChecked != true;
        _deploy.IsEnabled = !_busy && !SshHostSetupService.IsHostedGit(Form());
    }

    private void Say(string text, Severity severity)
    {
        _info.Severity = severity;
        _info.Message = text;
        _info.IsOpen = true;
    }
}
