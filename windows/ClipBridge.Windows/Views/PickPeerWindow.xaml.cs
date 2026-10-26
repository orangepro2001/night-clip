using System.Windows;
using System.Windows.Input;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Views;

/// <summary>Picks the receiving device for "发送文件…".</summary>
public partial class PickPeerWindow : Window
{
    private PickPeerWindow(IReadOnlyList<PeerItem> peers)
    {
        InitializeComponent();
        Title = Strings.FilePickPeerTitle;
        PromptText.Text = Strings.FilePickPeerPrompt;
        OkButton.Content = Strings.BtnOk;
        CancelButton.Content = Strings.BtnCancel;
        PeerList.ItemsSource = peers;
        PeerList.SelectedIndex = 0;
        PreviewKeyDown += (_, e) => { if (e.Key == Key.Escape) DialogResult = false; };
    }

    public PeerItem? Selected { get; private set; }

    public static PeerItem? Pick(Window? owner, IReadOnlyList<PeerItem> peers)
    {
        var w = new PickPeerWindow(peers);
        if (owner is { IsVisible: true }) w.Owner = owner;
        else w.WindowStartupLocation = WindowStartupLocation.CenterScreen;
        return w.ShowDialog() == true ? w.Selected : null;
    }

    private void Ok_Click(object sender, RoutedEventArgs e)
    {
        Selected = PeerList.SelectedItem as PeerItem;
        if (Selected is null) return;
        DialogResult = true;
    }

    private void Cancel_Click(object sender, RoutedEventArgs e) => DialogResult = false;

    private void PeerList_MouseDoubleClick(object sender, MouseButtonEventArgs e) => Ok_Click(sender, e);
}
