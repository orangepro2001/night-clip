namespace ClipBridge.Windows.Logic;

/// <summary>What the shell does with a received clipboard event.</summary>
public enum ClipAction
{
    /// <summary>Tray balloon only.</summary>
    Notify,
    /// <summary>Tray balloon; clicking it (or "复制" in the inbox) writes the clipboard.</summary>
    NotifyManual,
    /// <summary>Write the clipboard now, then clip.mark_applied.</summary>
    WriteNow,
    /// <summary>"auto" was requested but could not be honoured: balloon, never overwrite.</summary>
    NotifyDowngraded,
}

/// <summary>
/// docs/shell-guide.md §3 item 5 as a pure function. The core already guarantees
/// that sensitive events are never "auto"; this is the second check.
/// </summary>
public static class ClipboardDecider
{
    public static ClipAction Decide(string decision, bool sensitive, uint seqAtArrival, uint currentSeq)
    {
        switch (decision)
        {
            case "manual":
                return ClipAction.NotifyManual;
            case "auto":
                if (sensitive) return ClipAction.Notify;
                // seq 0 means the snapshot failed (e.g. no window station): we cannot prove the user
                // did not copy something else, so we must not overwrite.
                if (seqAtArrival == 0 || currentSeq == 0) return ClipAction.NotifyDowngraded;
                return seqAtArrival == currentSeq ? ClipAction.WriteNow : ClipAction.NotifyDowngraded;
            default:
                return ClipAction.Notify;
        }
    }

    /// <summary>The same check, repeated right before the actual write.</summary>
    public static bool StillSafeToWrite(bool sensitive, uint seqAtArrival, uint currentSeq) =>
        !sensitive && seqAtArrival != 0 && currentSeq != 0 && seqAtArrival == currentSeq;
}

public enum LocalCaptureDecision
{
    /// <summary>Hand the text to clip.local_changed.</summary>
    Send,
    /// <summary>Ignore silently (empty, or capture off).</summary>
    SkipSilently,
    /// <summary>Do not send; tell the user the text is too large.</summary>
    SkipTooLarge,
}

/// <summary>Decides what to do with text read from the local clipboard.</summary>
public static class LocalCapture
{
    public static LocalCaptureDecision Decide(string coreText, bool captureOn)
    {
        if (coreText.Length == 0) return LocalCaptureDecision.SkipSilently;
        if (!captureOn) return LocalCaptureDecision.SkipSilently;
        if (ClipText.IsTooLarge(coreText)) return LocalCaptureDecision.SkipTooLarge;
        return LocalCaptureDecision.Send;
    }
}
