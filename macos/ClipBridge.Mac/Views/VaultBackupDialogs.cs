using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Templates;
using Avalonia.Layout;
using Avalonia.Media;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

/// <summary>Backups & restore: list, back up now, verify, preview the differences, restore (docs/vault-api.md §3.5).</summary>
internal static class BackupsWindow
{
    /// <summary>Returns true when something was restored (the caller reloads the list).</summary>
    public static async Task<bool> ShowAsync(Window? owner, MacController shell)
    {
        var restored = false;
        var list = new ListBox { MinHeight = 200 };
        list.ItemTemplate = new FuncDataTemplate<BackupRow>((row, _) =>
        {
            var g = new Grid { ColumnDefinitions = new ColumnDefinitions("150,*,90") };
            g.Children.Add(new TextBlock { Text = row.TimeText });
            g.Children.Add(new TextBlock { Text = row.ReasonText, TextTrimming = TextTrimming.CharacterEllipsis }.With(t => Grid.SetColumn(t, 1)));
            g.Children.Add(new TextBlock { Text = row.SizeText, HorizontalAlignment = HorizontalAlignment.Right }.With(t => Grid.SetColumn(t, 2)));
            return g;
        });
        var header = new Grid { ColumnDefinitions = new ColumnDefinitions("150,*,90"), Margin = new Thickness(12, 0, 12, 4) };
        header.Children.Add(DialogKit.Text(Strings.ColTime, VaultUi.MutedBrush, 12));
        header.Children.Add(DialogKit.Text(Strings.ColReason, VaultUi.MutedBrush, 12).With(t => Grid.SetColumn(t, 1)));
        header.Children.Add(DialogKit.Text(Strings.ColSize, VaultUi.MutedBrush, 12).With(t => { Grid.SetColumn(t, 2); t.HorizontalAlignment = HorizontalAlignment.Right; }));
        var status = DialogKit.Text("", top: 8);
        var empty = DialogKit.Text(Strings.BackupsNone, VaultUi.MutedBrush, top: 4);
        empty.IsVisible = false;

        var now = DialogKit.Btn(Strings.BtnBackupNow);
        var verify = DialogKit.Btn(Strings.BtnBackupVerify);
        var preview = DialogKit.Btn(Strings.BtnBackupPreview);
        var close = DialogKit.Btn(Strings.Close, 90);

        var top = Ui.Column(0,
            DialogKit.Text(Strings.BackupsIntro, VaultUi.MutedBrush, 12, bottom: 4),
            DialogKit.Text(Strings.BackupsRestoreNote, VaultUi.NoteBrush, 12, bottom: 8),
            Ui.Row(now, verify, preview).With(r => r.Margin = new Thickness(0, 0, 0, 8)),
            header);
        var bottom = Ui.Column(0, status, DialogKit.ButtonRow(close));
        var dock = new DockPanel();
        DockPanel.SetDock(top, Dock.Top);
        DockPanel.SetDock(bottom, Dock.Bottom);
        dock.Children.Add(top);
        dock.Children.Add(bottom);
        dock.Children.Add(new Panel { Children = { list, empty } });

        var w = DialogKit.Create(shell, Strings.BackupsTitle, 600, dock, height: 560);
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
            empty.IsVisible = rows.Count == 0;
        }

        BackupRow? Selected() => list.SelectedItem as BackupRow;

        w.Opened += async (_, _) => await LoadAsync();

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
            var creds = await AskCredsDialog.AskAsync(w, shell, Strings.BackupVerifyTitle, Strings.BackupVerifyPrompt, Strings.LabelMasterPassword, allowEmpty: false);
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
            var asked = await AskCredsDialog.AskAsync(w, shell, Strings.RestorePreviewTitle, Strings.BackupPreviewPrompt, Strings.BackupOtherPassword, allowEmpty: true);
            if (asked is null) return;
            VaultCreds? creds = asked.Password.Length == 0 ? null : asked; // empty = the current credentials
            Say(Strings.Working, true);
            var r = await shell.VaultCallAsync(a => a.PreviewRestore(row.Name, creds));
            if (!r.Ok) { Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            Say("", true);
            if (await RestorePreviewWindow.ShowAsync(w, shell, row.Name, creds, r.Value!))
            {
                restored = true;
                await LoadAsync();
            }
        };

        await VaultUi.ShowModalAsync(w, owner);
        return restored;
    }
}

/// <summary>The differences between a backup and the current vault, with the restore modes.</summary>
internal static class RestorePreviewWindow
{
    /// <summary>Returns true when the restore ran.</summary>
    public static async Task<bool> ShowAsync(Window? owner, MacController shell, string backupName, VaultCreds? creds, RestorePreview preview)
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
                    cb.IsCheckedChanged += (_, _) => selection.Set(id, cb.IsChecked == true);
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
            var rb = new RadioButton { Content = RestoreModes.Label(mode), IsChecked = mode == RestoreMode.Entries, GroupName = "restore-mode", Margin = new Thickness(0, 2, 0, 2) };
            radios[mode] = rb;
            body.Children.Add(rb);
        }
        body.Children.Add(hint);
        var confirmText = DialogKit.Text(Strings.RestoreReplaceConfirm, VaultUi.ErrorBrush, top: 8);
        var confirmBox = new TextBox { Width = 160, HorizontalAlignment = HorizontalAlignment.Left };
        confirmText.IsVisible = false;
        confirmBox.IsVisible = false;
        body.Children.Add(confirmText);
        body.Children.Add(confirmBox);
        body.Children.Add(DialogKit.Text(Strings.BackupsRestoreNote, VaultUi.NoteBrush, 12, top: 12));
        var error = DialogKit.Text("", VaultUi.ErrorBrush, top: 8);
        body.Children.Add(error);

        var restore = DialogKit.Btn(Strings.BtnRestore, 90);
        var cancel = DialogKit.Btn(Strings.BtnCancel, 90);
        body.Children.Add(DialogKit.ButtonRow(cancel, restore));
        var w = DialogKit.Create(shell, Strings.RestorePreviewTitle, 560, body, height: 640, scroll: true);
        cancel.Click += (_, _) => w.Close();

        RestoreMode Mode() => radios.FirstOrDefault(kv => kv.Value.IsChecked == true).Key;
        void Refresh()
        {
            var m = Mode();
            hint.Text = RestoreModes.Describe(m);
            var replace = m == RestoreMode.Replace;
            confirmText.IsVisible = replace;
            confirmBox.IsVisible = replace;
            foreach (var cb in checks) cb.IsEnabled = m == RestoreMode.Entries;
        }
        foreach (var rb in radios.Values) rb.IsCheckedChanged += (_, _) => Refresh();
        Refresh();

        restore.Click += async (_, _) =>
        {
            var m = Mode();
            error.Text = "";
            if (!selection.CanRestore(m)) { error.Text = Strings.RestoreNothingSelected; return; }
            if (m == RestoreMode.Replace && !RestoreModes.ReplaceConfirmed(confirmBox.Text ?? "")) { error.Text = Strings.ExportCsvNotConfirmed; return; }
            restore.IsEnabled = false;
            var ids = m == RestoreMode.Entries ? selection.SelectedIds : null;
            var r = await shell.VaultCallAsync(a => a.Restore(backupName, RestoreModes.Wire(m), ids, creds));
            restore.IsEnabled = true;
            if (!r.Ok) { error.Text = r.Error ?? Strings.ErrVaultUnexpected; return; }
            done = true;
            shell.ShowStatus(Strings.RestoreDone + "：" + StatsText.Describe(r.Value!));
            await VaultUi.InfoAsync(w, Strings.RestoreDone + "\n" + StatsText.Describe(r.Value!));
            w.Close();
        };
        await VaultUi.ShowModalAsync(w, owner);
        return done;
    }
}

/// <summary>Import a KDBX file (with its password) or a CSV file.</summary>
internal static class ImportWindow
{
    public static async Task<bool> ShowAsync(Window? owner, MacController shell)
    {
        var done = false;
        string? path = null;
        var kdbx = new RadioButton { Content = Strings.ImportKdbx, IsChecked = true, GroupName = "import-kind" };
        var csv = new RadioButton { Content = Strings.ImportCsv, GroupName = "import-kind", Margin = new Thickness(0, 4, 0, 0) };
        var pathText = DialogKit.Text(Strings.ImportNoFile, VaultUi.MutedBrush, top: 4);
        var pick = DialogKit.Btn(Strings.ImportChooseFile);
        pick.Margin = new Thickness(0, 8, 0, 0);
        var creds = new CredsBox { Caption = Strings.ImportKdbxPassword, Margin = new Thickness(0, 10, 0, 0) };
        var csvNote = DialogKit.Text(Strings.ImportCsvNote, VaultUi.WarnBrush, 12, top: 10);
        csvNote.IsVisible = false;
        var error = DialogKit.Text("", VaultUi.ErrorBrush, top: 8);
        var go = DialogKit.Btn(Strings.BtnImportNow, 90);
        go.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, 90);

        var panel = Ui.Column(0, kdbx, csv, pick, pathText, creds, csvNote, error, DialogKit.ButtonRow(cancel, go));
        var w = DialogKit.Create(shell, Strings.ImportTitle, 520, panel);
        cancel.Click += (_, _) => w.Close();

        void Mode()
        {
            var isKdbx = kdbx.IsChecked == true;
            creds.IsVisible = isKdbx;
            csvNote.IsVisible = !isKdbx;
        }
        kdbx.IsCheckedChanged += (_, _) => Mode();
        csv.IsCheckedChanged += (_, _) => Mode();
        pick.Click += async (_, _) =>
        {
            var chosen = await VaultUi.PickOpenFileAsync(w, Strings.ImportTitle, kdbx.IsChecked == true ? VaultUi.KdbxType : VaultUi.CsvType);
            if (chosen is null) return;
            path = chosen;
            pathText.Text = chosen;
        };
        go.Click += async (_, _) =>
        {
            error.Text = "";
            if (path is null) { error.Text = Strings.ImportNoFile; return; }
            var file = path;
            var isKdbx = kdbx.IsChecked == true;
            if (isKdbx && creds.Password.Length == 0) { error.Text = Strings.CreateEmptyPassword; return; }
            go.IsEnabled = false;
            string message;
            if (isKdbx)
            {
                var c = creds.Creds;
                var r = await shell.VaultCallAsync(a => a.ImportFile(file, c));
                creds.Clear(keyFileToo: true);
                go.IsEnabled = true;
                if (!r.Ok) { error.Text = r.Error ?? Strings.ErrVaultUnexpected; return; }
                message = Strings.ImportedKdbx + "\n" + StatsText.Describe(r.Value!);
            }
            else
            {
                var r = await shell.VaultCallAsync(a => a.ImportCsv(file));
                go.IsEnabled = true;
                if (!r.Ok) { error.Text = r.Error ?? Strings.ErrVaultUnexpected; return; }
                message = Strings.ImportedCsv(r.Value.Added, r.Value.Skipped);
            }
            done = true;
            shell.ShowStatus(message.Split('\n')[0]);
            await VaultUi.InfoAsync(w, message);
            w.Close();
        };
        w.Closed += (_, _) => creds.Clear(keyFileToo: true);
        await VaultUi.ShowModalAsync(w, owner);
        return done;
    }
}

/// <summary>Export a KDBX copy (new password) or a plain-text CSV (red warning). Both re-ask the master password.</summary>
internal static class ExportWindow
{
    public static async Task ShowAsync(Window? owner, MacController shell, bool startWithKdbx = true)
    {
        var kdbx = new RadioButton { Content = Strings.ExportKdbx, IsChecked = startWithKdbx, GroupName = "export-kind" };
        var csv = new RadioButton { Content = Strings.ExportCsv, IsChecked = !startWithKdbx, GroupName = "export-kind", Margin = new Thickness(0, 4, 0, 0) };
        var master = new CredsBox { Caption = Strings.ExportMasterPrompt, Margin = new Thickness(0, 12, 0, 0) };

        var filePw = new CredsBox { Caption = Strings.ExportFilePassword };
        var filePwConfirm = new CredsBox { Caption = Strings.LabelConfirmPassword, ShowKeyFile = false, Margin = new Thickness(0, 8, 0, 0) };
        var kdbxPane = Ui.Column(0, DialogKit.Text(Strings.ExportFilePasswordNote, VaultUi.MutedBrush, 12, bottom: 6), filePw, filePwConfirm)
            .With(p => p.Margin = new Thickness(0, 12, 0, 0));

        var warn = new Border
        {
            Background = VaultUi.Brush(0x1AC62828), BorderBrush = VaultUi.ErrorBrush, BorderThickness = new Thickness(2),
            CornerRadius = new CornerRadius(6), Padding = new Thickness(12, 8),
            Child = Ui.Column(4,
                DialogKit.Text(Strings.ExportCsvWarningTitle, VaultUi.ErrorBrush, 15, bold: true),
                DialogKit.Text(Strings.ExportCsvWarning, VaultUi.ErrorBrush)),
        };
        var confirmWord = new TextBox { Width = 160, HorizontalAlignment = HorizontalAlignment.Left };
        var csvPane = Ui.Column(0, warn, DialogKit.Text(Strings.ExportCsvConfirmPrompt, VaultUi.ErrorBrush, top: 10, bottom: 4), confirmWord)
            .With(p => p.Margin = new Thickness(0, 12, 0, 0));

        var error = DialogKit.Text("", VaultUi.ErrorBrush, top: 10);
        var go = DialogKit.Btn(Strings.BtnExportNow, 90);
        var cancel = DialogKit.Btn(Strings.Close, 90);
        var panel = Ui.Column(0, kdbx, csv, master, kdbxPane, csvPane, error, DialogKit.ButtonRow(cancel, go));
        var w = DialogKit.Create(shell, Strings.ExportTitle, 540, panel, height: 640, scroll: true);
        cancel.Click += (_, _) => w.Close();

        void Mode()
        {
            var isKdbx = kdbx.IsChecked == true;
            kdbxPane.IsVisible = isKdbx;
            csvPane.IsVisible = !isKdbx;
            error.Text = "";
        }
        kdbx.IsCheckedChanged += (_, _) => Mode();
        csv.IsCheckedChanged += (_, _) => Mode();
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
                : ExportForm.CheckCsv(master.Password) ?? (ExportForm.CsvConfirmed(confirmWord.Text ?? "") ? null : Strings.ExportCsvNotConfirmed);
            if (problem is not null) { error.Text = problem; return; }
            // A last, separate warning for the plain-text file ("取消" is the default).
            if (!isKdbx && !await VaultUi.ConfirmAsync(w, Strings.ExportCsvWarning + "\n\n" + Strings.ExportCsvAreYouSure, Strings.ExportCsvWarningTitle, Strings.BtnExportNow, warning: true)) return;

            var path = await VaultUi.PickSaveFileAsync(w, Strings.ExportTitle, ExportForm.DefaultFileName(isKdbx ? "kdbx" : "csv", DateTimeOffset.Now),
                isKdbx ? VaultUi.KdbxType : VaultUi.CsvType);
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
            confirmWord.Text = "";
            shell.ShowStatus(Strings.ExportDone);
            await VaultUi.InfoAsync(w, Strings.ExportDoneAt(path), Strings.ExportDone);
        };
        w.Closed += (_, _) => WipeAll();
        await VaultUi.ShowModalAsync(w, owner);
    }
}

/// <summary>"Make an encrypted backup now" prompt shown right after the vault is created (it can be skipped).</summary>
internal static class CreatedPrompt
{
    public static async Task ShowAsync(Window? owner, MacController shell)
    {
        var export = DialogKit.Btn(Strings.BtnBackupExportNow, 150);
        export.IsDefault = true;
        var skip = DialogKit.Btn(Strings.BtnSkipForNow, 110);
        var panel = Ui.Column(0,
            DialogKit.Text(Strings.CreateBackupPrompt, bottom: 4),
            DialogKit.Text(Strings.CreateWarningText, VaultUi.ErrorBrush, 12, top: 8),
            DialogKit.ButtonRow(skip, export));
        var w = DialogKit.Create(shell, Strings.CreateDoneTitle, 500, panel);
        var exportNow = false;
        export.Click += (_, _) => { exportNow = true; w.Close(); };
        skip.Click += (_, _) => w.Close();
        await VaultUi.ShowModalAsync(w, owner);
        if (exportNow) await ExportWindow.ShowAsync(owner, shell, startWithKdbx: true);
    }
}
