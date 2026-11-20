using Avalonia.Media;
using MoonPassword.Windows.Logic;
using Lumen.UI.Services;

namespace MoonPassword.Windows.Views;

/// <summary>Theme mode and accent colour: applied from shell.json at start, changed and saved from Settings.</summary>
internal static class Appearance
{
    /// <summary>The shell's own default accent: a clear blue (Lumen's "Blue" preset).</summary>
    public const string DefaultAccent = "#2563EB";

    /// <summary>Accent choices offered in Settings: (label, #RRGGBB).</summary>
    public static readonly IReadOnlyList<(string Name, string Hex)> Accents =
    [
        ("蓝", "#2563EB"), ("靛", "#4F46E5"), ("天蓝", "#0284C7"), ("青", "#0D9488"),
        ("绿", "#059669"), ("琥珀", "#D97706"), ("玫红", "#E11D48"), ("紫", "#7C3AED"), ("石板灰", "#475569"),
    ];

    public static void Apply(ShellPrefs prefs)
    {
        ThemeManager.SetMode(ToMode(prefs.Theme));
        ThemeManager.SetAccent(Color.Parse(prefs.Accent ?? DefaultAccent));
    }

    public static ThemeMode ToMode(string theme) => theme switch
    {
        "light" => ThemeMode.Light,
        "dark" => ThemeMode.Dark,
        _ => ThemeMode.System,
    };

    public static string FromMode(ThemeMode mode) => mode switch
    {
        ThemeMode.Light => "light",
        ThemeMode.Dark => "dark",
        _ => "system",
    };

    public static string CurrentAccentHex(ShellPrefs prefs) => prefs.Accent ?? DefaultAccent;
}
