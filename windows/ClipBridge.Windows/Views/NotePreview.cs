using System.Windows;
using System.Windows.Controls;
using System.Windows.Documents;
using System.Windows.Input;
using System.Windows.Media;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The preview of a note: draws the block tree of <c>notes.render</c> with plain WPF elements. It never parses Markdown and
/// never executes anything: HTML and unknown blocks are shown as literal monospace text, images as link text (nothing is
/// fetched), and a link only becomes clickable when <see cref="NoteLinks"/> allows its scheme; <see cref="LinkClicked"/>
/// then reports the address and the owner decides how to open it.
/// </summary>
internal sealed class NotePreview : ScrollViewer
{
    private const double Gap = 8;

    private readonly StackPanel _root = new() { Margin = new Thickness(14, 10, 14, 14) };

    public event Action<string>? LinkClicked;

    public NotePreview()
    {
        VerticalScrollBarVisibility = ScrollBarVisibility.Auto;
        HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled;
        Content = _root;
    }

    public void Clear() => _root.Children.Clear();

    public void Show(RenderTree? tree)
    {
        _root.Children.Clear();
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
                return new Inline[] { Link(r.Href, r.Title, r.Children.Count > 0 ? Inlines(r.Children) : new List<Inline> { new Run(r.Href) }) };
            case InlineKind.Image:
                // Nothing is loaded from the network: the picture shows up as its description, as a link when it may be opened.
                return new Inline[] { Link(r.Href, r.Title, new List<Inline> { new Run(Strings.NoteImageText(r.Text)) }) };
            case InlineKind.HardBreak:
            case InlineKind.SoftBreak:
                return new Inline[] { new LineBreak() }; // a line break in the source is a line break in the preview, as in most note apps
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
}
