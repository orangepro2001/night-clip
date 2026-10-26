using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The unlocked vault: toolbar, filters and groups on the left, the entry list in the middle, the detail pane on
/// the right. It keeps only what the list needs (summaries without secrets); <see cref="Reset"/> drops it all on lock.
/// </summary>
public partial class VaultMainPanel : UserControl
{
    private ShellController _shell = null!;
    private readonly DispatcherTimer _searchTimer = new() { Interval = TimeSpan.FromMilliseconds(SearchInput.DebounceMs) };
    private IReadOnlyList<EntrySummary> _entries = Array.Empty<EntrySummary>();
    private IReadOnlyList<GroupInfo> _groups = Array.Empty<GroupInfo>();
    private QuickFilter _filter = QuickFilter.All;
    private string _groupId = "";
    private int _loadGeneration;
    private bool _suppress;

    /// <summary>A call failed with VAULT_LOCKED: the page must show the locked screen.</summary>
    public event Action? LockedDetected;

    /// <summary>The settings changed the auto-lock time.</summary>
    public event Action<int>? AutolockChanged;

    public VaultMainPanel()
    {
        InitializeComponent();
        _suppress = true; // the selection handlers need the shell, which is attached later
        foreach (var f in QuickFilters.All)
            FilterList.Items.Add(new ListBoxItem { Tag = f, Content = QuickFilters.Label(f), Padding = new Thickness(8, 5, 6, 5) });
        FilterList.SelectedIndex = 0;
        _suppress = false;
        _searchTimer.Tick += async (_, _) => { _searchTimer.Stop(); await ReloadAsync(); };
        PreviewMouseMove += (_, _) => NoteActivity();
        PreviewMouseDown += (_, _) => NoteActivity();
        PreviewKeyDown += (_, _) => NoteActivity();
        UpdateToolbar();
    }

    internal void Attach(ShellController shell)
    {
        _shell = shell;
        Detail.Attach(shell);
        Detail.DataChanged += () => _ = ReloadAsync();
    }

    private Window? OwnerWindow => Window.GetWindow(this);

    private void NoteActivity()
    {
        Detail.MarkActivity();
        _shell?.NoteVaultActivity();
    }

    // ---- lifecycle -----------------------------------------------------------------------------

    public async Task OnUnlockedAsync(VaultStatus status)
    {
        Detail.AutolockSeconds = status.AutolockS;
        _shell.VaultAutolockSeconds = status.AutolockS;
        ResetFilters();
        await ReloadAsync();
    }

    /// <summary>Drops the list, the groups, the search text and everything the detail pane holds.</summary>
    public void Reset()
    {
        _loadGeneration++;
        _searchTimer.Stop();
        Detail.Clear();
        _entries = Array.Empty<EntrySummary>();
        _groups = Array.Empty<GroupInfo>();
        _suppress = true;
        EntryList.ItemsSource = null;
        GroupList.ItemsSource = null;
        SearchBox.Clear();
        _suppress = false;
        ResetFilters();
        CountText.Text = "";
        EmptyText.Visibility = Visibility.Collapsed;
        UpdateToolbar();
    }

    private void ResetFilters()
    {
        _filter = QuickFilter.All;
        _groupId = "";
        _suppress = true;
        FilterList.SelectedIndex = 0;
        GroupList.SelectedItem = null;
        SearchBox.Clear();
        _suppress = false;
    }

    // ---- loading the list ----------------------------------------------------------------------------

    private string SearchText => SearchInput.Normalize(SearchBox.Text);

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
            GroupList.ItemsSource = nodes;
            GroupList.SelectedItem = nodes.FirstOrDefault(n => n.Id == _groupId);
            _suppress = false;
        }
        await ApplyListAsync(selectId ?? Detail.CurrentId);
    }

    private async Task ApplyListAsync(string? selectId)
    {
        var now = DateTimeOffset.UtcNow;
        var groupIds = _groupId.Length > 0 ? GroupTree.WithDescendants(_groups, _groupId) : null;
        var shown = QuickFilters.Apply(_entries, _filter, groupIds, SearchText, now);
        var rows = EntryRow.From(shown, now);
        _suppress = true;
        EntryList.ItemsSource = rows;
        var selected = selectId is null ? null : rows.FirstOrDefault(r => r.Id == selectId);
        EntryList.SelectedItem = selected;
        _suppress = false;
        CountText.Text = ListTexts.Count(rows.Count);
        EmptyText.Text = ListTexts.Empty(_filter, SearchText.Length > 0);
        EmptyText.Visibility = rows.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        UpdateToolbar();
        if (selected is null) await Detail.ShowAsync(null);
        else if (Detail.CurrentId != selected.Id) await Detail.ShowAsync(selected.Id);
    }

    private string? SelectedId => (EntryList.SelectedItem as EntryRow)?.Id;

    private void UpdateToolbar()
    {
        var t = ToolbarState.For(_filter, SelectedId is not null);
        EditButton.IsEnabled = t.CanEdit;
        DeleteButton.IsEnabled = t.CanDelete;
        DeleteButton.Content = t.DeleteLabel;
        RestoreButton.IsEnabled = t.CanRestore;
        RestoreButton.Visibility = _filter == QuickFilter.Trash ? Visibility.Visible : Visibility.Collapsed;
        EmptyTrashButton.Visibility = _filter == QuickFilter.Trash ? Visibility.Visible : Visibility.Collapsed;
        EmptyTrashButton.IsEnabled = t.CanEmptyTrash;
        NewButton.IsEnabled = _filter != QuickFilter.Trash;
        var groupSelected = GroupList.SelectedItem is GroupNode;
        RenameGroupButton.IsEnabled = groupSelected;
        DeleteGroupButton.IsEnabled = groupSelected;
    }

    /// <summary>Jumps to an entry from the health report: all entries, no search, no group, selected.</summary>
    public async Task JumpToAsync(string id)
    {
        _filter = QuickFilter.All;
        _groupId = "";
        _suppress = true;
        FilterList.SelectedIndex = 0;
        GroupList.SelectedItem = null;
        SearchBox.Clear();
        _suppress = false;
        await ReloadAsync(id);
    }

    // ---- navigation events ------------------------------------------------------------------------

    private async void FilterList_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (_suppress || FilterList.SelectedItem is not ListBoxItem { Tag: QuickFilter f }) return;
        _filter = f;
        _groupId = "";
        _suppress = true;
        GroupList.SelectedItem = null;
        _suppress = false;
        await ReloadAsync();
    }

    private async void GroupList_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (_suppress) return;
        if (GroupList.SelectedItem is not GroupNode node) { UpdateToolbar(); return; }
        _groupId = node.Id;
        _filter = QuickFilter.All;
        _suppress = true;
        FilterList.SelectedIndex = 0;
        _suppress = false;
        await ReloadAsync();
    }

    private async void EntryList_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (_suppress) return;
        UpdateToolbar();
        await Detail.ShowAsync(SelectedId);
    }

    private void SearchBox_TextChanged(object sender, TextChangedEventArgs e)
    {
        SearchHint.Visibility = SearchBox.Text.Length == 0 ? Visibility.Visible : Visibility.Collapsed;
        if (_suppress) return;
        _searchTimer.Stop();
        _searchTimer.Start();
    }

    // ---- entries ------------------------------------------------------------------------------------

    private async void New_Click(object sender, RoutedEventArgs e)
    {
        var id = await EntryEditorWindow.ShowAsync(OwnerWindow, _shell, _groups, "", _groupId, QuickFilters.KindOf(_filter));
        if (id is null) return;
        await JumpToAsync(id); // the new entry may not match the current filter: show everything and select it
        _shell.ShowStatus(Strings.EntrySaved);
    }

    private async void Edit_Click(object sender, RoutedEventArgs e)
    {
        if (SelectedId is not { } current) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var id = await EntryEditorWindow.ShowAsync(OwnerWindow, _shell, _groups, current, "", "");
        if (id is null) return;
        Detail.Clear(); // the entry changed: show it fresh, everything masked
        await ReloadAsync(id);
        _shell.ShowStatus(Strings.EntrySaved);
    }

    private async void Delete_Click(object sender, RoutedEventArgs e)
    {
        if (SelectedId is not { } id) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var permanent = _filter == QuickFilter.Trash;
        if (!VaultUi.Confirm(OwnerWindow, permanent ? Strings.DeletePermanentConfirm : Strings.DeleteToTrashConfirm, Strings.AppName, warning: permanent)) return;
        var r = await _shell.VaultCallAsync(a => a.Delete(id));
        if (!r.Ok) { VaultUi.Error(OwnerWindow, r.Error!); return; }
        Detail.Clear();
        await ReloadAsync();
        _shell.ShowStatus(r.Value ? Strings.EntryDeleted : Strings.EntryMovedToTrash);
    }

    private async void RestoreEntry_Click(object sender, RoutedEventArgs e)
    {
        if (SelectedId is not { } id) { _shell.ShowStatus(Strings.EntryChoose); return; }
        var r = await _shell.VaultCallAsync(a => a.RestoreEntry(id));
        if (!r.Ok) { VaultUi.Error(OwnerWindow, r.Error!); return; }
        Detail.Clear();
        await ReloadAsync();
        _shell.ShowStatus(Strings.EntryRestored);
    }

    private async void EmptyTrash_Click(object sender, RoutedEventArgs e)
    {
        var all = await _shell.VaultCallAsync(a => a.List(new VaultQuery(OnlyTrash: true)));
        var count = all.Ok ? all.Value!.Count : _entries.Count;
        if (count == 0) { _shell.ShowStatus(Strings.TrashAlreadyEmpty); return; }
        if (!VaultUi.Confirm(OwnerWindow, Strings.EmptyTrashConfirm(count), Strings.AppName, warning: true)) return;
        var r = await _shell.VaultCallAsync(a => a.EmptyTrash());
        if (!r.Ok) { VaultUi.Error(OwnerWindow, r.Error!); return; }
        Detail.Clear();
        await ReloadAsync();
        _shell.ShowStatus(Strings.TrashEmptied(r.Value));
    }

    // ---- groups -----------------------------------------------------------------------------------

    private async void NewGroup_Click(object sender, RoutedEventArgs e)
    {
        var name = InputWindow.Ask(OwnerWindow, Strings.BtnNewGroup, Strings.GroupNamePrompt, "", GroupNames.Normalize);
        if (name is null) return;
        var parent = _groupId;
        var r = await _shell.VaultCallAsync(a => a.GroupAdd(parent, name));
        if (!r.Ok) { VaultUi.Error(OwnerWindow, r.Error!); return; }
        await ReloadAsync();
    }

    private async void RenameGroup_Click(object sender, RoutedEventArgs e)
    {
        if (GroupList.SelectedItem is not GroupNode node) return;
        var name = InputWindow.Ask(OwnerWindow, Strings.BtnRenameGroup, Strings.GroupNamePrompt, node.Name, GroupNames.Normalize);
        if (name is null) return;
        var r = await _shell.VaultCallAsync(a => a.GroupRename(node.Id, name));
        if (!r.Ok) { VaultUi.Error(OwnerWindow, r.Error!); return; }
        await ReloadAsync();
    }

    private async void DeleteGroup_Click(object sender, RoutedEventArgs e)
    {
        if (GroupList.SelectedItem is not GroupNode node) return;
        if (node.Entries > 0 || _groups.Any(g => g.ParentId == node.Id)) { VaultUi.Info(OwnerWindow, Strings.GroupDeleteHint); return; }
        if (!VaultUi.Confirm(OwnerWindow, Strings.BtnDeleteGroup + "：" + node.Name + "？")) return;
        var r = await _shell.VaultCallAsync(a => a.GroupDelete(node.Id));
        if (!r.Ok) { VaultUi.Error(OwnerWindow, r.Error!); return; }
        _groupId = "";
        await ReloadAsync();
    }

    // ---- toolbar: tools ---------------------------------------------------------------------------

    private async void Lock_Click(object sender, RoutedEventArgs e) => await _shell.LockVaultAsync();

    private async void Health_Click(object sender, RoutedEventArgs e)
    {
        var id = HealthWindow.Pick(OwnerWindow, _shell);
        if (id is not null) await JumpToAsync(id);
    }

    private async void Backups_Click(object sender, RoutedEventArgs e)
    {
        if (BackupsWindow.Show(OwnerWindow, _shell))
        {
            Detail.Clear();
            await ReloadAsync();
        }
    }

    private void More_Click(object sender, RoutedEventArgs e)
    {
        MoreMenu.PlacementTarget = MoreButton;
        MoreMenu.IsOpen = true;
    }

    private async void Import_Click(object sender, RoutedEventArgs e)
    {
        if (ImportWindow.Show(OwnerWindow, _shell))
        {
            Detail.Clear();
            await ReloadAsync();
        }
    }

    private void Export_Click(object sender, RoutedEventArgs e) => ExportWindow.Show(OwnerWindow, _shell);

    private void ChangeMaster_Click(object sender, RoutedEventArgs e) => ChangeMasterWindow.Show(OwnerWindow, _shell);

    private async void Settings_Click(object sender, RoutedEventArgs e)
    {
        VaultSettingsWindow.Show(OwnerWindow, _shell);
        var st = await _shell.VaultCallAsync(a => a.Status());
        if (st.Ok)
        {
            Detail.AutolockSeconds = st.Value!.AutolockS;
            _shell.VaultAutolockSeconds = st.Value.AutolockS;
            AutolockChanged?.Invoke(st.Value.AutolockS);
        }
    }
}
