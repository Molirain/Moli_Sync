using System.Text.Json.Serialization;

namespace MoliSync.Client.Services;

public static class CloudKinds
{
    public const string Text = "text";
    public const string Image = "image";
    public const string File = "file";
}

public sealed class CloudRecord
{
    public string RecordId { get; set; } = string.Empty;

    public string SenderDeviceId { get; set; } = string.Empty;

    public string? SenderName { get; set; }

    public long CreatedAt { get; set; }

    public string Type { get; set; } = CloudKinds.Text;

    public string? TextContent { get; set; }

    public string? BlobId { get; set; }

    public string? FileName { get; set; }

    public long FileSize { get; set; }

    public string? MimeType { get; set; }

    public string? Blake3Hash { get; set; }

    [JsonIgnore]
    public bool IsText => string.Equals(Type, CloudKinds.Text, StringComparison.OrdinalIgnoreCase);

    [JsonIgnore]
    public bool IsImage => string.Equals(Type, CloudKinds.Image, StringComparison.OrdinalIgnoreCase);

    [JsonIgnore]
    public bool HasBlob => !string.IsNullOrWhiteSpace(BlobId);
}

public sealed class RecordsResponse
{
    public List<CloudRecord> Records { get; set; } = new();
}

public sealed class TextRecordRequest
{
    public string Text { get; set; } = string.Empty;
}

public sealed class ApiErrorEnvelope
{
    public ApiError Error { get; set; } = new();
}

public sealed class ApiError
{
    public string Code { get; set; } = string.Empty;

    public string Message { get; set; } = string.Empty;
}

/// <summary>Reply of <c>GET /api/v1/me</c>, including the server's own update status.</summary>
public sealed class ServerInfo
{
    public string DeviceId { get; set; } = string.Empty;

    public string ServerVersion { get; set; } = string.Empty;

    public ServerUpdateStatus? ServerUpdate { get; set; }
}

public sealed class ServerUpdateStatus
{
    public string Component { get; set; } = string.Empty;

    public string Current { get; set; } = string.Empty;

    public string? Latest { get; set; }

    public string? UpdateUrl { get; set; }

    public bool UpToDate { get; set; } = true;

    public string? LastError { get; set; }
}

public sealed class CloudException : Exception
{
    public CloudException(string message, int statusCode = 0, string? code = null, Exception? inner = null)
        : base(message, inner)
    {
        StatusCode = statusCode;
        Code = code;
    }

    public int StatusCode { get; }

    public string? Code { get; }
}

[JsonSourceGenerationOptions(
    PropertyNamingPolicy = JsonKnownNamingPolicy.SnakeCaseLower,
    PropertyNameCaseInsensitive = true,
    DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull)]
[JsonSerializable(typeof(CloudRecord))]
[JsonSerializable(typeof(RecordsResponse))]
[JsonSerializable(typeof(TextRecordRequest))]
[JsonSerializable(typeof(ApiErrorEnvelope))]
[JsonSerializable(typeof(ServerInfo))]
internal sealed partial class CloudJsonContext : JsonSerializerContext
{
}
