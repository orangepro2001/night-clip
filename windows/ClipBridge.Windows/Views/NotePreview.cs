using System.Windows;
using System.Windows.Controls;
using System.Windows.Documents;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The preview of a note: draws the block tree of <c>notes.render</c> with plain WPF elements. It never parses Markdown and
/// never executes anything: HTML and unknown blocks are shown as literal monospace text, web images as link text (nothing is
/// fetched from the network), and a link only becomes clickable when <see cref="NoteLinks"/> allows its scheme;
/// <see cref="LinkClicked"/> then reports the address and the owner decides how to open it.
/// Attachments are the exception to "nothing is fetched": an address <c>blob:&lt;sha256&gt;</c> (see <see cref="BlobRef"/>, never
/// a web address) is looked up through <see cref="ResolveAttachment"/>; a picture is drawn inline, any other file becomes a
/// chip that reports <see cref="AttachmentActivated"/> when clicked, and one that has not been synced yet is a placeholder that
/// is filled in when <see cref="AttachmentArrived"/> is called for it.
/// </summary>
internal sealed class NotePreview : ScrollViewer
{
    private const double Gap = 8;
    private const int PictureDecodeWidth = 1200;
    private const double PictureMaxWidth = 560;
    private const int PictureCacheSize = 24;

    private readonly StackPanel _root = new() { Margin = new Thickness(14, 10, 14, 14) };
    private readonly Dictionary<string, List<Action>> _waiting = new();
    private readonly Dictionary<string, (AttachmentView View, BitmapSource Picture)> _pictures = new();
    private readonly Queue<string> _pictureOrder = new();
    private int _generation;

    public event Action<string>? LinkClicked;

    /// <summary>The owner looks an attachment up (blob id, whether a picture file is wanted); it must not throw.</summary>
    public Func<string, bool, Task<AttachmentView>>? ResolveAttachment { get; set; }

    /// <summary>The user clicked a picture or a file chip of an attachment that is on this device.</summary>
    public event Action<AttachmentView>? AttachmentActivated;

    public NotePreview()
    {
        VerticalScrollBarVisibility = ScrollBarVisibility.Auto;
        HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled;
        Content = _root;
    }

    public void Clear()
    {
        _generation++; // answers of attachment lookups that are still running belong to a preview that is gone
        _waiting.Clear();
        _root.Children.Clear();
    }

    /// <summary>An attachment reached this device: whatever shows a placeholder for it looks it up again.</summary>
    public void AttachmentArrived(string blobId)
    {
        if (!_waiting.Remove(blobId, out var retries)) return;
        foreach (var retry in retries) retry();
    }

    public void Show(RenderTree? tree)
    {
        Clear();
        if (tree is null) return;
        if (tree.Blocks.Count == 0)
        {
            var empty = new TextBlock { Text = Strings.NotePreviewEmpty };
            empty.SetResourceReference(TextBlock.ForegroundProperty, "AppFaint");
            _root.Children.Add(empty);
            return;
        }
        foreach (var block in tree.Blocks) _root.Children.Add(Build(block, 0, Gap));
    }

    // ---- resources ------------------------------------------------------------------------------

    private static FontFamily MonoFont =>
        Application.Current?.TryFindResource("AppMonoFont") as FontFamily ?? new FontFamily("Consolas");

    private static void Themed(FrameworkElement e, DependencyProperty property, string key) => e.SetResourceReference(property, key);

    // ---- blocks ---------------------------------------------------------------------------------

    private UIElement Build(RenderBlock b, int depth, double gap) => b.Kind switch
    {
        BlockKind.Heading => Heading(b),
        BlockKind.Paragraph => WithGap(Text(b.Inline, NotePreviewStyle.BodySize), gap),
        BlockKind.Code => Code(b, gap),
        BlockKind.Quote => Quote(b, depth, gap),
        BlockKind.List => ListBlock(b, depth, gap),
        BlockKind.Table => Table(b, gap),
        BlockKind.Hr => Rule(),
        _ => Literal(b, gap), // html and anything this shell does not know: the original text, nothing else
    };

    private static T WithGap<T>(T element, double gap) where T : FrameworkElement
    {
        element.Margin = new Thickness(0, 0, 0, gap);
        return element;
    }

    private StackPanel Blocks(IEnumerable<RenderBlock> blocks, int depth, double gap)
    {
        var panel = new StackPanel();
        foreach (var block in blocks) panel.Children.Add(Build(block, depth, gap));
        return panel;
    }

    private UIElement Heading(RenderBlock b)
    {
        var text = Text(b.Inline, NotePreviewStyle.HeadingSize(b.Level));
        text.FontWeight = FontWeights.SemiBold;
        if (b.Level == 6) Themed(text, TextBlock.ForegroundProperty, "AppMuted");
        if (b.Level > 2)
        {
            text.Margin = new Thickness(0, 6, 0, 6);
            return text;
        }
        var line = new Border { BorderThickness = new Thickness(0, 0, 0, 1), Padding = new Thickness(0, 0, 0, 4), Margin = new Thickness(0, 8, 0, 8), Child = text };
        Themed(line, Border.BorderBrushProperty, "AppBorder");
        return line;
    }

    private UIElement Code(RenderBlock b, double gap)
    {
        var code = new TextBlock { Text = b.Text, FontFamily = MonoFont, FontSize = 12, TextWrapping = TextWrapping.NoWrap };
        var scroll = new ScrollViewer
        {
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            Content = code,
        };
        ForwardWheel(scroll);
        var inner = new StackPanel();
        if (b.Lang.Length > 0)
        {
            var lang = new TextBlock { Text = b.Lang, FontSize = 11, Margin = new Thickness(0, 0, 0, 4) };
            Themed(lang, TextBlock.ForegroundProperty, "AppFaint");
            inner.Children.Add(lang);
        }
        inner.Children.Add(scroll);
        return Box(inner, gap);
    }

    private UIElement Literal(RenderBlock b, double gap)
    {
        var text = new TextBlock
        {
            Text = b.LiteralText, FontFamily = MonoFont, FontSize = 12, TextWrapping = TextWrapping.Wrap, ToolTip = Strings.NoteLiteralTip,
        };
        Themed(text, TextBlock.ForegroundProperty, "AppMuted");
        return Box(text, gap);
    }

    /// <summary>A sunken, bordered box for code and literal text.</summary>
    private static Border Box(UIElement child, double gap)
    {
        var box = new Border
        {
            CornerRadius = new CornerRadius(6), BorderThickness = new Thickness(1), Padding = new Thickness(10, 8, 10, 8),
            Margin = new Thickness(0, 0, 0, gap), Child = child,
        };
        Themed(box, Border.BackgroundProperty, "AppSurfaceSunken");
        Themed(box, Border.BorderBrushProperty, "AppBorder");
        return box;
    }

    private UIElement Quote(RenderBlock b, int depth, double gap)
    {
        var bar = new Border { BorderThickness = new Thickness(3, 0, 0, 0), Padding = new Thickness(12, 0, 0, 0), Margin = new Thickness(0, 0, 0, gap), Child = Blocks(b.Children, depth + 1, gap) };
        Themed(bar, Border.BorderBrushProperty, "AppBorderStrong");
        return bar;
    }

    private UIElement ListBlock(RenderBlock b, int depth, double gap)
    {
        var panel = new StackPanel { Margin = new Thickness(0, 0, 0, gap) };
        Grid.SetIsSharedSizeScope(panel, true); // the markers of one list line up ("9." above "10.")
        var itemGap = b.Tight ? 2 : Gap - 2;
        for (var i = 0; i < b.Items.Count; i++)
        {
            var row = new Grid { Margin = new Thickness(0, 0, 0, itemGap) };
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto, MinWidth = 22, SharedSizeGroup = "ListMarker" });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            var marker = new TextBlock { Text = NotePreviewStyle.Marker(b, i, depth), Margin = new Thickness(0, 0, 8, 0), HorizontalAlignment = HorizontalAlignment.Right };
            Themed(marker, TextBlock.ForegroundProperty, "AppMuted");
            var content = Blocks(b.Items[i].Blocks, depth + 1, b.Tight ? 2 : Gap - 2);
            Grid.SetColumn(content, 1);
            row.Children.Add(marker);
            row.Children.Add(content);
            panel.Children.Add(row);
        }
        return panel;
    }

    private UIElement Table(RenderBlock b, double gap)
    {
        var columns = NotePreviewStyle.ColumnCount(b);
        var grid = new Grid();
        for (var c = 0; c < columns; c++) grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto, MinWidth = 40 });
        var rows = new List<(IReadOnlyList<IReadOnlyList<RenderInline>> Cells, bool Header)>();
        if (b.Header.Count > 0) rows.Add((b.Header, true));
        rows.AddRange(b.Rows.Select(r => (r, false)));
        for (var r = 0; r < rows.Count; r++)
        {
            grid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
            for (var c = 0; c < columns; c++)
            {
                var runs = c < rows[r].Cells.Count ? rows[r].Cells[c] : Array.Empty<RenderInline>();
                var cell = Text(runs, NotePreviewStyle.BodySize);
                cell.MaxWidth = 360;
                cell.TextAlignment = NotePreviewStyle.AlignOf(b, c) switch
                {
                    ColumnAlign.Center => TextAlignment.Center,
                    ColumnAlign.Right => TextAlignment.Right,
                    _ => TextAlignment.Left,
                };
                if (rows[r].Header) cell.FontWeight = FontWeights.SemiBold;
                var box = new Border { BorderThickness = new Thickness(0, 0, 1, 1), Padding = new Thickness(8, 4, 8, 4), Child = cell };
                Themed(box, Border.BorderBrushProperty, "AppBorder");
                if (rows[r].Header) Themed(box, Border.BackgroundProperty, "AppSurfaceSunken");
                Grid.SetRow(box, r);
                Grid.SetColumn(box, c);
                grid.Children.Add(box);
            }
        }
        var frame = new Border { BorderThickness = new Thickness(1, 1, 0, 0), Child = grid, HorizontalAlignment = HorizontalAlignment.Left };
        Themed(frame, Border.BorderBrushProperty, "AppBorder");
        var scroll = new ScrollViewer
        {
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            Margin = new Thickness(0, 0, 0, gap),
            Content = frame,
        };
        ForwardWheel(scroll);
        return scroll;
    }

    private static UIElement Rule()
    {
        var line = new Border { Height = 1, Margin = new Thickness(0, 10, 0, 12) };
        Themed(line, Border.BackgroundProperty, "AppBorderStrong");
        return line;
    }

    /// <summary>
    /// A ScrollViewer that only scrolls sideways would still swallow the mouse wheel and stop the page from scrolling while the
    /// pointer is over a code block or table: hand the wheel to the parent.
    /// </summary>
    private static void ForwardWheel(ScrollViewer inner)
    {
        inner.PreviewMouseWheel += (sender, e) =>
        {
            if (e.Handled || inner.Parent is not UIElement parent) return;
            e.Handled = true;
            parent.RaiseEvent(new MouseWheelEventArgs(e.MouseDevice, e.Timestamp, e.Delta) { RoutedEvent = MouseWheelEvent, Source = sender });
        };
    }

    // ---- inline text ------------------------------------------------------------------------------

    private TextBlock Text(IReadOnlyList<RenderInline> runs, double size)
    {
        var text = new TextBlock { TextWrapping = TextWrapping.Wrap, FontSize = size, LineHeight = size * 1.6 };
        text.Inlines.AddRange(Inlines(runs));
        return text;
    }

    private List<Inline> Inlines(IEnumerable<RenderInline> runs)
    {
        var list = new List<Inline>();
        foreach (var run in runs) list.AddRange(One(run));
        return list;
    }

    private IEnumerable<Inline> One(RenderInline r)
    {
        switch (r.Kind)
        {
            case InlineKind.Em:
            {
                var span = new Italic();
                span.Inlines.AddRange(Inlines(r.Children));
                return new Inline[] { span };
            }
            case InlineKind.Strong:
            {
                var span = new Bold();
                span.Inlines.AddRange(Inlines(r.Children));
                return new Inline[] { span };
            }
            case InlineKind.Del:
            {
                var span = new Span { TextDecorations = TextDecorations.Strikethrough };
                span.Inlines.AddRange(Inlines(r.Children));
                return new Inline[] { span };
            }
            case InlineKind.Code:
            {
                var code = new Run(r.Text) { FontFamily = MonoFont, FontSize = 12 };
                code.SetResourceReference(TextElement.BackgroundProperty, "AppSurfaceSunken");
                return new Inline[] { code };
            }
            case InlineKind.Link:
                if (BlobRef.TryParse(r.Href, out var fileBlob)) return new Inline[] { AttachmentChip(fileBlob, RenderInline.PlainText(r.Children)) };
                return new Inline[] { Link(r.Href, r.Title, r.Children.Count > 0 ? Inlines(r.Children) : new List<Inline> { new Run(r.Href) }) };
            case InlineKind.Image:
                if (BlobRef.TryParse(r.Href, out var pictureBlob)) return new Inline[] { AttachmentPicture(pictureBlob, r.Text) };
                // Nothing is loaded from the network: the picture shows up as its description, as a link when it may be opened.
                // (A malformed blob: address is no web address either; the guard in Link leaves it as plain text.)
                return new Inline[] { Link(r.Href, r.Title, new List<Inline> { new Run(Strings.NoteImageText(r.Text)) }) };
            case var k when NotePreviewStyle.IsLineBreak(k):
                // A line break in the source is a line break in the preview (also a soft one), as in most note apps.
                return new Inline[] { new LineBreak() };
            default:
            {
                var parts = new List<Inline>();
                if (r.Text.Length > 0) parts.Add(new Run(r.Text));
                parts.AddRange(Inlines(r.Children));
                return parts;
            }
        }
    }

    /// <summary>A clickable link when the address may be opened, otherwise the same words as plain text with the address as tooltip.</summary>
    private Inline Link(string href, string title, List<Inline> content)
    {
        var tip = title.Length > 0 ? title + "\n" + href : href;
        if (!NoteLinks.IsOpenable(href))
        {
            var plain = new Span { ToolTip = tip };
            plain.Inlines.AddRange(content);
            return plain;
        }
        var link = new Hyperlink { ToolTip = tip };
        link.Inlines.AddRange(content);
        link.SetResourceReference(TextElement.ForegroundProperty, "AppAccent");
        link.Click += (_, _) => LinkClicked?.Invoke(href);
        return link;
    }

    // ---- attachments --------------------------------------------------------------------------------

    private static TextBlock Placeholder(string text)
    {
        var block = new TextBlock { Text = text, FontSize = 12 };
        block.SetResourceReference(TextBlock.ForegroundProperty, "AppFaint");
        return block;
    }

    private async Task<AttachmentView> LookUpAsync(string blobId, bool wantPicture)
    {
        if (ResolveAttachment is null) return AttachmentView.Failure(blobId, "INTERNAL");
        try { return await ResolveAttachment(blobId, wantPicture); }
        catch (Exception) { return AttachmentView.Failure(blobId, "INTERNAL"); }
    }

    /// <summary>Calls <paramref name="retry"/> once the attachment arrives (see <see cref="AttachmentArrived"/>).</summary>
    private void WaitFor(string blobId, Action retry)
    {
        if (!_waiting.TryGetValue(blobId, out var list)) _waiting[blobId] = list = new List<Action>();
        list.Add(retry);
    }

    /// <summary>A picture in the text: a placeholder first (or the picture at once when it was shown before), then the picture.</summary>
    private Inline AttachmentPicture(string blobId, string alt)
    {
        var host = new ContentControl { Focusable = false };
        if (_pictures.TryGetValue(blobId, out var shown)) host.Content = PictureElement(shown.View, shown.Picture, alt);
        else
        {
            host.Content = Placeholder(Strings.AttachmentImageLoading);
            _ = LoadPictureAsync(blobId, host, alt, _generation);
        }
        return new InlineUIContainer(host) { BaselineAlignment = BaselineAlignment.Center };
    }

    private async Task LoadPictureAsync(string blobId, ContentControl host, string alt, int generation)
    {
        var view = await LookUpAsync(blobId, wantPicture: true);
        if (generation != _generation) return; // the preview shows something else by now
        switch (view.State)
        {
            case AttachmentState.Missing:
                host.Content = Placeholder(Strings.AttachmentPending);
                WaitFor(blobId, () => _ = LoadPictureAsync(blobId, host, alt, _generation));
                return;
            case AttachmentState.Failed:
                host.Content = Placeholder(Strings.AttachmentImageBroken);
                return;
        }
        if (!view.IsImage || view.CachePath.Length == 0)
        {
            host.Content = ChipElement(view, view.Name); // the note says picture, the file is something else
            return;
        }
        BitmapSource? picture;
        try { picture = await Task.Run(() => PreviewImages.Load(view.CachePath, PictureDecodeWidth)); }
        catch (Exception) { picture = null; } // a file the decoder does not know (WebP without the system codec) or a damaged one
        if (generation != _generation) return;
        if (picture is null)
        {
            host.Content = ChipElement(view, Strings.AttachmentImageBroken);
            return;
        }
        Remember(blobId, view, picture);
        host.Content = PictureElement(view, picture, alt);
    }

    private void Remember(string blobId, AttachmentView view, BitmapSource picture)
    {
        if (_pictures.ContainsKey(blobId)) return;
        _pictures[blobId] = (view, picture);
        _pictureOrder.Enqueue(blobId);
        while (_pictureOrder.Count > PictureCacheSize) _pictures.Remove(_pictureOrder.Dequeue());
    }

    private UIElement PictureElement(AttachmentView view, BitmapSource picture, string alt)
    {
        var image = new Image
        {
            Source = picture, Stretch = Stretch.Uniform, StretchDirection = StretchDirection.DownOnly,
            MaxWidth = PictureMaxWidth, MaxHeight = PictureMaxWidth, Cursor = Cursors.Hand,
            ToolTip = (alt.Length > 0 ? alt + "\n" : "") + Strings.AttachmentOpenTip,
        };
        RenderOptions.SetBitmapScalingMode(image, BitmapScalingMode.HighQuality);
        image.MouseLeftButtonUp += (_, _) => AttachmentActivated?.Invoke(view);
        return image;
    }

    /// <summary>A file (or a picture that cannot be drawn) as a clickable line "📎 name (size)".</summary>
    private UIElement ChipElement(AttachmentView view, string label)
    {
        var link = new Hyperlink(new Run(Strings.AttachmentChip(label.Length > 0 ? label : view.Label))) { ToolTip = Strings.AttachmentOpenTip };
        link.SetResourceReference(TextElement.ForegroundProperty, "AppAccent");
        link.Click += (_, _) => AttachmentActivated?.Invoke(view);
        return new TextBlock(link) { FontSize = NotePreviewStyle.BodySize };
    }

    /// <summary>A file in the text: "📎 name" at once, with the size once the core has answered; a placeholder while it is not here.</summary>
    private Inline AttachmentChip(string blobId, string linkText)
    {
        var run = new Run(Strings.AttachmentChip(linkText.Length > 0 ? linkText : blobId[..8]));
        var link = new Hyperlink(run);
        link.SetResourceReference(TextElement.ForegroundProperty, "AppAccent");
        AttachmentView? current = null;
        link.Click += (_, _) =>
        {
            if (current is not null) AttachmentActivated?.Invoke(current);
        };
        _ = LoadChipAsync(blobId, linkText, run, link, view => current = view, _generation);
        return link;
    }

    private async Task LoadChipAsync(string blobId, string linkText, Run run, Hyperlink link, Action<AttachmentView> found, int generation)
    {
        var view = await LookUpAsync(blobId, wantPicture: false);
        if (generation != _generation) return;
        switch (view.State)
        {
            case AttachmentState.Present:
                found(view);
                run.Text = Strings.AttachmentChip(view.Label);
                link.ToolTip = Strings.AttachmentOpenTip;
                break;
            case AttachmentState.Missing:
                run.Text = Strings.AttachmentChip(linkText.Length > 0 ? linkText : blobId[..8]) + "  " + Strings.AttachmentPending;
                link.SetResourceReference(TextElement.ForegroundProperty, "AppFaint");
                WaitFor(blobId, () =>
                {
                    link.SetResourceReference(TextElement.ForegroundProperty, "AppAccent");
                    _ = LoadChipAsync(blobId, linkText, run, link, found, _generation);
                });
                break;
            default:
                run.Text = Strings.AttachmentFailedChip;
                break;
        }
    }
}

/// <summary>Decodes pictures of attachments for the preview.</summary>
internal static class PreviewImages
{
    /// <summary>
    /// Reads the picture completely (the file is not locked afterwards) and decodes it no wider than <paramref name="maxWidth"/>
    /// pixels, so that a huge photo does not fill the memory. The result is frozen: it can be made on any thread. Throws when the
    /// file cannot be decoded.
    /// </summary>
    public static BitmapSource Load(string path, int maxWidth)
    {
        int width;
        using (var probe = File.OpenRead(path))
        {
            var decoder = BitmapDecoder.Create(probe, BitmapCreateOptions.DelayCreation | BitmapCreateOptions.IgnoreColorProfile, BitmapCacheOption.None);
            width = decoder.Frames[0].PixelWidth;
        }
        using var stream = File.OpenRead(path);
        var bitmap = new BitmapImage();
        bitmap.BeginInit();
        bitmap.CacheOption = BitmapCacheOption.OnLoad;
        bitmap.StreamSource = stream;
        if (width > maxWidth) bitmap.DecodePixelWidth = maxWidth;
        bitmap.EndInit();
        bitmap.Freeze();
        return bitmap;
    }
}
