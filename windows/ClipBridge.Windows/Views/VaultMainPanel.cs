using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Primitives;
using Avalonia.Controls.Templates;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The unlocked vault: a toolbar, quick filters and groups on the left, the searchable entry list in the middle and the
/// detail pane on the right. It keeps only what the list needs (summaries without secrets); <see cref="Reset"/> drops it
/// all on lock.
/// </summary>
internal sealed class VaultMainPanel : Grid
{
    private readonly ShellController _shell;
    private readonly DispatcherTimer _searchTimer = new() { Interval = TimeSpan.FromMilliseconds(SearchInput.DebounceMs) };
    private IReadOnlyList<EntrySummary> _entries = Array.Empty<EntrySummary>();
    private IReadOnlyList<GroupInfo> _groups = Array.Empty<GroupInfo>();
    private QuickFilter _filter = QuickFilter.All;
    private string _groupId = "";
    private int _loadGeneration;
    private bool _suppress;

    private readonly ListBox _filterList = new() { ItemsSource = QuickFilters.All };
    private readonly ListBox _groupList = new();
    private readonly ListBox _entryList = new();
    private readonly SearchBox _searchBox = new() { PlaceholderText = Strings.SearchHint, MaxLength = SearchInput.MaxChars };
    private readonly TextBlock _countText = Ui.Text("", "caption", "tertiary");
    private readonly EmptyState _empty = new() { Icon = IconKind.Search, IsVisible = false };
    private readonly EntryDetailPane _detail;
    private readonly Button _newButton;
    private readonly Button _deleteButton;
    private readonly Button _restoreButton;
    private readonly Button _emptyTrashButton;
    private readonly Button _renameGroupButton;
    private readonly Button _deleteGroupButton;

    /// <summary>A call failed with VAULT_LOCKED: the page must show the locked screen.</summary>
    public event Action? LockedDetected;

    public VaultMainPanel(ShellController shell)
    {
        _shell = shell;
        _detail = new EntryDetailPane(shell);
        Margin = new Thickness(28, 18, 28, 22);
        RowDefinitions = new RowDefinitions("Auto,*");

        // ---- toolbar
        _newButton = Ui.Button(AppIcons.Of(IconKind.Plus), Strings.BtnNewEntry, () => _ = NewAsync(), "primary");
        _deleteButton = Ui.Button(AppIcons.Of(IconKind.Trash), Strings.BtnDeleteEntry, () => _ = DeleteAsync(), "ghost");
        _restoreButton = Ui.Button(AppIcons.RotateCcw, Strings.BtnRestoreEntry, () => _ = RestoreEntryAsync(), "soft");
        _emptyTrashButton = Ui.Button(AppIcons.Of(IconKind.Trash), Strings.BtnEmptyTrash, () => _ = EmptyTrashAsync(), "danger-soft");
        var health = Ui.Button(AppIcons.ShieldCheck, Strings.BtnHealth, () => _ = HealthAsync(), "ghost");
        var backups = Ui.Button(AppIcons.History, Strings.BtnBackups, () => _ = BackupsAsync(), "ghost");
        var more = Ui.IconButton(AppIcons.Of(IconKind.MoreHorizontal), Strings.BtnMore, () => { });
        var menu = new MenuFlyout { Placement = PlacementMode.BottomEdgeAlignedRight };
        menu.Items.Add(MenuItem(Strings.BtnImport, ImportAsync));
        menu.Items.Add(MenuItem(Strings.BtnExport, () => ExportWindow.ShowAsync(Owner, _shell)));
        menu.Items.Add(new Separator());
        menu.Items.Add(MenuItem(Strings.BtnChangeMaster, () => ChangeMasterWindow.ShowAsync(Owner, _shell)));
        menu.Items.Add(MenuItem(Strings.BtnVaultSettings, SettingsAsync));
        more.Flyout = menu;
        var lockNow = Ui.Button(AppIcons.Of(IconKind.Lock), Strings.BtnLockNow, () => _ = _shell.LockVaultAsync());

        var toolbar = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto"), Margin = new Thickness(0, 0, 0, 14) };
        toolbar.Children.Add(new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, Children = { _newButton, _restoreButton, _emptyTrashButton, _deleteButton } });
        var right = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4, Children = { health, backups, more, lockNow } };
        lockNow.Margin = new Thickness(6, 0, 0, 0);
        Grid.SetColumn(right, 2);
        toolbar.Children.Add(right);
        Children.Add(toolbar);

        // ---- left: quick filters and groups
        _filterList.ItemTemplate = Ui.Template<QuickFilter>(f => new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 10,
            Children = { Ui.Icon(FilterIcon(f), 15), new TextBlock { Text = QuickFilters.Label(f), VerticalAlignment = VerticalAlignment.Center } },
        });
        _filterList.SelectedIndex = 0;
        _groupList.ItemTemplate = Ui.Template<GroupNode>(n => new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8,
            Margin = new Thickness(n.Depth * 14, 0, 0, 0),
            Children =
            {
                Ui.Icon(AppIcons.Of(IconKind.Folder), 14),
                new TextBlock { Text = n.Name, TextTrimming = TextTrimming.CharacterEllipsis, VerticalAlignment = VerticalAlignment.Center },
            },
        });
        var newGroup = Ui.IconButton(AppIcons.Of(IconKind.Plus), Strings.BtnNewGroup, () => _ = NewGroupAsync(), "sm");
        _renameGroupButton = Ui.IconButton(AppIcons.Of(IconKind.Pencil), Strings.BtnRenameGroup, () => _ = RenameGroupAsync(), "sm");
        _deleteGroupButton = Ui.IconButton(AppIcons.Of(IconKind.Trash), Strings.BtnDeleteGroup, () => _ = DeleteGroupAsync(), "sm");
        var groupHeader = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto"), Margin = new Thickness(10, 16, 0, 4) };
        groupHeader.Children.Add(Ui.Text(Strings.GroupsTitle, "overline").With(t => t.VerticalAlignment = VerticalAlignment.Center));
        var groupButtons = new StackPanel { Orientation = Orientation.Horizontal, Children = { newGroup, _renameGroupButton, _deleteGroupButton } };
        Grid.SetColumn(groupButtons, 1);
        groupHeader.Children.Add(groupButtons);
        var left = new Grid { RowDefinitions = new RowDefinitions("Auto,Auto,*") };
        left.Children.Add(_filterList);
        left.Children.Add(groupHeader.With(g => Grid.SetRow(g, 1)));
        left.Children.Add(_groupList.With(g => Grid.SetRow(g, 2)));

        // ---- middle: search and the entry list
        _entryList.ItemTemplate = Ui.Template<EntryRow>(row => EntryRowView(row));
        ScrollViewer.SetHorizontalScrollBarVisibility(_entryList, Avalonia.Controls.Primitives.ScrollBarVisibility.Disabled);
        var middle = new Grid { RowDefinitions = new RowDefinitions("Auto,Auto,*") };
        middle.Children.Add(_searchBox);
        middle.Children.Add(_countText.With(t => { Grid.SetRow(t, 1); t.Margin = new Thickness(4, 8, 0, 4); }));
        middle.Children.Add(_entryList.With(l => Grid.SetRow(l, 2)));
        middle.Children.Add(_empty.With(t => Grid.SetRow(t, 2)));

        // ---- right: the detail pane in a card
        var detailCard = new Card { Padding = new Thickness(0), Content = _detail, ClipToBounds = true };
        detailCard.Classes.Add("flat");

        var columns = new Grid { ColumnDefinitions = new ColumnDefinitions("190,12,280,16,*") };
        columns.ColumnDefinitions[0].MinWidth = 150;
        columns.ColumnDefinitions[2].MinWidth = 220;
        columns.ColumnDefinitions[4].MinWidth = 260;
        columns.Children.Add(left);
        columns.Children.Add(new GridSplitter { Background = Brushes.Transparent, ResizeDirection = GridResizeDirection.Columns }.With(s => Grid.SetColumn(s, 1)));
        columns.Children.Add(middle.With(m => Grid.SetColumn(m, 2)));
        columns.Children.Add(new GridSplitter { Background = Brushes.Transparent, ResizeDirection = GridResizeDirection.Columns }.With(s => Grid.SetColumn(s, 3)));
        columns.Children.Add(detailCard.With(r => Grid.SetColumn(r, 4)));
        Grid.SetRow(columns, 1);
        Children.Add(columns);

        // ---- events
        _filterList.SelectionChanged += async (_, _) => await FilterChangedAsync();
        _groupList.SelectionChanged += async (_, _) => await GroupChangedAsync();
        _entryList.SelectionChanged += async (_, _) => await EntryChangedAsync();
        _entryList.DoubleTapped += async (_, _) => await EditAsync();
        _entryList.KeyDown += async (_, e) =>
        {
            if (e.Key == Key.Enter) { e.Handled = true; await EditAsync(); }
            else if (e.Key == Key.Delete) { e.Handled = true; await DeleteAsync(); }
        };
        _searchBox.TextChanged += (_, _) =>
        {
            if (_suppress) return;
            _searchTimer.Stop();
            _searchTimer.Start();
        };
        _searchTimer.Tick += async (_, _) => { _searchTimer.Stop(); await ReloadAsync(); };
        _detail.DataChanged += () => _ = ReloadAsync();
        _detail.EditRequested += () => _ = EditAsync();

        AddHandler(PointerMovedEvent, (_, _) => NoteActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        AddHandler(PointerPressedEvent, (_, _) => NoteActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        AddHandler(KeyDownEvent, (_, _) => NoteActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        UpdateToolbar();
    }

    private static string FilterIcon(QuickFilter f) => f switch
    {
        QuickFilter.Logins => AppIcons.Of(IconKind.User),
        QuickFilter.ApiKeys => AppIcons.Key,
        QuickFilter.Secrets => AppIcons.Shield,
        QuickFilter.Notes => AppIcons.Of(IconKind.FileText),
        QuickFilter.Expiring => AppIcons.Of(IconKind.Calendar),
        QuickFilter.Trash => AppIcons.Of(IconKind.Trash),
        _ => AppIcons.Of(IconKind.Layers),
    };

    private static MenuItem MenuItem(string header, Func<Task> action)
    {
        var item = new MenuItem { Header = header };
        item.Click += async (_, _) => await action();
        return item;
    }

    private static Control EntryRowView(EntryRow row)
    {
        var grid = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto"), Margin = new Thickness(0, 2) };
        var tile = new Border { Width = 32, Height = 32, Child = new Icon { NameKey = VaultUi.KindIcon(row.Summary.Kind), Size = 15, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center } };
        tile.Classes.Add("tile");
        grid.Children.Add(tile);
        var text = new StackPanel { VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(10, 0, 6, 0), Spacing = 1 };
        text.Children.Add(new TextBlock { Text = row.Title, FontWeight = FontWeight.Medium, TextTrimming = TextTrimming.CharacterEllipsis });
        if (row.Subtitle.Length > 0) text.Children.Add(Ui.Text(row.Subtitle, "caption", "tertiary").With(t => { t.TextWrapping = TextWrapping.NoWrap; t.TextTrimming = TextTrimming.CharacterEllipsis; }));
        Grid.SetColumn(text, 1);
        grid.Children.Add(text);
        var badges = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4, VerticalAlignment = VerticalAlignment.Center };
        if (row.TotpBadge.Length > 0) badges.Children.Add(new Badge { Content = row.TotpBadge, Classes = { "info" } });
        if (row.ExpiryBadge.Length > 0) badges.Children.Add(new Badge { Content = row.ExpiryBadge, Classes = { row.ExpiryBrushArgb == 0xFFD93025u ? "danger" : "warning" } });
        if (row.AttachBadge.Length > 0) badges.Children.Add(Ui.Icon(AppIcons.Paperclip, 13));
        Grid.SetColumn(badges, 2);
        grid.Children.Add(badges);
        return grid;
    }

    private Window? Owner => VaultUi.OwnerOf(this);

    private void NoteActivity()
    {
        _detail.MarkActivity();
        _shell.NoteVaultActivity();
    }

    public void FocusSearch()
    {
        _searchBox.Focus();
        _searchBox.SelectAll();
    }

    // ---- lifecycle -----------------------------------------------------------------------------

    public async Task OnUnlockedAsync(VaultStatus status)
    {
        _detail.AutolockSeconds = status.AutolockS;
        _shell.VaultAutolockSeconds = status.AutolockS;
        ResetFilters();
        await ReloadAsync();
    }

    public void OnShown() => _detail.OnShown();

    public void OnHidden() => _detail.OnHidden();

    /// <summary>Drops the list, the groups, the search text and everything the detail pane holds.</summary>
    public void Reset()
    {
        _loadGeneration++;
        _searchTimer.Stop();
        _detail.Clear();
        _entries = Array.Empty<EntrySummary>();
        _groups = Array.Empty<GroupInfo>();
        _suppress = true;
        _entryList.ItemsSource = null;
        _groupList.ItemsSource = null;
        _searchBox.Text = "";
        _suppress = false;
        ResetFilters();
        _countText.Text = "";
        _empty.IsVisible = false;
        UpdateToolbar();
    }

    private void ResetFilters()
    {
        _filter = QuickFilter.All;
        _groupId = "";
        _suppress = true;
        _filterList.SelectedIndex = 0;
        _groupList.SelectedItem = null;
        _searchBox.Text = "";
        _suppress = false;
    }

    // ---- loading the list ----------------------------------------------------------------------------

    private string SearchText => SearchInput.Normalize(_searchBox.Text);

    public async Task ReloadAsync(string? selectId = null)
    {
        var gen = ++_loadGeneration;
        var query = QuickFilters.ToQuery(_filter, SearchText);
        var listTask = _shell.VaultCallAsync(a => a.List(query));
        var groupsTask = _shell.VaultCallAsync(a => a.Groups());
        var list = await listTask;
        var groups = await groupsTask;
        if (gen != _loadGeneration) return;
        if (!list.Ok)
        {
            if (list.ErrorCode == "VAULT_LOCKED") LockedDetected?.Invoke();
            else _shell.ShowStatus(list.Error ?? Strings.ErrVaultUnexpected, StatusKind.Error);
            return;
        }
        _entries = list.Value!;
        if (groups.Ok)
        {
            _groups = groups.Value!;
            _suppress = true;
            var nodes = GroupTree.Flatten(_groups);
            _groupList.ItemsSource = nodes;
            _groupList.SelectedItem = nodes.FirstOrDefault(n => n.Id == _groupId);
            _suppress = false;
        }
        await ApplyListAsync(selectId ?? _detail.CurrentId);
    }

    private async Task ApplyListAsync(string? selectId)
    {
        var now = DateTimeOffset.UtcNow;
        var groupIds = _groupId.Length > 0 ? GroupTree.WithDescendants(_groups, _groupId) : null;
        var shown = QuickFilters.Apply(_entries, _filter, groupIds, SearchText, now);
        var rows = EntryRow.From(shown, now);
        _suppress = true;
        _entryList.ItemsSource = rows;
        var selected = selectId is null ? null : rows.FirstOrDefault(r => r.Id == selectId);
        _entryList.SelectedItem = selected;
        _suppress = false;
        if (selected is not null) _entryList.ScrollIntoView(selected);
        _countText.Text = ListTexts.Count(rows.Count);
        _empty.Title = ListTexts.Empty(_filter, SearchText.Length > 0);
        _empty.Icon = SearchText.Length > 0 ? IconKind.Search : _filter == QuickFilter.Trash ? IconKind.Trash : IconKind.Lock;
        _empty.IsVisible = rows.Count == 0;
        UpdateToolbar();
        if (selected is null) await _detail.ShowAsync(null);
        else if (_detail.CurrentId != selected.Id) await _detail.ShowAsync(selected.Id);
    }

    private string? SelectedId => (_entryList.SelectedItem as EntryRow)?.Id;

    private void UpdateToolbar()
    {
        var t = ToolbarState.For(_filter, SelectedId is not null);
        _deleteButton.IsEnabled = t.CanDelete;
        if (_deleteButton.Content is StackPanel { Children.Count: 2 } p && p.Children[1] is TextBlock label) label.Text = t.DeleteLabel;
        _restoreButton.IsEnabled = t.CanRestore;
        _restoreButton.IsVisible = _filter == QuickFilter.Trash;
        _emptyTrashButton.IsVisible = _filter == QuickFilter.Trash;
        _emptyTrashButton.IsEnabled = t.CanEmptyTrash;
        _newButton.IsVisible = _filter != QuickFilter.Trash;
        var groupSelected = _groupList.SelectedItem is GroupNode;
        _renameGroupButton.IsEnabled = groupSelected;
        _deleteGroupButton.IsEnabled = groupSelected;
    }

    /// <summary>Jumps to an entry: all entries, no search, no group, selected.</summary>
    public async Task JumpToAsync(string id)
    {
        ResetFilters();
        await ReloadAsync(id);
    }

    // ---- navigation events ------------------------------------------------------------------------

    private async Task FilterChangedAsync()
    {
        if (_suppress || _filterList.SelectedItem is not QuickFilter f) return;
        _filter = f;
        _groupId = "";
        _suppress = true;
        _groupList.SelectedItem = null;
        _suppress = false;
        await ReloadAsync();
    }

    private async Task GroupChangedAsync()
    {
        if (_suppress) return;
        if (_groupList.SelectedItem is not GroupNode node) { UpdateToolbar(); return; }
        _groupId = node.Id;
        _filter = QuickFilter.All;
        _suppress = true;
        _filterList.SelectedItem = null;
        _suppress = false;
        await ReloadAsync();
    }

    private async Task EntryChangedAsync()
    {
        if (_suppress) return;
        UpdateToolbar();
        await _detail.ShowAsync(SelectedId);
    }

    // ---- entries ------------------------------------------------------------------------------------

    private async Task NewAsync()
    {
        if (_filter == QuickFilter.Trash) return;
        var id = await EntryEditorWindow.ShowAsync(Owner, _shell, _groups, "", _groupId, QuickFilters.KindOf(_filter));
        if (id is null) return;
        await JumpToAsync(id); // the new entry may not match the current filter: show everything and select it
        _shell.ShowStatus(Strings.EntrySaved, StatusKind.Success);
    }

    private async Task EditAsync()
    {
        if (_filter == QuickFilter.Trash) return;
        if (SelectedId is not { } current) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var id = await EntryEditorWindow.ShowAsync(Owner, _shell, _groups, current, "", "");
        if (id is null) return;
        _detail.Clear(); // the entry changed: show it fresh, everything masked
        await ReloadAsync(id);
        _shell.ShowStatus(Strings.EntrySaved, StatusKind.Success);
    }

    private async Task DeleteAsync()
    {
        if (SelectedId is not { } id) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var permanent = _filter == QuickFilter.Trash;
        if (!await VaultUi.ConfirmAsync(this, permanent ? Strings.DeletePermanentConfirm : Strings.DeleteToTrashConfirm,
                yes: permanent ? Strings.BtnDeletePermanentShort : Strings.BtnDeleteEntry, warning: permanent)) return;
        var r = await _shell.VaultCallAsync(a => a.Delete(id));
        if (!r.Ok) { await VaultUi.ErrorAsync(this, r.Error!); return; }
        _detail.Clear();
        await ReloadAsync();
        _shell.ShowStatus(r.Value ? Strings.EntryDeleted : Strings.EntryMovedToTrash, StatusKind.Success);
    }

    private async Task RestoreEntryAsync()
    {
        if (SelectedId is not { } id) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var r = await _shell.VaultCallAsync(a => a.RestoreEntry(id));
        if (!r.Ok) { await VaultUi.ErrorAsync(this, r.Error!); return; }
        _detail.Clear();
        await ReloadAsync();
        _shell.ShowStatus(Strings.EntryRestored, StatusKind.Success);
    }

    private async Task EmptyTrashAsync()
    {
        var all = await _shell.VaultCallAsync(a => a.List(new VaultQuery(OnlyTrash: true)));
        var count = all.Ok ? all.Value!.Count : _entries.Count;
        if (count == 0) { _shell.ShowStatus(Strings.TrashAlreadyEmpty); return; }
        if (!await VaultUi.ConfirmAsync(this, Strings.EmptyTrashConfirm(count), yes: Strings.BtnEmptyTrash, warning: true)) return;
        var r = await _shell.VaultCallAsync(a => a.EmptyTrash());
        if (!r.Ok) { await VaultUi.ErrorAsync(this, r.Error!); return; }
        _detail.Clear();
        await ReloadAsync();
        _shell.ShowStatus(Strings.TrashEmptied(r.Value), StatusKind.Success);
    }

    // ---- groups -----------------------------------------------------------------------------------

    private Task<string?> AskGroupNameAsync(string title, string initial) =>
        Ui.PromptAsync(this, title, Strings.GroupNamePrompt, initial, GroupNames.Normalize, Strings.GroupNameInvalid, Strings.BtnOk);

    private async Task NewGroupAsync()
    {
        var name = await AskGroupNameAsync(Strings.BtnNewGroup, "");
        if (name is null) return;
        var parent = _groupId;
        var r = await _shell.VaultCallAsync(a => a.GroupAdd(parent, name));
        if (!r.Ok) { await VaultUi.ErrorAsync(this, r.Error!); return; }
        await ReloadAsync();
    }

    private async Task RenameGroupAsync()
    {
        if (_groupList.SelectedItem is not GroupNode node) return;
        var name = await AskGroupNameAsync(Strings.BtnRenameGroup, node.Name);
        if (name is null) return;
        var r = await _shell.VaultCallAsync(a => a.GroupRename(node.Id, name));
        if (!r.Ok) { await VaultUi.ErrorAsync(this, r.Error!); return; }
        await ReloadAsync();
    }

    private async Task DeleteGroupAsync()
    {
        if (_groupList.SelectedItem is not GroupNode node) return;
        if (node.Entries > 0 || _groups.Any(g => g.ParentId == node.Id)) { await VaultUi.InfoAsync(this, Strings.GroupDeleteHint); return; }
        if (!await VaultUi.ConfirmAsync(this, Strings.BtnDeleteGroup + "：" + node.Name + "？", yes: Strings.BtnDeleteGroup, warning: true)) return;
        var r = await _shell.VaultCallAsync(a => a.GroupDelete(node.Id));
        if (!r.Ok) { await VaultUi.ErrorAsync(this, r.Error!); return; }
        _groupId = "";
        await ReloadAsync();
    }

    // ---- toolbar: tools ---------------------------------------------------------------------------

    private async Task HealthAsync()
    {
        var id = await HealthWindow.PickAsync(Owner, _shell);
        if (id is not null) await JumpToAsync(id);
    }

    private async Task BackupsAsync()
    {
        if (await BackupsWindow.ShowAsync(Owner, _shell))
        {
            _detail.Clear();
            await ReloadAsync();
        }
    }

    private async Task ImportAsync()
    {
        if (await ImportWindow.ShowAsync(Owner, _shell))
        {
            _detail.Clear();
            await ReloadAsync();
        }
    }

    private async Task SettingsAsync()
    {
        await VaultSettingsWindow.ShowAsync(Owner, _shell);
        var st = await _shell.VaultCallAsync(a => a.Status());
        if (st.Ok)
        {
            _detail.AutolockSeconds = st.Value!.AutolockS;
            _shell.VaultAutolockSeconds = st.Value.AutolockS;
        }
    }
}
