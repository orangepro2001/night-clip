using System.Text.Json;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

internal static class VJ
{
    public static JsonElement Parse(string json)
    {
        using var doc = JsonDocument.Parse(json);
        return doc.RootElement.Clone();
    }
}

/// <summary>An <see cref="ICoreCaller"/> that records calls and answers from a table.</summary>
internal sealed class FakeCore : ICoreCaller
{
    public readonly List<(string Method, JsonElement Params)> Calls = new();
    public readonly Dictionary<string, string> Replies = new();
    public Exception? Throw;

    public JsonElement Call(string method, object? parameters = null)
    {
        var p = JsonDocument.Parse(JsonSerializer.Serialize(parameters ?? new { })).RootElement.Clone();
        Calls.Add((method, p));
        if (Throw is not null) throw Throw;
        return VJ.Parse(Replies.TryGetValue(method, out var r) ? r : "{}");
    }

    public (string Method, JsonElement Params) Last => Calls[^1];
}

public class VaultModelTests
{
    [Fact]
    public void Status_ParsesAndDefaults()
    {
        var s = VaultStatus.FromJson(VJ.Parse("""
            {"exists":true,"unlocked":false,"autolock_s":120,"clip_clear_s":0,"retry_after_s":7,"entries":3,"backups":2,
             "last_manual_backup_at":1700000000,"key_file":true,"kdf":{"memory_mib":64,"iterations":3,"parallelism":2}}
            """));
        Assert.True(s.Exists);
        Assert.False(s.Unlocked);
        Assert.Equal(120, s.AutolockS);
        Assert.Equal(0, s.ClipClearS);
        Assert.Equal(7, s.RetryAfterS);
        Assert.Equal(3, s.Entries);
        Assert.Equal(1700000000, s.LastManualBackupAt);
        Assert.Contains("64 MiB", s.Kdf);
        Assert.True(s.KeyFile);
        // The first cores wrote the Go field names: still understood.
        Assert.Contains("32 MiB", VaultStatus.FromJson(VJ.Parse("""{"kdf":{"MemoryMiB":32,"Iterations":3}}""")).Kdf);

        var empty = VaultStatus.FromJson(VJ.Parse("{}"));
        Assert.False(empty.Exists);
        Assert.Equal(300, empty.AutolockS);
        Assert.Equal(20, empty.ClipClearS);
        Assert.Equal("", empty.Kdf);
    }

    [Fact]
    public void EntryList_ParsesSummaries()
    {
        var list = EntrySummary.ListFromJson(VJ.Parse("""
            {"entries":[{"id":"a","group_id":"g","kind":"apikey","title":"Stripe","username":"","url":"https://stripe.com",
              "tags":["x","y"],"has_totp":false,"has_attachments":true,"modified_at":5,"expires_at":9,"in_trash":false}]}
            """));
        var e = Assert.Single(list);
        Assert.Equal("apikey", e.Kind);
        Assert.Equal(new[] { "x", "y" }, e.Tags);
        Assert.True(e.HasAttachments);
        Assert.Equal(9, e.ExpiresAt);
    }

    [Fact]
    public void EntryDetail_ParsesFieldsStrengthAndAttachments()
    {
        var d = EntryDetail.FromJson(VJ.Parse("""
            {"id":"i1","kind":"login","title":"T","username":"u","url":"","notes":"n","tags":[],"has_password":true,"has_totp":true,
             "fields":[{"name":"Key ID","value":"K","protected":false,"has_value":true},{"name":"Secret","value":"","protected":true,"has_value":true}],
             "attachments":[{"name":"a.txt","size":12}],"history":2,"created_at":10,"modified_at":20,
             "password_strength":{"bits":55.5,"label":"good"}}
            """));
        Assert.Equal("i1", d.Id);
        Assert.Equal("n", d.Notes);
        Assert.Equal(2, d.Fields.Count);
        Assert.True(d.Fields[1].Protected);
        Assert.Equal("", d.Fields[1].Value);
        Assert.Equal("a.txt", Assert.Single(d.Attachments).Name);
        Assert.Equal(2, d.History);
        Assert.Equal("good", d.Strength.Label);
        Assert.Equal(55.5, d.Strength.Bits);
        Assert.True(d.Summary.HasTotp);
    }

    [Fact]
    public void Groups_Versions_Backups_Check_Health_Preview_Stats()
    {
        var g = GroupInfo.ListFromJson(VJ.Parse("""{"groups":[{"id":"r","parent_id":"","name":"Root","path":"Root","entries":1,"is_trash":false}]}"""));
        Assert.Equal("Root", Assert.Single(g).Name);

        var v = VersionInfo.ListFromJson(VJ.Parse("""{"versions":[{"index":0,"modified_at":3,"title":"old","username":"u","url":"x"}]}"""));
        Assert.Equal("old", Assert.Single(v).Title);

        var b = BackupInfo.ListFromJson(VJ.Parse("""{"backups":[{"name":"b1","time":9,"size":2048,"reason":"manual"}]}"""));
        Assert.Equal("manual", Assert.Single(b).Reason);

        var c = BackupCheck.FromJson(VJ.Parse("""{"entries":4,"groups":2,"in_trash":1,"modified_at":77}"""));
        Assert.Equal((4, 2, 1, 77L), (c.Entries, c.Groups, c.InTrash, c.ModifiedAt));

        var h = HealthReport.FromJson(VJ.Parse("""
            {"weak":[{"id":"a","title":"A","info":"weak"}],"reused":[{"id":"b","title":"B","info":"2"}],"old":[],"expired":[],"without_totp":3,"total":9}
            """));
        Assert.Equal(2, h.IssueCount);
        Assert.Equal(3, h.WithoutTotp);

        var p = RestorePreview.FromJson(VJ.Parse("""
            {"only_in_backup":[{"id":"a","title":"A","modified_at":1}],"only_in_current":[],"changed":[{"id":"c","title":"C","modified_at":2}],"same":5}
            """));
        Assert.Single(p.OnlyInBackup);
        Assert.Single(p.Changed);
        Assert.Equal(5, p.Same);

        var s = MergeStats.FromJson(VJ.Parse("""{"stats":{"entries_added":1,"entries_updated":2,"entries_removed":3,"entries_moved":4,"groups_added":5,"groups_removed":6}}"""));
        Assert.Equal(new[] { 1, 2, 3, 4, 5, 6 }, new[] { s.EntriesAdded, s.EntriesUpdated, s.EntriesRemoved, s.EntriesMoved, s.GroupsAdded, s.GroupsRemoved });
    }

    [Fact]
    public void Parsers_AreTolerantOfGarbage()
    {
        Assert.Empty(EntrySummary.ListFromJson(VJ.Parse("""{"entries":5}""")));
        Assert.Equal("", EntryDetail.FromJson(VJ.Parse("[]")).Id);
        Assert.Equal("very_weak", PasswordStrength.FromJson(default).Label);
    }
}

public class VaultApiParamsTests
{
    private static (VaultApi Api, FakeCore Core) Make()
    {
        var core = new FakeCore();
        return (new VaultApi(core), core);
    }

    [Fact]
    public void Unlock_SendsPasswordAndKeyFileBase64()
    {
        var (api, core) = Make();
        core.Replies["vault.unlock"] = """{"unlocked":true,"unlock_ms":1234}""";
        var ms = api.Unlock(new VaultCreds("pw", new byte[] { 1, 2, 3 }));
        Assert.Equal(1234, ms);
        Assert.Equal("vault.unlock", core.Last.Method);
        Assert.Equal("pw", core.Last.Params.GetProperty("password").GetString());
        Assert.Equal(Convert.ToBase64String(new byte[] { 1, 2, 3 }), core.Last.Params.GetProperty("key_file_b64").GetString());
    }

    [Fact]
    public void Unlock_WithoutKeyFile_OmitsTheField()
    {
        var (api, core) = Make();
        api.Unlock(new VaultCreds("pw"));
        Assert.False(core.Last.Params.TryGetProperty("key_file_b64", out _));
    }

    [Fact]
    public void Create_SendsKdf_OnlyWhenChosen()
    {
        var (api, core) = Make();
        api.Create(new VaultCreds("pw"), "low");
        Assert.Equal("low", core.Last.Params.GetProperty("kdf").GetString());
        api.Create(new VaultCreds("pw"));
        Assert.False(core.Last.Params.TryGetProperty("kdf", out _));
    }

    [Fact]
    public void ChangeMaster_NestsTheNewCredentials()
    {
        var (api, core) = Make();
        api.ChangeMaster(new VaultCreds("old"), new VaultCreds("new", new byte[] { 9 }));
        var p = core.Last.Params;
        Assert.Equal("old", p.GetProperty("password").GetString());
        Assert.Equal("new", p.GetProperty("new").GetProperty("password").GetString());
        Assert.True(p.GetProperty("new").TryGetProperty("key_file_b64", out _));
        Assert.False(p.TryGetProperty("key_file_b64", out _));
    }

    [Fact]
    public void ExportFile_UsesConfirmNames_ForTheMasterAndPlainNames_ForTheFile()
    {
        var (api, core) = Make();
        api.ExportFile(@"C:\x\v.kdbx", new VaultCreds("master", new byte[] { 1 }), new VaultCreds("filepw"));
        var p = core.Last.Params;
        Assert.Equal("vault.export_file", core.Last.Method);
        Assert.Equal("master", p.GetProperty("confirm_password").GetString());
        Assert.True(p.TryGetProperty("confirm_key_file_b64", out _));
        Assert.Equal("filepw", p.GetProperty("password").GetString());
        Assert.False(p.TryGetProperty("key_file_b64", out _));
        Assert.Equal(@"C:\x\v.kdbx", p.GetProperty("path").GetString());
    }

    [Fact]
    public void ExportCsv_SendsOnlyTheConfirmation()
    {
        var (api, core) = Make();
        api.ExportCsv("p.csv", new VaultCreds("master"));
        Assert.Equal("vault.export_csv", core.Last.Method);
        Assert.Equal("master", core.Last.Params.GetProperty("confirm_password").GetString());
        Assert.False(core.Last.Params.TryGetProperty("password", out _));
    }

    [Fact]
    public void List_SendsOnlyTheFiltersThatAreSet()
    {
        var (api, core) = Make();
        api.List(new VaultQuery());
        Assert.Empty(core.Last.Params.EnumerateObject());
        api.List(new VaultQuery("bank", Kind: "login", OnlyTrash: true));
        var p = core.Last.Params;
        Assert.Equal("bank", p.GetProperty("query").GetString());
        Assert.Equal("login", p.GetProperty("kind").GetString());
        Assert.True(p.GetProperty("trash").GetBoolean());
        Assert.False(p.TryGetProperty("group_id", out _));
    }

    [Fact]
    public void Restore_SendsModeIdsAndOptionalCreds()
    {
        var (api, core) = Make();
        api.Restore("b1", "entries", new[] { "x", "y" }, null);
        var p = core.Last.Params;
        Assert.Equal("entries", p.GetProperty("mode").GetString());
        Assert.Equal(2, p.GetProperty("ids").GetArrayLength());
        Assert.False(p.TryGetProperty("password", out _));
        api.Restore("b1", "replace", null, new VaultCreds("old"));
        Assert.False(core.Last.Params.TryGetProperty("ids", out _));
        Assert.Equal("old", core.Last.Params.GetProperty("password").GetString());
    }

    [Fact]
    public void Add_SendsGroup_Update_DoesNot()
    {
        var (api, core) = Make();
        var d = new EntryDraft { Title = "T", GroupId = "g1" };
        api.Add(d);
        Assert.Equal("g1", core.Last.Params.GetProperty("entry").GetProperty("group_id").GetString());
        api.Update("id1", d);
        Assert.Equal("id1", core.Last.Params.GetProperty("id").GetString());
        Assert.False(core.Last.Params.GetProperty("entry").TryGetProperty("group_id", out _));
    }

    [Fact]
    public void Attachments_UsePathNotBytes()
    {
        var (api, core) = Make();
        api.AttachAdd("id", "a.txt", @"C:\a.txt");
        Assert.Equal(@"C:\a.txt", core.Last.Params.GetProperty("path").GetString());
        Assert.False(core.Last.Params.TryGetProperty("data_b64", out _));
        core.Replies["vault.attach_get"] = """{"size":42}""";
        Assert.Equal(42, api.AttachSave("id", "a.txt", @"C:\out.txt"));
        Assert.Equal(@"C:\out.txt", core.Last.Params.GetProperty("path").GetString());
    }

    [Fact]
    public void RevealAndCopy_ReturnValues()
    {
        var (api, core) = Make();
        core.Replies["vault.reveal"] = """{"value":"s3cret"}""";
        core.Replies["vault.copy"] = """{"value":"s3cret","clear_after_s":20}""";
        Assert.Equal("s3cret", api.Reveal("i", "password"));
        var c = api.Copy("i", "field:Key ID");
        Assert.Equal(20, c.ClearAfterSeconds);
        Assert.Equal("field:Key ID", core.Last.Params.GetProperty("field").GetString());
    }

    [Fact]
    public void CoreErrors_PropagateWithTheCode()
    {
        var (api, core) = Make();
        core.Throw = new CoreException("VAULT_LOCKED", "locked");
        var ex = Assert.Throws<CoreException>(() => api.List(new VaultQuery()));
        Assert.Equal("VAULT_LOCKED", ex.Code);
    }
}
