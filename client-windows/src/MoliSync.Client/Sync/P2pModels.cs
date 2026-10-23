using System.Text.Json.Serialization;

namespace MoliSync.Client.Sync;

public sealed class P2pFileMeta
{
    public string Name { get; set; } = string.Empty;

    public long Size { get; set; }

    public string? MimeType { get; set; }
}

public sealed class P2pManifest
{
    public int Version { get; set; } = 1;

    public string SessionId { get; set; } = string.Empty;

    public string Ticket { get; set; } = string.Empty;

    public string DeviceId { get; set; } = string.Empty;

    /// <summary>SHA-256 of the sender certificate, used to pin the peer.</summary>
    public string DeviceFingerprint { get; set; } = string.Empty;

    public string DeviceName { get; set; } = string.Empty;

    public List<P2pFileMeta> Files { get; set; } = new();
}

public sealed class SessionTicketMessage
{
    public string Type { get; set; } = "session_ticket";

    public string SessionId { get; set; } = string.Empty;

    public string Ticket { get; set; } = string.Empty;

    public string FromDeviceId { get; set; } = string.Empty;

    public string ToDeviceId { get; set; } = string.Empty;

    public List<DeviceView> Devices { get; set; } = new();

    public string? RelayUrl { get; set; }

    public long ExpiresAt { get; set; }

    public string? TransferName { get; set; }
}

public sealed record P2pSession(
    string SessionId,
    string Ticket,
    string FromDeviceId,
    string ToDeviceId,
    IReadOnlyList<DeviceView> Devices,
    string? RelayUrl,
    long ExpiresAt,
    string ToFingerprint = "")
{
    public bool IsExpired => ExpiresAt > 0 && DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() > ExpiresAt;
}

[JsonSourceGenerationOptions(
    PropertyNamingPolicy = JsonKnownNamingPolicy.SnakeCaseLower,
    DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull)]
[JsonSerializable(typeof(P2pManifest))]
[JsonSerializable(typeof(SessionTicketMessage))]
public sealed partial class P2pJsonContext : JsonSerializerContext
{
}

public static class P2pProtocol
{
    public const int MaxManifestBytes = 64 * 1024;
    public const int MaxFiles = 100;
    public const long MaxTotalBytes = 1L << 40;
}
