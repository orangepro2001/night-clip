namespace MoonPassword.Windows.Logic;

/// <summary>
/// Decides which SSH agent events become a tray balloon. Signing is never confirmed (the user chose
/// "notify only", docs/adr/009-ssh-agent.md), so the balloons are the visible trace; they are kept
/// rare enough not to become noise: in "first" mode one per (key, program) until that pair has been
/// quiet for <see cref="QuietPeriod"/>.
/// </summary>
public sealed class SshNotifier
{
    public static readonly TimeSpan QuietPeriod = TimeSpan.FromMinutes(30);
    public static readonly TimeSpan RefusalEvery = TimeSpan.FromMinutes(1);
    public static readonly TimeSpan KeysNeededEvery = TimeSpan.FromMinutes(5);

    private readonly Dictionary<string, DateTimeOffset> _lastUse = new();
    private DateTimeOffset _lastRefusal = DateTimeOffset.MinValue;
    private DateTimeOffset _lastNeeded = DateTimeOffset.MinValue;

    /// <summary>"first", "all" or "off" (ShellPrefs.SshNotify).</summary>
    public string Mode { get; set; } = "first";

    /// <summary>The balloon for a signing request, or null.</summary>
    public (string Title, string Text)? OnUse(SshUse u, DateTimeOffset now)
    {
        if (Mode == "off") return null;
        if (!u.Signed)
        {
            if (now - _lastRefusal < RefusalEvery) return null;
            _lastRefusal = now;
            return (Strings.SshRefusedTitle, Strings.SshRefusedText(u));
        }
        var key = u.Fingerprint + "|" + u.ProgramName.ToLowerInvariant();
        var seen = _lastUse.TryGetValue(key, out var last) && now - last < QuietPeriod;
        _lastUse[key] = now;
        if (_lastUse.Count > 512) Trim(now);
        if (Mode == "first" && seen) return null;
        return (Strings.SshUsedTitle(u), Strings.SshUsedText(u));
    }

    /// <summary>The balloon for "a program wants keys but the vault is locked", or null.</summary>
    public (string Title, string Text)? OnKeysNeeded(IReadOnlyList<SshProc> client, DateTimeOffset now)
    {
        if (Mode == "off" || now - _lastNeeded < KeysNeededEvery) return null;
        _lastNeeded = now;
        return (Strings.SshKeysNeededTitle, Strings.SshKeysNeededText(SshText.Program(client)));
    }

    private void Trim(DateTimeOffset now)
    {
        foreach (var k in _lastUse.Where(p => now - p.Value >= QuietPeriod).Select(p => p.Key).ToList()) _lastUse.Remove(k);
    }
}

/// <summary>Whether a key file may be moved to the Recycle Bin after it was imported, and why not.</summary>
public sealed record RecycleDecision(bool Allowed, IReadOnlyList<string> Blockers);

/// <summary>Rules of the "move ~/.ssh keys into the vault" wizard.</summary>
public static class SshMigration
{
    /// <summary>A key the wizard can import (possibly after asking for its passphrase).</summary>
    public static bool CanImport(SshScanItem item) => item.Problem.Length == 0;

    /// <summary>Selected by default: importable and not in the vault yet.</summary>
    public static bool SelectByDefault(SshScanItem item) => CanImport(item) && !item.InVaultAlready;

    /// <summary>
    /// A private key file may only be offered for the Recycle Bin when every one of these holds: the vault
    /// holds the same key, the .pub next to it is right (so ~/.ssh/config keeps finding the key through
    /// the agent), the agent lists the key, and nothing else in the folder names the file (a script that
    /// passes it to a tool which cannot use the agent, such as SSHFS-Win, would break).
    /// </summary>
    public static RecycleDecision CanRecycle(SshScanItem item, string importedFingerprint, string pubState, IReadOnlyCollection<string> agentFingerprints)
    {
        var blockers = new List<string>();
        if (importedFingerprint.Length == 0 || (item.Fingerprint.Length > 0 && item.Fingerprint != importedFingerprint))
            blockers.Add(Strings.SshBlockFingerprint);
        if (pubState is not ("ok" or "created" or "same"))
            blockers.Add(Strings.SshBlockPub);
        if (importedFingerprint.Length > 0 && !agentFingerprints.Contains(importedFingerprint))
            blockers.Add(Strings.SshBlockAgent);
        if (item.ReferencedBy.Count > 0)
            blockers.Add(Strings.SshBlockReferenced(item.ReferencedBy.Select(r => System.IO.Path.GetFileName(r.File)).Distinct().ToList()));
        return new RecycleDecision(blockers.Count == 0, blockers);
    }

    /// <summary>Lines the wizard shows next to a key: where it is used.</summary>
    public static IReadOnlyList<string> Notes(SshScanItem item)
    {
        var notes = new List<string>();
        if (item.ConfigHosts.Count > 0) notes.Add(Strings.SshUsedByHosts(item.ConfigHosts));
        foreach (var r in item.ReferencedBy) notes.Add(Strings.SshReferencedBy(System.IO.Path.GetFileName(r.File), r.Line));
        if (item.PubState == "mismatch") notes.Add(Strings.SshPubMismatch);
        if (item.Problem.Length > 0) notes.Add(SshText.Problem(item.Problem == "corrupt" ? "corrupt" : "unsupported", item.Problem));
        return notes;
    }
}

/// <summary>What the shell found out about the machine's SSH setup (gathered by SshEnvProbe).</summary>
public sealed record SshEnvFacts(
    bool GitFound, string? GitSshCommand, bool WindowsSshFound, string WindowsSshVersion,
    string AgentService, string? SshAuthSockProcess, string? SshAuthSockUser)
{
    public static readonly SshEnvFacts Unknown = new(false, null, true, "", "unknown", null, null);
}

/// <summary>One thing about the SSH setup the user should know, and the fix the app can apply (if any).</summary>
public sealed record SshEnvIssue(string Id, bool IsWarning, string Title, string Text, string? Fix = null);

public static class SshEnv
{
    /// <summary>The Windows build of OpenSSH, which talks to the agent's named pipe.</summary>
    public const string WindowsSshCommand = "C:/Windows/System32/OpenSSH/ssh.exe";

    public const string FixGitSsh = "git_ssh";

    public static bool IsWindowsSsh(string? command)
    {
        if (string.IsNullOrWhiteSpace(command)) return false;
        var c = command.Trim().Trim('"', '\'').Replace('\\', '/');
        return c.Equals(WindowsSshCommand, StringComparison.OrdinalIgnoreCase)
            || c.Equals("C:/WINDOWS/System32/OpenSSH/ssh", StringComparison.OrdinalIgnoreCase)
            || c.EndsWith("/System32/OpenSSH/ssh.exe", StringComparison.OrdinalIgnoreCase);
    }

    public static IReadOnlyList<SshEnvIssue> Check(SshEnvFacts f, SshStatus status)
    {
        var issues = new List<SshEnvIssue>();
        if (!f.WindowsSshFound)
            issues.Add(new("no_openssh", true, Strings.SshEnvNoOpenSshTitle, Strings.SshEnvNoOpenSshText));
        if (status.State == "conflict")
        {
            issues.Add(f.AgentService == "running"
                ? new("service", true, Strings.SshEnvServiceTitle, Strings.SshEnvServiceText)
                : new("pipe_taken", true, Strings.SshEnvPipeTakenTitle, Strings.SshEnvPipeTakenText(status.Pipe)));
        }
        if (f.GitFound)
        {
            if (string.IsNullOrWhiteSpace(f.GitSshCommand))
                issues.Add(new("git_ssh", true, Strings.SshEnvGitTitle, Strings.SshEnvGitText, FixGitSsh));
            else if (!IsWindowsSsh(f.GitSshCommand))
                issues.Add(new("git_ssh_other", false, Strings.SshEnvGitOtherTitle, Strings.SshEnvGitOtherText(f.GitSshCommand!)));
        }
        var sock = f.SshAuthSockProcess ?? f.SshAuthSockUser;
        if (!string.IsNullOrWhiteSpace(sock) && !string.Equals(sock, status.Pipe, StringComparison.OrdinalIgnoreCase))
            issues.Add(new("auth_sock", true, Strings.SshEnvAuthSockTitle, Strings.SshEnvAuthSockText(sock!)));
        return issues;
    }
}
