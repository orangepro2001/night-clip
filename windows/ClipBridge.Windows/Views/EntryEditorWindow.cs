using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Templates;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The entry editor: kind templates, password strength bar, generator, custom fields and the "unsaved changes" prompt.
/// The form is only a view of an <see cref="EntryDraft"/>; validation and the JSON sent to the core are in Logic/EntryDraft.cs.
/// </summary>
internal sealed class EntryEditorWindow : SmallWindow
{
    private sealed class FieldRow
    {
        public required TextBox Name { get; init; }
        public required SecretBox Value { get; init; }
        public required CheckBox Protected { get; init; }
        public required Control Root { get; init; }

        public DraftField ToDraft() => new() { Name = Name.Text ?? "", Value = Value.Text, Protected = Protected.IsChecked == true };
    }

    private readonly ShellController _shell;
    private readonly List<FieldRow> _rows = new();
    private readonly DispatcherTimer _strengthTimer = new() { Interval = TimeSpan.FromMilliseconds(250) };
    private readonly string _editId;
    private readonly IReadOnlyList<GroupNode> _groupChoices;
    private EntryDraft _original = new();
    private long _originalExpiry;
    private int _strengthGeneration;
    private bool _loading;
    private bool _saved;
    private bool _discardConfirmed;
    private bool _ready;
    private string? _savedId;

    private readonly StackPanel _form = new() { Spacing = 14, Margin = new Thickness(0, 0, 14, 8) };
    private readonly SegmentedControl _kindBox = new() { Classes = { "block" } };
    private readonly ComboBox _groupBox = new() { HorizontalAlignment = HorizontalAlignment.Stretch };
    private readonly TextBox _titleBox = new() { MaxLength = 2000 };
    private readonly TextBox _userBox = new();
    private readonly SecretBox _pwdBox = new();
    private readonly StrengthBar _pwdStrength = new() { Margin = new Thickness(0, 6, 0, 0) };
    private readonly TextBox _urlBox = new() { PlaceholderText = "https://" };
    private readonly SecretBox _totpBox = new() { Placeholder = "otpauth://… / Base32" };
    private readonly CalendarDatePicker _expiryPicker = new() { Width = 200, HorizontalAlignment = HorizontalAlignment.Left };
    private readonly TextBox _tagsBox = new();
    private readonly TextBox _notesBox = new() { AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, MinHeight = 96, MaxHeight = 260, IsUndoEnabled = false };
    private readonly StackPanel _fieldsPanel = new() { Spacing = 6 };
    private readonly WrapPanel _suggestionPanel = new() { Orientation = Orientation.Horizontal };
    private readonly FormField _pwdField = new() { Label = Strings.LabelPassword };
    private readonly FormField _urlField = new() { Label = Strings.LabelUrl };
    private readonly InfoBar _errorBar = new() { Severity = Severity.Danger, IsOpen = false };
    private readonly InfoBar _warnBar = new() { Severity = Severity.Warning, IsOpen = false };
    private readonly StackPanel _loadingRow;
    private readonly Button _saveButton = new() { Content = Strings.BtnSave, MinWidth = 96, IsDefault = true, Classes = { "primary" } };
    private readonly Control _userSection, _pwdSection, _urlSection, _totpSection, _expirySection, _tagsSection, _notesSection, _fieldsSection;

    private EntryEditorWindow(ShellController shell, IReadOnlyList<GroupInfo> groups, string editId) : base(600, resizable: true)
    {
        _shell = shell;
        _editId = editId;
        _groupChoices = GroupTree.Choices(groups);
        Title = Strings.EditorNewTitle;
        Height = 740;
        MinWidth = 500;
        MinHeight = 480;
        ShowInTaskbar = false;

        foreach (var k in EntryKinds.All)
        {
            var t = KindTemplate.For(k);
            _kindBox.Items.Add(new SegmentedItem
            {
                Tag = k,
                Content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, Children = { Ui.Icon(VaultUi.KindIcon(k), 14), new TextBlock { Text = t.Label } } },
            });
        }
        _groupBox.ItemsSource = _groupChoices;
        _groupBox.ItemTemplate = Ui.Template<GroupNode>(n => new TextBlock { Text = n.Name, Margin = new Thickness(n.Depth * 12, 0, 0, 0) });

        var top = new Grid { ColumnDefinitions = new ColumnDefinitions("*,14,180") };
        top.Children.Add(new FormField { Label = Strings.LabelTitle, IsRequired = true, Content = _titleBox });
        var groupField = new FormField { Label = Strings.LabelGroup, Content = _groupBox };
        Grid.SetColumn(groupField, 2);
        top.Children.Add(groupField);
        _form.Children.Add(new FormField { Label = Strings.LabelKind, Content = _kindBox });
        _form.Children.Add(top);

        _userSection = new FormField { Label = Strings.LabelUsername, Content = _userBox };
        var generate = Ui.Button(AppIcons.Shuffle, Strings.BtnGenerate, () => _ = GenerateAsync());
        generate.Margin = new Thickness(8, 0, 0, 0);
        var pwdLine = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
        pwdLine.Children.Add(_pwdBox);
        Grid.SetColumn(generate, 1);
        pwdLine.Children.Add(generate);
        _pwdField.Content = new StackPanel { Children = { pwdLine, _pwdStrength } };
        _pwdSection = _pwdField;
        _urlField.Content = _urlBox;
        _urlSection = _urlField;
        _totpSection = new FormField { Label = Strings.LabelTotp, Content = _totpBox };
        _expirySection = new FormField { Label = Strings.LabelExpiry, Content = _expiryPicker };
        _tagsSection = new FormField { Label = Strings.LabelTags, Content = _tagsBox };
        _notesSection = new FormField { Label = Strings.LabelNotes, Content = _notesBox };
        var addField = Ui.Button(AppIcons.Of(IconKind.Plus), Strings.BtnAddField, () => AddFieldRow(new DraftField()), "ghost", "sm");
        _fieldsSection = new StackPanel
        {
            Spacing = 6,
            Children = { Ui.Text(Strings.LabelCustomFields, "medium"), _fieldsPanel, _suggestionPanel, addField },
        };
        foreach (var s in new Control[] { _userSection, _pwdSection, _urlSection, _totpSection, _expirySection, _tagsSection, _notesSection, _fieldsSection, _errorBar, _warnBar })
            _form.Children.Add(s);

        var cancel = new Button { Content = Strings.BtnCancel, MinWidth = 88 };
        _loadingRow = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8,
            VerticalAlignment = VerticalAlignment.Center,
            IsVisible = false,
            Children = { new Lumen.UI.Controls.Spinner { Size = 14, Thickness = 2 }, Ui.Text(Strings.EditorLoading, "caption", "secondary") },
        };
        var footer = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto,Auto"), Margin = new Thickness(0, 14, 0, 0) };
        footer.Children.Add(_loadingRow);
        Grid.SetColumn(cancel, 1);
        footer.Children.Add(cancel);
        _saveButton.Margin = new Thickness(8, 0, 0, 0);
        Grid.SetColumn(_saveButton, 2);
        footer.Children.Add(_saveButton);

        var root = new DockPanel();
        DockPanel.SetDock(footer, Dock.Bottom);
        root.Children.Add(footer);
        root.Children.Add(new ScrollViewer { Content = _form });
        SetBody(root, new Thickness(22, 20, 8, 18));

        shell.ProtectDialog(this);
        VaultUi.Track(this, () => { _saved = true; Close(); });
        DialogKit.WireActivity(this, shell); // typing a long note is activity: the idle lock must not strike mid-edit

        _kindBox.SelectionChanged += (_, _) => { if (_ready) UpdateSections(); };
        _pwdBox.TextChanged += () => { _strengthTimer.Stop(); _strengthTimer.Start(); };
        _strengthTimer.Tick += async (_, _) => { _strengthTimer.Stop(); await UpdateStrengthAsync(); };
        _saveButton.Click += async (_, _) => await SaveAsync();
        cancel.Click += (_, _) => Close();
        Closing += OnClosing;
        Closed += (_, _) => { _strengthTimer.Stop(); WipeSecrets(); };
        _ready = true;
    }

    /// <summary>
    /// Opens the editor modally. <paramref name="editId"/> empty = a new entry (in <paramref name="defaultGroupId"/>, of
    /// <paramref name="defaultKind"/>). Returns the id of the saved entry, or null when nothing was saved.
    /// </summary>
    public static async Task<string?> ShowAsync(Window? owner, ShellController shell, IReadOnlyList<GroupInfo> groups,
        string editId, string defaultGroupId, string defaultKind)
    {
        var w = new EntryEditorWindow(shell, groups, editId);
        if (editId.Length == 0)
        {
            w.Fill(new EntryDraft { Kind = EntryKinds.Normalize(defaultKind), GroupId = defaultGroupId });
            w.Opened += (_, _) => w._titleBox.Focus();
        }
        else
        {
            w.Title = Strings.EditorEditTitle;
            w.SetLoading(true);
            w.Opened += async (_, _) => await w.LoadAsync();
        }
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
        return w._savedId;
    }

    // ---- loading / filling ----------------------------------------------------------------------

    private void SetLoading(bool loading)
    {
        _loading = loading;
        _form.IsEnabled = !loading;
        _saveButton.IsEnabled = !loading;
        _loadingRow.IsVisible = loading;
    }

    private async Task LoadAsync()
    {
        var r = await _shell.VaultCallAsync(a => EntryDraftLoader.Load(a, _editId));
        if (!r.Ok)
        {
            _saved = true; // nothing to discard
            await VaultUi.ErrorAsync(this, r.Error!);
            Close();
            return;
        }
        Fill(r.Value!);
        SetLoading(false);
        _titleBox.Focus();
    }

    private string SelectedKind => (_kindBox.SelectedItem as SegmentedItem)?.Tag as string ?? EntryKinds.Login;

    private void Fill(EntryDraft d)
    {
        _ready = false;
        _originalExpiry = d.ExpiresAt;
        _titleBox.Text = d.Title;
        _userBox.Text = d.Username;
        _pwdBox.Text = d.Password;
        _urlBox.Text = d.Url;
        _totpBox.Text = d.Totp;
        _notesBox.Text = d.Notes;
        _tagsBox.Text = d.TagsText;
        _expiryPicker.SelectedDate = ExpiryDates.ToDate(d.ExpiresAt);
        _fieldsPanel.Children.Clear();
        _rows.Clear();
        foreach (var f in d.Fields) AddFieldRow(f);
        var kind = EntryKinds.All.FirstOrDefault(k => k == d.Kind) ?? EntryKinds.Login;
        _kindBox.SelectedItem = _kindBox.Items.OfType<SegmentedItem>().First(i => (string?)i.Tag == kind);
        _groupBox.SelectedItem = _groupChoices.FirstOrDefault(g => g.Id == d.GroupId) ?? _groupChoices.FirstOrDefault();
        _ready = true;
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
            GroupId = (_groupBox.SelectedItem as GroupNode)?.Id ?? "",
            Kind = SelectedKind,
            Title = _titleBox.Text ?? "", Username = _userBox.Text ?? "", Password = _pwdBox.Text, Url = _urlBox.Text ?? "", Totp = _totpBox.Text,
            Notes = _notesBox.Text ?? "", TagsText = _tagsBox.Text ?? "",
            Fields = _rows.Select(r => r.ToDraft()).Where(f => f.Name.Trim().Length > 0 || f.Value.Length > 0).ToList(),
        };
        var picked = _expiryPicker.SelectedDate;
        // A date that is the one already stored keeps its exact time (KeePassXC writes other times of day).
        d.ExpiresAt = picked is null ? 0
            : _originalExpiry > 0 && ExpiryDates.ToDate(_originalExpiry) == picked.Value.Date ? _originalExpiry
            : ExpiryDates.FromDate(picked);
        return d;
    }

    private bool IsDirty() => !_loading && !Collect().SameContentAs(_original);

    private void WipeSecrets()
    {
        _pwdBox.Clear();
        _totpBox.Clear();
        foreach (var r in _rows) r.Value.Clear();
        _notesBox.Text = "";
        _original = new EntryDraft();
    }

    // ---- kind templates ------------------------------------------------------------------------------

    private void UpdateSections()
    {
        var draft = Collect();
        var t = KindTemplate.For(draft.Kind);
        var v = FormVisibility.For(draft);
        _userSection.IsVisible = v.Username;
        _pwdSection.IsVisible = v.Password;
        _urlSection.IsVisible = v.Url;
        _totpSection.IsVisible = v.Totp;
        _expirySection.IsVisible = v.Expiry;
        _tagsSection.IsVisible = v.Tags;
        _notesSection.IsVisible = v.Notes;
        _fieldsSection.IsVisible = v.CustomFields;
        _pwdField.Label = t.PasswordLabel;
        _urlField.Label = t.UrlLabel;

        _suggestionPanel.Children.Clear();
        if (t.FieldSuggestions.Count > 0)
        {
            _suggestionPanel.Children.Add(Ui.Text(Strings.FieldSuggestionHint, "caption", "tertiary").With(x => { x.VerticalAlignment = VerticalAlignment.Center; x.Margin = new Thickness(0, 0, 6, 0); }));
            foreach (var name in t.FieldSuggestions)
            {
                var n = name;
                var b = Ui.TextButton("+ " + n, () =>
                {
                    if (!_rows.Any(r => string.Equals((r.Name.Text ?? "").Trim(), n, StringComparison.OrdinalIgnoreCase)))
                        AddFieldRow(new DraftField { Name = n });
                }, "soft", "sm");
                b.Margin = new Thickness(0, 0, 6, 6);
                _suggestionPanel.Children.Add(b);
            }
        }
    }

    // ---- custom fields -----------------------------------------------------------------------------------

    private void AddFieldRow(DraftField f)
    {
        var name = new TextBox { Text = f.Name, Width = 150, PlaceholderText = Strings.FieldNamePlaceholder };
        var value = new SecretBox { Masked = f.Protected, Text = f.Value };
        var prot = new CheckBox { Content = Strings.FieldProtected, IsChecked = f.Protected, VerticalAlignment = VerticalAlignment.Center };
        prot.IsCheckedChanged += (_, _) => value.Masked = prot.IsChecked == true;
        var line = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,8,*,10,Auto,Auto") };
        line.Children.Add(name);
        Grid.SetColumn(value, 2);
        line.Children.Add(value);
        Grid.SetColumn(prot, 4);
        line.Children.Add(prot);

        FieldRow? row = null;
        var remove = Ui.IconButton(AppIcons.Of(IconKind.X), Strings.BtnRemove, () =>
        {
            value.Clear();
            if (row is not null) _rows.Remove(row);
            _fieldsPanel.Children.Remove(line);
        }, "sm");
        Grid.SetColumn(remove, 5);
        line.Children.Add(remove);

        row = new FieldRow { Name = name, Value = value, Protected = prot, Root = line };
        _rows.Add(row);
        _fieldsPanel.Children.Add(line);
    }

    // ---- password -------------------------------------------------------------------------------------------

    private async Task UpdateStrengthAsync()
    {
        var gen = ++_strengthGeneration;
        var password = _pwdBox.Text;
        if (password.Length == 0) { _pwdStrength.Clear(); return; }
        var r = await _shell.VaultCallAsync(a => a.Strength(password));
        if (gen == _strengthGeneration && r.Ok) _pwdStrength.Set(r.Value!.Label, r.Value.Bits);
    }

    private async Task GenerateAsync()
    {
        if (await PasswordGenerator.AskAsync(this, _shell) is { } generated) _pwdBox.Text = generated;
    }

    // ---- save / cancel -------------------------------------------------------------------------------------

    private async Task SaveAsync()
    {
        if (_loading) return;
        var draft = Collect();
        var issues = EntryValidator.Validate(draft);
        var errors = string.Join("\n", issues.Where(i => i.IsError).Select(i => i.Message));
        var warnings = string.Join("\n", issues.Where(i => !i.IsError).Select(i => i.Message));
        _errorBar.Message = errors;
        _errorBar.IsOpen = errors.Length > 0;
        _warnBar.Message = warnings;
        _warnBar.IsOpen = warnings.Length > 0;
        if (EntryValidator.HasErrors(issues)) return;

        _saveButton.IsEnabled = false;
        var editId = _editId;
        var groupChanged = editId.Length > 0 && draft.GroupId != _original.GroupId;
        var r = await _shell.VaultCallAsync(a =>
        {
            if (editId.Length == 0) return a.Add(draft);
            a.Update(editId, draft);
            if (groupChanged) a.Move(editId, draft.GroupId);
            return editId;
        });
        _saveButton.IsEnabled = true;
        if (!r.Ok)
        {
            _errorBar.Message = r.Error ?? Strings.ErrVaultUnexpected;
            _errorBar.IsOpen = true;
            return;
        }
        _saved = true;
        _savedId = r.Value;
        Close();
    }

    /// <summary>Unsaved changes: cancel the close, ask, and close again when the user agrees.</summary>
    private void OnClosing(object? sender, WindowClosingEventArgs e)
    {
        if (_saved || _discardConfirmed || !IsDirty()) return;
        e.Cancel = true;
        Dispatcher.UIThread.Post(async () =>
        {
            if (!await VaultUi.ConfirmAsync(this, Strings.DiscardChangesText, Strings.DiscardChangesTitle, Strings.BtnDiscard, warning: true)) return;
            _discardConfirmed = true;
            Close();
        });
    }
}

/// <summary>
/// The password generator, as a dialog over the window that asked for it: options in, one generated secret out
/// (vault.generate). The value is only kept in the result box.
/// </summary>
internal sealed class PasswordGenerator : StackPanel
{
    private readonly ShellController _shell;
    private readonly SegmentedControl _mode = new() { Classes = { "block" } };
    private readonly Slider _length = new() { Minimum = GeneratorOptions.MinLength, Maximum = GeneratorOptions.MaxLength, Value = 20, TickFrequency = 1, IsSnapToTickEnabled = true, SmallChange = 1, LargeChange = 4 };
    private readonly Slider _words = new() { Minimum = GeneratorOptions.MinWords, Maximum = GeneratorOptions.MaxWords, Value = 6, TickFrequency = 1, IsSnapToTickEnabled = true, SmallChange = 1, LargeChange = 2 };
    private readonly TextBlock _lengthText = Ui.Text("", "medium");
    private readonly TextBlock _wordsText = Ui.Text("", "medium");
    private readonly CheckBox _lower = new() { Content = Strings.GenLower, IsChecked = true };
    private readonly CheckBox _upper = new() { Content = Strings.GenUpper, IsChecked = true };
    private readonly CheckBox _digits = new() { Content = Strings.GenDigits, IsChecked = true };
    private readonly CheckBox _symbols = new() { Content = Strings.GenSymbols, IsChecked = true };
    private readonly CheckBox _ambiguous = new() { Content = Strings.GenAmbiguous };
    private readonly TextBox _separator = new() { Text = "-", Width = 60, MaxLength = 8 };
    private readonly CheckBox _capitalize = new() { Content = Strings.GenCapitalize, IsChecked = true };
    private readonly CheckBox _number = new() { Content = Strings.GenAddNumber };
    private readonly TextBox _result = new() { IsReadOnly = true, FontSize = 16, TextWrapping = TextWrapping.Wrap, MinHeight = 44, IsUndoEnabled = false, Classes = { "mono" } };
    private readonly StrengthBar _bar = new();
    private readonly InfoBar _error = new() { Severity = Severity.Danger, IsOpen = false };
    private readonly Button _use = new() { Content = Strings.BtnUsePassword, MinWidth = 96, IsDefault = true, Classes = { "primary" } };
    private readonly StackPanel _passwordPane;
    private readonly StackPanel _passphrasePane;
    private bool _ready;
    private bool _adjusting;
    private int _generation;

    private PasswordGenerator(ShellController shell)
    {
        _shell = shell;
        Spacing = 14;
        Width = 440;
        _mode.Items.Add(new SegmentedItem { Content = Strings.GenModePassword });
        _mode.Items.Add(new SegmentedItem { Content = Strings.GenModePassphrase });
        _mode.SelectedIndex = 0;

        var classes = new WrapPanel { Orientation = Orientation.Horizontal };
        foreach (var c in new[] { _lower, _upper, _digits, _symbols })
        {
            c.Margin = new Thickness(0, 0, 16, 4);
            classes.Children.Add(c);
        }
        _passwordPane = new StackPanel { Spacing = 6, Children = { _lengthText, _length, classes, _ambiguous } };
        _passphrasePane = new StackPanel
        {
            Spacing = 6,
            IsVisible = false,
            Children =
            {
                _wordsText, _words,
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Children = { Ui.Text(Strings.GenSeparator).With(t => t.VerticalAlignment = VerticalAlignment.Center), _separator } },
                _capitalize, _number,
            },
        };
        var regenerate = Ui.Button(AppIcons.Of(IconKind.RefreshCw), Strings.BtnRegenerate, () => _ = RegenerateAsync(), "ghost");
        var cancel = new Button { Content = Strings.BtnCancel, MinWidth = 88, IsCancel = true };
        var footer = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto,Auto") };
        footer.Children.Add(regenerate);
        Grid.SetColumn(cancel, 2);
        footer.Children.Add(cancel);
        _use.Margin = new Thickness(8, 0, 0, 0);
        Grid.SetColumn(_use, 3);
        footer.Children.Add(_use);

        Children.Add(Ui.Text(Strings.GeneratorTitle, "title"));
        Children.Add(_mode);
        Children.Add(_passwordPane);
        Children.Add(_passphrasePane);
        Children.Add(new StackPanel { Spacing = 8, Children = { _result, _bar } });
        Children.Add(_error);
        Children.Add(footer);

        _mode.SelectionChanged += async (_, _) => await ModeChangedAsync();
        _length.PropertyChanged += async (_, e) => { if (e.Property == Slider.ValueProperty) await OptionsChangedAsync(); };
        _words.PropertyChanged += async (_, e) => { if (e.Property == Slider.ValueProperty) await OptionsChangedAsync(); };
        foreach (var c in new[] { _lower, _upper, _digits, _symbols }) c.IsCheckedChanged += async (_, _) => await ClassChangedAsync();
        foreach (var c in new[] { _ambiguous, _capitalize, _number }) c.IsCheckedChanged += async (_, _) => await OptionsChangedAsync();
        _separator.TextChanged += async (_, _) => await OptionsChangedAsync();
        cancel.Click += (_, _) => Dialog.Close(this);
        _use.Click += (_, _) => Dialog.Close(this, _result.Text);
        AttachedToVisualTree += async (_, _) => await RegenerateAsync();
        DetachedFromVisualTree += (_, _) => _result.Text = "";
        UpdateLabels();
        _ready = true;
    }

    /// <summary>Shows the generator over <paramref name="anchor"/>'s window; returns the chosen text, or null when cancelled.</summary>
    public static async Task<string?> AskAsync(Visual anchor, ShellController shell)
    {
        var view = new PasswordGenerator(shell);
        var chosen = await Dialog.ShowAsync(view, anchor) as string;
        return string.IsNullOrEmpty(chosen) ? null : chosen;
    }

    private GeneratorOptions Options() => new(
        Passphrase: _mode.SelectedIndex == 1,
        Length: (int)_length.Value, Lower: _lower.IsChecked == true, Upper: _upper.IsChecked == true,
        Digits: _digits.IsChecked == true, Symbols: _symbols.IsChecked == true, ExcludeAmbiguous: _ambiguous.IsChecked == true,
        Words: (int)_words.Value, Separator: _separator.Text ?? "", Capitalize: _capitalize.IsChecked == true, Number: _number.IsChecked == true);

    private void UpdateLabels()
    {
        _lengthText.Text = Strings.GenLength((int)_length.Value);
        _wordsText.Text = Strings.GenWords((int)_words.Value);
    }

    private async Task RegenerateAsync()
    {
        var gen = ++_generation;
        var options = Options();
        var r = await _shell.VaultCallAsync(a => a.Generate(options));
        if (gen != _generation) return;
        if (!r.Ok)
        {
            _error.Message = r.Error ?? "";
            _error.IsOpen = true;
            _use.IsEnabled = false;
            return;
        }
        _error.IsOpen = false;
        _use.IsEnabled = true;
        _result.Text = r.Value!.Value;
        _bar.Set(r.Value.Strength.Label, r.Value.Strength.Bits);
    }

    private async Task ModeChangedAsync()
    {
        if (!_ready) return;
        var passphrase = _mode.SelectedIndex == 1;
        _passwordPane.IsVisible = !passphrase;
        _passphrasePane.IsVisible = passphrase;
        await RegenerateAsync();
    }

    private async Task OptionsChangedAsync()
    {
        if (!_ready || _adjusting) return;
        UpdateLabels();
        await RegenerateAsync();
    }

    private async Task ClassChangedAsync()
    {
        if (!_ready || _adjusting) return;
        // The core treats "no class selected" as "all": show that instead of an empty selection.
        if (_lower.IsChecked != true && _upper.IsChecked != true && _digits.IsChecked != true && _symbols.IsChecked != true)
        {
            _adjusting = true;
            _lower.IsChecked = true;
            _adjusting = false;
        }
        await RegenerateAsync();
    }
}
