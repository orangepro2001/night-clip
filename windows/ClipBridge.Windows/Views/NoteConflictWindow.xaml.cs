using System.Collections.ObjectModel;
using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// Shows a conflict copy next to the note it was set aside from and lets the user settle it: keep the note as it is (the copy
/// is dropped), put the copy's text in its place, or append the copy to the note. All three go through
/// <c>notes.sync_resolve</c>; the copy always ends up in the recycle bin, so nothing is lost for good.
/// </summary>
public partial class NoteConflictWindow : Window
{
    private readonly ShellController _shell;
    private readonly ObservableCollection<NoteConflictRow> _rows;
    private NoteConflict? _selected;
    private bool _busy;
    private int _generation;

    private NoteConflictWindow(ShellController shell, IReadOnlyList<NoteConflict> conflicts)
    {
        _shell = shell;
        _rows = new ObservableCollection<NoteConflictRow>(conflicts.Select(c => new NoteConflictRow(c)));
        InitializeComponent();
        Title = Strings.NoteConflictTitle;
        ConflictList.ItemsSource = _rows;
        EnableButtons(false);
        Loaded += (_, _) => { if (_rows.Count > 0) ConflictList.SelectedIndex = 0; };
        PreviewKeyDown += (_, e) => { if (e.Key == System.Windows.Input.Key.Escape) Close(); };
    }

    /// <summary>True when at least one conflict was settled.</summary>
    public bool Changed { get; private set; }

    internal static bool ShowFor(Window? owner, ShellController shell, IReadOnlyList<NoteConflict> conflicts)
    {
        if (conflicts.Count == 0) return false;
        var w = new NoteConflictWindow(shell, conflicts);
        if (owner is { IsVisible: true }) w.Owner = owner;
        else w.WindowStartupLocation = WindowStartupLocation.CenterScreen;
        w.ShowDialog();
        return w.Changed;
    }

    private void EnableButtons(bool enabled)
    {
        KeepMainButton.IsEnabled = enabled;
        KeepCopyButton.IsEnabled = enabled;
        MergeButton.IsEnabled = enabled;
    }

    private async void ConflictList_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (ConflictList.SelectedItem is not NoteConflictRow row) return;
        var generation = ++_generation;
        _selected = null;
        EnableButtons(false);
        ErrorText.Text = "";
        MainTitle.Text = CopyTitle.Text = Strings.NoteHistoryLoading;
        MainBox.Text = CopyBox.Text = "";
        var conflict = row.Conflict;
        var main = _shell.NotesCallAsync(api => api.Get(conflict.NoteId));
        var copy = _shell.NotesCallAsync(api => api.Get(conflict.ConflictNoteId));
        var mainResult = await main;
        var copyResult = await copy;
        if (generation != _generation) return; // another conflict was chosen meanwhile
        MainTitle.Text = CopyTitle.Text = "";
        if (!mainResult.Ok || !copyResult.Ok)
        {
            ErrorText.Text = mainResult.Error ?? copyResult.Error ?? "";
            return;
        }
        Show(MainTitle, MainBox, mainResult.Value!);
        Show(CopyTitle, CopyBox, copyResult.Value!);
        _selected = conflict;
        EnableButtons(!_busy);
    }

    private static void Show(TextBlock title, TextBox body, NoteDetail note)
    {
        title.Text = note.Title.Length > 0 ? note.Title : Strings.Untitled;
        body.Text = note.Body;
        body.ScrollToHome();
    }

    private async Task ResolveAsync(string action)
    {
        if (_selected is not { } conflict || _busy) return;
        _busy = true;
        EnableButtons(false);
        ErrorText.Text = "";
        var r = await _shell.NotesCallAsync(api => api.Resolve(conflict.NoteId, conflict.ConflictNoteId, action));
        _busy = false;
        if (r.Ok) Changed = true;
        if (!IsVisible) return; // the dialog was closed while the core worked
        if (!r.Ok)
        {
            ErrorText.Text = r.Error ?? "";
            EnableButtons(true);
            return;
        }
        var row = _rows.FirstOrDefault(x => x.Conflict.ConflictNoteId == conflict.ConflictNoteId);
        var index = row is null ? 0 : _rows.IndexOf(row);
        if (row is not null) _rows.Remove(row);
        if (_rows.Count == 0)
        {
            DialogResult = true;
            return;
        }
        ConflictList.SelectedIndex = Math.Min(index, _rows.Count - 1);
    }

    private async void KeepMain_Click(object sender, RoutedEventArgs e) => await ResolveAsync(ConflictActions.KeepMain);
    private async void KeepCopy_Click(object sender, RoutedEventArgs e) => await ResolveAsync(ConflictActions.KeepConflict);
    private async void Merge_Click(object sender, RoutedEventArgs e) => await ResolveAsync(ConflictActions.Merge);
    private void Later_Click(object sender, RoutedEventArgs e) => Close();
}
