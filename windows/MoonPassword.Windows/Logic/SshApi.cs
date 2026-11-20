using MoonPassword.Windows.Interop;

namespace MoonPassword.Windows.Logic;

/// <summary>
/// Typed wrapper over the ssh.* methods (docs/vault-api.md §3.9). Every call blocks (importing an
/// encrypted key runs its key derivation): call it off the UI thread. Failures throw
/// <see cref="CoreException"/>. Nothing here returns private key material.
/// </summary>
public sealed class SshApi
{
    private readonly ICoreCaller _core;
    public SshApi(ICoreCaller core) => _core = core;

    public SshStatus Status() => SshStatus.FromJson(_core.Call("ssh.status"));

    public SshKeyList Keys() => SshKeyList.FromJson(_core.Call("ssh.keys"));

    public IReadOnlyList<SshUse> Recent(int limit = 50) =>
        JsonRead.Arr(_core.Call("ssh.recent", new { limit }), "uses").Select(SshUse.FromJson).ToList();

    public SshSelfTest SelfTest() => SshSelfTest.FromJson(_core.Call("ssh.selftest"));

    /// <summary>Looks at a key file (the core reads <paramref name="path"/>) or pasted text.</summary>
    public SshInspect Inspect(SshSource source, string passphrase = "") =>
        SshInspect.FromJson(_core.Call("ssh.inspect", source.ToParams(passphrase)));

    public SshStored Import(SshSource source, string passphrase, string title, string groupId = "", bool agent = true,
        string attachmentName = "", bool allowDuplicate = false)
    {
        var p = source.ToParams(passphrase);
        p["title"] = title;
        p["agent"] = agent;
        if (groupId.Length > 0) p["group_id"] = groupId;
        if (attachmentName.Length > 0) p["attachment_name"] = attachmentName;
        if (allowDuplicate) p["allow_duplicate"] = true;
        return SshStored.FromJson(_core.Call("ssh.import", p));
    }

    /// <summary><paramref name="type"/>: ed25519, rsa or ecdsa; <paramref name="bits"/> 0 = the default.</summary>
    public SshStored Generate(string type, int bits, string title, string comment = "", string groupId = "", bool agent = true)
    {
        var p = new Dictionary<string, object?> { ["type"] = type, ["title"] = title, ["agent"] = agent };
        if (bits > 0) p["bits"] = bits;
        if (comment.Length > 0) p["comment"] = comment;
        if (groupId.Length > 0) p["group_id"] = groupId;
        return SshStored.FromJson(_core.Call("ssh.generate", p));
    }

    public string PublicKey(string id) => JsonRead.Str(_core.Call("ssh.public_key", new { id }), "public_key");

    public void SetAgent(string id, bool enabled) => _core.Call("ssh.set_agent", new { id, enabled });

    /// <summary>Limits the key to these servers (empty = any server). Returns what the core stored.</summary>
    public IReadOnlyList<string> SetHosts(string id, IReadOnlyList<string> hosts) =>
        JsonRead.StrArr(_core.Call("ssh.set_hosts", new { id, hosts }), "hosts");

    public string Normalize(string id, string passphrase = "") =>
        JsonRead.Str(_core.Call("ssh.normalize", new { id, passphrase }), "fingerprint");

    /// <summary>Writes the private key to <paramref name="path"/> (never overwrites). Asks the core to check the master password.</summary>
    public void Export(string id, string format, string passphrase, string path, VaultCreds master)
    {
        var p = new Dictionary<string, object?> { ["id"] = id, ["format"] = format, ["path"] = path };
        if (passphrase.Length > 0) p["passphrase"] = passphrase;
        master.AddTo(p);
        _core.Call("ssh.export", p);
    }

    /// <summary>Lists private keys and configured hosts in <paramref name="dir"/> (empty = ~/.ssh).</summary>
    public SshScan Scan(string dir = "") =>
        SshScan.FromJson(dir.Length > 0 ? _core.Call("ssh.scan", new { dir }) : _core.Call("ssh.scan"));

    public SshConfigTargets ConfigTargets(string path = "") =>
        SshConfigTargets.FromJson(_core.Call("ssh.config_targets", new { path }));

    /// <summary>Writes the public half next to a key file. Returns created, same or mismatch; never overwrites.</summary>
    public string WritePub(string path, string id) => JsonRead.Str(_core.Call("ssh.write_pub", new { path, id }), "state");
}

/// <summary>Where a key comes from: a file the core reads, or text the user pasted.</summary>
public sealed record SshSource(string Path = "", string Text = "")
{
    public static SshSource File(string path) => new(Path: path);
    public static SshSource Pasted(string text) => new(Text: text);

    public Dictionary<string, object?> ToParams(string passphrase)
    {
        var p = new Dictionary<string, object?>();
        if (Path.Length > 0) p["path"] = Path;
        else p["data_b64"] = Convert.ToBase64String(System.Text.Encoding.UTF8.GetBytes(Text));
        if (passphrase.Length > 0) p["passphrase"] = passphrase;
        return p;
    }
}
