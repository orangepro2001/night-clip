using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The entry editor: kind templates, password strength bar, generator popup, custom fields and the "unsaved changes"
/// prompt. The form is only a view of an <see cref="EntryDraft"/>; validation and the JSON sent to the core are in
/// Logic/EntryDraft.cs.
/// </summary>
public partial class EntryEditorWindow : Window
{
    private sealed class FieldRow
    {
        public required ComboBox Name { get; init; }
        public required SecretBox Value { get; init; }
        public required CheckBox Protected { get; init; }
        public required UIElement Root { get; init; }

        public DraftField ToDraft() => new() { Name = Name.Text, Value = Value.Text, Protected = Protected.IsChecked == true };
    }

    private readonly ShellController _shell;
    private readonly IReadOnlyList<GroupInfo> _groups;
    private readonly List<FieldRow> _rows = new();
    private readonly DispatcherTimer _strengthTimer = new() { Interval = TimeSpan.FromMilliseconds(250) };
    private readonly string _editId;
    private EntryDraft _original = new();
    private long _originalExpiry;
    private int _strengthGeneration;
    private bool _loading;
    private bool _saved;
    private bool _ready;

    private EntryEditorWindow(ShellController shell, IReadOnlyList<GroupInfo> groups, string editId)
    {
        _shell = shell;
        _groups = groups;
        _editId = editId;
        InitializeComponent();
        shell.ProtectDialog(this);
        VaultUi.Track(this, () => { _saved = true; Close(); });

        foreach (var kind in EntryKinds.All)
            KindBox.Items.Add(new ComboBoxItem { Tag = kind, Content = KindTemplate.For(kind).Glyph + "  " + KindTemplate.For(kind).Label });
        GroupCombo.ItemsSource = GroupTree.Choices(groups);

        PwdBox.TextChanged += () => { _strengthTimer.Stop(); _strengthTimer.Start(); };
        _strengthTimer.Tick += async (_, _) => { _strengthTimer.Stop(); await UpdateStrengthAsync(); };
        PreviewKeyDown += (_, e) =>
        {
            _shell.NoteVaultActivity(); // typing a long note is activity: keep the core's idle timer from locking mid-edit
            if (e.Key == System.Windows.Input.Key.Escape) Close();
        };
        PreviewMouseDown += (_, _) => _shell.NoteVaultActivity();
        Closing += OnClosing;
        Closed += (_, _) => WipeSecrets();
        _ready = true;
    }

    /// <summary>
    /// Opens the editor modally. <paramref name="editId"/> empty = a new entry (in <paramref name="defaultGroupId"/>, of
    /// <paramref name="defaultKind"/>). Returns the id of the saved entry, or null when nothing was saved.
    /// </summary>
    internal static async Task<string?> ShowAsync(Window? owner, ShellController shell, IReadOnlyList<GroupInfo> groups,
        string editId, string defaultGroupId, string defaultKind)
    {
        var w = new EntryEditorWindow(shell, groups, editId);
        VaultUi.Place(w, owner);
        EntryDraft draft;
        if (editId.Length == 0)
        {
            draft = new EntryDraft { Kind = EntryKinds.Normalize(defaultKind), GroupId = defaultGroupId };
            w.Title = Strings.EditorNewTitle;
            w.Fill(draft);
        }
        else
        {
            w.Title = Strings.EditorEditTitle;
            w.SetLoading(true);
            _ = w.LoadAsync();
        }
        return w.ShowDialog() == true ? w._savedId : null;
    }

    private string? _savedId;

    // ---- loading / filling ----------------------------------------------------------------------

    private void SetLoading(bool loading)
    {
        _loading = loading;
        FormPanel.IsEnabled = !loading;
        SaveButton.IsEnabled = !loading;
        LoadingText.Visibility = loading ? Visibility.Visible : Visibility.Collapsed;
    }

    private async Task LoadAsync()
    {
        var r = await _shell.VaultCallAsync(a => EntryDraftLoader.Load(a, _editId));
        if (!r.Ok)
        {
            VaultUi.Error(this, r.Error!);
            _saved = true; // nothing to discard
            Close();
            return;
        }
        Fill(r.Value!);
        SetLoading(false);
    }

    private void Fill(EntryDraft d)
    {
        _originalExpiry = d.ExpiresAt;
        TitleBox.Text = d.Title;
        UserBox.Text = d.Username;
        PwdBox.Text = d.Password;
        UrlBox.Text = d.Url;
        TotpBox.Text = d.Totp;
        NotesBox.Text = d.Notes;
        TagsBox.Text = d.TagsText;
        ExpiryPicker.SelectedDate = ExpiryDates.ToDate(d.ExpiresAt);
        FieldsPanel.Children.Clear();
        _rows.Clear();
        foreach (var f in d.Fields) AddFieldRow(f);

        foreach (ComboBoxItem item in KindBox.Items)
            if ((string)item.Tag == d.Kind) KindBox.SelectedItem = item;
        var choices = (IReadOnlyList<GroupNode>)GroupCombo.ItemsSource;
        GroupCombo.SelectedValue = choices.Any(g => g.Id == d.GroupId) ? d.GroupId : choices.FirstOrDefault()?.Id;
        _original = d.Clone();
        _original.GroupId = GroupCombo.SelectedValue as string ?? d.GroupId;
        // The form normalises a few things (tags, empty field rows); compare against what it would produce now.
        _original = Collect();
        UpdateSections();
        _ = UpdateStrengthAsync();
    }

    private EntryDraft Collect()
    {
        var d = new EntryDraft
        {
            Id = _editId,
            GroupId = GroupCombo.SelectedValue as string ?? "",
            Kind = KindBox.SelectedItem is ComboBoxItem { Tag: string k } ? k : EntryKinds.Login,
            Title = TitleBox.Text, Username = UserBox.Text, Password = PwdBox.Text, Url = UrlBox.Text, Totp = TotpBox.Text,
            Notes = NotesBox.Text, TagsText = TagsBox.Text,
            Fields = _rows.Select(r => r.ToDraft()).Where(f => f.Name.Trim().Length > 0 || f.Value.Length > 0).ToList(),
        };
        var picked = ExpiryPicker.SelectedDate;
        // A date that is the one already stored keeps its exact time (KeePassXC writes other times of day).
        d.ExpiresAt = picked is null ? 0
            : _originalExpiry > 0 && ExpiryDates.ToDate(_originalExpiry) == picked.Value.Date ? _originalExpiry
            : ExpiryDates.FromDate(picked);
        return d;
    }

    private bool IsDirty() => !_loading && !Collect().SameContentAs(_original);

    private void WipeSecrets()
    {
        PwdBox.Clear();
        TotpBox.Clear();
        foreach (var r in _rows) r.Value.Clear();
        NotesBox.Clear();
        _original = new EntryDraft();
    }

    // ---- kind templates ------------------------------------------------------------------------------

    private void KindBox_SelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!_ready) return;
        UpdateSections();
    }

    private void UpdateSections()
    {
        var draft = Collect();
        var t = KindTemplate.For(draft.Kind);
        var v = FormVisibility.For(draft);
        UserSection.Visibility = v.Username ? Visibility.Visible : Visibility.Collapsed;
        PwdSection.Visibility = v.Password ? Visibility.Visible : Visibility.Collapsed;
        UrlSection.Visibility = v.Url ? Visibility.Visible : Visibility.Collapsed;
        TotpSection.Visibility = v.Totp ? Visibility.Visible : Visibility.Collapsed;
        ExpirySection.Visibility = v.Expiry ? Visibility.Visible : Visibility.Collapsed;
        TagsSection.Visibility = v.Tags ? Visibility.Visible : Visibility.Collapsed;
        NotesSection.Visibility = v.Notes ? Visibility.Visible : Visibility.Collapsed;
        FieldsSection.Visibility = v.CustomFields ? Visibility.Visible : Visibility.Collapsed;
        PwdLabel.Text = t.PasswordLabel;
        UrlLabel.Text = t.UrlLabel;

        SuggestionPanel.Children.Clear();
        if (t.FieldSuggestions.Count > 0)
        {
            SuggestionPanel.Children.Add(new TextBlock { Text = Strings.FieldSuggestionHint, VerticalAlignment = VerticalAlignment.Center, Foreground = VaultUi.MutedBrush });
            foreach (var name in t.FieldSuggestions)
            {
                var n = name;
                var b = new Button { Content = n, Padding = new Thickness(8, 2, 8, 2) };
                b.Click += (_, _) =>
                {
                    if (!_rows.Any(r => string.Equals(r.Name.Text.Trim(), n, StringComparison.OrdinalIgnoreCase)))
                        AddFieldRow(new DraftField { Name = n });
                };
                SuggestionPanel.Children.Add(b);
            }
        }
    }

    // ---- custom fields -----------------------------------------------------------------------------------

    private void AddField_Click(object sender, RoutedEventArgs e) => AddFieldRow(new DraftField());

    private void AddFieldRow(DraftField f)
    {
        var name = new ComboBox { IsEditable = true, Text = f.Name, Width = 150, Margin = new Thickness(0, 0, 6, 0) };
        foreach (var s in KindTemplate.For("apikey").FieldSuggestions) name.Items.Add(s);
        var value = new SecretBox { Masked = f.Protected, Text = f.Value };
        var prot = new CheckBox { Content = Strings.FieldProtected, IsChecked = f.Protected, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(8, 0, 0, 0) };
        prot.Click += (_, _) => value.Masked = prot.IsChecked == true;
        var remove = new Button { Content = Strings.BtnRemove };

        var line = new DockPanel { Margin = new Thickness(0, 4, 0, 0) };
        DockPanel.SetDock(remove, Dock.Right);
        DockPanel.SetDock(prot, Dock.Right);
        DockPanel.SetDock(name, Dock.Left);
        line.Children.Add(remove);
        line.Children.Add(prot);
        line.Children.Add(name);
        line.Children.Add(value);

        var row = new FieldRow { Name = name, Value = value, Protected = prot, Root = line };
        remove.Click += (_, _) =>
        {
            value.Clear();
            _rows.Remove(row);
            FieldsPanel.Children.Remove(line);
        };
        _rows.Add(row);
        FieldsPanel.Children.Add(line);
    }

    // ---- password -------------------------------------------------------------------------------------------

    private async Task UpdateStrengthAsync()
    {
        var gen = ++_strengthGeneration;
        var password = PwdBox.Text;
        if (password.Length == 0) { PwdStrength.Clear(); return; }
        var r = await _shell.VaultCallAsync(a => a.Strength(password));
        if (gen == _strengthGeneration && r.Ok) PwdStrength.Set(r.Value!.Label, r.Value.Bits);
    }

    private void Generate_Click(object sender, RoutedEventArgs e)
    {
        var generated = PasswordGeneratorWindow.Ask(this, _shell);
        if (generated is not null) PwdBox.Text = generated;
    }

    // ---- save / cancel -------------------------------------------------------------------------------------

    private async void Save_Click(object sender, RoutedEventArgs e)
    {
        var draft = Collect();
        var issues = EntryValidator.Validate(draft);
        ErrorText.Text = string.Join("\n", issues.Where(i => i.IsError).Select(i => i.Message));
        WarnText.Text = string.Join("\n", issues.Where(i => !i.IsError).Select(i => i.Message));
        if (EntryValidator.HasErrors(issues)) return;

        SaveButton.IsEnabled = false;
        var groupChanged = _editId.Length > 0 && draft.GroupId != _original.GroupId;
        var r = await _shell.VaultCallAsync(a =>
        {
            if (_editId.Length == 0) return a.Add(draft);
            a.Update(_editId, draft);
            if (groupChanged) a.Move(_editId, draft.GroupId);
            return _editId;
        });
        SaveButton.IsEnabled = true;
        if (!r.Ok)
        {
            ErrorText.Text = r.Error ?? Strings.ErrVaultUnexpected;
            return;
        }
        _saved = true;
        _savedId = r.Value;
        DialogResult = true;
    }

    private void Cancel_Click(object sender, RoutedEventArgs e) => Close();

    private void OnClosing(object? sender, System.ComponentModel.CancelEventArgs e)
    {
        if (_saved || DialogResult == true) return;
        if (IsDirty() && !VaultUi.Confirm(this, Strings.DiscardChangesText, Strings.DiscardChangesTitle, warning: true))
            e.Cancel = true;
    }
}
