using System.Windows;
using System.Windows.Controls;
using System.Windows.Documents;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Views;

/// <summary>
/// A line of text in which the pieces marked as highlighted (the words a search found) are emphasised. The pieces come from
/// <see cref="CodePointText.Segments"/>, so the positions are already converted to what a .NET string needs.
/// </summary>
internal sealed class HighlightText : TextBlock
{
    public static readonly DependencyProperty SegmentsProperty = DependencyProperty.Register(
        nameof(Segments), typeof(IReadOnlyList<TextSegment>), typeof(HighlightText), new PropertyMetadata(null, OnSegmentsChanged));

    public HighlightText()
    {
        // A style for TextBlock is not applied to a class derived from it: ask for it, to get the colours of the theme.
        SetResourceReference(StyleProperty, typeof(TextBlock));
    }

    public IReadOnlyList<TextSegment>? Segments
    {
        get => (IReadOnlyList<TextSegment>?)GetValue(SegmentsProperty);
        set => SetValue(SegmentsProperty, value);
    }

    private static void OnSegmentsChanged(DependencyObject d, DependencyPropertyChangedEventArgs e)
    {
        var block = (HighlightText)d;
        block.Inlines.Clear();
        if (e.NewValue is not IReadOnlyList<TextSegment> segments) return;
        foreach (var segment in segments)
        {
            var run = new Run(segment.Text);
            if (segment.Highlighted)
            {
                run.FontWeight = FontWeights.Bold;
                run.SetResourceReference(TextElement.ForegroundProperty, "AppAccent");
                run.SetResourceReference(TextElement.BackgroundProperty, "AppAccentSoft");
            }
            block.Inlines.Add(run);
        }
    }
}
