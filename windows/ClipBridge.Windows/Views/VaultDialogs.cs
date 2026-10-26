using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>Builders for the code-only vault dialogs (no XAML: nothing to get out of step with the code).</summary>
internal static class DialogKit
{
    public static TextBlock Text(string text, Brush? foreground = null, double size = 0, bool bold = false, double top = 0, double bottom = 0)
    {
        var t = new TextBlock { Text = text, TextWrapping = TextWrapping.Wrap, Margin = new Thickness(0, top, 0, bottom) };
        if (foreground is not null) t.Foreground = foreground;
        if (size > 0) t.FontSize = size;
        if (bold) t.FontWeight = FontWeights.SemiBold;
        return t;
    }

    public static Button Btn(string text, RoutedEventHandler? click = null, double minWidth = 0)
    {
        var b = new Button { Content = text, MinWidth = minWidth };
        if (click is not null) b.Click += click;
        return b;
    }

    public static StackPanel ButtonRow(params UIElement[] buttons)
    {
        var p = new StackPanel { Orientation = Orientation.Horizontal, HorizontalAlignment = HorizontalAlignment.Right, Margin = new Thickness(0, 16, 0, 0) };
        foreach (var b in buttons) p.Children.Add(b);
        return p;
    }

    /// <summary>A tracked, protected dialog window; <paramref name="body"/> is its content with 18 px padding.</summary>
    public static Window Create(ShellController shell, Window? owner, string title, double width, UIElement body, double height = 0, bool scroll = false)
    {
        var w = new Window
        {
            Title = title, Width = width, ResizeMode = height > 0 ? ResizeMode.CanResize : ResizeMode.NoResize,
            ShowInTaskbar = false, WindowStartupLocation = WindowStartupLocation.CenterOwner,
        };
        if (height > 0) w.Height = height; else w.SizeToContent = SizeToContent.Height;
        var host = new Border { Padding = new Thickness(18), Child = body };
        w.Content = scroll ? new ScrollViewer { VerticalScrollBarVisibility = ScrollBarVisibility.Auto, Content = host } : host;
        shell.ProtectDialog(w);
        VaultUi.Track(w);
        VaultUi.Place(w, owner);
        w.PreviewKeyDown += (_, e) => { if (e.Key == System.Windows.Input.Key.Escape) w.Close(); };
        return w;
    }
}

/// <summary>Asks for a master password (and key file). Empty password can mean "the current credentials" when the caller allows it.</summary>
internal static class AskCredsDialog
{
    /// <summary>Returns the credentials, or null when cancelled. The password boxes are cleared before returning.</summary>
    public static VaultCreds? Ask(Window? owner, ShellController shell, string title, string prompt, string caption, bool allowEmpty)
    {
        var box = new CredsBox { Caption = caption };
        var panel = new StackPanel();
        panel.Children.Add(DialogKit.Text(prompt, bottom: 10));
        panel.Children.Add(box);
        VaultCreds? result = null;
        var ok = DialogKit.Btn(Strings.BtnOk, null, 90);
        ok.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, null, 90);
        cancel.IsCancel = true;
        panel.Children.Add(DialogKit.ButtonRow(ok, cancel));
        var w = DialogKit.Create(shell, owner, title, 440, panel);
        void Submit()
        {
            if (box.Password.Length == 0 && !allowEmpty) { box.ShowError(Strings.ExportNeedMaster); return; }
            result = box.Creds;
            w.DialogResult = true;
        }
        ok.Click += (_, _) => Submit();
        box.Submit += Submit;
        w.Loaded += (_, _) => box.FocusPassword();
        w.ShowDialog();
        box.Clear(keyFileToo: true);
        return result;
    }
}

/// <summary>The password health report: weak, reused, old and expired entries; Strings.BtnViewEntry jumps to the entry.</summary>
internal static class HealthWindow
{
    /// <summary>Returns the id of the entry to show, or null.</summary>
    public static string? Pick(Window? owner, ShellController shell)
    {
        string? picked = null;
        var summary = DialogKit.Text(Strings.VaultLoading, top: 4, bottom: 8);
        var list = new StackPanel();
        var panel = new DockPanel();
        var close = DialogKit.Btn(Strings.Close, null, 90);
        close.IsCancel = true;
        var buttons = DialogKit.ButtonRow(close);
        DockPanel.SetDock(buttons, Dock.Bottom);
        panel.Children.Add(buttons);
        var head = new StackPanel();
        head.Children.Add(DialogKit.Text(Strings.HealthNote, VaultUi.MutedBrush, 12));
        head.Children.Add(summary);
        DockPanel.SetDock(head, Dock.Top);
        panel.Children.Add(head);
        panel.Children.Add(new ScrollViewer { VerticalScrollBarVisibility = ScrollBarVisibility.Auto, Content = list });

        var w = DialogKit.Create(shell, owner, Strings.HealthTitle, 560, panel, height: 560);
        close.Click += (_, _) => w.Close();
        w.Loaded += async (_, _) =>
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
                    var line = new DockPanel { Margin = new Thickness(0, 2, 0, 2) };
                    var id = item.Id;
                    var view = DialogKit.Btn(Strings.BtnViewEntry, (_, _) => { picked = id; w.DialogResult = true; });
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
        return w.ShowDialog() == true ? picked : null;
    }
}

/// <summary>Auto-lock time, clipboard clearing and the screen-capture switch.</summary>
internal static class VaultSettingsWindow
{
    public static void Show(Window? owner, ShellController shell)
    {
        var autolock = new TextBox { Width = 100, HorizontalAlignment = HorizontalAlignment.Left };
        var clip = new TextBox { Width = 100, HorizontalAlignment = HorizontalAlignment.Left };
        var capture = new CheckBox { Content = Strings.VaultPreventCapture, IsChecked = shell.PreventCapture, Margin = new Thickness(0, 14, 0, 0) };
        var result = DialogKit.Text("", top: 8);
        var kdf = DialogKit.Text("", VaultUi.MutedBrush, 12, top: 10);

        var panel = new StackPanel();
        panel.Children.Add(DialogKit.Text(Strings.VaultAutolockLabel, bottom: 4));
        panel.Children.Add(autolock);
        panel.Children.Add(DialogKit.Text(Strings.VaultClipClearLabel, top: 12, bottom: 4));
        panel.Children.Add(clip);
        panel.Children.Add(capture);
        panel.Children.Add(DialogKit.Text(Strings.VaultPreventCaptureNote, VaultUi.MutedBrush, 12, top: 2));
        panel.Children.Add(kdf);
        panel.Children.Add(result);
        var save = DialogKit.Btn(Strings.BtnSave, null, 90);
        save.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.Close, null, 90);
        cancel.IsCancel = true;
        panel.Children.Add(DialogKit.ButtonRow(save, cancel));
        var w = DialogKit.Create(shell, owner, Strings.VaultSettingsTitle, 460, panel);

        w.Loaded += async (_, _) =>
        {
            var st = await shell.VaultCallAsync(a => a.Status());
            if (!st.Ok) { result.Text = st.Error ?? ""; result.Foreground = VaultUi.ErrorBrush; return; }
            autolock.Text = st.Value!.AutolockS.ToString(System.Globalization.CultureInfo.InvariantCulture);
            clip.Text = st.Value.ClipClearS.ToString(System.Globalization.CultureInfo.InvariantCulture);
            if (st.Value.Kdf.Length > 0) kdf.Text = Strings.VaultKdfInfo(st.Value.Kdf);
        };
        save.Click += async (_, _) =>
        {
            var (auto, autoError) = VaultSettingsForm.ParseAutolock(autolock.Text);
            var (clear, clearError) = VaultSettingsForm.ParseClipClear(clip.Text);
            if (auto is null || clear is null)
            {
                result.Foreground = VaultUi.ErrorBrush;
                result.Text = autoError ?? clearError ?? "";
                return;
            }
            var inv = System.Globalization.CultureInfo.InvariantCulture;
            var e1 = await shell.CallAsync(a => a.SettingsSet("vault_autolock_s", auto.Value.ToString(inv)));
            var e2 = e1 ?? await shell.CallAsync(a => a.SettingsSet("vault_clip_clear_s", clear.Value.ToString(inv)));
            shell.SetPreventCapture(capture.IsChecked == true);
            result.Foreground = e2 is null ? VaultUi.OkBrush : VaultUi.ErrorBrush;
            result.Text = e2 ?? Strings.VaultSettingsSaved;
        };
        w.ShowDialog();
    }
}

/// <summary>Change the master password: the current one, the new one twice, a live strength bar.</summary>
internal static class ChangeMasterWindow
{
    public static void Show(Window? owner, ShellController shell)
    {
        var current = new CredsBox { Caption = Strings.ChangeCurrent };
        var next = new CredsBox { Caption = Strings.ChangeNew };
        var confirm = new CredsBox { Caption = Strings.LabelConfirmPassword, ShowKeyFile = false };
        var strength = new StrengthBar { HorizontalAlignment = HorizontalAlignment.Left, Margin = new Thickness(0, 6, 0, 0) };
        var weak = DialogKit.Text(Strings.CreateWeakWarning, VaultUi.Brush(0xFFB36B00), 12, top: 4);
        weak.Visibility = Visibility.Collapsed;
        var error = DialogKit.Text("", VaultUi.ErrorBrush, top: 10);

        var panel = new StackPanel();
        panel.Children.Add(DialogKit.Text(Strings.ChangeNote, VaultUi.MutedBrush, 12, bottom: 10));
        panel.Children.Add(current);
        panel.Children.Add(new Border { Height = 12 });
        panel.Children.Add(next);
        panel.Children.Add(strength);
        panel.Children.Add(weak);
        panel.Children.Add(new Border { Height = 12 });
        panel.Children.Add(confirm);
        panel.Children.Add(error);
        var ok = DialogKit.Btn(Strings.BtnChange, null, 90);
        ok.IsDefault = true;
        var cancel = DialogKit.Btn(Strings.BtnCancel, null, 90);
        cancel.IsCancel = true;
        panel.Children.Add(DialogKit.ButtonRow(ok, cancel));
        var w = DialogKit.Create(shell, owner, Strings.ChangeMasterTitle, 500, panel, scroll: false);

        var generation = 0;
        var timer = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(250) };
        timer.Tick += async (_, _) =>
        {
            timer.Stop();
            var gen = ++generation;
            var pw = next.Password;
            if (pw.Length == 0) { strength.Clear(); weak.Visibility = Visibility.Collapsed; return; }
            var r = await shell.VaultCallAsync(a => a.Strength(pw));
            if (gen != generation || !r.Ok) return;
            strength.Set(r.Value!.Label, r.Value.Bits);
            weak.Visibility = StrengthDisplay.IsWeak(r.Value.Label) ? Visibility.Visible : Visibility.Collapsed;
        };
        next.PasswordChanged += () => { timer.Stop(); timer.Start(); };

        var busy = false;
        ok.Click += async (_, _) =>
        {
            if (busy) return;
            var problem = ChangeMasterForm.Check(current.Password, next.Password, confirm.Password);
            if (problem is not null) { error.Text = problem; return; }
            if (!VaultUi.Confirm(w, Strings.ChangeNote, Strings.ChangeMasterTitle, warning: true)) return;
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
            VaultUi.Info(w, Strings.ChangeDone);
            w.DialogResult = true;
        };
        w.Loaded += (_, _) => current.FocusPassword();
        w.Closed += (_, _) =>
        {
            timer.Stop();
            current.Clear(true);
            next.Clear(true);
            confirm.Clear();
        };
        w.ShowDialog();
    }
}
