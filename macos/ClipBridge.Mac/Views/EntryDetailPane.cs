using Avalonia;
using Avalonia.Controls;
using Avalonia.Layout;
using Avalonia.Media;
using Avalonia.Threading;
using ClipBridge.Windows;
using ClipBridge.Windows.Logic;

namespace ClipBridge.Mac.Views;

/// <summary>
/// The right-hand pane of the vault page (port of the Windows EntryDetailPane): one entry in detail. Secrets are masked
/// until "显示" is pressed (<see cref="RevealState"/> masks them again after 15 s or when another entry is selected); every
/// copy goes through <c>vault.copy</c>; the TOTP code is refreshed once a second from <c>vault.totp</c> (only while the
/// user is active). All values fetched with vault.reveal are dropped by <see cref="ClearSecrets"/> (lock, hide, entry change).
/// </summary>
internal sealed class EntryDetailPane : Border
{
    private readonly MacController _shell = MacController.Current;
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
    private bool _shown = true;
    private TextBlock? _totpText;
    private TotpRing? _ring;
    private TextBlock? _totpPaused;

    /// <summary>The list should reload (an attachment or a version changed the entry).</summary>
    public event Action? DataChanged;

    public EntryDetailPane()
    {
        Child = new ScrollViewer { Content = _root };
        _timer.Tick += (_, _) => OnTick();
        Render();
    }

    public string? CurrentId => _detail?.Id;

    public int AutolockSeconds
    {
        get => _autolockSeconds;
        set => _autolockSeconds = Math.Max(0, value);
    }

    /// <summary>Called on every mouse move / key press on the vault page.</summary>
    public void MarkActivity() => _lastActivity = DateTimeOffset.UtcNow;

    /// <summary>The vault page is visible again.</summary>
    public void OnShown()
    {
        _shown = true;
        if (_detail is not null) _timer.Start();
    }

    /// <summary>The vault page was hidden: everything revealed is masked again.</summary>
    public void OnHidden()
    {
        _shown = false;
        _timer.Stop();
        ClearSecrets();
        Render();
    }

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
        if (_shown) _timer.Start();
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
        _ring.IsVisible = live;
        _totpPaused.IsVisible = !live && !_totpBusy;
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
        header.Children.Add(new TextBlock { Text = KindTemplate.GlyphFor(d.Summary.Kind), FontSize = 24, Margin = new Thickness(0, 0, 10, 0), VerticalAlignment = VerticalAlignment.Center });
        header.Children.Add(new TextBlock
        {
            Text = d.Summary.Title.Length > 0 ? d.Summary.Title : Strings.Untitled, FontSize = 18, FontWeight = FontWeight.SemiBold,
            TextWrapping = TextWrapping.Wrap, VerticalAlignment = VerticalAlignment.Center,
        });
        _root.Children.Add(header);

        foreach (var row in _rows) _root.Children.Add(BuildRow(row));
        if (d.Summary.HasTotp) _root.Children.Add(BuildTotp());

        if (d.HasPassword)
        {
            var strength = new StrengthBar { Margin = new Thickness(0, 2, 0, 0) };
            strength.Set(d.Strength.Label, d.Strength.Bits);
            _root.Children.Add(Block(Strings.LabelStrength, strength));
        }
        if (d.Summary.Tags.Count > 0)
            _root.Children.Add(Block(Strings.LabelTagsShort, new TextBlock { Text = string.Join("  ", d.Summary.Tags.Select(t => "#" + t)), TextWrapping = TextWrapping.Wrap }));

        var meta = new StackPanel { Margin = new Thickness(0, 4, 0, 12) };
        if (d.Summary.ExpiresAt > 0)
        {
            var state = QuickFilters.Expiry(d.Summary.ExpiresAt, DateTimeOffset.UtcNow);
            meta.Children.Add(new TextBlock
            {
                Text = Strings.LabelExpiresAt + "：" + StampText.DateOnly(d.Summary.ExpiresAt)
                       + (state == ExpiryState.Expired ? "（" + Strings.BadgeExpired + "）" : state == ExpiryState.Soon ? "（" + Strings.BadgeExpiring + "）" : ""),
                Foreground = state is ExpiryState.Expired or ExpiryState.Soon ? VaultUi.WarnBrush : VaultUi.MutedBrush, FontSize = 12,
            });
        }
        meta.Children.Add(Muted(Strings.LabelModified + "：" + StampText.Local(d.Summary.ModifiedAt)));
        meta.Children.Add(Muted(Strings.LabelCreated + "：" + StampText.Local(d.CreatedAt)));
        _root.Children.Add(meta);

        if (!d.Summary.InTrash) _root.Children.Add(BuildAttachments());
        if (_history.Count > 0) _root.Children.Add(BuildHistory());

        RefreshTotpUi();
    }

    private static Control Block(string label, Control content)
    {
        var p = new StackPanel { Margin = new Thickness(0, 0, 0, 10), Spacing = 2 };
        p.Children.Add(Muted(label));
        p.Children.Add(content);
        return p;
    }

    private static Button SmallButton(string text) => new() { Content = text, VerticalAlignment = VerticalAlignment.Top, Margin = new Thickness(4, 0, 0, 0) };

    private Control BuildRow(DetailRow row)
    {
        var now = DateTimeOffset.UtcNow;
        var shown = row.Concealed && _reveal.IsRevealed(row.Key, now) && _revealed.ContainsKey(row.Key);
        var text = DetailRows.Display(row, shown ? _revealed[row.Key] : null);

        var line = new DockPanel();
        var buttons = new StackPanel { Orientation = Orientation.Horizontal, VerticalAlignment = VerticalAlignment.Top };
        DockPanel.SetDock(buttons, Dock.Right);
        if (row.Concealed)
        {
            var toggle = SmallButton(shown ? Strings.BtnHide : Strings.BtnReveal);
            toggle.Click += async (_, _) => await ToggleRevealAsync(row);
            buttons.Children.Add(toggle);
        }
        if (row.Kind == DetailKind.Link)
        {
            var open = SmallButton(Strings.BtnOpenUrl);
            open.Click += async (_, _) => { if (_shell.OpenWebUrl(row.KnownValue) is { } error) await VaultUi.ErrorAsync(this, error); };
            buttons.Children.Add(open);
        }
        if (row.CanCopy)
        {
            var copy = SmallButton(Strings.BtnCopyField);
            copy.Click += async (_, _) => await CopyAsync(row.RevealField, row.Label);
            buttons.Children.Add(copy);
        }
        line.Children.Add(buttons);
        var value = new TextBlock { Text = text, TextWrapping = TextWrapping.Wrap, VerticalAlignment = VerticalAlignment.Center };
        if (row.Kind == DetailKind.Secret || (row.Concealed && shown)) value.FontFamily = VaultUi.Mono;
        if (row.Kind == DetailKind.Link) value.Foreground = VaultUi.LinkBrush;
        line.Children.Add(value);
        return Block(row.Label, line);
    }

    private Control BuildTotp()
    {
        var line = new DockPanel();
        var copy = SmallButton(Strings.BtnCopyField);
        copy.VerticalAlignment = VerticalAlignment.Center;
        copy.Click += async (_, _) => await CopyAsync("totp", Strings.TotpTitle);
        DockPanel.SetDock(copy, Dock.Right);
        line.Children.Add(copy);
        _ring = new TotpRing { Margin = new Thickness(0, 0, 12, 0), VerticalAlignment = VerticalAlignment.Center };
        DockPanel.SetDock(_ring, Dock.Left);
        line.Children.Add(_ring);
        _totpText = new TextBlock { FontFamily = VaultUi.Mono, FontSize = 24, FontWeight = FontWeight.SemiBold, VerticalAlignment = VerticalAlignment.Center };
        line.Children.Add(_totpText);
        _totpPaused = Muted(Strings.TotpPaused);
        _totpPaused.IsVisible = false;
        return Block(Strings.TotpTitle, Ui.Column(2, line, _totpPaused));
    }

    private Control BuildAttachments()
    {
        var panel = new StackPanel { Spacing = 2 };
        var d = _detail!;
        if (d.Attachments.Count == 0) panel.Children.Add(Muted(Strings.AttachmentsNone));
        foreach (var a in d.Attachments)
        {
            var line = new DockPanel();
            var buttons = new StackPanel { Orientation = Orientation.Horizontal };
            DockPanel.SetDock(buttons, Dock.Right);
            var open = SmallButton(MacStrings.AttachOpen);
            open.Click += async (_, _) => await OpenAttachmentAsync(a);
            var save = SmallButton(Strings.BtnSaveAttachment);
            save.Click += async (_, _) => await SaveAttachmentAsync(a);
            var remove = SmallButton(Strings.BtnRemoveAttachment);
            remove.Click += async (_, _) => await RemoveAttachmentAsync(a);
            buttons.Children.Add(open);
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

    private Control BuildHistory()
    {
        var panel = new StackPanel { Spacing = 2, Margin = new Thickness(0, 4, 0, 0) };
        foreach (var v in _history.OrderByDescending(x => x.Index))
        {
            var line = new DockPanel();
            var restore = SmallButton(Strings.BtnRestoreVersion);
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
        return new Expander
        {
            Header = Strings.HistoryTitle + " (" + _history.Count + ")", Content = panel, Margin = new Thickness(0, 0, 0, 8),
            HorizontalAlignment = HorizontalAlignment.Stretch,
        };
    }

    // ---- actions ---------------------------------------------------------------------------------

    private Task Fail(string message) => VaultUi.ErrorAsync(this, message);

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
            if (!r.Ok) { await Fail(r.Error!); return; }
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
        if (error is not null) await Fail(error);
    }

    private async Task AddAttachmentAsync()
    {
        if (_detail is null) return;
        var id = _detail.Id;
        var path = await VaultUi.PickOpenFileAsync(this, Strings.BtnAddAttachment);
        if (path is null || _detail?.Id != id) return;
        long length;
        try { length = new FileInfo(path).Length; }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { await Fail(Strings.KeyFileUnreadable); return; }
        if (length > AttachmentRules.MaxBytes) { await Fail(Strings.AttachTooBig); return; }
        var name = Path.GetFileName(path);
        var r = await _shell.VaultCallAsync(a => a.AttachAdd(id, name, path));
        if (!r.Ok) { await Fail(r.Error!); return; }
        await ShowAsync(id);
        DataChanged?.Invoke();
    }

    private async Task OpenAttachmentAsync(AttachmentInfo att)
    {
        if (_detail is null) return;
        if (await _shell.OpenVaultAttachmentAsync(_detail.Id, att.Name) is { } error) await Fail(error);
    }

    private async Task SaveAttachmentAsync(AttachmentInfo att)
    {
        if (_detail is null) return;
        var id = _detail.Id;
        var chosen = await VaultUi.PickSaveFileAsync(this, Strings.BtnSaveAttachment, att.Name);
        if (chosen is null) return;
        var r = await _shell.VaultCallAsync(a => a.AttachSave(id, att.Name, chosen));
        if (!r.Ok) { await Fail(r.Error!); return; }
        _shell.ShowStatus(Strings.AttachSaved + "：" + chosen);
    }

    private async Task RemoveAttachmentAsync(AttachmentInfo att)
    {
        if (_detail is null || !await VaultUi.ConfirmAsync(this, Strings.AttachRemoveConfirm + "\n" + att.Name, yes: Strings.BtnRemoveAttachment)) return;
        if (_detail is null) return;
        var id = _detail.Id;
        var r = await _shell.VaultCallAsync(a => a.AttachRemove(id, att.Name));
        if (!r.Ok) { await Fail(r.Error!); return; }
        await ShowAsync(id);
        DataChanged?.Invoke();
    }

    private async Task RestoreVersionAsync(VersionInfo v)
    {
        if (_detail is null || !await VaultUi.ConfirmAsync(this, Strings.HistoryRestoreConfirm, yes: Strings.BtnRestoreVersion)) return;
        if (_detail is null) return;
        var id = _detail.Id;
        var r = await _shell.VaultCallAsync(a => a.HistoryRestore(id, v.Index));
        if (!r.Ok) { await Fail(r.Error!); return; }
        ClearSecrets();
        await ShowAsync(id);
        DataChanged?.Invoke();
        _shell.ShowStatus(Strings.HistoryRestored);
    }
}
