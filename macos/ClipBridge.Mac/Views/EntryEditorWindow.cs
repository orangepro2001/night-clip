using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Templates;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

/// <summary>
/// The entry editor (port of the Windows EntryEditorWindow): kind templates, password strength bar, generator, custom
/// fields and the "unsaved changes" prompt. The form is only a view of an <see cref="EntryDraft"/>; validation and the
/// JSON sent to the core are in Logic/EntryDraft.cs.
/// </summary>
internal sealed class EntryEditorWindow : Window
{
    private sealed class FieldRow
    {
        public required TextBox Name { get; init; }
        public required SecretBox Value { get; init; }
        public required CheckBox Protected { get; init; }
        public required Control Root { get; init; }

        public DraftField ToDraft() => new() { Name = Name.Text ?? "", Value = Value.Text, Protected = Protected.IsChecked == true };
    }

    private readonly MacController _shell;
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

    private readonly StackPanel _form = new() { Margin = new Thickness(18, 14, 18, 8) };
    private readonly ComboBox _kindBox = new() { ItemsSource = EntryKinds.All, HorizontalAlignment = HorizontalAlignment.Stretch };
    private readonly ComboBox _groupBox = new() { HorizontalAlignment = HorizontalAlignment.Stretch };
    private readonly TextBox _titleBox = new() { MaxLength = 2000 };
    private readonly TextBox _userBox = new();
    private readonly SecretBox _pwdBox = new();
    private readonly StrengthBar _pwdStrength = new() { Margin = new Thickness(0, 6, 0, 0) };
    private readonly TextBox _urlBox = new();
    private readonly SecretBox _totpBox = new();
    private readonly CalendarDatePicker _expiryPicker = new() { Width = 200, HorizontalAlignment = HorizontalAlignment.Left };
    private readonly TextBox _tagsBox = new();
    private readonly TextBox _notesBox = new() { AcceptsReturn = true, TextWrapping = TextWrapping.Wrap, MinHeight = 90, MaxHeight = 260, IsUndoEnabled = false };
    private readonly StackPanel _fieldsPanel = new() { Spacing = 4 };
    private readonly WrapPanel _suggestionPanel = new() { ItemSpacing = 4, LineSpacing = 4, Margin = new Thickness(0, 4, 0, 0) };
    private readonly TextBlock _pwdLabel = Label(Strings.LabelPassword);
    private readonly TextBlock _urlLabel = Label(Strings.LabelUrl);
    private readonly TextBlock _errorText = DialogKit.Text("", VaultUi.ErrorBrush, top: 14);
    private readonly TextBlock _warnText = DialogKit.Text("", VaultUi.WarnBrush, top: 4);
    private readonly TextBlock _loadingText = new() { Text = Strings.EditorLoading, Foreground = VaultUi.MutedBrush, VerticalAlignment = VerticalAlignment.Center, IsVisible = false };
    private readonly Button _saveButton = new() { Content = Strings.BtnSave, MinWidth = 90, IsDefault = true };
    private readonly StackPanel _userSection, _pwdSection, _urlSection, _totpSection, _expirySection, _tagsSection, _notesSection, _fieldsSection;

    private static TextBlock Label(string text) => new() { Text = text, Margin = new Thickness(0, 12, 0, 4) };

    private static StackPanel Section(params Control[] children)
    {
        var p = new StackPanel();
        foreach (var c in children) p.Children.Add(c);
        return p;
    }

    private EntryEditorWindow(MacController shell, IReadOnlyList<GroupInfo> groups, string editId)
    {
        _shell = shell;
        _editId = editId;
        _groupChoices = GroupTree.Choices(groups);
        Title = Strings.EditorNewTitle;
        Width = 560;
        Height = 720;
        MinWidth = 480;
        MinHeight = 460;
        ShowInTaskbar = false;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;

        _kindBox.ItemTemplate = new FuncDataTemplate<string>((k, _) => new TextBlock { Text = KindTemplate.For(k).Glyph + "  " + KindTemplate.For(k).Label });
        _groupBox.ItemsSource = _groupChoices;
        _groupBox.ItemTemplate = new FuncDataTemplate<GroupNode>((n, _) => new TextBlock { Text = n.Name, Margin = new Thickness(n.Depth * 12, 0, 0, 0) });

        var top = new Grid { ColumnDefinitions = new ColumnDefinitions("170,*") };
        top.Children.Add(Section(new TextBlock { Text = Strings.LabelKind, Margin = new Thickness(0, 0, 0, 4) }, _kindBox).With(s => s.Margin = new Thickness(0, 0, 12, 0)));
        top.Children.Add(Section(new TextBlock { Text = Strings.LabelGroup, Margin = new Thickness(0, 0, 0, 4) }, _groupBox).With(s => Grid.SetColumn(s, 1)));
        _form.Children.Add(top);
        _form.Children.Add(Label(Strings.LabelTitle));
        _form.Children.Add(_titleBox);

        _userSection = Section(Label(Strings.LabelUsername), _userBox);
        var generate = new Button { Content = Strings.BtnGenerate, Margin = new Thickness(6, 0, 0, 0), VerticalAlignment = VerticalAlignment.Center };
        var pwdLine = new DockPanel();
        DockPanel.SetDock(generate, Dock.Right);
        pwdLine.Children.Add(generate);
        pwdLine.Children.Add(_pwdBox);
        _pwdSection = Section(_pwdLabel, pwdLine, _pwdStrength);
        _urlSection = Section(_urlLabel, _urlBox);
        _totpSection = Section(Label(Strings.LabelTotp), _totpBox);
        _expirySection = Section(Label(Strings.LabelExpiry), _expiryPicker);
        _tagsSection = Section(Label(Strings.LabelTags), _tagsBox);
        _notesSection = Section(Label(Strings.LabelNotes), _notesBox);
        var addField = new Button { Content = Strings.BtnAddField, HorizontalAlignment = HorizontalAlignment.Left, Margin = new Thickness(0, 6, 0, 0) };
        _fieldsSection = Section(
            new TextBlock { Text = Strings.LabelCustomFields, Margin = new Thickness(0, 14, 0, 4), FontWeight = FontWeight.SemiBold },
            _fieldsPanel, _suggestionPanel, addField);
        foreach (var s in new Control[] { _userSection, _pwdSection, _urlSection, _totpSection, _expirySection, _tagsSection, _notesSection, _fieldsSection, _errorText, _warnText })
            _form.Children.Add(s);

        var cancel = new Button { Content = Strings.BtnCancel, MinWidth = 90 };
        var buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right };
        buttons.Children.Add(cancel);
        buttons.Children.Add(_saveButton);
        var bottom = new DockPanel();
        DockPanel.SetDock(buttons, Dock.Right);
        bottom.Children.Add(buttons);
        bottom.Children.Add(_loadingText);
        var grid = new Grid { RowDefinitions = new RowDefinitions("*,Auto") };
        grid.Children.Add(new ScrollViewer { Content = _form });
        grid.Children.Add(new Border
        {
            BorderBrush = VaultUi.Brush(0x40808080), BorderThickness = new Thickness(0, 1, 0, 0), Padding = new Thickness(18, 10), Child = bottom,
        }.With(b => Grid.SetRow(b, 1)));
        Content = grid;

        shell.ProtectDialog(this);
        VaultUi.Track(this, () => { _saved = true; Close(); });
        DialogKit.WireActivity(this, shell, closeOnEscape: true); // typing a long note is activity: the idle lock must not strike mid-edit

        _kindBox.SelectionChanged += (_, _) => { if (_ready) UpdateSections(); };
        _pwdBox.TextChanged += () => { _strengthTimer.Stop(); _strengthTimer.Start(); };
        _strengthTimer.Tick += async (_, _) => { _strengthTimer.Stop(); await UpdateStrengthAsync(); };
        generate.Click += async (_, _) =>
        {
            if (await PasswordGeneratorWindow.AskAsync(this, _shell) is { } generated) _pwdBox.Text = generated;
        };
        addField.Click += (_, _) => AddFieldRow(new DraftField());
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
    public static async Task<string?> ShowAsync(Window? owner, MacController shell, IReadOnlyList<GroupInfo> groups,
        string editId, string defaultGroupId, string defaultKind)
    {
        var w = new EntryEditorWindow(shell, groups, editId);
        if (editId.Length == 0)
        {
            w.Fill(new EntryDraft { Kind = EntryKinds.Normalize(defaultKind), GroupId = defaultGroupId });
        }
        else
        {
            w.Title = Strings.EditorEditTitle;
            w.SetLoading(true);
            w.Opened += async (_, _) => await w.LoadAsync();
        }
        await VaultUi.ShowModalAsync(w, owner);
        return w._savedId;
    }

    // ---- loading / filling ----------------------------------------------------------------------

    private void SetLoading(bool loading)
    {
        _loading = loading;
        _form.IsEnabled = !loading;
        _saveButton.IsEnabled = !loading;
        _loadingText.IsVisible = loading;
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
    }

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
        _kindBox.SelectedItem = EntryKinds.All.FirstOrDefault(k => k == d.Kind) ?? EntryKinds.Login;
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
            Kind = _kindBox.SelectedItem as string ?? EntryKinds.Login,
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
        _pwdLabel.Text = t.PasswordLabel;
        _urlLabel.Text = t.UrlLabel;

        _suggestionPanel.Children.Clear();
        if (t.FieldSuggestions.Count > 0)
        {
            _suggestionPanel.Children.Add(new TextBlock { Text = Strings.FieldSuggestionHint, VerticalAlignment = VerticalAlignment.Center, Foreground = VaultUi.MutedBrush });
            foreach (var name in t.FieldSuggestions)
            {
                var n = name;
                var b = new Button { Content = n };
                b.Click += (_, _) =>
                {
                    if (!_rows.Any(r => string.Equals((r.Name.Text ?? "").Trim(), n, StringComparison.OrdinalIgnoreCase)))
                        AddFieldRow(new DraftField { Name = n });
                };
                _suggestionPanel.Children.Add(b);
            }
        }
    }

    // ---- custom fields -----------------------------------------------------------------------------------

    private void AddFieldRow(DraftField f)
    {
        var name = new TextBox { Text = f.Name, Width = 150, Margin = new Thickness(0, 0, 6, 0), PlaceholderText = Strings.FieldName };
        var value = new SecretBox { Masked = f.Protected, Text = f.Value };
        var prot = new CheckBox { Content = Strings.FieldProtected, IsChecked = f.Protected, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(8, 0, 0, 0) };
        prot.IsCheckedChanged += (_, _) => value.Masked = prot.IsChecked == true;
        var remove = new Button { Content = Strings.BtnRemove, Margin = new Thickness(6, 0, 0, 0), VerticalAlignment = VerticalAlignment.Center };

        var line = new DockPanel();
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
            _fieldsPanel.Children.Remove(line);
        };
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

    // ---- save / cancel -------------------------------------------------------------------------------------

    private async Task SaveAsync()
    {
        if (_loading) return;
        var draft = Collect();
        var issues = EntryValidator.Validate(draft);
        _errorText.Text = string.Join("\n", issues.Where(i => i.IsError).Select(i => i.Message));
        _warnText.Text = string.Join("\n", issues.Where(i => !i.IsError).Select(i => i.Message));
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
            _errorText.Text = r.Error ?? Strings.ErrVaultUnexpected;
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
            if (!await VaultUi.ConfirmAsync(this, Strings.DiscardChangesText, Strings.DiscardChangesTitle, warning: true)) return;
            _discardConfirmed = true;
            Close();
        });
    }
}

/// <summary>The "生成…" popup: options in, one generated secret out (vault.generate). The value is only kept in the result box.</summary>
internal sealed class PasswordGeneratorWindow : Window
{
    private readonly MacController _shell;
    private readonly RadioButton _modePassword = new() { Content = Strings.GenModePassword, IsChecked = true, GroupName = "gen-mode" };
    private readonly RadioButton _modePassphrase = new() { Content = Strings.GenModePassphrase, GroupName = "gen-mode" };
    private readonly Slider _length = new() { Minimum = GeneratorOptions.MinLength, Maximum = GeneratorOptions.MaxLength, Value = 20, TickFrequency = 1, IsSnapToTickEnabled = true, SmallChange = 1, LargeChange = 4 };
    private readonly Slider _words = new() { Minimum = GeneratorOptions.MinWords, Maximum = GeneratorOptions.MaxWords, Value = 6, TickFrequency = 1, IsSnapToTickEnabled = true, SmallChange = 1, LargeChange = 2 };
    private readonly TextBlock _lengthText = new();
    private readonly TextBlock _wordsText = new();
    private readonly CheckBox _lower = new() { Content = Strings.GenLower, IsChecked = true };
    private readonly CheckBox _upper = new() { Content = Strings.GenUpper, IsChecked = true };
    private readonly CheckBox _digits = new() { Content = Strings.GenDigits, IsChecked = true };
    private readonly CheckBox _symbols = new() { Content = Strings.GenSymbols, IsChecked = true };
    private readonly CheckBox _ambiguous = new() { Content = Strings.GenAmbiguous };
    private readonly TextBox _separator = new() { Text = "-", Width = 50, MaxLength = 8 };
    private readonly CheckBox _capitalize = new() { Content = Strings.GenCapitalize, IsChecked = true };
    private readonly CheckBox _number = new() { Content = Strings.GenAddNumber };
    private readonly TextBox _result = new() { IsReadOnly = true, FontFamily = VaultUi.Mono, FontSize = 16, TextWrapping = TextWrapping.Wrap, MinHeight = 42, IsUndoEnabled = false };
    private readonly StrengthBar _bar = new() { Margin = new Thickness(0, 8, 0, 0) };
    private readonly TextBlock _error = DialogKit.Text("", VaultUi.ErrorBrush, top: 6);
    private readonly Button _use = new() { Content = Strings.BtnUsePassword, MinWidth = 80, IsDefault = true };
    private readonly StackPanel _passwordPane;
    private readonly StackPanel _passphrasePane;
    private bool _ready;
    private bool _adjusting;
    private int _generation;
    private string? _chosen;

    private PasswordGeneratorWindow(MacController shell)
    {
        _shell = shell;
        Title = Strings.GeneratorTitle;
        Width = 460;
        SizeToContent = SizeToContent.Height;
        CanResize = false;
        ShowInTaskbar = false;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;

        var classes = new WrapPanel { ItemSpacing = 16, LineSpacing = 4, Margin = new Thickness(0, 6, 0, 0) };
        foreach (var c in new[] { _lower, _upper, _digits, _symbols }) classes.Children.Add(c);
        _passwordPane = Ui.Column(4, _lengthText, _length, classes, _ambiguous).With(p => p.Margin = new Thickness(0, 12, 0, 0));
        _passphrasePane = Ui.Column(4, _wordsText, _words,
            Ui.Row(new TextBlock { Text = Strings.GenSeparator, VerticalAlignment = VerticalAlignment.Center }, _separator),
            _capitalize, _number).With(p => { p.Margin = new Thickness(0, 12, 0, 0); p.IsVisible = false; });
        var regenerate = new Button { Content = Strings.BtnRegenerate };
        var cancel = new Button { Content = Strings.BtnCancel, MinWidth = 80, IsCancel = true };
        Content = new StackPanel
        {
            Margin = new Thickness(18),
            Children =
            {
                Ui.Row(_modePassword, _modePassphrase).With(r => r.Spacing = 18),
                _passwordPane, _passphrasePane,
                _result.With(r => r.Margin = new Thickness(0, 14, 0, 0)),
                _bar, _error,
                DialogKit.ButtonRow(regenerate, cancel, _use),
            },
        };

        shell.ProtectDialog(this);
        VaultUi.Track(this);
        DialogKit.WireActivity(this, shell, closeOnEscape: true);

        _modePassword.IsCheckedChanged += async (_, _) => await ModeChangedAsync();
        _length.PropertyChanged += async (_, e) => { if (e.Property == Slider.ValueProperty) await OptionsChangedAsync(); };
        _words.PropertyChanged += async (_, e) => { if (e.Property == Slider.ValueProperty) await OptionsChangedAsync(); };
        foreach (var c in new[] { _lower, _upper, _digits, _symbols }) c.IsCheckedChanged += async (_, _) => await ClassChangedAsync();
        foreach (var c in new[] { _ambiguous, _capitalize, _number }) c.IsCheckedChanged += async (_, _) => await OptionsChangedAsync();
        _separator.TextChanged += async (_, _) => await OptionsChangedAsync();
        regenerate.Click += async (_, _) => await RegenerateAsync();
        cancel.Click += (_, _) => Close();
        _use.Click += (_, _) =>
        {
            _chosen = _result.Text;
            Close();
        };
        Opened += async (_, _) => await RegenerateAsync();
        Closed += (_, _) => _result.Text = "";
        UpdateLabels();
        _ready = true;
    }

    /// <summary>Shows the popup; returns the chosen text, or null when cancelled.</summary>
    public static async Task<string?> AskAsync(Window? owner, MacController shell)
    {
        var w = new PasswordGeneratorWindow(shell);
        await VaultUi.ShowModalAsync(w, owner);
        return string.IsNullOrEmpty(w._chosen) ? null : w._chosen;
    }

    private GeneratorOptions Options() => new(
        Passphrase: _modePassphrase.IsChecked == true,
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
            _error.Text = r.Error ?? "";
            _use.IsEnabled = false;
            return;
        }
        _error.Text = "";
        _use.IsEnabled = true;
        _result.Text = r.Value!.Value;
        _bar.Set(r.Value.Strength.Label, r.Value.Strength.Bits);
    }

    private async Task ModeChangedAsync()
    {
        if (!_ready) return;
        var passphrase = _modePassphrase.IsChecked == true;
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
