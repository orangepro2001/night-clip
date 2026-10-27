using System.Collections.ObjectModel;
using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The revisions of a note. Choosing one shows its text read-only; "恢复此版本" hands that text back to the page, which stores
/// it as a normal edit (so nothing is lost: the version it replaces stays in the history).
/// </summary>
public partial class NoteHistoryWindow : Window
{
    private const int PageSize = 50;

    private readonly ShellController _shell;
    private readonly string _noteId;
    private readonly bool _canRestore;
    private readonly ObservableCollection<NoteRevisionRow> _rows = new();
    private string _cursor = "";
    private NoteDetail? _shown;
    private int _generation;

    private NoteHistoryWindow(ShellController shell, string noteId, bool canRestore)
    {
        _shell = shell;
        _noteId = noteId;
        _canRestore = canRestore;
        InitializeComponent();
        Title = Strings.NoteHistoryTitle;
        RevList.ItemsSource = _rows;
        InfoText.Text = Strings.NoteHistoryLoading;
        Rendered.ResolveAttachment = (blobId, wantPicture) => Task.Run(() =>
        {
            try { return _shell.Attachments.Resolve(blobId, wantPicture); }
            catch (Exception) { return AttachmentView.Failure(blobId, "INTERNAL"); }
        });
        Rendered.LinkClicked += href =>
        {
            if (_shell.OpenNoteLink(href) is { } error) InfoText.Text = error;
        };
        Loaded += async (_, _) => await LoadPageAsync();
        PreviewKeyDown += (_, e) => { if (e.Key == System.Windows.Input.Key.Escape) Close(); };
    }

    /// <summary>The revision the user wants back (its title and text), or null.</summary>
    public NoteDetail? Picked { get; private set; }

    internal static NoteDetail? Pick(Window? owner, ShellController shell, string noteId, bool canRestore)
    {
        var w = new NoteHistoryWindow(shell, noteId, canRestore);
        if (owner is { IsVisible: true }) w.Owner = owner;
        else w.WindowStartupLocation = WindowStartupLocation.CenterScreen;
        return w.ShowDialog() == true ? w.Picked : null;
    }

    private string DeviceName(string deviceId) => _shell.Peers.FirstOrDefault(p => p.DeviceId == deviceId)?.DisplayName ?? "";

    private async Task LoadPageAsync()
    {
        var cursor = _cursor;
        var r = await _shell.NotesCallAsync(api => api.History(_noteId, PageSize, cursor));
        if (!r.Ok)
        {
            InfoText.Text = r.Error;
            return;
        }
        foreach (var revision in r.Value!.Items) _rows.Add(new NoteRevisionRow(revision, _shell.DeviceId, DeviceName));
        _cursor = r.Value.NextCursor;
        MoreButton.Visibility = _cursor.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        if (_rows.Count == 0) InfoText.Text = Strings.NoteHistoryEmpty;
        else if (RevList.SelectedIndex < 0) RevList.SelectedIndex = 0;
    }

    private async void More_Click(object sender, RoutedEventArgs e) => await LoadPageAsync();

    private async void RevList_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (RevList.SelectedItem is not NoteRevisionRow row) return;
        var generation = ++_generation;
        _shown = null;
        UseButton.IsEnabled = false;
        var r = await _shell.NotesCallAsync(api => api.Get(_noteId, row.Revision.RevId));
        if (generation != _generation) return; // another revision was chosen meanwhile
        if (!r.Ok)
        {
            BodyBox.Text = "";
            InfoText.Text = r.Error;
            return;
        }
        var d = r.Value!;
        _shown = d;
        BodyBox.Text = d.Body;
        BodyBox.ScrollToHome();
        InfoText.Text = d.Deleted ? Strings.NoteRevisionDeletedInfo
            : Strings.NoteRevisionInfo(d.Title.Length > 0 ? d.Title : Strings.Untitled, d.IsHead);
        UseButton.IsEnabled = _canRestore && !d.Deleted && !d.IsHead;
        if (_rendered) await RenderShownAsync(generation);
    }

    // ---- source or rendered ----------------------------------------------------------------------

    private bool _rendered;

    private void ShowSource_Click(object sender, RoutedEventArgs e) => SetRendered(false);

    private void ShowRendered_Click(object sender, RoutedEventArgs e) => SetRendered(true);

    private async void SetRendered(bool rendered)
    {
        _rendered = rendered;
        ShowSource.IsChecked = !rendered;
        ShowRendered.IsChecked = rendered;
        BodyBox.Visibility = rendered ? Visibility.Collapsed : Visibility.Visible;
        RenderedFrame.Visibility = rendered ? Visibility.Visible : Visibility.Collapsed;
        if (rendered) await RenderShownAsync(_generation);
    }

    /// <summary>Draws the revision that is shown from the core's render tree of that revision (<c>notes.render {note_id, rev_id}</c>).</summary>
    private async Task RenderShownAsync(int generation)
    {
        if (_shown is not { Deleted: false } d)
        {
            Rendered.Clear();
            return;
        }
        var r = await _shell.NotesCallAsync(api => api.Render(_noteId, d.RevId));
        if (generation != _generation) return;
        if (r.Ok) Rendered.Show(r.Value);
        else InfoText.Text = r.Error;
    }

    private void Use_Click(object sender, RoutedEventArgs e)
    {
        if (_shown is null) return;
        Picked = _shown;
        DialogResult = true;
    }

    private void Close_Click(object sender, RoutedEventArgs e) => DialogResult = false;
}
