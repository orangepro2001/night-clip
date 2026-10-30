using System.Text;
using System.Text.Json;

namespace ClipBridge.Windows.Logic;

/// <summary>
/// Builds the copyable diagnostics text from a <c>diagnostics.get</c> result. Only a whitelist of
/// fields is read, so nothing new that the core might add (and certainly no secret) can leak into it.
/// </summary>
public static class DiagnosticsText
{
    public static string Build(JsonElement diag, string appVersion, DateTimeOffset now, IReadOnlyCollection<string>? warnings = null)
    {
        var sb = new StringBuilder();
        sb.AppendLine("ClipBridge 诊断信息");
        sb.AppendLine($"时间: {now:yyyy-MM-dd HH:mm:ss zzz}");
        sb.AppendLine($"应用版本: {appVersion}");
        sb.AppendLine($"核心版本: {JsonRead.Str(diag, "core", "?")}  协议: {JsonRead.Str(diag, "protocol", "?")}");
        sb.AppendLine($"设备 ID: {JsonRead.Str(diag, "device_id", "?")}");
        sb.AppendLine($"指纹前缀: {JsonRead.Str(diag, "fingerprint_prefix", "?")}");
        sb.AppendLine($"平台: {JsonRead.Str(diag, "platform", "?")}  监听: {(JsonRead.Bool(diag, "listen") ? "是" : "否")}");

        var addrs = JsonRead.StrArr(diag, "listening_addrs");
        sb.AppendLine($"监听地址: {(addrs.Count == 0 ? "（无）" : string.Join(", ", addrs))}  端口: {JsonRead.Long(diag, "port")}");
        var listenError = JsonRead.Str(diag, "listen_error");
        if (listenError.Length > 0) sb.AppendLine($"监听失败: {listenError}");
        var local = JsonRead.StrArr(diag, "local_addrs");
        if (local.Count > 0) sb.AppendLine($"本机网卡地址: {string.Join(", ", local)}");
        sb.AppendLine($"丢弃的事件数: {JsonRead.Long(diag, "events_dropped")}");
        if (warnings is { Count: > 0 }) sb.AppendLine($"警告: {string.Join(", ", warnings)}");

        sb.AppendLine("对端:");
        var peers = JsonRead.Arr(diag, "peers");
        if (peers.Count == 0) sb.AppendLine("  （无）");
        foreach (var p in peers)
        {
            sb.AppendLine($"  - {JsonRead.Str(p, "name")} [{JsonRead.Str(p, "platform")}] {JsonRead.Str(p, "device_id")}");
            sb.AppendLine($"    状态: {JsonRead.Str(p, "state")}  排队: {JsonRead.Long(p, "outbox_pending")}  " +
                          $"接收游标: {JsonRead.Long(p, "recv_cursor")}  发送序号: {JsonRead.Long(p, "next_seq_out")}  策略: {JsonRead.Str(p, "apply_mode")}");
            var pa = JsonRead.StrArr(p, "tailnet_addrs");
            if (pa.Count > 0) sb.AppendLine($"    地址: {string.Join(", ", pa)}");
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
