using ClipBridge.Windows.Logic;
using Drawing = System.Drawing;
using WinForms = System.Windows.Forms;

namespace ClipBridge.Windows.Services;

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
    private readonly WinForms.ToolStripMenuItem _syncItem;
    private readonly WinForms.ToolStripMenuItem _autoStartItem;
    private readonly WinForms.ToolStripMenuItem _pauseItem;
    private readonly WinForms.ToolStripMenuItem _resumeItem;
    private readonly WinForms.ToolStripMenuItem _undoItem;
    private readonly Dictionary<TrayState, Drawing.Icon> _icons = new();
    private readonly List<IntPtr> _iconHandles = new();

    private Action? _balloonAction;
    private DateTime _balloonShownAt = DateTime.MinValue;
    private bool _disposed;

    public event Action? OpenRequested;
    public event Action? PairRequested;
    public event Action<bool>? SyncToggled;
    public event Action? SendNowRequested;
    /// <summary>Minutes to pause sync for; 0 = resume now.</summary>
    public event Action<int>? PauseRequested;
    public event Action? UndoRequested;
    public event Action<bool>? AutoStartToggled;
    public event Action? CheckUpdateRequested;
    public event Action? ExitRequested;

    public TrayService()
    {
        var size = Math.Max(16, WinForms.SystemInformation.SmallIconSize.Width);
        foreach (var state in Enum.GetValues<TrayState>()) _icons[state] = Render(size, state);

        _menu.Items.Add(Item(Strings.TrayOpen, () => OpenRequested?.Invoke()));
        _menu.Items.Add(Item(Strings.TrayPair, () => PairRequested?.Invoke()));
        _menu.Items.Add(new WinForms.ToolStripSeparator());
        _syncItem = new WinForms.ToolStripMenuItem(Strings.TraySync) { CheckOnClick = true };
        _syncItem.Click += (_, _) => SyncToggled?.Invoke(_syncItem.Checked);
        _menu.Items.Add(_syncItem);
        _pauseItem = new WinForms.ToolStripMenuItem(Strings.TrayPause);
        foreach (var minutes in new[] { 15, 60, 240 })
        {
            var m = minutes;
            var label = m == 15 ? Strings.TrayPause15 : m == 60 ? Strings.TrayPause60 : Strings.TrayPause240;
            _pauseItem.DropDownItems.Add(Item(label, () => PauseRequested?.Invoke(m)));
        }
        _menu.Items.Add(_pauseItem);
        _resumeItem = Item("", () => PauseRequested?.Invoke(0));
        _resumeItem.Visible = false;
        _menu.Items.Add(_resumeItem);
        _undoItem = Item(Strings.TrayUndo, () => UndoRequested?.Invoke());
        _undoItem.Enabled = false;
        _menu.Items.Add(_undoItem);
        _menu.Items.Add(Item(Strings.TraySendNow, () => SendNowRequested?.Invoke()));
        _autoStartItem = new WinForms.ToolStripMenuItem(Strings.TrayAutoStart) { CheckOnClick = true };
        _autoStartItem.Click += (_, _) => AutoStartToggled?.Invoke(_autoStartItem.Checked);
        _menu.Items.Add(_autoStartItem);
        _menu.Items.Add(Item(Strings.TrayCheckUpdate, () => CheckUpdateRequested?.Invoke()));
        _menu.Items.Add(new WinForms.ToolStripSeparator());
        _menu.Items.Add(Item(Strings.TrayExit, () => ExitRequested?.Invoke()));

        _icon = new WinForms.NotifyIcon
        {
            Icon = _icons[TrayState.Normal],
            Text = Strings.TrayTipNoPeers,
            ContextMenuStrip = _menu,
            Visible = true,
        };
        _icon.MouseClick += (_, e) =>
        {
            if (e.Button == WinForms.MouseButtons.Left) OpenRequested?.Invoke();
        };
        _icon.BalloonTipClicked += OnBalloonClicked;
    }

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

    public void SetSyncChecked(bool on) => _syncItem.Checked = on;
    public void SetAutoStartChecked(bool on) => _autoStartItem.Checked = on;

    /// <summary>Shows "resume" instead of "pause" while sync is paused; <paramref name="until"/> is local time, null = not paused.</summary>
    public void SetPaused(DateTime? until)
    {
        _pauseItem.Visible = until is null;
        _resumeItem.Visible = until is not null;
        if (until is { } u) _resumeItem.Text = Strings.TrayResume(u.ToString("HH:mm"));
    }

    public void SetUndoAvailable(bool available) => _undoItem.Enabled = available;

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
            var body = state switch
            {
                TrayState.Normal => Drawing.Color.FromArgb(0x2E, 0x9E, 0x4F),
                TrayState.NoConnection => Drawing.Color.FromArgb(0x8A, 0x8F, 0x98),
                _ => Drawing.Color.FromArgb(0xE0, 0x9B, 0x00),
            };
            using (var bodyBrush = new Drawing.SolidBrush(body))
            using (var path = RoundedRect(new Drawing.RectangleF(s * 0.14f, s * 0.12f, s * 0.72f, s * 0.82f), s * 0.14f))
                g.FillPath(bodyBrush, path);
            g.FillRectangle(Drawing.Brushes.White, new Drawing.RectangleF(s * 0.26f, s * 0.30f, s * 0.48f, s * 0.54f));
            using (var clipBrush = new Drawing.SolidBrush(Drawing.Color.FromArgb(0x33, 0x3A, 0x45)))
            using (var path = RoundedRect(new Drawing.RectangleF(s * 0.32f, s * 0.03f, s * 0.36f, s * 0.20f), s * 0.06f))
                g.FillPath(clipBrush, path);
            using var line = new Drawing.Pen(body, Math.Max(1f, s * 0.06f));
            g.DrawLine(line, s * 0.34f, s * 0.45f, s * 0.66f, s * 0.45f);
            g.DrawLine(line, s * 0.34f, s * 0.60f, s * 0.66f, s * 0.60f);
            g.DrawLine(line, s * 0.34f, s * 0.75f, s * 0.54f, s * 0.75f);
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
