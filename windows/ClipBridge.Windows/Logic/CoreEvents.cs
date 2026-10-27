using System.Text.Json;

namespace ClipBridge.Windows.Logic;

/// <summary>A core event in typed form. Parsing never throws: unknown or malformed events become <see cref="UnknownEvent"/>.</summary>
public abstract record CoreEvent
{
    public string Type { get; init; } = "";

    /// <summary>event_id / transfer_id / pair_id / join_id / device_id, whichever the event carries.</summary>
    public string? Id { get; init; }

    /// <summary>What may be logged: the type and the id, never any payload.</summary>
    public string LogLine => string.IsNullOrEmpty(Id) ? Type : Type + " " + Id;
}

public sealed record CoreStartedEvent : CoreEvent { public string DeviceId { get; init; } = ""; }

public sealed record CoreListeningEvent : CoreEvent
{
    public IReadOnlyList<string> Addrs { get; init; } = Array.Empty<string>();
    public int Port { get; init; }
}

public sealed record CoreWarningEvent : CoreEvent { public string Code { get; init; } = ""; }

public sealed record PeerStateEvent : CoreEvent
{
    public string DeviceId { get; init; } = "";
    public string State { get; init; } = "";
}

public sealed record PeerRevokedEvent : CoreEvent
{
    public string DeviceId { get; init; } = "";
    public string Name { get; init; } = "";
}

public sealed record ClipReceivedEvent : CoreEvent
{
    public string EventId { get; init; } = "";
    public string From { get; init; } = "";
    public string FromName { get; init; } = "";
    public string Text { get; init; } = "";
    public bool Sensitive { get; init; }
    public string Decision { get; init; } = "notify";
    public long ReceivedAt { get; init; }

    /// <summary>False when the event waited (offline catch-up, slow link): the core already downgrades "auto" then.</summary>
    public bool Live { get; init; } = true;

    /// <summary>GetClipboardSequenceNumber() read on the pump thread the moment the event arrived (0 = unknown).</summary>
    public uint SeqAtArrival { get; init; }
}

public sealed record ClipAckEvent : CoreEvent
{
    public string EventId { get; init; } = "";
    public string From { get; init; } = "";
    public string Result { get; init; } = "";
    public string Reason { get; init; } = "";
}

public sealed record OutboxExpiredEvent : CoreEvent { public int Count { get; init; } }

public sealed record PairRequestEvent : CoreEvent
{
    public string PairId { get; init; } = "";
    public string Name { get; init; } = "";
    public string Platform { get; init; } = "";
    public string DeviceId { get; init; } = "";
    public string Sas { get; init; } = "";
    public string ExpiresAt { get; init; } = "";
}

public sealed record PairSasEvent : CoreEvent
{
    public string JoinId { get; init; } = "";
    public string Sas { get; init; } = "";
}

public sealed record PairResultEvent : CoreEvent
{
    public string State { get; init; } = "";
    public string Role { get; init; } = "";
    public string PairId { get; init; } = "";
    public string JoinId { get; init; } = "";
    public string Code { get; init; } = "";
    public string Message { get; init; } = "";
    public string DeviceId { get; init; } = "";
    public string Name { get; init; } = "";
}

public sealed record FileOfferedEvent : CoreEvent
{
    public string TransferId { get; init; } = "";
    public string From { get; init; } = "";
    public string FromName { get; init; } = "";
    public string Name { get; init; } = "";
    public long Size { get; init; }
    public string Sha256 { get; init; } = "";
    public string MimeType { get; init; } = "";
}

public sealed record FileStateEvent : CoreEvent
{
    public string TransferId { get; init; } = "";
    public string State { get; init; } = "";
}

public sealed record FileProgressEvent : CoreEvent
{
    public string TransferId { get; init; } = "";
    public long Offset { get; init; }
    public long Size { get; init; }
    public long RateBps { get; init; }
}

public sealed record FileDoneEvent : CoreEvent
{
    public string TransferId { get; init; } = "";
    public string Direction { get; init; } = "";
    public string Path { get; init; } = "";
    public string Name { get; init; } = "";
    public long Size { get; init; }
    public string Sha256 { get; init; } = "";
}

public sealed record FileFailedEvent : CoreEvent
{
    public string TransferId { get; init; } = "";
    public string Code { get; init; } = "";
    public string Message { get; init; } = "";
}

/// <summary>Any <c>update.*</c> event; the update panel re-reads <c>update.status</c> on each.</summary>
public sealed record UpdateEvent : CoreEvent
{
    public string VersionName { get; init; } = "";
    public bool Mandatory { get; init; }
    public string Code { get; init; } = "";
}

/// <summary>The core locked the vault (<c>reason</c>: idle / user / ...). Everything the UI holds must be dropped.</summary>
public sealed record VaultLockedEvent : CoreEvent { public string Reason { get; init; } = ""; }

/// <summary>The vault changed behind the UI's back (<c>reason</c>: "sync"): reload the list.</summary>
public sealed record VaultChangedEvent : CoreEvent { public string Reason { get; init; } = ""; }

/// <summary>A note changed: created, edited, deleted, restored, merged or synced in. It carries no text: fetch it with notes.get.</summary>
public sealed record NotesChangedEvent : CoreEvent
{
    public string NoteId { get; init; } = "";
    public string RevId { get; init; } = "";
}

/// <summary>A peer's note sync changed state (<c>idle</c>, <c>syncing</c>, <c>error</c>, <c>peer_old</c>) or its backlog.</summary>
public sealed record NotesSyncUpdatedEvent : CoreEvent
{
    public string PeerId { get; init; } = "";
    public string State { get; init; } = "";
    public int Behind { get; init; }
}

/// <summary>
/// The sync set a conflict copy aside (<see cref="Reason"/> <c>diverged</c>, with <see cref="ConflictNoteId"/>) or kept an edit
/// although another device deleted the note (<c>deleted_elsewhere</c>).
/// </summary>
public sealed record NotesSyncConflictEvent : CoreEvent
{
    public const string Diverged = "diverged";
    public const string DeletedElsewhere = "deleted_elsewhere";

    public string NoteId { get; init; } = "";
    public string ConflictNoteId { get; init; } = "";
    public string Reason { get; init; } = "";
}

/// <summary>An attachment (<see cref="BlobId"/>, 64 hex digits) just reached this device: notes that refer to it can show it now.</summary>
public sealed record NotesAttachmentEvent : CoreEvent
{
    public string BlobId { get; init; } = "";
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
                ev = new CoreStartedEvent { DeviceId = Id("device_id") };
                id = Id("device_id");
                break;
            case "core.listening":
                ev = new CoreListeningEvent { Addrs = JsonRead.StrArr(e, "addrs"), Port = (int)JsonRead.Long(e, "port") };
                break;
            case "core.warning":
                ev = new CoreWarningEvent { Code = Id("code") };
                break;
            case "peer.state":
                ev = new PeerStateEvent { DeviceId = Id("device_id"), State = Id("state") };
                id = Id("device_id");
                break;
            case "peer.revoked":
                ev = new PeerRevokedEvent { DeviceId = Id("device_id"), Name = Id("name") };
                id = Id("device_id");
                break;
            case "clip.received":
                ev = new ClipReceivedEvent
                {
                    EventId = Id("event_id"), From = Id("from"), FromName = Id("from_name"), Text = Id("text"),
                    Sensitive = JsonRead.Bool(e, "sensitive"), Decision = Id("decision") is { Length: > 0 } d ? d : "notify",
                    ReceivedAt = JsonRead.Long(e, "received_at"), Live = JsonRead.Bool(e, "live", true),
                };
                id = Id("event_id");
                break;
            case "clip.ack":
                ev = new ClipAckEvent { EventId = Id("event_id"), From = Id("from"), Result = Id("result"), Reason = Id("reason") };
                id = Id("event_id");
                break;
            case "outbox.expired":
                ev = new OutboxExpiredEvent { Count = (int)JsonRead.Long(e, "count") };
                break;
            case "pair.request":
                ev = new PairRequestEvent
                {
                    PairId = Id("pair_id"), Name = Id("name"), Platform = Id("platform"), DeviceId = Id("device_id"),
                    Sas = Id("sas"), ExpiresAt = Id("expires_at"),
                };
                id = Id("pair_id");
                break;
            case "pair.sas":
                ev = new PairSasEvent { JoinId = Id("join_id"), Sas = Id("sas") };
                id = Id("join_id");
                break;
            case "pair.result":
                ev = new PairResultEvent
                {
                    State = Id("state"), Role = Id("role"), PairId = Id("pair_id"), JoinId = Id("join_id"),
                    Code = Id("code"), Message = Id("message"), DeviceId = Id("device_id"), Name = Id("name"),
                };
                id = Id("pair_id") is { Length: > 0 } p ? p : Id("join_id");
                break;
            case "file.offered":
                ev = new FileOfferedEvent
                {
                    TransferId = Id("transfer_id"), From = Id("from"), FromName = Id("from_name"), Name = Id("name"),
                    Size = JsonRead.Long(e, "size"), Sha256 = Id("sha256"), MimeType = Id("mime_type"),
                };
                id = Id("transfer_id");
                break;
            case "file.state":
                ev = new FileStateEvent { TransferId = Id("transfer_id"), State = Id("state") };
                id = Id("transfer_id");
                break;
            case "file.progress":
                ev = new FileProgressEvent
                {
                    TransferId = Id("transfer_id"), Offset = JsonRead.Long(e, "offset"), Size = JsonRead.Long(e, "size"),
                    RateBps = JsonRead.Long(e, "rate_bps"),
                };
                id = Id("transfer_id");
                break;
            case "file.done":
                ev = new FileDoneEvent
                {
                    TransferId = Id("transfer_id"), Direction = Id("direction"), Path = Id("path"), Name = Id("name"),
                    Size = JsonRead.Long(e, "size"), Sha256 = Id("sha256"),
                };
                id = Id("transfer_id");
                break;
            case "file.failed":
                ev = new FileFailedEvent { TransferId = Id("transfer_id"), Code = Id("code"), Message = Id("message") };
                id = Id("transfer_id");
                break;
            case "vault.changed":
                ev = new VaultChangedEvent { Reason = Id("reason") };
                break;
            case "vault.locked":
                ev = new VaultLockedEvent { Reason = Id("reason") };
                break;
            case "notes.changed":
                ev = new NotesChangedEvent { NoteId = Id("note_id"), RevId = Id("rev_id") };
                id = Id("note_id");
                break;
            case "notes.sync.updated":
                ev = new NotesSyncUpdatedEvent
                {
                    PeerId = Id("peer_id"), State = Id("state"), Behind = (int)Math.Clamp(JsonRead.Long(e, "behind"), 0, int.MaxValue),
                };
                id = Id("peer_id");
                break;
            case "notes.sync.conflict":
                ev = new NotesSyncConflictEvent { NoteId = Id("note_id"), ConflictNoteId = Id("conflict_note_id"), Reason = Id("reason") };
                id = Id("note_id");
                break;
            case "notes.attachment":
                ev = new NotesAttachmentEvent { BlobId = Id("blob_id") };
                id = Id("blob_id");
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
