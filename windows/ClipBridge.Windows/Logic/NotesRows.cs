namespace ClipBridge.Windows.Logic;

/// <summary>One row of the history dialog: when, who, and the title the note had then.</summary>
public sealed class NoteRevisionRow
{
    /// <param name="deviceName">Maps a device id to the name of a paired device ("" when unknown).</param>
    public NoteRevisionRow(NoteRevision revision, string ownDeviceId, Func<string, string> deviceName)
    {
        Revision = revision;
        TimeText = NotesTime.Stamp(revision.CreatedAt);
        OriginText = Origin(revision, ownDeviceId, deviceName);
        Title = revision.Deleted ? Strings.NoteRevisionDeleted
            : revision.Title.Length > 0 ? revision.Title : Strings.Untitled;
    }

    public NoteRevision Revision { get; }
    public string TimeText { get; }
    public string OriginText { get; }
    public string Title { get; }

    /// <summary>"本机", "合并" (made by the sync), the name of the other device, or "未知设备".</summary>
    public static string Origin(NoteRevision revision, string ownDeviceId, Func<string, string> deviceName)
    {
        if (revision.IsMerge) return Strings.NoteOriginMerge;
        if (revision.OriginDevice.Length > 0 && revision.OriginDevice == ownDeviceId) return Strings.NoteOriginThisDevice;
        var name = revision.OriginDevice.Length > 0 ? deviceName(revision.OriginDevice) : "";
        return name.Length > 0 ? name : Strings.NoteOriginUnknown;
    }
}

/// <summary>One row of the conflict dialog.</summary>
public sealed class NoteConflictRow
{
    public NoteConflictRow(NoteConflict conflict)
    {
        Conflict = conflict;
        Title = conflict.Title.Length > 0 ? conflict.Title : Strings.Untitled;
        TimeText = NotesTime.Stamp(conflict.CreatedAt);
    }

    public NoteConflict Conflict { get; }
    public string Title { get; }
    public string TimeText { get; }
}
