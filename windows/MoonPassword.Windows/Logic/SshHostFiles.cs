using System.Globalization;
using System.Net;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text.RegularExpressions;

namespace MoonPassword.Windows.Logic;

public sealed record SshHostForm(string Alias, string Host, string User, int Port, string Jump = "", string HostKeyAlias = "")
{
    private static readonly Regex Name = new(@"\A[A-Za-z0-9_][A-Za-z0-9._-]{0,62}\z", RegexOptions.CultureInvariant);
    private static readonly Regex Dns = new(@"\A[A-Za-z0-9_][A-Za-z0-9._-]{0,252}\z", RegexOptions.CultureInvariant);
    private static readonly Regex JumpPart = new(@"\A(?:[A-Za-z0-9_][A-Za-z0-9._-]{0,62}@)?(?:[A-Za-z0-9_][A-Za-z0-9._-]{0,252}|\[[0-9A-Fa-f:]+\])(?::[0-9]{1,5})?\z", RegexOptions.CultureInvariant);
    public static bool IsAlias(string value) => Name.IsMatch(value);

    public static void Check(SshHostForm host)
    {
        if (!IsAlias(host.Alias)) throw new ArgumentException("别名只能包含英文字母、数字、点、下划线和短横线。");
        if (!Dns.IsMatch(host.Host) && (!IPAddress.TryParse(host.Host, out _) || host.Host.Contains('%')))
            throw new ArgumentException("请输入有效的服务器域名或 IP 地址。");
        if (!Name.IsMatch(host.User)) throw new ArgumentException("请输入有效的 SSH 登录用户名。");
        if (host.Port is < 1 or > 65535) throw new ArgumentException("SSH 端口必须在 1 到 65535 之间。");
        if (host.Jump.Length > 0 && (host.Jump.Length > 1024 || host.Jump.Split(',').Any(part => !JumpPart.IsMatch(part))))
            throw new ArgumentException("跳板机格式应为别名、user@host 或 user@host:port。");
        if (host.HostKeyAlias.Length > 0 && !Dns.IsMatch(host.HostKeyAlias))
            throw new ArgumentException("已有配置的 HostKeyAlias 无法安全迁移。");
    }
}

public sealed record SshManagedIdentity(string PublicId, string Pipe);

/// <summary>Owns only the files in ~/.ssh/moonpassword. A single Include enables them in the user's config.</summary>
public sealed class SshHostFiles(string sshFolder)
{

    private const string Marker = "# Managed by MoonPassword";
    private const int MaxConfigBytes = 2 * 1024 * 1024;
    private static readonly Regex Id = new(@"\A[0-9a-f]{32}\z", RegexOptions.CultureInvariant);
    private static readonly Regex AgentPipe = new(@"\A\\\\\.\\pipe\\[A-Za-z0-9._-]+\z", RegexOptions.CultureInvariant);

    private readonly string _folder = sshFolder;
    private string HostsFolder => System.IO.Path.Combine(_folder, "moonpassword", "hosts");
    public string Include => "Include " + Quote(Slash(HostsFolder) + "/*.conf");
    private static string Quote(string value) => string.Concat('"', value, '"');
    public string PublicPath(string id) => Slash(System.IO.Path.Combine(_folder, "moonpassword", "keys", id + ".pub"));
    private static string Slash(string path) => path.Replace('\\', '/');
    public IReadOnlyList<string> ManagedAliases() => !Directory.Exists(HostsFolder) ? [] :
        Directory.GetFiles(HostsFolder, "host-*.conf")
            .Select(p => System.IO.Path.GetFileNameWithoutExtension(p)[5..])
            .Where(a => SshHostForm.IsAlias(a) && IsManaged(a))
            .Order(StringComparer.OrdinalIgnoreCase).ToArray();

    private string HostFile(string alias)
    {
        if (!SshHostForm.IsAlias(alias)) throw new ArgumentException("无效的 SSH 别名。");
        return System.IO.Path.Combine(HostsFolder, "host-" + alias.ToLowerInvariant() + ".conf");
    }

    public bool IsManaged(string alias) => Read(HostFile(alias))?.StartsWith(Marker + "\n", StringComparison.Ordinal) == true;

    public SshManagedIdentity? Identity(string alias)
    {
        var content = Read(HostFile(alias));
        if (content?.StartsWith(Marker + "\n", StringComparison.Ordinal) != true) return null;
        var fields = SshEffectiveHost.Split(content);
        var file = fields.First("IdentityFile");
        if (!file.EndsWith(".pub", StringComparison.OrdinalIgnoreCase)) return null;
        var id = System.IO.Path.GetFileNameWithoutExtension(file);
        if (!file.Equals(PublicPath(id), StringComparison.OrdinalIgnoreCase)) return null;
        var pipe = fields.First("IdentityAgent");
        return Id.IsMatch(id) && ValidPipe(pipe) ? new SshManagedIdentity(id, pipe) : null;
    }

    public Change Begin() => new(this);

    public static bool ValidPipe(string path) => AgentPipe.IsMatch(path);
    public static bool ValidId(string value) => Id.IsMatch(value);

    private static string? Read(string path)
    {
        if (!File.Exists(path)) return null;
        var file = new FileInfo(path);
        if ((file.Attributes & FileAttributes.ReparsePoint) != 0)
            throw new IOException("SSH 配置指向符号链接。请在其原始文件中管理该配置。");
        if (file.Length > MaxConfigBytes) throw new IOException("SSH 配置文件过大。");
        return File.ReadAllText(path, new System.Text.UTF8Encoding(false, true));
    }

    private static void Replace(string path, string? value)
    {
        if (value is null)
        {
            File.Delete(path);
            return;
        }
        Directory.CreateDirectory(System.IO.Path.GetDirectoryName(path)!);
        var temp = path + ".moonpassword-" + Guid.NewGuid().ToString("N");
        try
        {
            File.WriteAllText(temp, value, new System.Text.UTF8Encoding(false));
            if (OperatingSystem.IsWindows())
            {
                if (File.Exists(path))
                {
                    // Preserve this config file's existing ownership and ACL.
                    File.Replace(temp, path, null);
                    return;
                }
                using var user = WindowsIdentity.GetCurrent();
                var acl = new FileSecurity();
                acl.SetOwner(user.User!);
                acl.SetAccessRuleProtection(isProtected: true, preserveInheritance: false);
                acl.AddAccessRule(new FileSystemAccessRule(user.User!, FileSystemRights.FullControl, AccessControlType.Allow));
                new FileInfo(temp).SetAccessControl(acl);
            }
            File.Move(temp, path, overwrite: true);
        }
        finally { File.Delete(temp); }
    }

    public sealed class Change(SshHostFiles files) : IDisposable
    {
        private readonly List<(string Path, string? Before, string After)> _changes = [];
        private bool _committed;

        public void Set(SshHostForm host, SshManagedIdentity? key, string? publicKey = null)
        {
            SshHostForm.Check(host);
            var file = files.HostFile(host.Alias);
            var existing = Read(file);
            if (existing is not null && !existing.StartsWith(Marker + "\n", StringComparison.Ordinal))
                throw new IOException("此别名的配置文件不是由 MoonPassword 管理的。");
            var lines = new List<string> { Marker, "Host " + host.Alias, "  HostName " + host.Host,
                "  User " + host.User, "  Port " + host.Port.ToString(CultureInfo.InvariantCulture) };
            if (host.Jump.Length > 0) lines.Add("  ProxyJump " + host.Jump);
            if (host.HostKeyAlias.Length > 0) lines.Add("  HostKeyAlias " + host.HostKeyAlias);
            if (key is not null)
            {
                if (!ValidId(key.PublicId) || !ValidPipe(key.Pipe)) throw new ArgumentException("无效的 SSH Agent 配置。");
                if (publicKey is not null)
                {
                    var pub = files.PublicPath(key.PublicId);
                    var value = publicKey.TrimEnd() + "\n";
                    var old = Read(pub);
                    if (old is not null && old != value) throw new IOException("已存在不匹配的 SSH 公钥文件。");
                    Write(pub, old, value);
                }
                lines.Add("  IdentityFile " + Quote(files.PublicPath(key.PublicId)));
                lines.Add("  IdentitiesOnly yes");
                lines.Add("  IdentityAgent \"" + key.Pipe.Replace("\\", "\\\\") + "\"");
            }
            Write(file, existing, string.Join("\n", lines) + "\n");
            var config = System.IO.Path.Combine(files._folder, "config");
            var original = Read(config);
            if (original?.Split('\n').Any(line => line.Trim() == files.Include) != true)
                Write(config, original, files.Include + "\n" + (original ?? ""));
        }

        private void Write(string path, string? before, string after)
        {
            if (before == after) return;
            if (Read(path) != before) throw new IOException("SSH 配置已被另一个程序修改，请重试。");
            Replace(path, after);
            _changes.Add((path, before, after));
        }

        public void Commit() => _committed = true;

        public void Dispose()
        {
            if (_committed) return;
            IOException? error = null;
            for (int i = _changes.Count - 1; i >= 0; i--)
            {
                var c = _changes[i];
                try { if (Read(c.Path) == c.After) Replace(c.Path, c.Before); }
                catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
                { error = new IOException("无法回退本机 SSH 配置，请检查 ~/.ssh/config。", ex); }
            }
            if (error is not null) throw error;
        }
    }
}

public sealed class SshEffectiveHost
{
    private readonly List<(string Key, string Value)> _items = [];
    public static SshEffectiveHost Split(string text)
    {
        var result = new SshEffectiveHost();
        foreach (var raw in text.Split('\n'))
        {
            var line = raw.Trim();
            var i = line.IndexOfAny([' ', '\t']);
            if (i <= 0 || line.StartsWith('#')) continue;
            var key = line[..i];
            var val = line[(i + 1)..].Trim();
            if (val.StartsWith('"') && val.EndsWith('"') && val.Length > 1)
                val = val[1..^1].Replace("\\\\", "\\");
            result._items.Add((key, val));
        }
        return result;
    }
    public string First(string directive) => Values(directive).FirstOrDefault() ?? "";
    public IEnumerable<string> Values(string directive) => _items.Where(p => p.Key.Equals(directive, StringComparison.OrdinalIgnoreCase)).Select(p => p.Value);

    public SshHostForm ToForm(string alias) => new(alias, First("hostname"), First("user"),
        int.TryParse(First("port"), out var p) ? p : 22, Optional("proxyjump"), Optional("hostkeyalias"));

    public string Optional(string directive)
    {
        var val = First(directive);
        return val.Equals("none", StringComparison.OrdinalIgnoreCase) ? "" : val;
    }
}
