using System.Collections.ObjectModel;
using System.ComponentModel;
using System.Globalization;
using System.Text.Json;

namespace ClipBridge.Windows.Logic;

/// <summary>One row of the transfer list (docs/shell-guide.md §2). Mutated on the UI thread only.</summary>
public sealed class TransferRow : INotifyPropertyChanged
{
    public event PropertyChangedEventHandler? PropertyChanged;

    public string TransferId { get; }
    public string PeerId { get; private set; } = "";
    public string PeerName { get; private set; } = "";
    public bool Incoming { get; private set; }
    public string Name { get; private set; } = "";
    public long Size { get; private set; }
    public long Offset { get; private set; }
    public long RateBps { get; private set; }
    public string State { get; private set; } = "";
    public string ErrorCode { get; private set; } = "";
    public string Path { get; private set; } = "";
    public long CreatedAt { get; private set; }

    public TransferRow(string transferId) => TransferId = transferId;

    // ---- display ------------------------------------------------------------------------
    public string DisplayName => FileDisplay.Shorten(Name, 60);
    public string DirectionText => Incoming ? Strings.DirIn : Strings.DirOut;
    public string DirectionGlyph => Incoming ? "↓" : "↑";
    public string SizeText => Size > 0 ? Formatting.Bytes(Size) : "";
    public double Progress => Size > 0 ? Math.Clamp((double)Offset / Size, 0, 1) : (State == "done" ? 1 : 0);
    public bool ShowProgress => State is "transferring" or "accepted" or "hashing";
    public string ProgressText => State == "transferring" || (State == "accepted" && Offset > 0)
        ? $"{Formatting.Bytes(Offset)} / {Formatting.Bytes(Size)}（{Formatting.Percent(Progress)}）" : "";
    public string SpeedText => State == "transferring" ? Formatting.Speed(RateBps) : "";
    public string EtaText => State == "transferring" ? Formatting.Eta(Math.Max(0, Size - Offset), RateBps) : "";
    public string Detail
    {
        get
        {
            if (State != "transferring") return "";
            var parts = new List<string> { ProgressText };
            if (RateBps > 0) parts.Add(SpeedText);
            if (EtaText.Length > 0) parts.Add(EtaText);
            return string.Join("  ·  ", parts.Where(p => p.Length > 0));
        }
    }

    public string StateText => State switch
    {
        "hashing" => Strings.TransferHashing,
        "offered" => Incoming ? Strings.TransferOfferedIn : Strings.TransferOffered,
        "accepted" => Strings.TransferAccepted,
        "transferring" => Strings.TransferTransferring,
        "done" => Strings.TransferDone,
        "rejected" => Strings.TransferRejected,
        "cancelled" => Strings.TransferCancelled,
        "expired" => Strings.TransferExpired,
        "failed" => ErrorCode.Length > 0 ? Strings.TransferFailedWith(ErrorMessages.ForFile(ErrorCode)) : Strings.TransferFailed,
        _ => State,
    };

    // ---- what the buttons may do --------------------------------------------------------
    public bool IsTerminal => State is "done" or "rejected" or "cancelled" or "failed" or "expired";
    public bool CanAccept => Incoming && State == "offered";
    public bool CanReject => Incoming && State == "offered";
    public bool CanCancel => !IsTerminal && !CanAccept;
    public bool CanOpen => State == "done" && Path.Length > 0;

    // ---- updates ------------------------------------------------------------------------
    internal void Set(string? peerId = null, string? peerName = null, bool? incoming = null, string? name = null,
        long? size = null, long? offset = null, long? rate = null, string? state = null, string? error = null,
        string? path = null, long? createdAt = null)
    {
        if (peerId is { Length: > 0 }) PeerId = peerId;
        if (peerName is { Length: > 0 }) PeerName = peerName;
        if (incoming.HasValue) Incoming = incoming.Value;
        if (name is { Length: > 0 }) Name = name;
        if (size.HasValue && size.Value > 0) Size = size.Value;
        if (offset.HasValue) Offset = offset.Value;
        if (rate.HasValue) RateBps = rate.Value;
        if (state is { Length: > 0 }) State = state;
        if (error is not null) ErrorCode = error;
        if (path is { Length: > 0 }) Path = path;
        if (createdAt.HasValue && createdAt.Value > 0) CreatedAt = createdAt.Value;
        if (State is "done") { Offset = Size > 0 ? Size : Offset; RateBps = 0; }
        else if (State is "failed" or "cancelled" or "rejected" or "expired") RateBps = 0;
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(string.Empty));
    }
}

/// <summary>
/// The transfer list, newest first, kept in sync from <c>transfers.list</c> and file.* events.
/// UI thread only; <see cref="Rows"/> is bound by the transfer page.
/// </summary>
public sealed class TransferTracker
{
    private readonly Dictionary<string, TransferRow> _byId = new();
    public ObservableCollection<TransferRow> Rows { get; } = new();

    public TransferRow? Find(string transferId) => _byId.GetValueOrDefault(transferId);

    /// <summary>Replaces the content from a <c>transfers.list</c> result. Returns the number of rows.</summary>
    public int ReplaceAll(JsonElement result)
    {
        var seen = new HashSet<string>();
        var ordered = new List<TransferRow>();
        foreach (var t in JsonRead.Arr(result, "transfers"))
        {
            var id = JsonRead.Str(t, "transfer_id");
            if (id.Length == 0) continue;
            if (!_byId.TryGetValue(id, out var row)) { row = new TransferRow(id); _byId[id] = row; }
            row.Set(
                peerId: JsonRead.Str(t, "peer_id"), peerName: JsonRead.Str(t, "peer_name"),
                incoming: JsonRead.Str(t, "direction") == "in", name: JsonRead.Str(t, "name"),
                size: JsonRead.Long(t, "size"), offset: JsonRead.Long(t, "offset"), rate: JsonRead.Long(t, "rate_bps"),
                state: JsonRead.Str(t, "state"), error: JsonRead.Str(t, "error"), path: JsonRead.Str(t, "path"),
                createdAt: JsonRead.Long(t, "created_at"));
            seen.Add(id);
            ordered.Add(row);
        }
        foreach (var gone in _byId.Keys.Where(k => !seen.Contains(k)).ToList()) _byId.Remove(gone);
        Rows.Clear();
        foreach (var r in ordered) Rows.Add(r); // the core sends newest first
        return Rows.Count;
    }

    /// <summary>
    /// Applies a file.* event. Returns false when it refers to a transfer we do not know yet
    /// (typically one we started ourselves): the caller should reload <c>transfers.list</c>.
    /// </summary>
    public bool Apply(CoreEvent ev)
    {
        switch (ev)
        {
            case FileOfferedEvent o:
            {
                if (!_byId.TryGetValue(o.TransferId, out var row))
                {
                    row = new TransferRow(o.TransferId);
                    _byId[o.TransferId] = row;
                    Rows.Insert(0, row);
                }
                row.Set(peerId: o.From, peerName: o.FromName, incoming: true, name: o.Name, size: o.Size, state: "offered",
                    createdAt: DateTimeOffset.UtcNow.ToUnixTimeSeconds());
                return true;
            }
            case FileStateEvent s:
                if (!_byId.TryGetValue(s.TransferId, out var sr)) return false;
                sr.Set(state: s.State);
                return true;
            case FileProgressEvent p:
                if (!_byId.TryGetValue(p.TransferId, out var pr)) return false;
                pr.Set(offset: p.Offset, size: p.Size, rate: p.RateBps, state: pr.State is "accepted" or "offered" or "hashing" ? "transferring" : null);
                return true;
            case FileDoneEvent d:
                if (!_byId.TryGetValue(d.TransferId, out var dr)) return false;
                dr.Set(state: "done", path: d.Path, name: d.Name, size: d.Size);
                return true;
            case FileFailedEvent f:
                if (!_byId.TryGetValue(f.TransferId, out var fr)) return false;
                fr.Set(state: "failed", error: f.Code);
                return true;
            default:
                return true;
        }
    }

    public bool HasActive => Rows.Any(r => !r.IsTerminal);
}
