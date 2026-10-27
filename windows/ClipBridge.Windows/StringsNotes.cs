namespace ClipBridge.Windows;

/// <summary>User-facing text of the notes page and its dialogs (Simplified Chinese). Pure C#, linked into the tests.</summary>
public static partial class Strings
{
    // ---- navigation / page ----------------------------------------------------------------
    public const string NavNotes = "笔记";
    public const string NotesTitle = "笔记";
    public const string NotesSubtitle = "Markdown 笔记，在已配对的设备之间同步";
    public const string BtnNoteNew = "新建笔记";
    public const string BtnNoteTrash = "回收站";
    public const string BtnNoteHistory = "历史版本";
    public const string BtnNoteDelete = "删除";
    public const string BtnNoteRestore = "恢复";
    public const string BtnNoteLoadMore = "加载更多";
    public const string BtnNoteSaveNow = "立即保存";
    public const string NoteTitleHint = "标题";
    public const string NoteEditorEmpty = "选择左边的一篇笔记，或点击“新建笔记”。";
    public const string NotesEmpty = "还没有笔记。点击“新建笔记”开始。";
    public const string NotesTrashEmpty = "回收站是空的。";
    public const string NotesNotRunning = "核心尚未启动，暂时无法读取笔记。";
    public const string NoteViewSource = "源码";
    public const string NoteViewSplit = "分栏";
    public const string NoteViewPreview = "预览";
    public const string NoteConflictBadge = "冲突副本";
    public const string NoteDeleted = "已移到回收站，可以在“回收站”里恢复";
    public const string NoteRestored = "笔记已恢复";
    public static string NoteRevisionRestored(string time) => $"已恢复到 {time} 的版本（之前的内容仍在历史版本里）";
    public const string NoteRevisionSame = "这个版本和现在的内容一样。";

    // ---- editor: toolbar ------------------------------------------------------------------
    public const string EditBoldTip = "粗体（Ctrl+B）";
    public const string EditItalicTip = "斜体（Ctrl+I）";
    public const string EditStrikeTip = "删除线";
    public const string EditCodeTip = "行内代码";
    public const string EditHeadingTip1 = "一级标题";
    public const string EditHeadingTip2 = "二级标题";
    public const string EditHeadingTip3 = "三级标题";
    public const string EditBulletTip = "无序列表";
    public const string EditNumberedTip = "有序列表";
    public const string EditTaskTip = "任务列表";
    public const string EditQuoteTip = "引用";
    public const string EditCodeBlockLabel = "代码块";
    public const string EditCodeBlockTip = "代码块（用 ``` 围起来）";
    public const string EditLinkLabel = "链接";
    public const string EditLinkTip = "插入链接（Ctrl+K）";
    public const string EditRuleTip = "分隔线";

    // The words that appear in the text when a button is used without a selection.
    public const string EditPlaceholderBold = "粗体文字";
    public const string EditPlaceholderItalic = "斜体文字";
    public const string EditPlaceholderStrike = "删除线文字";
    public const string EditPlaceholderCode = "代码";
    public const string EditPlaceholderLink = "链接文字";
    public const string EditPlaceholderUrl = "https://";

    // ---- editor: state --------------------------------------------------------------------
    public const string NoteStateSaved = "已保存";
    public const string NoteStateDirty = "有未保存的修改…";
    public const string NoteStateSaving = "正在保存…";
    public const string NoteStateFailed = "保存失败，文字还在这里。继续输入，或点“立即保存”重试。";
    public const string NoteStateTooLarge = "笔记太大（正文不能超过 1 MiB，标题不能超过 1 KiB），没有保存。";
    public const string NoteStateTrashed = "笔记在回收站里，修改没有保存。";
    public const string NoteInTrashReadOnly = "这篇笔记在回收站里，只能查看。恢复之后才能编辑。";
    public const string NoteTrashedWhileEditing = "这篇笔记已被移到回收站（可能在另一台设备上被删除），修改暂时无法保存。你输入的文字还在这里，恢复笔记后会继续保存。";
    public static string NoteLeaveUnsaved(string reason) =>
        "这篇笔记还有没保存成功的修改" + (string.IsNullOrWhiteSpace(reason) ? "。" : "：\n" + reason) + "\n\n仍要离开吗？没保存的文字会丢失。";

    // ---- editor: what happened to the note ------------------------------------------------
    public const string NoteMerged = "其他设备也修改了这篇笔记，已自动合并，编辑区显示的是合并后的内容。";
    public const string NoteMergedKept = "其他设备也修改了这篇笔记，已自动合并。你刚输入的文字没有动，下次保存时会再次合并。";
    public const string NoteConflictCopyMade = "另一台设备也改了这篇笔记的同一处。较新的内容保留为正文，较旧的一份已保存为冲突副本，请在上方的提示条里处理。";

    // ---- preview --------------------------------------------------------------------------
    public const string NotePreviewEmpty = "（没有内容）";
    public const string NoteLiteralTip = "按原文显示，不会被执行";
    public static string NoteImageText(string alt) => alt.Length > 0 ? $"[图片：{alt}]" : "[图片]";
    public const string NoteLinkNotOpened = "不能打开这个链接：只支持 http、https 和 mailto 地址。";

    // ---- history --------------------------------------------------------------------------
    public const string NoteHistoryTitle = "历史版本";
    public const string NoteHistoryLoading = "正在加载…";
    public const string NoteHistoryEmpty = "没有可显示的历史版本。";
    public const string BtnNoteUseRevision = "恢复此版本";
    public const string NoteRevisionDeleted = "（已删除）";
    public const string NoteRevisionDeletedInfo = "这个版本是删除记录，没有正文。";
    public static string NoteRevisionInfo(string title, bool current) => $"标题：{title}" + (current ? "（当前版本）" : "");
    public const string NoteOriginMerge = "合并";
    public const string NoteOriginThisDevice = "本机";
    public const string NoteOriginUnknown = "其他设备";

    // ---- conflicts ------------------------------------------------------------------------
    public static string NoteConflictBanner(int count, bool more = false) => $"有 {count}{(more ? "+" : "")} 份冲突副本等待处理";
    public const string BtnNoteResolveConflicts = "处理冲突…";
    public const string NoteConflictTitle = "处理冲突副本";
    public const string NoteConflictIntro =
        "两台设备同时修改了同一处：较新的内容成为正文，另一份保存为冲突副本。比较下面两个版本，再决定怎么处理。副本处理后会进入回收站，不会直接消失。";
    public const string NoteConflictMine = "现有笔记";
    public const string NoteConflictCopy = "冲突副本";
    public const string BtnNoteKeepMine = "保留现有笔记";
    public const string NoteKeepMineTip = "丢掉冲突副本，笔记保持现在的样子";
    public const string BtnNoteKeepCopy = "保留副本";
    public const string NoteKeepCopyTip = "用冲突副本的标题和正文替换现有笔记";
    public const string BtnNoteMerge = "合并";
    public const string NoteMergeTip = "把冲突副本接在现有笔记后面，中间用分隔线隔开";
    public const string BtnNoteLater = "稍后处理";
    public static string NoteSyncConflictCopy(string title) => $"《{title}》在两台设备上被同时修改。较新的内容保留为正文，另一份保存为冲突副本。";
    public static string NoteDeletedElsewhere(string title) => $"《{title}》已在另一台设备上被删除，但这台设备上有更新的修改，所以保留了这篇笔记。";
    public const string NoteConflictBalloonTitle = "笔记出现冲突副本";
    public const string NoteConflictBalloon = "两台设备同时改了同一篇笔记，请打开“笔记”页处理。";
    public const string NoteDeletedElsewhereBalloon = "一篇笔记在另一台设备上被删除，但这里有更新的修改，已保留。";

    // ---- sync line ------------------------------------------------------------------------
    public const string NotesSyncNoPeers = "笔记同步：还没有已配对的设备，笔记只保存在这台电脑上";
    public static string NotesSyncDone(string name, string when) => $"{name}：已同步（{when}）";
    public static string NotesSyncNever(string name) => $"{name}：已连接，还没有同步过";
    public static string NotesSyncOffline(string name, string when) => $"{name}：离线（上次同步 {when}）";
    public static string NotesSyncOfflineNever(string name) => $"{name}：离线，还没有同步过";
    public static string NotesSyncSyncing(string name) => $"{name}：正在同步…";
    public static string NotesSyncSyncingBehind(string name, int behind) => $"{name}：正在同步，还有 {behind} 条更新";
    public static string NotesSyncBehindSuffix(int behind) => $"，还有 {behind} 条更新待同步";
    public static string NotesSyncError(string name) => $"{name}：同步出错，稍后会自动重试";
    public static string NotesSyncPeerOld(string name) => $"{name} 使用了更新的笔记格式，笔记暂停同步。请先把这台电脑上的 ClipBridge 更新到最新版本。";
    public static string NotesSyncPending(int pending) => $"待发送 {pending} 条";

    // ---- errors ---------------------------------------------------------------------------
    public const string ErrNoteNotFound = "找不到这篇笔记（可能已被清理）";
    public const string ErrNoteInTrash = "这篇笔记在回收站里，请先恢复再编辑";
    public const string ErrNoteTooLarge = "笔记太大：正文不能超过 1 MiB，标题不能超过 1 KiB";
    public const string ErrNoteUnsupportedFormat = "这篇笔记使用了更新的格式，请先把 ClipBridge 更新到最新版本";
    public const string ErrNotImplemented = "这个功能暂时还没有";
}
