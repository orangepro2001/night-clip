using System.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Views;

/// <summary>What an import did: how many notes it made and, one click away, which files it skipped and why.</summary>
public partial class NoteImportResultWindow : Window
{
    private NoteImportResultWindow(NoteImportResult result)
    {
        InitializeComponent();
        Title = Strings.NoteImportResultTitle;
        SummaryText.Text = NotesTransferText.ImportSummary(result);
        if (result.Skipped.Count == 0)
        {
            SkippedExpander.Visibility = Visibility.Collapsed;
            return;
        }
        SkippedExpander.Header = Strings.NoteImportSkippedHeader(result.Skipped.Count);
        SkippedBox.Text = NotesTransferText.ImportDetails(result);
    }

    public static void Show(Window? owner, NoteImportResult result)
    {
        var w = new NoteImportResultWindow(result);
        if (owner is { IsVisible: true }) w.Owner = owner;
        else w.WindowStartupLocation = WindowStartupLocation.CenterScreen;
        w.ShowDialog();
    }

    private void Ok_Click(object sender, RoutedEventArgs e) => Close();
}
