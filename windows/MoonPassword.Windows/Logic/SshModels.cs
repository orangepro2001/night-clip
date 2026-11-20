using System.Text.Json;

namespace MoonPassword.Windows.Logic;

// Typed views of the ssh.* replies and events (docs/vault-api.md §3.9, docs/adr/009-ssh-agent.md).
// None of these carries private key material: the core only hands out public keys, fingerprints and
// metadata. Parsing is tolerant like JsonRead.

/// <summary>The "ssh" block of vault.get, and one item of ssh.keys.</summary>
public sealed record SshKeyInfo(
    string EntryId, string Title, string GroupPath, string KeyAttachment, string Algo, int Bits, string Fingerprint,
    string Comment, string PublicKey, bool Agent, bool Loaded, string SourceFormat, bool Encrypted,
    string Problem, string Reason, long LastUsedAt, long ExpiresAt)
{
    /// <summary>The servers the key is limited to (host patterns or "SHA256:" host key fingerprints); empty = any server.</summary>
    public IReadOnlyList<string> Hosts { get; init; } = Array.Empty<string>();

    public bool Usable => Problem.Length == 0 || Problem == "missing_settings";

    public static SshKeyInfo FromJson(JsonElement e) => new(
        JsonRead.Str(e, "entry_id"), JsonRead.Str(e, "title"), JsonRead.Str(e, "group_path"), JsonRead.Str(e, "key_attachment"),
        JsonRead.Str(e, "algo"), (int)JsonRead.Long(e, "bits"), JsonRead.Str(e, "fingerprint"), JsonRead.Str(e, "comment"),
        JsonRead.Str(e, "public_key"), JsonRead.Bool(e, "agent"), JsonRead.Bool(e, "loaded"), JsonRead.Str(e, "source_format"),
        JsonRead.Bool(e, "encrypted"), JsonRead.Str(e, "problem"), JsonRead.Str(e, "reason"),
        JsonRead.Long(e, "last_used_at"), JsonRead.Long(e, "expires_at"))
    {
        Hosts = JsonRead.StrArr(e, "hosts"),
    };

    /// <summary>"Ed25519", "RSA 3072", "ECDSA P-384".</summary>
    public string TypeText => SshText.Algo(Algo, Bits);
}

public sealed record SshKeyList(bool VaultUnlocked, IReadOnlyList<SshKeyInfo> Keys)
{
    public static SshKeyList FromJson(JsonElement r) =>
        new(JsonRead.Bool(r, "vault_unlocked"), JsonRead.Arr(r, "keys").Select(SshKeyInfo.FromJson).ToList());
}

/// <summary>ssh.inspect: what a key file is, without storing it.</summary>
public sealed record SshInspect(
    string Format, bool Encrypted, string Cipher, string Kdf, bool NeedsPassphrase, bool WrongPassphrase,
    string Algo, int Bits, string Fingerprint, string Comment, string PublicKey, IReadOnlyList<(string Id, string Title)> InVault)
{
    public static SshInspect FromJson(JsonElement r) => new(
        JsonRead.Str(r, "format"), JsonRead.Bool(r, "encrypted"), JsonRead.Str(r, "cipher"), JsonRead.Str(r, "kdf"),
        JsonRead.Bool(r, "needs_passphrase"), JsonRead.Bool(r, "wrong_passphrase"), JsonRead.Str(r, "algo"),
        (int)JsonRead.Long(r, "bits"), JsonRead.Str(r, "fingerprint"), JsonRead.Str(r, "comment"), JsonRead.Str(r, "public_key"),
        JsonRead.Arr(r, "in_vault").Select(v => (JsonRead.Str(v, "entry_id"), JsonRead.Str(v, "title"))).ToList());

    public string TypeText => SshText.Algo(Algo, Bits);
}

/// <summary>ssh.import / ssh.generate.</summary>
public sealed record SshStored(string Id, string Fingerprint, string PublicKey, string Algo, int Bits, string AttachmentName)
{
    public static SshStored FromJson(JsonElement r) => new(
        JsonRead.Str(r, "id"), JsonRead.Str(r, "fingerprint"), JsonRead.Str(r, "public_key"), JsonRead.Str(r, "algo"),
        (int)JsonRead.Long(r, "bits"), JsonRead.Str(r, "attachment_name"));
}

public sealed record SshRef(string File, int Line);

/// <summary>One private key file found by ssh.scan (never its content).</summary>
public sealed record SshScanItem(
    string Path, string Format, bool Encrypted, string Algo, int Bits, string Fingerprint, string Comment,
    string PubPath, string PubState, IReadOnlyList<string> ConfigHosts, IReadOnlyList<SshRef> ReferencedBy,
    string Problem, IReadOnlyList<(string Id, string Title)> InVault)
{
    public string Name => System.IO.Path.GetFileName(Path);
    public bool InVaultAlready => InVault.Count > 0;

    public static SshScanItem FromJson(JsonElement e) => new(
        JsonRead.Str(e, "path"), JsonRead.Str(e, "format"), JsonRead.Bool(e, "encrypted"), JsonRead.Str(e, "algo"),
        (int)JsonRead.Long(e, "bits"), JsonRead.Str(e, "fingerprint"), JsonRead.Str(e, "comment"), JsonRead.Str(e, "pub_path"),
        JsonRead.Str(e, "pub_state"), JsonRead.StrArr(e, "config_hosts"),
        JsonRead.Arr(e, "referenced_by").Select(r => new SshRef(JsonRead.Str(r, "file"), (int)JsonRead.Long(r, "line"))).ToList(),
        JsonRead.Str(e, "problem"),
        JsonRead.Arr(e, "in_vault").Select(v => (JsonRead.Str(v, "entry_id"), JsonRead.Str(v, "title"))).ToList());

    public string TypeText => SshText.Algo(Algo, Bits);
}

public sealed record SshConfigHost(
    IReadOnlyList<string> Aliases, string HostName, string User, string Port, IReadOnlyList<string> IdentityFiles)
{
    public static SshConfigHost FromJson(JsonElement e) => new(
        JsonRead.StrArr(e, "aliases"), JsonRead.Str(e, "host_name"), JsonRead.Str(e, "user"),
        JsonRead.Str(e, "port"), JsonRead.StrArr(e, "identity_files"));
}

public sealed record SshScan(string Dir, IReadOnlyList<SshScanItem> Items, int Skipped, bool VaultUnlocked)
{
    public IReadOnlyList<SshConfigHost> ConfigHosts { get; init; } = Array.Empty<SshConfigHost>();
    public bool ConfigPartial { get; init; }

    public static SshScan FromJson(JsonElement r) => new(
        JsonRead.Str(r, "dir"), JsonRead.Arr(r, "items").Select(SshScanItem.FromJson).ToList(),
        (int)JsonRead.Long(r, "skipped"), JsonRead.Bool(r, "vault_unlocked"))
    {
        ConfigHosts = JsonRead.Arr(r, "config_hosts").Select(SshConfigHost.FromJson).ToList(),
        ConfigPartial = JsonRead.Bool(r, "config_partial"),
    };
}

/// <summary>ssh.status.</summary>
public sealed record SshStatus(
    bool Supported, bool Enabled, string State, string Pipe, string Error, int KeysLoaded, string KeysState,
    long ExpiresAt, bool VaultUnlocked, int TtlSeconds, bool AllowForwarding, int Connections)
{
    public static readonly SshStatus Unknown = new(true, false, "stopped", "", "", 0, "empty", 0, false, 14400, false, 0);

    public static SshStatus FromJson(JsonElement r) => new(
        JsonRead.Bool(r, "supported", true), JsonRead.Bool(r, "enabled"), JsonRead.Str(r, "state", "stopped"), JsonRead.Str(r, "pipe"),
        JsonRead.Str(r, "error"), (int)JsonRead.Long(r, "keys_loaded"), JsonRead.Str(r, "keys_state", "empty"),
        JsonRead.Long(r, "expires_at"), JsonRead.Bool(r, "vault_unlocked"), (int)JsonRead.Long(r, "ttl_s", 14400),
        JsonRead.Bool(r, "allow_forwarding"), (int)JsonRead.Long(r, "connections"));

    public bool Listening => State == "listening";
    public bool InGrace => KeysState == "grace";
}

public sealed record SshProc(uint Pid, string Name, string Path);

/// <summary>One signing request (ssh.recent item, ssh.used / ssh.refused event).</summary>
public sealed record SshUse(
    long At, string Fingerprint, string Title, string EntryId, IReadOnlyList<SshProc> Client, string Host, string HostKeyFp,
    string User, string Kind, string Namespace, bool Forwarded, string Result, string Reason)
{
    public bool Signed => Result == "signed";

    public static SshUse FromJson(JsonElement e) => new(
        JsonRead.Long(e, "at"), JsonRead.Str(e, "fingerprint"), JsonRead.Str(e, "title"), JsonRead.Str(e, "entry_id"),
        SshText.Procs(e), JsonRead.Str(e, "host"), JsonRead.Str(e, "host_key_fp"), JsonRead.Str(e, "user"),
        JsonRead.Str(e, "kind"), JsonRead.Str(e, "namespace"), JsonRead.Bool(e, "forwarded"), JsonRead.Str(e, "result"),
        JsonRead.Str(e, "reason"));

    /// <summary>The program behind the request: the outermost process that is not ssh itself, e.g. git.exe.</summary>
    public string ProgramName => SshText.Program(Client);
}

public sealed record SshSelfTest(bool Ok, int Listed, IReadOnlyList<string> Fingerprints, string Error)
{
    public static SshSelfTest FromJson(JsonElement r) =>
        new(JsonRead.Bool(r, "ok"), (int)JsonRead.Long(r, "listed"), JsonRead.StrArr(r, "fingerprints"), JsonRead.Str(r, "error"));
}

/// <summary>Display helpers shared by the shells.</summary>
public static class SshText
{
    public static string Algo(string algo, int bits) => algo switch
    {
        "ssh-ed25519" => "Ed25519",
        "ssh-rsa" => bits > 0 ? $"RSA {bits}" : "RSA",
        "ecdsa-sha2-nistp256" => "ECDSA P-256",
        "ecdsa-sha2-nistp384" => "ECDSA P-384",
        "ecdsa-sha2-nistp521" => "ECDSA P-521",
        "" => "",
        _ => algo,
    };

    /// <summary>"SHA256:abcd…wxyz" shortened for narrow places.</summary>
    public static string ShortFingerprint(string fp) =>
        fp.Length > 22 ? fp[..15] + "…" + fp[^6..] : fp;

    internal static IReadOnlyList<SshProc> Procs(JsonElement e) =>
        JsonRead.Arr(e, "client").Select(p => new SshProc((uint)JsonRead.Long(p, "pid"), JsonRead.Str(p, "name"), JsonRead.Str(p, "path"))).ToList();

    private static readonly HashSet<string> SshClients = new(StringComparer.OrdinalIgnoreCase)
    {
        "ssh.exe", "ssh", "ssh-add.exe", "ssh-keygen.exe", "scp.exe", "sftp.exe",
    };

    private static readonly HashSet<string> Shells = new(StringComparer.OrdinalIgnoreCase)
    {
        "cmd.exe", "powershell.exe", "pwsh.exe", "bash.exe", "sh.exe", "conhost.exe", "WindowsTerminal.exe", "OpenConsole.exe", "wsl.exe",
    };

    /// <summary>
    /// The interesting program in a client chain (the client first, then its parents): the first one
    /// that is neither an ssh tool nor a shell, else the first process.
    /// </summary>
    public static string Program(IReadOnlyList<SshProc> chain)
    {
        foreach (var p in chain)
        {
            if (p.Name.Length > 0 && !SshClients.Contains(p.Name) && !Shells.Contains(p.Name)) return Strip(p.Name);
        }
        return chain.Count > 0 && chain[0].Name.Length > 0 ? Strip(chain[0].Name) : "";
    }

    private static string Strip(string name) =>
        name.EndsWith(".exe", StringComparison.OrdinalIgnoreCase) ? name[..^4] : name;

    /// <summary>The problem of a stored key in words, or "" when it is usable.</summary>
    public static string Problem(string problem, string reason) => problem switch
    {
        "" or "missing_settings" => "",
        "needs_passphrase" => Strings.SshProblemPassphrase,
        "external_file" => Strings.SshProblemExternal,
        "missing_key" => Strings.SshProblemMissing,
        "too_large" => Strings.SshProblemTooLarge,
        "unsupported" => Strings.SshUnsupported(reason),
        _ => Strings.SshProblemCorrupt,
    };
}

/// <summary>Host binding (ssh.set_hosts): which servers a key is offered to.</summary>
public static class SshHosts
{
    public const int Max = 32;

    /// <summary>Splits what the user typed (one per line, or separated by commas / spaces), trimmed and without duplicates.</summary>
    public static IReadOnlyList<string> Parse(string? text)
    {
        var out_ = new List<string>();
        foreach (var raw in (text ?? "").Split(new[] { '\n', '\r', ',', ' ', '\t', '，', '、' }, StringSplitOptions.RemoveEmptyEntries))
        {
            var h = raw.Trim();
            if (h.Length > 0 && !out_.Contains(h, StringComparer.Ordinal)) out_.Add(h);
        }
        return out_;
    }

    /// <summary>The same rule as the core (sshagent.ValidHostPattern): a host name or address with * ? wildcards, "[host]:port", or "SHA256:" plus 43 base64 characters.</summary>
    public static bool Valid(string p)
    {
        if (p.Length == 0 || p.Length > 255) return false;
        if (p.StartsWith("SHA256:", StringComparison.Ordinal))
        {
            var b64 = p["SHA256:".Length..];
            if (b64.Length != 43) return false;
            try { return Convert.FromBase64String(b64 + "=").Length == 32; }
            catch (FormatException) { return false; }
        }
        foreach (var c in p)
        {
            if (char.IsAsciiLetterOrDigit(c) || ".-_*?:[]".Contains(c)) continue;
            return false;
        }
        return true;
    }

    /// <summary>The first pattern that is not valid, or null.</summary>
    public static string? FirstInvalid(IEnumerable<string> patterns) => patterns.FirstOrDefault(p => !Valid(p));

    /// <summary>
    /// Servers this key recently logged in to (newest first): the host name when known_hosts names it, otherwise the
    /// host key fingerprint, so the suggestion always works.
    /// </summary>
    public static IReadOnlyList<string> Suggestions(IEnumerable<SshUse> recent, string fingerprint)
    {
        var out_ = new List<string>();
        foreach (var u in recent)
        {
            if (u.Fingerprint != fingerprint || !u.Signed || u.Kind != "userauth") continue;
            var h = u.Host.Length > 0 ? u.Host : u.HostKeyFp;
            if (h.Length > 0 && !out_.Contains(h, StringComparer.OrdinalIgnoreCase)) out_.Add(h);
        }
        return out_;
    }

    /// <summary>How a pattern is shown: fingerprints shortened.</summary>
    public static string Display(string p) => p.StartsWith("SHA256:", StringComparison.Ordinal) ? SshText.ShortFingerprint(p) : p;
}
