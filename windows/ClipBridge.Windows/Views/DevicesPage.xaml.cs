using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Microsoft.Win32;

namespace ClipBridge.Windows.Views;

public partial class DevicesPage : UserControl
{
    private ShellController _shell = null!;

    public DevicesPage() => InitializeComponent();

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        PeerList.ItemsSource = shell.Peers;
        shell.Peers.CollectionChanged += (_, _) => UpdateEmpty();
        UpdateEmpty();
    }

    private void UpdateEmpty() =>
        EmptyText.Visibility = _shell.Peers.Count == 0 ? Visibility.Visible : Visibility.Collapsed;

    private static PeerItem? PeerOf(object sender) => (sender as FrameworkElement)?.DataContext as PeerItem;

    private Window? OwnerWindow => Window.GetWindow(this);

    private void Tell(string? message)
    {
        if (!string.IsNullOrEmpty(message)) _shell.ShowStatus(message);
    }

    // ---- toolbar ---------------------------------------------------------------------------

    private void Pair_Click(object sender, RoutedEventArgs e) => _shell.ShowPairListen();

    private void Join_Click(object sender, RoutedEventArgs e) => _shell.ShowPairJoin();

    private async void SendFiles_Click(object sender, RoutedEventArgs e) => await SendFlowAsync(null);

    // ---- per-device actions --------------------------------------------------------------------

    private static string? NonEmpty(string? s) => string.IsNullOrWhiteSpace(s) ? null : s;

    /// <summary>Top "发送文字…": typed text to every device online now.</summary>
    private async void SendTextAll_Click(object sender, RoutedEventArgs e)
    {
        if (InputWindow.Ask(OwnerWindow, Strings.SendTextAllTitle, Strings.SendTextAllPrompt, "", NonEmpty) is { } text)
            Tell(await _shell.SendTextAsync(text, null));
    }

    /// <summary>A card's "发送文字…": typed text to that device only (queued while it is offline).</summary>
    private async void SendTextOne_Click(object sender, RoutedEventArgs e)
    {
        if (PeerOf(sender) is not { } peer) return;
        if (InputWindow.Ask(OwnerWindow, Strings.SendTextOneTitle(peer.DisplayName), Strings.SendTextOnePrompt, "", NonEmpty) is { } text)
            Tell(await _shell.SendTextAsync(text, new[] { peer.DeviceId }));
    }

    private void SendClipOne_Click(object sender, RoutedEventArgs e)
    {
        if (PeerOf(sender) is { } peer) _shell.SendClipboardTo(peer);
    }

    private async void SendOne_Click(object sender, RoutedEventArgs e)
    {
        if (PeerOf(sender) is { } peer) await SendFlowAsync(peer);
    }

    /// <summary>Choose a device (unless given), choose files, offer them one by one.</summary>
    private async Task SendFlowAsync(PeerItem? preselected)
    {
        var peer = preselected;
        if (peer is null)
        {
            var candidates = _shell.Peers.Where(p => p.CanSendFile).ToList();
            if (candidates.Count == 0)
            {
                MessageBox.Show(OwnerWindow!, Strings.FileNoPeers, Strings.AppName, MessageBoxButton.OK, MessageBoxImage.Information);
                return;
            }
            peer = candidates.Count == 1 ? candidates[0] : PickPeerWindow.Pick(OwnerWindow, candidates);
            if (peer is null) return;
        }

        var dialog = new OpenFileDialog { Title = Strings.FilePickTitle, Multiselect = true, CheckFileExists = true };
        if (dialog.ShowDialog(OwnerWindow) != true) return;
        await SendPathsAsync(peer, dialog.FileNames);
    }

    private async Task SendPathsAsync(PeerItem peer, IEnumerable<string> paths)
    {
        var error = await _shell.SendFilesAsync(peer, paths);
        if (error is not null)
            MessageBox.Show(OwnerWindow!, error, Strings.FileFailedTitle, MessageBoxButton.OK, MessageBoxImage.Warning);
        MainWindow.Instance?.ShowPage(MainPage.Transfers);
    }

    private void Peer_DragOver(object sender, DragEventArgs e)
    {
        var ok = PeerOf(sender) is { CanSendFile: true } && e.Data.GetDataPresent(DataFormats.FileDrop);
        e.Effects = ok ? DragDropEffects.Copy : DragDropEffects.None;
        e.Handled = true;
    }

    private async void Peer_Drop(object sender, DragEventArgs e)
    {
        e.Handled = true;
        if (PeerOf(sender) is not { CanSendFile: true } peer) return;
        if (e.Data.GetData(DataFormats.FileDrop) is not string[] paths || paths.Length == 0) return;
        await SendPathsAsync(peer, paths);
    }

    private async void Mode_Changed(object sender, SelectionChangedEventArgs e)
    {
        // Programmatic selection (binding the stored mode) never differs from the item; only a user choice does.
        if (sender is ComboBox { IsLoaded: true, SelectedValue: string mode } box && box.DataContext is PeerItem peer && mode != peer.ApplyMode)
            Tell(await _shell.SetPeerModeAsync(peer, mode));
    }

    private async void Send_Click(object sender, RoutedEventArgs e)
    {
        if (sender is CheckBox { DataContext: PeerItem peer } box)
            Tell(await _shell.SetPeerSendAsync(peer, box.IsChecked == true));
    }

    private async void Rename_Click(object sender, RoutedEventArgs e)
    {
        if (PeerOf(sender) is not { } peer) return;
        var name = InputWindow.Ask(OwnerWindow, Strings.RenameTitle, Strings.RenamePrompt, peer.DisplayName, PeerItem.NormalizeName);
        if (name is null) return;
        Tell(await _shell.RenamePeerAsync(peer, name));
    }

    private async void Reconnect_Click(object sender, RoutedEventArgs e)
    {
        if (PeerOf(sender) is not { } peer) return;
        if (peer.IsUnpaired)
        {
            var answer = MessageBox.Show(OwnerWindow!, Strings.RepairHint + "\n\n" + Strings.RepairNow + "?",
                Strings.ConfirmTitle, MessageBoxButton.YesNo, MessageBoxImage.Question);
            if (answer != MessageBoxResult.Yes) return;
            var error = await _shell.RemovePeerAsync(peer, forget: true);
            Tell(error);
            if (error is null) _shell.ShowPairListen();
            return;
        }
        Tell(await _shell.ReconnectPeerAsync(peer));
    }

    private async void Unpair_Click(object sender, RoutedEventArgs e)
    {
        if (PeerOf(sender) is not { } peer) return;
        var answer = MessageBox.Show(OwnerWindow!, Strings.ConfirmUnpair(peer.DisplayName), Strings.ConfirmTitle,
            MessageBoxButton.YesNo, MessageBoxImage.Warning, MessageBoxResult.No);
        if (answer == MessageBoxResult.Yes) Tell(await _shell.RemovePeerAsync(peer, forget: false));
    }

    private async void Forget_Click(object sender, RoutedEventArgs e)
    {
        if (PeerOf(sender) is not { } peer) return;
        var answer = MessageBox.Show(OwnerWindow!, Strings.ConfirmForget(peer.DisplayName), Strings.ConfirmTitle,
            MessageBoxButton.YesNo, MessageBoxImage.Warning, MessageBoxResult.No);
        if (answer == MessageBoxResult.Yes) Tell(await _shell.RemovePeerAsync(peer, forget: true));
    }
}
