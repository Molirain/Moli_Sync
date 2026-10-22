using System.Buffers.Binary;
using System.Text.Json;
using MoliSync.Client.Sync;

namespace MoliSync.Client.Net;

/// <summary>
/// Fixed data-plane frame, identical to the Android implementation:
///
///   0  1B  frame type
///   1  4B  file index, big endian
///   5  8B  offset, big endian
///   13 4B  payload length, big endian
///   17 32B BLAKE3 digest of the payload (zeros for control frames)
///   49 NB  payload
/// </summary>
public static class P2pFrameType
{
    public const byte Data = 1;
    public const byte ResumeRequest = 2;
    public const byte FileAck = 3;
    public const byte Abort = 255;
}

public sealed record P2pFrame(byte Type, int FileIndex, long Offset, byte[] Hash, byte[] Payload);

public static class P2pFrames
{
    public const int HeaderSize = 49;
    public const int HashSize = 32;
    public const int MaxPayload = 4 * 1024 * 1024;

    private static readonly byte[] ZeroHash = new byte[HashSize];

    public static void Write(
        Stream output,
        byte type,
        int fileIndex,
        long offset,
        ReadOnlySpan<byte> hash,
        ReadOnlySpan<byte> payload)
    {
        var header = new byte[HeaderSize];
        header[0] = type;
        BinaryPrimitives.WriteInt32BigEndian(header.AsSpan(1), fileIndex);
        BinaryPrimitives.WriteInt64BigEndian(header.AsSpan(5), offset);
        BinaryPrimitives.WriteInt32BigEndian(header.AsSpan(13), payload.Length);
        hash.CopyTo(header.AsSpan(17));
        output.Write(header);
        if (!payload.IsEmpty)
        {
            output.Write(payload);
        }
    }

    public static void WriteData(Stream output, int fileIndex, long offset, byte[] hash, byte[] payload)
    {
        Write(output, P2pFrameType.Data, fileIndex, offset, hash, payload);
    }

    public static void WriteAck(Stream output, int fileIndex)
    {
        Write(output, P2pFrameType.FileAck, fileIndex, 0, ZeroHash, ReadOnlySpan<byte>.Empty);
    }

    public static void WriteAbort(Stream output, int fileIndex, string reason)
    {
        var payload = System.Text.Encoding.UTF8.GetBytes(reason.Length > 1024 ? reason[..1024] : reason);
        Write(output, P2pFrameType.Abort, fileIndex, 0, ZeroHash, payload);
    }

    public static async Task<P2pFrame> ReadAsync(Stream input, CancellationToken cancellationToken)
    {
        var header = new byte[HeaderSize];
        await input.ReadExactlyAsync(header, cancellationToken).ConfigureAwait(false);

        var type = header[0];
        var fileIndex = BinaryPrimitives.ReadInt32BigEndian(header.AsSpan(1));
        var offset = BinaryPrimitives.ReadInt64BigEndian(header.AsSpan(5));
        var length = BinaryPrimitives.ReadInt32BigEndian(header.AsSpan(13));
        if (length < 0 || length > MaxPayload)
        {
            throw new InvalidDataException("P2P frame payload length is invalid");
        }

        var hash = header.AsSpan(17, HashSize).ToArray();
        var payload = new byte[length];
        if (length > 0)
        {
            await input.ReadExactlyAsync(payload, cancellationToken).ConfigureAwait(false);
        }
        return new P2pFrame(type, fileIndex, offset, hash, payload);
    }

    public static async Task WriteManifestAsync(
        Stream output,
        P2pManifest manifest,
        CancellationToken cancellationToken)
    {
        var payload = JsonSerializer.SerializeToUtf8Bytes(manifest, P2pJsonContext.Default.P2pManifest);
        if (payload.Length > P2pProtocol.MaxManifestBytes)
        {
            throw new InvalidDataException("P2P manifest is too large");
        }
        var header = new byte[4];
        BinaryPrimitives.WriteInt32BigEndian(header, payload.Length);
        output.Write(header);
        output.Write(payload);
        await output.FlushAsync(cancellationToken).ConfigureAwait(false);
    }

    public static async Task<P2pManifest> ReadManifestAsync(Stream input, CancellationToken cancellationToken)
    {
        var header = new byte[4];
        await input.ReadExactlyAsync(header, cancellationToken).ConfigureAwait(false);
        var length = BinaryPrimitives.ReadInt32BigEndian(header);
        if (length <= 0 || length > P2pProtocol.MaxManifestBytes)
        {
            throw new InvalidDataException("P2P manifest length is invalid");
        }

        var payload = new byte[length];
        await input.ReadExactlyAsync(payload, cancellationToken).ConfigureAwait(false);
        return JsonSerializer.Deserialize(payload, P2pJsonContext.Default.P2pManifest)
            ?? throw new InvalidDataException("P2P manifest is empty");
    }
}
