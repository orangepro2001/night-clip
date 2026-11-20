using System.Text.Json;

namespace MoonPassword.Windows.Logic;

/// <summary>A core event in typed form. Parsing never throws: unknown or malformed events become <see cref="UnknownEvent"/>.</summary>
public abstract record CoreEvent
{
    public string Type { get; init; } = "";

    /// <summary>An identifier the event carries, if any (never a secret).</summary>
    public string? Id { get; init; }

    /// <summary>What may be logged: the type and the id, never any payload.</summary>
    public string LogLine => string.IsNullOrEmpty(Id) ? Type : Type + " " + Id;
}

public sealed record CoreStartedEvent : CoreEvent;

/// <summary>Any <c>update.*</c> event; the update panel re-reads <c>update.status</c> on each.</summary>
public sealed record UpdateEvent : CoreEvent
{
    public string VersionName { get; init; } = "";
    public bool Mandatory { get; init; }
    public string Code { get; init; } = "";
}

/// <summary>The core locked the vault (<c>reason</c>: idle / user / ...). Everything the UI holds must be dropped.</summary>
public sealed record VaultLockedEvent : CoreEvent { public string Reason { get; init; } = ""; }

/// <summary>The vault changed behind the UI's back (<c>reason</c>, e.g. "sync"): reload the list.</summary>
public sealed record VaultChangedEvent : CoreEvent { public string Reason { get; init; } = ""; }

/// <summary>ssh.used (signed) or ssh.refused: one request to the SSH agent. Carries no key material.</summary>
public sealed record SshUsedEvent : CoreEvent
{
    public SshUse Use { get; init; } = new(0, "", "", "", Array.Empty<SshProc>(), "", "", "", "", "", false, "", "");
    public int Suppressed { get; init; }
}

/// <summary>ssh.keys: the agent's key set changed (loaded / grace / cleared / expired).</summary>
public sealed record SshKeysEvent : CoreEvent
{
    public string State { get; init; } = "";
    public int Count { get; init; }
    public string KeysState { get; init; } = "";
    public string Reason { get; init; } = "";
    public long ExpiresAt { get; init; }
}

/// <summary>ssh.keys_needed: a program asked the agent for keys while none were loaded (the vault is locked).</summary>
public sealed record SshKeysNeededEvent : CoreEvent
{
    public IReadOnlyList<SshProc> Client { get; init; } = Array.Empty<SshProc>();
}

/// <summary>ssh.agent: the agent started, stopped, or could not take its pipe.</summary>
public sealed record SshAgentEvent : CoreEvent
{
    public string State { get; init; } = "";
    public string Pipe { get; init; } = "";
    public string Error { get; init; } = "";
}

public sealed record UnknownEvent : CoreEvent;

public static class CoreEventParser
{
    public static CoreEvent Parse(JsonElement e)
    {
        if (e.ValueKind != JsonValueKind.Object) return new UnknownEvent();
        var type = JsonRead.Str(e, "type");
        string Id(string name) => JsonRead.Str(e, name);

        CoreEvent ev;
        string? id = null;
        switch (type)
        {
            case "core.started":
                ev = new CoreStartedEvent();
                break;
            case "vault.changed":
                ev = new VaultChangedEvent { Reason = Id("reason") };
                break;
            case "vault.locked":
                ev = new VaultLockedEvent { Reason = Id("reason") };
                break;
            case "ssh.used":
            case "ssh.refused":
                ev = new SshUsedEvent { Use = SshUse.FromJson(e), Suppressed = (int)JsonRead.Long(e, "suppressed") };
                break;
            case "ssh.keys":
                ev = new SshKeysEvent
                {
                    State = Id("state"), Count = (int)JsonRead.Long(e, "count"), KeysState = Id("keys_state"),
                    Reason = Id("reason"), ExpiresAt = JsonRead.Long(e, "expires_at"),
                };
                break;
            case "ssh.keys_needed":
                ev = new SshKeysNeededEvent { Client = SshText.Procs(e) };
                break;
            case "ssh.agent":
                ev = new SshAgentEvent { State = Id("state"), Pipe = Id("pipe"), Error = Id("error") };
                break;
            default:
                if (type.StartsWith("update.", StringComparison.Ordinal))
                {
                    ev = new UpdateEvent
                    {
                        VersionName = Id("version_name"), Mandatory = JsonRead.Bool(e, "mandatory"), Code = Id("code"),
                    };
                }
                else ev = new UnknownEvent();
                break;
        }
        return ev with { Type = type, Id = string.IsNullOrEmpty(id) ? null : id };
    }

    /// <summary>Parses event JSON text; malformed input yields <see cref="UnknownEvent"/>.</summary>
    public static CoreEvent Parse(string json)
    {
        try
        {
            using var doc = JsonDocument.Parse(json);
            return Parse(doc.RootElement);
        }
        catch (JsonException) { return new UnknownEvent(); }
    }
}
