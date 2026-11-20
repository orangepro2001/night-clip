using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Media.Imaging;
using Lumen.UI.Controls;

namespace ClipBridge.Windows.Views;

/// <summary>
/// A compact Lumen-styled window for things that must appear even while the main window is hidden in the tray
/// (incoming pairing and file requests, received images). Content sits in its own DialogHost and ToastHost,
/// so confirmations and toasts stay in this window. Esc closes it.
/// </summary>
internal class SmallWindow : Window
{
    private readonly Panel _root = new();

    protected SmallWindow(double width, bool resizable = false)
    {
        Width = width;
        SizeToContent = resizable ? SizeToContent.Manual : SizeToContent.Height;
        CanResize = resizable;
        WindowStartupLocation = WindowStartupLocation.CenterScreen;
        Icon = AppIconImage.Create();
        Content = new DialogHost { Content = _root };
        KeyDown += (_, e) =>
        {
            if (e.Key == Key.Escape && !e.Handled)
            {
                e.Handled = true;
                Close();
            }
        };
    }

    /// <summary>Places the body (with the standard 24 px padding) and a toast host over it.</summary>
    protected void SetBody(Control body, Thickness? padding = null)
    {
        _root.Children.Clear();
        _root.Children.Add(new Border { Padding = padding ?? new Thickness(24, 22), Child = body });
        _root.Children.Add(new ToastHost());
    }

    /// <summary>Centres on the main window when it is visible, otherwise on the screen.</summary>
    protected void ShowNearMain()
    {
        if (MainWindow.Instance is { IsVisible: true } owner)
        {
            WindowStartupLocation = WindowStartupLocation.CenterOwner;
            Show(owner);
        }
        else Show();
        Activate();
    }
}

internal static class BitmapExtensions
{
    /// <summary>A BGRA32 pixel buffer as an Avalonia bitmap (premultiplied alpha: the QR code is opaque).</summary>
    public static WriteableBitmap ToAvalonia(this ClipBridge.Windows.Logic.QrRenderer.Bitmap source)
    {
        var bitmap = new WriteableBitmap(new PixelSize(source.Width, source.Height), new Vector(96, 96),
            Avalonia.Platform.PixelFormat.Bgra8888, Avalonia.Platform.AlphaFormat.Premul);
        using var frame = bitmap.Lock();
        var stride = source.Width * 4;
        for (var y = 0; y < source.Height; y++)
            System.Runtime.InteropServices.Marshal.Copy(source.Bgra32, y * stride, frame.Address + y * frame.RowBytes, stride);
        return bitmap;
    }
}
