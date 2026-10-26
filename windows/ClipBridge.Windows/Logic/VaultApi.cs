using System.Text.Json;
using ClipBridge.Windows.Interop;

namespace ClipBridge.Windows.Logic;

/// <summary>A master password plus an optional key file (its raw bytes, at most <see cref="KeyFileRules.MaxBytes"/>).</summary>
public sealed record VaultCreds(string Password, byte[]? KeyFile = null)
{
    /// <summary>Adds <c>password</c> and (when present) <c>key_file_b64</c> under the given names.</summary>
    public void AddTo(IDictionary<string, object?> p, string passwordKey = "password", string keyFileKey = "key_file_b64")
    {
        p[passwordKey] = Password;
        if (KeyFile is { Length: > 0 }) p[keyFileKey] = Convert.ToBase64String(KeyFile);
    }
}

/// <summary>What the list on the main page asks the core for.</summary>
public sealed record VaultQuery(string Query = "", string GroupId = "", string Kind = "", string Tag = "", bool OnlyTrash = false, bool IncludeTrash = false)
{
    public Dictionary<string, object?> ToParams()
    {
        var p = new Dictionary<string, object?>();
        if (Query.Length > 0) p["query"] = Query;
        if (GroupId.Length > 0) p["group_id"] = GroupId;
        if (Kind.Length > 0) p["kind"] = Kind;
        if (Tag.Length > 0) p["tag"] = Tag;
        if (OnlyTrash) p["trash"] = true;
        if (IncludeTrash) p["include_trash"] = true;
        return p;
    }
}

/// <summary>
/// Typed wrapper over the vault.* methods (docs/vault-api.md). Every method blocks (Argon2 can take
/// seconds): run it off the UI thread. Failures throw <see cref="CoreException"/> with the core's code.
/// Methods that return a secret (<see cref="Reveal"/>, <see cref="Copy"/>, <see cref="Generate"/>) say so;
/// callers drop the value as soon as they have used it.
/// </summary>
public sealed class VaultApi
{
    private readonly ICoreCaller _core;
    public VaultApi(ICoreCaller core) => _core = core;

    // ---- lifecycle ---------------------------------------------------------------------
    public VaultStatus Status() => VaultStatus.FromJson(_core.Call("vault.status"));

    /// <summary>Creates and unlocks. <paramref name="kdf"/>: low / standard / high (empty = core default).</summary>
    public long Create(VaultCreds creds, string kdf = "")
    {
        var p = new Dictionary<string, object?>();
        creds.AddTo(p);
        if (kdf.Length > 0) p["kdf"] = kdf;
        return JsonRead.Long(_core.Call("vault.create", p), "create_ms");
    }

    /// <summary>Returns the key-derivation time in milliseconds.</summary>
    public long Unlock(VaultCreds creds)
    {
        var p = new Dictionary<string, object?>();
        creds.AddTo(p);
        return JsonRead.Long(_core.Call("vault.unlock", p), "unlock_ms");
    }

    public bool Lock() => JsonRead.Bool(_core.Call("vault.lock"), "was_unlocked");

    public void VerifyMaster(VaultCreds creds)
    {
        var p = new Dictionary<string, object?>();
        creds.AddTo(p);
        _core.Call("vault.verify_master", p);
    }

    public void ChangeMaster(VaultCreds current, VaultCreds next, string kdf = "")
    {
        var p = new Dictionary<string, object?>();
        current.AddTo(p);
        var n = new Dictionary<string, object?>();
        next.AddTo(n);
        p["new"] = n;
        if (kdf.Length > 0) p["kdf"] = kdf;
        _core.Call("vault.change_master", p);
    }

    // ---- tools -------------------------------------------------------------------------
    public PasswordStrength Strength(string password) =>
        PasswordStrength.FromJson(_core.Call("vault.strength", new { password }));

    public GeneratedSecret Generate(GeneratorOptions options)
    {
        var r = _core.Call("vault.generate", options.ToParams());
        return new GeneratedSecret(JsonRead.Str(r, "value"), PasswordStrength.FromJson(JsonRead.Obj(r, "strength")));
    }

    public HealthReport Health() => HealthReport.FromJson(_core.Call("vault.health"));

    // ---- entries -----------------------------------------------------------------------
    public IReadOnlyList<EntrySummary> List(VaultQuery query) => EntrySummary.ListFromJson(_core.Call("vault.list", query.ToParams()));

    public EntryDetail Get(string id) => EntryDetail.FromJson(_core.Call("vault.get", new { id }));

    /// <summary>Returns one secret in plain text, for showing on screen only.</summary>
    public string Reveal(string id, string field) => JsonRead.Str(_core.Call("vault.reveal", new { id, field }), "value");

    /// <summary>Returns the secret for the clipboard (the core also marks it as "from the vault" so it is never broadcast).</summary>
    public CopyResult Copy(string id, string field)
    {
        var r = _core.Call("vault.copy", new { id, field });
        return new CopyResult(JsonRead.Str(r, "value"), (int)JsonRead.Long(r, "clear_after_s"));
    }

    public TotpCode Totp(string id)
    {
        var r = _core.Call("vault.totp", new { id });
        return new TotpCode(JsonRead.Str(r, "code"), (int)JsonRead.Long(r, "seconds_left"));
    }

    public string Add(EntryDraft draft) => JsonRead.Str(_core.Call("vault.add", new { entry = draft.ToParams(includeGroup: true) }), "id");

    public void Update(string id, EntryDraft draft) => _core.Call("vault.update", new { id, entry = draft.ToParams(includeGroup: false) });

    /// <summary>True when the entry was removed for good (it was already in the trash).</summary>
    public bool Delete(string id) => JsonRead.Bool(_core.Call("vault.delete", new { id }), "permanent");

    public void RestoreEntry(string id) => _core.Call("vault.restore_entry", new { id });
    public int EmptyTrash() => (int)JsonRead.Long(_core.Call("vault.empty_trash"), "removed");
    public void Move(string id, string groupId) => _core.Call("vault.move", new { id, group_id = groupId });
    public IReadOnlyList<VersionInfo> History(string id) => VersionInfo.ListFromJson(_core.Call("vault.history", new { id }));
    public void HistoryRestore(string id, int index) => _core.Call("vault.history_restore", new { id, index });

    // ---- groups and attachments --------------------------------------------------------
    public IReadOnlyList<GroupInfo> Groups() => GroupInfo.ListFromJson(_core.Call("vault.groups"));
    public string GroupAdd(string parentId, string name) => JsonRead.Str(_core.Call("vault.group_add", new { parent_id = parentId, name }), "id");
    public void GroupRename(string id, string name) => _core.Call("vault.group_rename", new { id, name });
    public void GroupDelete(string id) => _core.Call("vault.group_delete", new { id });

    /// <summary>The core reads <paramref name="path"/> itself (at most 5 MiB).</summary>
    public void AttachAdd(string id, string name, string path) => _core.Call("vault.attach_add", new { id, name, path });

    /// <summary>The core writes the attachment to <paramref name="path"/> and never overwrites an existing file.</summary>
    public long AttachSave(string id, string name, string path) =>
        JsonRead.Long(_core.Call("vault.attach_get", new { id, name, path }), "size");

    public void AttachRemove(string id, string name) => _core.Call("vault.attach_remove", new { id, name });

    // ---- backups -----------------------------------------------------------------------
    public IReadOnlyList<BackupInfo> BackupList() => BackupInfo.ListFromJson(_core.Call("vault.backup_list"));
    public BackupInfo BackupNow() => BackupInfo.FromJson(_core.Call("vault.backup_now"));

    public BackupCheck BackupVerify(string name, VaultCreds creds)
    {
        var p = new Dictionary<string, object?> { ["name"] = name };
        creds.AddTo(p);
        return BackupCheck.FromJson(_core.Call("vault.backup_verify", p));
    }

    /// <summary><paramref name="creds"/> null = the current credentials.</summary>
    public RestorePreview PreviewRestore(string name, VaultCreds? creds)
    {
        var p = new Dictionary<string, object?> { ["name"] = name };
        creds?.AddTo(p);
        return RestorePreview.FromJson(_core.Call("vault.restore_preview", p));
    }

    public MergeStats Restore(string name, string mode, IReadOnlyCollection<string>? ids, VaultCreds? creds)
    {
        var p = new Dictionary<string, object?> { ["name"] = name, ["mode"] = mode };
        if (ids is { Count: > 0 }) p["ids"] = ids;
        creds?.AddTo(p);
        return MergeStats.FromJson(_core.Call("vault.restore", p));
    }

    // ---- import / export ---------------------------------------------------------------
    public MergeStats ImportFile(string path, VaultCreds creds)
    {
        var p = new Dictionary<string, object?> { ["path"] = path };
        creds.AddTo(p);
        return MergeStats.FromJson(_core.Call("vault.import_file", p));
    }

    public (int Added, int Skipped) ImportCsv(string path)
    {
        var r = _core.Call("vault.import_csv", new { path });
        return ((int)JsonRead.Long(r, "added"), (int)JsonRead.Long(r, "skipped"));
    }

    /// <summary>A KDBX copy encrypted with <paramref name="fileCreds"/>; <paramref name="master"/> is the current master (asked again).</summary>
    public void ExportFile(string path, VaultCreds master, VaultCreds fileCreds, string kdf = "")
    {
        var p = new Dictionary<string, object?> { ["path"] = path };
        master.AddTo(p, "confirm_password", "confirm_key_file_b64");
        fileCreds.AddTo(p);
        if (kdf.Length > 0) p["kdf"] = kdf;
        _core.Call("vault.export_file", p);
    }

    /// <summary>Plain-text CSV. Callers must have shown the plain-text warning first.</summary>
    public void ExportCsv(string path, VaultCreds master)
    {
        var p = new Dictionary<string, object?> { ["path"] = path };
        master.AddTo(p, "confirm_password", "confirm_key_file_b64");
        _core.Call("vault.export_csv", p);
    }
}
