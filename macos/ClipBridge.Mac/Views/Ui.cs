using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Platform.Storage;

namespace ClipBridge.Mac.Views;

/// <summary>Small builders so the code-built views read like layouts.</summary>
internal static class Ui
{
    public static TextBlock Text(string text, double size = 13, FontWeight weight = FontWeight.Normal, double opacity = 1) => new()
    {
        Text = text, FontSize = size, FontWeight = weight, Opacity = opacity, TextWrapping = TextWrapping.Wrap,
    };

    public static TextBlock Heading(string text) => Text(text, 20, FontWeight.SemiBold);

    public static TextBlock Dim(string text) => Text(text, 12, opacity: 0.65);

    public static Button Button(string text, Func<Task> onClick, bool enabled = true)
    {
        var b = new Button { Content = text, IsEnabled = enabled, VerticalAlignment = VerticalAlignment.Center };
        b.Click += async (_, _) =>
        {
            b.IsEnabled = false;
            try { await onClick(); }
            finally { b.IsEnabled = true; }
        };
        return b;
    }

    public static Button Button(string text, Action onClick, bool enabled = true)
    {
        var b = new Button { Content = text, IsEnabled = enabled, VerticalAlignment = VerticalAlignment.Center };
        b.Click += (_, _) => onClick();
        return b;
    }

    public static StackPanel Row(params Control[] children)
    {
        var p = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        foreach (var c in children) p.Children.Add(c);
        return p;
    }

    public static StackPanel Column(double spacing, params Control[] children)
    {
        var p = new StackPanel { Spacing = spacing };
        foreach (var c in children) p.Children.Add(c);
        return p;
    }

    public static Border Card(Control child) => new()
    {
        Child = child,
        Padding = new Thickness(12),
        CornerRadius = new CornerRadius(8),
        BorderThickness = new Thickness(1),
        BorderBrush = new SolidColorBrush(Color.FromArgb(40, 128, 128, 128)),
        Background = new SolidColorBrush(Color.FromArgb(12, 128, 128, 128)),
    };

    public static IBrush ErrorBrush => new SolidColorBrush(Color.FromRgb(0xC0, 0x39, 0x2B));
    public static IBrush OkBrush => new SolidColorBrush(Color.FromRgb(0x2E, 0x8B, 0x57));

    /// <summary>Local paths of the files the user picks; empty when cancelled.</summary>
    public static async Task<IReadOnlyList<string>> PickFilesAsync(Visual owner, string title)
    {
        if (TopLevel.GetTopLevel(owner)?.StorageProvider is not { } sp) return Array.Empty<string>();
        var files = await sp.OpenFilePickerAsync(new FilePickerOpenOptions { Title = title, AllowMultiple = true });
        return files.Select(f => f.TryGetLocalPath()).OfType<string>().ToList();
    }

    public static async Task<string?> PickFolderAsync(Visual owner, string title)
    {
        if (TopLevel.GetTopLevel(owner)?.StorageProvider is not { } sp) return null;
        var dirs = await sp.OpenFolderPickerAsync(new FolderPickerOpenOptions { Title = title, AllowMultiple = false });
        return dirs.FirstOrDefault()?.TryGetLocalPath();
    }

    /// <summary>A modal yes/no question; true when the user confirms.</summary>
    public static async Task<bool> ConfirmAsync(Window owner, string title, string text, string yes)
    {
        var result = false;
        var dialog = new Window
        {
            Title = title, Width = 380, SizeToContent = SizeToContent.Height, CanResize = false,
            WindowStartupLocation = WindowStartupLocation.CenterOwner,
        };
        var ok = Button(yes, () => { result = true; dialog.Close(); });
        var cancel = Button(ClipBridge.Windows.Strings.BtnCancel, () => dialog.Close());
        dialog.Content = Column(16, Text(text), Row(cancel, ok)).With(p => p.Margin = new Thickness(20));
        await dialog.ShowDialog(owner);
        return result;
    }

    /// <summary>A modal one-line text prompt; null when cancelled.</summary>
    public static async Task<string?> PromptAsync(Window owner, string title, string prompt, string initial = "")
    {
        string? result = null;
        var dialog = new Window
        {
            Title = title, Width = 420, SizeToContent = SizeToContent.Height, CanResize = false,
            WindowStartupLocation = WindowStartupLocation.CenterOwner,
        };
        var box = new TextBox { Text = initial, AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, MinHeight = 32 };
        var ok = Button(ClipBridge.Windows.Strings.BtnOk, () => { result = box.Text ?? ""; dialog.Close(); });
        var cancel = Button(ClipBridge.Windows.Strings.BtnCancel, () => dialog.Close());
        dialog.Content = Column(12, Text(prompt), box, Row(cancel, ok)).With(p => p.Margin = new Thickness(20));
        dialog.Opened += (_, _) => box.Focus();
        await dialog.ShowDialog(owner);
        return result;
    }

    public static T With<T>(this T control, Action<T> set)
    {
        set(control);
        return control;
    }
}
