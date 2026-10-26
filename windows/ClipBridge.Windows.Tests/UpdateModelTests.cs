using System.Text.Json;
using ClipBridge.Windows.Update;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class UpdateModelTests
{
    private static UpdateInfo Parse(string json)
    {
        using var doc = JsonDocument.Parse(json);
        return UpdateInfo.FromJson(doc.RootElement.Clone());
    }

    [Fact]
    public void ParsesTheCoreStatusShape()
    {
        var u = Parse("""
            {"enabled":true,"auto":true,"state":"downloading","current_code":5,"current_name":"0.1.5",
             "latest":{"version_code":9,"version_name":"0.1.9","size":1000,"notes":"fixes"},
             "mandatory":true,"received":250,"total":1000,"ready_path":"","checked_at":1}
            """);
        Assert.True(u.Enabled);
        Assert.Equal(9, u.LatestCode);
        Assert.Equal("0.1.9", u.LatestName);
        Assert.Equal("fixes", u.Notes);
        Assert.True(u.Mandatory);
        Assert.Equal(0.25, u.ProgressFraction, 3);
        Assert.Equal(UpdateInfo.Action.None, u.ButtonAction);
        Assert.Contains("25", u.Text);
    }

    [Theory]
    [InlineData("idle", UpdateInfo.Action.Check)]
    [InlineData("up_to_date", UpdateInfo.Action.Check)]
    [InlineData("error", UpdateInfo.Action.Check)]
    [InlineData("available", UpdateInfo.Action.Download)]
    [InlineData("ready", UpdateInfo.Action.Install)]
    [InlineData("checking", UpdateInfo.Action.None)]
    [InlineData("downloading", UpdateInfo.Action.None)]
    public void ButtonFollowsState(string state, UpdateInfo.Action expected)
    {
        var u = Parse($$"""{"enabled":true,"state":"{{state}}","current_code":5}""");
        Assert.Equal(expected, u.ButtonAction);
        Assert.Equal(expected == UpdateInfo.Action.None, u.ButtonText == "");
    }

    [Fact]
    public void DisabledBuildsExplainThemselvesAndOfferNoAction()
    {
        var dev = Parse("""{"enabled":false,"state":"idle","current_code":0}""");
        Assert.Equal(UpdateInfo.Action.None, dev.ButtonAction);
        Assert.Contains("Development", dev.Text);

        var unconfigured = Parse("""{"enabled":false,"state":"idle","current_code":7}""");
        Assert.Contains("not configured", unconfigured.Text);
    }

    [Fact]
    public void ErrorTextPrefersTheMessage()
    {
        var u = Parse("""{"enabled":true,"state":"error","error":{"code":"UPDATE_FAILED","message":"HTTP 503"}}""");
        Assert.Contains("HTTP 503", u.Text);
        var codeOnly = Parse("""{"enabled":true,"state":"error","error":{"code":"UPDATE_FAILED","message":""}}""");
        Assert.Contains("UPDATE_FAILED", codeOnly.Text);
    }

    [Fact]
    public void ToleratesMissingAndMalformedFields()
    {
        var empty = UpdateInfo.FromJson(default);
        Assert.False(empty.Enabled);
        Assert.Equal(UpdateInfo.Action.None, empty.ButtonAction);

        var weird = Parse("""{"enabled":"yes","state":5,"latest":"x","received":"a","total":0}""");
        Assert.False(weird.Enabled);
        Assert.Equal(0, weird.ProgressFraction);

        var over = Parse("""{"enabled":true,"state":"downloading","received":2000,"total":1000}""");
        Assert.Equal(1.0, over.ProgressFraction);
    }
}
