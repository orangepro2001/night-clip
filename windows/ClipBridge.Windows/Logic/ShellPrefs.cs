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

    /// <summary>Show tray/Notification Center messages (default on). Off: received items still reach the inbox, dialogs still open.</summary>
    public bool Notifications { get; set; } = true;

    /// <summary>Exclude the window from screenshots and recordings while the vault page is shown (default on).</summary>
    public bool PreventCapture { get; set; } = true;

    /// <summary>The panes of the notes editor (source / split / preview), remembered between runs.</summary>
    public NotesViewMode NotesView { get; set; } = NotesViewModes.Default;

    /// <summary>Appearance of the window: "system" (follow Windows, the default), "light" or "dark".</summary>
    public string Theme { get; set; } = "system";

    /// <summary>Accent colour as #RRGGBB, or null for the shell's default.</summary>
    public string? Accent { get; set; }

    /// <summary>Whether the sidebar of the main window is expanded (default) or collapsed to icons.</summary>
    public bool SidebarOpen { get; set; } = true;

    /// <summary>Balloons for SSH key use: "first" (the first use per program and key, the default), "all" or "off".</summary>
    public string SshNotify { get; set; } = "first";

    public static string NormalizeSshNotify(string? value) => value is "all" or "off" ? value : "first";

    /// <summary>The core.sshCommand value this app set for Git, so "undo" only removes our own change.</summary>
    public string? GitSshCommandSet { get; set; }

    /// <summary>"system", "light" or "dark"; anything else reads as "system".</summary>
    public static string NormalizeTheme(string? value) => value is "light" or "dark" ? value : "system";

    /// <summary>A #RRGGBB colour, or null when <paramref name="value"/> is not one.</summary>
    public static string? NormalizeAccent(string? value)
    {
        if (value is not { Length: 7 } || value[0] != '#') return null;
        for (var i = 1; i < 7; i++)
            if (!Uri.IsHexDigit(value[i])) return null;
        return value.ToUpperInvariant();
    }

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
                Notifications = JsonRead.Bool(doc.RootElement, "notifications", true),
                NotesView = NotesViewModes.Parse(JsonRead.Str(doc.RootElement, "notes_view")),
                Theme = NormalizeTheme(JsonRead.Str(doc.RootElement, "theme")),
                Accent = NormalizeAccent(JsonRead.Str(doc.RootElement, "accent")),
                SidebarOpen = JsonRead.Bool(doc.RootElement, "sidebar_open", true),
                SshNotify = NormalizeSshNotify(JsonRead.Str(doc.RootElement, "ssh_notify")),
                GitSshCommandSet = JsonRead.Str(doc.RootElement, "git_ssh_command_set") is { Length: > 0 } g ? g : null,
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
                ["notifications"] = Notifications,
                ["notes_view"] = NotesViewModes.Key(NotesView),
                ["theme"] = NormalizeTheme(Theme),
                ["accent"] = NormalizeAccent(Accent),
                ["sidebar_open"] = SidebarOpen,
                ["ssh_notify"] = NormalizeSshNotify(SshNotify),
                ["git_ssh_command_set"] = GitSshCommandSet,
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
