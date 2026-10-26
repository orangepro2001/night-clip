using ClipBridge.Windows.Logic;

namespace ClipBridge.Windows.Services;

/// <summary>
/// Process-wide log file under %LOCALAPPDATA%\ClipBridge\logs. By policy it only ever receives event
/// types, ids and exception types/messages produced by this app, never clipboard text or secrets.
/// </summary>
internal static class AppLog
{
    private static LogFile? _file;

    public static string LogDirectory => Path.Combine(App.DataDir, "logs");

    public static void Init() => _file ??= new LogFile(Path.Combine(LogDirectory, "clipbridge.log"));

    public static void Write(string line) => _file?.Write(line);

    public static void Error(string where, Exception ex) => Write($"error in {where}: {ex.GetType().Name}: {ex.Message}");

    public static void Event(CoreEvent ev)
    {
        // Progress events fire several times a second and carry nothing worth keeping.
        if (ev is FileProgressEvent) return;
        Write("event " + ev.LogLine);
    }
}
