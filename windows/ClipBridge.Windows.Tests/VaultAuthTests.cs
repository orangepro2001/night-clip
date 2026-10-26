using ClipBridge.Windows.Interop;
using ClipBridge.Windows.Logic;
using Xunit;

namespace ClipBridge.Windows.Tests;

public class KeyFileTests
{
    [Theory]
    [InlineData(0L, false)]
    [InlineData(-1L, false)]
    [InlineData(1L, true)]
    [InlineData(1L << 20, true)]
    [InlineData((1L << 20) + 1, false)]
    public void Size_1Byte_To_1MiB(long length, bool ok) => Assert.Equal(ok, KeyFileRules.Check(length) is null);

    [Fact]
    public void Load_ReadsTheFile_AndRefusesBadOnes()
    {
        var dir = Path.Combine(Path.GetTempPath(), "cb-keyfile-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(dir);
        try
        {
            var good = Path.Combine(dir, "good.key");
            File.WriteAllBytes(good, new byte[] { 1, 2, 3 });
            var (bytes, error) = KeyFileRules.Load(good);
            Assert.Equal(new byte[] { 1, 2, 3 }, bytes);
            Assert.Null(error);

            var empty = Path.Combine(dir, "empty.key");
            File.WriteAllBytes(empty, Array.Empty<byte>());
            Assert.Equal(Strings.KeyFileEmpty, KeyFileRules.Load(empty).Error);

            var big = Path.Combine(dir, "big.key");
            File.WriteAllBytes(big, new byte[(1 << 20) + 1]);
            Assert.Equal(Strings.KeyFileTooBig, KeyFileRules.Load(big).Error);

            Assert.Equal(Strings.KeyFileMissing, KeyFileRules.Load(Path.Combine(dir, "nope")).Error);
        }
        finally { Directory.Delete(dir, recursive: true); }
    }
}

public class StrengthDisplayTests
{
    [Theory]
    [InlineData("very_weak", 1, 0xFFD93025u)]
    [InlineData("weak", 2, 0xFFE8710Au)]
    [InlineData("fair", 3, 0xFFF2B600u)]
    [InlineData("good", 4, 0xFF7CB342u)]
    [InlineData("strong", 5, 0xFF1E8E3Eu)]
    public void LabelToSegmentsAndColor(string label, int segments, uint color)
    {
        Assert.Equal(segments, StrengthDisplay.Segments(label));
        Assert.Equal(color, StrengthDisplay.Color(label));
        Assert.Equal(segments / 5.0, StrengthDisplay.Fraction(label), 3);
    }

    [Fact]
    public void UnknownLabel_IsTreatedAsVeryWeak()
    {
        Assert.Equal("very_weak", StrengthDisplay.Normalize("bogus"));
        Assert.Equal("very_weak", StrengthDisplay.Normalize(null));
        Assert.Equal(Strings.StrengthVeryWeak, StrengthDisplay.Text("???"));
    }

    [Fact]
    public void FiveLabels_FiveTexts_FiveColors()
    {
        var labels = new[] { "very_weak", "weak", "fair", "good", "strong" };
        Assert.Equal(5, labels.Select(StrengthDisplay.Text).Distinct().Count());
        Assert.Equal(5, labels.Select(StrengthDisplay.Color).Distinct().Count());
    }

    [Theory]
    [InlineData("very_weak", true)]
    [InlineData("weak", true)]
    [InlineData("fair", false)]
    [InlineData("strong", false)]
    public void WeakWarning(string label, bool weak) => Assert.Equal(weak, StrengthDisplay.IsWeak(label));

    [Fact]
    public void BitsText()
    {
        Assert.Equal("", StrengthDisplay.Bits(0));
        Assert.Equal(Strings.StrengthBits(56), StrengthDisplay.Bits(55.6));
    }
}

public class RateLimitTests
{
    private static readonly DateTimeOffset T0 = new(2030, 1, 1, 0, 0, 0, TimeSpan.Zero);

    [Fact]
    public void CountdownTicksDown_ThenFrees()
    {
        var r = new RateLimitCountdown();
        Assert.False(r.IsBlocked(T0));
        r.Start(4, T0);
        Assert.True(r.IsBlocked(T0));
        Assert.Equal(4, r.SecondsLeft(T0));
        Assert.Equal(3, r.SecondsLeft(T0.AddSeconds(1.5)));
        Assert.Equal(1, r.SecondsLeft(T0.AddSeconds(3)));
        Assert.False(r.IsBlocked(T0.AddSeconds(4)));
        Assert.Equal(0, r.SecondsLeft(T0.AddSeconds(100)));
    }

    [Fact]
    public void ZeroOrClear_Frees()
    {
        var r = new RateLimitCountdown();
        r.Start(0, T0);
        Assert.False(r.IsBlocked(T0));
        r.Start(10, T0);
        r.Clear();
        Assert.False(r.IsBlocked(T0));
    }

    [Fact]
    public void Outcome_FromRateLimitedError_CarriesTheWait()
    {
        var o = UnlockOutcome.FromError("RATE_LIMITED", 8);
        Assert.Equal(8, o.RetryAfterSeconds);
        Assert.Contains("8", o.Message);
        Assert.False(o.IsWrongCredentials);
        Assert.Equal(1, UnlockOutcome.FromError("RATE_LIMITED", 0).RetryAfterSeconds);   // never "no wait" for a rate limit
    }

    [Fact]
    public void Outcome_WrongPassword_IsFriendly_AndNeverSaysItCanBeRecovered()
    {
        var o = UnlockOutcome.FromError("VAULT_WRONG_CREDENTIALS", 0);
        Assert.True(o.IsWrongCredentials);
        Assert.Equal(0, o.RetryAfterSeconds);
        Assert.Contains("主密码或密钥文件不对", o.Message);
        Assert.DoesNotContain("找回", o.Message);
    }
}

public class VaultScreenTests
{
    private static VaultStatus St(bool exists, bool unlocked) => new(exists, unlocked, 300, 20, 0, 0, 0, 0, "");

    [Fact]
    public void ScreenFollowsStatus()
    {
        Assert.Equal(VaultScreen.Create, VaultScreens.From(St(false, false)));
        Assert.Equal(VaultScreen.Locked, VaultScreens.From(St(true, false)));
        Assert.Equal(VaultScreen.Unlocked, VaultScreens.From(St(true, true)));
    }

    [Fact]
    public void CreateWarning_StatesThatForgettingIsFinal()
    {
        Assert.Contains("永远无法打开", Strings.CreateWarningText);
        Assert.Contains("没有“找回密码”功能", Strings.CreateWarningText);
        Assert.Contains("导出一份加密备份", Strings.CreateWarningText);
        Assert.Contains("导出一份加密备份", Strings.CreateBackupPrompt);
    }
}

public class FormCheckTests
{
    [Fact]
    public void CreateVault_Checks()
    {
        Assert.Equal(Strings.CreateEmptyPassword, CreateVaultForm.Check("", "", null).Error);
        Assert.Equal(Strings.CreateMismatch, CreateVaultForm.Check("a", "b", null).Error);
        var ok = CreateVaultForm.Check("pw", "pw", "weak");
        Assert.Null(ok.Error);
        Assert.True(ok.WeakWarning);           // warned, but allowed
        Assert.False(CreateVaultForm.Check("pw", "pw", "strong").WeakWarning);
    }

    [Fact]
    public void Kdf_Choices()
    {
        Assert.Equal(new[] { "low", "standard", "high" }, CreateVaultForm.KdfChoices.ToArray());
        Assert.Equal(3, CreateVaultForm.KdfChoices.Select(CreateVaultForm.KdfLabel).Distinct().Count());
    }

    [Fact]
    public void ChangeMaster_Checks()
    {
        Assert.Equal(Strings.ChangeNeedCurrent, ChangeMasterForm.Check("", "n", "n"));
        Assert.Equal(Strings.CreateEmptyPassword, ChangeMasterForm.Check("c", "", ""));
        Assert.Equal(Strings.CreateMismatch, ChangeMasterForm.Check("c", "a", "b"));
        Assert.Null(ChangeMasterForm.Check("c", "n", "n"));
    }

    [Fact]
    public void Export_Checks()
    {
        Assert.Equal(Strings.ExportNeedMaster, ExportForm.CheckKdbx("", "x", "x"));
        Assert.Equal(Strings.CreateEmptyPassword, ExportForm.CheckKdbx("m", "", ""));
        Assert.Equal(Strings.CreateMismatch, ExportForm.CheckKdbx("m", "a", "b"));
        Assert.Null(ExportForm.CheckKdbx("m", "x", "x"));
        Assert.Equal(Strings.ExportNeedMaster, ExportForm.CheckCsv(""));
        Assert.Null(ExportForm.CheckCsv("m"));
    }

    [Fact]
    public void CsvExport_NeedsTheTypedWord()
    {
        Assert.True(ExportForm.CsvConfirmed(Strings.ExportCsvConfirmWord));
        Assert.True(ExportForm.CsvConfirmed(" " + Strings.ExportCsvConfirmWord + " "));
        Assert.False(ExportForm.CsvConfirmed(""));
        Assert.False(ExportForm.CsvConfirmed("ok"));
        Assert.Contains("明文", Strings.ExportCsvWarning);
    }

    [Fact]
    public void ExportFileNames()
    {
        var t = new DateTimeOffset(2030, 5, 6, 7, 8, 0, TimeSpan.Zero);
        Assert.EndsWith(".kdbx", ExportForm.DefaultFileName("kdbx", t));
        Assert.EndsWith(".csv", ExportForm.DefaultFileName("csv", t));
    }

    [Fact]
    public void FreeName_NeverReturnsAnExistingPath()
    {
        var taken = new HashSet<string> { Path.Combine("d", "a.txt"), Path.Combine("d", "a (1).txt"), Path.Combine("d", "a (2).txt") };
        Assert.Equal(Path.Combine("d", "b.txt"), FreeName.Next(Path.Combine("d", "b.txt"), taken.Contains));
        Assert.Equal(Path.Combine("d", "a (3).txt"), FreeName.Next(Path.Combine("d", "a.txt"), taken.Contains));
    }

    [Theory]
    [InlineData("0", 0, true)]
    [InlineData("30", 30, true)]
    [InlineData("86400", 86400, true)]
    [InlineData(" 300 ", 300, true)]
    [InlineData("29", 0, false)]
    [InlineData("86401", 0, false)]
    [InlineData("abc", 0, false)]
    [InlineData("", 0, false)]
    [InlineData("-5", 0, false)]
    [InlineData("3.5", 0, false)]
    public void Autolock(string text, int value, bool ok)
    {
        var (v, e) = VaultSettingsForm.ParseAutolock(text);
        Assert.Equal(ok, e is null);
        if (ok) Assert.Equal(value, v!.Value);
    }

    [Theory]
    [InlineData("0", 0, true)]
    [InlineData("5", 5, true)]
    [InlineData("300", 300, true)]
    [InlineData("4", 0, false)]
    [InlineData("301", 0, false)]
    [InlineData("x", 0, false)]
    public void ClipClear(string text, int value, bool ok)
    {
        var (v, e) = VaultSettingsForm.ParseClipClear(text);
        Assert.Equal(ok, e is null);
        if (ok) Assert.Equal(value, v!.Value);
    }
}

public class GeneratorOptionsTests
{
    [Fact]
    public void PasswordMode_SendsClassesAndLength()
    {
        var p = new GeneratorOptions(Length: 32, Symbols: false, ExcludeAmbiguous: true).ToParams();
        Assert.Equal("password", p["type"]);
        Assert.Equal(32, p["length"]);
        Assert.Equal(false, p["symbols"]);
        Assert.Equal(true, p["exclude_ambiguous"]);
    }

    [Fact]
    public void Length_IsClampedTo8_64()
    {
        Assert.Equal(8, new GeneratorOptions(Length: 1).ToParams()["length"]);
        Assert.Equal(64, new GeneratorOptions(Length: 500).ToParams()["length"]);
    }

    [Fact]
    public void NoClassSelected_FallsBackToLowercase()
    {
        var p = new GeneratorOptions(Lower: false, Upper: false, Digits: false, Symbols: false).ToParams();
        Assert.Equal(true, p["lower"]);
    }

    [Fact]
    public void PassphraseMode()
    {
        var p = new GeneratorOptions(Passphrase: true, Words: 99, Separator: "", Number: true).ToParams();
        Assert.Equal("passphrase", p["type"]);
        Assert.Equal(20, p["words"]);
        Assert.Equal("-", p["separator"]);
        Assert.Equal(true, p["number"]);
        Assert.False(p.ContainsKey("length"));
        Assert.Equal(3, new GeneratorOptions(Passphrase: true, Words: 0).ToParams()["words"]);
    }
}

public class VaultErrorMessageTests
{
    [Theory]
    [InlineData("VAULT_LOCKED")]
    [InlineData("VAULT_WRONG_CREDENTIALS")]
    [InlineData("VAULT_NOT_FOUND")]
    [InlineData("VAULT_EXISTS")]
    [InlineData("VAULT_UNSUPPORTED")]
    [InlineData("VAULT_BROKEN")]
    [InlineData("RATE_LIMITED")]
    [InlineData("TOO_LARGE")]
    [InlineData("INVALID")]
    public void EveryDocumentedCode_HasAFriendlyMessage(string code)
    {
        var text = ErrorMessages.ForVault(code, 5);
        Assert.NotEmpty(text);
        Assert.DoesNotContain("未知错误", text);
        Assert.DoesNotContain(code, text);
    }

    [Fact]
    public void SpecificWording()
    {
        Assert.Contains("请先解锁", ErrorMessages.ForVault("VAULT_LOCKED"));
        Assert.Contains("文件已损坏", ErrorMessages.ForVault("VAULT_WRONG_CREDENTIALS"));
        Assert.Contains("加密参数", ErrorMessages.ForVault("VAULT_UNSUPPORTED"));
        Assert.Contains("12 秒", ErrorMessages.ForVault("RATE_LIMITED", 12));
        Assert.Contains("重新解锁", ErrorMessages.ForVault("VAULT_BROKEN"));
    }

    [Fact]
    public void UnknownCode_ShowsTheCode_AndGenericForCodeKnowsTheVaultOnes()
    {
        Assert.Contains("SOMETHING_NEW", ErrorMessages.ForVault("SOMETHING_NEW"));
        Assert.Equal(Strings.ErrVaultWrongCredentials, ErrorMessages.ForCode("VAULT_WRONG_CREDENTIALS"));
        Assert.Equal(Strings.ErrVaultLocked, ErrorMessages.ForCode("VAULT_LOCKED"));
    }

    [Fact]
    public void CoreException_CarriesRetryAfter()
    {
        Assert.Equal(7, new CoreException("RATE_LIMITED", "m", 7).RetryAfterSeconds);
        Assert.Equal(0, new CoreException("X", "m").RetryAfterSeconds);
        Assert.Equal(0, new CoreException("X", "m", -3).RetryAfterSeconds);
    }
}

public class ShellPrefsCaptureTests
{
    [Fact]
    public void PreventCapture_DefaultsToOn_AndPersists()
    {
        var dir = Path.Combine(Path.GetTempPath(), "cb-prefs-" + Guid.NewGuid().ToString("N"));
        try
        {
            Assert.True(ShellPrefs.Load(dir).PreventCapture);          // no file: on
            var p = new ShellPrefs { PreventCapture = false, FirstRunDone = true };
            p.Save(dir);
            var back = ShellPrefs.Load(dir);
            Assert.False(back.PreventCapture);
            Assert.True(back.FirstRunDone);
            p.PreventCapture = true;
            p.Save(dir);
            Assert.True(ShellPrefs.Load(dir).PreventCapture);
        }
        finally { if (Directory.Exists(dir)) Directory.Delete(dir, recursive: true); }
    }

    [Fact]
    public void OldPrefsFiles_WithoutTheKey_KeepTheDefault()
    {
        var dir = Path.Combine(Path.GetTempPath(), "cb-prefs-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(dir);
        try
        {
            File.WriteAllText(Path.Combine(dir, ShellPrefs.FileName), """{"first_run_done":true}""");
            var p = ShellPrefs.Load(dir);
            Assert.True(p.PreventCapture);
            Assert.True(p.FirstRunDone);
        }
        finally { Directory.Delete(dir, recursive: true); }
    }
}
