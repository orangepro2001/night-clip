using System.ComponentModel;
using System.Text.Json;

namespace ClipBridge.Windows.Logic;

/// <summary>One clipboard item in the inbox. Sensitive text stays hidden until the user reveals it.</summary>
public sealed class InboxRow : INotifyPropertyChanged
{
    public event PropertyChangedEventHandler? PropertyChanged;

    public string EventId { get; }
    public string FromId { get; }
    public string FromName { get; }
    public string Text { get; }
    public bool Sensitive { get; }
    public string State { get; private set; }
    public long ReceivedAt { get; }
    public bool Revealed { get; private set; }
    private readonly DateTimeOffset _now;

    public InboxRow(string eventId, string fromId, string fromName, string text, bool sensitive, string state,
        long receivedAt, DateTimeOffset now)
    {
        EventId = eventId; FromId = fromId; FromName = fromName; Text = text; Sensitive = sensitive;
        State = state; ReceivedAt = receivedAt; _now = now;
    }

    public static InboxRow FromJson(JsonElement i, DateTimeOffset now) => new(
        JsonRead.Str(i, "event_id"), JsonRead.Str(i, "from"), JsonRead.Str(i, "from_name"), JsonRead.Str(i, "text"),
        JsonRead.Bool(i, "sensitive"), JsonRead.Str(i, "state", "received"), JsonRead.Long(i, "received_at"), now);

    public static InboxRow FromEvent(ClipReceivedEvent e, DateTimeOffset now) =>
        new(e.EventId, e.From, e.FromName, e.Text, e.Sensitive, "received", e.ReceivedAt > 0 ? e.ReceivedAt : now.ToUnixTimeSeconds(), now);

    public bool IsHidden => Sensitive && !Revealed;

    /// <summary>What the list shows: a one-line preview, or the placeholder while hidden.</summary>
    public string Preview => IsHidden ? Strings.ReceivedSensitive : ClipText.Preview(Text, 200);

    public string FromText => Strings.InboxFrom(FromName.Length > 0 ? FromName : FromId);
    public string TimeText => Formatting.Time(ReceivedAt, _now);

    public string StateText => State switch
    {
        "received" => Strings.InboxStateReceived,
        "applied" => Strings.InboxStateApplied,
        "ignored" => Strings.InboxStateIgnored,
        "expired" => Strings.InboxStateExpired,
        "error" => Strings.InboxStateError,
        _ => State,
    };

    public bool CanDismiss => State == "received";
    public bool CanCopy => Text.Length > 0;
    public bool CanReveal => Sensitive;
    public string RevealText => Revealed ? Strings.BtnHide : Strings.BtnReveal;

    public void ToggleReveal()
    {
        Revealed = !Revealed;
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(string.Empty));
    }

    public void SetState(string state)
    {
        if (State == state) return;
        State = state;
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(string.Empty));
    }

    public static IReadOnlyList<InboxRow> ListFromJson(JsonElement result, DateTimeOffset now) =>
        JsonRead.Arr(result, "items").Select(i => FromJson(i, now)).Where(r => r.EventId.Length > 0).ToList();
}
