using System.ComponentModel;
using System.Diagnostics;
using System.Globalization;
using System.Text.RegularExpressions;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;

namespace MoonPassword.Windows.Services;

internal sealed record SshProvisionedHost(SshHostForm Form, string NewKey, string OldKey);

internal sealed class SshHostSetupService(SshApi ssh, string sshFolder, string sshExe, bool useDefaultConfig = false)
{
    private readonly SshHostFiles _files = new(sshFolder);
    private static readonly Regex HostFingerprint = new(@"^debug1: Server host key: \S+ (SHA256:[A-Za-z0-9+/]{43}=?)\s*$", RegexOptions.Multiline);
    private static readonly Regex AcceptedKey = new(@"^debug1: Server accepts key: [^\r\n]*? (SHA256:[A-Za-z0-9+/]{43})(?:=)?(?:\s|$)", RegexOptions.Multiline);
    private static readonly Regex Fingerprint = new(@"\ASHA256:[A-Za-z0-9+/]{43}\z");
    private static readonly Regex Ed25519 = new(@"\Assh-ed25519 [A-Za-z0-9+/]+={0,2}\z");
    private static readonly SemaphoreSlim EditLock = new(1, 1);

    private static readonly string[] SafeConnection = [
        "-T", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ClearAllForwardings=yes",
        "-o", "PermitLocalCommand=no", "-o", "RemoteCommand=none", "-o", "ControlMaster=no",
        "-o", "ControlPath=none", "-o", "ControlPersist=no", "-o", "ConnectTimeout=8",
    ];

    public static SshHostSetupService ForUser(SshApi ssh) => new(ssh,
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".ssh"),
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "System32", "OpenSSH", "ssh.exe"), useDefaultConfig: true);

    public async Task<IReadOnlyList<(string Alias, bool Managed)>> HostsAsync()
    {
        var targets = await Task.Run(() => ssh.ConfigTargets(Path.Combine(sshFolder, "config")));
        return _files.ManagedAliases().Concat(targets.Targets.Select(t => t.Alias))
            .Where(SshHostForm.IsAlias).Distinct(StringComparer.OrdinalIgnoreCase)
            .Order(StringComparer.OrdinalIgnoreCase).Select(a => (a, _files.IsManaged(a))).ToArray();
    }

    public Task<SshKeyList> KeysAsync() => Task.Run(ssh.Keys);

    public async Task<SshHostForm> ReadAliasAsync(string alias)
    {
        if (!SshHostForm.IsAlias(alias)) throw new ArgumentException("无效的 SSH 别名。");
        return (await EffectiveAsync(alias)).ToForm(alias);
    }

    public async Task SaveAliasAsync(SshHostForm form, SshKeyInfo? selectedKey)
    {
        await EditLock.WaitAsync();
        try
        {
            await ValidateAliasAsync(form);
            var previous = _files.Identity(form.Alias);
            // If an alias was already provisioned, prevent an innocent edit from sending its
            // identity to a different SSH account or to a host with a different known_hosts name.
            if (previous is not null && selectedKey is null)
            {
                var old = await ReadAliasAsync(form.Alias);
                if (!SameEndpoint(form, old))
                    throw new InvalidOperationException("此别名已绑定密钥。更换服务器、账号或端口时请新建别名。");
            }
            var identity = selectedKey is null ? previous : await IdentityAsync(selectedKey.EntryId);
            using var edit = _files.Begin();
            edit.Set(form, identity, selectedKey?.PublicKey is { Length: > 0 } pk ? pk :
                selectedKey is not null ? await Task.Run(() => ssh.PublicKey(selectedKey.EntryId)) : null);
            await CheckEffectiveAsync(form, identity);
            edit.Commit();
        }
        finally { EditLock.Release(); }
    }

    public async Task<SshProvisionedHost> ProvisionAsync(SshHostForm form, string sourceAlias, Action<string> status)
    {
        await EditLock.WaitAsync();
        string? newId = null;
        var installed = false;
        try
        {
            await ValidateAliasAsync(form);
            if (_files.Identity(form.Alias) is not null)
                throw new InvalidOperationException("该别名已经关联密钥。请为另一台服务器或账号创建新别名。");
            if (IsHostedGit(form))
                throw new InvalidOperationException("GitHub 和 Codeberg 的公钥需要在网页账户设置里添加。请使用「保存别名」关联已有密钥。");
            await ValidateBootstrapAsync(form, sourceAlias);
            var agent = await Task.Run(ssh.Status);
            if (!agent.Listening || !SshHostFiles.ValidPipe(agent.Pipe))
                throw new InvalidOperationException("请先启用 MoonPassword SSH 代理，并确保管道正在监听。");
            if (!(await KeysAsync()).VaultUnlocked)
                throw new InvalidOperationException("请先解锁密码库，再配置专属密钥。");

            status("正在生成只用于此服务器的密钥…");
            var key = await Task.Run(() => ssh.Generate("ed25519", 0, form.Alias, agent: false));
            newId = key.Id;
            var identity = new SshManagedIdentity(key.Id, agent.Pipe);
            var line = AuthorizedLine(key);

            status("请在打开的 OpenSSH 窗口确认主机指纹或输入登录密码。");
            var (fingerprint, oldKey) = await BootstrapAsync(form, sourceAlias, line);
            installed = true;

            status("正在用新密钥单独验证登录…");
            await Task.Run(() =>
            {
                ssh.SetHosts(key.Id, [fingerprint]);
                ssh.SetAgent(key.Id, true);
            });
            await WaitForKeyAsync(key.Id);
            using var edit = _files.Begin();
            edit.Set(form, identity, key.PublicKey);
            await CheckEffectiveAsync(form, identity);
            if (!await AuthenticateNewKeyAsync(form.Alias, key.Fingerprint, "true"))
                throw new IOException("新密钥登录验证未通过。请检查服务器是否使用此账号的 ~/.ssh/authorized_keys。");
            edit.Commit();
            return new SshProvisionedHost(form, key.Fingerprint, oldKey);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or InvalidOperationException or CoreException or Win32Exception)
        {
            if (newId is not null)
            {
                try { await Task.Run(() => ssh.SetAgent(newId, false)); }
                catch (CoreException) { /* A session lock also clears the Agent. */ }
            }
            if (installed || newId is not null)
                throw new IOException(ex.Message +
                    "\n原登录授权仍可使用。本次生成的密钥保留在密码库中，远端可能已添加对应公钥。", ex);
            throw;
        }
        finally { EditLock.Release(); }
    }

    public async Task RevokePreviousAsync(SshProvisionedHost setup)
    {
        if (!Fingerprint.IsMatch(setup.NewKey) || !Fingerprint.IsMatch(setup.OldKey) || setup.NewKey == setup.OldKey)
            throw new InvalidOperationException("没有可安全识别的旧公钥。");
        await EditLock.WaitAsync();
        try
        {
            var id = _files.Identity(setup.Form.Alias) ?? throw new IOException("找不到已验证的别名配置。");
            await CheckEffectiveAsync(setup.Form, id);
            var key = (await KeysAsync()).Keys.SingleOrDefault(k => k.EntryId == id.PublicId);
            if (key?.Fingerprint != setup.NewKey || !key.Loaded)
                throw new InvalidOperationException("请先解锁密码库，确保这把新密钥已加载。");
            if (!await AuthenticateNewKeyAsync(setup.Form.Alias, setup.NewKey, "true"))
                throw new IOException("无法再次用新密钥登录，旧授权尚未移除。");
            if (!await AuthenticateNewKeyAsync(setup.Form.Alias, setup.NewKey, RevokeCommand(setup.OldKey, setup.NewKey)))
                throw new IOException("服务器未确认撤销。请检查旧公钥是否还在授权文件中。");
        }
        finally { EditLock.Release(); }
    }

    private async Task<bool> AuthenticateNewKeyAsync(string alias, string expected, string command)
    {
        var args = ClientArgs(SafeConnection.Concat(["-v", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
            "-o", "PreferredAuthentications=publickey", "--", alias, command]));
        var (code, _, trace) = await SshEnvProbe.RunAsync(sshExe, args);
        return code == 0 && AcceptedKey.Matches(trace).Any(m => m.Groups[1].Value == expected);
    }

    public static bool IsHostedGit(SshHostForm form) => form.Host.TrimEnd('.').ToLowerInvariant() is
        "github.com" or "ssh.github.com" or "codeberg.org" or "ssh.codeberg.org";

    private async Task ValidateAliasAsync(SshHostForm form)
    {
        SshHostForm.Check(form);
        if (!File.Exists(sshExe)) throw new IOException("找不到 Windows OpenSSH 客户端。");
        var hosts = await HostsAsync();
        if (hosts.Any(h => h.Alias.Equals(form.Alias, StringComparison.OrdinalIgnoreCase) && !h.Managed))
            throw new InvalidOperationException("同名别名由现有 SSH 配置管理。请改用新别名，原别名可作为登录来源。");
    }

    private async Task<SshManagedIdentity> IdentityAsync(string id)
    {
        var state = await Task.Run(ssh.Status);
        if (!state.Listening || !SshHostFiles.ValidPipe(state.Pipe))
            throw new InvalidOperationException("请先启用 MoonPassword SSH 代理。");
        return new SshManagedIdentity(id, state.Pipe);
    }

    private async Task<SshEffectiveHost> EffectiveAsync(string alias)
    {
        var (code, text, _) = await SshEnvProbe.RunAsync(sshExe, ClientArgs(["-G", "--", alias]));
        if (code != 0) throw new IOException("OpenSSH 无法解析当前配置，请检查 ~/.ssh/config。");
        return SshEffectiveHost.Split(text);
    }

    private async Task CheckEffectiveAsync(SshHostForm form, SshManagedIdentity? key)
    {
        var effective = await EffectiveAsync(form.Alias);
        if (!SameEndpoint(form, effective.ToForm(form.Alias)) ||
            !form.Jump.Equals(effective.Optional("proxyjump"), StringComparison.OrdinalIgnoreCase))
            throw new IOException("OpenSSH 的实际连接配置与表单不一致，可能存在更早匹配的 Host 或 Match 规则。");
        if (key is null) return;
        var identities = effective.Values("identityfile").ToArray();
        if (effective.First("identitiesonly") != "yes" || !effective.First("identityagent").Equals(key.Pipe, StringComparison.OrdinalIgnoreCase) ||
            identities.Length != 1 || !identities[0].Replace('\\', '/').Equals(_files.PublicPath(key.PublicId), StringComparison.OrdinalIgnoreCase))
            throw new IOException("此别名继承了其他 IdentityFile 或 IdentityAgent。请调整现有 SSH 配置后重试。");
    }

    private static bool SameEndpoint(SshHostForm x, SshHostForm y) =>
        x.Host.Equals(y.Host, StringComparison.OrdinalIgnoreCase) && x.User == y.User && x.Port == y.Port &&
        x.HostKeyAlias.Equals(y.HostKeyAlias, StringComparison.OrdinalIgnoreCase);

    private async Task ValidateBootstrapAsync(SshHostForm form, string source)
    {
        if (source.Length == 0) return;
        if (!SshHostForm.IsAlias(source)) throw new ArgumentException("无效的登录来源别名。");
        var config = await EffectiveAsync(source);
        var original = config.ToForm(source);
        if (!SameEndpoint(form, original) || !form.Jump.Equals(original.Jump, StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException("登录来源的主机、账号、端口或跳板机与表单不同。请重新选取来源，或清空来源后直接登录。");
        if (config.Optional("proxycommand").Length > 0)
            throw new InvalidOperationException("此来源使用自定义 ProxyCommand。请用 ProxyJump 别名或直接连接。");
    }

    private async Task<(string Host, string OldKey)> BootstrapAsync(SshHostForm form, string source, string publicKey)
    {
        var log = Path.Combine(Path.GetTempPath(), "moonpassword-ssh-" + Guid.NewGuid().ToString("N") + ".log");
        try
        {
            var args = new List<string>(SafeConnection) {
                "-v", "-E", log, "-o", "BatchMode=no", "-o", "StrictHostKeyChecking=ask",
                "-o", "HostName=" + form.Host, "-l", form.User, "-p", form.Port.ToString(CultureInfo.InvariantCulture)
            };
            if (form.Jump.Length > 0) { args.Add("-J"); args.Add(form.Jump); }
            else { args.Add("-o"); args.Add("ProxyCommand=none"); }
            if (form.HostKeyAlias.Length > 0) { args.Add("-o"); args.Add("HostKeyAlias=" + form.HostKeyAlias); }
            args.Add("--");
            args.Add(source.Length > 0 ? source : form.Host);
            args.Add(InstallerCommand(publicKey));
            var start = new ProcessStartInfo(sshExe) { UseShellExecute = true, WindowStyle = ProcessWindowStyle.Normal };
            foreach (var arg in ClientArgs(args)) start.ArgumentList.Add(arg);
            using var process = Process.Start(start) ?? throw new IOException("无法启动 OpenSSH 登录窗口。");
            await process.WaitForExitAsync();
            if (process.ExitCode != 0 || !File.Exists(log))
                throw new IOException("旧凭据登录或公钥写入失败。如果打开了 SSH 窗口，请核对登录和服务器指纹。");
            if (new FileInfo(log).Length > 262144) throw new IOException("OpenSSH 诊断过大，无法确认服务器指纹。");
            var trace = await File.ReadAllTextAsync(log);
            var fingerprints = HostFingerprint.Matches(trace).Select(m => m.Groups[1].Value).Distinct().ToArray();
            if (fingerprints.Length != 1)
                throw new IOException("无法从 OpenSSH 确认唯一的服务器指纹。");
            var oldKeys = AcceptedKey.Matches(trace).Select(m => m.Groups[1].Value).Distinct().ToArray();
            var old = oldKeys.Length == 1 && trace.Contains("using \"publickey\"", StringComparison.Ordinal) ? oldKeys[0] : "";
            return (fingerprints[0].TrimEnd('='), old);
        }
        finally { File.Delete(log); }
    }

    private string[] ClientArgs(IEnumerable<string> args) => useDefaultConfig ? args.ToArray() :
        ["-F", File.Exists(Path.Combine(sshFolder, "config")) ? Path.Combine(sshFolder, "config") : "none", ..args];

    private static string AuthorizedLine(SshStored key)
    {
        var parts = key.PublicKey.Split(' ', StringSplitOptions.RemoveEmptyEntries);
        var line = parts.Length >= 2 ? parts[0] + " " + parts[1] : "";
        if (!Ed25519.IsMatch(line) || !SshHostFiles.ValidId(key.Id)) throw new IOException("无法识别生成的 SSH 公钥。");
        return line + " MoonPassword-" + key.Id;
    }

    // No user-supplied strings are placed in the remote shell program. The line contains
    // only the generated key type, base64 and a validated UUID.
    internal static string InstallerCommand(string line) => "set -eu; umask 077; " +
        "d=\"$HOME/.ssh\"; f=\"$d/authorized_keys\"; " +
        "test ! -L \"$d\"; test ! -L \"$f\"; mkdir -p \"$d\"; " +
        "if test -e \"$f\"; then test -f \"$f\"; else : > \"$f\"; fi; " +
        "line='" + line + "'; if ! grep -Fqx -- \"$line\" \"$f\"; then printf '\\n%s\\n' \"$line\" >> \"$f\"; fi";

    // Replace the file only if the new key is present and nobody edited the original during the operation.
    private static string RevokeCommand(string oldKey, string newKey) =>
        "set -eu; umask 077; command -v ssh-keygen >/dev/null; " +
        "d=\"$HOME/.ssh\"; f=\"$d/authorized_keys\"; test ! -L \"$d\"; test ! -L \"$f\"; test -f \"$f\"; " +
        "src=$(mktemp \"$d/.moonpassword-src.XXXXXX\"); trap 'rm -f \"$src\" \"${out:-}\"' EXIT; " +
        "out=$(mktemp \"$d/.moonpassword-out.XXXXXX\"); cp -p \"$f\" \"$src\"; cp -p \"$src\" \"$out\"; " +
        "kept=0; removed=0; while IFS= read -r line || test -n \"$line\"; do " +
        "fp=$(printf '%s\\n' \"$line\" | ssh-keygen -lf - 2>/dev/null | awk 'NR == 1 {print $2}'); " +
        "if test \"$fp\" = '" + oldKey + "'; then removed=1; continue; fi; " +
        "if test \"$fp\" = '" + newKey + "'; then kept=1; fi; printf '%s\\n' \"$line\"; " +
        "done < \"$src\" > \"$out\"; test \"$kept\" = 1; " +
        "if test \"$removed\" = 1; then cmp -s \"$f\" \"$src\"; mv -f \"$out\" \"$f\"; fi";

    private async Task WaitForKeyAsync(string id)
    {
        for (int i = 0; i < 20; i++)
        {
            if ((await KeysAsync()).Keys.Any(k => k.EntryId == id && k.Loaded)) return;
            await Task.Delay(150);
        }
        throw new IOException("代理没有加载新密钥。请确认密码库保持解锁。");
    }
}
