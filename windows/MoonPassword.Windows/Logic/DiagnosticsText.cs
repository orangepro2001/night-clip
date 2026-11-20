using System.Text;
using System.Text.Json;

namespace MoonPassword.Windows.Logic;

/// <summary>
/// Builds the copyable diagnostics text from a <c>diagnostics.get</c> result. Only a whitelist of
/// fields is read, so nothing new that the core might add (and certainly no secret) can leak into it.
/// </summary>
public static class DiagnosticsText
{
    public static string Build(JsonElement diag, string appVersion, DateTimeOffset now)
    {
        var sb = new StringBuilder();
        sb.AppendLine(Strings.AppName + " 诊断信息");
        sb.AppendLine($"时间: {now:yyyy-MM-dd HH:mm:ss zzz}");
        sb.AppendLine($"应用版本: {appVersion}");
        sb.AppendLine($"核心版本: {JsonRead.Str(diag, "core", "?")}");
        sb.AppendLine($"平台: {JsonRead.Str(diag, "platform", "?")}");
        sb.AppendLine($"丢弃的事件数: {JsonRead.Long(diag, "events_dropped")}");

        var ssh = JsonRead.Obj(diag, "ssh");
        if (ssh.ValueKind == JsonValueKind.Object)
        {
            var err = JsonRead.Str(ssh, "error");
            sb.AppendLine($"SSH 代理: {(JsonRead.Bool(ssh, "enabled") ? "开" : "关")}，状态 {JsonRead.Str(ssh, "state")}，管道 {JsonRead.Str(ssh, "pipe")}，" +
                          $"已加载 {JsonRead.Long(ssh, "keys_loaded")} 把（{JsonRead.Str(ssh, "keys_state")}）" + (err.Length > 0 ? $"，错误: {err}" : ""));
        }

        var upd = JsonRead.Obj(diag, "update");
        if (upd.ValueKind == JsonValueKind.Object)
        {
            sb.AppendLine($"更新: 状态 {JsonRead.Str(upd, "state")}，当前 {JsonRead.Str(upd, "current_name")}（{JsonRead.Long(upd, "current_code")}），" +
                          $"{(JsonRead.Bool(upd, "enabled") ? "已启用" : "未启用")}");
        }
        return sb.ToString();
    }
}
