using System.Globalization;
using System.Text;

namespace ClipBridge.Windows.Logic;

/// <summary>
/// A tiny size-capped text log. Callers pass event types and ids and error kinds only — never clipboard
/// text, secrets or keys. One backup file (<c>.1</c>) is kept.
/// </summary>
public sealed class LogFile
{
    private readonly string _path;
    private readonly long _maxBytes;
    private readonly object _gate = new();

    public LogFile(string path, long maxBytes = 512 * 1024)
    {
        _path = path;
        _maxBytes = maxBytes;
    }

    public string Path => _path;

    public void Write(string line, DateTimeOffset? now = null)
    {
        try
        {
            lock (_gate)
            {
                var dir = System.IO.Path.GetDirectoryName(_path);
                if (!string.IsNullOrEmpty(dir)) Directory.CreateDirectory(dir);
                var info = new FileInfo(_path);
                if (info.Exists && info.Length > _maxBytes)
                {
                    var backup = _path + ".1";
                    File.Move(_path, backup, overwrite: true);
                }
                var stamp = (now ?? DateTimeOffset.Now).ToString("yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture);
                File.AppendAllText(_path, stamp + " " + line.Replace('\r', ' ').Replace('\n', ' ') + Environment.NewLine, Encoding.UTF8);
            }
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException) { /* logging must never break the app */ }
    }
}
