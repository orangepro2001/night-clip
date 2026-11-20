using System.Runtime.InteropServices;
using Drawing = System.Drawing;
using WinForms = System.Windows.Forms;

namespace ClipBridge.Windows.Services;

/// <summary>
/// A flat look for the tray menu that matches the Lumen windows: neutral surfaces, a soft hover, thin separators, the
/// accent colour for check marks, light or dark like the app. On Windows 11 the menu also gets rounded corners.
/// </summary>
internal sealed class TrayMenuRenderer : WinForms.ToolStripProfessionalRenderer
{
    private readonly Palette _palette;

    private TrayMenuRenderer(Palette palette) : base(palette)
    {
        _palette = palette;
        RoundedEdges = false;
    }

    public static TrayMenuRenderer Create() => new(new Palette());

    /// <summary>Follow the app's light / dark appearance and accent (called each time the menu opens).</summary>
    public void Apply(bool dark, Drawing.Color accent)
    {
        _palette.Dark = dark;
        _palette.Accent = accent;
    }

    protected override void OnRenderItemText(WinForms.ToolStripItemTextRenderEventArgs e)
    {
        e.TextColor = e.Item.Enabled ? _palette.Text : _palette.TextDisabled;
        base.OnRenderItemText(e);
    }

    protected override void OnRenderArrow(WinForms.ToolStripArrowRenderEventArgs e)
    {
        e.ArrowColor = _palette.TextMuted;
        base.OnRenderArrow(e);
    }

    protected override void OnRenderItemCheck(WinForms.ToolStripItemImageRenderEventArgs e)
    {
        // A plain accent tick instead of the boxed check of the classic renderer.
        var g = e.Graphics;
        g.SmoothingMode = Drawing.Drawing2D.SmoothingMode.AntiAlias;
        var r = e.ImageRectangle;
        var size = Math.Min(r.Width, r.Height);
        var x = r.X + (r.Width - size) / 2f;
        var y = r.Y + (r.Height - size) / 2f;
        using var pen = new Drawing.Pen(_palette.Accent, Math.Max(1.6f, size / 8f)) { StartCap = Drawing.Drawing2D.LineCap.Round, EndCap = Drawing.Drawing2D.LineCap.Round, LineJoin = Drawing.Drawing2D.LineJoin.Round };
        g.DrawLines(pen, new[]
        {
            new Drawing.PointF(x + size * 0.2f, y + size * 0.52f),
            new Drawing.PointF(x + size * 0.42f, y + size * 0.74f),
            new Drawing.PointF(x + size * 0.82f, y + size * 0.3f),
        });
    }

    protected override void OnRenderMenuItemBackground(WinForms.ToolStripItemRenderEventArgs e)
    {
        if (!e.Item.Selected || !e.Item.Enabled) return;
        var g = e.Graphics;
        g.SmoothingMode = Drawing.Drawing2D.SmoothingMode.AntiAlias;
        var r = new Drawing.Rectangle(4, 1, e.Item.Width - 8, e.Item.Height - 2);
        using var brush = new Drawing.SolidBrush(_palette.Hover);
        using var path = Rounded(r, 6);
        g.FillPath(brush, path);
    }

    protected override void OnRenderSeparator(WinForms.ToolStripSeparatorRenderEventArgs e)
    {
        var y = e.Item.Height / 2;
        using var pen = new Drawing.Pen(_palette.Separator);
        e.Graphics.DrawLine(pen, 10, y, e.Item.Width - 10, y);
    }

    private static Drawing.Drawing2D.GraphicsPath Rounded(Drawing.Rectangle r, int radius)
    {
        var d = radius * 2;
        var p = new Drawing.Drawing2D.GraphicsPath();
        p.AddArc(r.X, r.Y, d, d, 180, 90);
        p.AddArc(r.Right - d, r.Y, d, d, 270, 90);
        p.AddArc(r.Right - d, r.Bottom - d, d, d, 0, 90);
        p.AddArc(r.X, r.Bottom - d, d, d, 90, 90);
        p.CloseFigure();
        return p;
    }

    /// <summary>Asks Windows 11 for rounded corners on a menu window (ignored on Windows 10).</summary>
    public static void RoundCorners(IntPtr handle)
    {
        if (handle == IntPtr.Zero || !OperatingSystem.IsWindowsVersionAtLeast(10, 0, 22000)) return;
        var preference = DWMWCP_ROUNDSMALL;
        DwmSetWindowAttribute(handle, DWMWA_WINDOW_CORNER_PREFERENCE, ref preference, sizeof(int));
    }

    private const int DWMWA_WINDOW_CORNER_PREFERENCE = 33;
    private const int DWMWCP_ROUNDSMALL = 3;

    [DllImport("dwmapi.dll")]
    private static extern int DwmSetWindowAttribute(IntPtr hwnd, int attribute, ref int value, int size);

    /// <summary>The colours, swapped between light and dark (Lumen's neutral tokens).</summary>
    private sealed class Palette : WinForms.ProfessionalColorTable
    {
        public bool Dark { get; set; }
        public Drawing.Color Accent { get; set; } = Drawing.Color.FromArgb(0x25, 0x63, 0xEB);

        private static Drawing.Color C(int rgb) => Drawing.Color.FromArgb((rgb >> 16) & 0xFF, (rgb >> 8) & 0xFF, rgb & 0xFF);

        public Drawing.Color Surface => Dark ? C(0x1E1E23) : C(0xFFFFFF);
        public Drawing.Color Hover => Dark ? C(0x2D2D34) : C(0xEFEFF1);
        public Drawing.Color Border => Dark ? C(0x36363F) : C(0xE4E4E7);
        public Drawing.Color Separator => Dark ? C(0x2D2D34) : C(0xEFEFF1);
        public Drawing.Color Text => Dark ? C(0xF4F4F5) : C(0x18181B);
        public Drawing.Color TextMuted => Dark ? C(0xA1A1AA) : C(0x52525B);
        public Drawing.Color TextDisabled => Dark ? C(0x5A5A63) : C(0xB4B4BB);

        public override Drawing.Color ToolStripDropDownBackground => Surface;
        public override Drawing.Color ImageMarginGradientBegin => Surface;
        public override Drawing.Color ImageMarginGradientMiddle => Surface;
        public override Drawing.Color ImageMarginGradientEnd => Surface;
        public override Drawing.Color MenuBorder => Border;
        public override Drawing.Color MenuItemBorder => Hover;
        public override Drawing.Color MenuItemSelected => Hover;
        public override Drawing.Color MenuItemSelectedGradientBegin => Hover;
        public override Drawing.Color MenuItemSelectedGradientEnd => Hover;
        public override Drawing.Color SeparatorDark => Separator;
        public override Drawing.Color SeparatorLight => Separator;
        public override Drawing.Color CheckBackground => Surface;
        public override Drawing.Color CheckSelectedBackground => Hover;
        public override Drawing.Color CheckPressedBackground => Hover;
    }
}
