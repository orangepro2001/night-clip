using MoonPassword.Windows.Logic;

namespace MoonPassword.Windows.Services;

/// <summary>
/// Process-wide log file under the data folder's logs\ directory. By policy it only ever receives event
/// types, ids and exception types/messages produced by this app, never vault contents or secrets.
/// </summary>
internal static class AppLog
{
    private static LogFile? _file;

    public static string LogDirectory => Path.Combine(App.DataDir, "logs");

    public static void Init() => _file ??= new LogFile(Path.Combine(LogDirectory, "moonpassword.log"));

    public static void Write(string line) => _file?.Write(line);

    /// <summary>Type and message; a development instance (own data folder) also gets the stack trace (method names only, no data).</summary>
    public static void Error(string where, Exception ex) =>
        Write($"error in {where}: {ex.GetType().Name}: {ex.Message}" + (App.IsDevInstance ? Environment.NewLine + ex : ""));

    public static void Event(CoreEvent ev)
    {
        // Progress events fire several times a second and carry nothing worth keeping.
        if (ev.Type == "update.progress") return;
        Write("event " + ev.LogLine);
    }
}
