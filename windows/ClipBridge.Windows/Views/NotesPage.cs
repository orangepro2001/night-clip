using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Primitives;
using Avalonia.Controls.Templates;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Platform.Storage;
using Avalonia.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The "笔记" page: the search box and the list of notes on the left, the editor on the right, banners for notices and conflict
/// copies, and a line with the sync state at the bottom. It connects the pieces: the list (<see cref="NotesList"/>), the search
/// (<see cref="NoteSearchResults"/>), the editing rules (<see cref="NoteEditorSession"/> / <see cref="NoteEditorModel"/>), the live
/// preview (<see cref="LivePreview"/>) and the dialogs. Typed text is saved when the user pauses, when the focus leaves the editor,
/// when another note is chosen, when the page is hidden and when the application exits.
/// </summary>
internal sealed class NotesPage : UserControl, ISearchablePage
{
    private readonly ShellController _shell;
    private readonly NoteEditorSession _session;
    private readonly NotesList _list = new();
    private readonly NoteSearchResults _hits = new();
    private readonly DebouncedWork<string> _searchWork = new(SearchInput.DebounceMs);
    private readonly LivePreview _livePreview = new();
    private readonly DispatcherTimer _tick = new() { Interval = TimeSpan.FromMilliseconds(50) };
    private readonly DispatcherTimer _clock = new() { Interval = TimeSpan.FromSeconds(30) };
    private readonly Coalescer _listReload;
    private readonly Coalescer _statusReload;
    private readonly Coalescer _conflictReload;
    private IReadOnlyList<NoteConflict> _conflicts = Array.Empty<NoteConflict>();
    private bool _conflictsMore;
    private NotesSyncStatus? _syncStatus;
    private string _lastFailure = "";
    private bool _loaded;
    private bool _suppress;
    private int _openGeneration;

    private readonly SearchBox _searchBox = new() { MaxLength = 200, PlaceholderText = Strings.NoteSearchHint };
    private readonly ListBox _noteList = new();
    private readonly EmptyState _listEmpty = new() { Icon = IconKind.FileText, IsVisible = false, Padding = new Thickness(12, 24) };
    private readonly Button _moreButton;
    private readonly Button _newButton;
    private readonly ToggleButton _trashToggle = new();
    private readonly InfoBar _notice = new() { Severity = Severity.Info, IsClosable = true, IsOpen = false };
    private readonly InfoBar _conflictBanner = new() { Severity = Severity.Warning, IsOpen = false };
    private readonly NoteEditorPane _editor = new();
    private readonly TextBlock _syncText = Ui.Text("", "caption", "tertiary").With(t => { t.TextWrapping = TextWrapping.NoWrap; t.TextTrimming = TextTrimming.CharacterEllipsis; });

    public NotesPage(ShellController shell)
    {
        _shell = shell;
        _session = new NoteEditorSession(shell.NotesGateway);
        _listReload = new Coalescer(RefreshListAsync);
        _statusReload = new Coalescer(RefreshStatusAsync);
        _conflictReload = new Coalescer(RefreshConflictsAsync);

        // ---- toolbar
        _newButton = Ui.Button(AppIcons.Of(IconKind.Plus), Strings.BtnNoteNew, () => _ = Safe(NewNoteAsync), "primary");
        _trashToggle.Content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, Children = { Ui.Icon(AppIcons.Of(IconKind.Trash), 15), new TextBlock { Text = Strings.BtnNoteTrash } } };
        _trashToggle.Classes.Add("ghost");
        _trashToggle.Click += async (_, _) => await TrashClickedAsync();
        var more = Ui.IconButton(AppIcons.Of(IconKind.MoreHorizontal), Strings.BtnNoteMore, () => { });
        var menu = new MenuFlyout { Placement = PlacementMode.BottomEdgeAlignedRight };
        menu.Items.Add(MenuItem(Strings.MenuNoteExport, ExportAsync));
        menu.Items.Add(MenuItem(Strings.MenuNoteImport, ImportFileAsync));
        menu.Items.Add(MenuItem(Strings.MenuNoteImportFolder, ImportFolderAsync));
        menu.Items.Add(new Separator());
        menu.Items.Add(MenuItem(Strings.MenuNoteCleanup, CleanupAsync));
        more.Flyout = menu;
        var toolbar = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto"), Margin = new Thickness(0, 0, 0, 12) };
        toolbar.Children.Add(Ui.Text(Strings.NotesSubtitle, "secondary").With(t => t.VerticalAlignment = VerticalAlignment.Center));
        var actions = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, Children = { _trashToggle, more, _newButton } };
        Grid.SetColumn(actions, 1);
        toolbar.Children.Add(actions);

        // ---- banners
        _conflictBanner.Actions = Ui.Button(AppIcons.Of(IconKind.Layers), Strings.BtnNoteResolveConflicts, () => _ = Safe(ResolveConflictsAsync), "sm");
        var banners = new StackPanel { Spacing = 8, Children = { _notice, _conflictBanner } };

        // ---- left: search and list
        _noteList.ItemTemplate = Ui.Template<NoteRow>(row => NoteRowView(row));
        ScrollViewer.SetHorizontalScrollBarVisibility(_noteList, Avalonia.Controls.Primitives.ScrollBarVisibility.Disabled);
        _noteList.SelectionChanged += async (_, _) =>
        {
            if (_suppress || _noteList.SelectedItem is not NoteRow row) return;
            await Safe(() => OpenNoteAsync(row.NoteId));
        };
        _moreButton = Ui.TextButton(Strings.BtnNoteLoadMore, () => _ = MoreAsync(), "ghost", "block");
        _moreButton.IsVisible = false;
        var left = new Grid { RowDefinitions = new RowDefinitions("Auto,*,Auto") };
        left.Children.Add(_searchBox);
        var listHost = new Panel { Margin = new Thickness(0, 10, 0, 0), Children = { _noteList, _listEmpty } };
        Grid.SetRow(listHost, 1);
        left.Children.Add(listHost);
        Grid.SetRow(_moreButton, 2);
        left.Children.Add(_moreButton);

        // ---- right: the editor in a card
        var editorCard = new Card { Padding = new Thickness(18, 14, 18, 14), Content = _editor };
        editorCard.Classes.Add("flat");

        var columns = new Grid { ColumnDefinitions = new ColumnDefinitions("270,14,*") };
        columns.ColumnDefinitions[0].MinWidth = 200;
        columns.ColumnDefinitions[2].MinWidth = 360;
        columns.Children.Add(left);
        columns.Children.Add(new GridSplitter { Background = Brushes.Transparent, ResizeDirection = GridResizeDirection.Columns }.With(s => Grid.SetColumn(s, 1)));
        Grid.SetColumn(editorCard, 2);
        columns.Children.Add(editorCard);

        var root = new Grid { RowDefinitions = new RowDefinitions("Auto,Auto,*,Auto"), Margin = new Thickness(28, 18, 28, 14) };
        root.Children.Add(toolbar);
        Grid.SetRow(banners, 1);
        banners.Margin = new Thickness(0, 0, 0, 10);
        root.Children.Add(banners);
        Grid.SetRow(columns, 2);
        root.Children.Add(columns);
        var syncRow = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, Margin = new Thickness(2, 10, 0, 0), Children = { Ui.Icon(AppIcons.Of(IconKind.RefreshCw), 12).With(i => i[!Icon.ForegroundProperty] = i.GetResourceObservable("Lumen.Brush.TextTertiary").ToBinding()), _syncText } };
        Grid.SetRow(syncRow, 3);
        root.Children.Add(syncRow);
        Content = root;

        // ---- wiring
        _searchBox.TextChanged += (_, _) => OnSearchChanged();
        _editor.SetViewMode(shell.NotesView, notify: false);
        _editor.Edited += (title, body) =>
        {
            _session.OnEdited(title, body);
            RequestPreview();
        };
        _editor.FocusLeft += () => _ = Safe(_session.FlushAsync);
        _editor.SaveNowRequested += () => _ = Safe(_session.FlushAsync);
        _editor.HistoryRequested += async () => await Safe(ShowHistoryAsync);
        _editor.DeleteRequested += async () => await Safe(DeleteCurrentAsync);
        _editor.RestoreRequested += async () => await Safe(RestoreCurrentAsync);
        _editor.ViewModeChanged += OnViewModeChanged;
        _editor.LinkClicked += href =>
        {
            if (_shell.OpenNoteLink(href) is { } error) _shell.ShowStatus(error, StatusKind.Error);
        };
        _editor.AttachRequested += async (paths, temporary) => await Safe(() => AttachFilesAsync(paths, temporary));
        _editor.Message += m => _shell.ShowStatus(m, StatusKind.Warning);
        var preview = _editor.PreviewControl;
        preview.ResolveAttachment = ResolveAttachmentAsync;
        preview.AttachmentActivated += async view => await Safe(() => OpenAttachmentAsync(view));

        _session.StateChanged += () => _editor.SetState(_session.Model);
        _session.TextReplaced += (title, body) =>
        {
            _editor.SetText(title, body);
            RequestPreview();
        };
        _session.Notice += ShowNotice;
        _session.Failed += message =>
        {
            _lastFailure = message;
            _shell.ShowStatus(message, StatusKind.Error);
        };
        _session.ConflictCopyMade += copyId => { _ = _conflictReload.RunAsync(); };

        shell.NotesChanged += OnNotesChanged;
        shell.NotesSyncUpdated += update => { if (_loaded) _ = _statusReload.RunAsync(); };
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
        _clock.Tick += (_, _) => ApplySyncText();
        _clock.Start();
        _editor.ShowEmpty();
    }

    private static MenuItem MenuItem(string header, Func<Task> action)
    {
        var item = new MenuItem { Header = header };
        item.Click += async (_, _) => await Safe(action);
        return item;
    }

    private static Control NoteRowView(NoteRow row)
    {
        var title = new TextBlock { FontWeight = FontWeight.SemiBold, TextTrimming = TextTrimming.CharacterEllipsis };
        HighlightText.Set(title, row.TitleSegments);
        var panel = new StackPanel { Spacing = 2, Margin = new Thickness(0, 2) };
        panel.Children.Add(title);
        if (row.HasPreview)
        {
            var preview = Ui.Text("", "caption", "secondary").With(t => { t.MaxLines = 2; t.TextTrimming = TextTrimming.CharacterEllipsis; });
            HighlightText.Set(preview, row.PreviewSegments);
            panel.Children.Add(preview);
        }
        var meta = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, Margin = new Thickness(0, 2, 0, 0) };
        meta.Children.Add(Ui.Text(row.TimeText, "caption", "tertiary").With(t => t.VerticalAlignment = VerticalAlignment.Center));
        if (row.HasBadge) meta.Children.Add(new Badge { Content = row.ConflictBadge, Classes = { "warning" } });
        panel.Children.Add(meta);
        return panel;
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

    /// <summary>Another page was chosen, or the window went to the tray: what is typed is saved.</summary>
    public void OnHidden() => _ = Safe(_session.FlushAsync);

    public void FocusSearch()
    {
        if (!_searchBox.IsEnabled) return;
        _searchBox.Focus();
        _searchBox.SelectAll();
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
        else _shell.ShowStatus(r.Error!, StatusKind.Error);
    }

    private void ShowListMessage(string text)
    {
        _noteList.ItemsSource = null;
        _listEmpty.Title = text;
        _listEmpty.Description = null;
        _listEmpty.IsVisible = true;
        _moreButton.IsVisible = false;
    }

    /// <summary>Shows what <see cref="_list"/> holds (or the hits while a search is active), keeping the open note selected.</summary>
    private void ApplyList(int scrollToIndex = -1)
    {
        if (_hits.Active)
        {
            ApplyHits(scrollToIndex);
            return;
        }
        Show(NoteRow.From(_list.Items, DateTimeOffset.Now), scrollToIndex, _list.Trash ? Strings.NotesTrashEmpty : Strings.NotesEmpty, _list.HasMore,
            _list.Trash ? IconKind.Trash : IconKind.FileText);
    }

    private void ApplyHits(int scrollToIndex = -1) =>
        Show(NoteRow.From(_hits.Items, DateTimeOffset.Now), scrollToIndex, Strings.NotesSearchEmpty(_hits.Query), _hits.HasMore, IconKind.Search);

    private void Show(IReadOnlyList<NoteRow> rows, int scrollToIndex, string emptyText, bool hasMore, IconKind emptyIcon)
    {
        var open = _session.Model.NoteId;
        _suppress = true;
        _noteList.ItemsSource = rows;
        _noteList.SelectedItem = open.Length > 0 ? rows.FirstOrDefault(r => r.NoteId == open) : null;
        _suppress = false;
        if (scrollToIndex >= 0 && scrollToIndex < rows.Count) _noteList.ScrollIntoView(rows[scrollToIndex]);
        _listEmpty.Icon = emptyIcon;
        _listEmpty.Title = emptyText;
        _listEmpty.Description = null;
        _listEmpty.IsVisible = rows.Count == 0;
        _moreButton.IsVisible = hasMore;
    }

    private void SelectOpenNote()
    {
        _suppress = true;
        _noteList.SelectedItem = (_noteList.ItemsSource as IEnumerable<NoteRow>)?.FirstOrDefault(r => r.NoteId == _session.Model.NoteId);
        _suppress = false;
    }

    private async Task MoreAsync()
    {
        if (_hits.Active)
        {
            await Safe(LoadMoreHitsAsync);
            return;
        }
        var before = _list.Items.Count;
        var r = await _shell.NotesCallAsync(api => _list.LoadMore(api));
        if (r.Ok) ApplyList(before);
        else _shell.ShowStatus(r.Error!, StatusKind.Error);
    }

    // ---- search -------------------------------------------------------------------------------

    private void OnSearchChanged()
    {
        if (_suppress) return;
        var query = NoteSearchResults.Normalize(_searchBox.Text);
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

    /// <summary>Runs the search once the typing has paused; an answer that the next keystroke overtook is dropped.</summary>
    private async Task PumpSearchAsync()
    {
        var job = _searchWork.TakeDue(DateTimeOffset.UtcNow);
        if (job is null) return;
        var r = await _shell.NotesCallAsync(api => api.Search(job.Value, NoteSearchResults.PageSize));
        if (!_searchWork.Finish(job)) return; // the query changed meanwhile, or was emptied
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!, StatusKind.Error);
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
            _shell.ShowStatus(r.Error!, StatusKind.Error);
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
        _searchBox.Text = "";
        _suppress = false;
    }

    private async Task TrashClickedAsync()
    {
        var wantTrash = _trashToggle.IsChecked == true;
        var switched = false;
        await Safe(async () => switched = await SwitchViewAsync(wantTrash));
        if (!switched) _trashToggle.IsChecked = _list.Trash; // staying where we were
    }

    /// <summary>Shows the live notes or the recycle bin. The open note is saved first; false when the user keeps it open.</summary>
    private async Task<bool> SwitchViewAsync(bool trash)
    {
        if (!await LeaveCurrentAsync()) return false;
        _openGeneration++;
        _session.Close();
        _livePreview.Reset();
        _editor.ShowEmpty();
        _newButton.IsEnabled = !trash;
        _trashToggle.IsChecked = trash;
        if (trash) EndSearch(); // the search looks through live notes only
        _searchBox.IsEnabled = !trash;
        ToolTip.SetTip(_searchBox, trash ? Strings.NoteSearchDisabledInTrash : null);
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
            _shell.ShowStatus(r.Error!, StatusKind.Error);
            SelectOpenNote();
            return false;
        }
        ShowNote(r.Value!);
        return true;
    }

    private void ShowNote(NoteDetail note)
    {
        _session.Open(note);
        _editor.ShowNote(note);
        _editor.SetState(_session.Model);
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
        _editor.Freeze(true); // nothing may be typed while the note is saved and the next one is read
        try
        {
            if (await _session.FlushAsync()) return true;
            var reason = model.State switch
            {
                EditorSaveState.TooLarge => Strings.NoteStateTooLarge,
                EditorSaveState.Trashed => Strings.NoteStateTrashed,
                _ => _lastFailure,
            };
            return await Dialog.ConfirmAsync(Strings.NoteLeaveTitle, Strings.NoteLeaveUnsaved(reason), Strings.BtnLeaveAnyway, Strings.BtnStay, isDanger: true, anchor: this);
        }
        finally { _editor.Freeze(false); }
    }

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
            _editor.FocusTitle(); // an empty note is open already
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
            _shell.ShowStatus(r.Error!, StatusKind.Error);
            return;
        }
        ShowNote(r.Value!);
        await _listReload.RunAsync();
        _editor.FocusTitle();
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
            _shell.ShowStatus(r.Error!, StatusKind.Error);
            return;
        }
        _openGeneration++;
        _session.Close();
        _editor.ShowEmpty();
        _shell.ShowStatus(Strings.NoteDeleted, StatusKind.Success);
        await _listReload.RunAsync();
    }

    private async Task RestoreCurrentAsync()
    {
        if (!_session.Model.IsOpen) return;
        var fromTrashView = _list.Trash;
        if (!await _session.RestoreNoteAsync()) return; // the session has told the user why
        _shell.ShowStatus(Strings.NoteRestored, StatusKind.Success);
        if (fromTrashView)
        {
            _openGeneration++;
            _session.Close();
            _editor.ShowEmpty();
        }
        await _listReload.RunAsync();
    }

    // ---- history ----------------------------------------------------------------------------------

    private async Task ShowHistoryAsync()
    {
        var model = _session.Model;
        if (!model.IsOpen) return;
        await _session.FlushAsync(); // what is typed becomes a revision first; if that fails the history simply lacks it
        var picked = await NoteHistoryWindow.PickAsync(VaultUi.OwnerOf(this), _shell, model.NoteId, canRestore: !model.ReadOnly);
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
        if (_editor.ViewMode == NotesViewMode.Source || !model.IsOpen) return;
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
        if (_livePreview.Accept(job)) _editor.SetPreview(r.Value);
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
            !await Dialog.ConfirmAsync(Strings.AttachmentRiskyTitle, Strings.AttachmentRiskyText(view.Name), Strings.BtnOpenAnyway, Strings.BtnCancel, isDanger: true, anchor: this))
            return;
        var r = await _shell.AttachmentCallAsync(resolver => resolver.ExportForOpening(view.BlobId));
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!, StatusKind.Error);
            return;
        }
        if (_shell.OpenAttachmentFile(r.Value.Path) is { } error) _shell.ShowStatus(error, StatusKind.Error);
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
                    _shell.ShowStatus(Strings.AttachFolder(name), StatusKind.Warning);
                    continue;
                }
                _shell.ShowStatus(Strings.AttachAdding);
                var r = await _shell.NotesCallAsync(api => api.AttachmentAdd(path), ErrorMessages.ForNotesAttachment);
                if (!r.Ok)
                {
                    _shell.ShowStatus(Strings.AttachFailed(name, r.Error!), StatusKind.Error);
                    continue;
                }
                if (_session.Model.NoteId != noteId)
                {
                    ShowNotice(Strings.AttachSwitchedNote);
                    return;
                }
                _editor.InsertBlock(r.Value!.Markdown);
                _shell.ShowStatus(Strings.AttachAdded(r.Value.Name), StatusKind.Success);
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
        _conflictBanner.IsOpen = _conflicts.Count > 0;
        _conflictBanner.Message = Strings.NoteConflictBanner(_conflicts.Count, _conflictsMore);
    }

    private async Task ResolveConflictsAsync()
    {
        await _session.FlushAsync(); // the original note may be the one open here
        await NoteConflictWindow.ShowForAsync(VaultUi.OwnerOf(this), _shell, _conflicts);
        await _conflictReload.RunAsync();
        await _listReload.RunAsync();
    }

    // ---- menu: export, import, cleanup of attachments -------------------------------------------------------

    private async Task ExportAsync()
    {
        await _session.FlushAsync(); // what is typed belongs in the export
        var includeTrash = new CheckBox { Content = Strings.NoteExportTrash };
        var choose = new Button { Content = Strings.BtnNoteExportChoose, IsDefault = true, MinWidth = 130, Classes = { "primary" } };
        var cancel = new Button { Content = Strings.BtnCancel, IsCancel = true, MinWidth = 88 };
        var panel = new StackPanel
        {
            Spacing = 14,
            Width = 440,
            Children =
            {
                Ui.Text(Strings.NoteExportTitle, "title"),
                Ui.Text(Strings.NoteExportIntro, "secondary"),
                includeTrash,
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Children = { cancel, choose } },
            },
        };
        choose.Click += (_, _) => Dialog.Close(panel, includeTrash.IsChecked == true);
        cancel.Click += (_, _) => Dialog.Close(panel);
        if (await Dialog.ShowAsync(panel, this) is not bool trash) return;

        var path = await Ui.PickSaveFileAsync(this, Strings.NoteExportSaveTitle, NotesTransfer.DefaultExportName(DateTimeOffset.Now), null, ".zip");
        if (path is null) return;
        _shell.ShowStatus(Strings.NoteExportTitle + "…");
        var r = await _shell.NotesCallAsync(api => NotesTransfer.ExportTo(api, path, trash), ErrorMessages.ForNotesTransfer);
        if (r.Ok)
        {
            ShowNotice(NotesTransferText.ExportDone(r.Value!));
            return;
        }
        await Dialog.AlertAsync(Strings.NoteExportTitle, r.Error!, Strings.BtnOk, this);
    }

    private static readonly FilePickerFileType NoteFiles = new(Strings.NoteImportTypeName) { Patterns = ["*.zip", "*.md", "*.markdown", "*.txt"] };

    private async Task ImportFileAsync()
    {
        if (TopLevel.GetTopLevel(this)?.StorageProvider is not { } sp) return;
        var files = await sp.OpenFilePickerAsync(new FilePickerOpenOptions
        {
            Title = Strings.NoteImportPickTitle,
            AllowMultiple = false,
            FileTypeFilter = [NoteFiles, FilePickerFileTypes.All],
        });
        if (files.Select(f => f.TryGetLocalPath()).OfType<string>().FirstOrDefault() is { } path) await ImportAsync(path);
    }

    private async Task ImportFolderAsync()
    {
        if (await Ui.PickFolderAsync(this, Strings.NoteImportFolderTitle) is { } folder) await ImportAsync(folder);
    }

    /// <summary>Imports a .zip, a text file or a folder as new notes, shows what happened and brings the list up to date.</summary>
    private async Task ImportAsync(string path)
    {
        var r = await _shell.NotesCallAsync(api => NotesTransfer.Import(api, path), ErrorMessages.ForNotesTransfer);
        if (!r.Ok)
        {
            await Dialog.AlertAsync(Strings.NoteImportResultTitle, r.Error!, Strings.BtnOk, this);
            return;
        }
        if (_list.Trash) await SwitchViewAsync(false);
        await _listReload.RunAsync();
        if (_hits.Active) _searchWork.PostNow(_hits.Query, DateTimeOffset.UtcNow);
        if (r.Value!.Skipped.Count == 0)
        {
            ShowNotice(NotesTransferText.ImportSummary(r.Value));
            return;
        }
        var details = new TextBox
        {
            Text = NotesTransferText.ImportDetails(r.Value), IsReadOnly = true, AcceptsReturn = true, TextWrapping = TextWrapping.NoWrap,
            Height = 180, Classes = { "code" },
        };
        var ok = new Button { Content = Strings.BtnOk, IsDefault = true, IsCancel = true, MinWidth = 88, Classes = { "primary" } };
        var panel = new StackPanel
        {
            Spacing = 12,
            Width = 500,
            Children =
            {
                Ui.Text(Strings.NoteImportResultTitle, "title"),
                Ui.Text(NotesTransferText.ImportSummary(r.Value), "subtitle"),
                Ui.Text(Strings.NoteImportNote, "caption", "tertiary"),
                new Expander { Header = Strings.NoteImportSkippedHeader(r.Value.Skipped.Count), Content = details, HorizontalAlignment = HorizontalAlignment.Stretch },
                new StackPanel { Orientation = Orientation.Horizontal, HorizontalAlignment = HorizontalAlignment.Right, Children = { ok } },
            },
        };
        ok.Click += (_, _) => Dialog.Close(panel);
        await Dialog.ShowAsync(panel, this);
    }

    private async Task CleanupAsync()
    {
        if (!await Dialog.ConfirmAsync(Strings.MenuNoteCleanup, Strings.NoteCleanupConfirm, Strings.BtnCleanup, Strings.BtnCancel, anchor: this)) return;
        var r = await _shell.NotesCallAsync(api => api.AttachmentCleanup(), ErrorMessages.ForNotesAttachment);
        if (!r.Ok)
        {
            _shell.ShowStatus(r.Error!, StatusKind.Error);
            return;
        }
        _shell.Attachments.Forget();
        ShowNotice(NotesTransferText.CleanupDone(r.Value!));
    }

    // ---- notices and the sync line -------------------------------------------------------------------

    private void ShowNotice(string text)
    {
        _notice.Message = text;
        _notice.IsOpen = true;
    }

    private async Task RefreshStatusAsync()
    {
        var r = await _shell.NotesCallAsync(api => api.SyncStatus());
        _syncStatus = r.Ok ? r.Value : null;
        if (r.Ok) ApplySyncText();
        else
        {
            _syncText.Text = r.Error ?? "";
            ToolTip.SetTip(_syncText, null);
        }
    }

    private void ApplySyncText()
    {
        if (_syncStatus is not { } status) return;
        var now = DateTimeOffset.Now;
        _syncText.Text = NotesSyncText.Line(status, now);
        ToolTip.SetTip(_syncText, status.Peers.Count > 0 ? NotesSyncText.Details(status, now) : null);
        var warn = status.Peers.Any(p => p.State is NotesSyncStates.PeerOld or NotesSyncStates.Error);
        _syncText.Classes.Set("warning", warn);
        _syncText.Classes.Set("tertiary", !warn);
    }
}
