using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class QuickFilterTests
{
    private static readonly DateTimeOffset Now = DateTimeOffset.FromUnixTimeSeconds(1_800_000_000);

    private static EntrySummary E(string id, string title = "T", string kind = "login", string group = "g", long expires = 0, string user = "", string url = "", bool totp = false, bool attach = false) =>
        new(id, group, kind, title, user, url, Array.Empty<string>(), totp, attach, 0, expires, false);

    [Theory]
    [InlineData(QuickFilter.All, "", false)]
    [InlineData(QuickFilter.Logins, "login", false)]
    [InlineData(QuickFilter.ApiKeys, "apikey", false)]
    [InlineData(QuickFilter.Secrets, "secret", false)]
    [InlineData(QuickFilter.Notes, "note", false)]
    [InlineData(QuickFilter.Expiring, "", false)]
    [InlineData(QuickFilter.Trash, "", true)]
    public void ToQuery_MapsFilters(QuickFilter f, string kind, bool trash)
    {
        var q = QuickFilters.ToQuery(f, "  bank ");
        Assert.Equal(kind, q.Kind);
        Assert.Equal(trash, q.OnlyTrash);
        Assert.Equal("bank", q.Query);
        Assert.Equal("", q.GroupId);   // groups are filtered on the client
    }

    [Fact]
    public void AllSevenFiltersHaveDistinctLabels() =>
        Assert.Equal(7, QuickFilters.All.Select(QuickFilters.Label).Distinct().Count());

    [Fact]
    public void Expiry_States()
    {
        var t = Now.ToUnixTimeSeconds();
        Assert.Equal(ExpiryState.None, QuickFilters.Expiry(0, Now));
        Assert.Equal(ExpiryState.Expired, QuickFilters.Expiry(t - 1, Now));
        Assert.Equal(ExpiryState.Soon, QuickFilters.Expiry(t + 1, Now));
        Assert.Equal(ExpiryState.Soon, QuickFilters.Expiry(t + 30 * 86400L, Now));
        Assert.Equal(ExpiryState.Ok, QuickFilters.Expiry(t + 30 * 86400L + 1, Now));
    }

    [Fact]
    public void Apply_ExpiringKeepsSoonAndExpired_SoonestFirst()
    {
        var t = Now.ToUnixTimeSeconds();
        var list = new[]
        {
            E("far", expires: t + 90 * 86400L), E("none"), E("soon", "B", expires: t + 5 * 86400L), E("gone", "A", expires: t - 10 * 86400L),
        };
        var r = QuickFilters.Apply(list, QuickFilter.Expiring, null, "", Now);
        Assert.Equal(new[] { "gone", "soon" }, r.Select(e => e.Id).ToArray());
    }

    [Fact]
    public void Apply_GroupFilter_IncludesTheGivenIds()
    {
        var list = new[] { E("1", group: "a"), E("2", group: "b"), E("3", group: "c") };
        var r = QuickFilters.Apply(list, QuickFilter.All, new HashSet<string> { "a", "c" }, "", Now);
        Assert.Equal(new[] { "1", "3" }, r.Select(e => e.Id).ToArray());
    }

    [Fact]
    public void Apply_SearchLooksThroughTheWholeVault_IgnoringTheGroup()
    {
        var list = new[] { E("1", group: "a"), E("2", group: "b") };
        Assert.Equal(2, QuickFilters.Apply(list, QuickFilter.All, new HashSet<string> { "a" }, "x", Now).Count);
    }

    [Fact]
    public void Apply_TrashIgnoresGroups()
    {
        var list = new[] { E("1", group: "a"), E("2", group: "b") };
        Assert.Equal(2, QuickFilters.Apply(list, QuickFilter.Trash, new HashSet<string> { "a" }, "", Now).Count);
    }
}

public class GroupTreeTests
{
    private static GroupInfo G(string id, string parent, string name, int entries = 0, bool trash = false) =>
        new(id, parent, name, name, entries, trash);

    private static readonly GroupInfo[] Groups =
    {
        G("root", "", "Root", 2), G("work", "root", "Work", 1), G("srv", "work", "Servers", 3), G("home", "root", "Home"),
        G("bin", "root", "Recycle Bin", 5, trash: true), G("old", "bin", "Old"),
    };

    [Fact]
    public void Flatten_DepthFirst_SiblingsByName_NoRootNoTrash()
    {
        var nodes = GroupTree.Flatten(Groups);
        Assert.Equal(new[] { "home", "work", "srv" }, nodes.Select(n => n.Id).ToArray());
        Assert.Equal(new[] { 0, 0, 1 }, nodes.Select(n => n.Depth).ToArray());
        Assert.Equal("   Servers", nodes[2].Display);
    }

    [Fact]
    public void WithDescendants_CollectsSubgroups()
    {
        Assert.Equal(new[] { "srv", "work" }, GroupTree.WithDescendants(Groups, "work").OrderBy(x => x).ToArray());
        Assert.Equal(new[] { "home" }, GroupTree.WithDescendants(Groups, "home").ToArray());
    }

    [Fact]
    public void Choices_StartWithTheRoot_AndIndentTheRest()
    {
        var c = GroupTree.Choices(Groups);
        Assert.Equal("root", c[0].Id);
        Assert.Equal(Strings.GroupRoot, c[0].Name);
        Assert.Equal(4, c.Count);
        Assert.Equal(2, c.First(x => x.Id == "srv").Depth);
    }

    [Fact]
    public void DamagedFile_CycleDoesNotHang()
    {
        var cyc = new[] { G("root", "", "Root"), G("a", "b", "A"), G("b", "a", "B") };
        var nodes = GroupTree.Flatten(cyc);   // must terminate; the orphans are shown at most once
        Assert.True(nodes.Count <= 2);
    }

    [Fact]
    public void Empty_IsEmpty() => Assert.Empty(GroupTree.Flatten(Array.Empty<GroupInfo>()));
}

public class EntryRowTests
{
    private static readonly DateTimeOffset Now = DateTimeOffset.FromUnixTimeSeconds(1_800_000_000);

    private static EntrySummary E(string kind = "login", string title = "T", string user = "", string url = "", bool totp = false, long expires = 0, bool attach = false) =>
        new("id", "g", kind, title, user, url, Array.Empty<string>(), totp, attach, 0, expires, false);

    [Fact]
    public void Row_ShowsKindIconTotpBadgeAndSubtitle()
    {
        var r = new EntryRow(E(user: "me", url: "https://www.example.com/login", totp: true), Now);
        Assert.Equal(KindTemplate.GlyphFor("login"), r.Glyph);
        Assert.Equal(Strings.BadgeTotp, r.TotpBadge);
        Assert.Equal("me  ·  www.example.com", r.Subtitle);
        Assert.Equal("", r.ExpiryBadge);
    }

    [Fact]
    public void Row_ExpiryBadges()
    {
        var t = Now.ToUnixTimeSeconds();
        Assert.Equal(Strings.BadgeExpired, new EntryRow(E(expires: t - 5), Now).ExpiryBadge);
        Assert.Equal(Strings.BadgeExpiring, new EntryRow(E(expires: t + 86400), Now).ExpiryBadge);
        Assert.Equal("", new EntryRow(E(expires: t + 400 * 86400L), Now).ExpiryBadge);
    }

    [Fact]
    public void Row_UntitledAndAttachments()
    {
        var r = new EntryRow(E(title: "", attach: true), Now);
        Assert.Equal(Strings.Untitled, r.Title);
        Assert.NotEqual("", r.AttachBadge);
        Assert.Equal("", new EntryRow(E(), Now).TotpBadge);
    }

    [Fact]
    public void Subtitle_NonWebUrlIsShownShortened()
    {
        Assert.Equal("", EntryRow.Describe(E()));
        Assert.Equal("ssh://host", EntryRow.Describe(E(url: "ssh://host")));
        var longText = new string('x', 100);
        Assert.Equal(48, UrlText.Short(longText).Length);
    }

    [Fact]
    public void Toolbar_StateByFilterAndSelection()
    {
        var none = ToolbarState.For(QuickFilter.All, false);
        Assert.False(none.CanEdit || none.CanDelete || none.CanRestore || none.CanEmptyTrash);
        var sel = ToolbarState.For(QuickFilter.All, true);
        Assert.True(sel.CanEdit && sel.CanDelete);
        Assert.False(sel.CanRestore);
        Assert.Equal(Strings.BtnDeleteEntry, sel.DeleteLabel);
        var trash = ToolbarState.For(QuickFilter.Trash, true);
        Assert.True(trash.CanRestore && trash.CanDelete && trash.CanEmptyTrash);
        Assert.False(trash.CanEdit);
        Assert.Equal(Strings.BtnDeletePermanent, trash.DeleteLabel);
    }

    [Fact]
    public void ListTexts_Variants()
    {
        Assert.Equal(Strings.VaultNoMatches, ListTexts.Empty(QuickFilter.All, true));
        Assert.Equal(Strings.VaultTrashEmpty, ListTexts.Empty(QuickFilter.Trash, false));
        Assert.Equal(Strings.VaultEmpty, ListTexts.Empty(QuickFilter.Logins, false));
        Assert.Equal("3 项", ListTexts.Count(3));
    }
}

public class SmallVaultLogicTests
{
    [Theory]
    [InlineData("  Work  ", "Work")]
    [InlineData("", null)]
    [InlineData("   ", null)]
    [InlineData("a\nb", null)]
    [InlineData("a\0b", null)]
    public void GroupNameRules(string input, string? expected) => Assert.Equal(expected, GroupNames.Normalize(input));

    [Fact]
    public void GroupNameRules_LimitIs128Bytes()
    {
        Assert.NotNull(GroupNames.Normalize(new string('x', 128)));
        Assert.Null(GroupNames.Normalize(new string('x', 129)));
        Assert.Null(GroupNames.Normalize(new string('中', 43)));   // 129 bytes
        Assert.NotNull(GroupNames.Normalize(new string('中', 42)));
    }

    [Fact]
    public void SearchInput_TrimsAndCaps()
    {
        Assert.Equal("a b", SearchInput.Normalize("  a b "));
        Assert.Equal("", SearchInput.Normalize(null));
        Assert.Equal(SearchInput.MaxChars, SearchInput.Normalize(new string('x', 500)).Length);
    }

    [Fact]
    public void TotpActivityPolicy_PollsOnlyWhileTheUserIsActive()
    {
        var now = DateTimeOffset.UtcNow;
        Assert.Equal(TimeSpan.FromSeconds(60), TotpActivityPolicy.ActiveWindow(86400));
        Assert.Equal(TimeSpan.FromSeconds(33), TotpActivityPolicy.ActiveWindow(100));
        Assert.Equal(TimeSpan.FromSeconds(10), TotpActivityPolicy.ActiveWindow(30));
        Assert.True(TotpActivityPolicy.ShouldPoll(now.AddSeconds(-5), now, 300));
        Assert.False(TotpActivityPolicy.ShouldPoll(now.AddSeconds(-90), now, 300));
        Assert.False(TotpActivityPolicy.ShouldPoll(now.AddSeconds(-11), now, 30));
    }

    [Fact]
    public void IdleKeepAlive_PingsOnlyWithFreshInput_AndNotTooOften()
    {
        var t = DateTimeOffset.UtcNow;
        Assert.Equal(TimeSpan.FromSeconds(100), IdleKeepAlive.Interval(300));
        Assert.Equal(TimeSpan.FromSeconds(10), IdleKeepAlive.Interval(30));
        Assert.Equal(TimeSpan.FromSeconds(120), IdleKeepAlive.Interval(86400));
        // typing now, last ping long ago: ping
        Assert.True(IdleKeepAlive.ShouldPing(t, t.AddMinutes(-10), t, 300));
        // typing, but pinged a moment ago
        Assert.False(IdleKeepAlive.ShouldPing(t, t.AddSeconds(-20), t, 300));
        // no input since the last ping: never ping, so an unattended window still locks
        Assert.False(IdleKeepAlive.ShouldPing(t.AddMinutes(-5), t.AddMinutes(-1), t, 300));
        Assert.False(IdleKeepAlive.ShouldPing(DateTimeOffset.MinValue, DateTimeOffset.MinValue, t, 300));
    }

    [Fact]
    public void AttachmentRules_Limit5MiB()
    {
        Assert.Null(AttachmentRules.Check(5L << 20));
        Assert.Equal(Strings.AttachTooBig, AttachmentRules.Check((5L << 20) + 1));
    }

    [Fact]
    public void StampText_LocalAndEmpty()
    {
        var utc = TimeZoneInfo.Utc;
        Assert.Equal("", StampText.Local(0, utc));
        Assert.Equal("2023-11-14 22:13", StampText.Local(1700000000, utc));
        Assert.Equal("2023-11-14", StampText.DateOnly(1700000000, utc));
    }

    [Fact]
    public void VaultResult_SuccessAndFailure()
    {
        var ok = VaultResult<int>.Success(5);
        Assert.True(ok.Ok);
        Assert.Equal(5, ok.Value);
        var bad = VaultResult<int>.Fail("RATE_LIMITED", 9);
        Assert.False(bad.Ok);
        Assert.Equal("RATE_LIMITED", bad.ErrorCode);
        Assert.Contains("9", bad.Error);
        Assert.Equal(9, bad.RetryAfterSeconds);
    }
}
