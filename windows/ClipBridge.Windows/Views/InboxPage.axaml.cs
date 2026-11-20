using System.Collections.ObjectModel;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Layout;
using Avalonia.Media;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;

namespace ClipBridge.Windows.Views;

/// <summary>Received clipboard items: filter (all / not handled / copied), search, copy, reveal, ignore, read in full.</summary>
public partial class InboxPage : UserControl, ISearchablePage
{
    private readonly ShellController _shell;
    private readonly ObservableCollection<InboxRow> _view = new();

    public InboxPage() : this(ShellController.Current) { }

    internal InboxPage(ShellController shell)
    {
        _shell = shell;
        InitializeComponent();
        FilterAll.Content = Strings.InboxFilterAll;
        FilterNew.Content = Strings.InboxFilterNew;
        FilterCopied.Content = Strings.InboxFilterCopied;
        Filter.SelectedIndex = 0;
        Search.PlaceholderText = Strings.InboxSearchPlaceholder;
        ClearText.Text = Strings.BtnClear;
        Items.ItemsSource = _view;
        shell.Inbox.CollectionChanged += (_, _) => Refilter();
        Refilter();
    }

    public async void OnShown()
    {
        await _shell.ReloadInboxAsync();
        Refilter();
    }

    public void FocusSearch()
    {
        Search.Focus();
        Search.SelectAll();
    }

    private void Filter_Changed(object? sender, SelectionChangedEventArgs e) => Refilter();

    private void Search_Changed(object? sender, TextChangedEventArgs e) => Refilter();

    private bool Matches(InboxRow row)
    {
        var keep = Filter.SelectedIndex switch
        {
            1 => row.CanDismiss,
            2 => row.IsApplied,
            _ => true,
        };
        if (!keep) return false;
        var q = Search.Text?.Trim();
        if (string.IsNullOrEmpty(q)) return true;
        // Hidden (sensitive) text is never searched: a match would reveal that the word is in it.
        return row.FromText.Contains(q, StringComparison.CurrentCultureIgnoreCase)
               || (!row.IsHidden && row.Text.Contains(q, StringComparison.CurrentCultureIgnoreCase));
    }

    private void Refilter()
    {
        if (Items is null) return;
        _view.Clear();
        foreach (var row in _shell.Inbox)
            if (Matches(row)) _view.Add(row);

        var total = _shell.Inbox.Count;
        var unhandled = _shell.Inbox.Count(r => r.CanDismiss);
        Summary.Text = total == 0 ? "" : Strings.InboxSummary(total, unhandled, _view.Count);
        ClearButton.IsEnabled = total > 0;
        ListCard.IsVisible = _view.Count > 0;
        Empty.IsVisible = _view.Count == 0;
        var filtered = total > 0;
        Empty.Icon = filtered ? Lumen.UI.Icons.IconKind.Search : Lumen.UI.Icons.IconKind.Inbox;
        Empty.Title = filtered ? Strings.InboxNoMatch : Strings.InboxEmptyTitle;
        Empty.Description = filtered ? Strings.InboxNoMatchHint : Strings.InboxEmpty;
    }

    private static InboxRow? RowOf(object? sender) => (sender as StyledElement)?.DataContext as InboxRow;

    private void Reveal_Click(object? sender, RoutedEventArgs e) => RowOf(sender)?.ToggleReveal();

    private async void Copy_Click(object? sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is not { } row) return;
        var error = await _shell.CopyInboxItemAsync(row);
        _shell.ShowStatus(error ?? Strings.AppliedToast, error is null ? StatusKind.Success : StatusKind.Error);
        if (Filter.SelectedIndex != 0) Refilter();
    }

    private async void Ignore_Click(object? sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is not { } row) return;
        if (await _shell.DismissInboxItemAsync(row) is { } error) _shell.ShowStatus(error, StatusKind.Error);
        if (Filter.SelectedIndex != 0) Refilter();
    }

    /// <summary>The whole text in a dialog (the list shows four lines at most).</summary>
    private async void View_Click(object? sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is not { } row) return;
        var text = new SelectableTextBlock
        {
            Text = row.Text,
            TextWrapping = TextWrapping.Wrap,
        };
        var copy = Ui.Button(AppIcons.Of(Lumen.UI.Icons.IconKind.Copy), Strings.BtnCopy, () => { }, "primary");
        var close = new Button { Content = Strings.Close, IsCancel = true, MinWidth = 88 };
        var panel = new StackPanel
        {
            Spacing = 14,
            Width = 560,
            Children =
            {
                new StackPanel { Spacing = 2, Children = { Ui.Text(row.FromText, "subtitle"), Ui.Text(row.TimeText + "  ·  " + Strings.CharCount(row.Text.Length), "caption", "tertiary") } },
                new Border
                {
                    Padding = new Thickness(14, 12),
                    CornerRadius = new CornerRadius(8),
                    Child = new ScrollViewer { MaxHeight = 380, Content = text },
                }.With(b => b[!Border.BackgroundProperty] = b.GetResourceObservable("Lumen.Brush.SurfaceSubtle").ToBinding()),
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Children = { close, copy } },
            },
        };
        copy.Click += async (_, _) =>
        {
            Dialog.Close(panel);
            var error = await _shell.CopyInboxItemAsync(row);
            _shell.ShowStatus(error ?? Strings.AppliedToast, error is null ? StatusKind.Success : StatusKind.Error);
        };
        close.Click += (_, _) => Dialog.Close(panel);
        await Dialog.ShowAsync(panel, this);
    }

    private async void Clear_Click(object? sender, RoutedEventArgs e)
    {
        if (!await Dialog.ConfirmAsync(Strings.InboxClearTitle, Strings.ConfirmClearInbox, Strings.BtnClear, Strings.BtnCancel, isDanger: true, anchor: this))
            return;
        var error = await _shell.ClearInboxAsync();
        _shell.ShowStatus(error ?? Strings.InboxCleared, error is null ? StatusKind.Success : StatusKind.Error);
    }
}
