using System.Text.Json;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class CoreEventTests
{
    private static CoreEvent P(string json) => CoreEventParser.Parse(json);

    [Fact]
    public void ClipReceived_MapsAllFields()
    {
        var e = Assert.IsType<ClipReceivedEvent>(P(
            "{\"type\":\"clip.received\",\"event_id\":\"e1\",\"from\":\"dev_a\",\"from_name\":\"Pixel\",\"text\":\"hi\",\"sensitive\":true,\"decision\":\"auto\",\"received_at\":1790000000}"));
        Assert.Equal("e1", e.EventId);
        Assert.Equal("Pixel", e.FromName);
        Assert.Equal("hi", e.Text);
        Assert.True(e.Sensitive);
        Assert.Equal("auto", e.Decision);
        Assert.Equal(1790000000, e.ReceivedAt);
        Assert.Equal("e1", e.Id);
    }

    [Fact]
    public void ClipReceived_LiveDefaultsToTrue_AndReadsFalse()
    {
        var old = Assert.IsType<ClipReceivedEvent>(P("{\"type\":\"clip.received\",\"event_id\":\"e1\",\"text\":\"x\"}"));
        Assert.True(old.Live);
        var stale = Assert.IsType<ClipReceivedEvent>(P("{\"type\":\"clip.received\",\"event_id\":\"e2\",\"text\":\"x\",\"decision\":\"manual\",\"live\":false}"));
        Assert.False(stale.Live);
        Assert.Equal("manual", stale.Decision);
    }

    [Fact]
    public void LogLine_NeverContainsPayload()
    {
        var e = P("{\"type\":\"clip.received\",\"event_id\":\"e1\",\"text\":\"TOP-SECRET-TEXT\",\"decision\":\"notify\"}");
        Assert.Equal("clip.received e1", e.LogLine);
        Assert.DoesNotContain("SECRET", e.LogLine);
        Assert.Equal("core.warning", P("{\"type\":\"core.warning\",\"code\":\"NO_TAILNET\"}").LogLine);
    }

    [Fact]
    public void MissingDecision_DefaultsToNotify() =>
        Assert.Equal("notify", Assert.IsType<ClipReceivedEvent>(P("{\"type\":\"clip.received\",\"event_id\":\"x\"}")).Decision);

    [Fact]
    public void SeqAtArrival_SurvivesWith()
    {
        var e = Assert.IsType<ClipReceivedEvent>(P("{\"type\":\"clip.received\",\"event_id\":\"x\"}")) with { SeqAtArrival = 99 };
        Assert.Equal(99u, e.SeqAtArrival);
        Assert.Equal("clip.received", e.Type);
    }

    [Fact]
    public void Pairing_Events()
    {
        var req = Assert.IsType<PairRequestEvent>(P(
            "{\"type\":\"pair.request\",\"pair_id\":\"p1\",\"name\":\"Pixel\",\"platform\":\"android\",\"device_id\":\"dev_x\",\"sas\":\"482913\",\"expires_at\":\"2026-10-26T10:00:00Z\"}"));
        Assert.Equal(("p1", "Pixel", "android", "482913"), (req.PairId, req.Name, req.Platform, req.Sas));
        var sas = Assert.IsType<PairSasEvent>(P("{\"type\":\"pair.sas\",\"join_id\":\"j1\",\"sas\":\"111222\"}"));
        Assert.Equal(("j1", "111222"), (sas.JoinId, sas.Sas));
        var res = Assert.IsType<PairResultEvent>(P("{\"type\":\"pair.result\",\"state\":\"failed\",\"role\":\"joiner\",\"join_id\":\"j1\",\"code\":\"PAIRING_FAILED\",\"message\":\"m\"}"));
        Assert.Equal(("failed", "joiner", "PAIRING_FAILED"), (res.State, res.Role, res.Code));
        Assert.Equal("j1", res.Id);
    }

    [Fact]
    public void File_Events()
    {
        var o = Assert.IsType<FileOfferedEvent>(P("{\"type\":\"file.offered\",\"transfer_id\":\"tr_1\",\"from\":\"dev_a\",\"from_name\":\"Pixel\",\"name\":\"a.pdf\",\"size\":123456,\"sha256\":\"ab\",\"mime_type\":\"application/pdf\"}"));
        Assert.Equal(("tr_1", "a.pdf", 123456L), (o.TransferId, o.Name, o.Size));
        var p = Assert.IsType<FileProgressEvent>(P("{\"type\":\"file.progress\",\"transfer_id\":\"tr_1\",\"offset\":1048576,\"size\":123456789,\"rate_bps\":52428800}"));
        Assert.Equal((1048576L, 123456789L, 52428800L), (p.Offset, p.Size, p.RateBps));
        var d = Assert.IsType<FileDoneEvent>(P("{\"type\":\"file.done\",\"transfer_id\":\"tr_1\",\"direction\":\"in\",\"path\":\"C:\\\\x\\\\a.pdf\",\"name\":\"a.pdf\",\"size\":5}"));
        Assert.Equal("C:\\x\\a.pdf", d.Path);
        var f = Assert.IsType<FileFailedEvent>(P("{\"type\":\"file.failed\",\"transfer_id\":\"tr_1\",\"code\":\"HASH_MISMATCH\"}"));
        Assert.Equal("HASH_MISMATCH", f.Code);
        Assert.IsType<FileStateEvent>(P("{\"type\":\"file.state\",\"transfer_id\":\"tr_1\",\"state\":\"transferring\"}"));
    }

    [Fact]
    public void Core_And_Peer_Events()
    {
        var l = Assert.IsType<CoreListeningEvent>(P("{\"type\":\"core.listening\",\"addrs\":[\"100.101.146.117\"],\"port\":47821}"));
        Assert.Equal(new[] { "100.101.146.117" }, l.Addrs);
        Assert.Equal(47821, l.Port);
        Assert.Equal("NO_TAILNET", Assert.IsType<CoreWarningEvent>(P("{\"type\":\"core.warning\",\"code\":\"NO_TAILNET\"}")).Code);
        var s = Assert.IsType<PeerStateEvent>(P("{\"type\":\"peer.state\",\"device_id\":\"dev_a\",\"state\":\"connected\"}"));
        Assert.Equal("connected", s.State);
        Assert.Equal("Pixel", Assert.IsType<PeerRevokedEvent>(P("{\"type\":\"peer.revoked\",\"device_id\":\"d\",\"name\":\"Pixel\"}")).Name);
        var intro = Assert.IsType<PeerIntroducedEvent>(P("{\"type\":\"peer.introduced\",\"device_id\":\"d\",\"name\":\"Mac\",\"via_name\":\"PC\"}"));
        Assert.Equal(("d", "Mac", "PC"), (intro.DeviceId, intro.Name, intro.ViaName));
        Assert.Equal(2, Assert.IsType<OutboxExpiredEvent>(P("{\"type\":\"outbox.expired\",\"count\":2}")).Count);
        var a = Assert.IsType<ClipAckEvent>(P("{\"type\":\"clip.ack\",\"event_id\":\"e\",\"result\":\"ignored\",\"reason\":\"user_dismissed\"}"));
        Assert.Equal("ignored", a.Result);
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
    [InlineData("{}")]
    [InlineData("[]")]
    [InlineData("not json")]
    [InlineData("{\"type\":5}")]
    public void UnknownOrMalformed_IsUnknownEvent_NotAnException(string json) =>
        Assert.IsType<UnknownEvent>(P(json));

    [Fact]
    public void WrongFieldTypes_DoNotThrow()
    {
        var e = Assert.IsType<ClipReceivedEvent>(P("{\"type\":\"clip.received\",\"event_id\":7,\"text\":null,\"sensitive\":\"yes\",\"received_at\":\"x\"}"));
        Assert.Equal("", e.EventId);
        Assert.Equal("", e.Text);
        Assert.False(e.Sensitive);
        Assert.Equal(0, e.ReceivedAt);
    }
}

public class ErrorMessageTests
{
    [Theory]
    [InlineData("NO_TAILNET", "Tailscale")]
    [InlineData("LISTEN_FAILED", "47821")]
    [InlineData("PEER_OFFLINE", "不在线")]
    [InlineData("PAIRING_EXPIRED", "过期")]
    [InlineData("NO_SPACE", "空间")]
    [InlineData("KEY_INVALID", "密钥")]
    public void KnownCodes_HaveHumanText(string code, string fragment) =>
        Assert.Contains(fragment, ErrorMessages.ForCode(code));

    [Theory]
    [InlineData("NOT_PAIRED"), InlineData("PAIRING_EXPIRED"), InlineData("PAIRING_FAILED"), InlineData("NO_TAILNET"),
     InlineData("PEER_OFFLINE"), InlineData("TARGET_MISMATCH"), InlineData("SOURCE_MISMATCH"), InlineData("EXPIRED"),
     InlineData("HASH_MISMATCH"), InlineData("SEQUENCE_GAP"), InlineData("TOO_LARGE"), InlineData("NO_SPACE"),
     InlineData("RATE_LIMITED"), InlineData("INVALID"), InlineData("DB_UNAVAILABLE"), InlineData("KEY_INVALID"),
     InlineData("NOT_INITIALIZED"), InlineData("METHOD_NOT_FOUND"), InlineData("UPDATE_DISABLED"),
     InlineData("UPDATE_FAILED"), InlineData("UPDATE_REJECTED"), InlineData("INTERNAL")]
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
    public void FileFailures_UseTheDedicatedWording()
    {
        Assert.Equal("文件校验失败，请让对方重发", ErrorMessages.ForFile("HASH_MISMATCH"));
        Assert.Contains("磁盘", ErrorMessages.ForFile("NO_SPACE"));
        Assert.Contains("不在线", ErrorMessages.ForFile("PEER_OFFLINE"));
    }

    [Fact]
    public void PairResults()
    {
        Assert.Equal(Strings.PairRejected, ErrorMessages.ForPairResult("rejected", null));
        Assert.Equal(Strings.ErrPairingExpired, ErrorMessages.ForPairResult("expired", null));
        Assert.Equal(Strings.ErrPairingFailed, ErrorMessages.ForPairResult("failed", "PAIRING_FAILED"));
        Assert.Equal(Strings.PairFailed, ErrorMessages.ForPairResult("failed", ""));
    }

    [Fact]
    public void Suppressed_OnlyTooLargeAndSensitiveSpeakUp()
    {
        Assert.Contains("文件", ErrorMessages.ForSuppressed("too_large"));
        Assert.NotNull(ErrorMessages.ForSuppressed("sensitive_blocked"));
        foreach (var quiet in new[] { "echo", "duplicate", "empty", "capture_off", "no_peers", "", null })
            Assert.Null(ErrorMessages.ForSuppressed(quiet));
    }
}
