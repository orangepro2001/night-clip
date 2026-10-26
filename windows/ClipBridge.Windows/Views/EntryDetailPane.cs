using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Threading;
using ClipBridge.Windows.Logic;
using ClipBridge.Windows.Services;

namespace ClipBridge.Windows.Views;

/// <summary>
/// The right-hand pane of the vault page: one entry in detail. Secrets are masked until "显示" is pressed
/// (<see cref="RevealState"/> masks them again after 15 s or when another entry is selected); every copy goes through
/// <c>vault.copy</c>; the TOTP code is refreshed once a second from <c>vault.totp</c> (only while the user is active).
/// All values fetched with vault.reveal are dropped by <see cref="ClearSecrets"/> (lock, hide, entry change).
/// </summary>
internal sealed class EntryDetailPane : UserControl
{
    private ShellController _shell = null!;
    private readonly StackPanel _root = new() { Margin = new Thickness(14, 10, 14, 16) };
    private readonly DispatcherTimer _timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly Dictionary<string, string> _revealed = new();
    private readonly RevealState _reveal = new();
    private readonly TotpCountdown _totp = new();
    private EntryDetail? _detail;
    private IReadOnlyList<DetailRow> _rows = Array.Empty<DetailRow>();
    private IReadOnlyList<VersionInfo> _history = Array.Empty<VersionInfo>();
    private DateTimeOffset _lastActivity = DateTimeOffset.UtcNow;
    private int _autolockSeconds = 300;
    private int _generation;
    private bool _totpBusy;
    private TextBlock? _totpText;
    private TotpRing? _ring;
    private TextBlock? _totpPaused;

    /// <summary>The list should reload (an attachment or a version changed the entry).</summary>
    public event Action? DataChanged;

    public EntryDetailPane()
    {
        Content = new ScrollViewer { VerticalScrollBarVisibility = ScrollBarVisibility.Auto, Content = _root };
        _timer.Tick += (_, _) => OnTick();
        IsVisibleChanged += (_, e) =>
        {
            if ((bool)e.NewValue) { if (_detail is not null) _timer.Start(); }
            else { _timer.Stop(); ClearSecrets(); Render(); }
        };
        Render();
    }

    internal void Attach(ShellController shell) => _shell = shell;

    public string? CurrentId => _detail?.Id;

    public int AutolockSeconds
    {
        get => _autolockSeconds;
        set => _autolockSeconds = Math.Max(30, value);
    }

    /// <summary>Called on every mouse move / key press on the vault page.</summary>
    public void MarkActivity() => _lastActivity = DateTimeOffset.UtcNow;

    // ---- showing an entry --------------------------------------------------------------------

    /// <summary>Drops every revealed value, the TOTP code and the entry itself (called on lock).</summary>
    public void Clear()
    {
        _generation++;
        ClearSecrets();
        _detail = null;
        _rows = Array.Empty<DetailRow>();
        _history = Array.Empty<VersionInfo>();
        _timer.Stop();
        Render();
    }

    private void ClearSecrets()
    {
        _revealed.Clear();
        _reveal.Clear();
        _totp.Clear();
    }

    public async Task ShowAsync(string? id)
    {
        var gen = ++_generation;
        if (id is null) { Clear(); return; }
        if (_detail?.Id != id)
        {
            ClearSecrets(); // another entry: everything is masked again
            _detail = null;
            Render(Strings.VaultLoading);
        }
        _reveal.SelectEntry(id);

        var r = await _shell.VaultCallAsync(a => a.Get(id));
        if (gen != _generation) return;
        if (!r.Ok)
        {
            _detail = null;
            _rows = Array.Empty<DetailRow>();
            Render(r.Error);
            return;
        }
        _detail = r.Value;
        _rows = DetailRows.Build(_detail!);
        _history = Array.Empty<VersionInfo>();
        if (_detail!.History > 0)
        {
            var h = await _shell.VaultCallAsync(a => a.History(id));
            if (gen != _generation) return;
            if (h.Ok) _history = h.Value!;
        }
        Render();
        _timer.Start();
        if (_detail.Summary.HasTotp) await FetchTotpAsync();
    }

    // ---- timer: re-masking and TOTP ----------------------------------------------------------

    private void OnTick()
    {
        var now = DateTimeOffset.UtcNow;
        var expired = _reveal.Expired(now);
        if (expired.Count > 0)
        {
            foreach (var key in expired) _revealed.Remove(key);
            Render();
        }
        if (_detail is { Summary.HasTotp: true } && _totpText is not null)
        {
            if (_totp.NeedsRefresh(now))
            {
                if (TotpActivityPolicy.ShouldPoll(_lastActivity, now, _autolockSeconds)) _ = FetchTotpAsync();
                else _totp.Clear(); // paused: an expired code is not shown
            }
            RefreshTotpUi();
        }
    }

    private async Task FetchTotpAsync()
    {
        if (_totpBusy || _detail is null) return;
        var id = _detail.Id;
        var gen = _generation;
        _totpBusy = true;
        try
        {
            var r = await _shell.VaultCallAsync(a => a.Totp(id));
            if (gen != _generation || !r.Ok) return;
            _totp.Update(r.Value!, DateTimeOffset.UtcNow);
            RefreshTotpUi();
        }
        finally { _totpBusy = false; }
    }

    private void RefreshTotpUi()
    {
        if (_totpText is null || _ring is null || _totpPaused is null) return;
        var now = DateTimeOffset.UtcNow;
        var live = _totp.HasCode && _totp.SecondsLeft(now) > 0;
        _totpText.Text = live ? TotpCountdown.Group(_totp.Code) : "······";
        _ring.Visibility = live ? Visibility.Visible : Visibility.Collapsed;
        _totpPaused.Visibility = !live && !_totpBusy ? Visibility.Visible : Visibility.Collapsed;
        if (live) _ring.Set(_totp.Fraction(now), _totp.SecondsLeft(now));
    }

    // ---- rendering -----------------------------------------------------------------------------

    private static TextBlock Muted(string text, double size = 12) =>
        new() { Text = text, Foreground = VaultUi.MutedBrush, FontSize = size, TextWrapping = TextWrapping.Wrap };

    private void Render(string? message = null)
    {
        _root.Children.Clear();
        _totpText = null;
        _ring = null;
        _totpPaused = null;

        if (_detail is null)
        {
            _root.Children.Add(new TextBlock
            {
                Text = message ?? Strings.DetailEmpty, Foreground = message is null ? VaultUi.MutedBrush : VaultUi.ErrorBrush,
                TextWrapping = TextWrapping.Wrap, Margin = new Thickness(0, 40, 0, 0), HorizontalAlignment = HorizontalAlignment.Center,
            });
            return;
        }

        var d = _detail;
        var header = new DockPanel { Margin = new Thickness(0, 0, 0, 12) };
        header.Children.Add(new TextBlock { Text = KindTemplate.GlyphFor(d.Summary.Kind), FontSize = 26, Margin = new Thickness(0, 0, 10, 0), VerticalAlignment = VerticalAlignment.Center });
        header.Children.Add(new TextBlock
        {
            Text = d.Summary.Title.Length > 0 ? d.Summary.Title : Strings.Untitled, FontSize = 20, FontWeight = FontWeights.SemiBold,
            TextWrapping = TextWrapping.Wrap, VerticalAlignment = VerticalAlignment.Center,
        });
        _root.Children.Add(header);

        foreach (var row in _rows) _root.Children.Add(BuildRow(row));
        if (d.Summary.HasTotp) _root.Children.Add(BuildTotp());

        if (d.HasPassword)
        {
            var strength = new StrengthBar { HorizontalAlignment = HorizontalAlignment.Left, Margin = new Thickness(0, 2, 0, 0) };
            strength.Set(d.Strength.Label, d.Strength.Bits);
            _root.Children.Add(Block(Strings.LabelStrength, strength));
        }
        if (d.Summary.Tags.Count > 0) _root.Children.Add(Block(Strings.LabelTagsShort, new TextBlock { Text = string.Join("  ", d.Summary.Tags.Select(t => "#" + t)), TextWrapping = TextWrapping.Wrap }));

        var meta = new StackPanel();
        if (d.Summary.ExpiresAt > 0)
        {
            var state = QuickFilters.Expiry(d.Summary.ExpiresAt, DateTimeOffset.UtcNow);
            meta.Children.Add(new TextBlock
            {
                Text = Strings.LabelExpiresAt + "：" + StampText.DateOnly(d.Summary.ExpiresAt) + (state == ExpiryState.Expired ? "（" + Strings.BadgeExpired + "）" : state == ExpiryState.Soon ? "（" + Strings.BadgeExpiring + "）" : ""),
                Foreground = state is ExpiryState.Expired or ExpiryState.Soon ? VaultUi.Brush(0xFFB36B00) : VaultUi.MutedBrush, FontSize = 12,
            });
        }
        meta.Children.Add(Muted(Strings.LabelModified + "：" + StampText.Local(d.Summary.ModifiedAt)));
        meta.Children.Add(Muted(Strings.LabelCreated + "：" + StampText.Local(d.CreatedAt)));
        meta.Margin = new Thickness(0, 4, 0, 12);
        _root.Children.Add(meta);

        if (!d.Summary.InTrash) _root.Children.Add(BuildAttachments());
        if (_history.Count > 0) _root.Children.Add(BuildHistory());

        RefreshTotpUi();
    }

    private static UIElement Block(string label, UIElement content)
    {
        var p = new StackPanel { Margin = new Thickness(0, 0, 0, 10) };
        p.Children.Add(Muted(label));
        p.Children.Add(content);
        return p;
    }

    private UIElement BuildRow(DetailRow row)
    {
        var now = DateTimeOffset.UtcNow;
        var shown = row.Concealed && _reveal.IsRevealed(row.Key, now) && _revealed.ContainsKey(row.Key);
        var text = DetailRows.Display(row, shown ? _revealed[row.Key] : null);

        var line = new DockPanel();
        var buttons = new StackPanel { Orientation = Orientation.Horizontal, VerticalAlignment = VerticalAlignment.Top };
        DockPanel.SetDock(buttons, Dock.Right);
        if (row.Concealed)
        {
            var toggle = new Button { Content = shown ? Strings.BtnHide : Strings.BtnReveal, MinWidth = 52 };
            toggle.Click += async (_, _) => await ToggleRevealAsync(row);
            buttons.Children.Add(toggle);
        }
        if (row.Kind == DetailKind.Link)
        {
            var open = new Button { Content = Strings.BtnOpenUrl };
            open.Click += (_, _) => { if (_shell.OpenWebUrl(row.KnownValue) is { } error) VaultUi.Error(Window.GetWindow(this), error); };
            buttons.Children.Add(open);
        }
        if (row.CanCopy)
        {
            var copy = new Button { Content = Strings.BtnCopyField };
            copy.Click += async (_, _) => await CopyAsync(row.RevealField, row.Label);
            buttons.Children.Add(copy);
        }
        line.Children.Add(buttons);
        line.Children.Add(new TextBlock
        {
            Text = text, TextWrapping = TextWrapping.Wrap, VerticalAlignment = VerticalAlignment.Center,
            FontFamily = row.Kind == DetailKind.Secret || (row.Concealed && shown) ? new FontFamily("Consolas") : SystemFonts.MessageFontFamily,
            Foreground = row.Kind == DetailKind.Link ? VaultUi.Brush(0xFF1A5FB4) : Brushes.Black,
        });
        return Block(row.Label, line);
    }

    private UIElement BuildTotp()
    {
        var line = new DockPanel();
        var copy = new Button { Content = Strings.BtnCopyField, VerticalAlignment = VerticalAlignment.Center };
        copy.Click += async (_, _) => await CopyAsync("totp", Strings.TotpTitle);
        DockPanel.SetDock(copy, Dock.Right);
        line.Children.Add(copy);
        _ring = new TotpRing { Margin = new Thickness(0, 0, 12, 0), VerticalAlignment = VerticalAlignment.Center };
        DockPanel.SetDock(_ring, Dock.Left);
        line.Children.Add(_ring);
        _totpText = new TextBlock { FontFamily = new FontFamily("Consolas"), FontSize = 26, FontWeight = FontWeights.SemiBold, VerticalAlignment = VerticalAlignment.Center };
        line.Children.Add(_totpText);
        _totpPaused = Muted(Strings.TotpPaused);
        _totpPaused.Visibility = Visibility.Collapsed;
        var panel = new StackPanel();
        panel.Children.Add(line);
        panel.Children.Add(_totpPaused);
        return Block(Strings.TotpTitle, panel);
    }

    private UIElement BuildAttachments()
    {
        var panel = new StackPanel();
        var d = _detail!;
        if (d.Attachments.Count == 0) panel.Children.Add(Muted(Strings.AttachmentsNone));
        foreach (var a in d.Attachments)
        {
            var line = new DockPanel { Margin = new Thickness(0, 2, 0, 2) };
            var buttons = new StackPanel { Orientation = Orientation.Horizontal };
            DockPanel.SetDock(buttons, Dock.Right);
            var save = new Button { Content = Strings.BtnSaveAttachment };
            save.Click += async (_, _) => await SaveAttachmentAsync(a);
            var remove = new Button { Content = Strings.BtnRemoveAttachment };
            remove.Click += async (_, _) => await RemoveAttachmentAsync(a);
            buttons.Children.Add(save);
            buttons.Children.Add(remove);
            line.Children.Add(buttons);
            line.Children.Add(new TextBlock { Text = "📎 " + a.Name + "  (" + Formatting.Bytes(a.Size) + ")", VerticalAlignment = VerticalAlignment.Center, TextTrimming = TextTrimming.CharacterEllipsis });
            panel.Children.Add(line);
        }
        var add = new Button { Content = Strings.BtnAddAttachment, HorizontalAlignment = HorizontalAlignment.Left, Margin = new Thickness(0, 6, 0, 0) };
        add.Click += async (_, _) => await AddAttachmentAsync();
        panel.Children.Add(add);
        return Block(Strings.AttachmentsTitle, panel);
    }

    private UIElement BuildHistory()
    {
        var panel = new StackPanel { Margin = new Thickness(0, 4, 0, 0) };
        foreach (var v in _history.OrderByDescending(x => x.Index))
        {
            var line = new DockPanel { Margin = new Thickness(0, 2, 0, 2) };
            var restore = new Button { Content = Strings.BtnRestoreVersion };
            restore.Click += async (_, _) => await RestoreVersionAsync(v);
            DockPanel.SetDock(restore, Dock.Right);
            line.Children.Add(restore);
            line.Children.Add(new TextBlock
            {
                Text = StampText.Local(v.ModifiedAt) + "  ·  " + (v.Title.Length > 0 ? v.Title : Strings.Untitled) + (v.Username.Length > 0 ? "  ·  " + v.Username : ""),
                VerticalAlignment = VerticalAlignment.Center, TextTrimming = TextTrimming.CharacterEllipsis,
            });
            panel.Children.Add(line);
        }
        return new Expander { Header = Strings.HistoryTitle + " (" + _history.Count + ")", Content = panel, Margin = new Thickness(0, 0, 0, 8) };
    }

    // ---- actions ---------------------------------------------------------------------------------

    private void Fail(string message) => VaultUi.Error(Window.GetWindow(this), message);

    private async Task ToggleRevealAsync(DetailRow row)
    {
        if (_detail is null) return;
        var now = DateTimeOffset.UtcNow;
        if (_reveal.IsRevealed(row.Key, now))
        {
            _reveal.Hide(row.Key);
            _revealed.Remove(row.Key);
            Render();
            return;
        }
        string value;
        if (DetailRows.NeedsFetch(row))
        {
            var id = _detail.Id;
            var gen = _generation;
            var r = await _shell.VaultCallAsync(a => a.Reveal(id, row.RevealField));
            if (gen != _generation) return;
            if (!r.Ok) { Fail(r.Error!); return; }
            value = r.Value!;
        }
        else value = row.KnownValue; // already in vault.get's answer (notes)
        _revealed[row.Key] = value;
        _reveal.Reveal(row.Key, DateTimeOffset.UtcNow);
        Render();
    }

    private async Task CopyAsync(string field, string label)
    {
        if (_detail is null) return;
        var error = await _shell.CopyVaultSecretAsync(_detail.Id, field, label);
        if (error is not null) Fail(error);
    }

    private async Task AddAttachmentAsync()
    {
        if (_detail is null) return;
        var path = VaultUi.PickOpenFile(Window.GetWindow(this), Strings.BtnAddAttachment, Strings.FilterAnyFile);
        if (path is null) return;
        long length;
        try { length = new FileInfo(path).Length; }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { Fail(Strings.KeyFileUnreadable); return; }
        if (length > AttachmentRules.MaxBytes) { Fail(Strings.AttachTooBig); return; }
        var id = _detail.Id;
        var name = System.IO.Path.GetFileName(path);
        var r = await _shell.VaultCallAsync(a => a.AttachAdd(id, name, path));
        if (!r.Ok) { Fail(r.Error!); return; }
        await ShowAsync(id);
        DataChanged?.Invoke();
    }

    private async Task SaveAttachmentAsync(AttachmentInfo att)
    {
        if (_detail is null) return;
        var chosen = VaultUi.PickSaveFile(Window.GetWindow(this), Strings.BtnSaveAttachment, Strings.FilterAnyFile, att.Name);
        if (chosen is null) return;
        var id = _detail.Id;
        var r = await _shell.VaultCallAsync(a => a.AttachSave(id, att.Name, chosen));
        if (!r.Ok) { Fail(r.Error!); return; }
        _shell.ShowStatus(Strings.AttachSaved + "：" + chosen);
    }

    private async Task RemoveAttachmentAsync(AttachmentInfo att)
    {
        if (_detail is null || !VaultUi.Confirm(Window.GetWindow(this), Strings.AttachRemoveConfirm + "\n" + att.Name)) return;
        var id = _detail.Id;
        var r = await _shell.VaultCallAsync(a => a.AttachRemove(id, att.Name));
        if (!r.Ok) { Fail(r.Error!); return; }
        await ShowAsync(id);
        DataChanged?.Invoke();
    }

    private async Task RestoreVersionAsync(VersionInfo v)
    {
        if (_detail is null || !VaultUi.Confirm(Window.GetWindow(this), Strings.HistoryRestoreConfirm)) return;
        var id = _detail.Id;
        var r = await _shell.VaultCallAsync(a => a.HistoryRestore(id, v.Index));
        if (!r.Ok) { Fail(r.Error!); return; }
        ClearSecrets();
        await ShowAsync(id);
        DataChanged?.Invoke();
        _shell.ShowStatus(Strings.HistoryRestored);
    }
}
