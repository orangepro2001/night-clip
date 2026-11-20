using MoonPassword.Windows.Logic;
using Drawing = System.Drawing;
using WinForms = System.Windows.Forms;

namespace MoonPassword.Windows.Services;

/// <summary>
/// The notification-area icon, its menu and balloon notifications. UI thread only. The icons are drawn
/// at run time (no binary assets). <see cref="Dispose"/> hides and disposes the NotifyIcon; skipping it
/// would leave a ghost icon in the tray until the mouse moves over it.
/// </summary>
internal sealed class TrayService : IDisposable
{
    private static readonly TimeSpan BalloonClickValidity = TimeSpan.FromSeconds(30);

    private readonly WinForms.NotifyIcon _icon;
    private readonly WinForms.ContextMenuStrip _menu = new();
    private readonly WinForms.ToolStripMenuItem _autoStartItem;
    private readonly WinForms.ToolStripMenuItem _lockVaultItem;
    private readonly Dictionary<TrayState, Drawing.Icon> _icons = new();
    private readonly List<IntPtr> _iconHandles = new();

    private readonly TrayMenuRenderer _renderer = TrayMenuRenderer.Create();
    private Action? _balloonAction;
    private DateTime _balloonShownAt = DateTime.MinValue;
    private bool _disposed;

    public event Action? OpenRequested;
    public event Action? LockVaultRequested;
    public event Action<bool>? AutoStartToggled;
    public event Action? CheckUpdateRequested;
    public event Action? ExitRequested;

    public TrayService()
    {
        var size = Math.Max(16, WinForms.SystemInformation.SmallIconSize.Width);
        foreach (var state in Enum.GetValues<TrayState>()) _icons[state] = Render(size, state);

        _menu.Items.Add(Item(Strings.TrayOpen, () => OpenRequested?.Invoke()));
        _lockVaultItem = Item(Strings.TrayLockVault, () => LockVaultRequested?.Invoke());
        _menu.Items.Add(_lockVaultItem);
        _menu.Items.Add(new WinForms.ToolStripSeparator());
        _autoStartItem = new WinForms.ToolStripMenuItem(Strings.TrayAutoStart) { CheckOnClick = true };
        _autoStartItem.Click += (_, _) => AutoStartToggled?.Invoke(_autoStartItem.Checked);
        _menu.Items.Add(_autoStartItem);
        _menu.Items.Add(Item(Strings.TrayCheckUpdate, () => CheckUpdateRequested?.Invoke()));
        _menu.Items.Add(new WinForms.ToolStripSeparator());
        _menu.Items.Add(Item(Strings.TrayExit, () => ExitRequested?.Invoke()));
        StyleMenu();

        _icon = new WinForms.NotifyIcon
        {
            Icon = _icons[TrayState.Normal],
            Text = Strings.AppName,
            ContextMenuStrip = _menu,
            Visible = true,
        };
        _icon.MouseClick += (_, e) =>
        {
            if (e.Button == WinForms.MouseButtons.Left) OpenRequested?.Invoke();
        };
        _icon.BalloonTipClicked += OnBalloonClicked;
    }

    /// <summary>The flat Lumen-like look (TrayMenuRenderer): roomier rows, the app's light / dark and accent, rounded corners.</summary>
    private void StyleMenu()
    {
        _menu.Renderer = _renderer;
        _menu.Padding = new WinForms.Padding(2, 6, 2, 6);
        foreach (var item in _menu.Items.OfType<WinForms.ToolStripMenuItem>())
            item.Padding = new WinForms.Padding(4, 5, 12, 5);
        _menu.Opening += (_, _) => ApplyLook(_menu);
    }

    private void ApplyLook(WinForms.ToolStripDropDown dropDown)
    {
        var accent = Lumen.UI.Services.ThemeManager.Accent;
        _renderer.Apply(Lumen.UI.Services.ThemeManager.IsDark, Drawing.Color.FromArgb(accent.A, accent.R, accent.G, accent.B));
        TrayMenuRenderer.RoundCorners(dropDown.Handle);
        dropDown.Invalidate();
    }

    /// <summary>Development snapshots: opens the menu at a screen point and returns its bounds (screen pixels).</summary>
    internal Drawing.Rectangle ShowMenuForSnapshot(Drawing.Point at)
    {
        _menu.Show(at);
        return _menu.Bounds;
    }

    internal void HideMenu() => _menu.Close();

    /// <summary>Development snapshots: paints the open menu into a bitmap of its size.</summary>
    internal void DrawMenu(Drawing.Bitmap target) => _menu.DrawToBitmap(target, new Drawing.Rectangle(0, 0, target.Width, target.Height));

    private static WinForms.ToolStripMenuItem Item(string text, Action onClick)
    {
        var item = new WinForms.ToolStripMenuItem(text);
        item.Click += (_, _) => onClick();
        return item;
    }

    public void SetState(TrayState state, string tooltip)
    {
        if (_disposed) return;
        _icon.Icon = _icons[state];
        _icon.Text = tooltip.Length <= 63 ? tooltip : tooltip[..63];
    }

    public void SetAutoStartChecked(bool on) => _autoStartItem.Checked = on;

    /// <summary>"锁定密码库" is offered only while the vault is unlocked.</summary>
    public void SetVaultUnlocked(bool unlocked) => _lockVaultItem.Enabled = unlocked;

    /// <summary>
    /// Shows a balloon. <paramref name="onClick"/> runs if the user clicks it within 30 seconds; a
    /// later click (e.g. from the notification centre) just opens the main window.
    /// </summary>
    public void ShowBalloon(string title, string text, Action? onClick = null)
    {
        if (_disposed) return;
        _balloonAction = onClick;
        _balloonShownAt = DateTime.UtcNow;
        _icon.ShowBalloonTip(5000, Limit(title, 63), Limit(text, 255), WinForms.ToolTipIcon.Info);
    }

    private void OnBalloonClicked(object? sender, EventArgs e)
    {
        var action = _balloonAction;
        var fresh = DateTime.UtcNow - _balloonShownAt <= BalloonClickValidity;
        _balloonAction = null;
        try
        {
            if (action is not null && fresh) action();
            else OpenRequested?.Invoke();
        }
        catch (Exception ex) { AppLog.Error("balloon click", ex); }
    }

    private static string Limit(string s, int max) => s.Length <= max ? s : s[..(max - 1)] + "…";

    // ---- icon drawing ----------------------------------------------------------------

    private Drawing.Icon Render(int size, TrayState state)
    {
        using var bmp = new Drawing.Bitmap(size, size, Drawing.Imaging.PixelFormat.Format32bppArgb);
        using (var g = Drawing.Graphics.FromImage(bmp))
        {
            g.SmoothingMode = Drawing.Drawing2D.SmoothingMode.AntiAlias;
            g.Clear(Drawing.Color.Transparent);
            float s = size;
            // the brand tile (amber while something needs attention) with the moon mark
            var top = state == TrayState.Normal ? Drawing.ColorTranslator.FromHtml(BrandMark.BackgroundTop) : Drawing.Color.FromArgb(0xE0, 0x9B, 0x00);
            var bottom = state == TrayState.Normal ? Drawing.ColorTranslator.FromHtml(BrandMark.BackgroundBottom) : Drawing.Color.FromArgb(0xB4, 0x74, 0x00);
            var tile = new Drawing.RectangleF(0, 0, s, s);
            using (var bodyBrush = new Drawing.Drawing2D.LinearGradientBrush(tile, top, bottom, Drawing.Drawing2D.LinearGradientMode.Vertical))
            using (var path = RoundedRect(new Drawing.RectangleF(s * 0.02f, s * 0.02f, s * 0.96f, s * 0.96f), s * 0.22f))
                g.FillPath(bodyBrush, path);
            var k = s * 0.84f / 24f;
            var o = s * 0.08f;
            using var mark = new Drawing.Drawing2D.GraphicsPath(Drawing.Drawing2D.FillMode.Alternate);
            foreach (var poly in new[] { BrandMark.Crescent, BrandMark.Keyhole })
            {
                var pts = new Drawing.PointF[poly.Length / 2];
                for (var i = 0; i < pts.Length; i++) pts[i] = new Drawing.PointF(poly[2 * i] * k + o, poly[2 * i + 1] * k + o);
                mark.AddPolygon(pts);
            }
            using var moon = new Drawing.SolidBrush(Drawing.ColorTranslator.FromHtml(BrandMark.Moon));
            g.FillPath(moon, mark);
        }
        var handle = bmp.GetHicon();
        _iconHandles.Add(handle); // kept alive for the icon's lifetime, destroyed in Dispose
        return Drawing.Icon.FromHandle(handle);
    }

    private static Drawing.Drawing2D.GraphicsPath RoundedRect(Drawing.RectangleF r, float radius)
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

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _icon.Visible = false; // removes the icon from the notification area immediately
        _icon.Dispose();
        _menu.Dispose();
        foreach (var icon in _icons.Values) icon.Dispose();
        foreach (var h in _iconHandles) NativeMethods.DestroyIcon(h);
        _iconHandles.Clear();
    }
}
