using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// A received image (already saved in the download folder): a preview, "复制到剪贴板" (the full image as a
/// bitmap) and "保存到本地" (shows the saved file selected in Explorer and closes). At most a few at once.
/// </summary>
public sealed class ImagePreviewWindow : Window
{
    private const int MaxOpen = 3;
    private const int PreviewPixels = 1280; // decode width: enough for the window, bounded memory
    private static int _open;

    /// <summary>Opens a preview unless too many are open already; false = the caller should notify instead.</summary>
    internal static bool TryOpen(string path, string name, string from)
    {
        if (_open >= MaxOpen || !File.Exists(path)) return false;
        _open++;
        var w = new ImagePreviewWindow(path, name, from);
        w.Closed += (_, _) => _open--;
        w.Show();
        w.Activate();
        if (MainWindow.Instance is { IsVisible: true } m) TaskbarFlash.Flash(m);
        return true;
    }

    private readonly ShellController _shell = ShellController.Current;
    private readonly string _path;
    private readonly TextBlock _status = new() { TextWrapping = TextWrapping.Wrap, Margin = new Thickness(0, 8, 0, 0) };

    private ImagePreviewWindow(string path, string name, string from)
    {
        _path = path;
        Title = Strings.ImagePreviewTitle(from) + " — " + FileDisplay.Shorten(name, 40);
        Width = 680;
        Height = 600;
        MinWidth = 420;
        MinHeight = 360;
        WindowStartupLocation = WindowStartupLocation.CenterScreen;

        var image = new Image { Stretch = Stretch.Uniform, StretchDirection = StretchDirection.DownOnly };
        try { image.Source = Load(path, PreviewPixels); }
        catch (Exception ex) when (ex is IOException or NotSupportedException or ArgumentException or InvalidOperationException
                                       or UnauthorizedAccessException or COMException or FileFormatException)
        {
            AppLog.Error("image preview", ex);
            _status.Text = Strings.ImageLoadFailed;
        }

        var copy = new Button { Content = Strings.BtnCopyImage, Margin = new Thickness(0, 0, 8, 0), IsDefault = true };
        copy.Click += (_, _) => Copy();
        var save = new Button { Content = Strings.BtnSaveLocal, ToolTip = Strings.BtnSaveLocalTip, Margin = new Thickness(0, 0, 8, 0) };
        save.Click += (_, _) =>
        {
            if (_shell.OpenContainingFolder(_path) is { } error) _status.Text = error;
            else Close();
        };
        var close = new Button { Content = Strings.Close, IsCancel = true };
        close.Click += (_, _) => Close();

        var bottom = new StackPanel { Margin = new Thickness(0, 12, 0, 0) };
        bottom.Children.Add(new TextBlock { Text = Strings.ImageSavedAt(path), TextWrapping = TextWrapping.Wrap, Opacity = 0.7 });
        var buttons = new StackPanel { Orientation = Orientation.Horizontal, Margin = new Thickness(0, 10, 0, 0) };
        buttons.Children.Add(copy);
        buttons.Children.Add(save);
        buttons.Children.Add(close);
        bottom.Children.Add(buttons);
        bottom.Children.Add(_status);

        var root = new DockPanel { Margin = new Thickness(16) };
        DockPanel.SetDock(bottom, Dock.Bottom);
        root.Children.Add(bottom);
        root.Children.Add(new Border { Child = image, Background = new SolidColorBrush(Color.FromArgb(16, 128, 128, 128)) });
        Content = root;
    }

    /// <summary>Decodes into memory (no file lock left behind); <paramref name="maxWidth"/> 0 = full size.</summary>
    private static BitmapSource Load(string path, int maxWidth)
    {
        var bmp = new BitmapImage();
        bmp.BeginInit();
        bmp.CacheOption = BitmapCacheOption.OnLoad;
        bmp.CreateOptions = BitmapCreateOptions.IgnoreColorProfile;
        bmp.UriSource = new Uri(path);
        if (maxWidth > 0) bmp.DecodePixelWidth = maxWidth;
        bmp.EndInit();
        if (maxWidth > 0 && bmp.PixelWidth < maxWidth)
        {
            // Smaller than the decode width: decode again at its own size instead of scaling it up.
            bmp = new BitmapImage();
            bmp.BeginInit();
            bmp.CacheOption = BitmapCacheOption.OnLoad;
            bmp.CreateOptions = BitmapCreateOptions.IgnoreColorProfile;
            bmp.UriSource = new Uri(path);
            bmp.EndInit();
        }
        bmp.Freeze();
        return bmp;
    }

    /// <summary>The full-size image as a bitmap on the clipboard (the monitor only reads text, so it is not synced on).</summary>
    private void Copy()
    {
        try
        {
            var full = Load(_path, 0);
            for (var attempt = 0; ; attempt++)
            {
                try
                {
                    Clipboard.SetImage(full);
                    break;
                }
                catch (COMException) when (attempt < 4)
                {
                    Thread.Sleep(60); // another program holds the clipboard for a moment
                }
            }
            _status.Text = Strings.ImageCopied;
        }
        catch (Exception ex) when (ex is IOException or NotSupportedException or ArgumentException or InvalidOperationException
                                       or UnauthorizedAccessException or COMException or FileFormatException or ExternalException)
        {
            AppLog.Error("image copy", ex);
            _status.Text = Strings.ImageCopyFailed;
        }
    }
}
