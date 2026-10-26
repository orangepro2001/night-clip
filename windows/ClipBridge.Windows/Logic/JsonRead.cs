using System.Text.Json;

namespace ClipBridge.Windows.Logic;

/// <summary>Tolerant readers for the core's JSON: a missing or mistyped field yields the default.</summary>
public static class JsonRead
{
    public static string Str(JsonElement e, string name, string def = "") =>
        e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.String
            ? v.GetString() ?? def : def;

    public static long Long(JsonElement e, string name, long def = 0)
    {
        if (e.ValueKind != JsonValueKind.Object || !e.TryGetProperty(name, out var v) || v.ValueKind != JsonValueKind.Number)
            return def;
        if (v.TryGetInt64(out var l)) return l;
        return double.IsFinite(v.GetDouble()) ? (long)v.GetDouble() : def;
    }

    public static bool Bool(JsonElement e, string name, bool def = false) =>
        e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v)
            ? v.ValueKind switch { JsonValueKind.True => true, JsonValueKind.False => false, _ => def }
            : def;

    public static JsonElement Obj(JsonElement e, string name) =>
        e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.Object
            ? v : default;

    public static IReadOnlyList<JsonElement> Arr(JsonElement e, string name)
    {
        if (e.ValueKind != JsonValueKind.Object || !e.TryGetProperty(name, out var v) || v.ValueKind != JsonValueKind.Array)
            return Array.Empty<JsonElement>();
        var list = new List<JsonElement>();
        foreach (var item in v.EnumerateArray()) list.Add(item);
        return list;
    }

    public static IReadOnlyList<string> StrArr(JsonElement e, string name)
    {
        var list = new List<string>();
        foreach (var item in Arr(e, name))
            if (item.ValueKind == JsonValueKind.String && item.GetString() is { } s) list.Add(s);
        return list;
    }
}
