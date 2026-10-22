using System.Net;
using System.Net.Sockets;

namespace MoliSync.Client.Net;

public enum EndpointNetwork
{
    Lan = 0,
    Vpn = 1,
}

/// <summary>One candidate P2P endpoint reported by the server for a target device.</summary>
public sealed record P2PEndpoint(string IpAddress, int Port, EndpointNetwork Network, int Priority)
{
    public IPEndPoint ToIPEndPoint()
    {
        if (!IPAddress.TryParse(IpAddress, out var address))
        {
            throw new FormatException($"invalid endpoint IP address: {IpAddress}");
        }
        if (Port is < 1 or > 65535)
        {
            throw new FormatException($"invalid endpoint port: {Port}");
        }
        return new IPEndPoint(address, Port);
    }
}

public enum ConnectionPath
{
    Lan = 0,
    Vpn = 1,
    Relay = 2,
}

/// <summary>
/// An established byte stream. For LAN/VPN paths the stream is a NetworkStream
/// over the winning TCP socket; the caller must perform peer authentication
/// (mTLS plus the session ticket proof) before sending any payload.
/// </summary>
public sealed class PeerConnection : IAsyncDisposable
{
    private readonly Socket? _socket;

    private PeerConnection(ConnectionPath path, P2PEndpoint? endpoint, Stream stream, Socket? socket)
    {
        Path = path;
        Endpoint = endpoint;
        Stream = stream;
        _socket = socket;
    }

    public ConnectionPath Path { get; }

    public P2PEndpoint? Endpoint { get; }

    public Stream Stream { get; }

    public static PeerConnection FromSocket(P2PEndpoint endpoint, Socket socket)
    {
        ArgumentNullException.ThrowIfNull(endpoint);
        ArgumentNullException.ThrowIfNull(socket);

        var path = endpoint.Network == EndpointNetwork.Lan ? ConnectionPath.Lan : ConnectionPath.Vpn;
        return new PeerConnection(path, endpoint, new NetworkStream(socket, ownsSocket: true), socket);
    }

    public static PeerConnection FromRelay(Stream stream)
    {
        ArgumentNullException.ThrowIfNull(stream);
        return new PeerConnection(ConnectionPath.Relay, null, stream, null);
    }

    public async ValueTask DisposeAsync()
    {
        await Stream.DisposeAsync().ConfigureAwait(false);
        _socket?.Dispose();
    }
}

public enum RelayFallbackReason
{
    AllCandidatesFailed = 0,
    Timeout = 1,
}

public sealed record RelayFallbackContext(
    RelayFallbackReason Reason,
    TimeSpan Elapsed,
    IReadOnlyList<P2PEndpoint> Candidates,
    IReadOnlyList<string> Failures);

public delegate Task<PeerConnection> RelayFallbackHandler(
    RelayFallbackContext context,
    CancellationToken cancellationToken);

public sealed class RelayFallbackRequiredException : Exception
{
    public RelayFallbackRequiredException(RelayFallbackContext context)
        : base($"P2P connection failed ({context.Reason}); no relay fallback handler is configured")
    {
        Context = context;
    }

    public RelayFallbackContext Context { get; }
}
