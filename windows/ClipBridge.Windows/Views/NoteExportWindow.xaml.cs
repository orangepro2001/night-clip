using System.Windows;
using ClipBridge.Windows.Logic;
using Microsoft.Win32;

namespace ClipBridge.Windows.Views;

/// <summary>Where to write the export and whether the recycle bin goes with it.</summary>
public sealed record NoteExportChoice(string Path, bool IncludeTrash);

/// <summary>
/// The first step of "导出全部笔记…": a save dialog cannot carry a check box, so this small window asks about the recycle bin and
/// then opens the save dialog. The save dialog itself asks whether an existing file may be replaced.
/// </summary>
public partial class NoteExportWindow : Window
{
    private NoteExportWindow()
    {
        InitializeComponent();
        Title = Strings.NoteExportTitle;
    }

    private NoteExportChoice? Choice { get; set; }

    /// <summary>The chosen file and option, or null when the user gave up.</summary>
    public static NoteExportChoice? Ask(Window? owner)
    {
        var w = new NoteExportWindow();
        if (owner is { IsVisible: true }) w.Owner = owner;
        else w.WindowStartupLocation = WindowStartupLocation.CenterScreen;
        return w.ShowDialog() == true ? w.Choice : null;
    }

    private void Choose_Click(object sender, RoutedEventArgs e)
    {
        var dialog = new SaveFileDialog
        {
            Title = Strings.NoteExportSaveTitle, Filter = Strings.NoteZipFilter, DefaultExt = ".zip", AddExtension = true,
            FileName = NotesTransfer.DefaultExportName(DateTimeOffset.Now), OverwritePrompt = true, CheckPathExists = true,
        };
        if (dialog.ShowDialog(this) != true) return; // back to this window
        Choice = new NoteExportChoice(dialog.FileName, TrashBox.IsChecked == true);
        DialogResult = true;
    }
}
