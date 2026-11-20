using Avalonia;
using Avalonia.Controls;
using Ellipse = Avalonia.Controls.Shapes.Ellipse;
using Avalonia.Platform.Storage;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;
using Drawing = System.Drawing;
using WinForms = System.Windows.Forms;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The right-hand side of the notes page: title, formatting toolbar, Markdown source and the preview. It holds no rules: every
/// change of the text is reported through <see cref="Edited"/> and the page feeds it back what the editor session decides
/// (<see cref="SetText"/>, <see cref="SetState"/>, <see cref="SetPreview"/>). Formatting only edits the source text, through
/// <see cref="MarkdownEdit"/>.
/// </summary>
internal sealed class NoteEditorPane : UserControl
{
    private const uint CF_BITMAP = 2;
    private const uint CF_DIB = 8;
    private const uint CF_HDROP = 15;

    /// <summary>"\r\n" is one position in the selection of some text boxes: probed once, see <see cref="TextOffsets"/>.</summary>
    private static readonly Lazy<bool> BreaksCountOne = new(() =>
    {
        var probe = new TextBox { Text = "a\r\nb" };
        probe.SelectionStart = 3;
        probe.SelectionEnd = 4;
        return probe.SelectedText != "b";
    });

    private readonly EmptyState _empty = new() { Icon = IconKind.FileText, Title = Strings.NoteEditorEmptyTitle, Description = Strings.NoteEditorEmpty };
    private readonly Grid _editorGrid = new() { RowDefinitions = new RowDefinitions("Auto,Auto,Auto,*,Auto"), IsVisible = false };
    private readonly TextBox _title = new() { MaxLength = 300, PlaceholderText = Strings.NoteTitleHint, FontSize = 18, FontWeight = FontWeight.SemiBold, BorderThickness = new Thickness(0), Background = Brushes.Transparent, Padding = new Thickness(2, 4) };
    private readonly TextBox _source = new() { AcceptsReturn = true, AcceptsTab = false, TextWrapping = TextWrapping.Wrap, VerticalContentAlignment = VerticalAlignment.Top, FontSize = 13.5, Padding = new Thickness(14, 12), Classes = { "mono" } };
    private readonly InfoBar _trashBanner = new() { Severity = Severity.Warning, IsOpen = false };
    private readonly WrapPanel _toolbar = new() { Orientation = Orientation.Horizontal };
    private readonly SegmentedControl _modes = new();
    private readonly Grid _content = new() { ColumnDefinitions = new ColumnDefinitions("*,10,*") };
    private readonly GridSplitter _splitter = new() { Background = Brushes.Transparent, ResizeDirection = GridResizeDirection.Columns };
    private readonly Border _previewFrame;
    private readonly NotePreview _preview = new();
    private readonly TextBlock _status = Ui.Text("", "caption", "tertiary").With(t => { t.VerticalAlignment = VerticalAlignment.Center; t.TextWrapping = TextWrapping.NoWrap; });
    private readonly Ellipse _statusDot = new() { Width = 7, Height = 7, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(2, 0, 8, 0) };
    private readonly Button _retry;
    private readonly Button _historyButton;
    private readonly Button _deleteButton;
    private bool _suppress;
    private bool _readOnly;
    private bool _frozen;
    private NotesViewMode _mode = NotesViewModes.Default;

    /// <summary>The title or the text changed (the whole values; line breaks may be "\r\n").</summary>
    public event Action<string, string>? Edited;

    /// <summary>The keyboard focus left the editor: the page saves what is typed.</summary>
    public event Action? FocusLeft;

    public event Action? HistoryRequested;
    public event Action? DeleteRequested;
    public event Action? RestoreRequested;
    public event Action? SaveNowRequested;
    public event Action<NotesViewMode>? ViewModeChanged;
    public event Action<string>? LinkClicked;

    /// <summary>
    /// Files to store as attachments and insert (picked with the button, dropped, or pasted from the clipboard). The flag says the
    /// files are temporary copies (a pasted picture) that the receiver deletes after the core has read them.
    /// </summary>
    public event Action<IReadOnlyList<string>, bool>? AttachRequested;

    /// <summary>A short message for the user (a pasted picture could not be saved).</summary>
    public event Action<string>? Message;

    /// <summary>The preview, so that the page can hand it the attachments.</summary>
    internal NotePreview PreviewControl => _preview;

    public NoteEditorPane()
    {
        // ---- title row
        _historyButton = Ui.IconButton(AppIcons.History, Strings.BtnNoteHistory, () => HistoryRequested?.Invoke());
        _deleteButton = Ui.IconButton(AppIcons.Of(IconKind.Trash), Strings.BtnNoteDelete, () => DeleteRequested?.Invoke());
        var titleRow = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto"), Margin = new Thickness(0, 0, 0, 8) };
        titleRow.Children.Add(_title);
        var titleButtons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 2, VerticalAlignment = VerticalAlignment.Center, Children = { _modes, _historyButton, _deleteButton } };
        _modes.Margin = new Thickness(8, 0, 6, 0);
        Grid.SetColumn(titleButtons, 1);
        titleRow.Children.Add(titleButtons);

        // ---- trash banner
        _trashBanner.Actions = Ui.Button(AppIcons.RotateCcw, Strings.BtnNoteRestore, () => RestoreRequested?.Invoke(), "sm");
        _trashBanner.Margin = new Thickness(0, 0, 0, 8);

        // ---- toolbar
        void Format(string icon, string tip, MarkdownAction action) =>
            _toolbar.Children.Add(Ui.IconButton(icon, tip, () => ApplyFormat(action), "sm").With(b => b.Focusable = false));
        void FormatText(string label, string tip, MarkdownAction action) =>
            _toolbar.Children.Add(Ui.TextButton(label, () => ApplyFormat(action), "ghost", "sm").With(b => { b.Focusable = false; b.MinWidth = 28; b.Padding = new Thickness(6, 0); ToolTip.SetTip(b, tip); }));
        void Divider() => _toolbar.Children.Add(new Border { Width = 1, Height = 18, Margin = new Thickness(6, 0), VerticalAlignment = VerticalAlignment.Center }.With(b => b[!Border.BackgroundProperty] = b.GetResourceObservable("Lumen.Brush.Border").ToBinding()));
        Format(AppIcons.Bold, Strings.EditBoldTip, MarkdownAction.Bold);
        Format(AppIcons.Italic, Strings.EditItalicTip, MarkdownAction.Italic);
        Format(AppIcons.Strikethrough, Strings.EditStrikeTip, MarkdownAction.Strikethrough);
        Format(AppIcons.Code, Strings.EditCodeTip, MarkdownAction.InlineCode);
        Divider();
        FormatText("H1", Strings.EditHeadingTip1, MarkdownAction.Heading1);
        FormatText("H2", Strings.EditHeadingTip2, MarkdownAction.Heading2);
        FormatText("H3", Strings.EditHeadingTip3, MarkdownAction.Heading3);
        Divider();
        Format(AppIcons.List, Strings.EditBulletTip, MarkdownAction.BulletList);
        FormatText("1.", Strings.EditNumberedTip, MarkdownAction.NumberedList);
        Format(AppIcons.CheckSquare, Strings.EditTaskTip, MarkdownAction.TaskList);
        FormatText("❝", Strings.EditQuoteTip, MarkdownAction.Quote);
        Format(AppIcons.Terminal, Strings.EditCodeBlockTip, MarkdownAction.CodeBlock);
        Format(AppIcons.Link, Strings.EditLinkTip, MarkdownAction.Link);
        Format(AppIcons.Of(IconKind.Minus), Strings.EditRuleTip, MarkdownAction.HorizontalRule);
        Divider();
        _toolbar.Children.Add(Ui.IconButton(AppIcons.Paperclip, Strings.EditAttachTip, () => _ = AttachAsync(), "sm").With(b => b.Focusable = false));

        foreach (var (label, icon) in new[] { (Strings.NoteViewSource, AppIcons.Code), (Strings.NoteViewSplit, AppIcons.Of(IconKind.LayoutDashboard)), (Strings.NoteViewPreview, AppIcons.Of(IconKind.Eye)) })
            _modes.Items.Add(new SegmentedItem { Content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 5, Children = { Ui.Icon(icon, 13), new TextBlock { Text = label, FontSize = 12 } } } });
        _modes.SelectionChanged += (_, _) =>
        {
            if (_suppress) return;
            SetViewMode(_modes.SelectedIndex switch { 0 => NotesViewMode.Source, 2 => NotesViewMode.Preview, _ => NotesViewMode.Split });
        };
        var toolRow = new Border { Child = _toolbar, Margin = new Thickness(0, 0, 0, 8), Padding = new Thickness(4, 2), CornerRadius = new CornerRadius(8) };
        toolRow[!Border.BackgroundProperty] = toolRow.GetResourceObservable("Lumen.Brush.SurfaceSubtle").ToBinding();

        // ---- source and preview
        _previewFrame = new Border { BorderThickness = new Thickness(1), CornerRadius = new CornerRadius(8), Child = _preview, ClipToBounds = true };
        _previewFrame[!Border.BorderBrushProperty] = _previewFrame.GetResourceObservable("Lumen.Brush.Border").ToBinding();
        _previewFrame[!Border.BackgroundProperty] = _previewFrame.GetResourceObservable("Lumen.Brush.SurfaceSubtle").ToBinding();
        _content.Children.Add(_source);
        Grid.SetColumn(_splitter, 1);
        _content.Children.Add(_splitter);
        Grid.SetColumn(_previewFrame, 2);
        _content.Children.Add(_previewFrame);

        // ---- status row
        _retry = Ui.Button(AppIcons.Save, Strings.BtnNoteSaveNow, () => SaveNowRequested?.Invoke(), "soft", "sm");
        _retry.IsVisible = false;
        var statusRow = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto"), Margin = new Thickness(0, 8, 0, 0) };
        statusRow.Children.Add(_statusDot);
        Grid.SetColumn(_status, 1);
        statusRow.Children.Add(_status);
        Grid.SetColumn(_retry, 2);
        statusRow.Children.Add(_retry);

        Place(titleRow, 0);
        Place(_trashBanner, 1);
        Place(toolRow, 2);
        Place(_content, 3);
        Place(statusRow, 4);
        Content = new Panel { Children = { _empty, _editorGrid } };

        // ---- events
        _preview.LinkClicked += href => LinkClicked?.Invoke(href);
        _title.TextChanged += (_, _) => { if (!_suppress) Edited?.Invoke(_title.Text ?? "", _source.Text ?? ""); };
        _source.TextChanged += (_, _) => { if (!_suppress) Edited?.Invoke(_title.Text ?? "", _source.Text ?? ""); };
        _title.AddHandler(KeyDownEvent, OnTitleKeyDown, RoutingStrategies.Tunnel);
        _source.AddHandler(KeyDownEvent, OnSourceKeyDown, RoutingStrategies.Tunnel);
        _source.PastingFromClipboard += OnPasting;
        DragDrop.SetAllowDrop(_source, true);
        _source.AddHandler(DragDrop.DragOverEvent, OnDragOver, RoutingStrategies.Tunnel);
        _source.AddHandler(DragDrop.DropEvent, OnDrop, RoutingStrategies.Tunnel);
        PropertyChanged += (_, e) =>
        {
            if (e.Property == IsKeyboardFocusWithinProperty && e.NewValue is false) FocusLeft?.Invoke();
        };
        SetViewMode(NotesViewModes.Default, notify: false);
    }

    private void Place(Control child, int row)
    {
        Grid.SetRow(child, row);
        _editorGrid.Children.Add(child);
    }

    public NotesViewMode ViewMode => _mode;

    // ---- what the page tells the pane ------------------------------------------------------------------

    public void ShowEmpty()
    {
        _empty.IsVisible = true;
        _editorGrid.IsVisible = false;
        _preview.Clear();
    }

    /// <summary>Shows a note from the start: its title, its text, no preview yet.</summary>
    public void ShowNote(NoteDetail note)
    {
        _suppress = true;
        _title.Text = note.Title;
        _source.Text = note.Body;
        _suppress = false;
        _readOnly = note.Deleted;
        ApplyReadOnly();
        _empty.IsVisible = false;
        _editorGrid.IsVisible = true;
        _source.CaretIndex = 0;
        _preview.Clear();
    }

    /// <summary>The core's text replaced the text in the boxes (a reload, a restored version). The caret stays where it was.</summary>
    public void SetText(string title, string body)
    {
        var titleCaret = _title.CaretIndex;
        var bodyCaret = _source.CaretIndex;
        _suppress = true;
        if (ClipText.ToCore(_title.Text ?? "") != title)
        {
            _title.Text = title;
            _title.CaretIndex = Math.Min(titleCaret, title.Length);
        }
        if (ClipText.ToCore(_source.Text ?? "") != body)
        {
            _source.Text = body;
            _source.CaretIndex = Math.Min(bodyCaret, body.Length);
        }
        _suppress = false;
    }

    /// <summary>The page is saving or reading another note: nothing may be typed meanwhile.</summary>
    public void Freeze(bool frozen)
    {
        _frozen = frozen;
        ApplyReadOnly();
    }

    public void SetPreview(RenderTree? tree) => _preview.Show(tree);

    public void FocusTitle() => Dispatcher.UIThread.Post(() => _title.Focus(), DispatcherPriority.Input);

    public void FocusBody() => Dispatcher.UIThread.Post(() => _source.Focus(), DispatcherPriority.Input);

    /// <summary>The status line, the recycle bin banner and the buttons follow the editor's state.</summary>
    public void SetState(NoteEditorModel model)
    {
        _readOnly = model.ReadOnly;
        ApplyReadOnly();
        _status.Text = model.State switch
        {
            EditorSaveState.Dirty => Strings.NoteStateDirty,
            EditorSaveState.Saving => Strings.NoteStateSaving,
            EditorSaveState.Failed => Strings.NoteStateFailed,
            EditorSaveState.TooLarge => Strings.NoteStateTooLarge,
            EditorSaveState.Trashed => Strings.NoteStateTrashed,
            _ => model.IsOpen ? Strings.NoteStateSaved : "",
        };
        var bad = model.State is EditorSaveState.Failed or EditorSaveState.TooLarge or EditorSaveState.Trashed;
        var busy = model.State is EditorSaveState.Dirty or EditorSaveState.Saving;
        _status.Classes.Set("danger", bad);
        _status.Classes.Set("tertiary", !bad);
        var dot = bad ? "Lumen.Brush.Danger" : busy ? "Lumen.Brush.Warning" : "Lumen.Brush.Success";
        _statusDot[!Avalonia.Controls.Shapes.Shape.FillProperty] = _statusDot.GetResourceObservable(dot).ToBinding();
        _statusDot.IsVisible = model.IsOpen;
        _retry.IsVisible = model.State == EditorSaveState.Failed;
        _trashBanner.IsOpen = model.InTrash;
        _trashBanner.Message = model.ReadOnly ? Strings.NoteInTrashReadOnly : Strings.NoteTrashedWhileEditing;
        _deleteButton.IsVisible = !model.InTrash;
    }

    private void ApplyReadOnly()
    {
        var locked = _readOnly || _frozen;
        _title.IsReadOnly = locked;
        _source.IsReadOnly = locked;
        _toolbar.IsEnabled = !_readOnly && !_frozen && _mode != NotesViewMode.Preview;
    }

    // ---- view mode ----------------------------------------------------------------------------------------

    public void SetViewMode(NotesViewMode mode, bool notify = true)
    {
        _mode = mode;
        _suppress = true;
        _modes.SelectedIndex = mode switch { NotesViewMode.Source => 0, NotesViewMode.Preview => 2, _ => 1 };
        _suppress = false;
        var showSource = mode != NotesViewMode.Preview;
        var showPreview = mode != NotesViewMode.Source;
        _source.IsVisible = showSource;
        _previewFrame.IsVisible = showPreview;
        _splitter.IsVisible = mode == NotesViewMode.Split;
        _content.ColumnDefinitions[0].Width = showSource ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
        _content.ColumnDefinitions[1].Width = mode == NotesViewMode.Split ? new GridLength(10) : new GridLength(0);
        _content.ColumnDefinitions[2].Width = showPreview ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
        ApplyReadOnly();
        if (notify) ViewModeChanged?.Invoke(mode);
    }

    // ---- typing -------------------------------------------------------------------------------------------

    private void OnTitleKeyDown(object? sender, KeyEventArgs e)
    {
        if (e.Key == Key.S && e.KeyModifiers == KeyModifiers.Control)
        {
            e.Handled = true;
            SaveNowRequested?.Invoke();
        }
        else if (e.Key == Key.Enter && e.KeyModifiers == KeyModifiers.None)
        {
            e.Handled = true; // Enter in the title goes on to the text, as in most note apps
            _source.Focus();
        }
    }

    /// <summary>Ctrl+B bold, Ctrl+I italic, Ctrl+K link, Ctrl+S save now.</summary>
    private void OnSourceKeyDown(object? sender, KeyEventArgs e)
    {
        if (e.KeyModifiers != KeyModifiers.Control) return;
        MarkdownAction? action = e.Key switch
        {
            Key.B => MarkdownAction.Bold,
            Key.I => MarkdownAction.Italic,
            Key.K => MarkdownAction.Link,
            _ => null,
        };
        if (e.Key == Key.S)
        {
            e.Handled = true;
            SaveNowRequested?.Invoke();
        }
        else if (action is { } a)
        {
            e.Handled = true;
            ApplyFormat(a);
        }
    }

    // ---- formatting ---------------------------------------------------------------------------------------

    private static int ToIndex(string text, int boxPosition) => BreaksCountOne.Value ? TextOffsets.FromBox(text, boxPosition) : boxPosition;

    private static int ToPosition(string text, int index) => BreaksCountOne.Value ? TextOffsets.ToBox(text, index) : index;

    /// <summary>
    /// Applies a toolbar action to the selection. The change goes in as the replacement of one stretch of text (so Ctrl+Z
    /// undoes it) and the selection ends up on the formatted text.
    /// </summary>
    private void ApplyFormat(MarkdownAction action)
    {
        if (_source.IsReadOnly || !_source.IsVisible) return;
        ApplyEdit((text, from, length) => MarkdownEdit.Apply(action, text, from, length, Environment.NewLine));
        _source.Focus();
    }

    /// <summary>Makes the edit that <paramref name="plan"/> works out for the text and the selection (indexes into the text).</summary>
    private void ApplyEdit(Func<string, int, int, TextEdit> plan)
    {
        var text = _source.Text ?? "";
        var selFrom = Math.Min(_source.SelectionStart, _source.SelectionEnd);
        var selTo = Math.Max(_source.SelectionStart, _source.SelectionEnd);
        var from = ToIndex(text, selFrom);
        var to = ToIndex(text, selTo);
        var edit = plan(text, from, to - from);

        var start = ToPosition(text, edit.Start);
        _source.SelectionStart = start;
        _source.SelectionEnd = ToPosition(text, edit.Start + edit.RemoveLength);
        _source.SelectedText = edit.Insert;

        var after = _source.Text ?? "";
        _source.SelectionStart = ToPosition(after, edit.SelectionStart);
        _source.SelectionEnd = ToPosition(after, edit.SelectionStart + edit.SelectionLength);
    }

    /// <summary>Puts the Markdown of a new attachment on a line of its own at the caret.</summary>
    public void InsertBlock(string markdown)
    {
        if (_source.IsReadOnly) return;
        ApplyEdit((text, from, length) => MarkdownEdit.InsertBlock(text, from, length, markdown, Environment.NewLine));
    }

    // ---- attachments: button, paste, drop -------------------------------------------------------------

    private async Task AttachAsync()
    {
        if (_source.IsReadOnly) return;
        var files = await Ui.PickFilesAsync(this, Strings.AttachPickTitle);
        if (files.Count > 0) AttachRequested?.Invoke(files, false);
    }

    /// <summary>
    /// Pasting a picture or files stores them as attachments. Text always wins (Excel puts a picture next to the cells' text, and a
    /// copied file name is text too), so ordinary pastes are left to the text box.
    /// </summary>
    private void OnPasting(object? sender, RoutedEventArgs e)
    {
        if (_source.IsReadOnly) return;
        if (NativeMethods.IsClipboardFormatAvailable(NativeMethods.CF_UNICODETEXT)) return;
        try
        {
            if (NativeMethods.IsClipboardFormatAvailable(CF_HDROP))
            {
                var files = WinForms.Clipboard.GetFileDropList().Cast<string>().ToList();
                if (files.Count == 0) return;
                e.Handled = true;
                AttachRequested?.Invoke(files, false);
            }
            else if (NativeMethods.IsClipboardFormatAvailable(CF_DIB) || NativeMethods.IsClipboardFormatAvailable(CF_BITMAP))
            {
                using var picture = WinForms.Clipboard.GetImage();
                if (picture is null) return;
                e.Handled = true;
                if (SavePastedPicture(picture) is { } path) AttachRequested?.Invoke(new[] { path }, true);
                else Message?.Invoke(Strings.AttachNoTemp);
            }
        }
        catch (System.Runtime.InteropServices.ExternalException)
        {
            // The clipboard is held by another program: let the text box try its own paste.
        }
    }

    /// <summary>The picture of the clipboard as a PNG in its own temporary folder (so the attachment gets a clean name), or null.</summary>
    private static string? SavePastedPicture(Drawing.Image picture)
    {
        try
        {
            var dir = Path.Combine(Path.GetTempPath(), "ClipBridge-notes", "paste-" + Guid.NewGuid().ToString("N")[..8]);
            Directory.CreateDirectory(dir);
            var path = Path.Combine(dir, AttachmentPolicy.PastedImageName(DateTimeOffset.Now));
            picture.Save(path, Drawing.Imaging.ImageFormat.Png);
            return path;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or NotSupportedException or ArgumentException
                                       or System.Runtime.InteropServices.ExternalException)
        {
            return null;
        }
    }

    /// <summary>Deletes a temporary file made by <see cref="SavePastedPicture"/> together with its folder.</summary>
    internal static void DeleteTemporary(string path)
    {
        try
        {
            File.Delete(path);
            var dir = Path.GetDirectoryName(path);
            if (dir is not null && Path.GetFileName(dir).StartsWith("paste-", StringComparison.Ordinal)) Directory.Delete(dir);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { /* temp files are cleaned up by the system one day */ }
    }

    private void OnDragOver(object? sender, DragEventArgs e)
    {
        if (_source.IsReadOnly || !e.DataTransfer.Contains(DataFormat.File)) return;
        e.DragEffects = DragDropEffects.Copy;
        e.Handled = true;
    }

    private void OnDrop(object? sender, DragEventArgs e)
    {
        if (_source.IsReadOnly || !e.DataTransfer.Contains(DataFormat.File)) return;
        var files = e.DataTransfer.TryGetFiles()?.Select(f => f.TryGetLocalPath()).OfType<string>().ToList();
        if (files is not { Count: > 0 }) return;
        e.Handled = true;
        AttachRequested?.Invoke(files, false); // the Markdown goes in at the caret
    }
}
