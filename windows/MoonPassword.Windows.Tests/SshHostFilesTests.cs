using MoonPassword.Windows.Logic;
using Xunit;

namespace MoonPassword.Windows.Tests;

public sealed class SshHostFilesTests : IDisposable
{
    private readonly string _dir = Directory.CreateTempSubdirectory("moonpassword-ssh-config-").FullName;
    private static readonly SshHostForm Host = new("work", "build.example.test", "deploy", 2200, "jump");
    private static readonly SshManagedIdentity Key = new("1234567890abcdef1234567890abcdef", @"\\.\pipe\moonpassword-test");

    [Fact]
    public void ManagedAliasWritesOnlyOneIncludeAndPreservesExistingConfiguration()
    {
        var config = Path.Combine(_dir, "config");
        const string original = "# personal config\r\nHost original\r\n  HostName original.example.test\r\n";
        File.WriteAllText(config, original);
        var files = new SshHostFiles(_dir);
        using (var edit = files.Begin())
        {
            edit.Set(Host, Key, "fixture public key");
            edit.Commit();
        }
        var actual = File.ReadAllText(config);
        Assert.Equal(files.Include + "\n" + original, actual);
        Assert.Equal(Key, files.Identity("work"));
        Assert.Equal(["work"], files.ManagedAliases());
        using (var edit = files.Begin())
        {
            edit.Set(Host with { Jump = "second-jump" }, Key);
            edit.Commit();
        }
        Assert.Equal(actual, File.ReadAllText(config));
        Assert.Equal("fixture public key\n", File.ReadAllText(files.PublicPath(Key.PublicId)));
    }

    [Fact]
    public void WithoutCommitTheAliasAndSoleIncludeRollBack()
    {
        var files = new SshHostFiles(_dir);
        using (var edit = files.Begin()) edit.Set(Host, Key, "fixture public key");
        Assert.False(File.Exists(Path.Combine(_dir, "config")));
        Assert.False(File.Exists(files.PublicPath(Key.PublicId)));
        Assert.False(files.IsManaged(Host.Alias));
    }

    [Theory]
    [InlineData("*", "server", "user", "")]
    [InlineData("good", "server\n  ProxyCommand other", "user", "")]
    [InlineData("good", "server", "user name", "")]
    [InlineData("good", "server", "user", "jump;other")]
    public void RejectsConfigSyntaxInFormFields(string alias, string host, string user, string jump)
    {
        Assert.Throws<ArgumentException>(() => SshHostForm.Check(new(alias, host, user, 22, jump)));
    }

    public void Dispose() => Directory.Delete(_dir, recursive: true);
}
