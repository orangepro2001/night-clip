namespace ClipBridge.Windows.Logic;

public enum PairJoinPhase { Idle, Joining, AwaitingSas, Accepted, Failed }

/// <summary>
/// State machine of the joining side of a pairing. <c>pair.join</c> returns the join id
/// immediately but pair.sas / pair.result can reach the UI thread before the call's reply does,
/// so events whose join id is not yet known are buffered and replayed by <see cref="OnJoinStarted"/>.
/// UI thread only.
/// </summary>
public sealed class PairJoinSession
{
    private string? _joinId;
    private readonly List<CoreEvent> _early = new();

    public PairJoinPhase Phase { get; private set; } = PairJoinPhase.Idle;
    public string Sas { get; private set; } = "";
    public string PeerName { get; private set; } = "";
    public string ErrorMessage { get; private set; } = "";

    public void Begin()
    {
        _joinId = null;
        _early.Clear();
        Sas = ""; PeerName = ""; ErrorMessage = "";
        Phase = PairJoinPhase.Joining;
    }

    /// <summary>The <c>pair.join</c> call failed (validation, busy, ...).</summary>
    public void OnJoinFailed(string message)
    {
        _early.Clear();
        ErrorMessage = message;
        Phase = PairJoinPhase.Failed;
    }

    public void OnJoinStarted(string joinId)
    {
        _joinId = joinId;
        var replay = _early.ToArray();
        _early.Clear();
        foreach (var ev in replay) OnEvent(ev);
    }

    /// <summary>Feed every core event; unrelated ones are ignored. Returns true when the phase or data changed.</summary>
    public bool OnEvent(CoreEvent ev)
    {
        if (Phase is PairJoinPhase.Idle or PairJoinPhase.Accepted or PairJoinPhase.Failed) return false;
        switch (ev)
        {
            case PairSasEvent sas:
                if (_joinId is null) { _early.Add(ev); return false; }
                if (sas.JoinId != _joinId) return false;
                Sas = sas.Sas;
                Phase = PairJoinPhase.AwaitingSas;
                return true;
            case PairResultEvent r when r.Role is "joiner" or "":
                if (_joinId is null) { _early.Add(ev); return false; }
                if (r.JoinId != _joinId) return false;
                if (r.State == "accepted")
                {
                    PeerName = r.Name;
                    Phase = PairJoinPhase.Accepted;
                }
                else
                {
                    ErrorMessage = ErrorMessages.ForPairResult(r.State, r.Code);
                    Phase = PairJoinPhase.Failed;
                }
                return true;
            default:
                return false;
        }
    }
}
