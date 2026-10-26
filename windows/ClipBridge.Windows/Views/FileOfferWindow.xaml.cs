using System.Windows;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Microsoft.Win32;

namespace ClipBridge.Windows.Views;

/// <summary>An incoming file offer: who, what, how big, where to save; accept or reject (nothing is ever auto-accepted).</summary>
public partial class FileOfferWindow : Window
{
    private readonly ShellController _shell = ShellController.Current;
    private readonly FileOfferedEvent _offer;
    private bool _isClosed;

    internal FileOfferWindow(FileOfferedEvent offer)
    {
        _offer = offer;
        InitializeComponent();
        Title = Strings.FileOfferTitle;
        var name = FileDisplay.Sanitize(offer.Name);
        FromText.Text = Strings.FileOfferFrom(offer.FromName.Length > 0 ? offer.FromName : offer.From);
        NameText.Text = FileDisplay.Shorten(name, 70);
        NameText.ToolTip = name;
        SizeText.Text = Formatting.Bytes(offer.Size);
        if (FileDisplay.IsExecutable(name))
        {
            DangerText.Text = Strings.FileOfferDanger;
            DangerText.Visibility = Visibility.Visible;
        }
        SaveLabel.Text = Strings.FileOfferSaveTo;
        DirBox.Text = _shell.Settings.DownloadDir;
        ChangeButton.Content = Strings.BtnChangeFolder;
        AcceptButton.Content = Strings.BtnAccept;
        RejectButton.Content = Strings.BtnReject;
        LaterButton.Content = Strings.FileOfferDecideLater;

        _shell.EventReceived += OnCoreEvent;
        Closed += (_, _) =>
        {
            _isClosed = true;
            _shell.EventReceived -= OnCoreEvent;
        };
        PreviewKeyDown += (_, e) => { if (e.Key == System.Windows.Input.Key.Escape) CloseSafe(); };
    }

    private void CloseSafe()
    {
        if (!_isClosed) Close();
    }

    /// <summary>The offer was decided elsewhere (transfer list, the sender cancelled, it expired): nothing left to ask.</summary>
    private void OnCoreEvent(CoreEvent ev)
    {
        var gone = ev switch
        {
            FileStateEvent s when s.TransferId == _offer.TransferId => s.State != "offered",
            FileFailedEvent f when f.TransferId == _offer.TransferId => true,
            FileDoneEvent d when d.TransferId == _offer.TransferId => true,
            _ => false,
        };
        if (gone) CloseSafe();
    }

    private void Change_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new OpenFolderDialog { Title = Strings.FileOfferSaveTo, InitialDirectory = DirBox.Text };
        if (dialog.ShowDialog(this) == true) DirBox.Text = dialog.FolderName;
    }

    private async void Accept_Click(object sender, RoutedEventArgs e)
    {
        if (!SettingsModel.IsAbsoluteWindowsPath(DirBox.Text))
        {
            StatusText.Text = Strings.ValidationDownloadDir;
            return;
        }
        SetBusy(true);
        var error = await _shell.AcceptTransferAsync(_offer.TransferId, DirBox.Text);
        if (_isClosed) return;
        if (error is null) { CloseSafe(); return; }
        StatusText.Text = error;
        SetBusy(false);
    }

    private async void Reject_Click(object sender, RoutedEventArgs e)
    {
        SetBusy(true);
        var error = await _shell.RejectTransferAsync(_offer.TransferId);
        if (_isClosed) return;
        if (error is null) { CloseSafe(); return; }
        StatusText.Text = error;
        SetBusy(false);
    }

    private void Later_Click(object sender, RoutedEventArgs e) => CloseSafe();

    private void SetBusy(bool busy)
    {
        AcceptButton.IsEnabled = !busy;
        RejectButton.IsEnabled = !busy;
        ChangeButton.IsEnabled = !busy;
    }
}
