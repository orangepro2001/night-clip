using System.Windows;

namespace ClipBridge.Windows.Views;

/// <summary>First start: sync stays off until the user answers here. <see cref="Choice"/> is null if the window was just closed.</summary>
public partial class FirstRunWindow : Window
{
    public bool? Choice { get; private set; }

    public FirstRunWindow()
    {
        InitializeComponent();
        Title = Strings.FirstRunTitle;
        TitleText.Text = Strings.FirstRunTitle;
        BodyText.Text = Strings.FirstRunText;
        TailscaleText.Text = Strings.FirstRunTailscale;
        EnableButton.Content = Strings.FirstRunEnable;
        LaterButton.Content = Strings.FirstRunLater;
        if (MainWindow.Instance is { IsVisible: true } owner) { Owner = owner; WindowStartupLocation = WindowStartupLocation.CenterOwner; }
    }

    private void Enable_Click(object sender, RoutedEventArgs e)
    {
        Choice = true;
        Close();
    }

    private void Later_Click(object sender, RoutedEventArgs e)
    {
        Choice = false;
        Close();
    }
}
