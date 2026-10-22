namespace MoliSync.Client.Logging;

public enum ClientLogLevel
{
    Debug = 0,
    Info = 1,
    Warn = 2,
    Error = 3,
}

public interface IClientLog
{
    void Debug(string message);

    void Info(string message);

    void Warn(string message, Exception? exception = null);

    void Error(string message, Exception? exception = null);
}

public sealed class ConsoleClientLog : IClientLog
{
    private static readonly object ConsoleGate = new();
    private readonly ClientLogLevel _minimum;

    public ConsoleClientLog(ClientLogLevel minimum = ClientLogLevel.Info)
    {
        _minimum = minimum;
    }

    public void Debug(string message) => Write(ClientLogLevel.Debug, message, null);

    public void Info(string message) => Write(ClientLogLevel.Info, message, null);

    public void Warn(string message, Exception? exception = null) => Write(ClientLogLevel.Warn, message, exception);

    public void Error(string message, Exception? exception = null) => Write(ClientLogLevel.Error, message, exception);

    private void Write(ClientLogLevel level, string message, Exception? exception)
    {
        if (level < _minimum)
        {
            return;
        }

        var prefix = level switch
        {
            ClientLogLevel.Debug => "DBG",
            ClientLogLevel.Info => "INF",
            ClientLogLevel.Warn => "WRN",
            _ => "ERR",
        };

        lock (ConsoleGate)
        {
            Console.WriteLine($"[{DateTimeOffset.Now:HH:mm:ss}] {prefix} {Sanitize(message)}");
            if (exception is not null)
            {
                Console.WriteLine($"    {exception.GetType().Name}: {Sanitize(exception.Message)}");
            }
        }
    }

    private static string Sanitize(string value) =>
        string.Create(value.Length, value, static (span, source) =>
        {
            for (var index = 0; index < source.Length; index++)
            {
                span[index] = char.IsControl(source[index]) ? ' ' : source[index];
            }
        });
}

public sealed class NullClientLog : IClientLog
{
    public static readonly NullClientLog Instance = new();

    private NullClientLog()
    {
    }

    public void Debug(string message)
    {
    }

    public void Info(string message)
    {
    }

    public void Warn(string message, Exception? exception = null)
    {
    }

    public void Error(string message, Exception? exception = null)
    {
    }
}
