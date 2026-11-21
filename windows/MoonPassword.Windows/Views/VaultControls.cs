using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Shapes;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Platform.Storage;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using Lumen.UI.Controls;

namespace MoonPassword.Windows.Views;

/// <summary>Small helpers shared by the vault views: questions, pickers, and closing every vault window on lock.</summary>
internal static class VaultUi
{
    public static IBrush Brush(uint argb) => new SolidColorBrush(Color.FromUInt32(argb));

    private static readonly Dictionary<Window, Action> OpenWindows = new();

    /// <summary>
    /// Registers a vault window so that locking the vault can close it (a window must not keep secrets on screen after a
    /// lock). <paramref name="forceClose"/> closes it without any "unsaved changes" prompt.
    /// </summary>
    public static void Track(Window dialog, Action? forceClose = null)
    {
        OpenWindows[dialog] = forceClose ?? dialog.Close;
        dialog.Closed += (_, _) => OpenWindows.Remove(dialog);
    }

    /// <summary>Closes every open vault window and any question open over the main window (on lock).</summary>
    public static void CloseAll()
    {
        foreach (var close in OpenWindows.Values.ToList())
        {
            try { close(); }
            catch (InvalidOperationException) { /* already closing */ }
        }
        if (_mainQuestions > 0 && MainWindow.Instance is { } main) DialogHost.FindFor(main)?.Close();
    }

    private static int _mainQuestions;

    /// <summary>Runs a question; when it is over the main window, a lock closes it.</summary>
    private static async Task<T> Asking<T>(Visual from, Func<Task<T>> ask)
    {
        var inMain = TopLevel.GetTopLevel(from) is MainWindow;
        if (inMain) _mainQuestions++;
        try { return await ask(); }
        finally { if (inMain) _mainQuestions--; }
    }

    public static Window? OwnerOf(Visual v) => TopLevel.GetTopLevel(v) as Window;

    /// <summary>The icon for an entry kind (login, API key, secret, note).</summary>
    public static string KindIcon(string? kind) => EntryKinds.Normalize(kind) switch
    {
        EntryKinds.ApiKey => AppIcons.Key,
        EntryKinds.Secret => AppIcons.Shield,
        EntryKinds.Note => AppIcons.Of(Lumen.UI.Icons.IconKind.FileText),
        EntryKinds.Ssh => AppIcons.Terminal,
        _ => AppIcons.Of(Lumen.UI.Icons.IconKind.User),
    };

    /// <summary>A yes/no question in the window of <paramref name="from"/>; "取消" is the safe answer (Esc).</summary>
    public static Task<bool> ConfirmAsync(Visual from, string text, string title = Strings.ConfirmTitle, string? yes = null, bool warning = false) =>
        Asking(from, () => Dialog.ConfirmAsync(title, text, yes ?? Strings.BtnOk, Strings.BtnCancel, isDanger: warning, anchor: from));

    public static Task InfoAsync(Visual from, string text, string title = Strings.AppName) =>
        Asking(from, async () => { await Dialog.AlertAsync(title, text, Strings.BtnOk, from); return true; });

    public static Task ErrorAsync(Visual from, string text, string title = Strings.ErrorTitle) =>
        Asking(from, async () => { await Dialog.AlertAsync(title, text, Strings.BtnOk, from); return true; });

    public static readonly FilePickerFileType KdbxType = new(Strings.FileTypeKdbx) { Patterns = ["*.kdbx"] };
    public static readonly FilePickerFileType CsvType = new(Strings.FileTypeCsv) { Patterns = ["*.csv"] };
    public static readonly FilePickerFileType BitwardenType = new(Strings.ImportBitwarden) { Patterns = ["*.json"] };
    public static readonly FilePickerFileType AnyType = new(Strings.FileTypeAll) { Patterns = ["*.*"] };

    /// <summary>One local file the user picks; null when cancelled.</summary>
    public static async Task<string?> PickOpenFileAsync(Visual from, string title, FilePickerFileType? type = null)
    {
        if (TopLevel.GetTopLevel(from)?.StorageProvider is not { } sp) return null;
        var options = new FilePickerOpenOptions { Title = title, AllowMultiple = false };
        if (type is not null) options.FileTypeFilter = [type, AnyType];
        var files = await sp.OpenFilePickerAsync(options);
        return files.Select(f => f.TryGetLocalPath()).OfType<string>().FirstOrDefault();
    }

    /// <summary>A save dialog. The core never overwrites, so an existing name is replaced by a free one (the caller is told).</summary>
    public static async Task<string?> PickSaveFileAsync(Visual from, string title, string fileName, FilePickerFileType? type = null)
    {
        if (TopLevel.GetTopLevel(from)?.StorageProvider is not { } sp) return null;
        var options = new FilePickerSaveOptions { Title = title, SuggestedFileName = fileName, ShowOverwritePrompt = false };
        if (type is not null)
        {
            options.FileTypeChoices = [type];
            options.DefaultExtension = type.Patterns?.FirstOrDefault()?.TrimStart('*', '.');
        }
        var file = await sp.SaveFilePickerAsync(options);
        var path = file?.TryGetLocalPath();
        return path is null ? null : FreeName.Next(path, File.Exists);
    }
}

/// <summary>Builders for the code-built vault windows.</summary>
internal static class DialogKit
{
    /// <summary>A wrapped text with Lumen text classes (e.g. "caption", "secondary", "danger").</summary>
    public static TextBlock Text(string text, params string[] classes) => Ui.Text(text, classes);

    public static Button Btn(string text, double minWidth = 0, params string[] classes)
    {
        var b = new Button { Content = text, MinWidth = minWidth, VerticalAlignment = VerticalAlignment.Center };
        foreach (var c in classes) b.Classes.Add(c);
        return b;
    }

    /// <summary>Buttons on the right, Windows order: the default action is the rightmost.</summary>
    public static StackPanel ButtonRow(params Control[] buttons)
    {
        var p = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Margin = new Thickness(0, 18, 0, 0) };
        foreach (var b in buttons) p.Children.Add(b);
        return p;
    }

    /// <summary>
    /// A tracked, capture-protected vault window. <paramref name="body"/> is its content; with <paramref name="height"/>
    /// the window is resizable, otherwise it sizes to its content.
    /// </summary>
    public static VaultWindow Create(ShellController shell, string title, double width, Control body, double height = 0, bool scroll = false)
    {
        var w = new VaultWindow(shell, title, width, height);
        w.SetContent(scroll ? new ScrollViewer { Content = body } : body);
        return w;
    }

    /// <summary>Input in a vault window counts as activity (the core's idle lock measures real inactivity).</summary>
    public static void WireActivity(Control root, ShellController shell)
    {
        root.AddHandler(InputElement.KeyDownEvent, (_, _) => shell.NoteVaultActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        root.AddHandler(InputElement.PointerPressedEvent, (_, _) => shell.NoteVaultActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        root.AddHandler(InputElement.PointerMovedEvent, (_, _) => shell.NoteVaultActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
    }
}

/// <summary>A vault window: Lumen-styled, excluded from captures, closed when the vault locks, Esc closes it.</summary>
internal sealed class VaultWindow : SmallWindow
{
    private readonly ShellController _shell;

    public VaultWindow(ShellController shell, string title, double width, double height) : base(width, resizable: height > 0)
    {
        _shell = shell;
        Title = title;
        ShowInTaskbar = false;
        if (height > 0) Height = height;
        shell.ProtectDialog(this);
        VaultUi.Track(this);
    }

    public void SetContent(Control body)
    {
        SetBody(body, new Thickness(22, 20));
        DialogKit.WireActivity(this, _shell);
    }

    /// <summary>Modal over a visible owner, otherwise a plain window; completes when it closes.</summary>
    public async Task ShowModalAsync(Window? owner)
    {
        if (owner is { IsVisible: true })
        {
            WindowStartupLocation = WindowStartupLocation.CenterOwner;
            await ShowDialog(owner);
            return;
        }
        var done = new TaskCompletionSource();
        Closed += (_, _) => done.TrySetResult();
        WindowStartupLocation = WindowStartupLocation.CenterScreen;
        Show();
        await done.Task;
    }
}

/// <summary>Five coloured segments and a word: how strong a password is (vault.strength labels).</summary>
internal sealed class StrengthBar : Border
{
    private readonly Border[] _segments = new Border[5];
    private readonly TextBlock _text = new() { Margin = new Thickness(8, 0, 0, 0), VerticalAlignment = VerticalAlignment.Center, FontSize = 12 };

    /// <param name="segmentWidth">Width of each of the five segments (narrow panes use a smaller one).</param>
    public StrengthBar(double segmentWidth = 34)
    {
        var bar = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 3, VerticalAlignment = VerticalAlignment.Center };
        for (var i = 0; i < _segments.Length; i++)
        {
            _segments[i] = new Border { Width = segmentWidth, Height = 6, CornerRadius = new CornerRadius(3), VerticalAlignment = VerticalAlignment.Center };
            bar.Children.Add(_segments[i]);
        }
        // The word wraps under the segments when the pane is narrow.
        var row = new WrapPanel { Orientation = Orientation.Horizontal };
        row.Children.Add(bar);
        row.Children.Add(_text);
        Child = row;
        HorizontalAlignment = HorizontalAlignment.Left;
        Clear();
    }

    private static void Track(Border segment) =>
        segment[!BackgroundProperty] = segment.GetResourceObservable("Lumen.Brush.Track").ToBinding();

    public void Set(string? label, double bits = 0)
    {
        var filled = StrengthDisplay.Segments(label);
        var color = VaultUi.Brush(StrengthDisplay.Color(label));
        for (var i = 0; i < _segments.Length; i++)
        {
            if (i < filled) _segments[i].Background = color;
            else Track(_segments[i]);
        }
        var bitsText = StrengthDisplay.Bits(bits);
        _text.Text = StrengthDisplay.Text(label) + (bitsText.Length > 0 ? "  ·  " + bitsText : "");
        _text.Foreground = color;
    }

    public void Clear()
    {
        foreach (var s in _segments) Track(s);
        _text.Text = "";
    }
}

/// <summary>A ring that empties as the TOTP code runs out, with the seconds in the middle.</summary>
internal sealed class TotpRing : Grid
{
    private const double Size = 34;
    private readonly Arc _arc = new() { Width = Size, Height = Size, StrokeThickness = 3.5, StartAngle = -90, StrokeLineCap = PenLineCap.Round };
    private readonly TextBlock _label = new() { HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center, FontSize = 11, FontWeight = FontWeight.SemiBold };

    public TotpRing()
    {
        Width = Size;
        Height = Size;
        var track = new Ellipse { Width = Size, Height = Size, StrokeThickness = 3.5 };
        track[!Shape.StrokeProperty] = track.GetResourceObservable("Lumen.Brush.Track").ToBinding();
        Children.Add(track);
        Children.Add(_arc);
        Children.Add(_label);
    }

    /// <summary><paramref name="fraction"/> 1 = full ring.</summary>
    public void Set(double fraction, int secondsLeft)
    {
        var color = VaultUi.Brush(TotpCountdown.ColorArgb(secondsLeft));
        _arc.Stroke = color;
        _arc.SweepAngle = 360 * Math.Clamp(fraction, 0, 1);
        _label.Text = secondsLeft.ToString(System.Globalization.CultureInfo.InvariantCulture);
        _label.Foreground = color;
    }
}

/// <summary>
/// A text box for secrets: masked with an eye button to show / hide, or, with <see cref="Masked"/> off, a plain text box.
/// Undo is off so the box keeps no copies of earlier values; clear it with <see cref="Clear"/>.
/// </summary>
internal sealed class SecretBox : UserControl
{
    private readonly TextBox _box = new() { PasswordChar = '•', IsUndoEnabled = false, VerticalContentAlignment = VerticalAlignment.Center };
    private readonly Button _toggle;
    private readonly Icon _eye = new() { Size = 14 };
    private bool _masked = true;
    private bool _shown;

    public event Action? TextChanged;

    public SecretBox()
    {
        _toggle = new Button { Content = _eye, Width = 28, Height = 28, MinHeight = 0, Padding = new Thickness(0), Margin = new Thickness(0, 0, 4, 0), Focusable = false };
        _toggle.Classes.Add("ghost");
        _toggle.Classes.Add("icon");
        _toggle.Classes.Add("sm");
        _box.InnerRightContent = _toggle;
        Content = _box;
        _toggle.Click += (_, _) => { _shown = !_shown; Apply(); };
        _box.TextChanged += (_, _) => TextChanged?.Invoke();
        Apply();
    }

    public string Text
    {
        get => _box.Text ?? "";
        set => _box.Text = value;
    }

    /// <summary>False: an ordinary visible text box (for non-protected custom fields).</summary>
    public bool Masked
    {
        get => _masked;
        set { _masked = value; _shown = false; Apply(); }
    }

    public string? Placeholder
    {
        get => _box.PlaceholderText;
        set => _box.PlaceholderText = value;
    }

    public void FocusBox() => _box.Focus();

    public void Clear()
    {
        _shown = false;
        Text = "";
        Apply();
    }

    private void Apply()
    {
        var plain = !_masked || _shown;
        _box.PasswordChar = _masked ? '•' : default;
        _box.RevealPassword = plain;
        _box.Classes.Set("mono", _masked);
        _toggle.IsVisible = _masked;
        _eye.NameKey = _shown ? AppIcons.EyeOff : AppIcons.Of(Lumen.UI.Icons.IconKind.Eye);
        ToolTip.SetTip(_toggle, _shown ? Strings.BtnHidePassword : Strings.BtnShowPassword);
    }
}

/// <summary>
/// Master password (+ optional key file) input used by every vault screen that asks for credentials. The password is
/// cleared with <see cref="Clear"/> right after each use.
/// </summary>
internal sealed class CredsBox : StackPanel
{
    private readonly FormField _field = new();
    private readonly TextBox _password = new() { PasswordChar = '•', IsUndoEnabled = false };
    private readonly TextBlock _keyName = Ui.Text("", "caption", "tertiary").With(t => { t.VerticalAlignment = VerticalAlignment.Center; t.TextWrapping = TextWrapping.NoWrap; t.TextTrimming = TextTrimming.CharacterEllipsis; t.MaxWidth = 240; });
    private readonly Button _pick;
    private readonly Button _drop;
    private readonly StackPanel _keyRow = new() { Orientation = Orientation.Horizontal, Spacing = 8, Margin = new Thickness(0, 8, 0, 0) };
    private byte[]? _keyFile;

    /// <summary>Enter pressed in the password box.</summary>
    public event Action? Submit;

    /// <summary>The password changed (used for the live strength bar).</summary>
    public event Action? PasswordChanged;

    public CredsBox()
    {
        _field.Content = _password;
        Children.Add(_field);
        _pick = Ui.Button(AppIcons.Key, Strings.BtnPickKeyFile, () => _ = PickKeyFileAsync(), "ghost", "sm");
        _drop = Ui.TextButton(Strings.BtnNoKeyFile, () => SetKeyFile(null, null), "ghost", "sm");
        _drop.IsVisible = false;
        _keyRow.Children.Add(_pick);
        _keyRow.Children.Add(_drop);
        _keyRow.Children.Add(_keyName);
        Children.Add(_keyRow);
        Caption = Strings.LabelMasterPassword;
        _keyName.Text = Strings.KeyFileNone;
        _password.AddHandler(KeyDownEvent, (_, e) =>
        {
            if (e.Key == Key.Enter) { e.Handled = true; Submit?.Invoke(); }
        }, RoutingStrategies.Tunnel);
        _password.TextChanged += (_, _) => { _field.Error = null; PasswordChanged?.Invoke(); };
    }

    public string Caption
    {
        get => _field.Label ?? "";
        set => _field.Label = value;
    }

    public bool ShowKeyFile
    {
        get => _keyRow.IsVisible;
        set => _keyRow.IsVisible = value;
    }

    public string Password => _password.Text ?? "";
    public byte[]? KeyFile => _keyFile;
    public VaultCreds Creds => new(Password, _keyFile);

    public void FocusPassword() => _password.Focus();

    /// <summary>Forget the password (and, with <paramref name="keyFileToo"/>, the key file bytes).</summary>
    public void Clear(bool keyFileToo = false)
    {
        _password.Text = "";
        if (keyFileToo) SetKeyFile(null, null);
    }

    public void ShowError(string? message) => _field.Error = string.IsNullOrEmpty(message) ? null : message;

    private async Task PickKeyFileAsync()
    {
        var path = await VaultUi.PickOpenFileAsync(this, Strings.LabelKeyFile);
        if (path is null) return;
        var (bytes, error) = KeyFileRules.Load(path);
        if (bytes is null) { ShowError(error); return; }
        ShowError(null);
        SetKeyFile(bytes, System.IO.Path.GetFileName(path));
    }

    private void SetKeyFile(byte[]? bytes, string? name)
    {
        _keyFile = bytes;
        _keyName.Text = bytes is null ? Strings.KeyFileNone : Strings.KeyFileChosen(name ?? "");
        _drop.IsVisible = bytes is not null;
    }
}
