using System.Text.Json.Serialization;

namespace MoliSync.Client.Sync;

[JsonSourceGenerationOptions(
    PropertyNamingPolicy = JsonKnownNamingPolicy.SnakeCaseLower,
    PropertyNameCaseInsensitive = true,
    DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull)]
[JsonSerializable(typeof(ServerEnvelope))]
[JsonSerializable(typeof(HelloMessage))]
[JsonSerializable(typeof(HeartbeatMessage))]
[JsonSerializable(typeof(PingMessage))]
internal sealed partial class ProtocolJsonContext : JsonSerializerContext
{
}
