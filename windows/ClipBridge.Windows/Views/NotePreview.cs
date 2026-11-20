using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Documents;
using Avalonia.Controls.Primitives;
using Avalonia.Input;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Media.Imaging;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The preview of a note: draws the block tree of <c>notes.render</c> with plain Avalonia elements. It never parses Markdown and
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
    private const double Gap = 10;
    private const int PictureDecodeWidth = 1200;
    private const double PictureMaxWidth = 560;
    private const int PictureCacheSize = 24;

    private readonly StackPanel _root = new() { Margin = new Thickness(18, 14, 18, 18) };
    private readonly Dictionary<string, List<Action>> _waiting = new();
    private readonly Dictionary<string, (AttachmentView View, Bitmap Picture)> _pictures = new();
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
            _root.Children.Add(Ui.Text(Strings.NotePreviewEmpty, "tertiary"));
            return;
        }
        foreach (var block in tree.Blocks) _root.Children.Add(Build(block, 0, Gap));
    }

    // ---- resources ------------------------------------------------------------------------------

    private static void Themed(StyledElement e, AvaloniaProperty property, string key) =>
        e[!property] = e.GetResourceObservable(key).ToBinding();

    private static FontFamily MonoFont =>
        Application.Current?.TryGetResource("Lumen.Font.Mono", Application.Current.ActualThemeVariant, out var f) == true && f is FontFamily family
            ? family : new FontFamily("Cascadia Mono, Consolas");

    // ---- blocks ---------------------------------------------------------------------------------

    private Control Build(RenderBlock b, int depth, double gap) => b.Kind switch
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

    private static T WithGap<T>(T element, double gap) where T : Control
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

    private Control Heading(RenderBlock b)
    {
        var text = Text(b.Inline, NotePreviewStyle.HeadingSize(b.Level));
        text.FontWeight = FontWeight.SemiBold;
        if (b.Level == 6) Themed(text, TextBlock.ForegroundProperty, "Lumen.Brush.TextSecondary");
        if (b.Level > 2)
        {
            text.Margin = new Thickness(0, 8, 0, 6);
            return text;
        }
        var line = new Border { BorderThickness = new Thickness(0, 0, 0, 1), Padding = new Thickness(0, 0, 0, 6), Margin = new Thickness(0, 10, 0, 10), Child = text };
        Themed(line, Border.BorderBrushProperty, "Lumen.Brush.Border");
        return line;
    }

    private Control Code(RenderBlock b, double gap)
    {
        var code = new SelectableTextBlock { Text = b.Text, FontFamily = MonoFont, FontSize = 12.5, TextWrapping = TextWrapping.NoWrap };
        var scroll = new ScrollViewer
        {
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            Content = code,
        };
        var inner = new StackPanel();
        if (b.Lang.Length > 0) inner.Children.Add(Ui.Text(b.Lang, "caption", "tertiary").With(t => t.Margin = new Thickness(0, 0, 0, 6)));
        inner.Children.Add(scroll);
        return Box(inner, gap);
    }

    private Control Literal(RenderBlock b, double gap)
    {
        var text = new SelectableTextBlock { Text = b.LiteralText, FontFamily = MonoFont, FontSize = 12, TextWrapping = TextWrapping.Wrap };
        ToolTip.SetTip(text, Strings.NoteLiteralTip);
        Themed(text, SelectableTextBlock.ForegroundProperty, "Lumen.Brush.TextSecondary");
        return Box(text, gap);
    }

    /// <summary>A sunken, bordered box for code and literal text.</summary>
    private static Border Box(Control child, double gap)
    {
        var box = new Border
        {
            CornerRadius = new CornerRadius(8), BorderThickness = new Thickness(1), Padding = new Thickness(12, 10),
            Margin = new Thickness(0, 0, 0, gap), Child = child,
        };
        Themed(box, Border.BackgroundProperty, "Lumen.Brush.SurfaceSubtle");
        Themed(box, Border.BorderBrushProperty, "Lumen.Brush.Border");
        return box;
    }

    private Control Quote(RenderBlock b, int depth, double gap)
    {
        var bar = new Border { BorderThickness = new Thickness(3, 0, 0, 0), Padding = new Thickness(14, 2, 0, 2), Margin = new Thickness(0, 0, 0, gap), Child = Blocks(b.Children, depth + 1, gap) };
        Themed(bar, Border.BorderBrushProperty, "Lumen.Brush.Accent");
        return bar;
    }

    private Control ListBlock(RenderBlock b, int depth, double gap)
    {
        var panel = new Grid { Margin = new Thickness(0, 0, 0, gap), ColumnDefinitions = new ColumnDefinitions("Auto,*") };
        var itemGap = b.Tight ? 3 : Gap - 2;
        for (var i = 0; i < b.Items.Count; i++)
        {
            panel.RowDefinitions.Add(new RowDefinition(GridLength.Auto));
            // One grid for the whole list: the markers line up ("9." above "10.").
            var marker = Ui.Text(NotePreviewStyle.Marker(b, i, depth), "secondary").With(t =>
            {
                t.Margin = new Thickness(0, 0, 8, itemGap);
                t.HorizontalAlignment = HorizontalAlignment.Right;
                t.MinWidth = 18;
                t.TextWrapping = TextWrapping.NoWrap;
                t.FontSize = NotePreviewStyle.BodySize;
                t.LineHeight = NotePreviewStyle.BodySize * 1.6;
            });
            var content = Blocks(b.Items[i].Blocks, depth + 1, b.Tight ? 3 : Gap - 2);
            content.Margin = new Thickness(0, 0, 0, itemGap);
            Grid.SetRow(marker, i);
            Grid.SetRow(content, i);
            Grid.SetColumn(content, 1);
            panel.Children.Add(marker);
            panel.Children.Add(content);
        }
        return panel;
    }

    private Control Table(RenderBlock b, double gap)
    {
        var columns = NotePreviewStyle.ColumnCount(b);
        var grid = new Grid();
        for (var c = 0; c < columns; c++) grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto, MinWidth = 40 });
        var rows = new List<(IReadOnlyList<IReadOnlyList<RenderInline>> Cells, bool Header)>();
        if (b.Header.Count > 0) rows.Add((b.Header, true));
        rows.AddRange(b.Rows.Select(r => (r, false)));
        for (var r = 0; r < rows.Count; r++)
        {
            grid.RowDefinitions.Add(new RowDefinition(GridLength.Auto));
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
                if (rows[r].Header) cell.FontWeight = FontWeight.SemiBold;
                var box = new Border { BorderThickness = new Thickness(0, 0, 1, 1), Padding = new Thickness(10, 5), Child = cell };
                Themed(box, Border.BorderBrushProperty, "Lumen.Brush.Border");
                if (rows[r].Header) Themed(box, Border.BackgroundProperty, "Lumen.Brush.SurfaceSubtle");
                Grid.SetRow(box, r);
                Grid.SetColumn(box, c);
                grid.Children.Add(box);
            }
        }
        var frame = new Border { BorderThickness = new Thickness(1, 1, 0, 0), Child = grid, HorizontalAlignment = HorizontalAlignment.Left };
        Themed(frame, Border.BorderBrushProperty, "Lumen.Brush.Border");
        return new ScrollViewer
        {
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            Margin = new Thickness(0, 0, 0, gap),
            Content = frame,
        };
    }

    private static Control Rule()
    {
        var line = new Border { Height = 1, Margin = new Thickness(0, 10, 0, 14) };
        Themed(line, Border.BackgroundProperty, "Lumen.Brush.BorderStrong");
        return line;
    }

    // ---- inline text ------------------------------------------------------------------------------

    private SelectableTextBlock Text(IReadOnlyList<RenderInline> runs, double size)
    {
        var text = new SelectableTextBlock { TextWrapping = TextWrapping.Wrap, FontSize = size, LineHeight = size * 1.6 };
        text.Inlines ??= new InlineCollection();
        foreach (var inline in Inlines(runs)) text.Inlines.Add(inline);
        return text;
    }

    private List<Inline> Inlines(IEnumerable<RenderInline> runs)
    {
        var list = new List<Inline>();
        foreach (var run in runs) list.AddRange(One(run));
        return list;
    }

    private static Span SpanOf(Span span, IEnumerable<Inline> children)
    {
        foreach (var c in children) span.Inlines.Add(c);
        return span;
    }

    private IEnumerable<Inline> One(RenderInline r)
    {
        switch (r.Kind)
        {
            case InlineKind.Em:
                return new Inline[] { SpanOf(new Italic(), Inlines(r.Children)) };
            case InlineKind.Strong:
                return new Inline[] { SpanOf(new Bold(), Inlines(r.Children)) };
            case InlineKind.Del:
                return new Inline[] { SpanOf(new Span { TextDecorations = TextDecorations.Strikethrough }, Inlines(r.Children)) };
            case InlineKind.Code:
            {
                var code = new Run(r.Text) { FontFamily = MonoFont, FontSize = 12.5 };
                Themed(code, TextElement.BackgroundProperty, "Lumen.Brush.SurfaceHover");
                return new Inline[] { code };
            }
            case InlineKind.Link:
                if (BlobRef.TryParse(r.Href, out var fileBlob)) return new Inline[] { AttachmentChip(fileBlob, RenderInline.PlainText(r.Children)) };
                return new Inline[] { Link(r.Href, r.Title, r.Children.Count > 0 ? RenderInline.PlainText(r.Children) : r.Href) };
            case InlineKind.Image:
                if (BlobRef.TryParse(r.Href, out var pictureBlob)) return new Inline[] { AttachmentPicture(pictureBlob, r.Text) };
                // Nothing is loaded from the network: the picture shows up as its description, as a link when it may be opened.
                // (A malformed blob: address is no web address either; the guard in Link leaves it as plain text.)
                return new Inline[] { Link(r.Href, r.Title, Strings.NoteImageText(r.Text)) };
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

    /// <summary>A link-looking text that reports clicks (Avalonia has no Hyperlink inline).</summary>
    private static TextBlock LinkText(string text, string? tip, Action onClick)
    {
        var block = new TextBlock { Text = text, Cursor = new Cursor(StandardCursorType.Hand), TextDecorations = TextDecorations.Underline };
        Themed(block, TextBlock.ForegroundProperty, "Lumen.Brush.AccentText");
        if (tip is not null) ToolTip.SetTip(block, tip);
        block.PointerReleased += (_, e) =>
        {
            if (e.InitialPressMouseButton == MouseButton.Left) onClick();
        };
        return block;
    }

    /// <summary>A clickable link when the address may be opened, otherwise the same words as plain text with the address as tooltip.</summary>
    private Inline Link(string href, string title, string content)
    {
        var tip = title.Length > 0 ? title + "\n" + href : href;
        if (!NoteLinks.IsOpenable(href))
        {
            var plain = new TextBlock { Text = content };
            ToolTip.SetTip(plain, tip);
            return new InlineUIContainer(plain) { BaselineAlignment = BaselineAlignment.Baseline };
        }
        return new InlineUIContainer(LinkText(content, tip, () => LinkClicked?.Invoke(href))) { BaselineAlignment = BaselineAlignment.Baseline };
    }

    // ---- attachments --------------------------------------------------------------------------------

    private static TextBlock Placeholder(string text) => Ui.Text(text, "caption", "tertiary");

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
        Bitmap? picture;
        try { picture = await Task.Run(() => PreviewImages.Load(view.CachePath, PictureDecodeWidth)); }
        catch (Exception) { picture = null; } // a file the decoder does not know or a damaged one
        if (generation != _generation) return;
        if (picture is null)
        {
            host.Content = ChipElement(view, Strings.AttachmentImageBroken);
            return;
        }
        Remember(blobId, view, picture);
        host.Content = PictureElement(view, picture, alt);
    }

    private void Remember(string blobId, AttachmentView view, Bitmap picture)
    {
        if (_pictures.ContainsKey(blobId)) return;
        _pictures[blobId] = (view, picture);
        _pictureOrder.Enqueue(blobId);
        while (_pictureOrder.Count > PictureCacheSize) _pictures.Remove(_pictureOrder.Dequeue());
    }

    private Control PictureElement(AttachmentView view, Bitmap picture, string alt)
    {
        var image = new Image
        {
            Source = picture, Stretch = Stretch.Uniform, StretchDirection = StretchDirection.DownOnly,
            MaxWidth = PictureMaxWidth, MaxHeight = PictureMaxWidth, Cursor = new Cursor(StandardCursorType.Hand),
        };
        ToolTip.SetTip(image, (alt.Length > 0 ? alt + "\n" : "") + Strings.AttachmentOpenTip);
        RenderOptions.SetBitmapInterpolationMode(image, BitmapInterpolationMode.HighQuality);
        image.PointerReleased += (_, e) =>
        {
            if (e.InitialPressMouseButton == MouseButton.Left) AttachmentActivated?.Invoke(view);
        };
        return new Border { CornerRadius = new CornerRadius(8), ClipToBounds = true, Child = image, Margin = new Thickness(0, 4) };
    }

    /// <summary>A file (or a picture that cannot be drawn) as a clickable line "📎 name (size)".</summary>
    private Control ChipElement(AttachmentView view, string label) =>
        LinkText(Strings.AttachmentChip(label.Length > 0 ? label : view.Label), Strings.AttachmentOpenTip, () => AttachmentActivated?.Invoke(view));

    /// <summary>A file in the text: "📎 name" at once, with the size once the core has answered; a placeholder while it is not here.</summary>
    private Inline AttachmentChip(string blobId, string linkText)
    {
        AttachmentView? current = null;
        var link = LinkText(Strings.AttachmentChip(linkText.Length > 0 ? linkText : blobId[..8]), null, () =>
        {
            if (current is not null) AttachmentActivated?.Invoke(current);
        });
        _ = LoadChipAsync(blobId, linkText, link, view => current = view, _generation);
        return new InlineUIContainer(link) { BaselineAlignment = BaselineAlignment.Baseline };
    }

    private async Task LoadChipAsync(string blobId, string linkText, TextBlock link, Action<AttachmentView> found, int generation)
    {
        var view = await LookUpAsync(blobId, wantPicture: false);
        if (generation != _generation) return;
        switch (view.State)
        {
            case AttachmentState.Present:
                found(view);
                link.Text = Strings.AttachmentChip(view.Label);
                ToolTip.SetTip(link, Strings.AttachmentOpenTip);
                Themed(link, TextBlock.ForegroundProperty, "Lumen.Brush.AccentText");
                break;
            case AttachmentState.Missing:
                link.Text = Strings.AttachmentChip(linkText.Length > 0 ? linkText : blobId[..8]) + "  " + Strings.AttachmentPending;
                Themed(link, TextBlock.ForegroundProperty, "Lumen.Brush.TextTertiary");
                WaitFor(blobId, () => _ = LoadChipAsync(blobId, linkText, link, found, _generation));
                break;
            default:
                link.Text = Strings.AttachmentFailedChip;
                break;
        }
    }
}

/// <summary>Decodes pictures of attachments for the preview.</summary>
internal static class PreviewImages
{
    /// <summary>
    /// Reads the picture completely (the file is not locked afterwards) and decodes it no wider than <paramref name="maxWidth"/>
    /// pixels, so that a huge photo does not fill the memory. Safe on any thread. Throws when the file cannot be decoded.
    /// </summary>
    public static Bitmap Load(string path, int maxWidth)
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
}

/// <summary>Fills a text block with search-hit segments: the words that matched are emphasised.</summary>
internal static class HighlightText
{
    public static void Set(TextBlock block, IReadOnlyList<TextSegment>? segments)
    {
        block.Inlines ??= new InlineCollection();
        block.Inlines.Clear();
        if (segments is null) return;
        foreach (var segment in segments)
        {
            var run = new Run(segment.Text);
            if (segment.Highlighted)
            {
                run.FontWeight = FontWeight.Bold;
                run[!TextElement.ForegroundProperty] = block.GetResourceObservable("Lumen.Brush.AccentText").ToBinding();
                run[!TextElement.BackgroundProperty] = block.GetResourceObservable("Lumen.Brush.AccentSubtle").ToBinding();
            }
            block.Inlines.Add(run);
        }
    }
}
