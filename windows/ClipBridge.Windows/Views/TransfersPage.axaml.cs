using System.Collections.ObjectModel;
using System.ComponentModel;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Interactivity;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>File transfers, newest first: filter (all / in progress / finished), accept, reject, cancel, open.</summary>
public partial class TransfersPage : UserControl
{
    private readonly ShellController _shell;
    private readonly ObservableCollection<TransferRow> _view = new();
    private readonly HashSet<TransferRow> _watched = new();

    public TransfersPage() : this(ShellController.Current) { }

    internal TransfersPage(ShellController shell)
    {
        _shell = shell;
        InitializeComponent();
        FilterAll.Content = Strings.TransfersFilterAll;
        FilterActive.Content = Strings.TransfersFilterActive;
        FilterDone.Content = Strings.TransfersFilterDone;
        Filter.SelectedIndex = 0;
        FolderText.Text = Strings.TransfersOpenDownloads;
        ToolTip.SetTip(RefreshButton, Strings.BtnRefresh);
        Items.ItemsSource = _view;
        shell.Transfers.Rows.CollectionChanged += (_, _) => Refilter();
        Refilter();
    }

    public async void OnShown() => await _shell.ReloadTransfersAsync();

    private void Filter_Changed(object? sender, SelectionChangedEventArgs e) => Refilter();

    private bool Matches(TransferRow row) => Filter.SelectedIndex switch
    {
        1 => row.IsActive,
        2 => row.IsTerminal,
        _ => true,
    };

    /// <summary>A row changing state (e.g. finishing) may move between the "in progress" and "finished" views.</summary>
    private void OnRowChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (Filter.SelectedIndex == 0 || sender is not TransferRow row) return;
        if (Matches(row) != _view.Contains(row)) Refilter();
    }

    private void Refilter()
    {
        if (Items is null) return;
        foreach (var row in _watched) row.PropertyChanged -= OnRowChanged;
        _watched.Clear();
        _view.Clear();
        foreach (var row in _shell.Transfers.Rows)
        {
            row.PropertyChanged += OnRowChanged;
            _watched.Add(row);
            if (Matches(row)) _view.Add(row);
        }
        var any = _shell.Transfers.Rows.Count > 0;
        ListCard.IsVisible = _view.Count > 0;
        Empty.IsVisible = _view.Count == 0;
        Empty.Title = any ? Strings.TransfersNoMatch : Strings.TransfersEmptyTitle;
        Empty.Description = any ? null : Strings.TransfersEmptyHint;
    }

    private static TransferRow? RowOf(object? sender) => (sender as StyledElement)?.DataContext as TransferRow;

    private void Tell(string? error)
    {
        if (!string.IsNullOrEmpty(error)) _shell.ShowStatus(error, StatusKind.Error);
    }

    private async void Refresh_Click(object? sender, RoutedEventArgs e) => await _shell.ReloadTransfersAsync();

    private void Folder_Click(object? sender, RoutedEventArgs e) => Tell(_shell.OpenFolder(_shell.Settings.DownloadDir));

    private async void Accept_Click(object? sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(await _shell.AcceptTransferAsync(row.TransferId, null));
    }

    private async void Reject_Click(object? sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(await _shell.RejectTransferAsync(row.TransferId));
    }

    private async void Cancel_Click(object? sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(await _shell.CancelTransferAsync(row.TransferId));
    }

    private async void Open_Click(object? sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(await _shell.OpenTransferFileAsync(row, this));
    }

    private void Folder_Row_Click(object? sender, RoutedEventArgs e)
    {
        if (RowOf(sender) is { } row) Tell(_shell.OpenContainingFolder(row.Path));
    }
}
