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

/// <summary>
/// The right-hand pane of the vault page: one entry in detail. Secrets are masked until the eye button is pressed
/// (<see cref="RevealState"/> masks them again after 15 s or when another entry is selected); every copy goes through
/// <c>vault.copy</c>; the TOTP code is refreshed once a second from <c>vault.totp</c> (only while the user is active).
/// All values fetched with vault.reveal are dropped by <see cref="ClearSecrets"/> (lock, hide, entry change).
/// </summary>
internal sealed class EntryDetailPane : Border
{
    private readonly ShellController _shell;
    private readonly StackPanel _root = new() { Margin = new Thickness(20, 18, 20, 20), Spacing = 14 };
    private readonly DispatcherTimer _timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly Dictionary<string, string> _revealed = new();
    private readonly RevealState _reveal = new();
    private readonly TotpCountdown _totp = new();
    private EntryDetail? _detail;
    private IReadOnlyList<DetailRow> _rows = Array.Empty<DetailRow>();
    private IReadOnlyList<VersionInfo> _history = Array.Empty<VersionInfo>();
    private IReadOnlyList<SshUse> _sshRecent = Array.Empty<SshUse>();
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

    /// <summary>Edit / delete / restore from the header: the main panel owns those actions.</summary>
    public event Action? EditRequested;

    public EntryDetailPane(ShellController shell)
    {
        _shell = shell;
        Child = new ScrollViewer { Content = _root };
        _timer.Tick += (_, _) => OnTick();
        // The agent loaded, dropped or used a key: the SSH section's badges change.
        shell.SshChanged += () =>
        {
            if (_shown && _detail?.Ssh is not null) _ = ShowAsync(_detail.Id);
        };
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
            Render(loading: true);
        }
        _reveal.SelectEntry(id);

        var r = await _shell.VaultCallAsync(a => a.Get(id));
        if (gen != _generation) return;
        if (!r.Ok)
        {
            _detail = null;
            _rows = Array.Empty<DetailRow>();
            Render(error: r.Error);
            return;
        }
        _detail = r.Value;
        _rows = DetailRows.Build(_detail!);
        _history = Array.Empty<VersionInfo>();
        _sshRecent = Array.Empty<SshUse>();
        if (_detail!.Ssh is not null)
        {
            var recent = await _shell.SshCallAsync(a => a.Recent(100));
            if (gen != _generation) return;
            if (recent.Ok) _sshRecent = recent.Value!;
        }
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
        _totpText.Text = live ? TotpCountdown.Group(_totp.Code) : "··· ···";
        _ring.IsVisible = live;
        _totpPaused.IsVisible = !live && !_totpBusy;
        if (live) _ring.Set(_totp.Fraction(now), _totp.SecondsLeft(now));
    }

    // ---- rendering -----------------------------------------------------------------------------

    private void Render(bool loading = false, string? error = null)
    {
        _root.Children.Clear();
        _totpText = null;
        _ring = null;
        _totpPaused = null;

        if (_detail is null)
        {
            if (loading)
                _root.Children.Add(new Lumen.UI.Controls.Spinner { Size = 22, HorizontalAlignment = HorizontalAlignment.Center, Margin = new Thickness(0, 60, 0, 0) });
            else if (error is not null)
                _root.Children.Add(new InfoBar { Severity = Severity.Danger, Message = error });
            else
                _root.Children.Add(new EmptyState { Icon = IconKind.MousePointer, Title = Strings.DetailEmptyTitle, Description = Strings.DetailEmpty, Margin = new Thickness(0, 30, 0, 0) });
            return;
        }

        var d = _detail;
        var tile = new Border { Width = 44, Height = 44, CornerRadius = new CornerRadius(12), Child = new Icon { NameKey = VaultUi.KindIcon(d.Summary.Kind), Size = 20, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center } };
        tile.Classes.Add("tile");
        tile.Classes.Add("accent");
        var titles = new StackPanel { VerticalAlignment = VerticalAlignment.Center, Spacing = 2, Margin = new Thickness(12, 0, 8, 0) };
        titles.Children.Add(Ui.Text(d.Summary.Title.Length > 0 ? d.Summary.Title : Strings.Untitled, "subtitle"));
        titles.Children.Add(Ui.Text(KindTemplate.For(d.Summary.Kind).Label + (d.Summary.InTrash ? "  ·  " + Strings.InTrashLabel : ""), "caption", "tertiary"));
        var header = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto") };
        header.Children.Add(tile);
        Grid.SetColumn(titles, 1);
        header.Children.Add(titles);
        if (!d.Summary.InTrash)
        {
            var edit = Ui.Button(AppIcons.Of(IconKind.Pencil), Strings.BtnEditEntry, () => EditRequested?.Invoke(), "soft", "sm");
            Grid.SetColumn(edit, 2);
            header.Children.Add(edit);
        }
        _root.Children.Add(header);
        if (d.Ssh is { } ssh) _root.Children.Add(BuildSsh(ssh, d.Summary.InTrash));

        var fields = new StackPanel { Spacing = 0 };
        foreach (var row in _rows) fields.Children.Add(BuildRow(row));
        if (d.Summary.HasTotp) fields.Children.Add(BuildTotp());
        if (d.HasPassword)
        {
            var strength = new StrengthBar(22) { Margin = new Thickness(0, 2, 0, 0) };
            strength.Set(d.Strength.Label, d.Strength.Bits);
            fields.Children.Add(FieldBlock(Strings.LabelStrength, strength, null));
        }
        if (d.Summary.Tags.Count > 0)
        {
            var tags = new WrapPanel { Orientation = Orientation.Horizontal };
            foreach (var t in d.Summary.Tags) tags.Children.Add(new Badge { Content = "#" + t, Margin = new Thickness(0, 2, 6, 2) });
            fields.Children.Add(FieldBlock(Strings.LabelTagsShort, tags, null));
        }
        if (fields.Children.Count > 0) _root.Children.Add(Section(fields));

        var meta = new StackPanel { Spacing = 3 };
        if (d.Summary.ExpiresAt > 0)
        {
            var state = QuickFilters.Expiry(d.Summary.ExpiresAt, DateTimeOffset.UtcNow);
            var expiry = Ui.Text(Strings.LabelExpiresAt + "：" + StampText.DateOnly(d.Summary.ExpiresAt)
                + (state == ExpiryState.Expired ? "（" + Strings.BadgeExpired + "）" : state == ExpiryState.Soon ? "（" + Strings.BadgeExpiring + "）" : ""),
                "caption", state is ExpiryState.Expired or ExpiryState.Soon ? "warning" : "tertiary");
            meta.Children.Add(expiry);
        }
        meta.Children.Add(Ui.Text(Strings.LabelModified + "：" + StampText.Local(d.Summary.ModifiedAt), "caption", "tertiary"));
        meta.Children.Add(Ui.Text(Strings.LabelCreated + "：" + StampText.Local(d.CreatedAt), "caption", "tertiary"));
        _root.Children.Add(meta);

        if (!d.Summary.InTrash) _root.Children.Add(BuildAttachments());
        // Restoring a version does not bring back attachments, so an SSH key's history is not offered.
        if (_history.Count > 0 && d.Ssh is null) _root.Children.Add(BuildHistory());

        RefreshTotpUi();
    }

    /// <summary>A rounded block holding field rows.</summary>
    private static Border Section(Control content)
    {
        var b = new Border { CornerRadius = new CornerRadius(10), BorderThickness = new Thickness(1), Padding = new Thickness(14, 4), Child = content };
        b[!BorderBrushProperty] = b.GetResourceObservable("Lumen.Brush.Border").ToBinding();
        return b;
    }

    /// <summary>Caption above, value below, action buttons on the right.</summary>
    private static Control FieldBlock(string label, Control value, Control? actions)
    {
        var grid = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto"), Margin = new Thickness(0, 8) };
        var left = new StackPanel { Spacing = 3, VerticalAlignment = VerticalAlignment.Center };
        left.Children.Add(Ui.Text(label, "caption", "tertiary"));
        left.Children.Add(value);
        grid.Children.Add(left);
        if (actions is not null)
        {
            actions.VerticalAlignment = VerticalAlignment.Center;
            actions.Margin = new Thickness(8, 0, -6, 0);
            Grid.SetColumn(actions, 1);
            grid.Children.Add(actions);
        }
        return grid;
    }

    private Control BuildRow(DetailRow row)
    {
        var now = DateTimeOffset.UtcNow;
        var shown = row.Concealed && _reveal.IsRevealed(row.Key, now) && _revealed.ContainsKey(row.Key);
        var text = DetailRows.Display(row, shown ? _revealed[row.Key] : null);

        var buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 2 };
        if (row.Concealed)
            buttons.Children.Add(Ui.IconButton(shown ? AppIcons.EyeOff : AppIcons.Of(IconKind.Eye), shown ? Strings.BtnHide : Strings.BtnReveal, () => _ = ToggleRevealAsync(row), "sm"));
        if (row.Kind == DetailKind.Link)
            buttons.Children.Add(Ui.IconButton(AppIcons.Of(IconKind.ExternalLink), Strings.BtnOpenUrl, () =>
            {
                if (_shell.OpenWebUrl(row.KnownValue) is { } error) _ = VaultUi.ErrorAsync(this, error);
            }, "sm"));
        if (row.CanCopy)
            buttons.Children.Add(Ui.IconButton(AppIcons.Of(IconKind.Copy), Strings.BtnCopyField, () => _ = CopyAsync(row.RevealField, row.Label), "sm"));

        var value = new SelectableTextBlock { Text = text, TextWrapping = TextWrapping.Wrap };
        if (row.Kind == DetailKind.Secret || (row.Concealed && shown)) value.Classes.Add("mono");
        if (row.Kind == DetailKind.Link) value[!SelectableTextBlock.ForegroundProperty] = value.GetResourceObservable("Lumen.Brush.AccentText").ToBinding();
        return FieldBlock(row.Label, value, buttons.Children.Count > 0 ? buttons : null);
    }

    private Control BuildTotp()
    {
        _ring = new TotpRing { Margin = new Thickness(0, 0, 10, 0), VerticalAlignment = VerticalAlignment.Center };
        _totpText = new TextBlock { FontSize = 22, FontWeight = FontWeight.SemiBold, VerticalAlignment = VerticalAlignment.Center, Classes = { "mono" }, LetterSpacing = 1 };
        _totpPaused = Ui.Text(Strings.TotpPaused, "caption", "tertiary");
        _totpPaused.IsVisible = false;
        var value = new StackPanel
        {
            Spacing = 2,
            Children = { new StackPanel { Orientation = Orientation.Horizontal, Children = { _ring, _totpText } }, _totpPaused },
        };
        var copy = Ui.IconButton(AppIcons.Of(IconKind.Copy), Strings.BtnCopyField, () => _ = CopyAsync("totp", Strings.TotpTitle), "sm");
        var actions = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4, Children = { copy } };
        if (_detail is { Summary.InTrash: false } detail)
            actions.Children.Add(Ui.IconButton(AppIcons.QrCode, "导出与迁移验证码", () =>
                _ = OtpTransferWindow.ExportAsync(VaultUi.OwnerOf(this), _shell, detail.Summary.Id), "sm"));
        return FieldBlock(Strings.TotpTitle, value, actions);
    }

    private Control BuildAttachments()
    {
        var panel = new StackPanel { Spacing = 6 };
        panel.Children.Add(Ui.Text(Strings.AttachmentsTitle, "caption", "strong", "secondary"));
        var d = _detail!;
        if (d.OtherAttachments.Count == 0) panel.Children.Add(Ui.Text(Strings.AttachmentsNone, "caption", "tertiary"));
        foreach (var a in d.OtherAttachments)
        {
            var line = new Grid { ColumnDefinitions = new ColumnDefinitions("Auto,*,Auto") };
            line.Children.Add(Ui.Icon(AppIcons.Paperclip, 14).With(i => { i.Margin = new Thickness(0, 0, 8, 0); i.VerticalAlignment = VerticalAlignment.Center; }));
            var name = new TextBlock { Text = a.Name + "  ·  " + Formatting.Bytes(a.Size), VerticalAlignment = VerticalAlignment.Center, TextTrimming = TextTrimming.CharacterEllipsis };
            Grid.SetColumn(name, 1);
            line.Children.Add(name);
            var buttons = new StackPanel
            {
                Orientation = Orientation.Horizontal,
                Spacing = 2,
                Children =
                {
                    Ui.IconButton(AppIcons.Save, Strings.BtnSaveAttachment, () => _ = SaveAttachmentAsync(a), "sm"),
                    Ui.IconButton(AppIcons.Of(IconKind.Trash), Strings.BtnRemoveAttachment, () => _ = RemoveAttachmentAsync(a), "sm"),
                },
            };
            Grid.SetColumn(buttons, 2);
            line.Children.Add(buttons);
            panel.Children.Add(line);
        }
        panel.Children.Add(Ui.Button(AppIcons.Of(IconKind.Plus), Strings.BtnAddAttachment, () => _ = AddAttachmentAsync(), "ghost", "sm"));
        return panel;
    }

    // ---- SSH keys --------------------------------------------------------------------------------

    private Control BuildSsh(SshKeyInfo k, bool inTrash)
    {
        var outer = new StackPanel { Spacing = 10 };
        if (SshText.Problem(k.Problem, k.Reason) is { Length: > 0 } problem)
        {
            outer.Children.Add(new InfoBar { Severity = Severity.Warning, IsOpen = true, IsClosable = false, Message = problem });
            if (k.Problem == "needs_passphrase" && !inTrash)
                outer.Children.Add(Ui.TextButton(Strings.SshBtnNormalize, () => _ = NormalizeAsync(k, needsPassphrase: true), "soft", "sm"));
        }

        var fields = new StackPanel { Spacing = 0 };
        if (k.TypeText.Length > 0)
            fields.Children.Add(FieldBlock(Strings.SshLabelType, Ui.Text(k.TypeText + "  ·  " + Strings.SshFormatName(k.SourceFormat)), null));
        if (k.Fingerprint.Length > 0)
            fields.Children.Add(FieldBlock(Strings.SshLabelFingerprint, SshUi.Mono(k.Fingerprint, wrap: false).With(t => t.TextTrimming = TextTrimming.CharacterEllipsis),
                Ui.IconButton(AppIcons.Of(IconKind.Copy), Strings.BtnCopyField, () => _ = SshUi.CopyAsync(_shell, k.Fingerprint, Strings.CopiedPlain), "sm")));
        if (k.Comment.Length > 0)
            fields.Children.Add(FieldBlock(Strings.SshLabelComment, new SelectableTextBlock { Text = k.Comment, TextWrapping = TextWrapping.Wrap }, null));
        if (k.PublicKey.Length > 0)
            fields.Children.Add(FieldBlock(Strings.SshLabelPublicKey, SshUi.Mono(k.PublicKey).With(t => t.MaxLines = 4),
                Ui.IconButton(AppIcons.Of(IconKind.Copy), Strings.SshBtnCopyPublic, () => _ = SshUi.CopyAsync(_shell, k.PublicKey, Strings.SshCopiedPublic), "sm")));
        if (fields.Children.Count > 0) outer.Children.Add(Section(fields));
        if (inTrash) return outer;

        // Agent: the switch, what the agent does with the key right now, when it was last used.
        var toggle = new ToggleSwitch { IsChecked = k.Agent, IsEnabled = k.Usable, VerticalAlignment = VerticalAlignment.Center };
        toggle.IsCheckedChanged += (_, _) => _ = SetAgentAsync(k, toggle.IsChecked == true);
        var state = _shell.SshState;
        var badge = !k.Agent ? SshUi.Badge(Strings.SshBadgeAgentOff, "")
            : k.Loaded && state.InGrace ? SshUi.Badge(Strings.SshBadgeGrace(DateTimeOffset.FromUnixTimeSeconds(state.ExpiresAt)), "warning")
            : k.Loaded ? SshUi.Badge(Strings.SshBadgeLoaded, "success")
            : SshUi.Badge(Strings.SshBadgeNotLoaded, "");
        var title = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Children = { Ui.Text(Strings.SshLabelAgent, "medium"), badge } };
        var info = new StackPanel { Spacing = 3, Children = { title, Ui.Text(Strings.SshAgentHint, "caption", "tertiary").With(t => t.TextWrapping = TextWrapping.Wrap) } };
        info.Children.Add(Ui.Text(k.LastUsedAt > 0 ? Strings.SshLastUsed(StampText.Local(k.LastUsedAt)) : Strings.SshNeverUsed, "caption", "tertiary"));
        var agentRow = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto"), Margin = new Thickness(0, 8) };
        agentRow.Children.Add(info);
        Grid.SetColumn(toggle, 1);
        agentRow.Children.Add(toggle);
        var agentBlock = new StackPanel { Spacing = 6, Children = { agentRow } };
        if (k.Agent && state.Supported && !state.Listening)
        {
            // The pane is narrow: the button goes under the text instead of squeezing it.
            agentBlock.Children.Add(new InfoBar { Severity = Severity.Info, IsOpen = true, IsClosable = false, Message = Strings.SshAgentNotRunning });
            agentBlock.Children.Add(Ui.TextButton(Strings.SshBtnEnableAgent, () => _ = EnableAgentAsync(), "soft", "sm").With(b => b.Margin = new Thickness(0, 0, 0, 8)));
        }
        if (k.Usable) agentBlock.Children.Add(BuildHosts(k));
        outer.Children.Add(Section(agentBlock));

        var actions = new WrapPanel { Orientation = Orientation.Horizontal };
        void Add(Button b) { b.Margin = new Thickness(0, 0, 8, 6); actions.Children.Add(b); }
        if (k.Usable)
        {
            Add(Ui.Button(AppIcons.Save, Strings.SshBtnWritePub, () => _ = WritePubAsync(k), "soft", "sm"));
            Add(Ui.Button(AppIcons.Key, Strings.SshBtnExport, () => _ = ExportAsync(k), "soft", "sm"));
            if (k.Encrypted) Add(Ui.Button(AppIcons.Shield, Strings.SshBtnNormalize, () => _ = NormalizeAsync(k, needsPassphrase: false), "soft", "sm"));
        }
        if (actions.Children.Count > 0) outer.Children.Add(actions);
        return outer;
    }

    /// <summary>Host binding: the servers the key is offered to, and a one-click limit to the ones it was used with.</summary>
    private Control BuildHosts(SshKeyInfo k)
    {
        var panel = new StackPanel { Spacing = 6, Margin = new Thickness(0, 4, 0, 8) };
        var head = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
        head.Children.Add(new StackPanel
        {
            Spacing = 2,
            Children = { Ui.Text(Strings.SshHostsTitle, "medium"), Ui.Text(Strings.SshHostsHint, "caption", "tertiary").With(t => t.TextWrapping = TextWrapping.Wrap) },
        });
        var edit = Ui.TextButton(Strings.SshHostsEdit, () => _ = EditHostsAsync(k), "soft", "sm");
        edit.VerticalAlignment = VerticalAlignment.Top;
        edit.Margin = new Thickness(8, 0, 0, 0);
        Grid.SetColumn(edit, 1);
        head.Children.Add(edit);
        panel.Children.Add(head);

        var chips = new WrapPanel { Orientation = Orientation.Horizontal };
        if (k.Hosts.Count == 0) chips.Children.Add(new Badge { Content = Strings.SshHostsAny, Margin = new Thickness(0, 2, 6, 2) });
        foreach (var h in k.Hosts)
        {
            var b = new Badge { Content = SshHosts.Display(h), Margin = new Thickness(0, 2, 6, 2) };
            b.Classes.Add("accent");
            ToolTip.SetTip(b, h);
            chips.Children.Add(b);
        }
        panel.Children.Add(chips);

        var used = SshHosts.Suggestions(_sshRecent, k.Fingerprint)
            .Where(h => !k.Hosts.Contains(h) && !k.Hosts.Contains(SshHosts.ExactHostPrefix + h, StringComparer.OrdinalIgnoreCase))
            .ToList();
        if (used.Count > 0)
        {
            var hosts = k.Hosts.Count == 0 ? used : k.Hosts.Concat(used).ToList();
            panel.Children.Add(Ui.Text(Strings.SshHostsSuggest(used), "caption", "secondary").With(t => t.TextWrapping = TextWrapping.Wrap));
            panel.Children.Add(Ui.TextButton(Strings.SshHostsUseSuggested, () => _ = SaveHostsAsync(k, hosts), "ghost", "sm"));
        }
        else if (k.Hosts.Count == 0)
            panel.Children.Add(Ui.Text(Strings.SshHostsUnboundNote, "caption", "tertiary").With(t => t.TextWrapping = TextWrapping.Wrap));
        return panel;
    }

    private async Task EditHostsAsync(SshKeyInfo k)
    {
        var hosts = await SshHostsWindow.ShowAsync(VaultUi.OwnerOf(this), _shell, k.Hosts);
        if (hosts is not null) await SaveHostsAsync(k, hosts);
    }

    private async Task SaveHostsAsync(SshKeyInfo k, IReadOnlyList<string> hosts)
    {
        var id = k.EntryId;
        var r = await _shell.SshCallAsync(a => a.SetHosts(id, hosts));
        if (!r.Ok) { await Fail(r.Error); return; }
        _shell.ShowStatus(Strings.SshHostsSaved, StatusKind.Success);
        if (_detail?.Id == id) await ShowAsync(id);
    }

    private async Task SetAgentAsync(SshKeyInfo k, bool on)
    {
        if (on == k.Agent) return;
        var id = k.EntryId;
        var r = await _shell.SshCallAsync(a => a.SetAgent(id, on));
        if (!r.Ok) await Fail(r.Error);
        if (_detail?.Id == id) await ShowAsync(id);
    }

    private async Task EnableAgentAsync()
    {
        if (await _shell.SetSshAgentAsync(true) is { } error) { await Fail(error); return; }
        if (_detail is not null) await ShowAsync(_detail.Id);
    }

    private async Task WritePubAsync(SshKeyInfo k)
    {
        var name = (k.KeyAttachment.Length > 0 ? k.KeyAttachment : "id_key") + ".pub";
        var path = await Ui.PickSaveFileAsync(this, Strings.SshBtnWritePub, name, SshUi.SshDir, "pub");
        if (path is null) return;
        var id = k.EntryId;
        var r = await _shell.SshCallAsync(a => a.WritePub(path, id));
        if (!r.Ok) { await Fail(r.Error); return; }
        switch (r.Value)
        {
            case "created": _shell.ShowStatus(Strings.SshPubWritten(path), StatusKind.Success); break;
            case "same": _shell.ShowStatus(Strings.SshPubSame, StatusKind.Info); break;
            default: await Fail(Strings.SshPubExists); break;
        }
    }

    private async Task ExportAsync(SshKeyInfo k)
    {
        var st = await _shell.VaultCallAsync(a => a.Status());
        await SshExportWindow.ShowAsync(VaultUi.OwnerOf(this), _shell, k, st.Ok && st.Value!.KeyFile);
    }

    private async Task NormalizeAsync(SshKeyInfo k, bool needsPassphrase)
    {
        var id = k.EntryId;
        var passphrase = "";
        if (needsPassphrase)
        {
            var entered = await Ui.PromptAsync(this, Strings.SshNormalizeTitle, Strings.SshImportPassphrase, "",
                v => string.IsNullOrEmpty(v) ? null : v, Strings.ErrSshNeedsPassphrase, Strings.SshBtnNormalize);
            if (entered is null) return;
            passphrase = entered;
        }
        else if (!await VaultUi.ConfirmAsync(this, Strings.SshNormalizeText, Strings.SshNormalizeTitle, Strings.SshBtnNormalize)) return;
        var r = await _shell.SshCallAsync(a => a.Normalize(id, passphrase));
        if (!r.Ok) { await Fail(r.Error); return; }
        _shell.ShowStatus(Strings.SshNormalizeDone, StatusKind.Success);
        if (_detail?.Id == id) await ShowAsync(id);
        DataChanged?.Invoke();
    }

    private Control BuildHistory()
    {
        var panel = new StackPanel { Spacing = 4 };
        foreach (var v in _history.OrderByDescending(x => x.Index))
        {
            var line = new Grid { ColumnDefinitions = new ColumnDefinitions("*,Auto") };
            line.Children.Add(new TextBlock
            {
                Text = StampText.Local(v.ModifiedAt) + "  ·  " + (v.Title.Length > 0 ? v.Title : Strings.Untitled) + (v.Username.Length > 0 ? "  ·  " + v.Username : ""),
                VerticalAlignment = VerticalAlignment.Center, TextTrimming = TextTrimming.CharacterEllipsis, FontSize = 12,
            });
            var restore = Ui.TextButton(Strings.BtnRestoreVersion, () => _ = RestoreVersionAsync(v), "ghost", "sm");
            Grid.SetColumn(restore, 1);
            line.Children.Add(restore);
            panel.Children.Add(line);
        }
        return new Expander
        {
            Header = Strings.HistoryTitle + " (" + _history.Count + ")", Content = panel,
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

    private async Task SaveAttachmentAsync(AttachmentInfo att)
    {
        if (_detail is null) return;
        var id = _detail.Id;
        var chosen = await VaultUi.PickSaveFileAsync(this, Strings.BtnSaveAttachment, att.Name);
        if (chosen is null) return;
        var r = await _shell.VaultCallAsync(a => a.AttachSave(id, att.Name, chosen));
        if (!r.Ok) { await Fail(r.Error!); return; }
        _shell.ShowStatus(Strings.AttachSaved + "：" + chosen, StatusKind.Success);
    }

    private async Task RemoveAttachmentAsync(AttachmentInfo att)
    {
        if (_detail is null || !await VaultUi.ConfirmAsync(this, Strings.AttachRemoveConfirm + "\n" + att.Name, yes: Strings.BtnRemoveAttachment, warning: true)) return;
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
        _shell.ShowStatus(Strings.HistoryRestored, StatusKind.Success);
    }
}
