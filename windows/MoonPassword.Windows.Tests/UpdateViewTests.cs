using System.Text.Json;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Update;
using Xunit;

namespace MoonPassword.Windows.Tests;

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
