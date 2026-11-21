using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Templates;
using Avalonia.Layout;
using Avalonia.Media;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace MoonPassword.Windows.Views;

/// <summary>A status line in a vault window: an InfoBar that is hidden while empty.</summary>
internal sealed class StatusBar : InfoBar
{
    public StatusBar() => IsOpen = false;

    public void Say(string text, bool ok)
    {
        Severity = ok ? Severity.Success : Severity.Danger;
        Message = text;
        IsOpen = text.Length > 0;
    }

    public void Busy(string text)
    {
        Severity = Severity.Info;
        Message = text;
        IsOpen = true;
    }

    public void Hide() => IsOpen = false;
}

/// <summary>Backups & restore: list, back up now, verify, preview the differences, restore (docs/vault-api.md §3.5).</summary>
internal static class BackupsWindow
{
    /// <summary>Returns true when something was restored (the caller reloads the list).</summary>
    public static async Task<bool> ShowAsync(Window? owner, ShellController shell)
    {
        var restored = false;
        var list = new ListBox { MinHeight = 200 };
        list.ItemTemplate = Ui.Template<BackupRow>(row =>
        {
            var g = new Grid { ColumnDefinitions = new ColumnDefinitions("160,*,90") };
            g.Children.Add(new TextBlock { Text = row.TimeText });
            g.Children.Add(new TextBlock { Text = row.ReasonText, TextTrimming = TextTrimming.CharacterEllipsis }.With(t => Grid.SetColumn(t, 1)));
            g.Children.Add(new TextBlock { Text = row.SizeText, HorizontalAlignment = HorizontalAlignment.Right }.With(t => Grid.SetColumn(t, 2)));
            return g;
        });
        var header = new Grid { ColumnDefinitions = new ColumnDefinitions("160,*,90"), Margin = new Thickness(12, 0, 12, 4) };
        header.Children.Add(Ui.Text(Strings.ColTime, "caption", "tertiary"));
        header.Children.Add(Ui.Text(Strings.ColReason, "caption", "tertiary").With(t => Grid.SetColumn(t, 1)));
        header.Children.Add(Ui.Text(Strings.ColSize, "caption", "tertiary").With(t => { Grid.SetColumn(t, 2); t.HorizontalAlignment = HorizontalAlignment.Right; }));
        var status = new StatusBar { Margin = new Thickness(0, 8, 0, 0) };
        var empty = new EmptyState { Icon = IconKind.Package, Title = Strings.BackupsNone, IsVisible = false };

        var now = Ui.Button(AppIcons.Save, Strings.BtnBackupNow, () => { }, "primary");
        var verify = Ui.Button(AppIcons.ShieldCheck, Strings.BtnBackupVerify, () => { });
        var preview = Ui.Button(AppIcons.Of(IconKind.Eye), Strings.BtnBackupPreview, () => { });
        var close = DialogKit.Btn(Strings.Close, 90);

        var top = new StackPanel
        {
            Spacing = 8,
            Margin = new Thickness(0, 0, 0, 8),
            Children =
            {
                Ui.Text(Strings.BackupsTitle, "title"),
                Ui.Text(Strings.BackupsIntro, "caption", "tertiary"),
                new InfoBar { Severity = Severity.Info, Message = Strings.BackupsRestoreNote },
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Children = { now, verify, preview } },
                header,
            },
        };
        var bottom = new StackPanel { Children = { status, DialogKit.ButtonRow(close) } };
        var dock = new DockPanel();
        DockPanel.SetDock(top, Dock.Top);
        DockPanel.SetDock(bottom, Dock.Bottom);
        dock.Children.Add(top);
        dock.Children.Add(bottom);
        dock.Children.Add(new Panel { Children = { list, empty } });

        var w = DialogKit.Create(shell, Strings.BackupsTitle, 640, dock, height: 600);
        close.Click += (_, _) => w.Close();

        async Task LoadAsync()
        {
            var r = await shell.VaultCallAsync(a => a.BackupList());
            if (!r.Ok) { status.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
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
            if (!r.Ok) { status.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            status.Say(Strings.BackupCreated, true);
            await LoadAsync();
        };

        verify.Click += async (_, _) =>
        {
            if (Selected() is not { } row) { status.Say(Strings.BackupChoose, false); return; }
            var creds = await AskCredsDialog.AskAsync(w, shell, Strings.BackupVerifyTitle, Strings.BackupVerifyPrompt, Strings.LabelMasterPassword, allowEmpty: false);
            if (creds is null) return;
            status.Busy(Strings.Working);
            var r = await shell.VaultCallAsync(a => a.BackupVerify(row.Name, creds));
            if (!r.Ok) { status.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            var c = r.Value!;
            status.Say(Strings.BackupVerified(c.Entries, c.Groups, c.InTrash, StampText.Local(c.ModifiedAt)), true);
        };

        preview.Click += async (_, _) =>
        {
            if (Selected() is not { } row) { status.Say(Strings.BackupChoose, false); return; }
            var asked = await AskCredsDialog.AskAsync(w, shell, Strings.RestorePreviewTitle, Strings.BackupPreviewPrompt, Strings.BackupOtherPassword, allowEmpty: true);
            if (asked is null) return;
            VaultCreds? creds = asked.Password.Length == 0 ? null : asked; // empty = the current credentials
            status.Busy(Strings.Working);
            var r = await shell.VaultCallAsync(a => a.PreviewRestore(row.Name, creds));
            if (!r.Ok) { status.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            status.Hide();
            if (await RestorePreviewWindow.ShowAsync(w, shell, row.Name, creds, r.Value!))
            {
                restored = true;
                await LoadAsync();
            }
        };

        await w.ShowModalAsync(owner);
        return restored;
    }
}

/// <summary>The differences between a backup and the current vault, with the restore modes.</summary>
internal static class RestorePreviewWindow
{
    /// <summary>Returns true when the restore ran.</summary>
    public static async Task<bool> ShowAsync(Window? owner, ShellController shell, string backupName, VaultCreds? creds, RestorePreview preview)
    {
        var selection = new RestoreSelection(preview);
        var done = false;
        var body = new StackPanel { Spacing = 8 };
        body.Children.Add(Ui.Text(Strings.RestorePreviewTitle, "title"));
        body.Children.Add(Ui.Text(selection.Summary, "secondary"));
        if (selection.IsEmpty) body.Children.Add(new InfoBar { Severity = Severity.Success, Message = Strings.DiffNothing });

        var checks = new List<CheckBox>();
        foreach (var group in selection.Groups())
        {
            var items = new StackPanel { Spacing = 2 };
            foreach (var item in group.Items)
            {
                var label = (item.Title.Length > 0 ? item.Title : Strings.Untitled) + (item.ModifiedAt > 0 ? "  ·  " + StampText.Local(item.ModifiedAt) : "");
                if (group.Selectable)
                {
                    var id = item.Id;
                    var cb = new CheckBox { Content = label, IsChecked = selection.IsSelected(id) };
                    cb.IsCheckedChanged += (_, _) => selection.Set(id, cb.IsChecked == true);
                    checks.Add(cb);
                    items.Children.Add(cb);
                }
                else items.Children.Add(Ui.Text("• " + label, "secondary"));
            }
            var card = new Card { Header = group.Title + " (" + group.Items.Count + ")", Description = group.Hint, Content = items };
            card.Classes.Add("flat");
            card.Classes.Add("compact");
            body.Children.Add(card);
        }

        body.Children.Add(Ui.Text(Strings.RestoreChooseMode, "medium").With(t => t.Margin = new Thickness(0, 8, 0, 0)));
        var radios = new Dictionary<RestoreMode, RadioButton>();
        var hint = Ui.Text("", "caption", "tertiary");
        foreach (var mode in Enum.GetValues<RestoreMode>())
        {
            var rb = new RadioButton { Content = RestoreModes.Label(mode), IsChecked = mode == RestoreMode.Entries, GroupName = "restore-mode" };
            radios[mode] = rb;
            body.Children.Add(rb);
        }
        body.Children.Add(hint);
        var confirmBox = new TextBox { Width = 180 };
        var confirmField = new FormField { Label = Strings.RestoreReplaceConfirm, Content = confirmBox, IsVisible = false };
        body.Children.Add(confirmField);
        body.Children.Add(new InfoBar { Severity = Severity.Info, Message = Strings.BackupsRestoreNote });
        var error = new StatusBar();
        body.Children.Add(error);

        var restore = DialogKit.Btn(Strings.BtnRestore, 90, "primary");
        var cancel = DialogKit.Btn(Strings.BtnCancel, 90);
        body.Children.Add(DialogKit.ButtonRow(cancel, restore));
        var w = DialogKit.Create(shell, Strings.RestorePreviewTitle, 600, body, height: 680, scroll: true);
        cancel.Click += (_, _) => w.Close();

        RestoreMode Mode() => radios.FirstOrDefault(kv => kv.Value.IsChecked == true).Key;
        void Refresh()
        {
            var m = Mode();
            hint.Text = RestoreModes.Describe(m);
            var replace = m == RestoreMode.Replace;
            confirmField.IsVisible = replace;
            restore.Classes.Set("danger", replace);
            restore.Classes.Set("primary", !replace);
            foreach (var cb in checks) cb.IsEnabled = m == RestoreMode.Entries;
        }
        foreach (var rb in radios.Values) rb.IsCheckedChanged += (_, _) => Refresh();
        Refresh();

        restore.Click += async (_, _) =>
        {
            var m = Mode();
            error.Hide();
            confirmField.Error = null;
            if (!selection.CanRestore(m)) { error.Say(Strings.RestoreNothingSelected, false); return; }
            if (m == RestoreMode.Replace && !RestoreModes.ReplaceConfirmed(confirmBox.Text ?? "")) { confirmField.Error = Strings.ExportCsvNotConfirmed; return; }
            restore.IsEnabled = false;
            var ids = m == RestoreMode.Entries ? selection.SelectedIds : null;
            var r = await shell.VaultCallAsync(a => a.Restore(backupName, RestoreModes.Wire(m), ids, creds));
            restore.IsEnabled = true;
            if (!r.Ok) { error.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            done = true;
            shell.ShowStatus(Strings.RestoreDone + "：" + StatsText.Describe(r.Value!), StatusKind.Success);
            await VaultUi.InfoAsync(w, Strings.RestoreDone + "\n" + StatsText.Describe(r.Value!));
            w.Close();
        };
        await w.ShowModalAsync(owner);
        return done;
    }
}

/// <summary>Import KDBX, CSV or Bitwarden JSON.</summary>
internal static class ImportWindow
{
    public static async Task<bool> ShowAsync(Window? owner, ShellController shell)
    {
        var done = false;
        string? path = null;
        var kind = new SegmentedControl { Classes = { "block" } };
        kind.Items.Add(new SegmentedItem { Content = Strings.ImportKdbx });
        kind.Items.Add(new SegmentedItem { Content = Strings.ImportCsv });
        kind.Items.Add(new SegmentedItem { Content = Strings.ImportBitwarden });
        kind.SelectedIndex = 0;
        var pathBox = new TextBox { IsReadOnly = true, PlaceholderText = Strings.ImportNoFile };
        var pick = Ui.Button(AppIcons.Of(IconKind.Folder), Strings.ImportChooseFile, () => { });
        pick.Margin = new Thickness(8, 0, 0, 0);
        var fileRow = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
        fileRow.Children.Add(pathBox);
        Grid.SetColumn(pick, 1);
        fileRow.Children.Add(pick);
        var fileField = new FormField { Label = Strings.ImportFileLabel, Content = fileRow };
        var creds = new CredsBox { Caption = Strings.ImportKdbxPassword };
        var plaintextNote = new InfoBar { Severity = Severity.Warning, Message = Strings.ImportCsvNote, IsOpen = false };
        var error = new StatusBar();
        var go = DialogKit.Btn(Strings.BtnImportNow, 90, "primary");
        go.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, 90);

        var panel = new StackPanel { Spacing = 14, Children = { Ui.Text(Strings.ImportTitle, "title"), kind, fileField, creds, plaintextNote, error, DialogKit.ButtonRow(cancel, go) } };
        var w = DialogKit.Create(shell, Strings.ImportTitle, 540, panel);
        cancel.Click += (_, _) => w.Close();

        bool IsKdbx() => kind.SelectedIndex == 0;
        bool IsBitwarden() => kind.SelectedIndex == 2;
        kind.SelectionChanged += (_, _) =>
        {
            creds.IsVisible = IsKdbx();
            plaintextNote.IsOpen = !IsKdbx();
            plaintextNote.Message = IsBitwarden() ? Strings.ImportBitwardenNote : Strings.ImportCsvNote;
            path = null;
            pathBox.Text = null;
            fileField.Error = null;
        };
        pick.Click += async (_, _) =>
        {
            var format = kind.SelectedIndex;
            var fileType = IsKdbx() ? VaultUi.KdbxType : IsBitwarden() ? VaultUi.BitwardenType : VaultUi.CsvType;
            var chosen = await VaultUi.PickOpenFileAsync(w, Strings.ImportTitle, fileType);
            if (chosen is null || format != kind.SelectedIndex) return;
            path = chosen;
            pathBox.Text = chosen;
            fileField.Error = null;
        };
        go.Click += async (_, _) =>
        {
            error.Hide();
            if (path is null) { fileField.Error = Strings.ImportNoFile; return; }
            var file = path;
            var isKdbx = IsKdbx();
            if (isKdbx && creds.Password.Length == 0) { creds.ShowError(Strings.CreateEmptyPassword); return; }
            go.IsEnabled = false;
            string message;
            if (isKdbx)
            {
                var c = creds.Creds;
                var r = await shell.VaultCallAsync(a => a.ImportFile(file, c));
                creds.Clear(keyFileToo: true);
                go.IsEnabled = true;
                if (!r.Ok) { error.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
                message = Strings.ImportedKdbx + "\n" + StatsText.Describe(r.Value!);
            }
            else if (IsBitwarden())
            {
                var r = await shell.VaultCallAsync(a => a.ImportBitwardenJson(file));
                go.IsEnabled = true;
                if (!r.Ok) { error.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
                message = Strings.ImportedBitwarden(r.Value.Added, r.Value.Skipped, r.Value.PasskeysSkipped);
            }
            else
            {
                var r = await shell.VaultCallAsync(a => a.ImportCsv(file));
                go.IsEnabled = true;
                if (!r.Ok) { error.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
                message = Strings.ImportedCsv(r.Value.Added, r.Value.Skipped);
            }
            done = true;
            shell.ShowStatus(message.Split('\n')[0], StatusKind.Success);
            await VaultUi.InfoAsync(w, message);
            w.Close();
        };
        w.Closed += (_, _) => creds.Clear(keyFileToo: true);
        await w.ShowModalAsync(owner);
        return done;
    }
}

/// <summary>Export a KDBX copy (new password) or a plain-text CSV (red warning). Both re-ask the master password.</summary>
internal static class ExportWindow
{
    public static async Task ShowAsync(Window? owner, ShellController shell, bool startWithKdbx = true)
    {
        var kind = new SegmentedControl { Classes = { "block" } };
        kind.Items.Add(new SegmentedItem { Content = Strings.ExportKdbx });
        kind.Items.Add(new SegmentedItem { Content = Strings.ExportCsv });
        kind.SelectedIndex = startWithKdbx ? 0 : 1;
        var master = new CredsBox { Caption = Strings.ExportMasterPrompt };

        var filePw = new CredsBox { Caption = Strings.ExportFilePassword };
        var filePwConfirm = new CredsBox { Caption = Strings.LabelConfirmPassword, ShowKeyFile = false };
        var kdbxPane = new StackPanel { Spacing = 10, Children = { Ui.Text(Strings.ExportFilePasswordNote, "caption", "tertiary"), filePw, filePwConfirm } };

        var confirmWord = new TextBox { Width = 180 };
        var confirmField = new FormField { Label = Strings.ExportCsvConfirmPrompt, Content = confirmWord };
        var csvPane = new StackPanel
        {
            Spacing = 10,
            Children = { new InfoBar { Severity = Severity.Danger, Title = Strings.ExportCsvWarningTitle, Message = Strings.ExportCsvWarning }, confirmField },
        };

        var error = new StatusBar();
        var go = DialogKit.Btn(Strings.BtnExportNow, 90, "primary");
        var cancel = DialogKit.Btn(Strings.Close, 90);
        var panel = new StackPanel { Spacing = 14, Children = { Ui.Text(Strings.ExportTitle, "title"), kind, master, new Separator(), kdbxPane, csvPane, error, DialogKit.ButtonRow(cancel, go) } };
        var w = DialogKit.Create(shell, Strings.ExportTitle, 560, panel, height: 680, scroll: true);
        cancel.Click += (_, _) => w.Close();

        bool IsKdbx() => kind.SelectedIndex == 0;
        void Mode()
        {
            kdbxPane.IsVisible = IsKdbx();
            csvPane.IsVisible = !IsKdbx();
            go.Classes.Set("danger", !IsKdbx());
            go.Classes.Set("primary", IsKdbx());
            error.Hide();
        }
        kind.SelectionChanged += (_, _) => Mode();
        Mode();

        void WipeAll()
        {
            master.Clear(keyFileToo: true);
            filePw.Clear(keyFileToo: true);
            filePwConfirm.Clear();
        }

        go.Click += async (_, _) =>
        {
            error.Hide();
            var isKdbx = IsKdbx();
            var problem = isKdbx
                ? ExportForm.CheckKdbx(master.Password, filePw.Password, filePwConfirm.Password)
                : ExportForm.CheckCsv(master.Password) ?? (ExportForm.CsvConfirmed(confirmWord.Text ?? "") ? null : Strings.ExportCsvNotConfirmed);
            if (problem is not null) { error.Say(problem, false); return; }
            // A last, separate warning for the plain-text file ("取消" is the safe answer).
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
            if (!r.Ok) { error.Say(r.Error ?? Strings.ErrVaultUnexpected, false); return; }
            confirmWord.Text = "";
            shell.ShowStatus(Strings.ExportDone, StatusKind.Success);
            await VaultUi.InfoAsync(w, Strings.ExportDoneAt(path), Strings.ExportDone);
        };
        w.Closed += (_, _) => WipeAll();
        await w.ShowModalAsync(owner);
    }
}

/// <summary>"Make an encrypted backup now" prompt shown right after the vault is created (it can be skipped).</summary>
internal static class CreatedPrompt
{
    public static async Task ShowAsync(Window? owner, ShellController shell)
    {
        var export = DialogKit.Btn(Strings.BtnBackupExportNow, 150, "primary");
        export.IsDefault = true;
        var skip = DialogKit.Btn(Strings.BtnSkipForNow, 110);
        var panel = new StackPanel
        {
            Spacing = 12,
            Children =
            {
                Ui.Text(Strings.CreateDoneTitle, "title"),
                Ui.Text(Strings.CreateBackupPrompt, "secondary"),
                new InfoBar { Severity = Severity.Danger, Message = Strings.CreateWarningText },
                DialogKit.ButtonRow(skip, export),
            },
        };
        var w = DialogKit.Create(shell, Strings.CreateDoneTitle, 520, panel);
        var exportNow = false;
        export.Click += (_, _) => { exportNow = true; w.Close(); };
        skip.Click += (_, _) => w.Close();
        await w.ShowModalAsync(owner);
        if (exportNow) await ExportWindow.ShowAsync(owner, shell, startWithKdbx: true);
    }
}
