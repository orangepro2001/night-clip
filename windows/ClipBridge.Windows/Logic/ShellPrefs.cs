using System.Text.Json;

namespace ClipBridge.Windows.Logic;

/// <summary>
/// Small shell-side preferences in <c>shell.json</c>: the "first run" flag (the core cannot tell an
/// unset <c>capture</c> from an explicit "off") and a fallback download folder for cores that do not
/// know the <c>download_dir</c> setting yet. Corrupt or missing files simply mean defaults.
/// </summary>
public sealed class ShellPrefs
{
    public const string FileName = "shell.json";

    public bool FirstRunDone { get; set; }
    public string? DownloadDir { get; set; }

    /// <summary>Exclude the window from screenshots and recordings while the vault page is shown (default on).</summary>
    public bool PreventCapture { get; set; } = true;

    public static ShellPrefs Load(string dataDir)
    {
        try
        {
            var path = Path.Combine(dataDir, FileName);
            if (!File.Exists(path)) return new ShellPrefs();
            using var doc = JsonDocument.Parse(File.ReadAllText(path));
            return new ShellPrefs
            {
                FirstRunDone = JsonRead.Bool(doc.RootElement, "first_run_done"),
                DownloadDir = JsonRead.Str(doc.RootElement, "download_dir") is { Length: > 0 } d ? d : null,
                PreventCapture = JsonRead.Bool(doc.RootElement, "prevent_capture", true),
            };
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or JsonException)
        {
            return new ShellPrefs();
        }
    }

    public void Save(string dataDir)
    {
        try
        {
            Directory.CreateDirectory(dataDir);
            var json = JsonSerializer.Serialize(new Dictionary<string, object?>
            {
                ["first_run_done"] = FirstRunDone,
                ["download_dir"] = DownloadDir,
                ["prevent_capture"] = PreventCapture,
            });
            var path = Path.Combine(dataDir, FileName);
            var tmp = path + ".tmp";
            File.WriteAllText(tmp, json);
            File.Move(tmp, path, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { /* preferences are best effort */ }
    }

    /// <summary>Order of preference: the core's setting, then our fallback, then the system default.</summary>
    public static string ResolveDownloadDir(string? coreValue, string? prefsValue, string systemDefault)
    {
        if (SettingsModel.IsAbsoluteWindowsPath(coreValue)) return coreValue!;
        if (SettingsModel.IsAbsoluteWindowsPath(prefsValue)) return prefsValue!;
        return systemDefault;
    }
}
