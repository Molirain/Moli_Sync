namespace MoliSync.Client.Platform;

/// <summary>Clipboard abstraction so the sync dispatcher can be tested without touching the desktop clipboard.</summary>
public interface IClipboardSink
{
    /// <summary>Queues text for the clipboard. Returns false when the request was dropped.</summary>
    bool TrySetText(string text);
}
