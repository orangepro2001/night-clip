using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class PairingInputTests
{
    private static readonly DateTimeOffset Now = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000);
    private const string Psk = "AAAAAAAAAAAAAAAAAAAAAA";           // 16 bytes, unpadded base64url
    private static readonly string Fp = new('A', 43);              // 32 bytes

    private static string Link(string host = "100.101.146.117", string port = "47821", string psk = Psk,
        string? fp = null, long exp = 1_790_000_600, string v = "1") =>
        $"clipbridge://pair?v={v}&host={host}&port={port}&psk={psk}" + (fp is null ? "" : $"&fp={fp}") + $"&exp={exp}";

    [Fact]
    public void ValidLink_IsAccepted_AndPassedThroughUntouched()
    {
        var link = Link(fp: Fp);
        var r = PairingInput.FromLink(link, Now);
        Assert.True(r.Ok, r.Error);
        Assert.Equal(link, r.Request!.Link);
        Assert.Equal(link, r.Request.ToParams()["link"]);
    }

    [Fact]
    public void Link_SurroundedByTextOrWhitespace_IsExtracted()
    {
        var r = PairingInput.FromLink("  请加入：\n" + Link() + "\n谢谢", Now);
        Assert.True(r.Ok, r.Error);
        Assert.Equal(Link(), r.Request!.Link);
    }

    [Fact]
    public void Ipv6TailnetLink_IsAccepted() =>
        Assert.True(PairingInput.FromLink(Link(host: "fd7a:115c:a1e0::1"), Now).Ok);

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("https://example.com")]
    [InlineData("clipbridge://other?x=1")]
    [InlineData("clipbridge://pair")]
    public void NotALink_IsRejected(string text) => Assert.False(PairingInput.FromLink(text, Now).Ok);

    [Fact]
    public void NullText_IsRejected() => Assert.False(PairingInput.FromLink(null, Now).Ok);

    [Fact]
    public void Expired_ReportsExpiry()
    {
        var r = PairingInput.FromLink(Link(exp: 1_789_999_999), Now);
        Assert.False(r.Ok);
        Assert.Equal(Strings.ErrPairingExpired, r.Error);
    }

    [Theory]
    [InlineData("9")]
    [InlineData("2")]
    public void WrongVersion_IsRejected(string v) =>
        Assert.Equal(Strings.PairLinkVersion, PairingInput.FromLink(Link(v: v), Now).Error);

    [Fact]
    public void DuplicateParameter_IsRejected() =>
        Assert.False(PairingInput.FromLink(Link() + "&host=100.101.146.118", Now).Ok);

    [Theory]
    [InlineData("example.com")]
    [InlineData("100.101.146")]
    [InlineData("192.168.1.5")]        // an IP, but not a tailnet address
    [InlineData("127.0.0.1")]
    [InlineData("fe80::1")]
    public void HostMustBeATailnetIp(string host) => Assert.False(PairingInput.FromLink(Link(host: host), Now).Ok);

    [Theory]
    [InlineData("0")]
    [InlineData("65536")]
    [InlineData("-1")]
    [InlineData("abc")]
    [InlineData("")]
    public void BadPort_IsRejected(string port) => Assert.False(PairingInput.FromLink(Link(port: port), Now).Ok);

    [Theory]
    [InlineData("short")]
    [InlineData("AAAAAAAAAAAAAAAAAAAAAAAA")]      // too long
    [InlineData("AAAAAAAAAAAAAAAAAAAA!!")]        // not base64url
    public void BadPsk_IsRejected(string psk) => Assert.False(PairingInput.FromLink(Link(psk: psk), Now).Ok);

    [Fact]
    public void BadFingerprint_IsRejected() => Assert.False(PairingInput.FromLink(Link(fp: "tooshort"), Now).Ok);

    [Fact]
    public void ErrorMessages_NeverEchoTheSecret()
    {
        var secret = "SECRETSECRETSECRET!!!";
        var r = PairingInput.FromLink(Link(psk: secret), Now);
        Assert.DoesNotContain("SECRET", r.Error);
    }

    [Fact]
    public void Manual_Valid()
    {
        var r = PairingInput.FromManual(" 100.64.0.1 ", " 47821 ", " " + Psk + " ", "");
        Assert.True(r.Ok, r.Error);
        var p = r.Request!.ToParams();
        Assert.Equal("100.64.0.1", p["host"]);
        Assert.Equal(47821, p["port"]);
        Assert.Equal(Psk, p["psk"]);
        Assert.False(p.ContainsKey("fp"));
    }

    [Fact]
    public void Manual_WithFingerprint_PassesItOn()
    {
        var r = PairingInput.FromManual("100.64.0.1", "1", Psk, Fp);
        Assert.True(r.Ok, r.Error);
        Assert.Equal(Fp, r.Request!.ToParams()["fp"]);
    }

    [Theory]
    [InlineData(null, "47821", Psk)]
    [InlineData("", "47821", Psk)]
    [InlineData("100.64.0.1", "", Psk)]
    [InlineData("100.64.0.1", "47821", "")]
    [InlineData("100.64.0.1", "47821", null)]
    [InlineData("8.8.8.8", "47821", Psk)]
    public void Manual_Invalid(string? host, string? port, string? psk) =>
        Assert.False(PairingInput.FromManual(host, port, psk, null).Ok);

    [Theory]
    [InlineData("100.64.0.0", true)]
    [InlineData("100.127.255.255", true)]
    [InlineData("100.63.255.255", false)]
    [InlineData("100.128.0.0", false)]
    [InlineData("fd7a:115c:a1e0:ab12::7", true)]
    [InlineData("fd7a:115c:a1e1::1", false)]
    [InlineData("::ffff:100.100.1.1", true)]
    public void TailnetRanges(string ip, bool expected) =>
        Assert.Equal(expected, PairingInput.IsTailnetAddress(System.Net.IPAddress.Parse(ip)));
}

public class PairJoinSessionTests
{
    private static PairSasEvent Sas(string join, string sas = "123456") => new() { Type = "pair.sas", JoinId = join, Sas = sas };
    private static PairResultEvent Res(string join, string state, string role = "joiner", string code = "", string name = "") =>
        new() { Type = "pair.result", JoinId = join, State = state, Role = role, Code = code, Name = name };

    [Fact]
    public void HappyPath()
    {
        var s = new PairJoinSession();
        Assert.Equal(PairJoinPhase.Idle, s.Phase);
        s.Begin();
        Assert.Equal(PairJoinPhase.Joining, s.Phase);
        s.OnJoinStarted("j1");
        Assert.True(s.OnEvent(Sas("j1", "482913")));
        Assert.Equal(PairJoinPhase.AwaitingSas, s.Phase);
        Assert.Equal("482913", s.Sas);
        Assert.True(s.OnEvent(Res("j1", "accepted", name: "Tony-PC")));
        Assert.Equal(PairJoinPhase.Accepted, s.Phase);
        Assert.Equal("Tony-PC", s.PeerName);
    }

    [Fact]
    public void EventsBeforeTheJoinIdIsKnown_AreReplayed()
    {
        var s = new PairJoinSession();
        s.Begin();
        Assert.False(s.OnEvent(Sas("j7")));        // arrives before pair.join returned
        Assert.Equal(PairJoinPhase.Joining, s.Phase);
        s.OnJoinStarted("j7");
        Assert.Equal(PairJoinPhase.AwaitingSas, s.Phase);
        Assert.Equal("123456", s.Sas);
    }

    [Fact]
    public void EarlyEventsOfAnotherJoin_AreDroppedOnReplay()
    {
        var s = new PairJoinSession();
        s.Begin();
        s.OnEvent(Sas("other"));
        s.OnJoinStarted("mine");
        Assert.Equal(PairJoinPhase.Joining, s.Phase);
    }

    [Fact]
    public void Failure_MapsTheCode()
    {
        var s = new PairJoinSession();
        s.Begin();
        s.OnJoinStarted("j");
        s.OnEvent(Res("j", "failed", code: "PAIRING_FAILED"));
        Assert.Equal(PairJoinPhase.Failed, s.Phase);
        Assert.Equal(Strings.ErrPairingFailed, s.ErrorMessage);
    }

    [Fact]
    public void Rejected_And_Expired()
    {
        var s = new PairJoinSession();
        s.Begin(); s.OnJoinStarted("j");
        s.OnEvent(Res("j", "rejected"));
        Assert.Equal(Strings.PairRejected, s.ErrorMessage);
        s.Begin(); s.OnJoinStarted("k");
        s.OnEvent(Res("k", "expired"));
        Assert.Equal(Strings.ErrPairingExpired, s.ErrorMessage);
    }

    [Fact]
    public void ListenerRoleResults_AreIgnored()
    {
        var s = new PairJoinSession();
        s.Begin(); s.OnJoinStarted("j");
        Assert.False(s.OnEvent(Res("j", "accepted", role: "listener")));
        Assert.Equal(PairJoinPhase.Joining, s.Phase);
    }

    [Fact]
    public void CallFailure_FailsImmediately()
    {
        var s = new PairJoinSession();
        s.Begin();
        s.OnJoinFailed("busy");
        Assert.Equal(PairJoinPhase.Failed, s.Phase);
        Assert.Equal("busy", s.ErrorMessage);
        Assert.False(s.OnEvent(Sas("j")));
    }
}

public class QrRendererTests
{
    private static readonly string[] Rows = { "0000", "0110", "0100", "0000" };

    [Fact]
    public void Parse_ReadsModules()
    {
        var m = QrRenderer.Parse(Rows, 4)!;
        Assert.True(m[1, 1]); Assert.True(m[1, 2]); Assert.True(m[2, 1]);
        Assert.False(m[0, 0]); Assert.False(m[2, 2]);
    }

    [Theory]
    [InlineData(null, 4)]
    [InlineData(new[] { "00", "00" }, 4)]                       // wrong count
    [InlineData(new[] { "000", "000", "000", "000" }, 4)]       // wrong width
    [InlineData(new[] { "0000", "0x10", "0100", "0000" }, 4)]   // illegal character
    [InlineData(new[] { "0" }, 0)]
    public void Parse_RejectsMalformed(string[]? rows, int size) => Assert.Null(QrRenderer.Parse(rows, size));

    [Theory]
    [InlineData(4, 10, 6)]       // never below 6 px per module
    [InlineData(40, 100, 6)]
    [InlineData(40, 400, 10)]
    [InlineData(10, 1000, 100)]
    public void Scale_HasAMinimumOfSixPixels(int size, int target, int expected) =>
        Assert.Equal(expected, QrRenderer.ChooseScale(size, target));

    [Fact]
    public void Render_ScalesWithNearestNeighbour()
    {
        var bmp = QrRenderer.Render(Rows, 4, 0)!;
        Assert.Equal(24, bmp.Width);
        Assert.Equal(24, bmp.Height);
        Assert.Equal(24 * 24 * 4, bmp.Bgra32.Length);
        byte At(int x, int y) => bmp.Bgra32[(y * 24 + x) * 4];
        Assert.Equal(0xFF, At(0, 0));        // quiet zone: white
        Assert.Equal(0x00, At(6, 6));        // module (row 1, col 1) is dark: pixels 6..11
        Assert.Equal(0x00, At(11, 11));
        Assert.Equal(0x00, At(12, 6));       // module (1,2) is dark: pixels 12..17
        Assert.Equal(0x00, At(17, 11));
        Assert.Equal(0xFF, At(18, 6));       // module (1,3) is light
        Assert.Equal(0x00, At(6, 12));       // module (2,1) is dark
        Assert.Equal(0xFF, At(12, 12));      // module (2,2) is light
        Assert.Equal(0xFF, bmp.Bgra32[3]);   // opaque alpha
    }

    [Fact]
    public void Render_Invalid_ReturnsNull() => Assert.Null(QrRenderer.Render(new[] { "01" }, 2, 100));
}
