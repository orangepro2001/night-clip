using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class DetailRowsTests
{
    private static EntryDetail Detail(string kind, string user = "", string url = "", bool hasPassword = false, string notes = "",
        params EntryFieldInfo[] fields) =>
        new(new EntrySummary("id", "g", kind, "T", user, url, Array.Empty<string>(), false, false, 0, 0, false), notes, fields,
            hasPassword, Array.Empty<AttachmentInfo>(), 0, 0, new PasswordStrength(0, "weak"));

    [Fact]
    public void Login_RowsInOrder_PasswordConcealed()
    {
        var rows = DetailRows.Build(Detail("login", "me", "https://x.org", true, "hello"));
        Assert.Equal(new[] { "username", "password", "url", "notes" }, rows.Select(r => r.Key).ToArray());
        var pw = rows[1];
        Assert.True(pw.Concealed);
        Assert.Equal(DetailKind.Secret, pw.Kind);
        Assert.Equal("password", pw.RevealField);
        Assert.False(rows[0].Concealed);
        Assert.Equal(DetailKind.Link, rows[2].Kind);
        Assert.False(rows[3].Concealed);   // notes of a login are plain text
    }

    [Fact]
    public void ApiKey_UsesItsOwnLabels()
    {
        var rows = DetailRows.Build(Detail("apikey", url: "https://api.x", hasPassword: true));
        Assert.Equal(Strings.LabelApiKeyValue, rows.First(r => r.Key == "password").Label);
        Assert.Equal(Strings.LabelServiceUrl, rows.First(r => r.Key == "url").Label);
    }

    [Fact]
    public void SecureNoteAndSecret_NotesAreConcealedByDefault()
    {
        Assert.True(DetailRows.Build(Detail("note", notes: "recovery codes")).Single().Concealed);
        Assert.True(DetailRows.Build(Detail("secret", notes: "x")).Single().Concealed);
        Assert.False(DetailRows.Build(Detail("apikey", notes: "x")).Single().Concealed);
    }

    [Fact]
    public void CustomFields_ProtectedAreMasked_UnprotectedShowTheirValue()
    {
        var rows = DetailRows.Build(Detail("apikey", fields: new[]
        {
            new EntryFieldInfo("Region", "eu", false, true),
            new EntryFieldInfo("Token", "", true, true),
            new EntryFieldInfo("Empty", "", true, false),
        }));
        var region = rows.First(r => r.Key == "field:Region");
        Assert.False(region.Concealed);
        Assert.Equal("eu", DetailRows.Display(region, null));
        var token = rows.First(r => r.Key == "field:Token");
        Assert.True(token.Concealed);
        Assert.Equal("field:Token", token.RevealField);
        Assert.Equal(DetailRow.Mask, DetailRows.Display(token, null));
        Assert.Equal("tok", DetailRows.Display(token, "tok"));
        var empty = rows.First(r => r.Key == "field:Empty");
        Assert.False(empty.CanCopy);
        Assert.Equal(Strings.Dash, DetailRows.Display(empty, null));
    }

    [Fact]
    public void Notes_NeedNoFetch_Secrets_Do()
    {
        var rows = DetailRows.Build(Detail("note", hasPassword: true, notes: "n"));
        Assert.False(DetailRows.NeedsFetch(rows.First(r => r.Key == "notes")));
        Assert.True(DetailRows.NeedsFetch(rows.First(r => r.Key == "password")));
    }

    [Fact]
    public void Nothing_ToShow_ForAnEmptyEntry() => Assert.Empty(DetailRows.Build(Detail("login")));
}

public class RevealStateTests
{
    private static readonly DateTimeOffset T0 = new(2030, 1, 1, 0, 0, 0, TimeSpan.Zero);

    [Fact]
    public void Revealed_For15Seconds_ThenMaskedAgain()
    {
        var s = new RevealState();
        s.SelectEntry("e1");
        s.Reveal("password", T0);
        Assert.True(s.IsRevealed("password", T0.AddSeconds(14.9)));
        Assert.False(s.IsRevealed("password", T0.AddSeconds(15)));
        Assert.Equal(new[] { "password" }, s.Expired(T0.AddSeconds(15)).ToArray());
        Assert.Empty(s.Expired(T0.AddSeconds(16)));   // reported once
        Assert.False(s.Any);
    }

    [Fact]
    public void ChangingTheEntry_MasksEverything()
    {
        var s = new RevealState();
        s.SelectEntry("e1");
        s.Reveal("password", T0);
        s.Reveal("notes", T0);
        s.SelectEntry("e1");   // same entry: unchanged
        Assert.True(s.IsRevealed("password", T0));
        s.SelectEntry("e2");
        Assert.False(s.IsRevealed("password", T0));
        Assert.False(s.IsRevealed("notes", T0));
    }

    [Fact]
    public void HideAndClear()
    {
        var s = new RevealState();
        s.SelectEntry("e");
        s.Reveal("a", T0);
        s.Reveal("b", T0);
        s.Hide("a");
        Assert.False(s.IsRevealed("a", T0));
        Assert.True(s.IsRevealed("b", T0));
        s.Clear();
        Assert.False(s.Any);
        Assert.Equal("", s.EntryId);
    }

    [Fact]
    public void RevealingAgain_RestartsTheClock()
    {
        var s = new RevealState();
        s.SelectEntry("e");
        s.Reveal("a", T0);
        s.Reveal("a", T0.AddSeconds(10));
        Assert.True(s.IsRevealed("a", T0.AddSeconds(20)));
    }
}

public class TotpCountdownTests
{
    private static readonly DateTimeOffset T0 = new(2030, 1, 1, 0, 0, 0, TimeSpan.Zero);

    [Fact]
    public void CountsDownLocally_AndAsksForANewCodeAtZero()
    {
        var c = new TotpCountdown();
        Assert.True(c.NeedsRefresh(T0));
        c.Update(new TotpCode("123456", 20), T0);
        Assert.Equal(20, c.SecondsLeft(T0));
        Assert.Equal(15, c.SecondsLeft(T0.AddSeconds(5)));
        Assert.Equal(1, c.SecondsLeft(T0.AddSeconds(19.2)));   // rounded up
        Assert.False(c.NeedsRefresh(T0.AddSeconds(19)));
        Assert.True(c.NeedsRefresh(T0.AddSeconds(20)));
        Assert.Equal(0, c.SecondsLeft(T0.AddSeconds(25)));
    }

    [Fact]
    public void RingFraction_UsesThe30SecondPeriod_ByDefault()
    {
        var c = new TotpCountdown();
        c.Update(new TotpCode("123456", 30), T0);
        Assert.Equal(1.0, c.Fraction(T0), 3);
        Assert.Equal(0.5, c.Fraction(T0.AddSeconds(15)), 3);
        Assert.Equal(0.0, c.Fraction(T0.AddSeconds(31)), 3);
    }

    [Fact]
    public void LongerPeriods_AreInferredFromTheSecondsLeft()
    {
        var c = new TotpCountdown();
        c.Update(new TotpCode("123456", 45), T0);   // more than 30 left: a 60 s period
        Assert.Equal(0.75, c.Fraction(T0), 3);
        var d = new TotpCountdown();
        d.Update(new TotpCode("123456", 100), T0);  // 120 s
        Assert.Equal(100 / 120.0, d.Fraction(T0), 3);
    }

    [Fact]
    public void Clear_ForgetsTheCode()
    {
        var c = new TotpCountdown();
        c.Update(new TotpCode("123456", 10), T0);
        c.Clear();
        Assert.False(c.HasCode);
        Assert.Equal("", c.Code);
        Assert.True(c.NeedsRefresh(T0));
    }

    [Theory]
    [InlineData("123456", "123 456")]
    [InlineData("12345678", "1234 5678")]
    [InlineData("1234567", "123 4567")]
    [InlineData("", "")]
    public void Group(string code, string expected) => Assert.Equal(expected, TotpCountdown.Group(code));

    [Fact]
    public void RingIsRedInTheLastFiveSeconds()
    {
        Assert.Equal(0xFFD93025u, TotpCountdown.ColorArgb(5));
        Assert.Equal(0xFF1A73E8u, TotpCountdown.ColorArgb(6));
    }
}

public class ClipboardClearTests
{
    [Fact]
    public void ClearsWhenTheSequenceNumberIsUnchanged() =>
        Assert.Equal(ClearDecision.Clear, ClipboardClearPolicy.Decide(41, 41, 20));

    [Fact]
    public void DoesNotClear_WhenTheUserCopiedSomethingElse() =>
        Assert.Equal(ClearDecision.SkipChanged, ClipboardClearPolicy.Decide(41, 42, 20));

    [Fact]
    public void DoesNotClear_WhenClearingIsOff() =>
        Assert.Equal(ClearDecision.SkipDisabled, ClipboardClearPolicy.Decide(41, 41, 0));

    [Theory]
    [InlineData(0u, 5u)]
    [InlineData(5u, 0u)]
    public void DoesNotClear_WhenTheSequenceNumberIsUnknown(uint ours, uint current) =>
        Assert.Equal(ClearDecision.SkipUnknown, ClipboardClearPolicy.Decide(ours, current, 20));

    [Fact]
    public void OnLock_ClearsOurSecret_EvenWithoutATimer()
    {
        Assert.Equal(ClearDecision.Clear, ClipboardClearPolicy.DecideOnLock(7, 7));
        Assert.Equal(ClearDecision.SkipChanged, ClipboardClearPolicy.DecideOnLock(7, 8));
    }

    [Fact]
    public void Guard_ArmsOnWrite_AndIsSpentAfterwards()
    {
        var g = new ClipboardClearGuard();
        Assert.False(g.Armed);
        Assert.Equal(ClearDecision.SkipUnknown, g.OnTimer(1));   // nothing of ours on the clipboard
        g.Arm(10, 20);
        Assert.True(g.Armed);
        Assert.Equal(ClearDecision.Clear, g.OnTimer(10));
        Assert.Equal(ClearDecision.SkipChanged, g.OnTimer(11));
        g.Disarm();
        Assert.Equal(ClearDecision.SkipUnknown, g.OnTimer(10));
    }

    [Fact]
    public void Guard_OnLock_IgnoresTheClearSetting()
    {
        var g = new ClipboardClearGuard();
        g.Arm(3, 0);   // "never clear automatically"
        Assert.Equal(ClearDecision.Clear, g.OnLock(3));
        Assert.Equal(ClearDecision.SkipDisabled, g.OnTimer(3));
    }

    [Fact]
    public void Guard_RearmingReplacesTheEarlierWrite()
    {
        var g = new ClipboardClearGuard();
        g.Arm(3, 20);
        g.Arm(9, 20);   // a second copy
        Assert.Equal(ClearDecision.SkipChanged, g.OnTimer(3));
        Assert.Equal(ClearDecision.Clear, g.OnTimer(9));
    }
}

public class LockPolicyTests
{
    [Theory]
    [InlineData(LockTrigger.SessionLock, true)]
    [InlineData(LockTrigger.SessionLogoff, true)]
    [InlineData(LockTrigger.UserSwitch, true)]
    [InlineData(LockTrigger.RemoteDisconnect, true)]
    [InlineData(LockTrigger.Suspend, true)]
    [InlineData(LockTrigger.SessionUnlock, false)]
    [InlineData(LockTrigger.Resume, false)]
    [InlineData(LockTrigger.Other, false)]
    public void ShouldLock(LockTrigger t, bool expected) => Assert.Equal(expected, LockPolicy.ShouldLock(t));

    [Fact]
    public void LockedNotes_ExplainAutomaticLocks()
    {
        Assert.Equal(Strings.LockedIdle, VaultScreens.LockedNote("idle"));
        Assert.Equal(Strings.LockedSession, VaultScreens.LockedNote("session"));
        Assert.Equal("", VaultScreens.LockedNote("user"));
        Assert.Equal("", VaultScreens.LockedNote(null));
        Assert.Equal(Strings.LockedOther, VaultScreens.LockedNote("something-new"));
    }

    [Fact]
    public void LockedEvent_Parses()
    {
        var ev = CoreEventParser.Parse("""{"type":"vault.locked","reason":"idle"}""");
        var locked = Assert.IsType<VaultLockedEvent>(ev);
        Assert.Equal("idle", locked.Reason);
        Assert.Equal("vault.locked", locked.LogLine);   // type only: nothing else is ever logged
    }
}

public class BackupLogicTests
{
    [Theory]
    [InlineData("save", "保存之前")]
    [InlineData("delete", "删除之前")]
    [InlineData("import", "导入之前")]
    [InlineData("restore", "恢复之前")]
    [InlineData("rekey", "更改主密码之前")]
    [InlineData("merge", "合并之前")]
    [InlineData("create", "创建库时")]
    [InlineData("upgrade", "升级格式之前")]
    [InlineData("manual", "手动备份")]
    [InlineData("previous", "上一代文件")]
    [InlineData("future-reason", "future-reason")]
    public void Reasons_AreInChinese(string reason, string expected) => Assert.Equal(expected, BackupLabels.Reason(reason));

    [Fact]
    public void BackupRow_FormatsTimeSizeAndReason()
    {
        var row = BackupRow.From(new BackupInfo("b.kdbx", 1700000000, 2048, "manual"), DateTimeOffset.UtcNow, TimeZoneInfo.Utc);
        Assert.Equal("2023-11-14 22:13", row.TimeText);
        Assert.Equal("手动备份", row.ReasonText);
        Assert.Equal("2.0 KB", row.SizeText);
        Assert.Equal("b.kdbx", row.Name);
        Assert.Equal(Strings.Dash, BackupRow.From(new BackupInfo("x", 0, 0, ""), DateTimeOffset.UtcNow).TimeText);
    }

    [Fact]
    public void RestoreModes_WireNamesAndReplaceConfirmation()
    {
        Assert.Equal("entries", RestoreModes.Wire(RestoreMode.Entries));
        Assert.Equal("merge", RestoreModes.Wire(RestoreMode.Merge));
        Assert.Equal("replace", RestoreModes.Wire(RestoreMode.Replace));
        Assert.True(RestoreModes.ReplaceConfirmed(Strings.RestoreReplaceWord));
        Assert.True(RestoreModes.ReplaceConfirmed("  " + Strings.RestoreReplaceWord + " "));
        Assert.False(RestoreModes.ReplaceConfirmed(""));
        Assert.False(RestoreModes.ReplaceConfirmed("yes"));
        Assert.Equal(3, Enum.GetValues<RestoreMode>().Select(RestoreModes.Label).Distinct().Count());
    }

    private static RestorePreview Preview() => new(
        new[] { new DiffItem("b2", "Zeta", 1), new DiffItem("b1", "alpha", 1) },
        new[] { new DiffItem("c1", "OnlyHere", 1) },
        new[] { new DiffItem("x1", "Changed", 1) }, 4);

    [Fact]
    public void Diff_GroupsOrderedByTitle_OnlyBackupAndChangedAreSelectable()
    {
        var sel = new RestoreSelection(Preview());
        var groups = sel.Groups();
        Assert.Equal(new[] { DiffSection.OnlyInBackup, DiffSection.Changed, DiffSection.OnlyInCurrent }, groups.Select(g => g.Section).ToArray());
        Assert.Equal(new[] { "alpha", "Zeta" }, groups[0].Items.Select(i => i.Title).ToArray());
        Assert.True(groups[0].Selectable && groups[1].Selectable);
        Assert.False(groups[2].Selectable);
        Assert.Contains("4", sel.Summary);
        Assert.False(sel.IsEmpty);
    }

    [Fact]
    public void Selection_DefaultsToEverythingRestorable_AndIgnoresOnlyInCurrent()
    {
        var sel = new RestoreSelection(Preview());
        Assert.Equal(new[] { "b1", "b2", "x1" }, sel.SelectedIds.ToArray());
        sel.Set("c1", true);                  // exists only in the current vault: cannot be restored
        Assert.DoesNotContain("c1", sel.SelectedIds);
        sel.Set("b1", false);
        Assert.Equal(new[] { "b2", "x1" }, sel.SelectedIds.ToArray());
        sel.SetAll(DiffSection.OnlyInBackup, false);
        Assert.Equal(new[] { "x1" }, sel.SelectedIds.ToArray());
        sel.SetAll(DiffSection.Changed, false);
        Assert.False(sel.CanRestore(RestoreMode.Entries));
        Assert.True(sel.CanRestore(RestoreMode.Merge));
        Assert.True(sel.CanRestore(RestoreMode.Replace));
    }

    [Fact]
    public void Diff_EmptyPreview()
    {
        var sel = new RestoreSelection(new RestorePreview(Array.Empty<DiffItem>(), Array.Empty<DiffItem>(), Array.Empty<DiffItem>(), 9));
        Assert.True(sel.IsEmpty);
        Assert.Empty(sel.Groups());
    }

    [Fact]
    public void Stats_Text() => Assert.Contains("新增 1", StatsText.Describe(new MergeStats(1, 2, 3, 4, 5, 6)));
}

public class HealthViewTests
{
    [Fact]
    public void OnlySectionsWithEntriesAppear_WithFriendlyInfo()
    {
        var r = new HealthReport(
            new[] { new HealthIssue("a", "A", "very_weak") },
            new[] { new HealthIssue("b", "B", "3"), new HealthIssue("c", "C", "3") },
            Array.Empty<HealthIssue>(), Array.Empty<HealthIssue>(), 2, 10);
        var groups = HealthView.Groups(r);
        Assert.Equal(new[] { HealthSection.Weak, HealthSection.Reused }, groups.Select(g => g.Section).ToArray());
        Assert.Equal(Strings.StrengthVeryWeak, groups[0].Items[0].Info);
        Assert.Equal(Strings.HealthSharedBy(3), groups[1].Items[0].Info);
        Assert.Contains("10", HealthView.Summary(r));
    }

    [Fact]
    public void AllGood()
    {
        var r = new HealthReport(Array.Empty<HealthIssue>(), Array.Empty<HealthIssue>(), Array.Empty<HealthIssue>(), Array.Empty<HealthIssue>(), 0, 7);
        Assert.Empty(HealthView.Groups(r));
        Assert.Equal(Strings.HealthAllGood(7), HealthView.Summary(r));
    }

    [Fact]
    public void UntitledEntriesGetAPlaceholder()
    {
        var r = new HealthReport(Array.Empty<HealthIssue>(), Array.Empty<HealthIssue>(), Array.Empty<HealthIssue>(), new[] { new HealthIssue("x", "", "") }, 0, 1);
        Assert.Equal(Strings.Untitled, HealthView.Groups(r).Single().Items.Single().Title);
    }
}
