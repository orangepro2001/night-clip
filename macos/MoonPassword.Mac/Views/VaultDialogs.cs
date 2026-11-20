using System.Globalization;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using MoonPassword.Windows;
using MoonPassword.Windows.Logic;

namespace MoonPassword.Mac.Views;

/// <summary>Asks for a master password (and key file). An empty password can mean "the current credentials" when the caller allows it.</summary>
internal static class AskCredsDialog
{
    /// <summary>Returns the credentials, or null when cancelled. The password box is cleared before returning.</summary>
    public static async Task<VaultCreds?> AskAsync(Window? owner, MacController shell, string title, string prompt, string caption, bool allowEmpty)
    {
        var box = new CredsBox { Caption = caption };
        VaultCreds? result = null;
        var ok = DialogKit.Btn(Strings.BtnOk, 90);
        ok.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, 90);
        var panel = Ui.Column(0, DialogKit.Text(prompt, bottom: 10), box, DialogKit.ButtonRow(cancel, ok));
        var w = DialogKit.Create(shell, title, 440, panel);
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
        await VaultUi.ShowModalAsync(w, owner);
        box.Clear(keyFileToo: true); // the returned creds keep their own reference to the key file bytes
        return result;
    }
}

/// <summary>The password health report: weak, reused, old and expired entries; "查看" jumps to the entry.</summary>
internal static class HealthWindow
{
    /// <summary>Returns the id of the entry to show, or null.</summary>
    public static async Task<string?> PickAsync(Window? owner, MacController shell)
    {
        string? picked = null;
        var summary = DialogKit.Text(Strings.VaultLoading, top: 4, bottom: 8);
        var list = new StackPanel { Spacing = 2 };
        var close = DialogKit.Btn(Strings.Close, 90);
        var buttons = DialogKit.ButtonRow(close);
        var head = Ui.Column(0, DialogKit.Text(Strings.HealthNote, VaultUi.MutedBrush, 12), summary);
        var dock = new DockPanel();
        DockPanel.SetDock(buttons, Dock.Bottom);
        DockPanel.SetDock(head, Dock.Top);
        dock.Children.Add(buttons);
        dock.Children.Add(head);
        dock.Children.Add(new ScrollViewer { Content = list });

        var w = DialogKit.Create(shell, Strings.HealthTitle, 560, dock, height: 560);
        close.Click += (_, _) => w.Close();
        w.Opened += async (_, _) =>
        {
            var r = await shell.VaultCallAsync(a => a.Health());
            if (!r.Ok) { summary.Text = r.Error ?? Strings.ErrVaultUnexpected; summary.Foreground = VaultUi.ErrorBrush; return; }
            var report = r.Value!;
            summary.Text = HealthView.Summary(report) + (report.WithoutTotp > 0 ? "\n" + Strings.HealthWithoutTotp(report.WithoutTotp) : "");
            foreach (var group in HealthView.Groups(report))
            {
                list.Children.Add(DialogKit.Text(group.Title + " (" + group.Items.Count + ")", bold: true, size: 15, top: 10));
                list.Children.Add(DialogKit.Text(group.Hint, VaultUi.MutedBrush, 12, bottom: 4));
                foreach (var item in group.Items)
                {
                    var line = new DockPanel();
                    var id = item.Id;
                    var view = DialogKit.Btn(Strings.BtnViewEntry);
                    view.Click += (_, _) => { picked = id; w.Close(); };
                    DockPanel.SetDock(view, Dock.Right);
                    line.Children.Add(view);
                    line.Children.Add(new TextBlock
                    {
                        Text = item.Info.Length > 0 ? item.Title + "  ·  " + item.Info : item.Title,
                        VerticalAlignment = VerticalAlignment.Center, TextTrimming = TextTrimming.CharacterEllipsis,
                    });
                    list.Children.Add(line);
                }
            }
        };
        await VaultUi.ShowModalAsync(w, owner);
        return picked;
    }
}

/// <summary>Auto-lock time, clipboard clearing and the screen-capture switch.</summary>
internal static class VaultSettingsWindow
{
    public static async Task ShowAsync(Window? owner, MacController shell)
    {
        var inv = CultureInfo.InvariantCulture;
        var autolock = new TextBox { Width = 100, HorizontalAlignment = HorizontalAlignment.Left };
        var clip = new TextBox { Width = 100, HorizontalAlignment = HorizontalAlignment.Left };
        var capture = new CheckBox { Content = Strings.VaultPreventCapture, IsChecked = shell.PreventCapture, Margin = new Thickness(0, 14, 0, 0) };
        var result = DialogKit.Text("", top: 8);
        var kdf = DialogKit.Text("", VaultUi.MutedBrush, 12, top: 10);

        var save = DialogKit.Btn(Strings.BtnSave, 90);
        save.IsDefault = true;
        var close = DialogKit.Btn(Strings.Close, 90);
        var panel = Ui.Column(0,
            DialogKit.Text(Strings.VaultAutolockLabel, bottom: 4), autolock,
            DialogKit.Text(Strings.VaultClipClearLabel, top: 12, bottom: 4), clip,
            capture,
            DialogKit.Text(MacStrings.VaultPreventCaptureNote, VaultUi.MutedBrush, 12, top: 2),
            kdf, result,
            DialogKit.ButtonRow(close, save));
        var w = DialogKit.Create(shell, Strings.VaultSettingsTitle, 500, panel, height: 440, scroll: true);

        w.Opened += async (_, _) =>
        {
            var st = await shell.VaultCallAsync(a => a.Status());
            if (!st.Ok) { result.Text = st.Error ?? ""; result.Foreground = VaultUi.ErrorBrush; }
            else
            {
                autolock.Text = st.Value!.AutolockS.ToString(inv);
                clip.Text = st.Value.ClipClearS.ToString(inv);
                if (st.Value.Kdf.Length > 0) kdf.Text = Strings.VaultKdfInfo(st.Value.Kdf);
            }
        };
        save.Click += async (_, _) =>
        {
            var (auto, autoError) = VaultSettingsForm.ParseAutolock(autolock.Text ?? "");
            var (clear, clearError) = VaultSettingsForm.ParseClipClear(clip.Text ?? "");
            if (auto is null || clear is null)
            {
                result.Foreground = VaultUi.ErrorBrush;
                result.Text = autoError ?? clearError ?? "";
                return;
            }
            var e1 = await shell.CallAsync(a => a.SettingsSet("vault_autolock_s", auto.Value.ToString(inv)));
            var e2 = e1 ?? await shell.CallAsync(a => a.SettingsSet("vault_clip_clear_s", clear.Value.ToString(inv)));
            shell.SetPreventCapture(capture.IsChecked == true);
            MacWindowPrivacyRefresh(w, shell);
            result.Foreground = e2 is null ? VaultUi.OkBrush : VaultUi.ErrorBrush;
            result.Text = e2 ?? Strings.VaultSettingsSaved;
        };
        close.Click += (_, _) => w.Close();
        await VaultUi.ShowModalAsync(w, owner);
    }

    /// <summary>The settings window itself follows the new capture switch right away.</summary>
    private static void MacWindowPrivacyRefresh(Window w, MacController shell) => Platform.MacWindowPrivacy.Apply(w, shell.PreventCapture);
}

/// <summary>Change the master password: the current one, the new one twice, a live strength bar.</summary>
internal static class ChangeMasterWindow
{
    public static async Task ShowAsync(Window? owner, MacController shell)
    {
        var current = new CredsBox { Caption = Strings.ChangeCurrent };
        var next = new CredsBox { Caption = Strings.ChangeNew };
        var confirm = new CredsBox { Caption = Strings.LabelConfirmPassword, ShowKeyFile = false };
        var strength = new StrengthBar { Margin = new Thickness(0, 6, 0, 0) };
        var weak = DialogKit.Text(Strings.CreateWeakWarning, VaultUi.WarnBrush, 12, top: 4);
        weak.IsVisible = false;
        var error = DialogKit.Text("", VaultUi.ErrorBrush, top: 10);
        var ok = DialogKit.Btn(Strings.BtnChange, 90);
        ok.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, 90);
        var panel = Ui.Column(0,
            DialogKit.Text(Strings.ChangeNote, VaultUi.MutedBrush, 12, bottom: 10),
            current, new Border { Height = 12 }, next, strength, weak, new Border { Height = 12 }, confirm, error,
            DialogKit.ButtonRow(cancel, ok));
        var w = DialogKit.Create(shell, Strings.ChangeMasterTitle, 500, panel);

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
            if (problem is not null) { error.Text = problem; return; }
            if (!await VaultUi.ConfirmAsync(w, Strings.ChangeNote, Strings.ChangeMasterTitle, Strings.BtnChange, warning: true)) return;
            busy = true;
            ok.IsEnabled = false;
            error.Text = "";
            var cur = current.Creds;
            var nw = next.Creds;
            var r = await shell.VaultCallAsync(a => a.ChangeMaster(cur, nw));
            current.Clear(true);
            next.Clear(true);
            confirm.Clear();
            busy = false;
            ok.IsEnabled = true;
            if (!r.Ok) { error.Text = r.Error ?? Strings.ErrVaultUnexpected; return; }
            shell.ShowStatus(Strings.ChangeDone);
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
        await VaultUi.ShowModalAsync(w, owner);
    }
}
