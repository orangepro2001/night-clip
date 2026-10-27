using System.Windows;
using System.Windows.Controls;
using System.Windows.Controls.Primitives;
using System.Windows.Input;
using System.Windows.Media.Imaging;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using Microsoft.Win32;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The right-hand side of the notes page: title, formatting toolbar, Markdown source and the preview. It holds no rules: every
/// change of the text is reported through <see cref="Edited"/> and the page feeds it back what the editor session decides
/// (<see cref="SetText"/>, <see cref="SetState"/>, <see cref="SetPreview"/>). Formatting only edits the source text, through
/// <see cref="MarkdownEdit"/>.
/// </summary>
public partial class NoteEditorPane : UserControl
{
    /// <summary>"\r\n" is one position in <c>SelectionStart</c>/<c>Select</c> on some text boxes: probed once, see <see cref="TextOffsets"/>.</summary>
    private static readonly Lazy<bool> BreaksCountOne = new(() =>
    {
        var probe = new TextBox { Text = "a\r\nb" };
        probe.Select(3, 1);
        return probe.SelectedText != "b";
    });

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

    /// <summary>A short message for the status line (a pasted picture could not be saved).</summary>
    public event Action<string>? Message;

    /// <summary>The preview, so that the page can hand it the attachments.</summary>
    internal NotePreview PreviewControl => Preview;

    public NoteEditorPane()
    {
        InitializeComponent();
        Preview.LinkClicked += href => LinkClicked?.Invoke(href);
        DataObject.AddPastingHandler(SourceBox, Source_Pasting);
        IsKeyboardFocusWithinChanged += (_, e) => { if (!(bool)e.NewValue) FocusLeft?.Invoke(); };
        SetViewMode(NotesViewModes.Default, notify: false);
    }

    public NotesViewMode ViewMode => _mode;

    // ---- what the page tells the pane ------------------------------------------------------------------

    public void ShowEmpty()
    {
        EmptyText.Visibility = Visibility.Visible;
        EditorGrid.Visibility = Visibility.Collapsed;
        Preview.Clear();
    }

    /// <summary>Shows a note from the start: its title, its text, no preview yet.</summary>
    public void ShowNote(NoteDetail note)
    {
        _suppress = true;
        TitleBox.Text = note.Title;
        SourceBox.Text = note.Body;
        _suppress = false;
        _readOnly = note.Deleted;
        ApplyReadOnly();
        UpdateTitleHint();
        EmptyText.Visibility = Visibility.Collapsed;
        EditorGrid.Visibility = Visibility.Visible;
        SourceBox.CaretIndex = 0;
        SourceBox.ScrollToHome();
        Preview.Clear();
    }

    /// <summary>The core's text replaced the text in the boxes (a reload, a restored version). The caret stays where it was.</summary>
    public void SetText(string title, string body)
    {
        var titleCaret = TitleBox.CaretIndex;
        var bodyCaret = SourceBox.CaretIndex;
        _suppress = true;
        if (ClipText.ToCore(TitleBox.Text) != title)
        {
            TitleBox.Text = title;
            TitleBox.CaretIndex = Math.Min(titleCaret, TitleBox.Text.Length);
        }
        if (ClipText.ToCore(SourceBox.Text) != body)
        {
            SourceBox.Text = body;
            SourceBox.CaretIndex = Math.Min(bodyCaret, SourceBox.Text.Length);
        }
        _suppress = false;
        UpdateTitleHint();
    }

    /// <summary>The page is saving or reading another note: nothing may be typed meanwhile.</summary>
    public void Freeze(bool frozen)
    {
        _frozen = frozen;
        ApplyReadOnly();
    }

    public void SetPreview(RenderTree? tree) => Preview.Show(tree);

    public void FocusTitle() => Dispatcher.BeginInvoke(new Action(() => TitleBox.Focus()), DispatcherPriority.Input);

    public void FocusBody() => Dispatcher.BeginInvoke(new Action(() => SourceBox.Focus()), DispatcherPriority.Input);

    /// <summary>The status text, the recycle bin banner and the buttons follow the editor's state.</summary>
    public void SetState(NoteEditorModel model)
    {
        _readOnly = model.ReadOnly;
        ApplyReadOnly();
        StatusText.Text = model.State switch
        {
            EditorSaveState.Dirty => Strings.NoteStateDirty,
            EditorSaveState.Saving => Strings.NoteStateSaving,
            EditorSaveState.Failed => Strings.NoteStateFailed,
            EditorSaveState.TooLarge => Strings.NoteStateTooLarge,
            EditorSaveState.Trashed => Strings.NoteStateTrashed,
            _ => model.IsOpen ? Strings.NoteStateSaved : "",
        };
        var bad = model.State is EditorSaveState.Failed or EditorSaveState.TooLarge or EditorSaveState.Trashed;
        StatusText.SetResourceReference(TextBlock.ForegroundProperty, bad ? "AppDanger" : "AppFaint");
        RetryButton.Visibility = model.State == EditorSaveState.Failed ? Visibility.Visible : Visibility.Collapsed;
        TrashBanner.Visibility = model.InTrash ? Visibility.Visible : Visibility.Collapsed;
        TrashText.Text = model.ReadOnly ? Strings.NoteInTrashReadOnly : Strings.NoteTrashedWhileEditing;
        DeleteButton.Visibility = model.InTrash ? Visibility.Collapsed : Visibility.Visible;
    }

    private void ApplyReadOnly()
    {
        var locked = _readOnly || _frozen;
        TitleBox.IsReadOnly = locked;
        SourceBox.IsReadOnly = locked;
        Toolbar.IsEnabled = !_readOnly && !_frozen && _mode != NotesViewMode.Preview;
    }

    // ---- view mode ----------------------------------------------------------------------------------------

    public void SetViewMode(NotesViewMode mode, bool notify = true)
    {
        _mode = mode;
        ModeSource.IsChecked = mode == NotesViewMode.Source;
        ModeSplit.IsChecked = mode == NotesViewMode.Split;
        ModePreview.IsChecked = mode == NotesViewMode.Preview;
        var showSource = mode != NotesViewMode.Preview;
        var showPreview = mode != NotesViewMode.Source;
        SourceBox.Visibility = showSource ? Visibility.Visible : Visibility.Collapsed;
        PreviewFrame.Visibility = showPreview ? Visibility.Visible : Visibility.Collapsed;
        Splitter.Visibility = mode == NotesViewMode.Split ? Visibility.Visible : Visibility.Collapsed;
        SourceColumn.Width = showSource ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
        SplitterColumn.Width = mode == NotesViewMode.Split ? new GridLength(8) : new GridLength(0);
        PreviewColumn.Width = showPreview ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
        ApplyReadOnly();
        if (notify) ViewModeChanged?.Invoke(mode);
    }

    private void Mode_Click(object sender, RoutedEventArgs e)
    {
        if (sender is ToggleButton { Tag: string tag }) SetViewMode(NotesViewModes.Parse(tag.ToLowerInvariant()));
    }

    // ---- typing -------------------------------------------------------------------------------------------

    private void UpdateTitleHint() => TitleHint.Visibility = TitleBox.Text.Length == 0 ? Visibility.Visible : Visibility.Collapsed;

    private void Title_TextChanged(object sender, TextChangedEventArgs e)
    {
        UpdateTitleHint();
        if (!_suppress) Edited?.Invoke(TitleBox.Text, SourceBox.Text);
    }

    private void Source_TextChanged(object sender, TextChangedEventArgs e)
    {
        if (!_suppress) Edited?.Invoke(TitleBox.Text, SourceBox.Text);
    }

    private void Title_PreviewKeyDown(object sender, KeyEventArgs e)
    {
        if (e.Key == Key.S && Keyboard.Modifiers == ModifierKeys.Control)
        {
            e.Handled = true;
            SaveNowRequested?.Invoke();
        }
    }

    /// <summary>Ctrl+B bold, Ctrl+I italic, Ctrl+K link, Ctrl+S save now.</summary>
    private void Source_PreviewKeyDown(object sender, KeyEventArgs e)
    {
        if (Keyboard.Modifiers != ModifierKeys.Control) return;
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

    private void Format_Click(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: string tag } && Enum.TryParse<MarkdownAction>(tag, out var action)) ApplyFormat(action);
    }

    private int ToIndex(string text, int boxPosition) => BreaksCountOne.Value ? TextOffsets.FromBox(text, boxPosition) : boxPosition;

    private int ToPosition(string text, int index) => BreaksCountOne.Value ? TextOffsets.ToBox(text, index) : index;

    /// <summary>
    /// Applies a toolbar action to the selection. The change goes in as the replacement of one stretch of text (so Ctrl+Z
    /// undoes it) and the selection ends up on the formatted text.
    /// </summary>
    private void ApplyFormat(MarkdownAction action)
    {
        if (SourceBox.IsReadOnly || SourceBox.Visibility != Visibility.Visible) return;
        ApplyEdit((text, from, length) => MarkdownEdit.Apply(action, text, from, length, Environment.NewLine));
        SourceBox.Focus();
    }

    /// <summary>Makes the edit that <paramref name="plan"/> works out for the text and the selection (indexes into the text).</summary>
    private void ApplyEdit(Func<string, int, int, TextEdit> plan)
    {
        var text = SourceBox.Text;
        var from = ToIndex(text, SourceBox.SelectionStart);
        var to = ToIndex(text, SourceBox.SelectionStart + SourceBox.SelectionLength);
        var edit = plan(text, from, to - from);

        var start = ToPosition(text, edit.Start);
        SourceBox.Select(start, ToPosition(text, edit.Start + edit.RemoveLength) - start);
        SourceBox.SelectedText = edit.Insert;

        var after = SourceBox.Text;
        var selStart = ToPosition(after, edit.SelectionStart);
        SourceBox.Select(selStart, ToPosition(after, edit.SelectionStart + edit.SelectionLength) - selStart);
    }

    /// <summary>Puts the Markdown of a new attachment on a line of its own at the caret.</summary>
    public void InsertBlock(string markdown)
    {
        if (SourceBox.IsReadOnly) return;
        ApplyEdit((text, from, length) => MarkdownEdit.InsertBlock(text, from, length, markdown, Environment.NewLine));
    }

    // ---- attachments: button, paste, drop -------------------------------------------------------------

    private void Attach_Click(object sender, RoutedEventArgs e)
    {
        if (SourceBox.IsReadOnly) return;
        var dialog = new OpenFileDialog { Title = Strings.AttachPickTitle, Multiselect = true, CheckFileExists = true };
        if (dialog.ShowDialog(Window.GetWindow(this)) == true && dialog.FileNames.Length > 0) AttachRequested?.Invoke(dialog.FileNames, false);
    }

    /// <summary>
    /// Pasting a picture or files stores them as attachments. Text always wins (Excel puts a picture next to the cells' text, and a
    /// copied file name is text too), so ordinary pastes are left to the text box.
    /// </summary>
    private void Source_Pasting(object sender, DataObjectPastingEventArgs e)
    {
        if (SourceBox.IsReadOnly) return;
        var data = e.DataObject;
        if (data.GetDataPresent(DataFormats.UnicodeText) || data.GetDataPresent(DataFormats.Text)) return;
        if (data.GetDataPresent(DataFormats.FileDrop) && data.GetData(DataFormats.FileDrop) is string[] { Length: > 0 } files)
        {
            e.CancelCommand();
            AttachRequested?.Invoke(files, false);
        }
        else if (data.GetDataPresent(DataFormats.Bitmap) && data.GetData(DataFormats.Bitmap) is BitmapSource picture)
        {
            e.CancelCommand();
            if (SavePastedPicture(picture) is { } path) AttachRequested?.Invoke(new[] { path }, true);
            else Message?.Invoke(Strings.AttachNoTemp);
        }
    }

    /// <summary>The picture of the clipboard as a PNG in its own temporary folder (so the attachment gets a clean name), or null.</summary>
    private static string? SavePastedPicture(BitmapSource picture)
    {
        try
        {
            var dir = Path.Combine(Path.GetTempPath(), "ClipBridge-notes", "paste-" + Guid.NewGuid().ToString("N")[..8]);
            Directory.CreateDirectory(dir);
            var path = Path.Combine(dir, AttachmentPolicy.PastedImageName(DateTimeOffset.Now));
            var encoder = new PngBitmapEncoder();
            encoder.Frames.Add(BitmapFrame.Create(picture));
            using (var stream = File.Create(path)) encoder.Save(stream);
            return path;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or NotSupportedException or ArgumentException)
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

    private void Source_PreviewDragOver(object sender, DragEventArgs e)
    {
        if (SourceBox.IsReadOnly || !e.Data.GetDataPresent(DataFormats.FileDrop)) return;
        e.Effects = DragDropEffects.Copy;
        e.Handled = true;
    }

    private void Source_PreviewDrop(object sender, DragEventArgs e)
    {
        if (SourceBox.IsReadOnly || !e.Data.GetDataPresent(DataFormats.FileDrop)) return;
        if (e.Data.GetData(DataFormats.FileDrop) is not string[] { Length: > 0 } files) return;
        e.Handled = true;
        var index = SourceBox.GetCharacterIndexFromPoint(e.GetPosition(SourceBox), snapToText: true);
        if (index >= 0) SourceBox.Select(index, 0); // the Markdown goes where the files were dropped
        AttachRequested?.Invoke(files, false);
    }

    // ---- buttons ------------------------------------------------------------------------------------------

    private void History_Click(object sender, RoutedEventArgs e) => HistoryRequested?.Invoke();
    private void Delete_Click(object sender, RoutedEventArgs e) => DeleteRequested?.Invoke();
    private void Restore_Click(object sender, RoutedEventArgs e) => RestoreRequested?.Invoke();
    private void SaveNow_Click(object sender, RoutedEventArgs e) => SaveNowRequested?.Invoke();
}
