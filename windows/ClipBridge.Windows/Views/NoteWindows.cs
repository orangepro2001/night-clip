using System.Collections.ObjectModel;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Templates;
using Avalonia.Layout;
using Avalonia.Media;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The revisions of a note. Choosing one shows its text read-only; "恢复此版本" hands that text back to the page, which stores
/// it as a normal edit (so nothing is lost: the version it replaces stays in the history).
/// </summary>
internal sealed class NoteHistoryWindow : SmallWindow
{
    private const int PageSize = 50;

    private readonly ShellController _shell;
    private readonly string _noteId;
    private readonly bool _canRestore;
    private readonly ObservableCollection<NoteRevisionRow> _rows = new();
    private readonly ListBox _list = new();
    private readonly Button _more;
    private readonly Button _use;
    private readonly TextBlock _info = Ui.Text(Strings.NoteHistoryLoading, "secondary");
    private readonly TextBox _body = new() { IsReadOnly = true, AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, VerticalContentAlignment = VerticalAlignment.Top, Classes = { "mono" } };
    private readonly NotePreview _rendered = new();
    private readonly Border _renderedFrame;
    private readonly SegmentedControl _view = new();
    private string _cursor = "";
    private NoteDetail? _shown;
    private bool _showRendered;
    private int _generation;

    private NoteHistoryWindow(ShellController shell, string noteId, bool canRestore) : base(840, resizable: true)
    {
        _shell = shell;
        _noteId = noteId;
        _canRestore = canRestore;
        Title = Strings.NoteHistoryTitle;
        Height = 560;
        MinWidth = 640;
        MinHeight = 400;
        ShowInTaskbar = false;

        _list.ItemsSource = _rows;
        _list.ItemTemplate = Ui.Template<NoteRevisionRow>(row => new StackPanel
        {
            Spacing = 1,
            Margin = new Thickness(0, 2),
            Children =
            {
                new TextBlock { Text = row.TimeText, FontWeight = FontWeight.Medium },
                Ui.Text(row.OriginText + "  ·  " + row.Title, "caption", "tertiary").With(t => { t.TextWrapping = TextWrapping.NoWrap; t.TextTrimming = TextTrimming.CharacterEllipsis; }),
            },
        });
        _list.SelectionChanged += async (_, _) => await SelectedAsync();
        _more = Ui.TextButton(Strings.BtnNoteLoadMore, () => _ = LoadPageAsync(), "ghost", "block");
        _more.IsVisible = false;
        var left = new DockPanel();
        DockPanel.SetDock(_more, Dock.Bottom);
        left.Children.Add(_more);
        left.Children.Add(_list);

        _view.Items.Add(new SegmentedItem { Content = Strings.NoteViewSource });
        _view.Items.Add(new SegmentedItem { Content = Strings.NoteViewPreview });
        _view.SelectedIndex = 0;
        _view.SelectionChanged += async (_, _) => await SetRenderedAsync(_view.SelectedIndex == 1);
        _renderedFrame = new Border { BorderThickness = new Thickness(1), CornerRadius = new CornerRadius(8), Child = _rendered, IsVisible = false, ClipToBounds = true };
        _renderedFrame[!Border.BorderBrushProperty] = _renderedFrame.GetResourceObservable("Lumen.Brush.Border").ToBinding();
        _renderedFrame[!Border.BackgroundProperty] = _renderedFrame.GetResourceObservable("Lumen.Brush.SurfaceSubtle").ToBinding();
        _rendered.ResolveAttachment = (blobId, wantPicture) => Task.Run(() =>
        {
            try { return _shell.Attachments.Resolve(blobId, wantPicture); }
            catch (Exception) { return AttachmentView.Failure(blobId, "INTERNAL"); }
        });
        _rendered.LinkClicked += href =>
        {
            if (_shell.OpenNoteLink(href) is { } error) _info.Text = error;
        };
        var rightTop = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto"), Margin = new Thickness(0, 0, 0, 8) };
        rightTop.Children.Add(_info.With(t => t.VerticalAlignment = VerticalAlignment.Center));
        Grid.SetColumn(_view, 1);
        rightTop.Children.Add(_view);
        var right = new DockPanel();
        DockPanel.SetDock(rightTop, Dock.Top);
        right.Children.Add(rightTop);
        right.Children.Add(new Panel { Children = { _body, _renderedFrame } });

        _use = Ui.Button(AppIcons.RotateCcw, Strings.BtnNoteUseRevision, () => Use(), "primary");
        _use.IsEnabled = false;
        var close = new Button { Content = Strings.Close, MinWidth = 88 };
        close.Click += (_, _) => Close();
        var footer = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Margin = new Thickness(0, 14, 0, 0), Children = { close, _use } };

        var columns = new Grid { ColumnDefinitions = new ColumnDefinitions("260,14,*") };
        columns.Children.Add(left);
        Grid.SetColumn(right, 2);
        columns.Children.Add(right);
        var root = new DockPanel();
        var heading = Ui.Text(Strings.NoteHistoryTitle, "title").With(t => t.Margin = new Thickness(0, 0, 0, 12));
        DockPanel.SetDock(heading, Dock.Top);
        DockPanel.SetDock(footer, Dock.Bottom);
        root.Children.Add(heading);
        root.Children.Add(footer);
        root.Children.Add(columns);
        SetBody(root);
        Opened += async (_, _) => await LoadPageAsync();
    }

    /// <summary>The revision the user wants back (its title and text), or null.</summary>
    private NoteDetail? Picked { get; set; }

    internal static async Task<NoteDetail?> PickAsync(Window? owner, ShellController shell, string noteId, bool canRestore)
    {
        var w = new NoteHistoryWindow(shell, noteId, canRestore);
        if (owner is { IsVisible: true })
        {
            w.WindowStartupLocation = WindowStartupLocation.CenterOwner;
            await w.ShowDialog(owner);
        }
        else
        {
            var done = new TaskCompletionSource();
            w.Closed += (_, _) => done.TrySetResult();
            w.Show();
            await done.Task;
        }
        return w.Picked;
    }

    private string DeviceName(string deviceId) => _shell.Peers.FirstOrDefault(p => p.DeviceId == deviceId)?.DisplayName ?? "";

    private async Task LoadPageAsync()
    {
        var cursor = _cursor;
        var r = await _shell.NotesCallAsync(api => api.History(_noteId, PageSize, cursor));
        if (!r.Ok)
        {
            _info.Text = r.Error ?? "";
            return;
        }
        foreach (var revision in r.Value!.Items) _rows.Add(new NoteRevisionRow(revision, _shell.DeviceId, DeviceName));
        _cursor = r.Value.NextCursor;
        _more.IsVisible = _cursor.Length > 0;
        if (_rows.Count == 0) _info.Text = Strings.NoteHistoryEmpty;
        else if (_list.SelectedIndex < 0) _list.SelectedIndex = 0;
    }

    private async Task SelectedAsync()
    {
        if (_list.SelectedItem is not NoteRevisionRow row) return;
        var generation = ++_generation;
        _shown = null;
        _use.IsEnabled = false;
        var r = await _shell.NotesCallAsync(api => api.Get(_noteId, row.Revision.RevId));
        if (generation != _generation) return; // another revision was chosen meanwhile
        if (!r.Ok)
        {
            _body.Text = "";
            _info.Text = r.Error ?? "";
            return;
        }
        var d = r.Value!;
        _shown = d;
        _body.Text = d.Body;
        _body.CaretIndex = 0;
        _info.Text = d.Deleted ? Strings.NoteRevisionDeletedInfo
            : Strings.NoteRevisionInfo(d.Title.Length > 0 ? d.Title : Strings.Untitled, d.IsHead);
        _use.IsEnabled = _canRestore && !d.Deleted && !d.IsHead;
        if (_showRendered) await RenderShownAsync(generation);
    }

    private async Task SetRenderedAsync(bool rendered)
    {
        _showRendered = rendered;
        _body.IsVisible = !rendered;
        _renderedFrame.IsVisible = rendered;
        if (rendered) await RenderShownAsync(_generation);
    }

    /// <summary>Draws the revision that is shown from the core's render tree of that revision (<c>notes.render {note_id, rev_id}</c>).</summary>
    private async Task RenderShownAsync(int generation)
    {
        if (_shown is not { Deleted: false } d)
        {
            _rendered.Clear();
            return;
        }
        var r = await _shell.NotesCallAsync(api => api.Render(_noteId, d.RevId));
        if (generation != _generation) return;
        if (r.Ok) _rendered.Show(r.Value);
        else _info.Text = r.Error ?? "";
    }

    private void Use()
    {
        if (_shown is null) return;
        Picked = _shown;
        Close();
    }
}

/// <summary>
/// Shows a conflict copy next to the note it was set aside from and lets the user settle it: keep the note as it is (the copy
/// is dropped), put the copy's text in its place, or append the copy to the note. All three go through
/// <c>notes.sync_resolve</c>; the copy always ends up in the recycle bin, so nothing is lost for good.
/// </summary>
internal sealed class NoteConflictWindow : SmallWindow
{
    private readonly ShellController _shell;
    private readonly ObservableCollection<NoteConflictRow> _rows;
    private readonly ListBox _list = new();
    private readonly TextBlock _mainTitle = Ui.Text("", "caption", "tertiary");
    private readonly TextBlock _copyTitle = Ui.Text("", "caption", "tertiary");
    private readonly TextBox _mainBox = new() { IsReadOnly = true, AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, VerticalContentAlignment = VerticalAlignment.Top, Classes = { "mono" } };
    private readonly TextBox _copyBox = new() { IsReadOnly = true, AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, VerticalContentAlignment = VerticalAlignment.Top, Classes = { "mono" } };
    private readonly StatusBar _error = new();
    private readonly Button _keepMain;
    private readonly Button _keepCopy;
    private readonly Button _merge;
    private NoteConflict? _selected;
    private bool _busy;
    private int _generation;

    private NoteConflictWindow(ShellController shell, IReadOnlyList<NoteConflict> conflicts) : base(980, resizable: true)
    {
        _shell = shell;
        _rows = new ObservableCollection<NoteConflictRow>(conflicts.Select(c => new NoteConflictRow(c)));
        Title = Strings.NoteConflictTitle;
        Height = 620;
        MinWidth = 780;
        MinHeight = 440;
        ShowInTaskbar = false;

        _list.ItemsSource = _rows;
        _list.ItemTemplate = Ui.Template<NoteConflictRow>(row => new StackPanel
        {
            Spacing = 1,
            Margin = new Thickness(0, 2),
            Children =
            {
                new TextBlock { Text = row.Title, FontWeight = FontWeight.Medium, TextTrimming = TextTrimming.CharacterEllipsis },
                Ui.Text(row.TimeText, "caption", "tertiary"),
            },
        });
        _list.SelectionChanged += async (_, _) => await SelectedAsync();

        static Control Column(string heading, TextBlock title, TextBox body)
        {
            var dock = new DockPanel();
            var head = new StackPanel { Spacing = 2, Margin = new Thickness(0, 0, 0, 8), Children = { Ui.Text(heading, "medium"), title } };
            DockPanel.SetDock(head, Dock.Top);
            dock.Children.Add(head);
            dock.Children.Add(body);
            return dock;
        }

        _keepMain = Ui.TextButton(Strings.BtnNoteKeepMine, () => _ = ResolveAsync(ConflictActions.KeepMain));
        ToolTip.SetTip(_keepMain, Strings.NoteKeepMineTip);
        _keepCopy = Ui.TextButton(Strings.BtnNoteKeepCopy, () => _ = ResolveAsync(ConflictActions.KeepConflict));
        ToolTip.SetTip(_keepCopy, Strings.NoteKeepCopyTip);
        _merge = Ui.TextButton(Strings.BtnNoteMerge, () => _ = ResolveAsync(ConflictActions.Merge), "primary");
        ToolTip.SetTip(_merge, Strings.NoteMergeTip);
        var later = new Button { Content = Strings.BtnNoteLater };
        later.Classes.Add("ghost");
        later.Click += (_, _) => Close();
        EnableButtons(false);

        var columns = new Grid { ColumnDefinitions = new ColumnDefinitions("220,14,*,14,*") };
        columns.Children.Add(_list);
        var mine = Column(Strings.NoteConflictMine, _mainTitle, _mainBox);
        Grid.SetColumn(mine, 2);
        columns.Children.Add(mine);
        var copy = Column(Strings.NoteConflictCopy, _copyTitle, _copyBox);
        Grid.SetColumn(copy, 4);
        columns.Children.Add(copy);

        var footer = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto"), Margin = new Thickness(0, 14, 0, 0) };
        footer.Children.Add(later);
        var actions = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Children = { _keepMain, _keepCopy, _merge } };
        Grid.SetColumn(actions, 2);
        footer.Children.Add(actions);

        var top = new StackPanel { Spacing = 6, Margin = new Thickness(0, 0, 0, 12), Children = { Ui.Text(Strings.NoteConflictTitle, "title"), Ui.Text(Strings.NoteConflictIntro, "secondary") } };
        var bottom = new StackPanel { Children = { _error, footer } };
        var root = new DockPanel();
        DockPanel.SetDock(top, Dock.Top);
        DockPanel.SetDock(bottom, Dock.Bottom);
        root.Children.Add(top);
        root.Children.Add(bottom);
        root.Children.Add(columns);
        SetBody(root);
        Opened += (_, _) => { if (_rows.Count > 0) _list.SelectedIndex = 0; };
    }

    /// <summary>True when at least one conflict was settled.</summary>
    private bool Changed { get; set; }

    internal static async Task<bool> ShowForAsync(Window? owner, ShellController shell, IReadOnlyList<NoteConflict> conflicts)
    {
        if (conflicts.Count == 0) return false;
        var w = new NoteConflictWindow(shell, conflicts);
        if (owner is { IsVisible: true })
        {
            w.WindowStartupLocation = WindowStartupLocation.CenterOwner;
            await w.ShowDialog(owner);
        }
        else
        {
            var done = new TaskCompletionSource();
            w.Closed += (_, _) => done.TrySetResult();
            w.Show();
            await done.Task;
        }
        return w.Changed;
    }

    private void EnableButtons(bool enabled)
    {
        _keepMain.IsEnabled = enabled;
        _keepCopy.IsEnabled = enabled;
        _merge.IsEnabled = enabled;
    }

    private async Task SelectedAsync()
    {
        if (_list.SelectedItem is not NoteConflictRow row) return;
        var generation = ++_generation;
        _selected = null;
        EnableButtons(false);
        _error.Hide();
        _mainTitle.Text = _copyTitle.Text = Strings.NoteHistoryLoading;
        _mainBox.Text = _copyBox.Text = "";
        var conflict = row.Conflict;
        var main = _shell.NotesCallAsync(api => api.Get(conflict.NoteId));
        var copy = _shell.NotesCallAsync(api => api.Get(conflict.ConflictNoteId));
        var mainResult = await main;
        var copyResult = await copy;
        if (generation != _generation) return; // another conflict was chosen meanwhile
        _mainTitle.Text = _copyTitle.Text = "";
        if (!mainResult.Ok || !copyResult.Ok)
        {
            _error.Say(mainResult.Error ?? copyResult.Error ?? "", false);
            return;
        }
        Fill(_mainTitle, _mainBox, mainResult.Value!);
        Fill(_copyTitle, _copyBox, copyResult.Value!);
        _selected = conflict;
        EnableButtons(!_busy);
    }

    private static void Fill(TextBlock title, TextBox body, NoteDetail note)
    {
        title.Text = note.Title.Length > 0 ? note.Title : Strings.Untitled;
        body.Text = note.Body;
        body.CaretIndex = 0;
    }

    private async Task ResolveAsync(string action)
    {
        if (_selected is not { } conflict || _busy) return;
        _busy = true;
        EnableButtons(false);
        _error.Hide();
        var r = await _shell.NotesCallAsync(api => api.Resolve(conflict.NoteId, conflict.ConflictNoteId, action));
        _busy = false;
        if (r.Ok) Changed = true;
        if (!IsVisible) return; // the window was closed while the core worked
        if (!r.Ok)
        {
            _error.Say(r.Error ?? "", false);
            EnableButtons(true);
            return;
        }
        var row = _rows.FirstOrDefault(x => x.Conflict.ConflictNoteId == conflict.ConflictNoteId);
        var index = row is null ? 0 : _rows.IndexOf(row);
        if (row is not null) _rows.Remove(row);
        if (_rows.Count == 0)
        {
            Close();
            return;
        }
        _list.SelectedIndex = Math.Min(index, _rows.Count - 1);
    }
}
