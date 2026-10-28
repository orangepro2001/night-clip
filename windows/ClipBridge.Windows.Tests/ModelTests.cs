using System.Text.Json;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Update;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class PeerModelTests
{
    private static readonly DateTimeOffset Now = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000);

    private static JsonElement J(string json)
    {
        using var d = JsonDocument.Parse(json);
        return d.RootElement.Clone();
    }

    [Fact]
    public void Peers_SendEnabledDefaultsToTrueAndFollowsTheCore()
    {
        var list = PeerItem.ListFromJson(J(
            "{\"peers\":[{\"device_id\":\"dev_a\",\"name\":\"A\"},{\"device_id\":\"dev_b\",\"name\":\"B\",\"send_enabled\":false}," +
            "{\"device_id\":\"dev_c\",\"name\":\"C\",\"send_enabled\":true}]}"), Now);
        Assert.Equal(new[] { true, false, true }, list.Select(p => p.SendEnabled).ToArray());
    }

    [Fact]
    public void Peers_PlatformText()
    {
        var list = PeerItem.ListFromJson(J(
            "{\"peers\":[{\"device_id\":\"dev_m\",\"name\":\"Mac\",\"platform\":\"macos\",\"state\":\"connected\"}," +
            "{\"device_id\":\"dev_w\",\"name\":\"PC\",\"platform\":\"windows\",\"state\":\"connected\"}]}"), Now);
        Assert.Equal(new[] { "macOS", "Windows" }, list.Select(p => p.PlatformText).ToArray());
    }

    [Fact]
    public void Peers_MapFromPeersList()
    {
        var list = PeerItem.ListFromJson(J(
            "{\"peers\":[{\"device_id\":\"dev_a\",\"name\":\"Pixel\",\"platform\":\"android\",\"state\":\"offline\",\"outbox_pending\":3,\"last_seen_at\":1789999700,\"apply_mode\":\"manual\",\"tailnet_addrs\":[\"100.64.0.2\"]}," +
            "{\"device_id\":\"\",\"name\":\"junk\"}]}"), Now);
        var p = Assert.Single(list);
        Assert.Equal("Pixel", p.DisplayName);
        Assert.Equal("Android", p.PlatformText);
        Assert.Equal("离线（排队 3 条）", p.StateText);
        Assert.Equal("", p.PendingText);                 // already part of the offline text
        Assert.Equal("手动复制", p.ApplyModeText);
        Assert.Equal("最后在线：5 分钟前", p.LastSeenText);
        Assert.True(p.CanSendFile);
        Assert.True(p.CanReconnect);
    }

    [Theory]
    [InlineData("connected", "已连接", true, false, false)]
    [InlineData("connecting", "正在连接", true, false, false)]
    [InlineData("unpaired", "对方已不认识本机，需要重新配对", false, true, false)]
    [InlineData("revoked", "已解除配对", false, false, true)]
    public void States(string state, string text, bool canSend, bool unpaired, bool revoked)
    {
        var p = new PeerItem("d", "n", "windows", state, 0, 0, "notify", Array.Empty<string>(), Now);
        Assert.Equal(text, p.StateText);
        Assert.Equal(canSend, p.CanSendFile);
        Assert.Equal(unpaired, p.IsUnpaired);
        Assert.Equal(revoked, p.IsRevoked);
    }

    [Fact]
    public void ConnectedPeer_SaysConnectedInsteadOfLastSeen() =>
        Assert.Equal("最后在线：已连接", new PeerItem("d", "n", "windows", "connected", 0, 0, "auto", Array.Empty<string>(), Now).LastSeenText);

    [Theory]
    [InlineData("  Tony  ", "Tony")]
    [InlineData("", null)]
    [InlineData("   ", null)]
    public void NormalizeName(string input, string? expected) => Assert.Equal(expected, PeerItem.NormalizeName(input));

    [Fact]
    public void NormalizeName_LimitIs64()
    {
        Assert.NotNull(PeerItem.NormalizeName(new string('a', 64)));
        Assert.Null(PeerItem.NormalizeName(new string('a', 65)));
        Assert.Null(PeerItem.NormalizeName(null));
    }
}

public class InboxModelTests
{
    private static readonly DateTimeOffset Now = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000);

    private static InboxRow Row(bool sensitive, string text = "hunter2") =>
        new("e1", "dev_a", "Pixel", text, sensitive, "received", 1_790_000_000, Now);

    [Fact]
    public void SensitiveRows_AreHiddenUntilRevealed()
    {
        var r = Row(true);
        Assert.True(r.IsHidden);
        Assert.Equal(Strings.ReceivedSensitive, r.Preview);
        Assert.DoesNotContain("hunter2", r.Preview);
        r.ToggleReveal();
        Assert.Equal("hunter2", r.Preview);
        Assert.Equal(Strings.BtnHide, r.RevealText);
        r.ToggleReveal();
        Assert.True(r.IsHidden);
    }

    [Fact]
    public void NormalRows_ShowPreview_AndCannotReveal()
    {
        var r = Row(false, "line one\nline two");
        Assert.Equal("line one line two", r.Preview);
        Assert.False(r.CanReveal);
        Assert.True(r.CanCopy);
        Assert.True(r.CanDismiss);
    }

    [Fact]
    public void States_AndDismiss()
    {
        var r = Row(false);
        var raised = false;
        r.PropertyChanged += (_, _) => raised = true;
        r.SetState("ignored");
        Assert.True(raised);
        Assert.Equal(Strings.InboxStateIgnored, r.StateText);
        Assert.False(r.CanDismiss);
        Assert.Equal(Strings.InboxFrom("Pixel"), r.FromText);
    }

    [Fact]
    public void FromEvent_And_FromJson()
    {
        var e = Assert.IsType<ClipReceivedEvent>(CoreEventParser.Parse(
            "{\"type\":\"clip.received\",\"event_id\":\"e9\",\"from\":\"dev_a\",\"from_name\":\"PC\",\"text\":\"t\",\"sensitive\":true,\"decision\":\"notify\",\"received_at\":1790000000}"));
        var r = InboxRow.FromEvent(e, Now);
        Assert.Equal(("e9", "PC", true, "received"), (r.EventId, r.FromName, r.Sensitive, r.State));

        using var doc = JsonDocument.Parse("{\"items\":[{\"event_id\":\"i1\",\"from\":\"d\",\"from_name\":\"X\",\"text\":\"hi\",\"sensitive\":false,\"state\":\"applied\",\"received_at\":1790000000}]}");
        var list = InboxRow.ListFromJson(doc.RootElement, Now);
        Assert.Equal("applied", Assert.Single(list).State);
    }
}

public class SettingsModelTests
{
    private const string DefaultDir = @"C:\Users\me\Downloads\ClipBridge";

    private static SettingsInput Good(Func<SettingsInput, SettingsInput>? edit = null)
    {
        var i = new SettingsInput(true, "manual", "900", "200", true, @"D:\Recv", SensitiveDetection: true);
        return edit is null ? i : edit(i);
    }

    [Fact]
    public void FromJson_AppliesDefaults()
    {
        using var d = JsonDocument.Parse("{\"settings\":{\"capture\":\"on\",\"clip_ttl_s\":\"300\"}}");
        var s = SettingsValues.FromJson(d.RootElement, DefaultDir);
        Assert.True(s.Capture);
        Assert.Equal(300, s.ClipTtlSeconds);
        Assert.Equal(200, s.HistoryLimit);
        Assert.Equal("auto", s.DefaultApplyMode);   // the core's default for newly paired devices
        Assert.Equal(0, s.PausedUntil);
        Assert.True(s.SendSensitive);
        Assert.True(s.SensitiveDetection);
        Assert.Equal(DefaultDir, s.DownloadDir);
        Assert.Equal(4294967296, s.MaxFileBytes);
    }

    [Fact]
    public void FromJson_CaptureIsOnUnlessExplicitlyOff_AndPauseIsRead()
    {
        using var none = JsonDocument.Parse("{\"settings\":{}}");
        Assert.True(SettingsValues.FromJson(none.RootElement, DefaultDir).Capture);
        using var off = JsonDocument.Parse("{\"settings\":{\"capture\":\"off\",\"paused_until\":\"1790003600\"}}");
        var s = SettingsValues.FromJson(off.RootElement, DefaultDir);
        Assert.False(s.Capture);
        Assert.Equal(1790003600, s.PausedUntil);
        using var junk = JsonDocument.Parse("{\"settings\":{\"paused_until\":\"soon\"}}");
        Assert.Equal(0, SettingsValues.FromJson(junk.RootElement, DefaultDir).PausedUntil);
    }

    [Fact]
    public void FromJson_TakesTheCoresFileSettings_ButIgnoresARelativeDir()
    {
        using var d = JsonDocument.Parse("{\"settings\":{\"download_dir\":\"E:\\\\In\",\"max_file_bytes\":\"1000\",\"send_sensitive\":\"off\",\"sensitive_detection\":\"off\",\"default_apply_mode\":\"auto\"}}");
        var s = SettingsValues.FromJson(d.RootElement, DefaultDir);
        Assert.Equal(@"E:\In", s.DownloadDir);
        Assert.Equal(1000, s.MaxFileBytes);
        Assert.False(s.SendSensitive);
        Assert.False(s.SensitiveDetection);
        Assert.Equal("auto", s.DefaultApplyMode);

        using var rel = JsonDocument.Parse("{\"settings\":{\"download_dir\":\"下载\\\\ClipBridge\"}}");
        Assert.Equal(DefaultDir, SettingsValues.FromJson(rel.RootElement, DefaultDir).DownloadDir);
    }

    [Fact]
    public void Valid_Input_Passes()
    {
        var v = SettingsModel.Validate(Good());
        Assert.True(v.Ok);
        Assert.Equal(900, v.Values!.ClipTtlSeconds);
        Assert.Equal(@"D:\Recv", v.Values.DownloadDir);
    }

    [Theory]
    [InlineData("29", false)]
    [InlineData("30", true)]
    [InlineData("86400", true)]
    [InlineData("86401", false)]
    [InlineData("abc", false)]
    [InlineData("", false)]
    [InlineData("-5", false)]
    [InlineData("1.5", false)]
    public void Ttl_Range(string text, bool ok) =>
        Assert.Equal(ok, SettingsModel.Validate(Good(i => i with { TtlText = text })).Ok);

    [Theory]
    [InlineData("9", false)]
    [InlineData("10", true)]
    [InlineData("5000", true)]
    [InlineData("5001", false)]
    [InlineData("x", false)]
    public void History_Range(string text, bool ok) =>
        Assert.Equal(ok, SettingsModel.Validate(Good(i => i with { HistoryText = text })).Ok);

    [Theory]
    [InlineData(@"C:\Users\me\Downloads\ClipBridge", true)]
    [InlineData(@"C:\", true)]
    [InlineData(@"C:/Users/me", true)]
    [InlineData(@"\\server\share\dir", true)]
    [InlineData(@"relative\dir", false)]
    [InlineData(@"下载\ClipBridge", false)]
    [InlineData("", false)]
    [InlineData(@"C:", false)]
    [InlineData(@"C:\a|b", false)]
    [InlineData(@"C:\a\?x", false)]
    [InlineData(@"\\?\C:\x", false)]
    [InlineData(@"C:\a:b", false)]
    [InlineData(@"\\server", false)]
    public void AbsolutePath(string path, bool expected) =>
        Assert.Equal(expected, SettingsModel.IsAbsoluteWindowsPath(path));

    [Fact]
    public void DownloadDir_TrailingSeparatorIsTrimmed_AndRootKept()
    {
        Assert.Equal(@"D:\Recv", SettingsModel.Validate(Good(i => i with { DownloadDir = @"D:\Recv\" })).Values!.DownloadDir);
        Assert.Equal(@"D:\", SettingsModel.Validate(Good(i => i with { DownloadDir = @"D:\" })).Values!.DownloadDir);
    }

    [Fact]
    public void BadMode_And_MultipleErrors_AreAllReported()
    {
        var v = SettingsModel.Validate(Good(i => i with { DefaultApplyMode = "always", TtlText = "1", HistoryText = "1", DownloadDir = "x" }));
        Assert.False(v.Ok);
        Assert.Equal(4, v.Errors.Count);
        Assert.Null(v.Values);
    }

    [Fact]
    public void Diff_ListsOnlyChangedKeys()
    {
        var old = new SettingsValues(false, "notify", 900, 200, true, DefaultDir, 4294967296);
        Assert.Empty(SettingsModel.Diff(old, old));
        var changed = old with { Capture = true, ClipTtlSeconds = 60, SendSensitive = false, SensitiveDetection = false, DownloadDir = @"D:\x", DefaultApplyMode = "auto", HistoryLimit = 50 };
        var d = SettingsModel.Diff(old, changed).ToDictionary(k => k.Key, k => k.Value);
        Assert.Equal(7, d.Count);
        Assert.Equal("off", d["sensitive_detection"]);
        Assert.Equal("on", d["capture"]);
        Assert.Equal("60", d["clip_ttl_s"]);
        Assert.Equal("off", d["send_sensitive"]);
        Assert.Equal(@"D:\x", d["download_dir"]);
        Assert.Equal("auto", d["default_apply_mode"]);
        Assert.Equal("50", d["history_limit"]);
    }
}

public class UpdateViewTests
{
    private static UpdateInfo U(string json)
    {
        using var d = JsonDocument.Parse(json);
        return UpdateInfo.FromJson(d.RootElement);
    }

    [Fact]
    public void ChineseTextPerState()
    {
        Assert.Equal(Strings.UpdateDevBuild, UpdateView.Text(U("{}")));
        Assert.Equal(Strings.UpdateNotConfigured, UpdateView.Text(U("{\"enabled\":false,\"current_code\":5}")));
        Assert.Contains("0.1.9", UpdateView.Text(U("{\"enabled\":true,\"state\":\"available\",\"latest\":{\"version_name\":\"0.1.9\",\"version_code\":9}}")));
        Assert.Contains("必须", UpdateView.Text(U("{\"enabled\":true,\"state\":\"ready\",\"mandatory\":true,\"latest\":{\"version_name\":\"0.1.9\"}}")));
        Assert.Contains("25%", UpdateView.Text(U("{\"enabled\":true,\"state\":\"downloading\",\"received\":25,\"total\":100,\"latest\":{\"version_name\":\"0.1.9\"}}")));
        Assert.Contains("无法访问更新服务器", UpdateView.Text(U("{\"enabled\":true,\"state\":\"error\",\"error\":{\"code\":\"UPDATE_FAILED\",\"message\":\"HTTP 503\"}}")));
    }

    [Fact]
    public void ButtonsAndActionable()
    {
        var ready = U("{\"enabled\":true,\"state\":\"ready\"}");
        Assert.Equal(Strings.UpdateButtonInstall, UpdateView.ButtonText(ready));
        Assert.True(UpdateView.IsActionable(ready));
        Assert.Equal("", UpdateView.ButtonText(U("{\"enabled\":true,\"state\":\"checking\"}")));
        Assert.False(UpdateView.IsActionable(U("{\"enabled\":true,\"state\":\"up_to_date\"}")));
    }
}
