using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class CoreEventTests
{
    private static CoreEvent P(string json) => CoreEventParser.Parse(json);

    [Fact]
    public void CoreStarted_IsTyped()
    {
        var e = Assert.IsType<CoreStartedEvent>(P("{\"type\":\"core.started\"}"));
        Assert.Equal("core.started", e.Type);
        Assert.Null(e.Id);
    }

    [Fact]
    public void Vault_Events()
    {
        Assert.Equal("idle", Assert.IsType<VaultLockedEvent>(P("{\"type\":\"vault.locked\",\"reason\":\"idle\"}")).Reason);
        Assert.Equal("sync", Assert.IsType<VaultChangedEvent>(P("{\"type\":\"vault.changed\",\"reason\":\"sync\"}")).Reason);
    }

    [Fact]
    public void Ssh_Events()
    {
        var keys = Assert.IsType<SshKeysEvent>(P("{\"type\":\"ssh.keys\",\"state\":\"grace\",\"count\":2,\"keys_state\":\"grace\",\"reason\":\"idle\",\"expires_at\":1790000000}"));
        Assert.Equal(("grace", 2, "idle", 1790000000L), (keys.State, keys.Count, keys.Reason, keys.ExpiresAt));
        var agent = Assert.IsType<SshAgentEvent>(P("{\"type\":\"ssh.agent\",\"state\":\"conflict\",\"pipe\":\"p\",\"error\":\"taken\"}"));
        Assert.Equal(("conflict", "p", "taken"), (agent.State, agent.Pipe, agent.Error));
        Assert.IsType<SshKeysNeededEvent>(P("{\"type\":\"ssh.keys_needed\",\"client\":[]}"));
        Assert.IsType<SshUsedEvent>(P("{\"type\":\"ssh.used\",\"fingerprint\":\"SHA256:x\"}"));
        Assert.IsType<SshUsedEvent>(P("{\"type\":\"ssh.refused\",\"fingerprint\":\"SHA256:x\"}"));
    }

    [Fact]
    public void LogLine_NeverContainsPayload()
    {
        var e = P("{\"type\":\"vault.locked\",\"reason\":\"TOP-SECRET-REASON\"}");
        Assert.Equal("vault.locked", e.LogLine);
        Assert.DoesNotContain("SECRET", e.LogLine);
    }

    [Fact]
    public void Update_Events_AreGrouped()
    {
        var u = Assert.IsType<UpdateEvent>(P("{\"type\":\"update.available\",\"version_code\":12,\"version_name\":\"0.1.12\",\"mandatory\":true}"));
        Assert.Equal("update.available", u.Type);
        Assert.Equal("0.1.12", u.VersionName);
        Assert.True(u.Mandatory);
        Assert.IsType<UpdateEvent>(P("{\"type\":\"update.progress\",\"received\":1,\"total\":2}"));
    }

    [Theory]
    [InlineData("{\"type\":\"future.thing\"}")]
    [InlineData("{\"type\":\"clip.received\",\"event_id\":\"e1\",\"text\":\"x\"}")]
    [InlineData("{\"type\":\"pair.request\",\"pair_id\":\"p1\"}")]
    [InlineData("{}")]
    [InlineData("[]")]
    [InlineData("not json")]
    [InlineData("{\"type\":5}")]
    public void UnknownRetiredOrMalformed_IsUnknownEvent_NotAnException(string json) =>
        Assert.IsType<UnknownEvent>(P(json));

    [Fact]
    public void WrongFieldTypes_DoNotThrow()
    {
        var e = Assert.IsType<SshKeysEvent>(P("{\"type\":\"ssh.keys\",\"state\":7,\"count\":\"many\",\"expires_at\":\"x\"}"));
        Assert.Equal("", e.State);
        Assert.Equal(0, e.Count);
        Assert.Equal(0, e.ExpiresAt);
    }
}

public class ErrorMessageTests
{
    [Theory]
    [InlineData("NO_SPACE", "空间")]
    [InlineData("KEY_INVALID", "密钥")]
    [InlineData("RATE_LIMITED", "频繁")]
    [InlineData("UPDATE_FAILED", "更新")]
    public void KnownCodes_HaveHumanText(string code, string fragment) =>
        Assert.Contains(fragment, ErrorMessages.ForCode(code));

    [Theory]
    [InlineData("EXPIRED"), InlineData("HASH_MISMATCH"), InlineData("TOO_LARGE"), InlineData("NO_SPACE"),
     InlineData("RATE_LIMITED"), InlineData("INVALID"), InlineData("DB_UNAVAILABLE"), InlineData("KEY_INVALID"),
     InlineData("NOT_INITIALIZED"), InlineData("METHOD_NOT_FOUND"), InlineData("UPDATE_DISABLED"),
     InlineData("UPDATE_FAILED"), InlineData("UPDATE_REJECTED"), InlineData("INTERNAL"), InlineData("NOT_FOUND"),
     InlineData("VAULT_LOCKED"), InlineData("VAULT_WRONG_CREDENTIALS"), InlineData("SSH_KEY_CORRUPT")]
    public void EveryDocumentedCode_IsMapped(string code) =>
        Assert.DoesNotContain("未知", ErrorMessages.ForCode(code));

    [Fact]
    public void UnknownCode_ShowsGenericTextAndTheCode()
    {
        var m = ErrorMessages.ForCode("SOMETHING_NEW");
        Assert.Contains("SOMETHING_NEW", m);
        Assert.Contains("未知", m);
        Assert.Equal(Strings.ErrorGeneric, ErrorMessages.ForCode(""));
        Assert.Equal(Strings.ErrorGeneric, ErrorMessages.ForCode(null));
    }

    [Fact]
    public void RetiredNetworkCodes_AreNoLongerKnown()
    {
        foreach (var code in new[] { "NO_TAILNET", "NOT_PAIRED", "PEER_OFFLINE", "PAIRING_FAILED" })
            Assert.Contains(code, ErrorMessages.ForCode(code));
    }
}
