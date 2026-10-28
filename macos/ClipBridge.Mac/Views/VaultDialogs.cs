using System.Globalization;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

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

/// <summary>Auto-lock time, clipboard clearing, the screen-capture switch and device sync.</summary>
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
        var syncList = new StackPanel { Spacing = 6 };
        var syncStatus = DialogKit.Text("", VaultUi.MutedBrush, 12, top: 4);
        var syncNow = DialogKit.Btn(MacStrings.VaultSyncNow);

        var save = DialogKit.Btn(Strings.BtnSave, 90);
        save.IsDefault = true;
        var close = DialogKit.Btn(Strings.Close, 90);
        var panel = Ui.Column(0,
            DialogKit.Text(Strings.VaultAutolockLabel, bottom: 4), autolock,
            DialogKit.Text(Strings.VaultClipClearLabel, top: 12, bottom: 4), clip,
            capture,
            DialogKit.Text(MacStrings.VaultPreventCaptureNote, VaultUi.MutedBrush, 12, top: 2),
            kdf, result,
            DialogKit.ButtonRow(close, save),
            new Border { Height = 1, Background = VaultUi.Brush(0x40808080), Margin = new Thickness(0, 16, 0, 8) },
            DialogKit.Text(MacStrings.VaultSyncTitle, bold: true, size: 14),
            DialogKit.Text(MacStrings.VaultSyncIntro, VaultUi.MutedBrush, 12, top: 2, bottom: 8),
            syncList, syncStatus,
            syncNow.With(b => b.Margin = new Thickness(0, 8, 0, 0)));
        var w = DialogKit.Create(shell, Strings.VaultSettingsTitle, 500, panel, height: 640, scroll: true);

        async Task LoadSyncAsync()
        {
            var r = await shell.VaultSyncStatusAsync();
            syncList.Children.Clear();
            if (!r.Ok) { syncStatus.Text = r.Error ?? ""; syncStatus.Foreground = VaultUi.ErrorBrush; return; }
            if (r.Value!.Count == 0) syncList.Children.Add(DialogKit.Text(MacStrings.VaultSyncNone, VaultUi.MutedBrush));
            foreach (var p in r.Value!) syncList.Children.Add(SyncRow(p));
        }

        Control SyncRow(VaultSyncPeer p)
        {
            var enabled = new CheckBox { Content = MacStrings.VaultSyncEnabled, IsChecked = p.Enabled, VerticalAlignment = VerticalAlignment.Top };
            enabled.IsCheckedChanged += async (_, _) =>
            {
                var on = enabled.IsChecked == true;
                if (on == p.Enabled) return;
                var r = await shell.VaultSyncEnableAsync(p.DeviceId, on);
                if (!r.Ok) { syncStatus.Text = r.Error ?? ""; syncStatus.Foreground = VaultUi.ErrorBrush; }
                await LoadSyncAsync();
            };
            var state = MacStrings.VaultSyncState(p.State, p.Connected) + (p.LastOkAt > 0 ? "  ·  " + MacStrings.VaultSyncLastOk(StampText.Local(p.LastOkAt)) : "");
            var text = Ui.Column(2,
                new TextBlock { Text = p.Name.Length > 0 ? p.Name : p.DeviceId, FontWeight = FontWeight.SemiBold, TextTrimming = TextTrimming.CharacterEllipsis },
                DialogKit.Text(state, p.State is "error" or "needs_password" or "review" ? VaultUi.WarnBrush : VaultUi.MutedBrush, 12));
            if (p.Detail.Length > 0 && p.State == "error") text.Children.Add(DialogKit.Text(p.Detail, VaultUi.MutedBrush, 11));
            if (p.State == "needs_password")
            {
                var enter = DialogKit.Btn(MacStrings.VaultSyncEnterPassword);
                enter.Click += async (_, _) =>
                {
                    var creds = await AskCredsDialog.AskAsync(w, shell, MacStrings.VaultSyncTitle, MacStrings.VaultSyncPasswordPrompt, Strings.LabelMasterPassword, allowEmpty: false);
                    if (creds is null) return;
                    var r = await shell.VaultSyncResolvePasswordAsync(p.DeviceId, creds);
                    syncStatus.Text = r.Ok ? MacStrings.VaultSyncResolved : r.Error ?? "";
                    syncStatus.Foreground = r.Ok ? VaultUi.OkBrush : VaultUi.ErrorBrush;
                    await LoadSyncAsync();
                };
                text.Children.Add(enter);
            }
            else if (p.State == "review" && p.IncomingSha256.Length > 0)
            {
                var approve = DialogKit.Btn(MacStrings.VaultSyncApprove);
                approve.Click += async (_, _) =>
                {
                    if (!await VaultUi.ConfirmAsync(w, MacStrings.VaultSyncApproveConfirm, MacStrings.VaultSyncTitle, MacStrings.VaultSyncApprove, warning: true)) return;
                    var r = await shell.VaultSyncApproveAsync(p.DeviceId, p.IncomingSha256);
                    syncStatus.Text = r.Ok ? MacStrings.VaultSyncResolved : r.Error ?? "";
                    syncStatus.Foreground = r.Ok ? VaultUi.OkBrush : VaultUi.ErrorBrush;
                    await LoadSyncAsync();
                };
                text.Children.Add(approve);
            }
            var line = new DockPanel();
            DockPanel.SetDock(enabled, Dock.Right);
            line.Children.Add(enabled);
            line.Children.Add(text);
            return line;
        }

        void OnEvent(CoreEvent ev)
        {
            if (ev.Type is "vault.sync" or "vault.changed" or "vault.joined") _ = LoadSyncAsync();
        }
        shell.EventReceived += OnEvent;
        w.Closed += (_, _) => shell.EventReceived -= OnEvent;

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
            await LoadSyncAsync();
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
        syncNow.Click += async (_, _) =>
        {
            var r = await shell.VaultSyncNowAsync();
            syncStatus.Text = r.Ok ? MacStrings.VaultSyncStarted : r.Error ?? "";
            syncStatus.Foreground = r.Ok ? VaultUi.OkBrush : VaultUi.ErrorBrush;
        };
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
