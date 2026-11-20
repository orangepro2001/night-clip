using Lumen.UI.Icons;

namespace MoonPassword.Windows.Views;

/// <summary>
/// Stroke icons Lumen.UI 0.1.0 does not ship (Lucide / Feather shapes on the 24×24 grid), plus every built-in
/// <see cref="IconKind"/> under a "kind:" name so that one string key works for both (the sidebar template binds
/// <c>Icon.NameKey</c>). Registered once at start-up, before any window exists.
/// </summary>
internal static class AppIcons
{
    public const string Smartphone = "smartphone";
    public const string Monitor = "monitor";
    public const string Laptop = "laptop";
    public const string Wifi = "wifi";
    public const string WifiOff = "wifi-off";
    public const string Link = "link";
    public const string Send = "send";
    public const string ArrowLeftRight = "arrow-left-right";
    public const string QrCode = "qr-code";
    public const string Scan = "scan";
    public const string UserPlus = "user-plus";
    public const string Clipboard = "clipboard";
    public const string ClipboardCheck = "clipboard-check";
    public const string Image = "image";
    public const string FolderOpen = "folder-open";
    public const string Paperclip = "paperclip";
    public const string Save = "save";
    public const string BookOpen = "book-open";
    public const string Tag = "tag";
    public const string Key = "key";
    public const string Shield = "shield";
    public const string ShieldCheck = "shield-check";
    public const string Unlock = "unlock";
    public const string EyeOff = "eye-off";
    public const string Shuffle = "shuffle";
    public const string Power = "power";
    public const string Pause = "pause";
    public const string Play = "play";
    public const string RotateCcw = "rotate-ccw";
    public const string History = "history";
    public const string BellOff = "bell-off";
    public const string MoreVertical = "more-vertical";
    public const string Terminal = "terminal";
    public const string CheckSquare = "check-square";
    public const string Bold = "bold";
    public const string Italic = "italic";
    public const string Strikethrough = "strikethrough";
    public const string Heading = "heading";
    public const string List = "list";
    public const string Code = "code";
    public const string Download = "download-arrow";

    private static readonly Dictionary<string, string> Paths = new()
    {
        [Smartphone] = "M7 2 H17 A2 2 0 0 1 19 4 V20 A2 2 0 0 1 17 22 H7 A2 2 0 0 1 5 20 V4 A2 2 0 0 1 7 2 Z M12 18 H12.01",
        [Monitor] = "M4 3 H20 A2 2 0 0 1 22 5 V15 A2 2 0 0 1 20 17 H4 A2 2 0 0 1 2 15 V5 A2 2 0 0 1 4 3 Z M8 21 H16 M12 17 V21",
        [Laptop] = "M20 16 V7 A2 2 0 0 0 18 5 H6 A2 2 0 0 0 4 7 V16 M2.5 16 H21.5 L20.6 19.2 A1 1 0 0 1 19.6 20 H4.4 A1 1 0 0 1 3.4 19.2 Z",
        [Wifi] = "M5 12.55 A11 11 0 0 1 19.08 12.55 M1.42 9 A16 16 0 0 1 22.58 9 M8.53 16.11 A6 6 0 0 1 15.48 16.11 M12 20 H12.01",
        [WifiOff] = "M1 1 L23 23 M16.72 11.06 A10.94 10.94 0 0 1 19 12.55 M5 12.55 A10.94 10.94 0 0 1 10.71 10.24 M10.71 5.05 A16 16 0 0 1 22.58 9 M1.42 9 A15.91 15.91 0 0 1 6.12 6.12 M8.53 16.11 A6 6 0 0 1 15.47 16.11 M12 20 H12.01",
        [Link] = "M10 13 A5 5 0 0 0 17.54 13.54 L20.54 10.54 A5 5 0 0 0 13.47 3.47 L11.75 5.18 M14 11 A5 5 0 0 0 6.46 10.46 L3.46 13.46 A5 5 0 0 0 10.53 20.53 L12.24 18.82",
        [Send] = "M22 2 L11 13 M22 2 L15 22 L11 13 L2 9 Z",
        [ArrowLeftRight] = "M8 3 L4 7 L8 11 M4 7 H20 M16 21 L20 17 L16 13 M20 17 H4",
        [QrCode] = "M4 3 H7 A1 1 0 0 1 8 4 V7 A1 1 0 0 1 7 8 H4 A1 1 0 0 1 3 7 V4 A1 1 0 0 1 4 3 Z M17 3 H20 A1 1 0 0 1 21 4 V7 A1 1 0 0 1 20 8 H17 A1 1 0 0 1 16 7 V4 A1 1 0 0 1 17 3 Z M4 16 H7 A1 1 0 0 1 8 17 V20 A1 1 0 0 1 7 21 H4 A1 1 0 0 1 3 20 V17 A1 1 0 0 1 4 16 Z M21 16 H18 A2 2 0 0 0 16 18 V21 M21 21 V21.01 M12 7 V10 A2 2 0 0 1 10 12 H7 M3 12 H3.01 M12 3 H12.01 M12 16 V16.01 M16 12 H17 M21 12 V12.01 M12 21 V20",
        [Scan] = "M3 7 V5 A2 2 0 0 1 5 3 H7 M17 3 H19 A2 2 0 0 1 21 5 V7 M21 17 V19 A2 2 0 0 1 19 21 H17 M7 21 H5 A2 2 0 0 1 3 19 V17",
        [UserPlus] = "M16 21 V19 A4 4 0 0 0 12 15 H5 A4 4 0 0 0 1 19 V21 M4.5 7 A4 4 0 1 0 12.5 7 A4 4 0 1 0 4.5 7 Z M20 8 V14 M23 11 H17",
        [Clipboard] = "M16 4 H18 A2 2 0 0 1 20 6 V20 A2 2 0 0 1 18 22 H6 A2 2 0 0 1 4 20 V6 A2 2 0 0 1 6 4 H8 M9 2 H15 A1 1 0 0 1 16 3 V5 A1 1 0 0 1 15 6 H9 A1 1 0 0 1 8 5 V3 A1 1 0 0 1 9 2 Z",
        [ClipboardCheck] = "M16 4 H18 A2 2 0 0 1 20 6 V20 A2 2 0 0 1 18 22 H6 A2 2 0 0 1 4 20 V6 A2 2 0 0 1 6 4 H8 M9 2 H15 A1 1 0 0 1 16 3 V5 A1 1 0 0 1 15 6 H9 A1 1 0 0 1 8 5 V3 A1 1 0 0 1 9 2 Z M9 14 L11 16 L15 12",
        [Image] = "M5 3 H19 A2 2 0 0 1 21 5 V19 A2 2 0 0 1 19 21 H5 A2 2 0 0 1 3 19 V5 A2 2 0 0 1 5 3 Z M7 8.5 A1.5 1.5 0 1 0 10 8.5 A1.5 1.5 0 1 0 7 8.5 Z M21 15 L16 10 L5 21",
        [FolderOpen] = "M6 14 L7.5 11.1 A2 2 0 0 1 9.24 10 H20 A2 2 0 0 1 21.94 12.5 L20.4 18.5 A2 2 0 0 1 18.45 20 H4 A2 2 0 0 1 2 18 V5 A2 2 0 0 1 4 3 H7.9 A2 2 0 0 1 9.59 3.9 L10.4 5.1 A2 2 0 0 0 12.07 6 H18 A2 2 0 0 1 20 8 V10",
        [Paperclip] = "M21.44 11.05 L12.25 20.24 A6 6 0 0 1 3.76 11.75 L12.95 2.56 A4 4 0 0 1 18.61 8.22 L9.41 17.41 A2 2 0 0 1 6.58 14.58 L15.07 6.1",
        [Save] = "M19 21 H5 A2 2 0 0 1 3 19 V5 A2 2 0 0 1 5 3 H16 L21 8 V19 A2 2 0 0 1 19 21 Z M17 21 V13 H7 V21 M7 3 V8 H15",
        [BookOpen] = "M2 3 H8 A4 4 0 0 1 12 7 V21 A3 3 0 0 0 9 18 H2 Z M22 3 H16 A4 4 0 0 0 12 7 V21 A3 3 0 0 1 15 18 H22 Z",
        [Tag] = "M20.59 13.41 L13.42 20.58 A2 2 0 0 1 10.59 20.58 L2 12 V2 H12 L20.59 10.59 A2 2 0 0 1 20.59 13.41 Z M7 7 H7.01",
        [Key] = "M2 15.5 A5.5 5.5 0 1 0 13 15.5 A5.5 5.5 0 1 0 2 15.5 Z M11.4 11.6 L21 2 M15.5 7.5 L18.5 10.5 L22 7 L19 4",
        [Shield] = "M12 22 C12 22 20 18 20 12 V5 L12 2 L4 5 V12 C4 18 12 22 12 22 Z",
        [ShieldCheck] = "M12 22 C12 22 20 18 20 12 V5 L12 2 L4 5 V12 C4 18 12 22 12 22 Z M9 12 L11 14 L15 10",
        [Unlock] = "M5 11 H19 A2 2 0 0 1 21 13 V20 A2 2 0 0 1 19 22 H5 A2 2 0 0 1 3 20 V13 A2 2 0 0 1 5 11 Z M7 11 V7 A5 5 0 0 1 16.9 6",
        [EyeOff] = "M17.94 17.94 A10.07 10.07 0 0 1 12 20 C5 20 1 12 1 12 A18.45 18.45 0 0 1 6.06 6.06 M9.9 4.24 A9.12 9.12 0 0 1 12 4 C19 4 23 12 23 12 A18.5 18.5 0 0 1 20.84 15.19 M14.12 14.12 A3 3 0 1 1 9.88 9.88 M1 1 L23 23",
        [Shuffle] = "M16 3 H21 V8 M4 20 L21 3 M21 16 V21 H16 M15 15 L21 21 M4 4 L9 9",
        [Power] = "M18.36 6.64 A9 9 0 1 1 5.64 6.64 M12 2 V12",
        [Pause] = "M6 4 H10 V20 H6 Z M14 4 H18 V20 H14 Z",
        [Play] = "M5 3 L19 12 L5 21 Z",
        [RotateCcw] = "M1 4 V10 H7 M3.51 15 A9 9 0 1 0 5.64 5.64 L1 10",
        [History] = "M3 12 A9 9 0 1 0 12 3 A9.75 9.75 0 0 0 5.26 5.74 L3 8 M3 3 V8 H8 M12 7 V12 L16 14",
        [BellOff] = "M13.73 21 A2 2 0 0 1 10.27 21 M18.63 13 A17.89 17.89 0 0 1 18 8 M6.26 6.26 A5.86 5.86 0 0 0 6 8 C6 15 3 17 3 17 H17 M18 8 A6 6 0 0 0 9.33 2.67 M1 1 L23 23",
        [MoreVertical] = "M11 12 A1 1 0 1 0 13 12 A1 1 0 1 0 11 12 Z M11 5 A1 1 0 1 0 13 5 A1 1 0 1 0 11 5 Z M11 19 A1 1 0 1 0 13 19 A1 1 0 1 0 11 19 Z",
        [Terminal] = "M4 17 L10 11 L4 5 M12 19 H20",
        [CheckSquare] = "M9 11 L12 14 L22 4 M21 12 V19 A2 2 0 0 1 19 21 H5 A2 2 0 0 1 3 19 V5 A2 2 0 0 1 5 3 H16",
        [Bold] = "M6 4 H14 A4 4 0 0 1 14 12 H6 Z M6 12 H15 A4 4 0 0 1 15 20 H6 Z",
        [Italic] = "M19 4 H10 M14 20 H5 M15 4 L9 20",
        [Strikethrough] = "M16 4 H9 A3 3 0 0 0 6.17 8 M14 12 A4 4 0 0 1 14 20 H6 M4 12 H20",
        [Heading] = "M6 12 H18 M6 20 V4 M18 20 V4",
        [List] = "M8 6 H21 M8 12 H21 M8 18 H21 M3 6 H3.01 M3 12 H3.01 M3 18 H3.01",
        [Code] = "M16 18 L22 12 L16 6 M8 6 L2 12 L8 18",
        [Download] = "M12 3 V15 M7 10 L12 15 L17 10 M5 21 H19",
    };

    /// <summary>The name a built-in icon is registered under.</summary>
    public static string Of(IconKind kind) => "kind:" + kind;

    public static void Register()
    {
        foreach (var (name, path) in Paths) LumenIcons.Register(name, path);
        foreach (var (kind, path) in LumenIcons.PathData)
            if (kind != IconKind.None) LumenIcons.Register(Of(kind), path);
    }
}
