using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

public partial class InboxPage : UserControl
{
    private ShellController _shell = null!;

    public InboxPage() => InitializeComponent();

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        ItemList.ItemsSource = shell.Inbox;
        shell.Inbox.CollectionChanged += (_, _) => UpdateEmpty();
        UpdateEmpty();
    }

    private void UpdateEmpty() =>
        EmptyText.Visibility = _shell.Inbox.Count == 0 ? Visibility.Visible : Visibility.Collapsed;

    public async void OnShown() => await _shell.ReloadInboxAsync();

    private static InboxRow? RowOf(object sender) => (sender as FrameworkElement)?.DataContext as InboxRow;

    private void Tell(string? message)
    {
        if (!string.IsNullOrEmpty(message)) _shell.ShowStatus(message);
    }

    private void Reveal_Click(object sender, RoutedEventArgs e) => RowOf(sender)?.ToggleReveal();

    private async void Copy_Click(object sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is not { } row) return;
        var error = await _shell.CopyInboxItemAsync(row);
        _shell.ShowStatus(error ?? Strings.AppliedToast);
    }

    private async void Ignore_Click(object sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(await _shell.DismissInboxItemAsync(row));
    }

    private async void Clear_Click(object sender, RoutedEventArgs e)
    {
        var answer = MessageBox.Show(Window.GetWindow(this)!, Strings.ConfirmClearInbox, Strings.ConfirmTitle,
            MessageBoxButton.YesNo, MessageBoxImage.Question, MessageBoxResult.No);
        if (answer == MessageBoxResult.Yes) Tell(await _shell.ClearInboxAsync());
    }
}
