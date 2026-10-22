namespace MoliSync.Client.Sync;

public static class ProtocolTypes
{
    public const string Hello = "hello";
    public const string Heartbeat = "heartbeat";
    public const string Ping = "ping";
    public const string Welcome = "welcome";
    public const string HeartbeatAck = "heartbeat_ack";
    public const string Pong = "pong";
    public const string Presence = "presence";
    public const string Error = "error";
    public const string SyncRecord = "sync_record";
    public const string OpenSession = "open_session";
    public const string SessionTicket = "session_ticket";
}

public static class PayloadTypes
{
    public const string Text = "text";
    public const string Image = "image";
    public const string File = "file";
}

public static class EndpointNetworks
{
    public const string Lan = "lan";
    public const string Vpn = "vpn";
}

public sealed class DeviceDescriptor
{
    public string Name { get; set; } = string.Empty;

    public string Platform { get; set; } = "windows";
}

public sealed class EndpointModel
{
    public string Network { get; set; } = EndpointNetworks.Lan;

    public string Ip { get; set; } = string.Empty;

    public int Port { get; set; }

    public int Priority { get; set; }
}

public sealed class DeviceView
{
    public string DeviceId { get; set; } = string.Empty;

    public string? CommonName { get; set; }

    public string Name { get; set; } = string.Empty;

    public string Platform { get; set; } = string.Empty;

    public List<EndpointModel> Endpoints { get; set; } = new();

    public long ConnectedAt { get; set; }

    public long LastSeen { get; set; }

    public int Connections { get; set; }
}

public sealed class HelloMessage
{
    public string Type { get; set; } = ProtocolTypes.Hello;

    public DeviceDescriptor Device { get; set; } = new();

    public List<EndpointModel> Endpoints { get; set; } = new();
}

public sealed class HeartbeatMessage
{
    public string Type { get; set; } = ProtocolTypes.Heartbeat;

    /// <summary>Omitted when null, which keeps the previously reported pool.</summary>
    public List<EndpointModel>? Endpoints { get; set; }
}

public sealed class PingMessage
{
    public string Type { get; set; } = ProtocolTypes.Ping;

    public long ClientTime { get; set; }
}

public sealed class SyncRecord
{
    public string RecordId { get; set; } = string.Empty;

    public string SenderDeviceId { get; set; } = string.Empty;

    public long CreatedAt { get; set; }

    public string Type { get; set; } = string.Empty;

    public string? TextContent { get; set; }

    public CloudFileMeta? FileMeta { get; set; }
}

public sealed class CloudFileMeta
{
    public string BlobId { get; set; } = string.Empty;

    public string FileName { get; set; } = string.Empty;

    public long FileSize { get; set; }

    public string? MimeType { get; set; }

    public string? Blake3Hash { get; set; }

    public byte[]? ThumbnailWebp { get; set; }
}

/// <summary>
/// The JSON control frame. The server sends exactly one object per WebSocket
/// text frame with a "type" discriminator. SyncRecord is carried under
/// "record" so its own "type" field does not collide with the envelope tag.
/// </summary>
public sealed class ServerEnvelope
{
    public string? Type { get; set; }

    public int? ProtocolVersion { get; set; }

    public string? DeviceId { get; set; }

    public long? ServerTime { get; set; }

    public long? HeartbeatIntervalMs { get; set; }

    public long? PongTimeoutMs { get; set; }

    public List<DeviceView>? Devices { get; set; }

    public string? State { get; set; }

    public DeviceView? Device { get; set; }

    public bool? EndpointsChanged { get; set; }

    public string? Code { get; set; }

    public string? Message { get; set; }

    public bool? Fatal { get; set; }

    public SyncRecord? Record { get; set; }

    public string? SessionId { get; set; }

    public string? Ticket { get; set; }

    public string? FromDeviceId { get; set; }

    public string? ToDeviceId { get; set; }

    public string? RelayUrl { get; set; }

    public long? ExpiresAt { get; set; }
}

public sealed class OpenSessionMessage
{
    public string Type { get; set; } = ProtocolTypes.OpenSession;

    public string ToDeviceId { get; set; } = string.Empty;

    public string? TransferName { get; set; }
}
