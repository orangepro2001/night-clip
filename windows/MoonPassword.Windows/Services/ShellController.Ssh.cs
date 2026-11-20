using System.Text.Json;
using MoonPassword.Windows.Interop;
using MoonPassword.Windows.Logic;

namespace MoonPassword.Windows.Services;

/// <summary>An ssh.* call's outcome: the value, or the error text plus the core's code and details.</summary>
public sealed record SshResult<T>(bool Ok, T? Value, string Code, string Error, JsonElement Details)
{
    public string Reason => JsonRead.Str(Details, "reason");
    public bool WrongPassphrase => JsonRead.Bool(Details, "wrong");
    public string DuplicateId => JsonRead.Str(Details, "entry_id");
}

/// <summary>
/// The shell's side of the SSH agent (docs/adr/009-ssh-agent.md): calling ssh.*, turning agent events into
/// balloons, and the few things only the shell can do (Git's configuration, the Recycle Bin, an elevated
/// service change). The agent itself lives in the core.
/// </summary>
internal sealed partial class ShellController
{
    private SshApi? _ssh;
    private readonly SshNotifier _sshNotifier = new();

    public SshApi Ssh => _ssh ??= new SshApi(_core);

    /// <summary>The last ssh.status (refreshed on every ssh.* event).</summary>
    public SshStatus SshState { get; private set; } = SshStatus.Unknown;

    /// <summary>The agent's state, its keys or its recent uses changed. UI thread.</summary>
    public event Action? SshChanged;

    public async Task<SshResult<T>> SshCallAsync<T>(Func<SshApi, T> call)
    {
        try
        {
            var v = await Task.Run(() => call(Ssh));
            return new SshResult<T>(true, v, "", "", default);
        }
        catch (CoreException ex)
        {
            AppLog.Write("ssh call failed: " + ex.Code); // the code only: never parameters or key material
            var reason = JsonRead.Str(ex.Details, "reason");
            var wrong = JsonRead.Bool(ex.Details, "wrong");
            return new SshResult<T>(false, default, ex.Code, ErrorMessages.ForSsh(ex.Code, reason, wrong), ex.Details);
        }
        catch (Exception ex) when (IsMalformedReply(ex))
        {
            AppLog.Error("ssh reply", ex);
            return new SshResult<T>(false, default, "INTERNAL", ErrorMessages.ForCode("INTERNAL"), default);
        }
    }

    public async Task<SshResult<bool>> SshCallAsync(Action<SshApi> call) => await SshCallAsync(a => { call(a); return true; });

    public async Task RefreshSshStatusAsync()
    {
        var r = await SshCallAsync(a => a.Status());
        if (r.Ok)
        {
            SshState = r.Value!;
            SshChanged?.Invoke();
        }
    }

    /// <summary>Turns the agent on or off (core setting ssh_agent). Returns an error text or null.</summary>
    public async Task<string?> SetSshAgentAsync(bool on)
    {
        var error = await CallAsync(a => a.SettingsSet("ssh_agent", on ? "on" : "off"));
        await RefreshSshStatusAsync();
        return error;
    }

    private void HandleSshEvent(CoreEvent ev)
    {
        _sshNotifier.Mode = Prefs.SshNotify;
        var now = DateTimeOffset.Now;
        switch (ev)
        {
            case SshUsedEvent u:
                if (_sshNotifier.OnUse(u.Use, now) is { } b) Balloon(b.Title, b.Text, () => ShowMainWindow(MainPage.Settings));
                SshChanged?.Invoke();
                break;
            case SshKeysNeededEvent k:
                if (_sshNotifier.OnKeysNeeded(k.Client, now) is { } n) Balloon(n.Title, n.Text, () => ShowMainWindow(MainPage.Vault));
                break;
            case SshKeysEvent or SshAgentEvent:
                _ = RefreshSshStatusAsync();
                break;
        }
    }

    // ---- Git ----------------------------------------------------------------------------------

    /// <summary>
    /// git config --global core.sshCommand C:/Windows/System32/OpenSSH/ssh.exe. Only called after the user
    /// confirmed the exact command. Remembers that this app set it, so "undo" never removes a user's own value.
    /// </summary>
    public async Task<string?> FixGitSshAsync()
    {
        var (code, _, err) = await SshEnvProbe.RunAsync("git", "config", "--global", "core.sshCommand", SshEnv.WindowsSshCommand);
        if (code != 0) return Strings.SshGitFailed(err.Length > 0 ? err : "exit " + code);
        Prefs.GitSshCommandSet = SshEnv.WindowsSshCommand;
        Prefs.Save(App.DataDir);
        return null;
    }

    /// <summary>Removes core.sshCommand, but only while it still holds the value this app wrote.</summary>
    public async Task<string?> UndoGitSshAsync()
    {
        var (_, current, _) = await SshEnvProbe.RunAsync("git", "config", "--global", "--get", "core.sshCommand");
        if (Prefs.GitSshCommandSet is not { } ours || current.Trim() != ours) return null;
        var (code, _, err) = await SshEnvProbe.RunAsync("git", "config", "--global", "--unset", "core.sshCommand");
        if (code != 0) return Strings.SshGitFailed(err.Length > 0 ? err : "exit " + code);
        Prefs.GitSshCommandSet = null;
        Prefs.Save(App.DataDir);
        return null;
    }

    /// <summary>True when core.sshCommand currently holds the value this app wrote.</summary>
    public bool GitSshIsOurs(SshEnvFacts f) =>
        Prefs.GitSshCommandSet is { } ours && f.GitSshCommand is { } cur && cur.Trim() == ours;

    /// <summary>
    /// Stops and disables Windows' "OpenSSH Authentication Agent" service so the agent can take its pipe.
    /// Asks Windows for elevation (UAC); only called after the user confirmed. Returns an error text or null.
    /// </summary>
    public async Task<string?> DisableWindowsSshAgentServiceAsync()
    {
        var ok = await Task.Run(SshEnvProbe.DisableServiceElevated);
        if (!ok) return Strings.ErrorGeneric;
        // The pipe is free now: start our listener again.
        return await SetSshAgentAsync(true);
    }
}
