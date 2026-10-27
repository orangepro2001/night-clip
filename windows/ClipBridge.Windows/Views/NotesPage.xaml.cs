using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Microsoft.Win32;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The "笔记" page: the search box and the list of notes on the left, the editor on the right, a banner for conflict copies and a
/// line with the sync state at the bottom. It connects the pieces: the list (<see cref="NotesList"/>), the search
/// (<see cref="NoteSearchResults"/>), the editing rules (<see cref="NoteEditorSession"/> / <see cref="NoteEditorModel"/>), the live
/// preview (<see cref="LivePreview"/>) and the dialogs. Typed text is saved when the user pauses, when the focus leaves the editor,
/// when another note is chosen, when the page is hidden and when the application exits.
/// </summary>
public partial class NotesPage : UserControl
{
    private ShellController _shell = null!;
    private NoteEditorSession _session = null!;
    private readonly NotesList _list = new();
    private readonly NoteSearchResults _hits = new();
    private readonly DebouncedWork<string> _searchWork = new(SearchInput.DebounceMs);
    private readonly LivePreview _livePreview = new();
    private readonly DispatcherTimer _tick = new() { Interval = TimeSpan.FromMilliseconds(50) };
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

    public NotesPage() => InitializeComponent();

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        _session = new NoteEditorSession(shell.NotesGateway);
        _listReload = new Coalescer(RefreshListAsync);
        _statusReload = new Coalescer(RefreshStatusAsync);
        _conflictReload = new Coalescer(RefreshConflictsAsync);

        Editor.SetViewMode(shell.NotesView, notify: false);
        Editor.Edited += (title, body) =>
        {
            _session.OnEdited(title, body);
            RequestPreview();
        };
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
        Editor.AttachRequested += async (paths, temporary) => await Safe(() => AttachFilesAsync(paths, temporary));
        Editor.Message += _shell.ShowStatus;
        var preview = Editor.PreviewControl;
        preview.ResolveAttachment = ResolveAttachmentAsync;
        preview.AttachmentActivated += async view => await Safe(() => OpenAttachmentAsync(view));

        _session.StateChanged += () => Editor.SetState(_session.Model);
        _session.TextReplaced += (title, body) =>
        {
            Editor.SetText(title, body);
            RequestPreview();
        };
        _session.Notice += ShowNotice;
        _session.Failed += message =>
        {
            _lastFailure = message;
            _shell.ShowStatus(message);
        };
        _session.ConflictCopyMade += copyId => { _ = _conflictReload.RunAsync(); };

        shell.NotesChanged += OnNotesChanged;
        shell.NotesSyncUpdated += update => { if (_loaded) { _ = _statusReload.RunAsync(); } };
        shell.NotesConflict += OnNotesConflict;
        shell.NotesAttachmentArrived += arrived => preview.AttachmentArrived(arrived.BlobId);
        shell.NotesShutdown += () => _session.FlushBlocking();

        _tick.Tick += (_, _) =>
        {
            _ = Safe(_session.TickAsync);
            _ = Safe(PumpPreviewAsync);
            _ = Safe(PumpSearchAsync);
        };
        _tick.Start();
        PreviewKeyDown += OnPageKeyDown;
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

    /// <summary>Shows what <see cref="_list"/> holds (or the hits while a search is active), keeping the open note selected.</summary>
    private void ApplyList(int scrollToIndex = -1)
    {
        if (_hits.Active)
        {
            ApplyHits(scrollToIndex);
            return;
        }
        Show(NoteRow.From(_list.Items, DateTimeOffset.Now), scrollToIndex, _list.Trash ? Strings.NotesTrashEmpty : Strings.NotesEmpty, _list.HasMore);
    }

    private void ApplyHits(int scrollToIndex = -1) =>
        Show(NoteRow.From(_hits.Items, DateTimeOffset.Now), scrollToIndex, Strings.NotesSearchEmpty(_hits.Query), _hits.HasMore);

    private void Show(IReadOnlyList<NoteRow> rows, int scrollToIndex, string emptyText, bool hasMore)
    {
        var open = _session.Model.NoteId;
        _suppress = true;
        NoteList.ItemsSource = rows;
        NoteList.SelectedItem = open.Length > 0 ? rows.FirstOrDefault(r => r.NoteId == open) : null;
        _suppress = false;
        if (scrollToIndex >= 0 && scrollToIndex < rows.Count) NoteList.ScrollIntoView(rows[scrollToIndex]);
        EmptyText.Text = emptyText;
        EmptyText.Visibility = rows.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        MoreButton.Visibility = hasMore ? Visibility.Visible : Visibility.Collapsed;
    }

    private void SelectOpenNote()
    {
        _suppress = true;
        NoteList.SelectedItem = (NoteList.ItemsSource as IEnumerable<NoteRow>)?.FirstOrDefault(r => r.NoteId == _session.Model.NoteId);
        _suppress = false;
    }

    private async void More_Click(object sender, RoutedEventArgs e)
    {
        if (_hits.Active)
        {
            await Safe(LoadMoreHitsAsync);
            return;
        }
        var before = _list.Items.Count;
        var r = await _shell.NotesCallAsync(api => _list.LoadMore(api));
        if (r.Ok) ApplyList(before);
        else _shell.ShowStatus(r.Error!);
    }

    // ---- search -------------------------------------------------------------------------------

    private void SearchBox_TextChanged(object sender, TextChangedEventArgs e)
    {
        SearchHint.Visibility = SearchBox.Text.Length == 0 ? Visibility.Visible : Visibility.Collapsed;
        if (_suppress) return;
        var query = NoteSearchResults.Normalize(SearchBox.Text);
        if (query.Length == 0)
        {
            // An empty query is the normal list again, at once; whatever the search is still doing no longer matters.
            _searchWork.Cancel();
            if (_hits.Active)
            {
                _hits.Clear();
                ApplyList();
                _ = _listReload.RunAsync();
            }
            return;
        }
        _searchWork.Post(query, DateTimeOffset.UtcNow);
    }

    /// <summary>Esc empties the search box.</summary>
    private void SearchBox_PreviewKeyDown(object sender, KeyEventArgs e)
    {
        if (e.Key != Key.Escape || SearchBox.Text.Length == 0) return;
        e.Handled = true;
        SearchBox.Clear();
    }

    /// <summary>Ctrl+F goes to the search box.</summary>
    private void OnPageKeyDown(object sender, KeyEventArgs e)
    {
        if (e.Key != Key.F || Keyboard.Modifiers != ModifierKeys.Control || !SearchBox.IsEnabled) return;
        e.Handled = true;
        SearchBox.Focus();
        SearchBox.SelectAll();
    }

    /// <summary>Runs the search once the typing has paused; an answer that the next keystroke overtook is dropped.</summary>
    private async Task PumpSearchAsync()
    {
        var job = _searchWork.TakeDue(DateTimeOffset.UtcNow);
        if (job is null) return;
        var r = await _shell.NotesCallAsync(api => api.Search(job.Value, NoteSearchResults.PageSize));
        if (!_searchWork.Finish(job)) return; // the query changed meanwhile, or was emptied
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!);
            return;
        }
        _hits.Replace(job.Value, r.Value!);
        ApplyHits();
    }

    private async Task LoadMoreHitsAsync()
    {
        var query = _hits.Query;
        var cursor = _hits.NextCursor;
        if (cursor.Length == 0) return;
        var before = _hits.Items.Count;
        var r = await _shell.NotesCallAsync(api => api.Search(query, NoteSearchResults.PageSize, cursor));
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!);
            return;
        }
        if (_hits.Append(query, r.Value!)) ApplyHits(before); // false: the query changed meanwhile
    }

    /// <summary>Leaves the search without touching the list; the box is emptied silently.</summary>
    private void EndSearch()
    {
        _searchWork.Cancel();
        _hits.Clear();
        _suppress = true;
        SearchBox.Clear();
        _suppress = false;
        SearchHint.Visibility = Visibility.Visible;
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
        _livePreview.Reset();
        Editor.ShowEmpty();
        NewButton.IsEnabled = !trash;
        TrashToggle.IsChecked = trash;
        if (trash) EndSearch(); // the search looks through live notes only
        SearchBox.IsEnabled = !trash;
        SearchBox.ToolTip = trash ? Strings.NoteSearchDisabledInTrash : null;
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
        _livePreview.Reset();
        RequestPreview(immediately: true);
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
        if (_hits.Active)
        {
            EndSearch(); // a new note would not be among the hits
            ApplyList();
        }
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

    /// <summary>
    /// The preview shows what is in the editor, saved or not: the text goes to <c>notes.render {body}</c> about 150 ms after the
    /// last keystroke (see <see cref="LivePreview"/>). Nothing is rendered while only the source is visible.
    /// </summary>
    private void RequestPreview(bool immediately = false)
    {
        var model = _session.Model;
        if (Editor.ViewMode == NotesViewMode.Source || !model.IsOpen) return;
        _livePreview.Request(model.Body, DateTimeOffset.UtcNow, immediately);
    }

    private async Task PumpPreviewAsync()
    {
        var job = _livePreview.TakeDue(DateTimeOffset.UtcNow);
        if (job is null) return;
        var r = await _shell.NotesCallAsync(api => api.RenderBody(job.Value));
        if (!r.Ok)
        {
            _livePreview.Fail(job); // the last tree that rendered stays on screen
            return;
        }
        if (_livePreview.Accept(job)) Editor.SetPreview(r.Value);
    }

    private void OnViewModeChanged(NotesViewMode mode)
    {
        _shell.SetNotesView(mode);
        if (mode == NotesViewMode.Source) return;
        _livePreview.Reset(); // what the preview shows may be older than the text by now
        RequestPreview(immediately: true);
    }

    // ---- attachments: the preview side ---------------------------------------------------------------

    /// <summary>Looks an attachment up for the preview, off the UI thread. Never throws.</summary>
    private Task<AttachmentView> ResolveAttachmentAsync(string blobId, bool wantPicture) => Task.Run(() =>
    {
        try { return _shell.Attachments.Resolve(blobId, wantPicture); }
        catch (Exception ex)
        {
            AppLog.Error("attachment lookup", ex);
            return AttachmentView.Failure(blobId, "INTERNAL");
        }
    });

    /// <summary>A click on a picture or a file chip: exports the file under its real name and opens it with the default program.</summary>
    private async Task OpenAttachmentAsync(AttachmentView view)
    {
        if (AttachmentPolicy.IsRisky(view.Name) &&
            MessageBox.Show(Window.GetWindow(this)!, Strings.AttachmentRiskyText(view.Name), Strings.AttachmentRiskyTitle,
                MessageBoxButton.YesNo, MessageBoxImage.Warning, MessageBoxResult.No) != MessageBoxResult.Yes)
            return;
        var r = await _shell.AttachmentCallAsync(resolver => resolver.ExportForOpening(view.BlobId));
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!);
            return;
        }
        var error = _shell.OpenAttachmentFile(r.Value.Path);
        if (error is not null) _shell.ShowStatus(error);
    }

    // ---- attachments: adding them ----------------------------------------------------------------------

    /// <summary>
    /// Stores files as attachments (picked, dropped or pasted) and puts their Markdown into the note at the caret. A temporary
    /// file (a pasted picture) is deleted afterwards. If another note was opened meanwhile nothing is inserted.
    /// </summary>
    private async Task AttachFilesAsync(IReadOnlyList<string> paths, bool temporary)
    {
        var noteId = _session.Model.NoteId;
        try
        {
            foreach (var path in paths)
            {
                var name = FileDisplay.FileNameOf(path);
                if (!_session.Model.IsOpen || _session.Model.ReadOnly) return;
                if (Directory.Exists(path))
                {
                    _shell.ShowStatus(Strings.AttachFolder(name));
                    continue;
                }
                _shell.ShowStatus(Strings.AttachAdding);
                var r = await _shell.NotesCallAsync(api => api.AttachmentAdd(path), ErrorMessages.ForNotesAttachment);
                if (!r.Ok)
                {
                    _shell.ShowStatus(Strings.AttachFailed(name, r.Error!));
                    continue;
                }
                if (_session.Model.NoteId != noteId)
                {
                    ShowNotice(Strings.AttachSwitchedNote);
                    return;
                }
                Editor.InsertBlock(r.Value!.Markdown);
                _shell.ShowStatus(Strings.AttachAdded(r.Value.Name));
            }
        }
        finally
        {
            if (temporary)
                foreach (var path in paths) NoteEditorPane.DeleteTemporary(path);
        }
    }

    // ---- events from the core ----------------------------------------------------------------------

    private void OnNotesChanged(NotesChangedEvent e)
    {
        // While a search is shown its hits are asked for again (the list underneath is brought up to date when the search ends).
        if (_hits.Active) _searchWork.PostNow(_hits.Query, DateTimeOffset.UtcNow);
        else if (_loaded) _ = _listReload.RunAsync();
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

    // ---- menu: export, import, cleanup of attachments -------------------------------------------------------

    private void Menu_Click(object sender, RoutedEventArgs e)
    {
        NotesMenu.PlacementTarget = MenuButton;
        NotesMenu.IsOpen = true;
    }

    private async void Export_Click(object sender, RoutedEventArgs e) => await Safe(ExportAsync);

    private async Task ExportAsync()
    {
        await _session.FlushAsync(); // what is typed belongs in the export
        if (NoteExportWindow.Ask(Window.GetWindow(this)) is not { } choice) return;
        _shell.ShowStatus(Strings.NoteExportTitle + "…");
        var r = await _shell.NotesCallAsync(api => NotesTransfer.ExportTo(api, choice.Path, choice.IncludeTrash), ErrorMessages.ForNotesTransfer);
        if (r.Ok)
        {
            ShowNotice(NotesTransferText.ExportDone(r.Value!));
            return;
        }
        MessageBox.Show(Window.GetWindow(this)!, r.Error!, Strings.NoteExportTitle, MessageBoxButton.OK, MessageBoxImage.Warning);
    }

    private async void Import_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new OpenFileDialog { Title = Strings.NoteImportPickTitle, Filter = Strings.NoteImportFilter, CheckFileExists = true };
        if (dialog.ShowDialog(Window.GetWindow(this)) == true) await Safe(() => ImportAsync(dialog.FileName));
    }

    private async void ImportFolder_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new OpenFolderDialog { Title = Strings.NoteImportFolderTitle };
        if (dialog.ShowDialog(Window.GetWindow(this)) == true) await Safe(() => ImportAsync(dialog.FolderName));
    }

    /// <summary>Imports a .zip, a text file or a folder as new notes, shows what happened and brings the list up to date.</summary>
    private async Task ImportAsync(string path)
    {
        var r = await _shell.NotesCallAsync(api => NotesTransfer.Import(api, path), ErrorMessages.ForNotesTransfer);
        if (!r.Ok)
        {
            MessageBox.Show(Window.GetWindow(this)!, r.Error!, Strings.NoteImportResultTitle, MessageBoxButton.OK, MessageBoxImage.Warning);
            return;
        }
        if (_list.Trash) await SwitchViewAsync(false);
        await _listReload.RunAsync();
        if (_hits.Active) _searchWork.PostNow(_hits.Query, DateTimeOffset.UtcNow);
        if (r.Value!.Skipped.Count == 0) ShowNotice(NotesTransferText.ImportSummary(r.Value));
        else NoteImportResultWindow.Show(Window.GetWindow(this), r.Value);
    }

    private async void Cleanup_Click(object sender, RoutedEventArgs e) => await Safe(CleanupAsync);

    private async Task CleanupAsync()
    {
        if (MessageBox.Show(Window.GetWindow(this)!, Strings.NoteCleanupConfirm, Strings.MenuNoteCleanup,
                MessageBoxButton.YesNo, MessageBoxImage.Question, MessageBoxResult.No) != MessageBoxResult.Yes) return;
        var r = await _shell.NotesCallAsync(api => api.AttachmentCleanup(), ErrorMessages.ForNotesAttachment);
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!);
            return;
        }
        _shell.Attachments.Forget();
        ShowNotice(NotesTransferText.CleanupDone(r.Value!));
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
