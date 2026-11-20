using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Platform.Storage;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace MoonPassword.Windows.Views;

/// <summary>Small builders shared by the SSH windows.</summary>
internal static class SshUi
{
    public static readonly FilePickerFileType KeyFiles = new(Strings.SshFileTypeKeys)
    {
        Patterns = ["id_*", "*.pem", "*.key", "*.ppk", "*.der", "*.p8", "*"],
    };

    public static string SshDir => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".ssh");

    /// <summary>A read-only, selectable, monospace block (public keys, fingerprints).</summary>
    public static SelectableTextBlock Mono(string text, bool wrap = true)
    {
        var t = new SelectableTextBlock { Text = text, TextWrapping = wrap ? TextWrapping.Wrap : TextWrapping.NoWrap, FontSize = 12 };
        t.Classes.Add("mono");
        return t;
    }

    public static Badge Badge(string text, string kind)
    {
        var b = new Badge { Content = text, VerticalAlignment = VerticalAlignment.Center };
        if (kind.Length > 0) b.Classes.Add(kind);
        return b;
    }

    public static TextBox Passphrase(string placeholder) =>
        new() { PasswordChar = '•', IsUndoEnabled = false, PlaceholderText = placeholder, MinWidth = 180 };

    /// <summary>A bordered row block like the vault's detail sections.</summary>
    public static Border Block(Control child, double padding = 12)
    {
        var b = new Border { CornerRadius = new CornerRadius(10), BorderThickness = new Thickness(1), Padding = new Thickness(padding), Child = child };
        b[!Border.BorderBrushProperty] = b.GetResourceObservable("Lumen.Brush.Border").ToBinding();
        return b;
    }

    /// <summary>
    /// Takes a control out of whatever it is in, so a re-render can place it again (rows keep their
    /// checkbox and text boxes across renders; Avalonia refuses a control that still has a parent).
    /// </summary>
    public static T Detach<T>(T c) where T : Control
    {
        switch (c.Parent)
        {
            case Panel p: p.Children.Remove(c); break;
            case ContentControl cc when ReferenceEquals(cc.Content, c): cc.Content = null; break;
            case Decorator d when ReferenceEquals(d.Child, c): d.Child = null; break;
        }
        return c;
    }

    public static async Task CopyAsync(ShellController shell, string text, string done)
    {
        var ok = await shell.CopyTextAsync(text);
        shell.ShowStatus(ok ? done : Strings.ApplyFailed, ok ? StatusKind.Success : StatusKind.Error);
    }
}

/// <summary>
/// Import one or more private keys (files or pasted text). Each key is looked at with ssh.inspect first, so
/// the user sees its type, fingerprint and whether it needs a passphrase before anything is stored.
/// </summary>
internal sealed class SshImportWindow
{
    private sealed class Item
    {
        public required SshSource Source;
        public required string Name;
        public SshInspect? Info;
        public string Error = "";
        public bool Done;
        public readonly TextBox Pass = SshUi.Passphrase(Strings.SshImportPassphrase);
        public readonly TextBox Title = new() { MinWidth = 200 };
        public readonly CheckBox Include = new() { IsChecked = true, VerticalAlignment = VerticalAlignment.Center };
        public readonly ContentControl View = new();

        public bool Ready => Info is { NeedsPassphrase: false } && Error.Length == 0 && !Done;
    }

    private readonly ShellController _shell;
    private readonly string _groupId;
    private readonly VaultWindow _window;
    private readonly List<Item> _items = new();
    private readonly StackPanel _list = new() { Spacing = 8 };
    private readonly TextBlock _empty = Ui.Text(Strings.SshImportEmpty, "caption", "tertiary");
    private readonly TextBox _paste = new()
    {
        AcceptsReturn = true, TextWrapping = TextWrapping.NoWrap, MinHeight = 80, MaxHeight = 160,
        PlaceholderText = Strings.SshImportPastePlaceholder, FontSize = 12, IsUndoEnabled = false,
    };
    private readonly CheckBox _agent = new() { Content = Strings.SshImportAgentToggle, IsChecked = true };
    private readonly InfoBar _error = new() { Severity = Severity.Danger, IsOpen = false };
    private readonly Button _import;
    private int _imported;

    private SshImportWindow(ShellController shell, string groupId)
    {
        _shell = shell;
        _groupId = groupId;
        _paste.Classes.Add("mono");
        _import = DialogKit.Btn(Strings.SshImportButton(0), 110, "primary");
        _import.Click += (_, _) => _ = Ui.Busy(_import, ImportAsync);
        var cancel = DialogKit.Btn(Strings.BtnCancel, 88);
        cancel.IsCancel = true;

        var choose = Ui.Button(AppIcons.Of(IconKind.Folder), Strings.SshImportChooseFiles, () => _ = ChooseAsync(), "soft");
        var check = Ui.TextButton(Strings.SshImportPasteAdd, () => _ = AddPastedAsync(), "soft", "sm");
        var body = new StackPanel
        {
            Spacing = 14,
            Children =
            {
                DialogKit.Text(Strings.SshImportIntro, "caption", "secondary").With(t => t.TextWrapping = TextWrapping.Wrap),
                choose,
                new FormField { Label = Strings.SshImportPasteLabel, Content = new StackPanel { Spacing = 6, Children = { _paste, check } } },
                _empty,
                _list,
                _agent,
                _error,
            },
        };
        var buttons = DialogKit.ButtonRow(cancel, _import);
        var root = new DockPanel();
        DockPanel.SetDock(buttons, Dock.Bottom);
        root.Children.Add(buttons);
        root.Children.Add(new ScrollViewer { Content = body, Padding = new Thickness(0, 0, 8, 0) });
        _window = DialogKit.Create(shell, Strings.SshImportTitle, 640, root, height: 600);
        cancel.Click += (_, _) => _window.Close();
        _window.Closed += (_, _) =>
        {
            foreach (var i in _items) i.Pass.Text = "";
            _paste.Text = "";
        };
        Refresh();
    }

    /// <summary>Returns how many keys were imported.</summary>
    public static async Task<int> ShowAsync(Window? owner, ShellController shell, string groupId = "", IReadOnlyList<string>? paths = null)
    {
        var w = new SshImportWindow(shell, groupId);
        if (paths is { Count: > 0 }) _ = w.AddFilesAsync(paths);
        await w._window.ShowModalAsync(owner);
        return w._imported;
    }

    private async Task ChooseAsync()
    {
        if (TopLevel.GetTopLevel(_window)?.StorageProvider is not { } sp) return;
        IStorageFolder? start = Directory.Exists(SshUi.SshDir) ? await sp.TryGetFolderFromPathAsync(SshUi.SshDir) : null;
        var files = await sp.OpenFilePickerAsync(new FilePickerOpenOptions
        {
            Title = Strings.SshImportChooseFiles, AllowMultiple = true, SuggestedStartLocation = start,
            FileTypeFilter = [SshUi.KeyFiles, VaultUi.AnyType],
        });
        await AddFilesAsync(files.Select(f => f.TryGetLocalPath()).OfType<string>().ToList());
    }

    private async Task AddFilesAsync(IReadOnlyList<string> paths)
    {
        foreach (var p in paths)
        {
            if (_items.Any(i => string.Equals(i.Source.Path, p, StringComparison.OrdinalIgnoreCase))) continue;
            var item = new Item { Source = SshSource.File(p), Name = Path.GetFileName(p) };
            _items.Add(item);
            await InspectAsync(item, "");
        }
    }

    private async Task AddPastedAsync()
    {
        var text = _paste.Text ?? "";
        if (text.Trim().Length == 0) return;
        var item = new Item { Source = SshSource.Pasted(text), Name = Strings.SshImportPasted };
        _paste.Text = "";
        _items.Add(item);
        await InspectAsync(item, "");
    }

    private async Task InspectAsync(Item item, string passphrase)
    {
        var r = await _shell.SshCallAsync(a => a.Inspect(item.Source, passphrase));
        if (r.Ok)
        {
            var wasNull = item.Info is null;
            item.Info = r.Value;
            item.Error = "";
            if (wasNull || item.Title.Text is null or "")
                item.Title.Text = r.Value!.Comment.Length > 0 ? r.Value.Comment : item.Source.Path.Length > 0 ? item.Name : "";
            item.Include.IsChecked = r.Value!.InVault.Count == 0;
        }
        else
        {
            item.Error = r.Error;
            item.Include.IsChecked = false;
        }
        Refresh();
    }

    private void Refresh()
    {
        _list.Children.Clear();
        foreach (var item in _items)
        {
            Render(item);
            _list.Children.Add(item.View);
        }
        _empty.IsVisible = _items.Count == 0;
        var n = _items.Count(i => i.Ready && i.Include.IsChecked == true);
        _import.Content = Strings.SshImportButton(n);
        _import.IsEnabled = n > 0;
    }

    private void Render(Item item)
    {
        SshUi.Detach(item.Include);
        SshUi.Detach(item.Pass);
        SshUi.Detach(item.Title);
        var head = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,Auto,*,Auto,Auto") };
        item.Include.IsEnabled = item.Ready;
        item.Include.IsCheckedChanged -= OnIncludeChanged;
        item.Include.IsCheckedChanged += OnIncludeChanged;
        head.Children.Add(item.Include);
        var icon = Ui.Icon(AppIcons.Terminal, 16).With(i => { i.Margin = new Thickness(4, 0, 8, 0); i.VerticalAlignment = VerticalAlignment.Center; });
        Grid.SetColumn(icon, 1);
        head.Children.Add(icon);
        var name = new StackPanel { Spacing = 2, VerticalAlignment = VerticalAlignment.Center };
        name.Children.Add(Ui.Text(item.Name, "medium").With(t => t.TextTrimming = TextTrimming.CharacterEllipsis));
        if (item.Info is { } info)
        {
            var parts = new List<string> { Strings.SshFormatName(info.Format) };
            if (info.TypeText.Length > 0) parts.Add(info.TypeText);
            if (info.Fingerprint.Length > 0) parts.Add(SshText.ShortFingerprint(info.Fingerprint));
            name.Children.Add(Ui.Text(string.Join("  ·  ", parts), "caption", "tertiary").With(t => t.TextTrimming = TextTrimming.CharacterEllipsis));
        }
        Grid.SetColumn(name, 2);
        head.Children.Add(name);
        var badge = item.Done ? SshUi.Badge(Strings.SshMigrateImportedState, "success")
            : item.Error.Length > 0 ? SshUi.Badge(Strings.SshMigrateFailedState, "danger")
            : item.Info is null ? SshUi.Badge("…", "")
            : item.Info.WrongPassphrase ? SshUi.Badge(Strings.SshImportWrongPass, "danger")
            : item.Info.NeedsPassphrase ? SshUi.Badge(Strings.SshImportNeedsPass, "warning")
            : item.Info.InVault.Count > 0 ? SshUi.Badge(Strings.SshImportInVault, "")
            : SshUi.Badge(Strings.SshImportReady, "success");
        badge.Margin = new Thickness(8, 0, 4, 0);
        Grid.SetColumn(badge, 3);
        head.Children.Add(badge);
        var remove = Ui.IconButton(AppIcons.Of(IconKind.X), Strings.BtnRemove, () => { _items.Remove(item); item.Pass.Text = ""; Refresh(); }, "sm");
        Grid.SetColumn(remove, 4);
        head.Children.Add(remove);

        var body = new StackPanel { Spacing = 8, Children = { head } };
        if (item.Error.Length > 0)
            body.Children.Add(Ui.Text(item.Error, "caption", "danger").With(t => t.TextWrapping = TextWrapping.Wrap));
        else if (item.Info is { NeedsPassphrase: true } && !item.Done)
        {
            var unlock = Ui.TextButton(Strings.SshImportUnlock, () => _ = InspectAsync(item, item.Pass.Text ?? ""), "soft", "sm");
            item.Pass.KeyDown -= OnPassKey;
            item.Pass.KeyDown += OnPassKey;
            item.Pass.Tag = item;
            body.Children.Add(new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Children = { item.Pass, unlock } });
        }
        else if (item.Info is { } i && i.InVault.Count > 0)
            body.Children.Add(Ui.Text(Strings.ErrSshDuplicate + "（" + string.Join("、", i.InVault.Select(v => v.Title)) + "）", "caption", "tertiary"));
        if (item.Ready && item.Info!.InVault.Count == 0)
            body.Children.Add(new FormField { Label = Strings.LabelTitle, Content = item.Title });
        item.View.Content = SshUi.Block(body, 10);
    }

    private void OnIncludeChanged(object? sender, Avalonia.Interactivity.RoutedEventArgs e)
    {
        var n = _items.Count(i => i.Ready && i.Include.IsChecked == true);
        _import.Content = Strings.SshImportButton(n);
        _import.IsEnabled = n > 0;
    }

    private void OnPassKey(object? sender, Avalonia.Input.KeyEventArgs e)
    {
        if (e.Key != Avalonia.Input.Key.Enter || sender is not TextBox { Tag: Item item }) return;
        e.Handled = true;
        _ = InspectAsync(item, item.Pass.Text ?? "");
    }

    private async Task ImportAsync()
    {
        _error.IsOpen = false;
        var agent = _agent.IsChecked == true;
        var failed = 0;
        foreach (var item in _items.Where(i => i.Ready && i.Include.IsChecked == true).ToList())
        {
            var title = (item.Title.Text ?? "").Trim();
            var pass = item.Pass.Text ?? "";
            var src = item.Source;
            var r = await _shell.SshCallAsync(a => a.Import(src, pass, title, _groupId, agent));
            if (r.Ok)
            {
                item.Done = true;
                item.Pass.Text = "";
                _imported++;
            }
            else
            {
                item.Error = r.Error;
                failed++;
            }
        }
        Refresh();
        if (failed == 0 && _imported > 0)
        {
            _shell.ShowStatus(Strings.SshImportedToast(_imported), StatusKind.Success);
            _window.Close();
        }
    }
}

/// <summary>Generate a new key inside the vault and show its public half.</summary>
internal sealed class SshGenerateWindow
{
    private readonly ShellController _shell;
    private readonly string _groupId;
    private readonly VaultWindow _window;
    private readonly SegmentedControl _type = new();
    private readonly ComboBox _bits = new() { MinWidth = 120 };
    private readonly TextBlock _typeHint = Ui.Text(Strings.SshTypeEd25519Hint, "caption", "tertiary");
    private readonly TextBox _title = new() { PlaceholderText = Strings.SshGenerateTitlePlaceholder };
    private readonly TextBox _comment = new();
    private readonly CheckBox _agent = new() { Content = Strings.SshImportAgentToggle, IsChecked = true };
    private readonly FormField _titleField;
    private readonly StackPanel _form;
    private readonly StackPanel _result = new() { Spacing = 10, IsVisible = false };
    private readonly InfoBar _error = new() { Severity = Severity.Danger, IsOpen = false };
    private readonly Button _go;
    private readonly Button _close;
    public string? CreatedId { get; private set; }

    private SshGenerateWindow(ShellController shell, string groupId)
    {
        _shell = shell;
        _groupId = groupId;
        foreach (var (tag, label) in new[] { ("ed25519", "Ed25519"), ("rsa", "RSA"), ("ecdsa", "ECDSA") })
            _type.Items.Add(new SegmentedItem { Tag = tag, Content = label });
        _type.SelectedIndex = 0;
        _type.SelectionChanged += (_, _) => UpdateType();
        _titleField = new FormField { Label = Strings.SshGenerateTitleLabel, IsRequired = true, Content = _title };
        _title.TextChanged += (_, _) => { _titleField.Error = null; _comment.PlaceholderText = _title.Text; };
        _form = new StackPanel
        {
            Spacing = 14,
            Children =
            {
                DialogKit.Text(Strings.SshGenerateIntro, "caption", "secondary").With(t => t.TextWrapping = TextWrapping.Wrap),
                new FormField { Label = Strings.SshGenerateType, Content = new StackPanel { Spacing = 6, Children = { new StackPanel { Orientation = Orientation.Horizontal, Spacing = 10, Children = { _type, _bits } }, _typeHint } } },
                _titleField,
                new FormField { Label = Strings.SshGenerateComment, Content = _comment },
                _agent,
                _error,
            },
        };
        _go = DialogKit.Btn(Strings.SshGenerateButton, 96, "primary");
        _go.IsDefault = true;
        _go.Click += (_, _) => _ = Ui.Busy(_go, GenerateAsync);
        var cancel = DialogKit.Btn(Strings.BtnCancel, 88);
        cancel.IsCancel = true;
        _close = DialogKit.Btn(Strings.BtnDone, 96, "primary");
        _close.IsVisible = false;
        var body = new StackPanel { Spacing = 0, Children = { _form, _result, DialogKit.ButtonRow(cancel, _go, _close) } };
        _window = DialogKit.Create(shell, Strings.SshGenerateTitle, 560, body);
        cancel.Click += (_, _) => _window.Close();
        _close.Click += (_, _) => _window.Close();
        UpdateType();
    }

    /// <summary>Returns the id of the new entry, or null.</summary>
    public static async Task<string?> ShowAsync(Window? owner, ShellController shell, string groupId = "")
    {
        var w = new SshGenerateWindow(shell, groupId);
        w._window.Opened += (_, _) => w._title.Focus();
        await w._window.ShowModalAsync(owner);
        return w.CreatedId;
    }

    private string Type => (_type.SelectedItem as SegmentedItem)?.Tag as string ?? "ed25519";

    private void UpdateType()
    {
        _bits.Items.Clear();
        switch (Type)
        {
            case "rsa":
                _bits.Items.Add("3072");
                _bits.Items.Add("4096");
                _typeHint.Text = Strings.SshTypeRsaHint;
                break;
            case "ecdsa":
                _bits.Items.Add("256");
                _bits.Items.Add("384");
                _bits.Items.Add("521");
                _typeHint.Text = Strings.SshTypeEcdsaHint;
                break;
            default:
                _typeHint.Text = Strings.SshTypeEd25519Hint;
                break;
        }
        _bits.IsVisible = _bits.Items.Count > 0;
        if (_bits.Items.Count > 0) _bits.SelectedIndex = 0;
    }

    private async Task GenerateAsync()
    {
        var title = (_title.Text ?? "").Trim();
        if (title.Length == 0) { _titleField.Error = Strings.ValTitleRequired; return; }
        var type = Type;
        var bits = _bits.SelectedItem is string s && int.TryParse(s, out var b) ? b : 0;
        var comment = (_comment.Text ?? "").Trim();
        var agent = _agent.IsChecked == true;
        var r = await _shell.SshCallAsync(a => a.Generate(type, bits, title, comment, _groupId, agent));
        if (!r.Ok) { _error.Message = r.Error; _error.IsOpen = true; return; }
        CreatedId = r.Value!.Id;
        _form.IsVisible = false;
        _go.IsVisible = false;
        _close.IsVisible = true;
        _close.IsDefault = true;
        var pub = r.Value.PublicKey;
        _result.Children.Add(new InfoBar { Severity = Severity.Success, IsOpen = true, IsClosable = false, Message = Strings.SshGenerateDone });
        _result.Children.Add(SshUi.Block(SshUi.Mono(pub)));
        _result.Children.Add(new StackPanel
        {
            Orientation = Orientation.Horizontal, Spacing = 8,
            Children =
            {
                Ui.Button(AppIcons.Of(IconKind.Copy), Strings.SshBtnCopyPublic, () => _ = SshUi.CopyAsync(_shell, pub, Strings.SshCopiedPublic), "soft"),
                Ui.Text(SshText.Algo(r.Value.Algo, r.Value.Bits) + "  ·  " + r.Value.Fingerprint, "caption", "tertiary").With(t => t.VerticalAlignment = VerticalAlignment.Center),
            },
        });
        _result.IsVisible = true;
    }
}

/// <summary>Export a private key to a file, after the master password is entered again.</summary>
internal sealed class SshExportWindow
{
    private readonly ShellController _shell;
    private readonly string _id;
    private readonly string _suggested;
    private readonly VaultWindow _window;
    private readonly ComboBox _format = new() { MinWidth = 200 };
    private readonly SecretBox _pass = new() { Placeholder = Strings.SshExportPassphrase };
    private readonly CredsBox _master = new() { Caption = Strings.SshExportMaster };
    private readonly InfoBar _error = new() { Severity = Severity.Danger, IsOpen = false };

    private SshExportWindow(ShellController shell, string id, string suggestedName, bool rsaOrEcdsa, bool keyFile)
    {
        _shell = shell;
        _id = id;
        _suggested = suggestedName;
        _format.Items.Add(new ComboBoxItem { Content = Strings.SshFormatOpenSsh, Tag = "openssh" });
        _format.Items.Add(new ComboBoxItem { Content = Strings.SshFormatPkcs8, Tag = "pkcs8" });
        if (rsaOrEcdsa) _format.Items.Add(new ComboBoxItem { Content = Strings.SshFormatPem, Tag = "pem" });
        _format.SelectedIndex = 0;
        _format.SelectionChanged += (_, _) => _pass.IsEnabled = Format != "pem";
        _master.ShowKeyFile = keyFile;
        var go = DialogKit.Btn(Strings.SshExportButton, 96, "primary");
        go.Click += (_, _) => _ = Ui.Busy(go, ExportAsync);
        _master.Submit += () => _ = Ui.Busy(go, ExportAsync);
        var cancel = DialogKit.Btn(Strings.BtnCancel, 88);
        cancel.IsCancel = true;
        var body = new StackPanel
        {
            Spacing = 14,
            Children =
            {
                new InfoBar { Severity = Severity.Warning, IsOpen = true, IsClosable = false, Message = Strings.SshExportWarning },
                new FormField { Label = Strings.SshExportFormat, Content = _format },
                new FormField { Label = Strings.SshExportPassphrase, Content = _pass },
                _master,
                _error,
                DialogKit.ButtonRow(cancel, go),
            },
        };
        _window = DialogKit.Create(shell, Strings.SshExportTitle, 520, body);
        cancel.Click += (_, _) => _window.Close();
        _window.Closed += (_, _) => { _pass.Clear(); _master.Clear(keyFileToo: true); };
    }

    private string Format => (_format.SelectedItem as ComboBoxItem)?.Tag as string ?? "openssh";

    public static async Task ShowAsync(Window? owner, ShellController shell, SshKeyInfo key, bool keyFile)
    {
        var w = new SshExportWindow(shell, key.EntryId, key.KeyAttachment.Length > 0 ? key.KeyAttachment : "id_key", key.Algo != "ssh-ed25519", keyFile);
        w._window.Opened += (_, _) => w._master.FocusPassword();
        await w._window.ShowModalAsync(owner);
    }

    private async Task ExportAsync()
    {
        _error.IsOpen = false;
        if (_master.Password.Length == 0) { _master.ShowError(Strings.ValidationMasterRequired); return; }
        var path = await Ui.PickSaveFileAsync(_window, Strings.SshExportTitle, _suggested, SshUi.SshDir, null);
        if (path is null) return;
        var format = Format;
        var pass = format == "pem" ? "" : _pass.Text;
        var creds = _master.Creds;
        var id = _id;
        var r = await _shell.SshCallAsync(a => a.Export(id, format, pass, path, creds));
        if (!r.Ok)
        {
            if (r.Code == "VAULT_WRONG_CREDENTIALS") _master.ShowError(r.Error);
            else { _error.Message = r.Error; _error.IsOpen = true; }
            return;
        }
        _shell.ShowStatus(Strings.SshExported(path), StatusKind.Success);
        _window.Close();
    }
}

/// <summary>
/// Moves the keys in ~/.ssh into the vault: scan, choose, import, write the .pub files ssh needs, turn the agent
/// on, check the agent lists the keys, and only then offer (unticked) to move key files that nothing else reads
/// to the Recycle Bin. A vault backup is made before anything is imported.
/// </summary>
internal sealed class SshMigrateWindow
{
    private sealed class Row
    {
        public required SshScanItem Item;
        public readonly CheckBox Pick = new() { VerticalAlignment = VerticalAlignment.Center };
        public readonly TextBox Pass = SshUi.Passphrase(Strings.SshImportPassphrase);
        public string Status = "";
        public string StatusKind = "";
        public string ImportedId = "";
        public string ImportedFp = "";
        public string PubState = "";
        public RecycleDecision? Recycle;
        public readonly CheckBox RecyclePick = new() { VerticalAlignment = VerticalAlignment.Center };
    }

    private readonly ShellController _shell;
    private readonly VaultWindow _window;
    private readonly StackPanel _list = new() { Spacing = 8 };
    private readonly TextBlock _summary = Ui.Text(Strings.SshMigrateScanning, "secondary");
    private readonly InfoBar _info = new() { Severity = Severity.Info, IsOpen = false, IsClosable = false };
    private readonly InfoBar _error = new() { Severity = Severity.Danger, IsOpen = false };
    private readonly StackPanel _recycle = new() { Spacing = 8, IsVisible = false };
    private readonly Button _import;
    private readonly Button _recycleButton;
    private readonly Button _close;
    private readonly List<Row> _rows = new();
    private bool _imported;

    private SshMigrateWindow(ShellController shell)
    {
        _shell = shell;
        _import = DialogKit.Btn(Strings.SshMigrateImport, 120, "primary");
        _import.IsEnabled = false;
        _import.Click += (_, _) => _ = Ui.Busy(_import, ImportAsync);
        _recycleButton = DialogKit.Btn(Strings.SshMigrateRecycle, 0, "danger-soft");
        _recycleButton.IsVisible = false;
        _recycleButton.Click += (_, _) => _ = Ui.Busy(_recycleButton, RecycleAsync);
        _close = DialogKit.Btn(Strings.BtnCancel, 88);
        _close.IsCancel = true;
        var body = new StackPanel
        {
            Spacing = 14,
            Children =
            {
                DialogKit.Text(Strings.SshMigrateIntro, "caption", "secondary").With(t => t.TextWrapping = TextWrapping.Wrap),
                _summary,
                _info,
                _list,
                _recycle,
                _error,
            },
        };
        var buttons = DialogKit.ButtonRow(_close, _recycleButton, _import);
        var root = new DockPanel();
        DockPanel.SetDock(buttons, Dock.Bottom);
        root.Children.Add(buttons);
        root.Children.Add(new ScrollViewer { Content = body, Padding = new Thickness(0, 0, 8, 0) });
        _window = DialogKit.Create(shell, Strings.SshMigrateTitle, 700, root, height: 640);
        _close.Click += (_, _) => _window.Close();
        _window.Closed += (_, _) => { foreach (var r in _rows) r.Pass.Text = ""; };
    }

    public static async Task ShowAsync(Window? owner, ShellController shell)
    {
        var w = new SshMigrateWindow(shell);
        w._window.Opened += (_, _) => _ = w.ScanAsync();
        await w._window.ShowModalAsync(owner);
    }

    private async Task ScanAsync()
    {
        var r = await _shell.SshCallAsync(a => a.Scan());
        if (!r.Ok) { _summary.Text = r.Error; return; }
        var scan = r.Value!;
        _summary.Text = Strings.SshMigrateFound(scan.Items.Count, scan.Dir);
        foreach (var item in scan.Items)
        {
            var row = new Row { Item = item };
            row.Pick.IsChecked = SshMigration.SelectByDefault(item);
            row.Pick.IsEnabled = SshMigration.CanImport(item) && !item.InVaultAlready;
            row.Pick.IsCheckedChanged += (_, _) => UpdateButtons();
            _rows.Add(row);
        }
        if (!_shell.SshState.Listening && _rows.Count > 0)
        {
            _info.Message = Strings.SshMigrateNeedAgent;
            _info.IsOpen = true;
        }
        Render();
        UpdateButtons();
    }

    private void UpdateButtons()
    {
        _import.IsEnabled = !_imported && _rows.Any(r => r.Pick.IsChecked == true);
        _import.IsVisible = !_imported;
    }

    private void Render()
    {
        _list.Children.Clear();
        foreach (var row in _rows) _list.Children.Add(RenderRow(row));
    }

    private Control RenderRow(Row row)
    {
        var it = row.Item;
        SshUi.Detach(row.Pick);
        SshUi.Detach(row.Pass);
        var head = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto") };
        head.Children.Add(row.Pick);
        var name = new StackPanel { Spacing = 2, Margin = new Thickness(6, 0, 8, 0), VerticalAlignment = VerticalAlignment.Center };
        name.Children.Add(Ui.Text(it.Name, "medium"));
        var meta = new List<string> { Strings.SshFormatName(it.Format) };
        if (it.TypeText.Length > 0) meta.Add(it.TypeText);
        if (it.Fingerprint.Length > 0) meta.Add(SshText.ShortFingerprint(it.Fingerprint));
        if (it.Encrypted) meta.Add(Strings.SshImportNeedsPass);
        name.Children.Add(Ui.Text(string.Join("  ·  ", meta), "caption", "tertiary").With(t => t.TextTrimming = TextTrimming.CharacterEllipsis));
        Grid.SetColumn(name, 1);
        head.Children.Add(name);
        var badge = row.Status.Length > 0 ? SshUi.Badge(row.Status, row.StatusKind)
            : it.InVaultAlready ? SshUi.Badge(Strings.SshImportInVault, "")
            : it.Problem.Length > 0 ? SshUi.Badge(Strings.SshMigrateSkippedState, "danger")
            : null;
        if (badge is not null)
        {
            Grid.SetColumn(badge, 2);
            head.Children.Add(badge);
        }
        var body = new StackPanel { Spacing = 6, Children = { head } };
        foreach (var note in SshMigration.Notes(it))
            body.Children.Add(Ui.Text(note, "caption", note.Contains("直接") ? "warning" : "tertiary").With(t => { t.TextWrapping = TextWrapping.Wrap; t.Margin = new Thickness(30, 0, 0, 0); }));
        if (it.Encrypted && !_imported && row.Pick.IsEnabled)
            body.Children.Add(row.Pass.With(p => { p.Margin = new Thickness(30, 0, 0, 0); p.HorizontalAlignment = HorizontalAlignment.Left; p.Width = 260; }));
        return SshUi.Block(body, 10);
    }

    private async Task ImportAsync()
    {
        _error.IsOpen = false;
        // 1. a vault backup first
        var backup = await _shell.VaultCallAsync(a => a.BackupNow());
        if (!backup.Ok) { _error.Message = backup.Error; _error.IsOpen = true; return; }
        // 2. the agent on
        if (!_shell.SshState.Listening)
        {
            if (await _shell.SetSshAgentAsync(true) is { } e) { _error.Message = e; _error.IsOpen = true; }
        }
        // 3. import, then write the .pub files ssh needs to keep finding the keys
        int ok = 0, skipped = 0;
        foreach (var row in _rows.Where(r => r.Pick.IsChecked == true))
        {
            var it = row.Item;
            var pass = row.Pass.Text ?? "";
            var title = it.Comment.Length > 0 ? it.Comment : it.Name;
            var r = await _shell.SshCallAsync(a => a.Import(SshSource.File(it.Path), pass, title, "", true, it.Name));
            if (!r.Ok)
            {
                if (r.Code == "SSH_KEY_DUPLICATE")
                {
                    row.ImportedId = r.DuplicateId;
                    (row.Status, row.StatusKind) = (Strings.SshImportInVault, "");
                }
                else
                {
                    (row.Status, row.StatusKind) = (r.Error.Length > 18 ? Strings.SshMigrateFailedState : r.Error, "danger");
                    skipped++;
                    continue;
                }
            }
            else
            {
                row.ImportedId = r.Value!.Id;
                row.ImportedFp = r.Value.Fingerprint;
                (row.Status, row.StatusKind) = (Strings.SshMigrateImportedState, "success");
                ok++;
            }
            row.Pass.Text = "";
            var id = row.ImportedId;
            var w = await _shell.SshCallAsync(a => a.WritePub(it.PubPath, id));
            row.PubState = w.Ok ? w.Value! : "";
        }
        _imported = true;
        foreach (var row in _rows) row.Pick.IsEnabled = false;
        _summary.Text = Strings.SshMigrateDone(ok, skipped);
        UpdateButtons();

        // 4. ask the agent what it serves now, then decide what may go to the Recycle Bin
        await Task.Delay(400); // the core reloads its keys 200 ms after the last save
        var test = await _shell.SshCallAsync(a => a.SelfTest());
        var listed = test.Ok ? test.Value!.Fingerprints : Array.Empty<string>();
        foreach (var row in _rows.Where(r => r.ImportedId.Length > 0))
        {
            var fp = row.ImportedFp.Length > 0 ? row.ImportedFp : row.Item.Fingerprint;
            row.Recycle = SshMigration.CanRecycle(row.Item, fp, row.PubState, listed.ToHashSet());
        }
        Render();
        RenderRecycle();
        _close.Content = Strings.BtnDone;
        await _shell.RefreshSshStatusAsync();
    }

    private void RenderRecycle()
    {
        _recycle.Children.Clear();
        var candidates = _rows.Where(r => r.Recycle is not null).ToList();
        if (candidates.Count == 0) return;
        _recycle.Children.Add(Ui.Text(Strings.SshMigrateRecycle, "subtitle"));
        _recycle.Children.Add(Ui.Text(Strings.SshMigrateRecycleHint, "caption", "tertiary"));
        foreach (var row in candidates)
        {
            var line = new StackPanel { Spacing = 2 };
            SshUi.Detach(row.RecyclePick);
            if (row.Recycle!.Allowed)
            {
                row.RecyclePick.IsChecked = false; // never ticked by default
                row.RecyclePick.Content = row.Item.Path;
                row.RecyclePick.IsCheckedChanged += (_, _) => _recycleButton.IsEnabled = _rows.Any(r => r.RecyclePick.IsChecked == true);
                line.Children.Add(row.RecyclePick);
            }
            else
            {
                line.Children.Add(Ui.Text(row.Item.Path + "  ·  " + Strings.SshMigrateKeep, "medium"));
                foreach (var b in row.Recycle.Blockers)
                    line.Children.Add(Ui.Text(b, "caption", "tertiary").With(t => { t.TextWrapping = TextWrapping.Wrap; t.Margin = new Thickness(12, 0, 0, 0); }));
            }
            _recycle.Children.Add(line);
        }
        _recycle.IsVisible = true;
        var any = candidates.Any(r => r.Recycle!.Allowed);
        _recycleButton.IsVisible = any;
        _recycleButton.IsEnabled = false;
        if (!any) _recycle.Children.Add(Ui.Text(Strings.SshMigrateNothingToRecycle, "caption", "secondary"));
    }

    private async Task RecycleAsync()
    {
        var picked = _rows.Where(r => r.Recycle?.Allowed == true && r.RecyclePick.IsChecked == true).ToList();
        if (picked.Count == 0) return;
        if (!await VaultUi.ConfirmAsync(_window, string.Join("\n", picked.Select(r => r.Item.Path)), Strings.SshMigrateRecycle, Strings.SshMigrateRecycle, warning: true)) return;
        var moved = 0;
        foreach (var row in picked)
        {
            var path = row.Item.Path;
            if (await Task.Run(() => RecycleBin.Move(path)))
            {
                moved++;
                row.RecyclePick.IsEnabled = false;
                row.RecyclePick.IsChecked = false;
            }
        }
        _recycleButton.IsEnabled = false;
        _shell.ShowStatus(Strings.SshMigrateRecycled(moved), moved == picked.Count ? StatusKind.Success : StatusKind.Warning);
    }
}
