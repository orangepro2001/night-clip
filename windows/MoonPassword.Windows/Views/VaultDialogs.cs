using System.Globalization;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using MoonPassword.Windows.Logic;
using MoonPassword.Windows.Services;
using Lumen.UI.Controls;
using Lumen.UI.Icons;

namespace MoonPassword.Windows.Views;

/// <summary>Asks for a master password (and key file). An empty password can mean "the current credentials" when the caller allows it.</summary>
internal static class AskCredsDialog
{
    /// <summary>Returns the credentials, or null when cancelled. The password box is cleared before returning.</summary>
    public static async Task<VaultCreds?> AskAsync(Window? owner, ShellController shell, string title, string prompt, string caption, bool allowEmpty)
    {
        var box = new CredsBox { Caption = caption };
        VaultCreds? result = null;
        var ok = DialogKit.Btn(Strings.BtnOk, 90, "primary");
        ok.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, 90);
        var panel = new StackPanel { Spacing = 12, Children = { Ui.Text(title, "subtitle"), Ui.Text(prompt, "secondary"), box, DialogKit.ButtonRow(cancel, ok) } };
        var w = DialogKit.Create(shell, title, 460, panel);
        void Submit()
        {
            if (box.Password.Length == 0 && !allowEmpty) { box.ShowError(Strings.ExportNeedMaster); return; }
            result = box.Creds;
            w.Close();
        }
        ok.Click += (_, _) => Submit();
        cancel.Click += (_, _) => w.Close();
        box.Submit += Submit;
        w.Opened += (_, _) => box.FocusPassword();
        await w.ShowModalAsync(owner);
        box.Clear(keyFileToo: true); // the returned creds keep their own reference to the key file bytes
        return result;
    }
}

/// <summary>The password health report: weak, reused, old and expired entries; "查看" jumps to the entry.</summary>
internal static class HealthWindow
{
    /// <summary>Returns the id of the entry to show, or null.</summary>
    public static async Task<string?> PickAsync(Window? owner, ShellController shell)
    {
        string? picked = null;
        var summary = new InfoBar { Severity = Severity.Info, Message = Strings.VaultLoading };
        var list = new StackPanel { Spacing = 10 };
        var close = DialogKit.Btn(Strings.Close, 90);
        var buttons = DialogKit.ButtonRow(close);
        var head = new StackPanel { Spacing = 10, Margin = new Thickness(0, 0, 0, 10), Children = { Ui.Text(Strings.HealthTitle, "title"), Ui.Text(Strings.HealthNote, "caption", "tertiary"), summary } };
        var dock = new DockPanel();
        DockPanel.SetDock(buttons, Dock.Bottom);
        DockPanel.SetDock(head, Dock.Top);
        dock.Children.Add(buttons);
        dock.Children.Add(head);
        dock.Children.Add(new ScrollViewer { Content = list });

        var w = DialogKit.Create(shell, Strings.HealthTitle, 600, dock, height: 600);
        close.Click += (_, _) => w.Close();
        w.Opened += async (_, _) =>
        {
            var r = await shell.VaultCallAsync(a => a.Health());
            if (!r.Ok)
            {
                summary.Severity = Severity.Danger;
                summary.Message = r.Error ?? Strings.ErrVaultUnexpected;
                return;
            }
            var report = r.Value!;
            var groups = HealthView.Groups(report).ToList();
            summary.Severity = groups.Any(g => g.Items.Count > 0) ? Severity.Warning : Severity.Success;
            summary.Message = HealthView.Summary(report) + (report.WithoutTotp > 0 ? "\n" + Strings.HealthWithoutTotp(report.WithoutTotp) : "");
            foreach (var group in groups)
            {
                var rows = new StackPanel { Spacing = 0 };
                foreach (var item in group.Items)
                {
                    var id = item.Id;
                    var line = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto"), Margin = new Thickness(0, 3) };
                    line.Children.Add(new TextBlock
                    {
                        Text = item.Info.Length > 0 ? item.Title + "  ·  " + item.Info : item.Title,
                        VerticalAlignment = VerticalAlignment.Center, TextTrimming = TextTrimming.CharacterEllipsis,
                    });
                    var view = Ui.TextButton(Strings.BtnViewEntry, () => { picked = id; w.Close(); }, "ghost", "sm");
                    Grid.SetColumn(view, 1);
                    line.Children.Add(view);
                    rows.Children.Add(line);
                }
                var card = new Card { Header = group.Title + " (" + group.Items.Count + ")", Description = group.Hint, Content = rows };
                card.Classes.Add("flat");
                card.Classes.Add("compact");
                list.Children.Add(card);
            }
        };
        await w.ShowModalAsync(owner);
        return picked;
    }
}

/// <summary>Auto-lock time, clipboard clearing and the screen-capture switch.</summary>
internal static class VaultSettingsWindow
{
    public static async Task ShowAsync(Window? owner, ShellController shell)
    {
        var inv = CultureInfo.InvariantCulture;
        var autolock = new TextBox { Width = 120 };
        var clip = new TextBox { Width = 120 };
        var autolockField = new FormField { Label = Strings.VaultAutolockLabel, Content = autolock };
        var clipField = new FormField { Label = Strings.VaultClipClearLabel, Content = clip };
        var capture = new ToggleSwitch { IsChecked = shell.PreventCapture };
        var kdf = Ui.Text("", "caption", "tertiary");
        var result = new InfoBar { IsOpen = false };

        var save = DialogKit.Btn(Strings.BtnSave, 90, "primary");
        save.IsDefault = true;
        var close = DialogKit.Btn(Strings.Close, 90);
        var panel = new StackPanel
        {
            Spacing = 14,
            Children =
            {
                Ui.Text(Strings.VaultSettingsTitle, "title"),
                new StackPanel { Orientation = Orientation.Horizontal, Spacing = 20, Children = { autolockField, clipField } },
                SettingRows.SettingRow(Strings.VaultPreventCapture, Strings.VaultPreventCaptureNote, capture),
                kdf, result,
                DialogKit.ButtonRow(close, save),
            },
        };
        var w = DialogKit.Create(shell, Strings.VaultSettingsTitle, 520, panel);

        w.Opened += async (_, _) =>
        {
            var st = await shell.VaultCallAsync(a => a.Status());
            if (!st.Ok)
            {
                result.Severity = Severity.Danger;
                result.Message = st.Error ?? "";
                result.IsOpen = true;
                return;
            }
            autolock.Text = st.Value!.AutolockS.ToString(inv);
            clip.Text = st.Value.ClipClearS.ToString(inv);
            if (st.Value.Kdf.Length > 0) kdf.Text = Strings.VaultKdfInfo(st.Value.Kdf);
        };
        save.Click += async (_, _) =>
        {
            var (auto, autoError) = VaultSettingsForm.ParseAutolock(autolock.Text ?? "");
            var (clear, clearError) = VaultSettingsForm.ParseClipClear(clip.Text ?? "");
            autolockField.Error = autoError;
            clipField.Error = clearError;
            if (auto is null || clear is null) return;
            var e1 = await shell.CallAsync(a => a.SettingsSet("vault_autolock_s", auto.Value.ToString(inv)));
            var e2 = e1 ?? await shell.CallAsync(a => a.SettingsSet("vault_clip_clear_s", clear.Value.ToString(inv)));
            shell.SetPreventCapture(capture.IsChecked == true);
            DisplayAffinity.Set(w, shell.PreventCapture); // this window follows the new switch right away
            result.Severity = e2 is null ? Severity.Success : Severity.Danger;
            result.Message = e2 ?? Strings.VaultSettingsSaved;
            result.IsOpen = true;
        };
        close.Click += (_, _) => w.Close();
        await w.ShowModalAsync(owner);
    }
}

/// <summary>Change the master password: the current one, the new one twice, a live strength bar.</summary>
internal static class ChangeMasterWindow
{
    public static async Task ShowAsync(Window? owner, ShellController shell)
    {
        var current = new CredsBox { Caption = Strings.ChangeCurrent };
        var next = new CredsBox { Caption = Strings.ChangeNew };
        var confirm = new CredsBox { Caption = Strings.LabelConfirmPassword, ShowKeyFile = false };
        var strength = new StrengthBar();
        var weak = Ui.Text(Strings.CreateWeakWarning, "caption", "warning");
        weak.IsVisible = false;
        var error = new InfoBar { Severity = Severity.Danger, IsOpen = false };
        var ok = DialogKit.Btn(Strings.BtnChange, 90, "primary");
        ok.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, 90);
        var panel = new StackPanel
        {
            Spacing = 12,
            Children =
            {
                Ui.Text(Strings.ChangeMasterTitle, "title"),
                new InfoBar { Severity = Severity.Warning, Message = Strings.ChangeNote },
                current, new Separator(), next, strength, weak, confirm, error,
                DialogKit.ButtonRow(cancel, ok),
            },
        };
        var w = DialogKit.Create(shell, Strings.ChangeMasterTitle, 520, panel);

        var generation = 0;
        var timer = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(250) };
        timer.Tick += async (_, _) =>
        {
            timer.Stop();
            var gen = ++generation;
            var pw = next.Password;
            if (pw.Length == 0) { strength.Clear(); weak.IsVisible = false; return; }
            var r = await shell.VaultCallAsync(a => a.Strength(pw));
            if (gen != generation || !r.Ok) return;
            strength.Set(r.Value!.Label, r.Value.Bits);
            weak.IsVisible = StrengthDisplay.IsWeak(r.Value.Label);
        };
        next.PasswordChanged += () => { timer.Stop(); timer.Start(); };
        current.Submit += () => next.FocusPassword();
        next.Submit += () => confirm.FocusPassword();

        var busy = false;
        async Task ChangeAsync()
        {
            if (busy) return;
            var problem = ChangeMasterForm.Check(current.Password, next.Password, confirm.Password);
            if (problem is not null) { error.Message = problem; error.IsOpen = true; return; }
            if (!await VaultUi.ConfirmAsync(w, Strings.ChangeNote, Strings.ChangeMasterTitle, Strings.BtnChange, warning: true)) return;
            busy = true;
            ok.IsEnabled = false;
            error.IsOpen = false;
            var cur = current.Creds;
            var nw = next.Creds;
            var r = await shell.VaultCallAsync(a => a.ChangeMaster(cur, nw));
            current.Clear(true);
            next.Clear(true);
            confirm.Clear();
            busy = false;
            ok.IsEnabled = true;
            if (!r.Ok) { error.Message = r.Error ?? Strings.ErrVaultUnexpected; error.IsOpen = true; return; }
            shell.ShowStatus(Strings.ChangeDone, StatusKind.Success);
            await VaultUi.InfoAsync(w, Strings.ChangeDone);
            w.Close();
        }
        ok.Click += async (_, _) => await ChangeAsync();
        confirm.Submit += () => _ = ChangeAsync();
        cancel.Click += (_, _) => w.Close();
        w.Opened += (_, _) => current.FocusPassword();
        w.Closed += (_, _) =>
        {
            timer.Stop();
            current.Clear(true);
            next.Clear(true);
            confirm.Clear();
        };
        await w.ShowModalAsync(owner);
    }
}
