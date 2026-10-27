using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The "笔记" page: the list of notes on the left, the editor on the right, a banner for conflict copies and a line with the
/// sync state at the bottom. It connects the pieces: the list (<see cref="NotesList"/>), the editing rules
/// (<see cref="NoteEditorSession"/> / <see cref="NoteEditorModel"/>) and the dialogs. Typed text is saved when the user pauses,
/// when the focus leaves the editor, when another note is chosen, when the page is hidden and when the application exits.
/// </summary>
public partial class NotesPage : UserControl
{
    private ShellController _shell = null!;
    private NoteEditorSession _session = null!;
    private readonly NotesList _list = new();
    private readonly DispatcherTimer _tick = new() { Interval = TimeSpan.FromMilliseconds(150) };
    private readonly DispatcherTimer _clock = new() { Interval = TimeSpan.FromSeconds(30) };
    private Coalescer _listReload = null!;
    private Coalescer _statusReload = null!;
    private Coalescer _conflictReload = null!;
    private IReadOnlyList<NoteConflict> _conflicts = Array.Empty<NoteConflict>();
    private bool _conflictsMore;
    private NotesSyncStatus? _syncStatus;
    private string _lastFailure = "";
    private bool _loaded;
    private bool _suppress;
    private int _openGeneration;
    private int _previewGeneration;

    public NotesPage() => InitializeComponent();

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        _session = new NoteEditorSession(shell.NotesGateway);
        _listReload = new Coalescer(RefreshListAsync);
        _statusReload = new Coalescer(RefreshStatusAsync);
        _conflictReload = new Coalescer(RefreshConflictsAsync);

        Editor.SetViewMode(shell.NotesView, notify: false);
        Editor.Edited += (title, body) => _session.OnEdited(title, body);
        Editor.FocusLeft += () => _ = Safe(_session.FlushAsync);
        Editor.SaveNowRequested += () => _ = Safe(_session.FlushAsync);
        Editor.HistoryRequested += async () => await Safe(ShowHistoryAsync);
        Editor.DeleteRequested += async () => await Safe(DeleteCurrentAsync);
        Editor.RestoreRequested += async () => await Safe(RestoreCurrentAsync);
        Editor.ViewModeChanged += OnViewModeChanged;
        Editor.LinkClicked += href =>
        {
            var error = _shell.OpenNoteLink(href);
            if (error is not null) _shell.ShowStatus(error);
        };

        _session.StateChanged += () => Editor.SetState(_session.Model);
        _session.TextReplaced += (title, body) => Editor.SetText(title, body);
        _session.Notice += ShowNotice;
        _session.Failed += message =>
        {
            _lastFailure = message;
            _shell.ShowStatus(message);
        };
        _session.RevisionChanged += (noteId, revId) => _ = ShowPreviewAsync(noteId, revId);
        _session.ConflictCopyMade += copyId => { _ = _conflictReload.RunAsync(); };

        shell.NotesChanged += OnNotesChanged;
        shell.NotesSyncUpdated += update => { if (_loaded) { _ = _statusReload.RunAsync(); } };
        shell.NotesConflict += OnNotesConflict;
        shell.NotesShutdown += () => _session.FlushBlocking();

        _tick.Tick += async (_, _) => await Safe(_session.TickAsync);
        _tick.Start();
        _clock.Tick += (_, _) => ApplySyncText();
        _clock.Start();
        IsVisibleChanged += (_, e) =>
        {
            if (!(bool)e.NewValue) _ = Safe(_session.FlushAsync); // another page, or the window went to the tray
        };
        Editor.ShowEmpty();
    }

    /// <summary>The page became the visible one: load the list on the first visit, refresh it later.</summary>
    public async void OnShown()
    {
        if (!_shell.CoreRunning)
        {
            ShowListMessage(Strings.NotesNotRunning);
            return;
        }
        if (!_loaded)
        {
            _loaded = true;
            await Safe(() => ShowListAsync(trash: false));
        }
        else _ = _listReload.RunAsync();
        _ = _statusReload.RunAsync();
        _ = _conflictReload.RunAsync();
    }

    /// <summary>Runs an async handler; whatever goes wrong is logged and never takes the application down.</summary>
    private static async Task Safe(Func<Task> work)
    {
        try { await work(); }
        catch (Exception ex) { AppLog.Error("notes page", ex); }
    }

    // ---- the list -----------------------------------------------------------------------------

    private async Task ShowListAsync(bool trash)
    {
        var r = await _shell.NotesCallAsync(api => { _list.Show(api, trash); return true; });
        if (!r.Ok)
        {
            ShowListMessage(r.Error!);
            return;
        }
        ApplyList();
    }

    private async Task RefreshListAsync()
    {
        var r = await _shell.NotesCallAsync(api => { _list.Refresh(api); return true; });
        if (r.Ok) ApplyList();
        else _shell.ShowStatus(r.Error!);
    }

    private void ShowListMessage(string text)
    {
        NoteList.ItemsSource = null;
        EmptyText.Text = text;
        EmptyText.Visibility = Visibility.Visible;
        MoreButton.Visibility = Visibility.Collapsed;
    }

    /// <summary>Shows what <see cref="_list"/> holds, keeping the open note selected.</summary>
    private void ApplyList(int scrollToIndex = -1)
    {
        var rows = NoteRow.From(_list.Items, DateTimeOffset.Now);
        var open = _session.Model.NoteId;
        _suppress = true;
        NoteList.ItemsSource = rows;
        NoteList.SelectedItem = open.Length > 0 ? rows.FirstOrDefault(r => r.NoteId == open) : null;
        _suppress = false;
        if (scrollToIndex >= 0 && scrollToIndex < rows.Count) NoteList.ScrollIntoView(rows[scrollToIndex]);
        EmptyText.Text = _list.Trash ? Strings.NotesTrashEmpty : Strings.NotesEmpty;
        EmptyText.Visibility = rows.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        MoreButton.Visibility = _list.HasMore ? Visibility.Visible : Visibility.Collapsed;
    }

    private void SelectOpenNote()
    {
        _suppress = true;
        NoteList.SelectedItem = (NoteList.ItemsSource as IEnumerable<NoteRow>)?.FirstOrDefault(r => r.NoteId == _session.Model.NoteId);
        _suppress = false;
    }

    private async void More_Click(object sender, RoutedEventArgs e)
    {
        var before = _list.Items.Count;
        var r = await _shell.NotesCallAsync(api => _list.LoadMore(api));
        if (r.Ok) ApplyList(before);
        else _shell.ShowStatus(r.Error!);
    }

    private async void NoteList_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (_suppress || NoteList.SelectedItem is not NoteRow row) return;
        await Safe(() => OpenNoteAsync(row.NoteId));
    }

    private async void Trash_Click(object sender, RoutedEventArgs e)
    {
        var wantTrash = TrashToggle.IsChecked == true;
        var switched = false;
        await Safe(async () => switched = await SwitchViewAsync(wantTrash));
        if (!switched) TrashToggle.IsChecked = _list.Trash; // staying where we were
    }

    /// <summary>Shows the live notes or the recycle bin. The open note is saved first; false when the user keeps it open.</summary>
    private async Task<bool> SwitchViewAsync(bool trash)
    {
        if (!await LeaveCurrentAsync()) return false;
        _openGeneration++;
        _session.Close();
        Editor.ShowEmpty();
        NewButton.IsEnabled = !trash;
        TrashToggle.IsChecked = trash;
        await ShowListAsync(trash);
        return true;
    }

    // ---- opening, creating, deleting --------------------------------------------------------------

    private async Task<bool> OpenNoteAsync(string noteId)
    {
        if (_session.Model.IsOpen && _session.Model.NoteId == noteId) return true;
        var generation = ++_openGeneration;
        if (!await LeaveCurrentAsync())
        {
            SelectOpenNote(); // the user keeps editing the note that is open
            return false;
        }
        var r = await _shell.NotesCallAsync(api => api.Get(noteId));
        if (generation != _openGeneration) return false; // another note was chosen meanwhile
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!);
            SelectOpenNote();
            return false;
        }
        ShowNote(r.Value!);
        return true;
    }

    private void ShowNote(NoteDetail note)
    {
        _session.Open(note);
        Editor.ShowNote(note);
        Editor.SetState(_session.Model);
        _ = ShowPreviewAsync(note.NoteId, note.RevId);
    }

    /// <summary>
    /// Saves the open note before leaving it. When that is not possible the user decides: true = go on (the typed text that
    /// could not be saved is dropped), false = stay.
    /// </summary>
    private async Task<bool> LeaveCurrentAsync()
    {
        var model = _session.Model;
        if (!model.IsOpen) return true;
        Editor.Freeze(true); // nothing may be typed while the note is saved and the next one is read
        try
        {
            if (await _session.FlushAsync()) return true;
            var reason = model.State switch
            {
                EditorSaveState.TooLarge => Strings.NoteStateTooLarge,
                EditorSaveState.Trashed => Strings.NoteStateTrashed,
                _ => _lastFailure,
            };
            return MessageBox.Show(Window.GetWindow(this)!, Strings.NoteLeaveUnsaved(reason), Strings.AppName,
                MessageBoxButton.YesNo, MessageBoxImage.Warning, MessageBoxResult.No) == MessageBoxResult.Yes;
        }
        finally { Editor.Freeze(false); }
    }

    private async void New_Click(object sender, RoutedEventArgs e) => await Safe(NewNoteAsync);

    private async Task NewNoteAsync()
    {
        if (_list.Trash && !await SwitchViewAsync(false)) return;
        var model = _session.Model;
        if (model.IsOpen && !model.InTrash && !model.Dirty && model.Title.Length == 0 && model.Body.Length == 0)
        {
            Editor.FocusTitle(); // an empty note is open already
            return;
        }
        var generation = ++_openGeneration;
        if (!await LeaveCurrentAsync()) return;
        var r = await _shell.NotesCallAsync(api =>
        {
            var created = api.Create();
            return api.Get(created.NoteId, created.RevId);
        });
        if (generation != _openGeneration) return;
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!);
            return;
        }
        ShowNote(r.Value!);
        await _listReload.RunAsync();
        Editor.FocusTitle();
    }

    private async Task DeleteCurrentAsync()
    {
        var model = _session.Model;
        if (!model.IsOpen || model.InTrash) return;
        var noteId = model.NoteId;
        if (!await LeaveCurrentAsync()) return;
        var r = await _shell.NotesCallAsync(api => api.Delete(noteId));
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!);
            return;
        }
        _openGeneration++;
        _session.Close();
        Editor.ShowEmpty();
        _shell.ShowStatus(Strings.NoteDeleted);
        await _listReload.RunAsync();
    }

    private async Task RestoreCurrentAsync()
    {
        if (!_session.Model.IsOpen) return;
        var fromTrashView = _list.Trash;
        if (!await _session.RestoreNoteAsync()) return; // the session has told the user why
        _shell.ShowStatus(Strings.NoteRestored);
        if (fromTrashView)
        {
            _openGeneration++;
            _session.Close();
            Editor.ShowEmpty();
        }
        await _listReload.RunAsync();
    }

    // ---- history ----------------------------------------------------------------------------------

    private async Task ShowHistoryAsync()
    {
        var model = _session.Model;
        if (!model.IsOpen) return;
        await _session.FlushAsync(); // what is typed becomes a revision first; if that fails the history simply lacks it
        var picked = NoteHistoryWindow.Pick(Window.GetWindow(this), _shell, model.NoteId, canRestore: !model.ReadOnly);
        if (picked is null) return;
        if (await _session.UseRevisionAsync(picked.Title, picked.Body))
            ShowNotice(Strings.NoteRevisionRestored(NotesTime.Stamp(picked.UpdatedAt)));
    }

    // ---- preview ----------------------------------------------------------------------------------

    private async Task ShowPreviewAsync(string noteId, string revId)
    {
        if (Editor.ViewMode == NotesViewMode.Source) return;
        var generation = ++_previewGeneration;
        var r = await _shell.NotesCallAsync(api => api.Render(noteId, revId));
        if (generation != _previewGeneration || _session.Model.NoteId != noteId) return;
        if (r.Ok) Editor.SetPreview(r.Value);
        else _shell.ShowStatus(r.Error!);
    }

    private void OnViewModeChanged(NotesViewMode mode)
    {
        _shell.SetNotesView(mode);
        var model = _session.Model;
        if (mode != NotesViewMode.Source && model.IsOpen) _ = ShowPreviewAsync(model.NoteId, model.BaseRevId);
    }

    // ---- events from the core ----------------------------------------------------------------------

    private void OnNotesChanged(NotesChangedEvent e)
    {
        if (_loaded) _ = _listReload.RunAsync();
        if (_session.Model.IsOpen) _ = Safe(() => _session.RemoteChangedAsync(e.NoteId, e.RevId));
    }

    private async void OnNotesConflict(NotesSyncConflictEvent e)
    {
        await Safe(async () =>
        {
            if (_loaded) await _conflictReload.RunAsync();
            ShowNotice(NotesSyncText.ConflictNotice(e, await TitleOfAsync(e.NoteId)));
        });
    }

    private async Task<string> TitleOfAsync(string noteId)
    {
        var known = _list.Items.FirstOrDefault(i => i.NoteId == noteId);
        if (known is not null) return known.Title;
        var r = await _shell.NotesCallAsync(api => api.Get(noteId));
        return r.Ok ? r.Value!.Title : "";
    }

    // ---- conflict copies --------------------------------------------------------------------------

    private async Task RefreshConflictsAsync()
    {
        var r = await _shell.NotesCallAsync(api => api.Conflicts());
        if (!r.Ok) return; // the banner stays as it was
        _conflicts = r.Value!.Items;
        _conflictsMore = r.Value.HasMore;
        ConflictBanner.Visibility = _conflicts.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        ConflictText.Text = Strings.NoteConflictBanner(_conflicts.Count, _conflictsMore);
    }

    private async void Conflicts_Click(object sender, RoutedEventArgs e)
    {
        await Safe(async () =>
        {
            await _session.FlushAsync(); // the original note may be the one open here
            NoteConflictWindow.ShowFor(Window.GetWindow(this), _shell, _conflicts);
            await _conflictReload.RunAsync();
            await _listReload.RunAsync();
        });
    }

    // ---- notices and the sync line -------------------------------------------------------------------

    private void ShowNotice(string text)
    {
        NoticeText.Text = text;
        NoticeBanner.Visibility = Visibility.Visible;
    }

    private void CloseNotice_Click(object sender, RoutedEventArgs e) => NoticeBanner.Visibility = Visibility.Collapsed;

    private async Task RefreshStatusAsync()
    {
        var r = await _shell.NotesCallAsync(api => api.SyncStatus());
        _syncStatus = r.Ok ? r.Value : null;
        if (r.Ok) ApplySyncText();
        else
        {
            SyncText.Text = r.Error ?? "";
            SyncText.ToolTip = null;
        }
    }

    private void ApplySyncText()
    {
        if (_syncStatus is not { } status) return;
        var now = DateTimeOffset.Now;
        SyncText.Text = NotesSyncText.Line(status, now);
        SyncText.ToolTip = status.Peers.Count > 0 ? NotesSyncText.Details(status, now) : null;
        var warn = status.Peers.Any(p => p.State is NotesSyncStates.PeerOld or NotesSyncStates.Error);
        SyncText.SetResourceReference(TextBlock.ForegroundProperty, warn ? "AppWarning" : "AppFaint");
    }
}
