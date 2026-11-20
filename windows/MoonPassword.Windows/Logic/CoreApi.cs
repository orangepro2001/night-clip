using System.Text.Json;
using MoonPassword.Windows.Interop;

namespace MoonPassword.Windows.Logic;

/// <summary>
/// Typed wrapper over the core facade (docs/api-spec.md §7.3, docs/shell-guide.md §2) for everything that is not the
/// vault (VaultApi) or SSH (SshApi). Every method is a blocking call: run it off the UI thread. Methods throw
/// <see cref="CoreException"/> with the core's code.
/// </summary>
public sealed class CoreApi
{
    private readonly ICoreCaller _core;
    public CoreApi(ICoreCaller core) => _core = core;

    // ---- status -----------------------------------------------------------------------
    public JsonElement Status() => _core.Call("status");
    public JsonElement Diagnostics() => _core.Call("diagnostics.get");

    // ---- settings ---------------------------------------------------------------------
    public JsonElement SettingsGet() => _core.Call("settings.get");
    public void SettingsSet(string key, string value) => _core.Call("settings.set", new { key, value });

    // ---- update -----------------------------------------------------------------------
    public JsonElement UpdateStatus() => _core.Call("update.status");
    public void UpdateCheck() => _core.Call("update.check"); // up to 30 s
    public void UpdateDownload() => _core.Call("update.download");
    public void UpdateConfigAuto(bool auto) => _core.Call("update.config", new { auto });
}
