using System.Text;
using MoliSync.Client.Net;
using MoliSync.Client.Sync;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class P2pFramesTests
{
    [Fact]
    public async Task ManifestRoundTripsWithSnakeCaseFieldNames()
    {
        var manifest = new P2pManifest
        {
            SessionId = "session-1",
            Ticket = "dGlja2V0",
            DeviceId = "device-a",
            DeviceName = "书房电脑",
            Files =
            {
                new P2pFileMeta { Name = "photo.png", Size = 42, MimeType = "image/png" },
            },
        };

        using var buffer = new MemoryStream();
        await P2pFrames.WriteManifestAsync(buffer, manifest, CancellationToken.None);

        var raw = Encoding.UTF8.GetString(buffer.ToArray(), 4, (int)buffer.Length - 4);
        Assert.Contains("\"session_id\":\"session-1\"", raw, StringComparison.Ordinal);
        Assert.Contains("\"device_name\"", raw, StringComparison.Ordinal);
        Assert.Contains("\"mime_type\":\"image/png\"", raw, StringComparison.Ordinal);

        buffer.Position = 0;
        var restored = await P2pFrames.ReadManifestAsync(buffer, CancellationToken.None);

        Assert.Equal("session-1", restored.SessionId);
        Assert.Equal("device-a", restored.DeviceId);
        Assert.Equal("书房电脑", restored.DeviceName);
        Assert.Single(restored.Files);
        Assert.Equal(42, restored.Files[0].Size);
    }

    [Fact]
    public async Task DataFrameRoundTrips()
    {
        var payload = Encoding.UTF8.GetBytes("hello p2p");
        var hash = new byte[P2pFrames.HashSize];
        hash[0] = 7;

        using var buffer = new MemoryStream();
        P2pFrames.WriteData(buffer, 3, 1024, hash, payload);

        Assert.Equal(P2pFrames.HeaderSize + payload.Length, (int)buffer.Length);

        buffer.Position = 0;
        var frame = await P2pFrames.ReadAsync(buffer, CancellationToken.None);

        Assert.Equal(P2pFrameType.Data, frame.Type);
        Assert.Equal(3, frame.FileIndex);
        Assert.Equal(1024, frame.Offset);
        Assert.Equal(payload, frame.Payload);
        Assert.Equal(7, frame.Hash[0]);
    }

    [Fact]
    public void AckAndAbortFramesUseControlTypes()
    {
        using var buffer = new MemoryStream();
        P2pFrames.WriteAck(buffer, 2);
        P2pFrames.WriteAbort(buffer, 5, "cancelled");

        var bytes = buffer.ToArray();
        Assert.Equal(P2pFrames.HeaderSize * 2 + "cancelled".Length, bytes.Length);
        Assert.Equal(P2pFrameType.FileAck, bytes[0]);
        Assert.Equal(P2pFrameType.Abort, bytes[P2pFrames.HeaderSize]);
    }

    [Fact]
    public async Task RejectsOversizedPayloadLength()
    {
        using var buffer = new MemoryStream();
        var header = new byte[P2pFrames.HeaderSize];
        header[0] = P2pFrameType.Data;
        header[13] = 0xFF;
        header[14] = 0xFF;
        header[15] = 0xFF;
        header[16] = 0xFF;
        buffer.Write(header);
        buffer.Position = 0;

        await Assert.ThrowsAsync<InvalidDataException>(
            () => P2pFrames.ReadAsync(buffer, CancellationToken.None));
    }

    [Fact]
    public async Task RejectsInvalidManifestLength()
    {
        using var buffer = new MemoryStream();
        buffer.Write(new byte[] { 0xFF, 0xFF, 0xFF, 0xFF });
        buffer.Position = 0;

        await Assert.ThrowsAsync<InvalidDataException>(
            () => P2pFrames.ReadManifestAsync(buffer, CancellationToken.None));
    }
}
