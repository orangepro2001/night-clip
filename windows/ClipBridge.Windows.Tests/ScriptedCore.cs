using System.Text.Json;
using ClipBridge.Windows.Interop;

namespace ClipBridge.Windows.Tests;

/// <summary>An <see cref="ICoreCaller"/> that answers per method from a handler and records every call.</summary>
internal sealed class ScriptedCore : ICoreCaller
{
    public readonly List<(string Method, JsonElement Params)> Calls = new();
    private readonly Dictionary<string, Func<JsonElement, string>> _handlers = new();

    /// <summary>The handler gets the parameters of the call and returns the JSON of the result (or throws a <see cref="CoreException"/>).</summary>
    public ScriptedCore On(string method, Func<JsonElement, string> handler)
    {
        _handlers[method] = handler;
        return this;
    }

    public ScriptedCore On(string method, string result) => On(method, _ => result);

    public JsonElement Call(string method, object? parameters = null)
    {
        var p = JsonDocument.Parse(JsonSerializer.Serialize(parameters ?? new { })).RootElement.Clone();
        Calls.Add((method, p));
        return VJ.Parse(_handlers.TryGetValue(method, out var h) ? h(p) : "{}");
    }

    public IEnumerable<JsonElement> ParamsOf(string method) => Calls.Where(c => c.Method == method).Select(c => c.Params);

    public int Count(string method) => Calls.Count(c => c.Method == method);
}
