using Avalonia;
using Avalonia.Controls;
using Avalonia.Media;
using Avalonia.Media.Imaging;

namespace ClipBridge.Windows.Views;

/// <summary>The window / taskbar icon, drawn at run time like the tray icon (the repository carries no binary assets).</summary>
internal static class AppIconImage
{
    private static WindowIcon? _icon;

    public static WindowIcon Create()
    {
        if (_icon is not null) return _icon;
        const int size = 64;
        var bitmap = new RenderTargetBitmap(new PixelSize(size, size), new Vector(96, 96));
        using (var ctx = bitmap.CreateDrawingContext())
        {
            var background = new LinearGradientBrush
            {
                StartPoint = new RelativePoint(0, 0, RelativeUnit.Relative),
                EndPoint = new RelativePoint(1, 1, RelativeUnit.Relative),
                GradientStops = { new GradientStop(Color.Parse("#3B82F6"), 0), new GradientStop(Color.Parse("#1D4ED8"), 1) },
            };
            ctx.DrawRectangle(background, null, new RoundedRect(new Rect(2, 2, size - 4, size - 4), 14));
            var pen = new Pen(Brushes.White, 2.2, lineCap: PenLineCap.Round, lineJoin: PenLineJoin.Round);
            var clipboard = Geometry.Parse(
                "M16 4 H18 A2 2 0 0 1 20 6 V20 A2 2 0 0 1 18 22 H6 A2 2 0 0 1 4 20 V6 A2 2 0 0 1 6 4 H8 " +
                "M9 2 H15 A1 1 0 0 1 16 3 V5 A1 1 0 0 1 15 6 H9 A1 1 0 0 1 8 5 V3 A1 1 0 0 1 9 2 Z M8 12 H16 M8 16 H13");
            using (ctx.PushTransform(Matrix.CreateScale(1.75, 1.75) * Matrix.CreateTranslation(11, 11)))
                ctx.DrawGeometry(null, pen, clipboard);
        }
        _icon = new WindowIcon(bitmap);
        return _icon;
    }
}
