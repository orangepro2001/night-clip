using System.Runtime.InteropServices;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Media.Imaging;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;
using Drawing = System.Drawing;
using WinForms = System.Windows.Forms;

namespace ClipBridge.Windows.Views;

/// <summary>
/// A received image: a preview and "复制到剪贴板" (the full image as a bitmap). A <em>held</em> image
/// (ReceivedImages: not saved yet) offers "保存到…" — a save dialog, then Explorer shows the saved file — and
/// is deleted when the window closes without saving. An image saved into a folder the user chose offers
/// "在文件夹中显示". At most a few windows at once.
/// </summary>
internal sealed class ImagePreviewWindow : SmallWindow
{
    private const int MaxOpen = 3;
    private const int PreviewPixels = 1280; // decode width: enough for the window, bounded memory
    private static int _open;

    /// <summary>Opens a preview unless too many are open already; false = the caller should notify instead.</summary>
    internal static bool TryOpen(string path, string name, string from, bool held)
    {
        if (_open >= MaxOpen || !File.Exists(path)) return false;
        _open++;
        var w = new ImagePreviewWindow(path, name, from, held);
        w.Closed += (_, _) => _open--;
        w.Show();
        w.Activate();
        if (MainWindow.Instance is { IsVisible: true } m) TaskbarFlash.Flash(m);
        return true;
    }

    private readonly ShellController _shell = ShellController.Current;
    private readonly string _path;
    private readonly string _name;
    private readonly bool _held;
    private readonly PairDialogs.StatusLine _status = new();

    private ImagePreviewWindow(string path, string name, string from, bool held) : base(720, resizable: true)
    {
        _path = path;
        _name = name;
        _held = held;
        Title = Strings.ImagePreviewTitle(from) + " — " + FileDisplay.Shorten(name, 40);
        Height = 620;
        MinWidth = 440;
        MinHeight = 380;

        var image = new Image { Stretch = Stretch.Uniform, StretchDirection = StretchDirection.DownOnly };
        try { image.Source = Load(path, PreviewPixels); }
        catch (Exception ex) when (ex is IOException or NotSupportedException or ArgumentException or InvalidOperationException
                                       or UnauthorizedAccessException)
        {
            AppLog.Error("image preview", ex);
            _status.Error(Strings.ImageLoadFailed);
        }

        var copy = Ui.Button(AppIcons.Of(IconKind.Copy), Strings.BtnCopyImage, Copy, "primary");
        copy.IsDefault = true;
        var save = held
            ? Ui.Button(AppIcons.Save, Strings.BtnSaveAs, () => _ = SaveAsAsync())
            : Ui.Button(AppIcons.FolderOpen, Strings.BtnShowInFolder, () =>
            {
                if (_shell.OpenContainingFolder(_path) is { } error) _status.Error(error);
                else Close();
            });
        if (!held) ToolTip.SetTip(save, Strings.BtnSaveLocalTip);
        var close = new Button { Content = Strings.Close, IsCancel = true };
        close.Classes.Add("ghost");
        close.Click += (_, _) => Close();

        var header = new StackPanel
        {
            Spacing = 2,
            Children =
            {
                Ui.Text(FileDisplay.Shorten(name, 60), "subtitle"),
                Ui.Text(held ? Strings.ImageNotSavedYet : Strings.ImageSavedAt(path), "caption", "tertiary"),
            },
        };
        var frame = new Border
        {
            CornerRadius = new CornerRadius(12),
            ClipToBounds = true,
            Padding = new Thickness(8),
            Child = image,
        };
        frame[!Border.BackgroundProperty] = frame.GetResourceObservable("Lumen.Brush.SurfaceSubtle").ToBinding();

        var footer = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto,Auto,Auto"), Margin = new Thickness(0, 14, 0, 0) };
        footer.Children.Add(_status);
        Grid.SetColumn(close, 1);
        footer.Children.Add(close);
        save.Margin = new Thickness(8, 0, 0, 0);
        Grid.SetColumn(save, 2);
        footer.Children.Add(save);
        copy.Margin = new Thickness(8, 0, 0, 0);
        Grid.SetColumn(copy, 3);
        footer.Children.Add(copy);
        _status.Clear();

        var root = new DockPanel();
        DockPanel.SetDock(header, Dock.Top);
        header.Margin = new Thickness(0, 0, 0, 12);
        DockPanel.SetDock(footer, Dock.Bottom);
        root.Children.Add(header);
        root.Children.Add(footer);
        root.Children.Add(frame);
        SetBody(root, new Thickness(20, 18));
        Closed += (_, _) => DiscardIfUnsaved();
    }

    /// <summary>The save dialog (download folder, original name), then Explorer with the saved file selected.</summary>
    private async Task SaveAsAsync()
    {
        var dir = _shell.Settings.DownloadDir;
        var suggested = ReceivedImages.UniqueName(_name, n => File.Exists(Path.Combine(dir, n)));
        var target = await Ui.PickSaveFileAsync(this, Strings.BtnSaveAs, suggested, dir, FileDisplay.Extension(_name));
        if (target is null) return;
        try
        {
            File.Copy(_path, target, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or NotSupportedException or ArgumentException)
        {
            AppLog.Error("image save", ex);
            _status.Error(Strings.ImageSaveFailed(ex.Message));
            return;
        }
        _shell.OpenContainingFolder(target); // the saved file in Explorer, selected
        Close();
    }

    /// <summary>The held copy is always removed on close: a saved copy lives where the user put it.</summary>
    private void DiscardIfUnsaved()
    {
        if (!_held) return;
        try
        {
            var dir = Path.GetDirectoryName(_path);
            if (dir is not null && ReceivedImages.IsHeld(_path, ShellController.ImageHoldRoot)) Directory.Delete(dir, recursive: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { AppLog.Error("image discard", ex); }
    }

    /// <summary>Decodes from memory (no file lock left behind), at most <paramref name="maxWidth"/> pixels wide.</summary>
    private static Bitmap Load(string path, int maxWidth)
    {
        var bytes = File.ReadAllBytes(path);
        using (var probe = new MemoryStream(bytes))
        {
            var full = new Bitmap(probe);
            if (full.PixelSize.Width <= maxWidth) return full;
            full.Dispose();
        }
        using var stream = new MemoryStream(bytes);
        return Bitmap.DecodeToWidth(stream, maxWidth, BitmapInterpolationMode.HighQuality);
    }

    /// <summary>The full-size image as a bitmap on the clipboard (the monitor only reads text, so it is not synced on).</summary>
    private void Copy()
    {
        try
        {
            using var image = Drawing.Image.FromStream(new MemoryStream(File.ReadAllBytes(_path)));
            for (var attempt = 0; ; attempt++)
            {
                try
                {
                    WinForms.Clipboard.SetImage(image);
                    break;
                }
                catch (ExternalException) when (attempt < 4)
                {
                    Thread.Sleep(60); // another program holds the clipboard for a moment
                }
            }
            Toast.Success(Strings.ImageCopied, anchor: this);
        }
        catch (Exception ex) when (ex is IOException or NotSupportedException or ArgumentException or InvalidOperationException
                                       or UnauthorizedAccessException or ExternalException or OutOfMemoryException)
        {
            AppLog.Error("image copy", ex);
            _status.Error(Strings.ImageCopyFailed);
        }
    }
}
