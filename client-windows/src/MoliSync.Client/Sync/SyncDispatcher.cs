using MoliSync.Client.Logging;
using MoliSync.Client.Platform;

namespace MoliSync.Client.Sync;

public sealed record PresenceUpdate(string State, DeviceView Device);

public sealed record WelcomeState(
    int ProtocolVersion,
    string DeviceId,
    long ServerTime,
    TimeSpan HeartbeatInterval,
    TimeSpan PongTimeout,
    IReadOnlyList<DeviceView> Devices);

/// <summary>
/// Applies server control frames. Text records are copied to the clipboard and
/// stored in full. Image/file records are stored as metadata placeholders only;
/// their blob body is never fetched unless the user explicitly downloads it.
/// </summary>
public sealed class SyncDispatcher
{
    private static readonly TimeSpan MinimumHeartbeat = TimeSpan.FromSeconds(5);
    private static readonly TimeSpan MaximumHeartbeat = TimeSpan.FromMinutes(5);
    private static readonly TimeSpan MinimumPongTimeout = TimeSpan.FromSeconds(10);
    private static readonly TimeSpan MaximumPongTimeout = TimeSpan.FromMinutes(10);

    private readonly SyncItemStore _store;
    private readonly IClipboardSink _clipboard;
    private readonly IClientLog _log;
    private readonly bool _autoCopyText;
    private WelcomeState? _welcome;

    public SyncDispatcher(
        SyncItemStore store,
        IClipboardSink clipboard,
        IClientLog log,
        bool autoCopyText = true)
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _clipboard = clipboard ?? throw new ArgumentNullException(nameof(clipboard));
        _log = log ?? throw new ArgumentNullException(nameof(log));
        _autoCopyText = autoCopyText;
    }

    public event Action<PresenceUpdate>? PresenceChanged;

    public WelcomeState? Welcome => Volatile.Read(ref _welcome);

    public void Handle(ServerEnvelope? envelope)
    {
        if (envelope?.Type is null)
        {
            return;
        }

        switch (envelope.Type)
        {
            case ProtocolTypes.Welcome:
                HandleWelcome(envelope);
                break;
            case ProtocolTypes.SyncRecord:
                HandleSyncRecord(envelope.Record);
                break;
            case ProtocolTypes.Presence:
                HandlePresence(envelope);
                break;
            case ProtocolTypes.Error:
                _log.Error(
                    $"server rejected a frame: {envelope.Code}: {envelope.Message} (fatal={envelope.Fatal})");
                break;
            case ProtocolTypes.HeartbeatAck:
            case ProtocolTypes.Pong:
                _log.Debug($"server {envelope.Type}");
                break;
            default:
                _log.Debug($"ignoring unknown server message type \"{envelope.Type}\"");
                break;
        }
    }

    private void HandleWelcome(ServerEnvelope envelope)
    {
        var heartbeat = ClampMilliseconds(envelope.HeartbeatIntervalMs ?? 25_000, MinimumHeartbeat, MaximumHeartbeat);
        var pong = ClampMilliseconds(envelope.PongTimeoutMs ?? 60_000, MinimumPongTimeout, MaximumPongTimeout);
        var devices = envelope.Devices ?? new List<DeviceView>();

        var welcome = new WelcomeState(
            envelope.ProtocolVersion ?? 1,
            envelope.DeviceId ?? string.Empty,
            envelope.ServerTime ?? 0,
            heartbeat,
            pong,
            devices.ToArray());

        Volatile.Write(ref _welcome, welcome);
        _log.Info(
            $"control plane registered device {welcome.DeviceId} " +
            $"(heartbeat {heartbeat.TotalSeconds:F0}s, {devices.Count} peer(s) online)");
    }

    private void HandlePresence(ServerEnvelope envelope)
    {
        if (envelope.Device is null)
        {
            _log.Warn("presence frame without a device object");
            return;
        }

        var state = string.IsNullOrWhiteSpace(envelope.State) ? "unknown" : envelope.State;
        try
        {
            PresenceChanged?.Invoke(new PresenceUpdate(state, envelope.Device));
        }
        catch (Exception exception)
        {
            _log.Warn("presence subscriber failed", exception);
        }
        _log.Info($"peer {state}: {envelope.Device.Name} ({envelope.Device.DeviceId})");
    }

    private void HandleSyncRecord(SyncRecord? record)
    {
        if (record is null)
        {
            _log.Warn("sync_record frame without a record object");
            return;
        }
        if (string.IsNullOrWhiteSpace(record.RecordId))
        {
            _log.Warn("sync record without record_id");
            return;
        }

        var payloadType = (record.Type ?? string.Empty).Trim().ToLowerInvariant();
        switch (payloadType)
        {
            case PayloadTypes.Text:
                HandleText(record);
                break;
            case PayloadTypes.Image:
            case PayloadTypes.File:
                HandleFilePlaceholder(record, payloadType);
                break;
            default:
                _log.Warn($"unsupported payload type \"{record.Type}\" in record {record.RecordId}");
                break;
        }
    }

    private void HandleText(SyncRecord record)
    {
        if (record.TextContent is null)
        {
            _log.Warn($"text record {record.RecordId} has no text_content");
            return;
        }

        var item = new SyncItem(
            record.RecordId,
            record.SenderDeviceId,
            record.CreatedAt,
            PayloadTypes.Text,
            record.TextContent,
            null,
            SyncItemState.Ready,
            null,
            null);

        if (!_store.TryAdd(item))
        {
            _log.Debug($"duplicate text record ignored: {record.RecordId}");
            return;
        }

        _log.Info($"text record {record.RecordId} received ({record.TextContent.Length} chars)");
        if (_autoCopyText && !_clipboard.TrySetText(record.TextContent))
        {
            _log.Warn("clipboard is busy or text was too large; not copied");
        }
    }

    private void HandleFilePlaceholder(SyncRecord record, string payloadType)
    {
        var meta = record.FileMeta;
        if (meta is null)
        {
            _log.Warn($"{payloadType} record {record.RecordId} has no file_meta");
            return;
        }

        var item = new SyncItem(
            record.RecordId,
            record.SenderDeviceId,
            record.CreatedAt,
            payloadType,
            null,
            meta,
            SyncItemState.Placeholder,
            null,
            null);

        if (_store.TryAdd(item))
        {
            _log.Info(
                $"{payloadType} placeholder {meta.FileName} ({meta.FileSize} bytes) " +
                $"record {record.RecordId}; body kept on server until requested");
        }
        else
        {
            _log.Debug($"duplicate {payloadType} record ignored: {record.RecordId}");
        }
    }

    private static TimeSpan ClampMilliseconds(long milliseconds, TimeSpan minimum, TimeSpan maximum)
    {
        if (milliseconds <= 0)
        {
            return minimum;
        }
        if (milliseconds >= maximum.TotalMilliseconds)
        {
            return maximum;
        }

        var value = TimeSpan.FromMilliseconds(milliseconds);
        return value < minimum ? minimum : value;
    }
}
