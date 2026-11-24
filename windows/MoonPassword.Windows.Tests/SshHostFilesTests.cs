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

    [Fact]
    public void TakeoverBacksUpEveryChangedFileAndKeepsOtherHostsOptions()
    {
        var config = Path.Combine(_dir, "config");
        var included = Path.Combine(_dir, "servers.conf");
        const string root = "IdentityFile shared-global\r\nInclude servers.conf\r\nHost *\r\n  IdentityFile shared-default\r\n";
        const string child = "Host work sibling # shared\r\n  HostName build.example.test\r\n  IdentityFile shared-key\r\n  ServerAliveInterval 19\r\n";
        File.WriteAllText(config, root);
        File.WriteAllText(included, child);

        var files = new SshHostFiles(_dir);
        var plan = files.PlanTakeover("work", [new(included, 1), new(config, 3)]);
        using var edit = files.Begin();
        var backup = edit.ApplyTakeover(plan);
        edit.Set(Host, Key, "fixture public key", takeOver: true);
        edit.Commit();

        Assert.Equal(root, File.ReadAllText(Path.Combine(backup, "config.bak")));
        Assert.Equal(child, File.ReadAllText(Path.Combine(backup, "servers.conf.bak")));
        var actual = File.ReadAllText(included);
        Assert.Contains("Host work\r\n  HostName build.example.test\r\n  ServerAliveInterval 19\r\n", actual);
        Assert.Contains("Host work sibling !work # shared\r\n  HostName build.example.test\r\n  IdentityFile shared-key", actual);
        Assert.Contains("Match originalhost *,!work\r\nIdentityFile shared-global\r\nMatch all", File.ReadAllText(config));
        Assert.Contains("Host * !work\r\n  IdentityFile shared-default", File.ReadAllText(config));
        Assert.Equal(Key, files.Identity("work"));
    }

    [Fact]
    public void FailedTakeoverRestoresRootAndIncludedFilesByteForByte()
    {
        var config = Path.Combine(_dir, "config");
        var included = Path.Combine(_dir, "servers.conf");
        const string root = "# original\r\nInclude servers.conf\r\n";
        const string child = "Host work\r\n  IdentityFile old\r\n";
        File.WriteAllText(config, root, new System.Text.UTF8Encoding(true));
        File.WriteAllText(included, child);
        byte[] before = File.ReadAllBytes(config);

        var files = new SshHostFiles(_dir);
        var plan = files.PlanTakeover("work", [new(included, 1)]);
        string backup;
        using (var edit = files.Begin())
        {
            edit.WritePublicKey(Key.PublicId, "fixture public key");
            Assert.Equal(before, File.ReadAllBytes(config));
            Assert.Equal(child, File.ReadAllText(included));
            backup = edit.ApplyTakeover(plan);
            edit.Set(Host, Key, takeOver: true);
        }

        Assert.Equal(before, File.ReadAllBytes(config));
        Assert.Equal(before, File.ReadAllBytes(Path.Combine(backup, "config.bak")));
        Assert.Equal(child, File.ReadAllText(included));
        Assert.False(files.IsManaged("work"));
        Assert.False(File.Exists(files.PublicPath(Key.PublicId)));
    }

    [Fact]
    public void TakeoverDoesNotOverwriteConcurrentConfigEdits()
    {
        var config = Path.Combine(_dir, "config");
        File.WriteAllText(config, "Host work\n  IdentityFile old\n");
        var files = new SshHostFiles(_dir);
        var plan = files.PlanTakeover("work", [new(config, 1)]);
        File.AppendAllText(config, "  Port 2222\n");
        using var edit = files.Begin();
        Assert.Throws<IOException>(() => edit.ApplyTakeover(plan));
        Assert.Equal("Host work\n  IdentityFile old\n  Port 2222\n", File.ReadAllText(config));
        Assert.False(files.IsManaged("work"));
    }

    [Fact]
    public void ASecondTakeoverAddsAnotherExclusionToTheSharedIdentity()
    {
        var config = Path.Combine(_dir, "config");
        File.WriteAllText(config, "IdentityFile shared\nHost work sibling\n  HostName build.example.test\n");
        var files = new SshHostFiles(_dir);
        using (var first = files.Begin())
        {
            first.ApplyTakeover(files.PlanTakeover("work", [new(config, 2)]));
            first.Set(Host, Key, "fixture public key", takeOver: true);
            first.Commit();
        }
        int header = Array.FindIndex(File.ReadAllLines(config), line => line == "Host work sibling") + 1;
        using (var second = files.Begin())
        {
            second.ApplyTakeover(files.PlanTakeover("sibling", [new(config, header)]));
            second.Commit();
        }
        Assert.Contains("Match originalhost *,!work,!sibling\nIdentityFile shared\nMatch all", File.ReadAllText(config));
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
