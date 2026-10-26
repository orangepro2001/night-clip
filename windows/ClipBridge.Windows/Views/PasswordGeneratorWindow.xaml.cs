using System.Windows;
using System.Windows.Controls;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>The "生成…" popup: options in, one generated secret out (vault.generate). The value is only kept in the result box.</summary>
public partial class PasswordGeneratorWindow : Window
{
    private readonly ShellController _shell;
    private bool _ready;
    private int _generation;

    private PasswordGeneratorWindow(ShellController shell)
    {
        _shell = shell;
        InitializeComponent();
        shell.ProtectDialog(this);
        VaultUi.Track(this);
        _ready = true;
        UpdateLabels();
        Loaded += async (_, _) => await RegenerateAsync();
    }

    public string? Result { get; private set; }

    /// <summary>Shows the popup; returns the chosen text, or null when cancelled.</summary>
    internal static string? Ask(Window? owner, ShellController shell)
    {
        var w = new PasswordGeneratorWindow(shell);
        VaultUi.Place(w, owner);
        var ok = w.ShowDialog() == true;
        var result = ok ? w.Result : null;
        w.ResultBox.Clear();
        return result;
    }

    private GeneratorOptions Options() => new(
        Passphrase: ModePassphrase.IsChecked == true,
        Length: (int)LengthSlider.Value, Lower: LowerBox.IsChecked == true, Upper: UpperBox.IsChecked == true,
        Digits: DigitsBox.IsChecked == true, Symbols: SymbolsBox.IsChecked == true, ExcludeAmbiguous: AmbiguousBox.IsChecked == true,
        Words: (int)WordsSlider.Value, Separator: SeparatorBox.Text, Capitalize: CapitalizeBox.IsChecked == true, Number: NumberBox.IsChecked == true);

    private void UpdateLabels()
    {
        LengthText.Text = Strings.GenLength((int)LengthSlider.Value);
        WordsText.Text = Strings.GenWords((int)WordsSlider.Value);
    }

    private async Task RegenerateAsync()
    {
        var gen = ++_generation;
        var options = Options();
        var r = await _shell.VaultCallAsync(a => a.Generate(options));
        if (gen != _generation) return;
        if (!r.Ok)
        {
            ErrorText.Text = r.Error ?? "";
            UseButton.IsEnabled = false;
            return;
        }
        ErrorText.Text = "";
        UseButton.IsEnabled = true;
        ResultBox.Text = r.Value!.Value;
        Bar.Set(r.Value.Strength.Label, r.Value.Strength.Bits);
    }

    private async void Mode_Checked(object sender, RoutedEventArgs e)
    {
        if (!_ready) return;
        var passphrase = ModePassphrase.IsChecked == true;
        PasswordPane.Visibility = passphrase ? Visibility.Collapsed : Visibility.Visible;
        PassphrasePane.Visibility = passphrase ? Visibility.Visible : Visibility.Collapsed;
        await RegenerateAsync();
    }

    private async void Slider_ValueChanged(object sender, RoutedPropertyChangedEventArgs<double> e)
    {
        if (!_ready) return;
        UpdateLabels();
        await RegenerateAsync();
    }

    private async void Option_Click(object sender, RoutedEventArgs e)
    {
        if (!_ready) return;
        // The core treats "no class selected" as "all": show that instead of an empty selection.
        if (sender is CheckBox && LowerBox.IsChecked != true && UpperBox.IsChecked != true && DigitsBox.IsChecked != true && SymbolsBox.IsChecked != true)
            LowerBox.IsChecked = true;
        await RegenerateAsync();
    }

    private async void Separator_TextChanged(object sender, TextChangedEventArgs e)
    {
        if (!_ready) return;
        await RegenerateAsync();
    }

    private async void Regenerate_Click(object sender, RoutedEventArgs e) => await RegenerateAsync();

    private void Use_Click(object sender, RoutedEventArgs e)
    {
        Result = ResultBox.Text;
        DialogResult = true;
    }
}
