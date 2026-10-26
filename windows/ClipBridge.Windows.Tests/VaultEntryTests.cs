using System.Text.Json;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class EntryDraftTests
{
    private static EntryDraft Login() => new()
    {
        Kind = "login", Title = " Bank ", Username = "me", Password = "pw", Url = " https://bank.example ", Notes = "n",
        TagsText = "b, a ;b,,", Totp = "JBSWY3DPEHPK3PXP", ExpiresAt = 100,
        Fields = { new DraftField { Name = "Key ID", Value = "K", Protected = false }, new DraftField { Name = "PIN", Value = "1234", Protected = true } },
    };

    [Fact]
    public void ToParams_ShapeAndTrimming()
    {
        var json = JsonSerializer.SerializeToElement(Login().ToParams(includeGroup: false));
        Assert.Equal("login", json.GetProperty("kind").GetString());
        Assert.Equal("Bank", json.GetProperty("title").GetString());
        Assert.Equal("https://bank.example", json.GetProperty("url").GetString());
        Assert.Equal(new[] { "b", "a" }, json.GetProperty("tags").EnumerateArray().Select(t => t.GetString()).ToArray());
        var fields = json.GetProperty("fields").EnumerateArray().ToList();
        Assert.Equal(2, fields.Count);
        Assert.True(fields[1].GetProperty("protected").GetBoolean());
        Assert.Equal(100, json.GetProperty("expires_at").GetInt64());
        Assert.False(json.TryGetProperty("group_id", out _));
    }

    [Fact]
    public void ToParams_SendsHiddenFieldsToo_SoUpdateNeverEraseThem()
    {
        // A "note" entry that somehow has a username: the form hides the box, but the value must survive an update.
        var d = new EntryDraft { Kind = "note", Title = "N", Username = "kept", Password = "kept-too" };
        var json = JsonSerializer.SerializeToElement(d.ToParams(false));
        Assert.Equal("kept", json.GetProperty("username").GetString());
        Assert.Equal("kept-too", json.GetProperty("password").GetString());
        Assert.True(FormVisibility.For(d).Username);   // and the form shows the box because it holds a value
    }

    [Fact]
    public void ToParams_DropsEmptyFieldRows_AndIncludesGroupForNewEntries()
    {
        var d = new EntryDraft { Title = "T", GroupId = "g", Fields = { new DraftField(), new DraftField { Name = "  ", Value = "" } } };
        var json = JsonSerializer.SerializeToElement(d.ToParams(true));
        Assert.Empty(json.GetProperty("fields").EnumerateArray());
        Assert.Equal("g", json.GetProperty("group_id").GetString());
    }

    [Fact]
    public void ParseTags_DedupesCaseInsensitively_AndSplitsOnBothSeparators()
    {
        Assert.Equal(new[] { "a", "B", "c d" }, EntryDraft.ParseTags("a, A;B ,c d,，"));
        Assert.Empty(EntryDraft.ParseTags(" ,; "));
    }

    [Fact]
    public void SameContentAs_DetectsEveryKindOfChange()
    {
        var a = Login();
        Assert.True(a.SameContentAs(a.Clone()));
        var changes = new List<Action<EntryDraft>>
        {
            d => d.Title += "x", d => d.Kind = "secret", d => d.Username += "x", d => d.Password += "x", d => d.Url += "x",
            d => d.Notes += "x", d => d.Totp += "A", d => d.TagsText += ",new", d => d.ExpiresAt = 0,
            d => d.Fields[0].Value += "x", d => d.Fields[1].Protected = false, d => d.Fields.RemoveAt(0), d => d.Fields[0].Name = "Other",
        };
        foreach (var change in changes)
        {
            var b = a.Clone();
            change(b);
            Assert.False(a.SameContentAs(b));
        }
    }

    [Fact]
    public void SameContentAs_IgnoresTagFormatting_AndIdentity()
    {
        var a = Login();
        var b = a.Clone();
        b.TagsText = "b,a";
        b.Id = "other";
        b.GroupId = "elsewhere";
        Assert.True(a.SameContentAs(b));
    }

    [Fact]
    public void Clone_IsDeep()
    {
        var a = Login();
        var b = a.Clone();
        b.Fields[0].Value = "changed";
        Assert.Equal("K", a.Fields[0].Value);
    }
}

public class EntryValidatorTests
{
    private static IReadOnlyList<ValidationIssue> V(Action<EntryDraft> edit)
    {
        var d = new EntryDraft { Title = "T" };
        edit(d);
        return EntryValidator.Validate(d);
    }

    [Fact]
    public void ValidEntry_HasNoIssues() => Assert.Empty(V(_ => { }));

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public void TitleIsRequired(string title) =>
        Assert.Contains(V(d => d.Title = title), i => i.Field == "title" && i.IsError);

    [Fact]
    public void SizeLimitsAreInUtf8Bytes()
    {
        var okChinese = new string('中', EntryValidator.MaxFieldBytes / 3);       // exactly at the limit (3 bytes each)
        Assert.DoesNotContain(V(d => d.Notes = okChinese), i => i.IsError);
        var tooLong = new string('中', EntryValidator.MaxFieldBytes / 3 + 1);
        Assert.Contains(V(d => d.Notes = tooLong), i => i.Field == "notes" && i.IsError);
        Assert.Contains(V(d => d.Password = new string('a', EntryValidator.MaxFieldBytes + 1)), i => i.Field == "password" && i.IsError);
    }

    [Theory]
    [InlineData("Title")]
    [InlineData("username")]
    [InlineData("PASSWORD")]
    [InlineData("Url")]
    [InlineData("notes")]
    [InlineData("OTP")]
    [InlineData("KPH: x")]
    [InlineData("kph:abc")]
    public void ReservedFieldNamesAreErrors(string name) =>
        Assert.Contains(V(d => d.Fields.Add(new DraftField { Name = name, Value = "v" })), i => i.Field == "fields" && i.IsError);

    [Fact]
    public void FieldNames_LengthEmptyAndDuplicates()
    {
        Assert.Contains(V(d => d.Fields.Add(new DraftField { Name = "", Value = "v" })), i => i.IsError);
        Assert.Contains(V(d => d.Fields.Add(new DraftField { Name = new string('x', 129), Value = "v" })), i => i.IsError);
        Assert.DoesNotContain(V(d => d.Fields.Add(new DraftField { Name = new string('x', 128), Value = "v" })), i => i.IsError);
        Assert.Contains(V(d => { d.Fields.Add(new DraftField { Name = "Key", Value = "1" }); d.Fields.Add(new DraftField { Name = "key", Value = "2" }); }),
            i => i.IsError && i.Message.Contains("key"));
        // An empty row is not an error: it is dropped on save.
        Assert.Empty(V(d => d.Fields.Add(new DraftField())));
    }

    [Fact]
    public void CountLimits()
    {
        Assert.Contains(V(d => { for (var i = 0; i < 101; i++) d.Fields.Add(new DraftField { Name = "f" + i, Value = "v" }); }), i => i.IsError);
        Assert.Contains(V(d => d.TagsText = string.Join(",", Enumerable.Range(0, 101).Select(i => "t" + i))), i => i.IsError);
    }

    [Fact]
    public void Warnings_AreNotErrors()
    {
        var issues = V(d => { d.Url = "ftp://x"; d.Totp = "!!!"; });
        Assert.Equal(2, issues.Count(i => !i.IsError));
        Assert.False(EntryValidator.HasErrors(issues));
        Assert.Empty(V(d => { d.Url = "https://example.org/x"; d.Totp = "otpauth://totp/x?secret=ABC"; }));
    }

    [Theory]
    [InlineData("JBSWY3DPEHPK3PXP", true)]
    [InlineData("jbsw y3dp ehpk 3pxp", true)]
    [InlineData("JBSWY3DP-EHPK3PXP", true)]
    [InlineData("otpauth://totp/a?secret=X", true)]
    [InlineData("otpauth://", false)]
    [InlineData("short", false)]
    [InlineData("0000000000000000", false)]   // 0, 1, 8, 9 are not base32
    public void TotpInput_LooksValid(string text, bool expected) => Assert.Equal(expected, TotpInput.LooksValid(text));
}

public class KindTemplateTests
{
    [Fact]
    public void Login_Template()
    {
        var t = KindTemplate.For("login");
        Assert.True(t.Username && t.Password && t.Url && t.Totp && t.Notes && t.Tags && t.Expiry && t.CustomFields);
        Assert.Empty(t.FieldSuggestions);
    }

    [Fact]
    public void ApiKey_Template_HasSuggestionsExpiryAndNoUsername()
    {
        var t = KindTemplate.For("apikey");
        Assert.False(t.Username);
        Assert.False(t.Totp);
        Assert.True(t.Password && t.Url && t.Expiry && t.CustomFields);
        Assert.Contains("Key ID", t.FieldSuggestions);
        Assert.Contains("Environment", t.FieldSuggestions);
        Assert.Contains("Region", t.FieldSuggestions);
        Assert.Equal(Strings.LabelServiceUrl, t.UrlLabel);
        Assert.Equal(Strings.LabelApiKeyValue, t.PasswordLabel);
    }

    [Fact]
    public void Secret_And_Note_Templates()
    {
        var s = KindTemplate.For("secret");
        Assert.True(s.Password && s.Notes);
        Assert.False(s.Username || s.Url || s.Totp || s.Expiry || s.CustomFields);
        var n = KindTemplate.For("note");
        Assert.True(n.Notes);
        Assert.False(n.Password || n.Username || n.Url || n.Totp);
    }

    [Fact]
    public void UnknownKind_FallsBackToLogin()
    {
        Assert.Equal("login", KindTemplate.For("bogus").Kind);
        Assert.Equal("login", KindTemplate.For(null).Kind);
        Assert.Equal("login", EntryKinds.Normalize(""));
    }

    [Fact]
    public void EveryKind_HasADistinctGlyph() =>
        Assert.Equal(EntryKinds.All.Count, EntryKinds.All.Select(KindTemplate.GlyphFor).Distinct().Count());

    [Fact]
    public void FormVisibility_FollowsTemplate_AndShowsFilledHiddenFields()
    {
        var note = FormVisibility.For(new EntryDraft { Kind = "note", Title = "n" });
        Assert.True(note.Notes);
        Assert.False(note.Password || note.Username || note.Url || note.Totp || note.CustomFields || note.Expiry);
        var api = FormVisibility.For(new EntryDraft { Kind = "apikey", Username = "svc" });
        Assert.True(api.Username);   // the template hides it, but it holds a value
        Assert.False(api.Totp);
        var withField = FormVisibility.For(new EntryDraft { Kind = "secret", Fields = { new DraftField { Name = "x", Value = "y" } } });
        Assert.True(withField.CustomFields);
    }
}

public class UrlSafetyTests
{
    [Theory]
    [InlineData("https://example.com/path?q=1", true)]
    [InlineData("http://localhost:8080", true)]
    [InlineData("  https://example.com  ", true)]
    [InlineData("javascript:alert(1)", false)]
    [InlineData("file:///C:/Windows/System32/calc.exe", false)]
    [InlineData("ftp://example.com", false)]
    [InlineData("mailto:a@b.c", false)]
    [InlineData("example.com", false)]
    [InlineData("calc.exe", false)]
    [InlineData("\\\\server\\share", false)]
    [InlineData("https://", false)]
    [InlineData("https://exa\nmple.com", false)]
    [InlineData("", false)]
    [InlineData(null, false)]
    public void OnlyExplicitHttpAndHttpsOpen(string? url, bool expected) =>
        Assert.Equal(expected, UrlSafety.TryGetWebUri(url) is not null);
}

public class ExpiryDatesTests
{
    private static readonly TimeZoneInfo Plus8 = TimeZoneInfo.CreateCustomTimeZone("t8", TimeSpan.FromHours(8), "t8", "t8");

    [Fact]
    public void FromDate_IsTheLastSecondOfThatLocalDay()
    {
        var unix = ExpiryDates.FromDate(new DateTime(2030, 1, 2), Plus8);
        Assert.Equal(new DateTimeOffset(2030, 1, 2, 23, 59, 59, TimeSpan.FromHours(8)).ToUnixTimeSeconds(), unix);
    }

    [Fact]
    public void RoundTrip_AndNone()
    {
        var unix = ExpiryDates.FromDate(new DateTime(2031, 6, 30, 5, 5, 5), Plus8);
        Assert.Equal(new DateTime(2031, 6, 30), ExpiryDates.ToDate(unix, Plus8));
        Assert.Equal(0, ExpiryDates.FromDate(null));
        Assert.Null(ExpiryDates.ToDate(0));
        Assert.Null(ExpiryDates.ToDate(-5));
    }
}
