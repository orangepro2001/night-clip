using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Shapes;
using Avalonia.Input;
using Avalonia.Interactivity;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Platform.Storage;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

/// <summary>Small helpers shared by the vault views (the Mac counterpart of the Windows VaultUi + DialogKit).</summary>
internal static class VaultUi
{
    public static IBrush Brush(uint argb) => new SolidColorBrush(Color.FromUInt32(argb));

    public static IBrush ErrorBrush => Brush(0xFFC62828);
    public static IBrush OkBrush => Brush(0xFF1E8E3E);
    public static IBrush MutedBrush => Brush(0xFF8A8A8A);
    public static IBrush WarnBrush => Brush(0xFFB36B00);
    public static IBrush LinkBrush => Brush(0xFF1A73E8);
    public static IBrush NoteBrush => Brush(0xFF1A5FB4);
    public static readonly FontFamily Mono = new("Menlo");

    private static readonly Dictionary<Window, Action> OpenDialogs = new();

    /// <summary>
    /// Registers a vault window so that locking the vault can close it (a window must not keep secrets on screen after a
    /// lock). <paramref name="forceClose"/> closes it without any "unsaved changes" prompt.
    /// </summary>
    public static void Track(Window dialog, Action? forceClose = null)
    {
        OpenDialogs[dialog] = forceClose ?? dialog.Close;
        dialog.Closed += (_, _) => OpenDialogs.Remove(dialog);
    }

    /// <summary>Closes every open vault window (on lock), message boxes included.</summary>
    public static void CloseAll()
    {
        foreach (var close in OpenDialogs.Values.ToList())
        {
            try { close(); }
            catch (InvalidOperationException) { /* already closing */ }
        }
    }

    public static Window? OwnerOf(Visual v) => TopLevel.GetTopLevel(v) as Window;

    /// <summary>Shows <paramref name="dialog"/> modally over a visible owner, else as a plain window; completes when it closes.</summary>
    public static async Task ShowModalAsync(Window dialog, Window? owner)
    {
        if (owner is { IsVisible: true })
        {
            await dialog.ShowDialog(owner);
            return;
        }
        var done = new TaskCompletionSource();
        dialog.Closed += (_, _) => done.TrySetResult();
        dialog.WindowStartupLocation = WindowStartupLocation.CenterScreen;
        dialog.Show();
        await done.Task;
    }

    private static async Task<bool> AskAsync(Window? owner, string title, string text, string? yes, string no, IBrush? foreground)
    {
        var result = false;
        var w = new Window
        {
            Title = title, Width = 420, SizeToContent = SizeToContent.Height, CanResize = false, ShowInTaskbar = false,
            WindowStartupLocation = WindowStartupLocation.CenterOwner,
        };
        var buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right };
        var close = new Button { Content = no, MinWidth = 80, IsCancel = true, IsDefault = yes is null };
        close.Click += (_, _) => w.Close();
        buttons.Children.Add(close);
        if (yes is not null)
        {
            var ok = new Button { Content = yes, MinWidth = 80 };
            ok.Click += (_, _) => { result = true; w.Close(); };
            buttons.Children.Add(ok);
        }
        var body = Ui.Text(text);
        if (foreground is not null) body.Foreground = foreground;
        w.Content = Ui.Column(16, body, buttons).With(p => p.Margin = new Thickness(20));
        Track(w);
        await ShowModalAsync(w, owner);
        return result;
    }

    /// <summary>A yes/no question; "取消" is the default, as on Windows (No is the default button).</summary>
    public static Task<bool> ConfirmAsync(Visual from, string text, string title = Strings.AppName, string? yes = null, bool warning = false) =>
        AskAsync(OwnerOf(from), title, text, yes ?? Strings.BtnOk, Strings.BtnCancel, warning ? WarnBrush : null);

    public static Task InfoAsync(Visual from, string text, string title = Strings.AppName) =>
        AskAsync(OwnerOf(from), title, text, null, Strings.BtnOk, null);

    public static Task ErrorAsync(Visual from, string text, string title = Strings.AppName) =>
        AskAsync(OwnerOf(from), title, text, null, Strings.BtnOk, ErrorBrush);

    public static readonly FilePickerFileType KdbxType = new(MacStrings.FileTypeKdbx) { Patterns = new[] { "*.kdbx" } };
    public static readonly FilePickerFileType CsvType = new(MacStrings.FileTypeCsv) { Patterns = new[] { "*.csv" } };
    public static readonly FilePickerFileType AnyType = new(MacStrings.FileTypeAll) { Patterns = new[] { "*" } };

    /// <summary>One local file the user picks; null when cancelled.</summary>
    public static async Task<string?> PickOpenFileAsync(Visual from, string title, FilePickerFileType? type = null)
    {
        if (TopLevel.GetTopLevel(from)?.StorageProvider is not { } sp) return null;
        var options = new FilePickerOpenOptions { Title = title, AllowMultiple = false };
        if (type is not null) options.FileTypeFilter = new[] { type, AnyType };
        var files = await sp.OpenFilePickerAsync(options);
        return files.Select(f => f.TryGetLocalPath()).OfType<string>().FirstOrDefault();
    }

    /// <summary>A save panel. The core never overwrites, so an existing name is replaced by a free one.</summary>
    public static async Task<string?> PickSaveFileAsync(Visual from, string title, string fileName, FilePickerFileType? type = null)
    {
        if (TopLevel.GetTopLevel(from)?.StorageProvider is not { } sp) return null;
        var options = new FilePickerSaveOptions { Title = title, SuggestedFileName = fileName, ShowOverwritePrompt = false };
        if (type is not null) options.FileTypeChoices = new[] { type };
        var file = await sp.SaveFilePickerAsync(options);
        var path = file?.TryGetLocalPath();
        return path is null ? null : FreeName.Next(path, File.Exists);
    }
}

/// <summary>Builders for the code-only vault dialogs (port of the Windows DialogKit).</summary>
internal static class DialogKit
{
    public static TextBlock Text(string text, IBrush? foreground = null, double size = 0, bool bold = false, double top = 0, double bottom = 0)
    {
        var t = new TextBlock { Text = text, TextWrapping = TextWrapping.Wrap, Margin = new Thickness(0, top, 0, bottom) };
        if (foreground is not null) t.Foreground = foreground;
        if (size > 0) t.FontSize = size;
        if (bold) t.FontWeight = FontWeight.SemiBold;
        return t;
    }

    public static Button Btn(string text, double minWidth = 0) => new() { Content = text, MinWidth = minWidth, VerticalAlignment = VerticalAlignment.Center };

    /// <summary>Buttons on the right, macOS order: the default (last argument first in the code) ends up rightmost.</summary>
    public static StackPanel ButtonRow(params Control[] buttons)
    {
        var p = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Right, Margin = new Thickness(0, 16, 0, 0) };
        foreach (var b in buttons) p.Children.Add(b);
        return p;
    }

    /// <summary>A tracked, protected dialog window; <paramref name="body"/> is its content with 18 px padding.</summary>
    public static Window Create(MacController shell, string title, double width, Control body, double height = 0, bool scroll = false)
    {
        var w = new Window
        {
            Title = title, Width = width, CanResize = height > 0, ShowInTaskbar = false,
            WindowStartupLocation = WindowStartupLocation.CenterOwner,
        };
        if (height > 0) w.Height = height; else w.SizeToContent = SizeToContent.Height;
        var host = new Border { Padding = new Thickness(18), Child = body };
        w.Content = scroll ? new ScrollViewer { Content = host } : host;
        shell.ProtectDialog(w);
        VaultUi.Track(w);
        WireActivity(w, shell, closeOnEscape: true);
        return w;
    }

    /// <summary>Input in a vault window counts as activity (the core's idle lock measures real inactivity); Esc closes.</summary>
    public static void WireActivity(Window w, MacController shell, bool closeOnEscape)
    {
        w.AddHandler(InputElement.KeyDownEvent, (_, e) =>
        {
            shell.NoteVaultActivity();
            if (closeOnEscape && e.Key == Key.Escape) { e.Handled = true; w.Close(); }
        }, RoutingStrategies.Tunnel, handledEventsToo: true);
        w.AddHandler(InputElement.PointerPressedEvent, (_, _) => shell.NoteVaultActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
        w.AddHandler(InputElement.PointerMovedEvent, (_, _) => shell.NoteVaultActivity(), RoutingStrategies.Tunnel, handledEventsToo: true);
    }
}

/// <summary>Five coloured segments and a word: how strong a password is (vault.strength labels).</summary>
internal sealed class StrengthBar : Border
{
    private const uint Empty = 0x40808080;
    private readonly Border[] _segments = new Border[5];
    private readonly TextBlock _text = new() { Margin = new Thickness(8, 0, 0, 0), VerticalAlignment = VerticalAlignment.Center, FontSize = 12 };

    public StrengthBar()
    {
        var row = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 3 };
        for (var i = 0; i < _segments.Length; i++)
        {
            _segments[i] = new Border { Width = 34, Height = 7, CornerRadius = new CornerRadius(2), Background = VaultUi.Brush(Empty), VerticalAlignment = VerticalAlignment.Center };
            row.Children.Add(_segments[i]);
        }
        row.Children.Add(_text);
        Child = row;
        HorizontalAlignment = HorizontalAlignment.Left;
        Clear();
    }

    public void Set(string? label, double bits = 0)
    {
        var filled = StrengthDisplay.Segments(label);
        var color = VaultUi.Brush(StrengthDisplay.Color(label));
        for (var i = 0; i < _segments.Length; i++)
            _segments[i].Background = i < filled ? color : VaultUi.Brush(Empty);
        var bitsText = StrengthDisplay.Bits(bits);
        _text.Text = StrengthDisplay.Text(label) + (bitsText.Length > 0 ? "  ·  " + bitsText : "");
        _text.Foreground = color;
    }

    public void Clear()
    {
        foreach (var s in _segments) s.Background = VaultUi.Brush(Empty);
        _text.Text = "";
    }
}

/// <summary>A ring that empties as the TOTP code runs out, with the seconds in the middle.</summary>
internal sealed class TotpRing : Grid
{
    private const double Size = 34;
    private readonly Arc _arc = new() { Width = Size, Height = Size, StrokeThickness = 4, StartAngle = -90 };
    private readonly TextBlock _label = new() { HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center, FontSize = 11 };

    public TotpRing()
    {
        Width = Size;
        Height = Size;
        Children.Add(new Ellipse { Width = Size, Height = Size, StrokeThickness = 4, Stroke = VaultUi.Brush(0x40808080) });
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
/// A text box for secrets: masked with a show / hide button, or, with <see cref="Masked"/> off, a plain text box.
/// Undo is off so the box keeps no copies of earlier values; clear it with <see cref="Clear"/>.
/// </summary>
internal sealed class SecretBox : DockPanel
{
    private readonly TextBox _box = new() { PasswordChar = '•', IsUndoEnabled = false, VerticalContentAlignment = VerticalAlignment.Center };
    private readonly Button _toggle = new() { Content = Strings.BtnShowPassword, Margin = new Thickness(6, 0, 0, 0), MinWidth = 52, VerticalAlignment = VerticalAlignment.Center };
    private bool _masked = true;
    private bool _shown;

    public event Action? TextChanged;

    public SecretBox()
    {
        SetDock(_toggle, Dock.Right);
        Children.Add(_toggle);
        Children.Add(_box);
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
        _toggle.IsVisible = _masked;
        _toggle.Content = _shown ? Strings.BtnHidePassword : Strings.BtnShowPassword;
    }
}

/// <summary>
/// Master password (+ optional key file) input used by every vault dialog that asks for credentials. The password is
/// cleared with <see cref="Clear"/> right after each use.
/// </summary>
internal sealed class CredsBox : StackPanel
{
    private readonly TextBlock _caption = new() { Margin = new Thickness(0, 0, 0, 4) };
    private readonly TextBox _password = new() { PasswordChar = '•', IsUndoEnabled = false };
    private readonly TextBlock _keyName = new() { Foreground = VaultUi.MutedBrush, VerticalAlignment = VerticalAlignment.Center, TextTrimming = TextTrimming.CharacterEllipsis, MaxWidth = 260 };
    private readonly TextBlock _error = new() { Foreground = VaultUi.ErrorBrush, TextWrapping = TextWrapping.Wrap, IsVisible = false, Margin = new Thickness(0, 4, 0, 0) };
    private readonly Button _pick = new() { Content = Strings.BtnPickKeyFile };
    private readonly Button _drop = new() { Content = Strings.BtnNoKeyFile, IsVisible = false };
    private readonly StackPanel _keyRow = new() { Orientation = Orientation.Horizontal, Spacing = 8, Margin = new Thickness(0, 6, 0, 0) };
    private byte[]? _keyFile;

    /// <summary>Return pressed in the password box.</summary>
    public event Action? Submit;

    /// <summary>The password changed (used for the live strength bar).</summary>
    public event Action? PasswordChanged;

    public CredsBox()
    {
        Children.Add(_caption);
        Children.Add(_password);
        _keyRow.Children.Add(_pick);
        _keyRow.Children.Add(_drop);
        _keyRow.Children.Add(_keyName);
        Children.Add(_keyRow);
        Children.Add(_error);
        Caption = Strings.LabelMasterPassword;
        _keyName.Text = Strings.KeyFileNone;
        _password.AddHandler(KeyDownEvent, (_, e) =>
        {
            if (e.Key == Key.Enter) { e.Handled = true; Submit?.Invoke(); }
        }, RoutingStrategies.Tunnel);
        _password.TextChanged += (_, _) => PasswordChanged?.Invoke();
        _pick.Click += async (_, _) => await PickKeyFileAsync();
        _drop.Click += (_, _) => SetKeyFile(null, null);
    }

    public string Caption
    {
        get => _caption.Text ?? "";
        set => _caption.Text = value;
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

    public void ShowError(string? message)
    {
        _error.Text = message ?? "";
        _error.IsVisible = !string.IsNullOrEmpty(message);
    }

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
