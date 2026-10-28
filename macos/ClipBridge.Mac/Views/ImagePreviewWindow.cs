using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Media.Imaging;
using Avalonia.Platform.Storage;
using ClipBridge.Mac.Platform;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

/// <summary>
/// A received image: a preview and "复制到剪贴板" (as an NSImage). A held image (not saved yet) offers "保存到…"
/// — a save panel, then Finder shows the saved file — and is deleted when the window closes; an image saved
/// into a folder the user chose offers "保存到本地" (reveal in Finder). Same behaviour as the Windows window.
/// </summary>
internal sealed class ImagePreviewWindow : Window
{
    private const int MaxOpen = 3;
    private const int PreviewPixels = 1280;
    private static int _open;

    /// <summary>The most recently opened preview (diagnostics only).</summary>
    public static ImagePreviewWindow? Latest { get; private set; }

    /// <summary>Opens a preview unless too many are open already; false = the caller should notify instead.</summary>
    public static bool TryOpen(string path, string name, string from, bool held)
    {
        if (_open >= MaxOpen || !File.Exists(path)) return false;
        _open++;
        var w = new ImagePreviewWindow(path, name, from, held);
        Latest = w;
        w.Closed += (_, _) => { _open--; if (Latest == w) Latest = null; };
        w.Show();
        w.Activate();
        return true;
    }

    private readonly MacController _shell = MacController.Current;
    private readonly string _path;
    private readonly string _name;
    private readonly bool _held;
    private readonly TextBlock _status = Ui.Text("");
    private readonly Bitmap? _bitmap;

    private ImagePreviewWindow(string path, string name, string from, bool held)
    {
        _path = path;
        _name = name;
        _held = held;
        Title = Strings.ImagePreviewTitle(from) + " — " + FileDisplay.Shorten(name, 40);
        Width = 680;
        Height = 600;
        MinWidth = 420;
        MinHeight = 360;
        WindowStartupLocation = WindowStartupLocation.CenterScreen;

        try { _bitmap = Load(path); }
        catch (Exception ex)
        {
            MacLog.Error("image preview", ex);
            _status.Text = Strings.ImageLoadFailed;
        }

        var bottom = Ui.Column(8,
            Ui.Dim(held ? Strings.ImageNotSavedYet : Strings.ImageSavedAt(path)),
            Ui.Row(
                Ui.Button(Strings.BtnCopyImage, Copy),
                held
                    ? Ui.Button(Strings.BtnSaveAs, SaveAsAsync)
                    : Ui.Button(Strings.BtnSaveLocal, () =>
                    {
                        if (MacOpen.Reveal(_path) is { } error) _status.Text = error;
                        else Close();
                    }),
                Ui.Button(Strings.Close, Close)),
            _status);
        var root = new DockPanel { Margin = new Thickness(16) };
        DockPanel.SetDock(bottom, Dock.Bottom);
        bottom.Margin = new Thickness(0, 12, 0, 0);
        root.Children.Add(bottom);
        root.Children.Add(new Border
        {
            Background = new SolidColorBrush(Color.FromArgb(16, 128, 128, 128)),
            CornerRadius = new CornerRadius(6),
            Child = new Image { Source = _bitmap, Stretch = Stretch.Uniform, StretchDirection = StretchDirection.DownOnly },
        });
        Content = root;
        Topmost = true;
        Closed += (_, _) =>
        {
            _bitmap?.Dispose();
            DiscardHeld();
        };
    }

    /// <summary>The save panel (download folder, original name), then Finder with the saved file selected.</summary>
    private async Task SaveAsAsync()
    {
        if (StorageProvider is not { CanSave: true } sp) return;
        var dir = _shell.Settings.DownloadDir;
        Directory.CreateDirectory(dir);
        var file = await sp.SaveFilePickerAsync(new Avalonia.Platform.Storage.FilePickerSaveOptions
        {
            Title = Strings.BtnSaveAs,
            SuggestedFileName = ReceivedImages.UniqueName(_name, n => File.Exists(Path.Combine(dir, n))),
            SuggestedStartLocation = await sp.TryGetFolderFromPathAsync(new Uri(dir)),
            ShowOverwritePrompt = true,
        });
        if (file is null) return;
        try
        {
            await using (var output = await file.OpenWriteAsync())
            await using (var input = File.OpenRead(_path))
                await input.CopyToAsync(output);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            MacLog.Error("image save", ex);
            _status.Text = Strings.ImageSaveFailed(ex.Message);
            return;
        }
        if (file.TryGetLocalPath() is { } saved) MacOpen.Reveal(saved); // the saved path in Finder, file selected
        Close();
    }

    /// <summary>The held copy is always removed on close: a saved copy lives where the user put it.</summary>
    private void DiscardHeld()
    {
        if (!_held || !ReceivedImages.IsHeld(_path, MacController.ImageHoldRoot)) return;
        try
        {
            if (Path.GetDirectoryName(_path) is { } dir) Directory.Delete(dir, recursive: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { MacLog.Error("image discard", ex); }
    }

    /// <summary>Full size when small, otherwise decoded down to <see cref="PreviewPixels"/> wide.</summary>
    private static Bitmap Load(string path)
    {
        var full = new Bitmap(path);
        if (full.PixelSize.Width <= PreviewPixels) return full;
        full.Dispose();
        using var stream = File.OpenRead(path);
        return Bitmap.DecodeToWidth(stream, PreviewPixels, BitmapInterpolationMode.HighQuality);
    }

    private void Copy() => _status.Text = _shell.CopyImageFile(_path) ? Strings.ImageCopied : Strings.ImageCopyFailed;
}
