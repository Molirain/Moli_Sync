using System.Net.Security;
using System.Net.Sockets;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using MoliSync.Client.Logging;
using MoliSync.Client.Net;
using MoliSync.Client.Security;
using MoliSync.Client.Services;

namespace MoliSync.Client.Sync;

/// <summary>
/// Outbound P2P transfer. Direct LAN/VPN candidates are raced with the Happy
/// Eyeballs connector; if none answers the same session continues through the
/// server relay. The peer is authenticated by its certificate fingerprint,
/// which is also its DeviceID.
/// </summary>
public sealed class P2pSender
{
    private readonly ClientIdentity _identity;
    private readonly PinnedServerCertificateValidator _serverValidator;
    private readonly Uri _serverUri;
    private readonly IClientLog _log;

    public P2pSender(
        ClientIdentity identity,
        PinnedServerCertificateValidator serverValidator,
        Uri serverUri,
        IClientLog log)
    {
        _identity = identity ?? throw new ArgumentNullException(nameof(identity));
        _serverValidator = serverValidator ?? throw new ArgumentNullException(nameof(serverValidator));
        _serverUri = serverUri ?? throw new ArgumentNullException(nameof(serverUri));
        _log = log ?? throw new ArgumentNullException(nameof(log));
    }

    public async Task SendAsync(
        P2pSession session,
        IReadOnlyList<EndpointModel> endpoints,
        IReadOnlyList<string> paths,
        string deviceName,
        IProgress<long>? progress,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(session);
        ArgumentNullException.ThrowIfNull(paths);
        if (paths.Count == 0)
        {
            throw new ArgumentException("no files to send", nameof(paths));
        }

        var files = paths
            .Select(path => new P2pFileMeta
            {
                Name = Path.GetFileName(path),
                Size = new FileInfo(path).Length,
                MimeType = MimeTypes.FromFileName(path),
            })
            .ToArray();
        var total = files.Sum(file => file.Size);

        var candidates = endpoints
            .Select(endpoint => new P2PEndpoint(
                endpoint.Ip,
                endpoint.Port,
                string.Equals(endpoint.Network, EndpointNetworks.Vpn, StringComparison.Ordinal)
                    ? EndpointNetwork.Vpn
                    : EndpointNetwork.Lan,
                endpoint.Priority))
            .ToArray();

        var connector = new HappyEyeballsConnector(
            new HappyEyeballsOptions
            {
                RelayFallback = (_, token) => OpenRelayAsync(session, token),
            },
            _log);

        await using var connection = await connector.ConnectAsync(candidates, cancellationToken)
            .ConfigureAwait(false);

        Stream stream = connection.Stream;
        SslStream? peerTls = null;
        try
        {
            if (connection.Path != ConnectionPath.Relay)
            {
                peerTls = new SslStream(stream, leaveInnerStreamOpen: false, ValidatePeerCertificate);
                await peerTls.AuthenticateAsClientAsync(new SslClientAuthenticationOptions
                {
                    TargetHost = connection.Endpoint!.IpAddress,
                    ClientCertificates = new X509CertificateCollection { _identity.Certificate },
                    EnabledSslProtocols = SslProtocols.Tls12 | SslProtocols.Tls13,
                    CertificateRevocationCheckMode = X509RevocationMode.NoCheck,
                    // Identity is pinned by fingerprint instead of a shared CA.
                    RemoteCertificateValidationCallback = (_, certificate, _, _) =>
                        certificate is not null && MatchesTarget(certificate, session.ToFingerprint),
                }, cancellationToken).ConfigureAwait(false);
                stream = peerTls;
            }

            await P2pFrames.WriteManifestAsync(stream, new P2pManifest
            {
                SessionId = session.SessionId,
                Ticket = session.Ticket,
                DeviceId = _identity.DeviceId,
                DeviceFingerprint = _identity.DeviceId,
                DeviceName = deviceName,
                Files = files.ToList(),
            }, cancellationToken).ConfigureAwait(false);

            var sent = 0L;
            var buffer = new byte[256 * 1024];
            for (var index = 0; index < paths.Count; index++)
            {
                cancellationToken.ThrowIfCancellationRequested();
                var file = files[index];
                var fileOffset = 0L;
                await using var source = new FileStream(
                    paths[index],
                    FileMode.Open,
                    FileAccess.Read,
                    FileShare.Read,
                    buffer.Length,
                    FileOptions.Asynchronous | FileOptions.SequentialScan);

                if (source.Length != file.Size)
                {
                    throw new IOException($"{file.Name} changed after the transfer started");
                }

                int read;
                while ((read = await source.ReadAsync(buffer, cancellationToken).ConfigureAwait(false)) > 0)
                {
                    var chunk = read == buffer.Length ? buffer : buffer[..read];
                    P2pFrames.WriteData(stream, index, fileOffset, new byte[P2pFrames.HashSize], chunk);
                    fileOffset += read;
                    sent += read;
                    progress?.Report(sent);
                }

                if (fileOffset != file.Size)
                {
                    P2pFrames.WriteAbort(stream, index, "source file changed");
                    await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
                    throw new IOException($"{file.Name} was read incompletely");
                }
            }

            await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
            _log.Info(
                $"P2P sent {paths.Count} file(s), {total} bytes via {connection.Path}");
        }
        finally
        {
            if (peerTls is not null)
            {
                await peerTls.DisposeAsync().ConfigureAwait(false);
            }
        }
    }

    private bool MatchesTarget(X509Certificate certificate, string expectedFingerprint)
    {
        if (!DeviceView.IsFingerprint(expectedFingerprint))
        {
            return false;
        }

        var raw = certificate as X509Certificate2 ?? new X509Certificate2(certificate);
        var fingerprint = Convert.ToHexString(SHA256.HashData(raw.RawData)).ToLowerInvariant();
        return string.Equals(fingerprint, expectedFingerprint, StringComparison.Ordinal);
    }

    private bool ValidatePeerCertificate(
        object sender,
        X509Certificate? certificate,
        X509Chain? chain,
        SslPolicyErrors errors)
    {
        _ = sender;
        _ = chain;
        _ = errors;
        return certificate is not null;
    }

    private async Task<PeerConnection> OpenRelayAsync(P2pSession session, CancellationToken cancellationToken)
    {
        var stream = await OpenRelayStreamAsync(session, cancellationToken).ConfigureAwait(false);
        _log.Info("P2P relay stream established");
        return PeerConnection.FromRelay(stream);
    }

    /// <summary>
    /// Opens the server relay for a session. The caller owns the returned
    /// stream; bytes flowing through it are untouched by the server.
    /// </summary>
    public async Task<Stream> OpenRelayStreamAsync(P2pSession session, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(session);
        var relayUrl = session.RelayUrl
            ?? throw new InvalidOperationException("server did not provide a relay URL");

        var target = new Uri(_serverUri, relayUrl.TrimStart('/'));
        var host = target.Host;
        var port = target.IsDefaultPort ? 443 : target.Port;

        var tcp = new TcpClient();
        try
        {
            await tcp.ConnectAsync(host, port, cancellationToken).ConfigureAwait(false);
            var ssl = new SslStream(tcp.GetStream(), leaveInnerStreamOpen: false, _serverValidator.Validate);
            await ssl.AuthenticateAsClientAsync(new SslClientAuthenticationOptions
            {
                TargetHost = host,
                ClientCertificates = new X509CertificateCollection { _identity.Certificate },
                EnabledSslProtocols = SslProtocols.Tls12 | SslProtocols.Tls13,
                CertificateRevocationCheckMode = X509RevocationMode.NoCheck,
                RemoteCertificateValidationCallback = _serverValidator.Validate,
            }, cancellationToken).ConfigureAwait(false);

            var request = new StringBuilder()
                .Append("GET ").Append(target.PathAndQuery).Append(" HTTP/1.1\r\n")
                .Append("Host: ").Append(host).Append(':').Append(port).Append("\r\n")
                .Append("X-Session-Ticket: ").Append(session.Ticket).Append("\r\n")
                .Append("Connection: close\r\n")
                .Append("User-Agent: MoliSync-Windows\r\n\r\n")
                .ToString();
            var payload = Encoding.ASCII.GetBytes(request);
            await ssl.WriteAsync(payload, cancellationToken).ConfigureAwait(false);
            await ssl.FlushAsync(cancellationToken).ConfigureAwait(false);

            var status = await ReadLineAsync(ssl, cancellationToken).ConfigureAwait(false);
            var code = status.Split(' ') is { Length: >= 2 } parts && int.TryParse(parts[1], out var parsed)
                ? parsed
                : 0;
            while (!string.IsNullOrEmpty(await ReadLineAsync(ssl, cancellationToken).ConfigureAwait(false)))
            {
            }

            if (code != 200)
            {
                await ssl.DisposeAsync().ConfigureAwait(false);
                throw new IOException($"relay rejected the session (HTTP {code})");
            }

            return ssl;
        }
        catch
        {
            tcp.Dispose();
            throw;
        }
    }

    private static async Task<string> ReadLineAsync(Stream stream, CancellationToken cancellationToken)
    {
        var builder = new StringBuilder();
        var single = new byte[1];
        while (true)
        {
            var read = await stream.ReadAsync(single, cancellationToken).ConfigureAwait(false);
            if (read == 0)
            {
                throw new EndOfStreamException("relay closed before the response line completed");
            }
            if (single[0] == (byte)'\n')
            {
                return builder.ToString();
            }
            if (single[0] != (byte)'\r')
            {
                builder.Append((char)single[0]);
            }
            if (builder.Length > 8 * 1024)
            {
                throw new InvalidDataException("relay response line is too long");
            }
        }
    }
}
