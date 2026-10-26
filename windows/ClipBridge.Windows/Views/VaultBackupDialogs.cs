using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>Backups & restore: list, back up now, verify, preview the differences, restore (docs/vault-api.md §3.5).</summary>
internal static class BackupsWindow
{
    /// <summary>Returns true when something was restored (the caller reloads the list).</summary>
    public static bool Show(Window? owner, ShellController shell)
    {
        var restored = false;
        var list = new ListView { MinHeight = 200 };
        var grid = new GridView();
        grid.Columns.Add(new GridViewColumn { Header = Strings.ColTime, Width = 150, DisplayMemberBinding = new System.Windows.Data.Binding(nameof(BackupRow.TimeText)) });
        grid.Columns.Add(new GridViewColumn { Header = Strings.ColReason, Width = 170, DisplayMemberBinding = new System.Windows.Data.Binding(nameof(BackupRow.ReasonText)) });
        grid.Columns.Add(new GridViewColumn { Header = Strings.ColSize, Width = 90, DisplayMemberBinding = new System.Windows.Data.Binding(nameof(BackupRow.SizeText)) });
        list.View = grid;
        var status = DialogKit.Text("", top: 8);
        var empty = DialogKit.Text(Strings.BackupsNone, VaultUi.MutedBrush, top: 4);
        empty.Visibility = Visibility.Collapsed;

        var now = DialogKit.Btn(Strings.BtnBackupNow);
        var verify = DialogKit.Btn(Strings.BtnBackupVerify);
        var preview = DialogKit.Btn(Strings.BtnBackupPreview);
        var close = DialogKit.Btn(Strings.Close, null, 90);
        close.IsCancel = true;

        var top = new StackPanel();
        top.Children.Add(DialogKit.Text(Strings.BackupsIntro, VaultUi.MutedBrush, 12, bottom: 4));
        top.Children.Add(DialogKit.Text(Strings.BackupsRestoreNote, VaultUi.Brush(0xFF1A5FB4), 12, bottom: 8));
        var toolbar = new StackPanel { Orientation = Orientation.Horizontal, Margin = new Thickness(0, 0, 0, 8) };
        now.Margin = new Thickness(0);
        toolbar.Children.Add(now);
        toolbar.Children.Add(verify);
        toolbar.Children.Add(preview);
        top.Children.Add(toolbar);
        var bottom = new StackPanel();
        bottom.Children.Add(status);
        bottom.Children.Add(DialogKit.ButtonRow(close));
        var dock = new DockPanel();
        DockPanel.SetDock(top, Dock.Top);
        DockPanel.SetDock(bottom, Dock.Bottom);
        dock.Children.Add(top);
        dock.Children.Add(bottom);
        var center = new Grid();   // the last child of a DockPanel fills the rest: list and "no backups" text share it
        center.Children.Add(list);
        center.Children.Add(empty);
        dock.Children.Add(center);

        var w = DialogKit.Create(shell, owner, Strings.BackupsTitle, 600, dock, height: 560);
        close.Click += (_, _) => w.Close();

        void Say(string text, bool ok)
        {
            status.Text = text;
            status.Foreground = ok ? VaultUi.OkBrush : VaultUi.ErrorBrush;
        }

        async Task LoadAsync()
        {
            var r = await shell.VaultCallAsync(a => a.BackupList());
            if (!r.Ok) { Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            var rows = r.Value!.Select(b => BackupRow.From(b, DateTimeOffset.UtcNow)).ToList();
            list.ItemsSource = rows;
            empty.Visibility = rows.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        }

        BackupRow? Selected() => list.SelectedItem as BackupRow;

        w.Loaded += async (_, _) => await LoadAsync();

        now.Click += async (_, _) =>
        {
            now.IsEnabled = false;
            var r = await shell.VaultCallAsync(a => a.BackupNow());
            now.IsEnabled = true;
            if (!r.Ok) { Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            Say(Strings.BackupCreated, true);
            await LoadAsync();
        };

        verify.Click += async (_, _) =>
        {
            if (Selected() is not { } row) { Say(Strings.EntryChoose, false); return; }
            var creds = AskCredsDialog.Ask(w, shell, Strings.BackupVerifyTitle, Strings.BackupVerifyPrompt, Strings.LabelMasterPassword, allowEmpty: false);
            if (creds is null) return;
            Say(Strings.Working, true);
            var r = await shell.VaultCallAsync(a => a.BackupVerify(row.Name, creds));
            if (!r.Ok) { Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            var c = r.Value!;
            Say(Strings.BackupVerified(c.Entries, c.Groups, c.InTrash, StampText.Local(c.ModifiedAt)), true);
        };

        preview.Click += async (_, _) =>
        {
            if (Selected() is not { } row) { Say(Strings.EntryChoose, false); return; }
            var asked = AskCredsDialog.Ask(w, shell, Strings.RestorePreviewTitle, Strings.BackupPreviewPrompt, Strings.BackupOtherPassword, allowEmpty: true);
            if (asked is null) return;
            VaultCreds? creds = asked.Password.Length == 0 ? null : asked; // empty = the current credentials
            Say(Strings.Working, true);
            var r = await shell.VaultCallAsync(a => a.PreviewRestore(row.Name, creds));
            if (!r.Ok) { Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            Say("", true);
            if (RestorePreviewWindow.Show(w, shell, row.Name, creds, r.Value!))
            {
                restored = true;
                await LoadAsync();
            }
        };

        w.ShowDialog();
        return restored;
    }
}

/// <summary>The differences between a backup and the current vault, with the restore modes.</summary>
internal static class RestorePreviewWindow
{
    /// <summary>Returns true when the restore ran.</summary>
    public static bool Show(Window? owner, ShellController shell, string backupName, VaultCreds? creds, RestorePreview preview)
    {
        var selection = new RestoreSelection(preview);
        var done = false;
        var body = new StackPanel();
        body.Children.Add(DialogKit.Text(selection.Summary, bottom: 4));
        if (selection.IsEmpty) body.Children.Add(DialogKit.Text(Strings.DiffNothing, VaultUi.OkBrush, bottom: 6));

        var checks = new List<CheckBox>();
        foreach (var group in selection.Groups())
        {
            body.Children.Add(DialogKit.Text(group.Title + " (" + group.Items.Count + ")", bold: true, size: 14, top: 10));
            body.Children.Add(DialogKit.Text(group.Hint, VaultUi.MutedBrush, 12, bottom: 2));
            foreach (var item in group.Items)
            {
                var label = (item.Title.Length > 0 ? item.Title : Strings.Untitled) + (item.ModifiedAt > 0 ? "  ·  " + StampText.Local(item.ModifiedAt) : "");
                if (group.Selectable)
                {
                    var id = item.Id;
                    var cb = new CheckBox { Content = label, IsChecked = selection.IsSelected(id), Margin = new Thickness(8, 1, 0, 1) };
                    cb.Click += (_, _) => selection.Set(id, cb.IsChecked == true);
                    checks.Add(cb);
                    body.Children.Add(cb);
                }
                else body.Children.Add(DialogKit.Text("• " + label, VaultUi.MutedBrush, top: 1));
            }
        }

        body.Children.Add(DialogKit.Text(Strings.RestoreChooseMode, bold: true, size: 14, top: 14, bottom: 4));
        var radios = new Dictionary<RestoreMode, RadioButton>();
        var hint = DialogKit.Text("", VaultUi.MutedBrush, 12, top: 2);
        foreach (var mode in Enum.GetValues<RestoreMode>())
        {
            var rb = new RadioButton { Content = RestoreModes.Label(mode), IsChecked = mode == RestoreMode.Entries, Margin = new Thickness(0, 2, 0, 2) };
            radios[mode] = rb;
            body.Children.Add(rb);
        }
        body.Children.Add(hint);
        var confirmText = DialogKit.Text(Strings.RestoreReplaceConfirm, VaultUi.ErrorBrush, top: 8);
        var confirmBox = new TextBox { Width = 160, HorizontalAlignment = HorizontalAlignment.Left };
        confirmText.Visibility = Visibility.Collapsed;
        confirmBox.Visibility = Visibility.Collapsed;
        body.Children.Add(confirmText);
        body.Children.Add(confirmBox);
        body.Children.Add(DialogKit.Text(Strings.BackupsRestoreNote, VaultUi.Brush(0xFF1A5FB4), 12, top: 12));
        var error = DialogKit.Text("", VaultUi.ErrorBrush, top: 8);
        body.Children.Add(error);

        var restore = DialogKit.Btn(Strings.BtnRestore, null, 90);
        var cancel = DialogKit.Btn(Strings.BtnCancel, null, 90);
        cancel.IsCancel = true;
        body.Children.Add(DialogKit.ButtonRow(restore, cancel));
        var w = DialogKit.Create(shell, owner, Strings.RestorePreviewTitle, 560, body, height: 640, scroll: true);

        RestoreMode Mode() => radios.First(kv => kv.Value.IsChecked == true).Key;
        void Refresh()
        {
            var m = Mode();
            hint.Text = RestoreModes.Describe(m);
            var replace = m == RestoreMode.Replace;
            confirmText.Visibility = replace ? Visibility.Visible : Visibility.Collapsed;
            confirmBox.Visibility = replace ? Visibility.Visible : Visibility.Collapsed;
            foreach (var cb in checks) cb.IsEnabled = m == RestoreMode.Entries;
        }
        foreach (var rb in radios.Values) rb.Click += (_, _) => Refresh();
        Refresh();

        restore.Click += async (_, _) =>
        {
            var m = Mode();
            error.Text = "";
            if (!selection.CanRestore(m)) { error.Text = Strings.RestoreNothingSelected; return; }
            if (m == RestoreMode.Replace && !RestoreModes.ReplaceConfirmed(confirmBox.Text)) { error.Text = Strings.ExportCsvNotConfirmed; return; }
            restore.IsEnabled = false;
            var ids = m == RestoreMode.Entries ? selection.SelectedIds : null;
            var r = await shell.VaultCallAsync(a => a.Restore(backupName, RestoreModes.Wire(m), ids, creds));
            restore.IsEnabled = true;
            if (!r.Ok) { error.Text = r.Error ?? Strings.ErrVaultUnexpected; return; }
            done = true;
            shell.ShowStatus(Strings.RestoreDone + "：" + StatsText.Describe(r.Value!));
            VaultUi.Info(w, Strings.RestoreDone + "\n" + StatsText.Describe(r.Value!));
            w.DialogResult = true;
        };
        w.ShowDialog();
        return done;
    }
}

/// <summary>Import a KDBX file (with its password) or a CSV file.</summary>
internal static class ImportWindow
{
    public static bool Show(Window? owner, ShellController shell)
    {
        var done = false;
        string? path = null;
        var kdbx = new RadioButton { Content = Strings.ImportKdbx, IsChecked = true };
        var csv = new RadioButton { Content = Strings.ImportCsv, Margin = new Thickness(0, 4, 0, 0) };
        var pathText = DialogKit.Text(Strings.ImportNoFile, VaultUi.MutedBrush, top: 4);
        var pick = DialogKit.Btn(Strings.ImportChooseFile);
        pick.Margin = new Thickness(0, 8, 0, 0);
        pick.HorizontalAlignment = HorizontalAlignment.Left;
        var creds = new CredsBox { Caption = Strings.ImportKdbxPassword, Margin = new Thickness(0, 10, 0, 0) };
        var csvNote = DialogKit.Text(Strings.ImportCsvNote, VaultUi.Brush(0xFFB36B00), 12, top: 10);
        csvNote.Visibility = Visibility.Collapsed;
        var error = DialogKit.Text("", VaultUi.ErrorBrush, top: 8);
        var go = DialogKit.Btn(Strings.BtnImportNow, null, 90);
        go.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, null, 90);
        cancel.IsCancel = true;

        var panel = new StackPanel();
        panel.Children.Add(kdbx);
        panel.Children.Add(csv);
        panel.Children.Add(pick);
        panel.Children.Add(pathText);
        panel.Children.Add(creds);
        panel.Children.Add(csvNote);
        panel.Children.Add(error);
        panel.Children.Add(DialogKit.ButtonRow(go, cancel));
        var w = DialogKit.Create(shell, owner, Strings.ImportTitle, 520, panel);

        void Mode()
        {
            var isKdbx = kdbx.IsChecked == true;
            creds.Visibility = isKdbx ? Visibility.Visible : Visibility.Collapsed;
            csvNote.Visibility = isKdbx ? Visibility.Collapsed : Visibility.Visible;
        }
        kdbx.Click += (_, _) => Mode();
        csv.Click += (_, _) => Mode();
        pick.Click += (_, _) =>
        {
            var chosen = VaultUi.PickOpenFile(w, Strings.ImportTitle, kdbx.IsChecked == true ? Strings.FilterKdbx : Strings.FilterCsv);
            if (chosen is null) return;
            path = chosen;
            pathText.Text = chosen;
        };
        go.Click += async (_, _) =>
        {
            error.Text = "";
            if (path is null) { error.Text = Strings.ImportNoFile; return; }
            var isKdbx = kdbx.IsChecked == true;
            if (isKdbx && creds.Password.Length == 0) { error.Text = Strings.CreateEmptyPassword; return; }
            go.IsEnabled = false;
            string message;
            if (isKdbx)
            {
                var c = creds.Creds;
                var r = await shell.VaultCallAsync(a => a.ImportFile(path, c));
                creds.Clear(keyFileToo: true);
                go.IsEnabled = true;
                if (!r.Ok) { error.Text = r.Error ?? Strings.ErrVaultUnexpected; return; }
                message = Strings.ImportedKdbx + "\n" + StatsText.Describe(r.Value!);
            }
            else
            {
                var r = await shell.VaultCallAsync(a => a.ImportCsv(path));
                go.IsEnabled = true;
                if (!r.Ok) { error.Text = r.Error ?? Strings.ErrVaultUnexpected; return; }
                message = Strings.ImportedCsv(r.Value.Added, r.Value.Skipped);
            }
            done = true;
            shell.ShowStatus(message.Split('\n')[0]);
            VaultUi.Info(w, message);
            w.DialogResult = true;
        };
        w.Closed += (_, _) => creds.Clear(keyFileToo: true);
        w.ShowDialog();
        return done;
    }
}

/// <summary>Export a KDBX copy (new password) or a plain-text CSV (red warning). Both re-ask the master password.</summary>
internal static class ExportWindow
{
    public static void Show(Window? owner, ShellController shell, bool startWithKdbx = true)
    {
        var kdbx = new RadioButton { Content = Strings.ExportKdbx, IsChecked = startWithKdbx };
        var csv = new RadioButton { Content = Strings.ExportCsv, IsChecked = !startWithKdbx, Margin = new Thickness(0, 4, 0, 0) };
        var master = new CredsBox { Caption = Strings.ExportMasterPrompt, Margin = new Thickness(0, 12, 0, 0) };

        var kdbxPane = new StackPanel { Margin = new Thickness(0, 12, 0, 0) };
        var filePw = new CredsBox { Caption = Strings.ExportFilePassword };
        var filePwConfirm = new CredsBox { Caption = Strings.LabelConfirmPassword, ShowKeyFile = false, Margin = new Thickness(0, 8, 0, 0) };
        kdbxPane.Children.Add(DialogKit.Text(Strings.ExportFilePasswordNote, VaultUi.MutedBrush, 12, bottom: 6));
        kdbxPane.Children.Add(filePw);
        kdbxPane.Children.Add(filePwConfirm);

        var csvPane = new StackPanel { Margin = new Thickness(0, 12, 0, 0) };
        var warn = new Border
        {
            Background = VaultUi.Brush(0xFFFDECEA), BorderBrush = VaultUi.Brush(0xFFC62828), BorderThickness = new Thickness(2),
            CornerRadius = new CornerRadius(6), Padding = new Thickness(12, 8, 12, 8),
        };
        var warnPanel = new StackPanel();
        warnPanel.Children.Add(DialogKit.Text(Strings.ExportCsvWarningTitle, VaultUi.Brush(0xFFB71C1C), 15, bold: true));
        warnPanel.Children.Add(DialogKit.Text(Strings.ExportCsvWarning, VaultUi.Brush(0xFF7F1D1D), top: 4));
        warn.Child = warnPanel;
        csvPane.Children.Add(warn);
        csvPane.Children.Add(DialogKit.Text(Strings.ExportCsvConfirmPrompt, VaultUi.ErrorBrush, top: 10, bottom: 4));
        var confirmWord = new TextBox { Width = 160, HorizontalAlignment = HorizontalAlignment.Left };
        csvPane.Children.Add(confirmWord);

        var error = DialogKit.Text("", VaultUi.ErrorBrush, top: 10);
        var go = DialogKit.Btn(Strings.BtnExportNow, null, 90);
        var cancel = DialogKit.Btn(Strings.Close, null, 90);
        cancel.IsCancel = true;
        var panel = new StackPanel();
        panel.Children.Add(kdbx);
        panel.Children.Add(csv);
        panel.Children.Add(master);
        panel.Children.Add(kdbxPane);
        panel.Children.Add(csvPane);
        panel.Children.Add(error);
        panel.Children.Add(DialogKit.ButtonRow(go, cancel));
        var w = DialogKit.Create(shell, owner, Strings.ExportTitle, 540, panel, height: 640, scroll: true);

        void Mode()
        {
            var isKdbx = kdbx.IsChecked == true;
            kdbxPane.Visibility = isKdbx ? Visibility.Visible : Visibility.Collapsed;
            csvPane.Visibility = isKdbx ? Visibility.Collapsed : Visibility.Visible;
            error.Text = "";
        }
        kdbx.Click += (_, _) => Mode();
        csv.Click += (_, _) => Mode();
        Mode();

        void WipeAll()
        {
            master.Clear(keyFileToo: true);
            filePw.Clear(keyFileToo: true);
            filePwConfirm.Clear();
        }

        go.Click += async (_, _) =>
        {
            error.Text = "";
            var isKdbx = kdbx.IsChecked == true;
            var problem = isKdbx
                ? ExportForm.CheckKdbx(master.Password, filePw.Password, filePwConfirm.Password)
                : ExportForm.CheckCsv(master.Password) ?? (ExportForm.CsvConfirmed(confirmWord.Text) ? null : Strings.ExportCsvNotConfirmed);
            if (problem is not null) { error.Text = problem; return; }
            // A last, separate warning dialog for the plain-text file (No is the default button).
            if (!isKdbx && !VaultUi.Confirm(w, Strings.ExportCsvWarning + "\n\n" + Strings.ExportCsvAreYouSure, Strings.ExportCsvWarningTitle, warning: true)) return;

            var path = VaultUi.PickSaveFile(w, Strings.ExportTitle, isKdbx ? Strings.FilterKdbx : Strings.FilterCsv, ExportForm.DefaultFileName(isKdbx ? "kdbx" : "csv", DateTimeOffset.Now));
            if (path is null) return;

            go.IsEnabled = false;
            var m = master.Creds;
            var fileCreds = filePw.Creds;
            var r = isKdbx
                ? await shell.VaultCallAsync(a => a.ExportFile(path, m, fileCreds))
                : await shell.VaultCallAsync(a => a.ExportCsv(path, m));
            WipeAll();
            go.IsEnabled = true;
            if (!r.Ok) { error.Text = r.Error ?? Strings.ErrVaultUnexpected; return; }
            confirmWord.Clear();
            shell.ShowStatus(Strings.ExportDone);
            VaultUi.Info(w, Strings.ExportDoneAt(path), Strings.ExportDone);
        };
        w.Closed += (_, _) => WipeAll();
        w.ShowDialog();
    }
}

/// <summary>"Make an encrypted backup now" prompt shown right after the vault is created (it can be skipped).</summary>
internal static class CreatedPrompt
{
    public static void Show(Window? owner, ShellController shell)
    {
        var panel = new StackPanel();
        panel.Children.Add(DialogKit.Text(Strings.CreateBackupPrompt, bottom: 4));
        panel.Children.Add(DialogKit.Text(Strings.CreateWarningText, VaultUi.Brush(0xFF7F1D1D), 12, top: 8));
        var export = DialogKit.Btn(Strings.BtnBackupExportNow, null, 150);
        export.IsDefault = true;
        var skip = DialogKit.Btn(Strings.BtnSkipForNow, null, 110);
        skip.IsCancel = true;
        panel.Children.Add(DialogKit.ButtonRow(export, skip));
        var w = DialogKit.Create(shell, owner, Strings.CreateDoneTitle, 500, panel);
        var exportNow = false;
        export.Click += (_, _) => { exportNow = true; w.DialogResult = true; };
        w.ShowDialog();
        if (exportNow) ExportWindow.Show(owner, shell, startWithKdbx: true);
    }
}
