using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Shapes;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;
using Microsoft.Win32;

namespace ClipBridge.Windows.Views;

/// <summary>Small helpers shared by the vault views.</summary>
internal static class VaultUi
{
    public static Brush Brush(uint argb)
    {
        var b = new SolidColorBrush(Color.FromArgb((byte)(argb >> 24), (byte)(argb >> 16), (byte)(argb >> 8), (byte)argb));
        b.Freeze();
        return b;
    }

    private static readonly Dictionary<Window, Action> OpenDialogs = new();

    /// <summary>
    /// Registers a vault dialog so that locking the vault can close it (a dialog must not keep secrets on screen
    /// after a lock). <paramref name="forceClose"/> closes it without any "unsaved changes" prompt.
    /// </summary>
    public static void Track(Window dialog, Action? forceClose = null)
    {
        OpenDialogs[dialog] = forceClose ?? (() => dialog.Close());
        dialog.Closed += (_, _) => OpenDialogs.Remove(dialog);
    }

    /// <summary>Closes every open vault dialog (on lock). Message boxes cannot be tracked and may stay open.</summary>
    public static void CloseAll()
    {
        foreach (var close in OpenDialogs.Values.ToList())
        {
            try { close(); }
            catch (InvalidOperationException) { /* already closing */ }
        }
    }

    public static readonly Brush ErrorBrush = Brush(0xFFC62828);
    public static readonly Brush OkBrush = Brush(0xFF1E8E3E);
    public static readonly Brush MutedBrush = Brush(0xFF777777);

    /// <summary>Centres a dialog over its owner when that is showing, otherwise over the screen.</summary>
    public static void Place(Window dialog, Window? owner)
    {
        if (owner is { IsVisible: true }) dialog.Owner = owner;
        else dialog.WindowStartupLocation = WindowStartupLocation.CenterScreen;
    }

    public static bool Confirm(Window? owner, string text, string title = Strings.AppName, bool warning = false)
    {
        var icon = warning ? MessageBoxImage.Warning : MessageBoxImage.Question;
        var result = owner is { IsVisible: true }
            ? MessageBox.Show(owner, text, title, MessageBoxButton.YesNo, icon, MessageBoxResult.No)
            : MessageBox.Show(text, title, MessageBoxButton.YesNo, icon, MessageBoxResult.No);
        return result == MessageBoxResult.Yes;
    }

    public static void Info(Window? owner, string text, string title = Strings.AppName)
    {
        if (owner is { IsVisible: true }) MessageBox.Show(owner, text, title, MessageBoxButton.OK, MessageBoxImage.Information);
        else MessageBox.Show(text, title, MessageBoxButton.OK, MessageBoxImage.Information);
    }

    public static void Error(Window? owner, string text, string title = Strings.AppName)
    {
        if (owner is { IsVisible: true }) MessageBox.Show(owner, text, title, MessageBoxButton.OK, MessageBoxImage.Warning);
        else MessageBox.Show(text, title, MessageBoxButton.OK, MessageBoxImage.Warning);
    }

    public static string? PickOpenFile(Window? owner, string title, string filter)
    {
        var dialog = new OpenFileDialog { Title = title, Filter = filter, CheckFileExists = true };
        return dialog.ShowDialog(owner) == true ? dialog.FileName : null;
    }

    /// <summary>A save dialog. The core never overwrites, so an existing name is replaced by a free one (the caller is told).</summary>
    public static string? PickSaveFile(Window? owner, string title, string filter, string fileName)
    {
        var dialog = new SaveFileDialog { Title = title, Filter = filter, FileName = fileName, OverwritePrompt = false, AddExtension = true };
        return dialog.ShowDialog(owner) == true ? FreeName.Next(dialog.FileName, File.Exists) : null;
    }
}

/// <summary>Five coloured segments and a word: how strong a password is (vault.strength labels).</summary>
internal sealed class StrengthBar : UserControl
{
    private readonly Border[] _segments = new Border[5];
    private readonly TextBlock _text = new() { Margin = new Thickness(8, 0, 0, 0), VerticalAlignment = VerticalAlignment.Center };

    public StrengthBar()
    {
        var row = new StackPanel { Orientation = Orientation.Horizontal };
        for (var i = 0; i < _segments.Length; i++)
        {
            _segments[i] = new Border { Width = 34, Height = 7, Margin = new Thickness(0, 0, 3, 0), CornerRadius = new CornerRadius(2), Background = VaultUi.Brush(0xFFE0E0E0) };
            row.Children.Add(_segments[i]);
        }
        row.Children.Add(_text);
        Content = row;
        Clear();
    }

    public void Set(string? label, double bits = 0)
    {
        var filled = StrengthDisplay.Segments(label);
        var color = VaultUi.Brush(StrengthDisplay.Color(label));
        for (var i = 0; i < _segments.Length; i++)
            _segments[i].Background = i < filled ? color : VaultUi.Brush(0xFFE0E0E0);
        var bitsText = StrengthDisplay.Bits(bits);
        _text.Text = StrengthDisplay.Text(label) + (bitsText.Length > 0 ? "  ·  " + bitsText : "");
        _text.Foreground = color;
    }

    public void Clear()
    {
        foreach (var s in _segments) s.Background = VaultUi.Brush(0xFFE0E0E0);
        _text.Text = "";
    }
}

/// <summary>A ring that empties as the TOTP code runs out, with the seconds in the middle.</summary>
internal sealed class TotpRing : UserControl
{
    private const double Size = 34;
    private const double Thickness = 4;
    private readonly Ellipse _track = new() { Width = Size, Height = Size, StrokeThickness = Thickness, Stroke = VaultUi.Brush(0xFFE0E0E0) };
    private readonly Ellipse _arc = new() { Width = Size, Height = Size, StrokeThickness = Thickness, RenderTransformOrigin = new Point(0.5, 0.5), RenderTransform = new RotateTransform(-90) };
    private readonly TextBlock _label = new() { HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center, FontSize = 11 };

    public TotpRing()
    {
        Width = Size + Thickness;
        Height = Size + Thickness;
        var grid = new Grid();
        grid.Children.Add(_track);
        grid.Children.Add(_arc);
        grid.Children.Add(_label);
        Content = grid;
    }

    /// <summary><paramref name="fraction"/> 1 = full ring.</summary>
    public void Set(double fraction, int secondsLeft)
    {
        // Dash lengths are in multiples of the stroke thickness; the circle's length is pi * (diameter - thickness).
        var circumference = Math.PI * (Size - Thickness) / Thickness;
        var color = VaultUi.Brush(TotpCountdown.ColorArgb(secondsLeft));
        _arc.Stroke = color;
        _arc.StrokeDashArray = new DoubleCollection { circumference * Math.Clamp(fraction, 0, 1), circumference + 4 };
        _label.Text = secondsLeft.ToString(System.Globalization.CultureInfo.InvariantCulture);
        _label.Foreground = color;
    }
}

/// <summary>
/// A text box for secrets: masked (PasswordBox) with a show / hide button, or, with <see cref="Masked"/> off, a plain
/// multi-purpose TextBox. The value is only ever held by the two boxes; clear it with <see cref="Clear"/>.
/// </summary>
internal sealed class SecretBox : UserControl
{
    private readonly PasswordBox _password = new() { VerticalContentAlignment = VerticalAlignment.Center };
    private readonly TextBox _plain = new() { Visibility = Visibility.Collapsed, VerticalContentAlignment = VerticalAlignment.Center };
    private readonly Button _toggle = new() { Content = Strings.BtnShowPassword, Margin = new Thickness(6, 0, 0, 0), MinWidth = 52 };
    private bool _masked = true;
    private bool _shown;
    private bool _syncing;

    public event Action? TextChanged;

    public SecretBox()
    {
        var dock = new DockPanel();
        DockPanel.SetDock(_toggle, Dock.Right);
        dock.Children.Add(_toggle);
        dock.Children.Add(_password);
        dock.Children.Add(_plain);
        Content = dock;
        _toggle.Click += (_, _) => { _shown = !_shown; Apply(); };
        _password.PasswordChanged += (_, _) => { if (!_syncing) { _syncing = true; _plain.Text = _password.Password; _syncing = false; TextChanged?.Invoke(); } };
        _plain.TextChanged += (_, _) => { if (!_syncing) { _syncing = true; _password.Password = _plain.Text; _syncing = false; TextChanged?.Invoke(); } };
        Apply();
    }

    public string Text
    {
        get => _password.Password;
        set
        {
            _syncing = true;
            _password.Password = value;
            _plain.Text = value;
            _syncing = false;
            TextChanged?.Invoke();
        }
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
        var showPlain = !_masked || _shown;
        _password.Visibility = showPlain ? Visibility.Collapsed : Visibility.Visible;
        _plain.Visibility = showPlain ? Visibility.Visible : Visibility.Collapsed;
        _toggle.Visibility = _masked ? Visibility.Visible : Visibility.Collapsed;
        _toggle.Content = _shown ? Strings.BtnHidePassword : Strings.BtnShowPassword;
    }
}

/// <summary>
/// Master password (+ optional key file) input used by every vault dialog that asks for credentials. The
/// password is cleared with <see cref="Clear"/> right after each use.
/// </summary>
internal sealed class CredsBox : UserControl
{
    private readonly TextBlock _caption = new() { Margin = new Thickness(0, 0, 0, 4) };
    private readonly PasswordBox _password = new();
    private readonly TextBlock _keyName = new() { Foreground = VaultUi.MutedBrush, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(8, 0, 0, 0), TextTrimming = TextTrimming.CharacterEllipsis };
    private readonly TextBlock _error = new() { Foreground = VaultUi.ErrorBrush, TextWrapping = TextWrapping.Wrap, Visibility = Visibility.Collapsed, Margin = new Thickness(0, 4, 0, 0) };
    private readonly Button _pick = new() { Content = Strings.BtnPickKeyFile, Margin = new Thickness(0) };
    private readonly Button _drop = new() { Content = Strings.BtnNoKeyFile, Visibility = Visibility.Collapsed };
    private readonly StackPanel _keyRow = new() { Orientation = Orientation.Horizontal, Margin = new Thickness(0, 6, 0, 0) };
    private byte[]? _keyFile;

    /// <summary>Enter pressed in the password box.</summary>
    public event Action? Submit;

    /// <summary>The password changed (used for the live strength bar).</summary>
    public event Action? PasswordChanged;

    public CredsBox()
    {
        var panel = new StackPanel();
        panel.Children.Add(_caption);
        panel.Children.Add(_password);
        _keyRow.Children.Add(_pick);
        _keyRow.Children.Add(_drop);
        _keyRow.Children.Add(_keyName);
        panel.Children.Add(_keyRow);
        panel.Children.Add(_error);
        Content = panel;
        Caption = Strings.LabelMasterPassword;
        _keyName.Text = Strings.KeyFileNone;
        _password.PreviewKeyDown += (_, e) =>
        {
            if (e.Key == System.Windows.Input.Key.Enter) { e.Handled = true; Submit?.Invoke(); }
        };
        _password.PasswordChanged += (_, _) => PasswordChanged?.Invoke();
        _pick.Click += (_, _) => PickKeyFile();
        _drop.Click += (_, _) => SetKeyFile(null, null);
    }

    public string Caption
    {
        get => _caption.Text;
        set => _caption.Text = value;
    }

    public bool ShowKeyFile
    {
        get => _keyRow.Visibility == Visibility.Visible;
        set => _keyRow.Visibility = value ? Visibility.Visible : Visibility.Collapsed;
    }

    public string Password => _password.Password;
    public byte[]? KeyFile => _keyFile;
    public VaultCreds Creds => new(_password.Password, _keyFile);

    public void FocusPassword() => _password.Focus();

    /// <summary>Forget the password (and, with <paramref name="keyFileToo"/>, the key file bytes).</summary>
    public void Clear(bool keyFileToo = false)
    {
        _password.Clear();
        if (keyFileToo) SetKeyFile(null, null);
    }

    public void ShowError(string? message)
    {
        _error.Text = message ?? "";
        _error.Visibility = string.IsNullOrEmpty(message) ? Visibility.Collapsed : Visibility.Visible;
    }

    private void PickKeyFile()
    {
        var path = VaultUi.PickOpenFile(Window.GetWindow(this), Strings.LabelKeyFile, Strings.FilterAnyFile);
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
        _drop.Visibility = bytes is null ? Visibility.Collapsed : Visibility.Visible;
    }
}
