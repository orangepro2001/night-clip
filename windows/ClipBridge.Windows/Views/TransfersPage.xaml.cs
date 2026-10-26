using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

public partial class TransfersPage : UserControl
{
    private ShellController _shell = null!;

    public TransfersPage() => InitializeComponent();

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        TransferList.ItemsSource = shell.Transfers.Rows;
        shell.Transfers.Rows.CollectionChanged += (_, _) => UpdateEmpty();
        UpdateEmpty();
    }

    private void UpdateEmpty() =>
        EmptyText.Visibility = _shell.Transfers.Rows.Count == 0 ? Visibility.Visible : Visibility.Collapsed;

    public async void OnShown() => await _shell.ReloadTransfersAsync();

    private static TransferRow? RowOf(object sender) => (sender as FrameworkElement)?.DataContext as TransferRow;

    private void Tell(string? message)
    {
        if (!string.IsNullOrEmpty(message)) _shell.ShowStatus(message);
    }

    private async void Refresh_Click(object sender, RoutedEventArgs e) => await _shell.ReloadTransfersAsync();

    private async void Accept_Click(object sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(await _shell.AcceptTransferAsync(row.TransferId, null));
    }

    private async void Reject_Click(object sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(await _shell.RejectTransferAsync(row.TransferId));
    }

    private async void Cancel_Click(object sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(await _shell.CancelTransferAsync(row.TransferId));
    }

    private void Open_Click(object sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(_shell.OpenTransferFile(row, Window.GetWindow(this)));
    }

    private void Folder_Click(object sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(_shell.OpenContainingFolder(row.Path));
    }
}
