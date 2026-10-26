using System.Windows;

namespace ClipBridge.Windows.Views;

/// <summary>A one-line text prompt. The validator returns the accepted (normalised) text, or null to refuse it.</summary>
public partial class InputWindow : Window
{
    private readonly Func<string?, string?> _validate;

    private InputWindow(string title, string prompt, string initial, Func<string?, string?> validate)
    {
        _validate = validate;
        InitializeComponent();
        Title = title;
        PromptText.Text = prompt;
        InputBox.Text = initial;
        OkButton.Content = Strings.BtnOk;
        CancelButton.Content = Strings.BtnCancel;
        Loaded += (_, _) => { InputBox.Focus(); InputBox.SelectAll(); };
        PreviewKeyDown += (_, e) => { if (e.Key == System.Windows.Input.Key.Escape) DialogResult = false; };
    }

    public string? Result { get; private set; }

    public static string? Ask(Window? owner, string title, string prompt, string initial, Func<string?, string?> validate)
    {
        var w = new InputWindow(title, prompt, initial, validate);
        if (owner is { IsVisible: true }) w.Owner = owner;
        else w.WindowStartupLocation = WindowStartupLocation.CenterScreen;
        return w.ShowDialog() == true ? w.Result : null;
    }

    private void Ok_Click(object sender, RoutedEventArgs e)
    {
        var accepted = _validate(InputBox.Text);
        if (accepted is null)
        {
            ErrorText.Text = Strings.NameInvalid;
            return;
        }
        Result = accepted;
        DialogResult = true;
    }

    private void Cancel_Click(object sender, RoutedEventArgs e) => DialogResult = false;
}
