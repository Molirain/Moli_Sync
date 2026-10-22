using MoliSync.Client.Logging;

namespace MoliSync.Client.Services;

public sealed class FileClientLog : IClientLog
{
    private const long MaximumBytes = 2 * 1024 * 1024;
    private readonly object _gate = new();
    private readonly string _path;

    public FileClientLog(string? path = null)
    {
        _path = path ?? AppPaths.LogFile;
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_path)!);
        }
        catch (IOException)
        {
            // Logging must never take the application down.
        }
        catch (UnauthorizedAccessException)
        {
            // Same as above.
        }
    }

    public void Debug(string message) => Write("DBG", message, null);

    public void Info(string message) => Write("INF", message, null);

    public void Warn(string message, Exception? exception = null) => Write("WRN", message, exception);

    public void Error(string message, Exception? exception = null) => Write("ERR", message, exception);

    private void Write(string level, string message, Exception? exception)
    {
        lock (_gate)
        {
            try
            {
                RotateIfNeeded();
                var line = $"[{DateTimeOffset.Now:yyyy-MM-dd HH:mm:ss}] {level} {Sanitize(message)}{Environment.NewLine}";
                if (exception is not null)
                {
                    line += $"    {exception.GetType().Name}: {Sanitize(exception.Message)}{Environment.NewLine}";
                }
                File.AppendAllText(_path, line);
            }
            catch (IOException)
            {
                // Best effort only.
            }
            catch (UnauthorizedAccessException)
            {
                // Best effort only.
            }
        }
    }

    private void RotateIfNeeded()
    {
        var info = new FileInfo(_path);
        if (!info.Exists || info.Length < MaximumBytes)
        {
            return;
        }

        var previous = _path + ".1";
        if (File.Exists(previous))
        {
            File.Delete(previous);
        }
        File.Move(_path, previous);
    }

    private static string Sanitize(string value) =>
        value.Length == 0
            ? value
            : string.Create(value.Length, value, static (span, source) =>
            {
                for (var index = 0; index < source.Length; index++)
                {
                    span[index] = char.IsControl(source[index]) ? ' ' : source[index];
                }
            });
}
