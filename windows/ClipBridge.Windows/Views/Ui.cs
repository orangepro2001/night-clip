using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Platform.Storage;
using ClipBridge.Windows.Logic;
using Lumen.UI.Controls;

namespace ClipBridge.Windows.Views;

/// <summary>Small builders and dialogs shared by the code-built parts of the views.</summary>
internal static class Ui
{
    public static TextBlock Text(string text, params string[] classes)
    {
        var t = new TextBlock { Text = text, TextWrapping = TextWrapping.Wrap };
        foreach (var c in classes) t.Classes.Add(c);
        return t;
    }

    public static Icon Icon(string key, double size = 16) => new() { NameKey = key, Size = size };

    /// <summary>
    /// A code-built item template. Lists hand their templates <c>null</c> when they recycle a row, and a value type cannot be
    /// unboxed from it: anything that is not a <typeparamref name="T"/> gets an empty panel.
    /// </summary>
    public static Avalonia.Controls.Templates.FuncDataTemplate Template<T>(Func<T, Control> build) =>
        new(typeof(T), (data, _) => data is T item ? build(item) : new Panel());

    /// <summary>A button with an icon and a label.</summary>
    public static Button Button(string iconKey, string text, Action onClick, params string[] classes)
    {
        var b = new Button
        {
            Content = new StackPanel
            {
                Orientation = Orientation.Horizontal,
                Spacing = 6,
                Children = { Icon(iconKey, classes.Contains("sm") ? 14 : 15), new TextBlock { Text = text, VerticalAlignment = VerticalAlignment.Center } },
            },
        };
        foreach (var c in classes) b.Classes.Add(c);
        b.Click += (_, _) => onClick();
        return b;
    }

    /// <summary>A text-only button.</summary>
    public static Button TextButton(string text, Action onClick, params string[] classes)
    {
        var b = new Button { Content = text };
        foreach (var c in classes) b.Classes.Add(c);
        b.Click += (_, _) => onClick();
        return b;
    }

    /// <summary>A square ghost button with an icon and a tooltip.</summary>
    public static Button IconButton(string iconKey, string tip, Action onClick, params string[] classes)
    {
        var b = new Button { Content = Icon(iconKey, classes.Contains("sm") ? 14 : 16) };
        b.Classes.Add("ghost");
        b.Classes.Add("icon");
        foreach (var c in classes) b.Classes.Add(c);
        ToolTip.SetTip(b, tip);
        b.Click += (_, _) => onClick();
        return b;
    }

    /// <summary>Disables the button while <paramref name="work"/> runs (no double submits).</summary>
    public static async Task Busy(Button button, Func<Task> work)
    {
        button.IsEnabled = false;
        try { await work(); }
        finally { button.IsEnabled = true; }
    }

    // ---- dialogs (in-window, over the main window's AppShell or a window's own DialogHost) ----------------

    /// <summary>
    /// A one-line text prompt. <paramref name="validate"/> returns the accepted (normalised) text, or null to refuse it
    /// (<paramref name="invalidText"/> is shown). Null when cancelled.
    /// </summary>
    public static async Task<string?> PromptAsync(Visual anchor, string title, string prompt, string initial,
        Func<string?, string?> validate, string invalidText, string okText, bool multiline = false)
    {
        var box = new TextBox
        {
            Text = initial,
            AcceptsReturn = multiline,
            TextWrapping = multiline ? TextWrapping.Wrap : TextWrapping.NoWrap,
            MinHeight = multiline ? 96 : 0,
            MaxHeight = multiline ? 220 : double.PositiveInfinity,
        };
        var field = new FormField { Label = prompt, Content = box };
        var ok = new Button { Content = okText, IsDefault = !multiline, MinWidth = 88 };
        ok.Classes.Add("primary");
        var cancel = new Button { Content = Strings.BtnCancel, IsCancel = true, MinWidth = 88 };
        var panel = new StackPanel
        {
            Spacing = 14,
            Width = 400,
            Children =
            {
                Text(title, "title"),
                field,
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Children = { cancel, ok } },
            },
        };
        string? result = null;
        void Submit()
        {
            var accepted = validate(box.Text);
            if (accepted is null)
            {
                field.Error = invalidText;
                return;
            }
            result = accepted;
            Dialog.Close(panel, accepted);
        }
        ok.Click += (_, _) => Submit();
        cancel.Click += (_, _) => Dialog.Close(panel);
        box.TextChanged += (_, _) => field.Error = null;
        if (multiline)
            box.KeyDown += (_, e) =>
            {
                if (e.Key == Key.Enter && e.KeyModifiers == KeyModifiers.Control) { Submit(); e.Handled = true; }
            };
        panel.AttachedToVisualTree += (_, _) => { box.Focus(); box.SelectAll(); };
        await Dialog.ShowAsync(panel, anchor);
        return result;
    }

    /// <summary>Chooses one device from <paramref name="peers"/>; null when cancelled.</summary>
    public static async Task<PeerItem?> PickPeerAsync(Visual anchor, IReadOnlyList<PeerItem> peers)
    {
        var list = new ListBox
        {
            ItemsSource = peers,
            SelectedIndex = 0,
            MaxHeight = 300,
            ItemTemplate = Ui.Template<PeerItem>(peer =>
            {
                var row = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto") };
                var tile = DeviceTile(peer);
                var name = new StackPanel { Margin = new Thickness(12, 0, 0, 0), VerticalAlignment = VerticalAlignment.Center, Spacing = 1 };
                name.Children.Add(Text(peer.DisplayName, "medium"));
                name.Children.Add(Text(peer.PlatformText, "caption", "tertiary"));
                Grid.SetColumn(name, 1);
                var badge = StateBadge(peer);
                Grid.SetColumn(badge, 2);
                row.Children.Add(tile);
                row.Children.Add(name);
                row.Children.Add(badge);
                return row;
            }),
        };
        var ok = new Button { Content = Strings.BtnNext, IsDefault = true, MinWidth = 88 };
        ok.Classes.Add("primary");
        var cancel = new Button { Content = Strings.BtnCancel, IsCancel = true, MinWidth = 88 };
        var panel = new StackPanel
        {
            Spacing = 12,
            Width = 400,
            Children =
            {
                Text(Strings.FilePickPeerTitle, "title"),
                Text(Strings.FilePickPeerPrompt, "secondary"),
                list,
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Children = { cancel, ok } },
            },
        };
        ok.Click += (_, _) => { if (list.SelectedItem is PeerItem p) Dialog.Close(panel, p); };
        cancel.Click += (_, _) => Dialog.Close(panel);
        list.DoubleTapped += (_, _) => { if (list.SelectedItem is PeerItem p) Dialog.Close(panel, p); };
        panel.AttachedToVisualTree += (_, _) => list.Focus();
        return await Dialog.ShowAsync(panel, anchor) as PeerItem;
    }

    // ---- device visuals ---------------------------------------------------------------------

    public static string PlatformIcon(string platform) => platform switch
    {
        "android" => AppIcons.Smartphone,
        "macos" => AppIcons.Laptop,
        _ => AppIcons.Monitor,
    };

    /// <summary>36×36 tile with the platform icon, tinted by the connection state.</summary>
    public static Border DeviceTile(PeerItem peer)
    {
        var tile = new Border { Child = new Icon { NameKey = PlatformIcon(peer.Platform), Size = 18, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center } };
        tile.Classes.Add("tile");
        tile.Classes.Add(peer.State switch
        {
            "connected" => "success",
            "connecting" => "accent",
            "unpaired" or "revoked" => "danger",
            _ => "plain",
        });
        return tile;
    }

    /// <summary>A coloured status pill for a device's connection state.</summary>
    public static Badge StateBadge(PeerItem peer)
    {
        var badge = new Badge { Content = peer.StateText, VerticalAlignment = VerticalAlignment.Center };
        badge.Classes.Add("dot");
        var tone = peer.State switch
        {
            "connected" => "success",
            "connecting" => "info",
            "unpaired" or "revoked" => "danger",
            _ => null,
        };
        if (tone is not null) badge.Classes.Add(tone);
        return badge;
    }

    // ---- pickers ------------------------------------------------------------------------------

    /// <summary>Local paths of the files the user picks; empty when cancelled.</summary>
    public static async Task<IReadOnlyList<string>> PickFilesAsync(Visual owner, string title)
    {
        if (TopLevel.GetTopLevel(owner)?.StorageProvider is not { } sp) return Array.Empty<string>();
        var files = await sp.OpenFilePickerAsync(new FilePickerOpenOptions { Title = title, AllowMultiple = true });
        return files.Select(f => f.TryGetLocalPath()).OfType<string>().ToList();
    }

    public static async Task<string?> PickFolderAsync(Visual owner, string title, string? start = null)
    {
        if (TopLevel.GetTopLevel(owner)?.StorageProvider is not { } sp) return null;
        IStorageFolder? startFolder = null;
        if (!string.IsNullOrWhiteSpace(start) && Directory.Exists(start))
            startFolder = await sp.TryGetFolderFromPathAsync(start);
        var dirs = await sp.OpenFolderPickerAsync(new FolderPickerOpenOptions { Title = title, AllowMultiple = false, SuggestedStartLocation = startFolder });
        return dirs.FirstOrDefault()?.TryGetLocalPath();
    }

    /// <summary>A save-file dialog; returns the chosen local path or null.</summary>
    public static async Task<string?> PickSaveFileAsync(Visual owner, string title, string suggestedName, string? startDir, string? extension)
    {
        if (TopLevel.GetTopLevel(owner)?.StorageProvider is not { } sp) return null;
        IStorageFolder? startFolder = null;
        if (!string.IsNullOrWhiteSpace(startDir) && Directory.Exists(startDir))
            startFolder = await sp.TryGetFolderFromPathAsync(startDir);
        var options = new FilePickerSaveOptions
        {
            Title = title,
            SuggestedFileName = suggestedName,
            SuggestedStartLocation = startFolder,
            ShowOverwritePrompt = true,
        };
        if (!string.IsNullOrEmpty(extension))
        {
            var ext = extension.TrimStart('.');
            options.DefaultExtension = ext;
            options.FileTypeChoices = [new FilePickerFileType(ext.ToUpperInvariant()) { Patterns = ["*." + ext] }, FilePickerFileTypes.All];
        }
        var file = await sp.SaveFilePickerAsync(options);
        return file?.TryGetLocalPath();
    }
}
