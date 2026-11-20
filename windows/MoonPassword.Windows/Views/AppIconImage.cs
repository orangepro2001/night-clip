using Avalonia;
using Avalonia.Controls;
using Avalonia.Media;
using Avalonia.Media.Imaging;
using MoonPassword.Windows.Logic;

namespace MoonPassword.Windows.Views;

/// <summary>The window / taskbar icon, drawn at run time from <see cref="BrandMark"/> like the tray icon.</summary>
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
                EndPoint = new RelativePoint(0, 1, RelativeUnit.Relative),
                GradientStops =
                {
                    new GradientStop(Color.Parse(BrandMark.BackgroundTop), 0),
                    new GradientStop(Color.Parse(BrandMark.BackgroundBottom), 1),
                },
            };
            ctx.DrawRectangle(background, null, new RoundedRect(new Rect(2, 2, size - 4, size - 4), 14));
            // the 24×24 mark scaled into the middle 44 pixels
            const double scale = 44.0 / 24.0;
            const double offset = (size - 44) / 2.0;
            ctx.DrawGeometry(new SolidColorBrush(Color.Parse(BrandMark.Moon)), null, Mark(scale, offset));
        }
        _icon = new WindowIcon(bitmap);
        return _icon;
    }

    /// <summary>The crescent with the keyhole cut out (even-odd), scaled and moved.</summary>
    internal static Geometry Mark(double scale, double offset)
    {
        var geometry = new StreamGeometry();
        using (var g = geometry.Open())
        {
            g.SetFillRule(FillRule.EvenOdd);
            foreach (var poly in new[] { BrandMark.Crescent, BrandMark.Keyhole })
            {
                g.BeginFigure(new Point(poly[0] * scale + offset, poly[1] * scale + offset), isFilled: true);
                for (var i = 2; i < poly.Length; i += 2)
                    g.LineTo(new Point(poly[i] * scale + offset, poly[i + 1] * scale + offset));
                g.EndFigure(isClosed: true);
            }
        }
        return geometry;
    }
}
