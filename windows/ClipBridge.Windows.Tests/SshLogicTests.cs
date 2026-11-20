using System.Text.Json;
using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class SshModelTests
{
    private const string KeyJson = """
        {"entry_id":"e1","title":"GitHub","group_path":"Work","key_attachment":"id_ed25519","algo":"ssh-ed25519","bits":256,
         "fingerprint":"SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG","comment":"me@pc","public_key":"ssh-ed25519 AAAA me@pc",
         "agent":true,"loaded":true,"source_format":"ppk3","encrypted":false,"problem":"","last_used_at":1700000000}
        """;

    [Fact]
    public void KeyInfo_Parses_AndDescribesTheType()
    {
        var k = SshKeyInfo.FromJson(VJ.Parse(KeyJson));
        Assert.Equal("GitHub", k.Title);
        Assert.Equal("Ed25519", k.TypeText);
        Assert.True(k.Agent && k.Loaded && k.Usable);
        Assert.Equal(1700000000, k.LastUsedAt);
        Assert.Equal("PuTTY PPK v3", Strings.SshFormatName(k.SourceFormat));
    }

    [Theory]
    [InlineData("ssh-rsa", 3072, "RSA 3072")]
    [InlineData("ecdsa-sha2-nistp384", 384, "ECDSA P-384")]
    [InlineData("ssh-ed25519", 256, "Ed25519")]
    [InlineData("", 0, "")]
    [InlineData("sk-ssh-ed25519@openssh.com", 0, "sk-ssh-ed25519@openssh.com")]
    public void AlgoText(string algo, int bits, string text) => Assert.Equal(text, SshText.Algo(algo, bits));

    [Fact]
    public void Problems_HaveText_AndUsableOnlyWithoutOne()
    {
        Assert.Equal("", SshText.Problem("", ""));
        Assert.Equal("", SshText.Problem("missing_settings", ""));
        Assert.Contains("口令", SshText.Problem("needs_passphrase", ""));
        Assert.Contains("DSA", SshText.Problem("unsupported", "dsa"));
        Assert.Contains("损坏", SshText.Problem("corrupt", ""));
        var bad = SshKeyInfo.FromJson(VJ.Parse("""{"problem":"needs_passphrase"}"""));
        Assert.False(bad.Usable);
        Assert.True(SshKeyInfo.FromJson(VJ.Parse("""{"problem":"missing_settings"}""")).Usable);
    }

    [Fact]
    public void EntryDetail_CarriesTheSshBlock_AndHidesKeyAttachments()
    {
        var d = EntryDetail.FromJson(VJ.Parse("""
            {"id":"e1","kind":"ssh","title":"GitHub","attachments":[{"name":"id_ed25519","size":400},{"name":"KeeAgent.settings","size":900},{"name":"readme.txt","size":5}],
             "ssh":{"entry_id":"e1","key_attachment":"id_ed25519","algo":"ssh-ed25519"}}
            """));
        Assert.NotNull(d.Ssh);
        Assert.Equal(3, d.Attachments.Count);
        Assert.Equal("readme.txt", Assert.Single(d.OtherAttachments).Name);

        var plain = EntryDetail.FromJson(VJ.Parse("""{"id":"x","kind":"login","attachments":[{"name":"KeeAgent.settings","size":1}]}"""));
        Assert.Null(plain.Ssh);
        Assert.Single(plain.OtherAttachments); // only SSH entries hide it
    }

    [Fact]
    public void ScanAndInspectAndStatus_Parse()
    {
        var scan = SshScan.FromJson(VJ.Parse("""
            {"dir":"C:\\Users\\u\\.ssh","skipped":2,"vault_unlocked":true,"items":[{"path":"C:\\Users\\u\\.ssh\\id_rsa","format":"openssh","encrypted":true,
             "algo":"ssh-rsa","bits":3072,"fingerprint":"SHA256:x","pub_path":"C:\\Users\\u\\.ssh\\id_rsa.pub","pub_state":"ok",
             "config_hosts":["github.com"],"referenced_by":[{"file":"C:\\Users\\u\\.ssh\\mount.ps1","line":3}],"problem":"",
             "in_vault":[{"entry_id":"e9","title":"Old"}]}]}
            """));
        var it = Assert.Single(scan.Items);
        Assert.Equal("id_rsa", it.Name);
        Assert.True(it.InVaultAlready);
        Assert.Equal(3, Assert.Single(it.ReferencedBy).Line);
        Assert.Equal("RSA 3072", it.TypeText);

        var ins = SshInspect.FromJson(VJ.Parse("""{"format":"pkcs8-enc","encrypted":true,"cipher":"aes-256-cbc","kdf":"scrypt","needs_passphrase":true,"wrong_passphrase":false}"""));
        Assert.True(ins.NeedsPassphrase);
        Assert.Empty(ins.InVault);

        var st = SshStatus.FromJson(VJ.Parse("""{"supported":true,"enabled":true,"state":"listening","pipe":"\\\\.\\pipe\\openssh-ssh-agent","keys_loaded":2,"keys_state":"grace","expires_at":5,"ttl_s":3600}"""));
        Assert.True(st.Listening && st.InGrace);
        Assert.Equal(3600, st.TtlSeconds);
        Assert.Equal(14400, SshStatus.FromJson(VJ.Parse("{}")).TtlSeconds);
    }

    [Theory]
    [InlineData(new[] { "ssh.exe", "git.exe", "pwsh.exe" }, "git")]
    [InlineData(new[] { "ssh.exe", "pwsh.exe", "WindowsTerminal.exe" }, "ssh")]
    [InlineData(new[] { "ssh.exe", "Code.exe" }, "Code")]
    [InlineData(new string[0], "")]
    public void ProgramName_SkipsSshToolsAndShells(string[] chain, string expected) =>
        Assert.Equal(expected, SshText.Program(chain.Select((n, i) => new SshProc((uint)i + 1, n, "")).ToList()));
}

public class SshEventTests
{
    [Fact]
    public void UsedAndRefused_ParseIntoOneType()
    {
        var used = Assert.IsType<SshUsedEvent>(CoreEventParser.Parse("""
            {"type":"ssh.used","at":10,"fingerprint":"SHA256:f","title":"GitHub","client":[{"pid":7,"name":"ssh.exe"},{"pid":6,"name":"git.exe"}],
             "host":"github.com","user":"git","kind":"userauth","result":"signed","suppressed":3}
            """));
        Assert.True(used.Use.Signed);
        Assert.Equal("git", used.Use.ProgramName);
        Assert.Equal(3, used.Suppressed);
        Assert.Null(used.Id); // nothing about the key goes into the log line
        var refused = Assert.IsType<SshUsedEvent>(CoreEventParser.Parse("""{"type":"ssh.refused","result":"refused","reason":"forwarding","forwarded":true}"""));
        Assert.False(refused.Use.Signed);
        Assert.Equal("ssh.refused", refused.Type);
    }

    [Fact]
    public void KeysAgentAndNeeded_Parse()
    {
        var k = Assert.IsType<SshKeysEvent>(CoreEventParser.Parse("""{"type":"ssh.keys","state":"grace","count":2,"keys_state":"grace","reason":"idle","expires_at":99}"""));
        Assert.Equal(("grace", 2, 99L), (k.State, k.Count, k.ExpiresAt));
        var a = Assert.IsType<SshAgentEvent>(CoreEventParser.Parse("""{"type":"ssh.agent","state":"conflict","pipe":"p","error":"taken"}"""));
        Assert.Equal("conflict", a.State);
        var n = Assert.IsType<SshKeysNeededEvent>(CoreEventParser.Parse("""{"type":"ssh.keys_needed","client":[{"pid":1,"name":"ssh.exe"}]}"""));
        Assert.Single(n.Client);
    }
}

public class SshNotifierTests
{
    private static SshUse Use(string fp = "SHA256:a", string program = "git.exe", string result = "signed", string reason = "") =>
        new(0, fp, "GitHub", "e", new[] { new SshProc(1, "ssh.exe", ""), new SshProc(2, program, "") }, "github.com", "", "git", "userauth", "", false, result, reason);

    [Fact]
    public void FirstMode_OncePerKeyAndProgram_UntilQuiet()
    {
        var n = new SshNotifier();
        var t = DateTimeOffset.UnixEpoch;
        var b = n.OnUse(Use(), t);
        Assert.NotNull(b);
        Assert.Equal("git 用了 SSH 密钥", b!.Value.Title);
        Assert.Equal("「GitHub」：登录 git@github.com", b.Value.Text);
        Assert.Null(n.OnUse(Use(), t.AddMinutes(5)));
        Assert.Null(n.OnUse(Use(), t.AddMinutes(30))); // still busy: the quiet period restarts with every use
        Assert.NotNull(n.OnUse(Use(), t.AddMinutes(61)));
        Assert.NotNull(n.OnUse(Use(program: "Code.exe"), t.AddMinutes(62)));
        Assert.NotNull(n.OnUse(Use(fp: "SHA256:b"), t.AddMinutes(62)));
    }

    [Fact]
    public void AllAndOffModes()
    {
        var t = DateTimeOffset.UnixEpoch;
        var all = new SshNotifier { Mode = "all" };
        Assert.NotNull(all.OnUse(Use(), t));
        Assert.NotNull(all.OnUse(Use(), t.AddSeconds(1)));
        var off = new SshNotifier { Mode = "off" };
        Assert.Null(off.OnUse(Use(), t));
        Assert.Null(off.OnUse(Use(result: "refused", reason: "forwarding"), t));
        Assert.Null(off.OnKeysNeeded(Array.Empty<SshProc>(), t));
    }

    [Fact]
    public void RefusalsAndKeysNeeded_AreRateLimited()
    {
        var n = new SshNotifier();
        var t = DateTimeOffset.UnixEpoch;
        var r = n.OnUse(Use(result: "refused", reason: "forwarding"), t);
        Assert.Equal(Strings.SshRefusedTitle, r!.Value.Title);
        Assert.Null(n.OnUse(Use(result: "refused", reason: "no_keys"), t.AddSeconds(30)));
        Assert.NotNull(n.OnUse(Use(result: "refused", reason: "no_keys"), t.AddMinutes(2)));

        var k = n.OnKeysNeeded(new[] { new SshProc(1, "ssh.exe", ""), new SshProc(2, "git.exe", "") }, t);
        Assert.StartsWith("git 想用", k!.Value.Text);
        Assert.Null(n.OnKeysNeeded(Array.Empty<SshProc>(), t.AddMinutes(4)));
        Assert.NotNull(n.OnKeysNeeded(Array.Empty<SshProc>(), t.AddMinutes(6)));
    }

    [Theory]
    [InlineData("sshsig", "git", "", "签名 Git 提交")]
    [InlineData("sshsig", "file", "", "签名（file）")]
    [InlineData("userauth", "", "", "以 git 登录服务器")]
    [InlineData("other", "", "", "签名")]
    public void UsedText_DescribesWhatWasSigned(string kind, string ns, string host, string what)
    {
        var u = new SshUse(0, "SHA256:a", "K", "", Array.Empty<SshProc>(), host, "", "git", kind, ns, false, "signed", "");
        Assert.Equal("「K」：" + what, Strings.SshUsedText(u));
    }
}

public class SshMigrationTests
{
    private static SshScanItem Item(string fp = "SHA256:k", string problem = "", bool inVault = false, int refs = 0, string pub = "ok") => new(
        @"C:\Users\u\.ssh\id_rsa", "openssh", false, "ssh-rsa", 3072, fp, "", @"C:\Users\u\.ssh\id_rsa.pub", pub,
        Array.Empty<string>(), Enumerable.Range(1, refs).Select(i => new SshRef(@"C:\Users\u\.ssh\mount-ai-z.ps1", i)).ToList(), problem,
        inVault ? new[] { ("e", "Old") } : Array.Empty<(string, string)>());

    [Fact]
    public void Selection_Defaults()
    {
        Assert.True(SshMigration.SelectByDefault(Item()));
        Assert.False(SshMigration.SelectByDefault(Item(inVault: true)));
        Assert.False(SshMigration.SelectByDefault(Item(problem: "dsa")));
        Assert.False(SshMigration.CanImport(Item(problem: "corrupt")));
    }

    [Fact]
    public void Recycle_OnlyWhenEveryCheckPasses()
    {
        var agent = new HashSet<string> { "SHA256:k" };
        Assert.True(SshMigration.CanRecycle(Item(), "SHA256:k", "created", agent).Allowed);
        Assert.True(SshMigration.CanRecycle(Item(), "SHA256:k", "same", agent).Allowed);

        var referenced = SshMigration.CanRecycle(Item(refs: 2), "SHA256:k", "same", agent);
        Assert.False(referenced.Allowed);
        Assert.Contains("mount-ai-z.ps1", Assert.Single(referenced.Blockers)); // a referenced file is never offered

        Assert.False(SshMigration.CanRecycle(Item(), "SHA256:other", "same", agent).Allowed);
        Assert.False(SshMigration.CanRecycle(Item(), "SHA256:k", "mismatch", agent).Allowed);
        Assert.False(SshMigration.CanRecycle(Item(), "SHA256:k", "", agent).Allowed);
        Assert.False(SshMigration.CanRecycle(Item(), "SHA256:k", "same", new HashSet<string>()).Allowed);
        Assert.False(SshMigration.CanRecycle(Item(), "", "same", agent).Allowed);
        Assert.Equal(4, SshMigration.CanRecycle(Item(refs: 1), "SHA256:x", "mismatch", new HashSet<string>()).Blockers.Count);
    }

    [Fact]
    public void Notes_NameTheScriptsAndHosts()
    {
        var item = Item(refs: 1) with { ConfigHosts = new[] { "egg", "KOEI" }, PubState = "mismatch" };
        var notes = SshMigration.Notes(item);
        Assert.Contains(notes, n => n.Contains("egg、KOEI"));
        Assert.Contains(notes, n => n.Contains("mount-ai-z.ps1 第 1 行"));
        Assert.Contains(Strings.SshPubMismatch, notes);
        Assert.Contains(SshMigration.Notes(Item(problem: "dsa")), n => n.Contains("DSA"));
    }
}

public class SshEnvTests
{
    private static readonly SshStatus Listening = SshStatus.Unknown with { State = "listening", Pipe = @"\\.\pipe\openssh-ssh-agent", Enabled = true };

    [Theory]
    [InlineData("C:/Windows/System32/OpenSSH/ssh.exe", true)]
    [InlineData(@"C:\WINDOWS\System32\OpenSSH\ssh.exe", true)]
    [InlineData("\"C:/Windows/System32/OpenSSH/ssh.exe\"", true)]
    [InlineData("/usr/bin/ssh", false)]
    [InlineData("plink.exe", false)]
    [InlineData("", false)]
    [InlineData(null, false)]
    public void IsWindowsSsh(string? cmd, bool expected) => Assert.Equal(expected, SshEnv.IsWindowsSsh(cmd));

    [Fact]
    public void Git_WithoutSshCommand_GetsTheFix_OthersAreOnlyMentioned()
    {
        var unset = SshEnv.Check(new SshEnvFacts(true, null, true, "OpenSSH_for_Windows_9.5", "disabled", null, null), Listening);
        Assert.Equal(SshEnv.FixGitSsh, Assert.Single(unset).Fix);
        var ours = SshEnv.Check(new SshEnvFacts(true, "C:/Windows/System32/OpenSSH/ssh.exe", true, "", "disabled", null, null), Listening);
        Assert.Empty(ours);
        var foreign = Assert.Single(SshEnv.Check(new SshEnvFacts(true, "plink -batch", true, "", "disabled", null, null), Listening));
        Assert.Null(foreign.Fix); // the user's own value is never replaced
        Assert.False(foreign.IsWarning);
        Assert.Empty(SshEnv.Check(new SshEnvFacts(false, null, true, "", "disabled", null, null), Listening));
    }

    [Fact]
    public void Conflict_PointsAtTheServiceOrAnotherAgent()
    {
        var conflict = Listening with { State = "conflict" };
        Assert.Contains(SshEnv.Check(new SshEnvFacts(false, null, true, "", "running", null, null), conflict), i => i.Id == "service");
        Assert.Contains(SshEnv.Check(new SshEnvFacts(false, null, true, "", "disabled", null, null), conflict), i => i.Id == "pipe_taken");
    }

    [Fact]
    public void AuthSock_ElsewhereIsAWarning()
    {
        var other = SshEnv.Check(new SshEnvFacts(false, null, true, "", "disabled", null, @"\\.\pipe\1password"), Listening);
        Assert.Equal("auth_sock", Assert.Single(other).Id);
        Assert.Empty(SshEnv.Check(new SshEnvFacts(false, null, true, "", "disabled", @"\\.\pipe\openssh-ssh-agent", null), Listening));
        Assert.Contains(SshEnv.Check(new SshEnvFacts(false, null, false, "", "missing", null, null), Listening), i => i.Id == "no_openssh");
    }
}

public class SshApiTests
{
    [Fact]
    public void Import_SendsPathOrPastedText_NeverBoth()
    {
        var core = new ScriptedCore().On("ssh.import", """{"id":"n1","fingerprint":"SHA256:f","public_key":"ssh-ed25519 A","algo":"ssh-ed25519","bits":256,"attachment_name":"id_ed25519"}""");
        var api = new SshApi(core);
        var r = api.Import(SshSource.File(@"C:\k\id_ed25519"), "pp", "GitHub", agent: false);
        Assert.Equal("n1", r.Id);
        var p = core.ParamsOf("ssh.import").Single();
        Assert.Equal(@"C:\k\id_ed25519", JsonRead.Str(p, "path"));
        Assert.False(p.TryGetProperty("data_b64", out _));
        Assert.Equal("pp", JsonRead.Str(p, "passphrase"));
        Assert.False(JsonRead.Bool(p, "agent", true));

        api.Import(SshSource.Pasted("-----BEGIN X-----"), "", "T");
        var q = core.ParamsOf("ssh.import").Last();
        Assert.Equal("-----BEGIN X-----", System.Text.Encoding.UTF8.GetString(Convert.FromBase64String(JsonRead.Str(q, "data_b64"))));
        Assert.False(q.TryGetProperty("path", out _));
        Assert.False(q.TryGetProperty("passphrase", out _));
    }

    [Fact]
    public void Export_AddsTheMasterPassword_AndLockSendsAReason()
    {
        var core = new ScriptedCore().On("vault.lock", """{"was_unlocked":false}""");
        new SshApi(core).Export("e1", "openssh", "", @"C:\out\k", new VaultCreds("master pw"));
        var p = core.ParamsOf("ssh.export").Single();
        Assert.Equal("master pw", JsonRead.Str(p, "password"));
        Assert.False(p.TryGetProperty("passphrase", out _));

        Assert.False(new VaultApi(core).Lock("suspend"));
        Assert.Equal("suspend", JsonRead.Str(core.ParamsOf("vault.lock").Single(), "reason"));
    }

    [Fact]
    public void ErrorMessages_UseTheDetails()
    {
        Assert.Contains("DSA", ErrorMessages.ForSsh("SSH_KEY_UNSUPPORTED", "dsa"));
        Assert.Equal(Strings.ErrSshWrongPassphrase, ErrorMessages.ForSsh("SSH_KEY_PASSPHRASE", wrong: true));
        Assert.Equal(Strings.ErrSshNeedsPassphrase, ErrorMessages.ForSsh("SSH_KEY_PASSPHRASE"));
        Assert.Equal(Strings.ErrVaultLocked, ErrorMessages.ForSsh("VAULT_LOCKED"));
        Assert.Equal(Strings.ErrSshDuplicate, ErrorMessages.ForCode("SSH_KEY_DUPLICATE"));
    }

    [Fact]
    public void CoreException_KeepsDetails()
    {
        var ex = new CoreException("SSH_KEY_UNSUPPORTED", "x") { Details = VJ.Parse("""{"reason":"sk"}""") };
        Assert.Equal("sk", JsonRead.Str(ex.Details, "reason"));
        Assert.Equal("", JsonRead.Str(new CoreException("X", "y").Details, "reason"));
    }
}

public class SshKindTests
{
    [Theory]
    [InlineData("ssh", "ssh")]
    [InlineData("future-kind", "future-kind")]
    [InlineData("", "login")]
    [InlineData(null, "login")]
    [InlineData(" note ", "note")]
    public void Normalize_KeepsKindsItDoesNotKnow(string? kind, string expected) => Assert.Equal(expected, EntryKinds.Normalize(kind));

    [Fact]
    public void Ssh_IsKnownButNotCreatable_AndItsTemplateHidesLoginFields()
    {
        Assert.Contains(EntryKinds.Ssh, EntryKinds.Known);
        Assert.DoesNotContain(EntryKinds.Ssh, EntryKinds.All);
        Assert.False(EntryKinds.IsCreatable("ssh"));
        Assert.True(EntryKinds.IsCreatable("apikey"));
        var t = KindTemplate.For("ssh");
        Assert.Equal(Strings.KindSsh, t.Label);
        Assert.False(t.Username || t.Password || t.Url || t.Totp);
        Assert.True(t.Notes && t.Tags && t.CustomFields);
        Assert.Equal(EntryKinds.Login, KindTemplate.For("future-kind").Kind); // shown like a login, saved as itself
    }

    [Fact]
    public void Draft_OfAnSshEntry_SavesAsSsh()
    {
        var core = new ScriptedCore().On("vault.get", """{"id":"e1","kind":"ssh","title":"GitHub","fields":[],"attachments":[]}""");
        var draft = EntryDraftLoader.Load(new VaultApi(core), "e1");
        Assert.Equal("ssh", draft.Kind);
        new VaultApi(core).Update("e1", draft);
        var entry = core.ParamsOf("vault.update").Single().GetProperty("entry");
        Assert.Equal("ssh", JsonRead.Str(entry, "kind"));
    }

    [Fact]
    public void QuickFilter_Ssh_AsksForTheSshKind()
    {
        Assert.Equal("ssh", QuickFilters.ToQuery(QuickFilter.Ssh, "").Kind);
        Assert.Equal(Strings.FilterSsh, QuickFilters.Label(QuickFilter.Ssh));
    }

    [Fact]
    public void LockReasons()
    {
        Assert.Equal("suspend", LockPolicy.Reason(LockTrigger.Suspend));
        Assert.Equal("session", LockPolicy.Reason(LockTrigger.SessionLock));
        Assert.Equal("session", LockPolicy.Reason(LockTrigger.UserSwitch));
        Assert.Equal(Strings.LockedSession, VaultScreens.LockedNote("suspend"));
    }
}

public class SshPrefsAndDiagnosticsTests
{
    [Fact]
    public void Prefs_RoundTripSshNotify_AndGitMarker()
    {
        var dir = Path.Combine(Path.GetTempPath(), "cb-ssh-prefs-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(dir);
        try
        {
            var p = ShellPrefs.Load(dir);
            Assert.Equal("first", p.SshNotify);
            Assert.Null(p.GitSshCommandSet);
            p.SshNotify = "off";
            p.GitSshCommandSet = SshEnv.WindowsSshCommand;
            p.Save(dir);
            var back = ShellPrefs.Load(dir);
            Assert.Equal("off", back.SshNotify);
            Assert.Equal(SshEnv.WindowsSshCommand, back.GitSshCommandSet);
            Assert.Equal("first", ShellPrefs.NormalizeSshNotify("loud"));
        }
        finally { Directory.Delete(dir, true); }
    }

    [Fact]
    public void Diagnostics_ShowTheAgentState_WithoutKeyDetails()
    {
        var text = DiagnosticsText.Build(VJ.Parse("""
            {"core":"0.0.1","ssh":{"enabled":true,"state":"listening","pipe":"\\\\.\\pipe\\openssh-ssh-agent","keys_loaded":2,"keys_state":"loaded","error":"",
             "titles":["GitHub"],"fingerprints":["SHA256:secretish"]}}
            """), "1.0", DateTimeOffset.UnixEpoch);
        Assert.Contains("SSH 代理: 开，状态 listening", text);
        Assert.Contains("已加载 2 把", text);
        Assert.DoesNotContain("GitHub", text);
        Assert.DoesNotContain("SHA256", text);
    }
}
