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
[JsonSerializable(typeof(OpenSessionMessage))]
internal sealed partial class ProtocolJsonContext : JsonSerializerContext
{
}
