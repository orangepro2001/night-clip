using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Templates;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

/// <summary>
/// The unlocked vault (port of the Windows VaultMainPanel): toolbar, filters and groups on the left, the entry list in
/// the middle, the detail pane on the right. It keeps only what the list needs (summaries without secrets);
/// <see cref="Reset"/> drops it all on lock.
/// </summary>
internal sealed class VaultMainPanel : Grid
{
    private readonly MacController _shell = MacController.Current;
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
    private readonly TextBox _searchBox = new() { PlaceholderText = Strings.SearchHint, MaxLength = SearchInput.MaxChars };
    private readonly TextBlock _countText = DialogKit.Text("", VaultUi.MutedBrush, 12, top: 6, bottom: 4);
    private readonly TextBlock _emptyText = new()
    {
        Foreground = VaultUi.MutedBrush, TextWrapping = TextWrapping.Wrap, HorizontalAlignment = HorizontalAlignment.Center,
        VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(12), IsVisible = false,
    };
    private readonly EntryDetailPane _detail = new();
    private readonly Button _newButton = DialogKit.Btn(Strings.BtnNewEntry);
    private readonly Button _editButton = DialogKit.Btn(Strings.BtnEditEntry);
    private readonly Button _deleteButton = DialogKit.Btn(Strings.BtnDeleteEntry);
    private readonly Button _restoreButton = DialogKit.Btn(Strings.BtnRestoreEntry);
    private readonly Button _emptyTrashButton = DialogKit.Btn(Strings.BtnEmptyTrash);
    private readonly Button _renameGroupButton = DialogKit.Btn(Strings.BtnRenameGroup);
    private readonly Button _deleteGroupButton = DialogKit.Btn(Strings.BtnDeleteGroup);

    /// <summary>A call failed with VAULT_LOCKED: the page must show the locked screen.</summary>
    public event Action? LockedDetected;

    public VaultMainPanel()
    {
        RowDefinitions = new RowDefinitions("Auto,*");

        // ---- toolbar
        var health = DialogKit.Btn(Strings.BtnHealth);
        var backups = DialogKit.Btn(Strings.BtnBackups);
        var more = DialogKit.Btn(Strings.BtnMore);
        var lockNow = DialogKit.Btn(Strings.BtnLockNow);
        var menu = new MenuFlyout();
        menu.Items.Add(MenuItem(Strings.BtnImport, ImportAsync));
        menu.Items.Add(MenuItem(Strings.BtnExport, () => ExportWindow.ShowAsync(Owner, _shell)));
        menu.Items.Add(new Separator());
        menu.Items.Add(MenuItem(Strings.BtnChangeMaster, () => ChangeMasterWindow.ShowAsync(Owner, _shell)));
        menu.Items.Add(MenuItem(Strings.BtnVaultSettings, SettingsAsync));
        more.Flyout = menu;
        var toolbar = new WrapPanel { ItemSpacing = 6, LineSpacing = 4, Margin = new Thickness(0, 0, 0, 8) };
        foreach (var b in new Control[] { _newButton, _editButton, _deleteButton, _restoreButton, _emptyTrashButton, Spacer(), health, backups, more, Spacer(), lockNow })
            toolbar.Children.Add(b);
        Children.Add(toolbar);

        // ---- left: quick filters and groups
        _filterList.ItemTemplate = new FuncDataTemplate<QuickFilter>((f, _) => new TextBlock { Text = QuickFilters.Label(f) });
        _filterList.SelectedIndex = 0;
        _groupList.ItemTemplate = new FuncDataTemplate<GroupNode>((n, _) => new TextBlock
        {
            Text = n.Name, Margin = new Thickness(n.Depth * 12, 0, 0, 0), TextTrimming = TextTrimming.CharacterEllipsis,
        });
        var newGroup = DialogKit.Btn(Strings.BtnNewGroup);
        var groupButtons = new WrapPanel { ItemSpacing = 4, LineSpacing = 4, Margin = new Thickness(0, 0, 0, 4) };
        groupButtons.Children.Add(newGroup);
        groupButtons.Children.Add(_renameGroupButton);
        groupButtons.Children.Add(_deleteGroupButton);
        var left = new Grid { RowDefinitions = new RowDefinitions("Auto,Auto,Auto,*") };
        left.Children.Add(_filterList);
        left.Children.Add(DialogKit.Text(Strings.GroupsTitle, VaultUi.MutedBrush, bold: true, top: 14, bottom: 4).With(t => Grid.SetRow(t, 1)));
        left.Children.Add(groupButtons.With(g => Grid.SetRow(g, 2)));
        left.Children.Add(_groupList.With(g => Grid.SetRow(g, 3)));

        // ---- middle: search and the entry list
        _entryList.ItemTemplate = new FuncDataTemplate<EntryRow>((row, _) => EntryRowView(row));
        ScrollViewer.SetHorizontalScrollBarVisibility(_entryList, Avalonia.Controls.Primitives.ScrollBarVisibility.Disabled);
        var middle = new Grid { RowDefinitions = new RowDefinitions("Auto,Auto,*") };
        middle.Children.Add(_searchBox);
        middle.Children.Add(_countText.With(t => Grid.SetRow(t, 1)));
        middle.Children.Add(_entryList.With(l => Grid.SetRow(l, 2)));
        middle.Children.Add(_emptyText.With(t => Grid.SetRow(t, 2)));

        // ---- right: the detail pane
        var right = new Border
        {
            BorderBrush = VaultUi.Brush(0x40808080), BorderThickness = new Thickness(1), CornerRadius = new CornerRadius(10),
            Child = _detail,
        };

        var columns = new Grid { ColumnDefinitions = new ColumnDefinitions("150,6,250,6,*") };
        columns.ColumnDefinitions[0].MinWidth = 110;
        columns.ColumnDefinitions[2].MinWidth = 180;
        columns.ColumnDefinitions[4].MinWidth = 220;
        columns.Children.Add(left);
        columns.Children.Add(new GridSplitter { Background = Brushes.Transparent, ResizeDirection = GridResizeDirection.Columns }.With(s => Grid.SetColumn(s, 1)));
        columns.Children.Add(middle.With(m => Grid.SetColumn(m, 2)));
        columns.Children.Add(new GridSplitter { Background = Brushes.Transparent, ResizeDirection = GridResizeDirection.Columns }.With(s => Grid.SetColumn(s, 3)));
        columns.Children.Add(right.With(r => Grid.SetColumn(r, 4)));
        Grid.SetRow(columns, 1);
        Children.Add(columns);

        // ---- events
        _newButton.Click += async (_, _) => await NewAsync();
        _editButton.Click += async (_, _) => await EditAsync();
        _deleteButton.Click += async (_, _) => await DeleteAsync();
        _restoreButton.Click += async (_, _) => await RestoreEntryAsync();
        _emptyTrashButton.Click += async (_, _) => await EmptyTrashAsync();
        health.Click += async (_, _) => await HealthAsync();
        backups.Click += async (_, _) => await BackupsAsync();
        lockNow.Click += async (_, _) => await _shell.LockVaultAsync();
        newGroup.Click += async (_, _) => await NewGroupAsync();
        _renameGroupButton.Click += async (_, _) => await RenameGroupAsync();
        _deleteGroupButton.Click += async (_, _) => await DeleteGroupAsync();
        _filterList.SelectionChanged += async (_, _) => await FilterChangedAsync();
        _groupList.SelectionChanged += async (_, _) => await GroupChangedAsync();
        _entryList.SelectionChanged += async (_, _) => await EntryChangedAsync();
        _entryList.DoubleTapped += async (_, _) => await EditAsync();
        _searchBox.TextChanged += (_, _) =>
        {
            if (_suppress) return;
            _searchTimer.Stop();
            _searchTimer.Start();
        };
        _searchTimer.Tick += async (_, _) => { _searchTimer.Stop(); await ReloadAsync(); };
        _detail.DataChanged += () => _ = ReloadAsync();

        AddHandler(PointerMovedEvent, (_, _) => NoteActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        AddHandler(PointerPressedEvent, (_, _) => NoteActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        AddHandler(KeyDownEvent, (_, _) => NoteActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        UpdateToolbar();
    }

    private static Control Spacer() => new Border { Width = 12 };

    private static MenuItem MenuItem(string header, Func<Task> action)
    {
        var item = new MenuItem { Header = header };
        item.Click += async (_, _) => await action();
        return item;
    }

    private static Control EntryRowView(EntryRow row)
    {
        var grid = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto"), Margin = new Thickness(2, 3) };
        grid.Children.Add(new TextBlock { Text = row.Glyph, FontSize = 18, Margin = new Thickness(0, 0, 8, 0), VerticalAlignment = VerticalAlignment.Center });
        var text = new StackPanel { VerticalAlignment = VerticalAlignment.Center };
        text.Children.Add(new TextBlock { Text = row.Title, FontWeight = FontWeight.SemiBold, TextTrimming = TextTrimming.CharacterEllipsis });
        if (row.Subtitle.Length > 0)
            text.Children.Add(new TextBlock { Text = row.Subtitle, Foreground = VaultUi.MutedBrush, FontSize = 11, TextTrimming = TextTrimming.CharacterEllipsis });
        Grid.SetColumn(text, 1);
        grid.Children.Add(text);
        var badges = new StackPanel { VerticalAlignment = VerticalAlignment.Center, HorizontalAlignment = HorizontalAlignment.Right, Margin = new Thickness(6, 0, 0, 0) };
        if (row.TotpBadge.Length > 0) badges.Children.Add(new TextBlock { Text = row.TotpBadge, Foreground = VaultUi.LinkBrush, FontSize = 10, HorizontalAlignment = HorizontalAlignment.Right });
        if (row.ExpiryBadge.Length > 0) badges.Children.Add(new TextBlock { Text = row.ExpiryBadge, Foreground = VaultUi.Brush(row.ExpiryBrushArgb), FontSize = 10, HorizontalAlignment = HorizontalAlignment.Right });
        if (row.AttachBadge.Length > 0) badges.Children.Add(new TextBlock { Text = row.AttachBadge, FontSize = 10, HorizontalAlignment = HorizontalAlignment.Right });
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
        _emptyText.IsVisible = false;
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
            else _shell.ShowStatus(list.Error ?? Strings.ErrVaultUnexpected);
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
        _emptyText.Text = ListTexts.Empty(_filter, SearchText.Length > 0);
        _emptyText.IsVisible = rows.Count == 0;
        UpdateToolbar();
        if (selected is null) await _detail.ShowAsync(null);
        else if (_detail.CurrentId != selected.Id) await _detail.ShowAsync(selected.Id);
    }

    private string? SelectedId => (_entryList.SelectedItem as EntryRow)?.Id;

    private void UpdateToolbar()
    {
        var t = ToolbarState.For(_filter, SelectedId is not null);
        _editButton.IsEnabled = t.CanEdit;
        _deleteButton.IsEnabled = t.CanDelete;
        _deleteButton.Content = t.DeleteLabel;
        _restoreButton.IsEnabled = t.CanRestore;
        _restoreButton.IsVisible = _filter == QuickFilter.Trash;
        _emptyTrashButton.IsVisible = _filter == QuickFilter.Trash;
        _emptyTrashButton.IsEnabled = t.CanEmptyTrash;
        _newButton.IsEnabled = _filter != QuickFilter.Trash;
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
        _filterList.SelectedIndex = 0;
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
        _shell.ShowStatus(Strings.EntrySaved);
    }

    private async Task EditAsync()
    {
        if (_filter == QuickFilter.Trash) return;
        if (SelectedId is not { } current) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var id = await EntryEditorWindow.ShowAsync(Owner, _shell, _groups, current, "", "");
        if (id is null) return;
        _detail.Clear(); // the entry changed: show it fresh, everything masked
        await ReloadAsync(id);
        _shell.ShowStatus(Strings.EntrySaved);
    }

    private async Task DeleteAsync()
    {
        if (SelectedId is not { } id) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var permanent = _filter == QuickFilter.Trash;
        if (!await VaultUi.ConfirmAsync(this, permanent ? Strings.DeletePermanentConfirm : Strings.DeleteToTrashConfirm,
                yes: permanent ? Strings.BtnDeletePermanent : Strings.BtnDeleteEntry, warning: permanent)) return;
        var r = await _shell.VaultCallAsync(a => a.Delete(id));
        if (!r.Ok) { await VaultUi.ErrorAsync(this, r.Error!); return; }
        _detail.Clear();
        await ReloadAsync();
        _shell.ShowStatus(r.Value ? Strings.EntryDeleted : Strings.EntryMovedToTrash);
    }

    private async Task RestoreEntryAsync()
    {
        if (SelectedId is not { } id) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var r = await _shell.VaultCallAsync(a => a.RestoreEntry(id));
        if (!r.Ok) { await VaultUi.ErrorAsync(this, r.Error!); return; }
        _detail.Clear();
        await ReloadAsync();
        _shell.ShowStatus(Strings.EntryRestored);
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
        _shell.ShowStatus(Strings.TrashEmptied(r.Value));
    }

    // ---- groups -----------------------------------------------------------------------------------

    /// <summary>Asks for a group name until it is valid (GroupNames.Normalize) or the user cancels.</summary>
    private async Task<string?> AskGroupNameAsync(string title, string initial)
    {
        if (Owner is not { } owner) return null;
        var text = initial;
        while (true)
        {
            if (await Ui.PromptAsync(owner, title, Strings.GroupNamePrompt, text) is not { } typed) return null;
            if (GroupNames.Normalize(typed) is { } name) return name;
            text = typed;
        }
    }

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
        if (!await VaultUi.ConfirmAsync(this, Strings.BtnDeleteGroup + "：" + node.Name + "？", yes: Strings.BtnDeleteGroup)) return;
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
