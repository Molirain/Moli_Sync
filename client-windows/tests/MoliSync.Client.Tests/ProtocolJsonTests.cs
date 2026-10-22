using System.Text.Json;
using MoliSync.Client.Sync;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class ProtocolJsonTests
{
    [Fact]
    public void DeserializesNestedSyncRecordWithSnakeCaseFields()
    {
        const string json = """
        {
          "type": "sync_record",
          "record": {
            "record_id": "record-1",
            "sender_device_id": "device-a",
            "created_at": 1760000000000,
            "type": "file",
            "file_meta": {
              "blob_id": "blob-1",
              "file_name": "report.pdf",
              "file_size": 4096,
              "mime_type": "application/pdf",
              "blake3_hash": "abc123"
            }
          }
        }
        """;

        var envelope = JsonSerializer.Deserialize(json, ProtocolJsonContext.Default.ServerEnvelope);

        Assert.NotNull(envelope);
        Assert.Equal(ProtocolTypes.SyncRecord, envelope.Type);
        var record = Assert.IsType<SyncRecord>(envelope.Record);
        Assert.Equal("record-1", record.RecordId);
        Assert.Equal("device-a", record.SenderDeviceId);
        Assert.Equal(1760000000000, record.CreatedAt);
        Assert.Equal(PayloadTypes.File, record.Type);
        var meta = Assert.IsType<CloudFileMeta>(record.FileMeta);
        Assert.Equal("blob-1", meta.BlobId);
        Assert.Equal("report.pdf", meta.FileName);
        Assert.Equal(4096, meta.FileSize);
        Assert.Equal("abc123", meta.Blake3Hash);
    }

    [Fact]
    public void DeserializesWelcomeAndPresenceFields()
    {
        const string json = """
        {
          "type": "welcome",
          "protocol_version": 1,
          "device_id": "device-a",
          "server_time": 1760000000000,
          "heartbeat_interval_ms": 25000,
          "pong_timeout_ms": 60000,
          "devices": [
            {
              "device_id": "device-c",
              "name": "PC C",
              "platform": "windows",
              "endpoints": [
                {"network": "lan", "ip": "192.168.1.20", "port": 9527, "priority": 100}
              ],
              "connected_at": 1760000000000,
              "last_seen": 1760000000001,
              "active_connections": 1
            }
          ]
        }
        """;

        var envelope = JsonSerializer.Deserialize(json, ProtocolJsonContext.Default.ServerEnvelope);

        Assert.NotNull(envelope);
        Assert.Equal(1, envelope.ProtocolVersion);
        Assert.Equal(25000, envelope.HeartbeatIntervalMs);
        var device = Assert.Single(Assert.IsAssignableFrom<IEnumerable<DeviceView>>(envelope.Devices));
        Assert.Equal("device-c", device.DeviceId);
        Assert.Equal("lan", Assert.Single(device.Endpoints).Network);
    }

    [Fact]
    public void SerializesHelloWithDiscriminatorAndEndpointArray()
    {
        var hello = new HelloMessage
        {
            Device = new DeviceDescriptor { Name = "Desktop C", Platform = "windows" },
            Endpoints = new List<EndpointModel>(),
        };

        var json = JsonSerializer.Serialize(hello, ProtocolJsonContext.Default.HelloMessage);
        using var document = JsonDocument.Parse(json);
        var root = document.RootElement;

        Assert.Equal("hello", root.GetProperty("type").GetString());
        Assert.Equal("Desktop C", root.GetProperty("device").GetProperty("name").GetString());
        Assert.Equal("windows", root.GetProperty("device").GetProperty("platform").GetString());
        Assert.Equal(JsonValueKind.Array, root.GetProperty("endpoints").ValueKind);
        Assert.Equal(0, root.GetProperty("endpoints").GetArrayLength());
    }

    [Fact]
    public void HeartbeatOmitsEndpointsWhenLeavingThePoolUntouched()
    {
        var heartbeat = new HeartbeatMessage();
        var json = JsonSerializer.Serialize(heartbeat, ProtocolJsonContext.Default.HeartbeatMessage);
        using var document = JsonDocument.Parse(json);

        Assert.Equal("heartbeat", document.RootElement.GetProperty("type").GetString());
        Assert.False(document.RootElement.TryGetProperty("endpoints", out _));
    }
}
