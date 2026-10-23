using System.Collections.Concurrent;
using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Authentication;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using MoliSync.Client.Logging;
using MoliSync.Client.Net;
using MoliSync.Client.Security;

namespace MoliSync.Client.Sync;

/// <summary>
/// Inbound P2P listener. It binds an ephemeral TLS port, advertises it through
/// the control plane, and accepts a connection only when the sender proves the
/// server-issued session ticket and its certificate fingerprint matches the
/// DeviceID in the manifest.
/// </summary>
public sealed class P2pListener : IAsyncDisposable
{
    private readonly ClientIdentity _identity;
    private readonly string _dataDirectory;
    private readonly IClientLog _log;
    private readonly ConcurrentDictionary<string, P2pSession> _pending = new(StringComparer.Ordinal);
    private readonly CancellationTokenSource _cancellation = new();
    private Func<P2pSession, CancellationToken, Task<Stream>>? _relayOpener;
    private Func<string> _selfDeviceId = () => string.Empty;
    private TcpListener? _listener;
    private Task? _acceptLoop;

    public P2pListener(ClientIdentity identity, string dataDirectory, IClientLog log)
    {
        _identity = identity ?? throw new ArgumentNullException(nameof(identity));
        _dataDirectory = dataDirectory ?? throw new ArgumentNullException(nameof(dataDirectory));
        _log = log ?? throw new ArgumentNullException(nameof(log));
        _selfDeviceId = () => identity.DeviceId;
    }

    public event Action<string>? FileReceived;

    public int Port { get; private set; }

    public IReadOnlyList<EndpointModel> Endpoints() => LocalEndpoints.Candidates(Port);

    /// <summary>
    /// Supplies the DeviceID the server assigned to this client. It differs
    /// from the certificate fingerprint when the server derives identity from
    /// the certificate common name, and session tickets carry that value.
    /// </summary>
    public void UseSelfDeviceId(Func<string> provider) =>
        _selfDeviceId = provider ?? (() => _identity.DeviceId);

    public void Start()
    {
        if (_listener is not null)
        {
            return;
        }

        var listener = new TcpListener(IPAddress.Any, 0);
        listener.Start(16);
        _listener = listener;
        Port = ((IPEndPoint)listener.LocalEndpoint).Port;
        _log.Info($"P2P listener bound on port {Port}");

        _acceptLoop = Task.Run(() => AcceptLoopAsync(listener, _cancellation.Token));
    }

    public void Register(P2pSession session)
    {
        if (session.IsExpired)
        {
            return;
        }
        _pending[session.SessionId] = session;
        if (_pending.Count > 32)
        {
            var now = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            foreach (var pair in _pending.Where(pair => pair.Value.ExpiresAt > 0 && pair.Value.ExpiresAt < now))
            {
                _pending.TryRemove(pair.Key, out _);
            }
        }

        // A direct connection may never arrive (NAT, firewall); wait on the
        // server relay in parallel so the sender's fallback finds a peer.
        var opener = _relayOpener;
        if (opener is not null && !string.IsNullOrEmpty(session.RelayUrl))
        {
            _ = Task.Run(() => RelayStandbyAsync(session, opener, _cancellation.Token), CancellationToken.None);
        }
    }

    /// <summary>
    /// Supplies the relay dialer used for standby connections. The listener
    /// stays independent of the HTTP client and TLS policy.
    /// </summary>
    public void UseRelay(Func<P2pSession, CancellationToken, Task<Stream>> opener) =>
        _relayOpener = opener ?? throw new ArgumentNullException(nameof(opener));

    private async Task RelayStandbyAsync(
        P2pSession session,
        Func<P2pSession, CancellationToken, Task<Stream>> opener,
        CancellationToken cancellationToken)
    {
        try
        {
            await using var stream = await opener(session, cancellationToken).ConfigureAwait(false);
            var manifest = await P2pFrames.ReadManifestAsync(stream, cancellationToken).ConfigureAwait(false);
            ValidateManifest(manifest);
            if (!string.Equals(manifest.SessionId, session.SessionId, StringComparison.Ordinal) ||
                !string.Equals(manifest.Ticket, session.Ticket, StringComparison.Ordinal) ||
                !string.Equals(manifest.DeviceId, session.FromDeviceId, StringComparison.Ordinal))
            {
                throw new InvalidDataException("relay manifest does not match the session ticket");
            }

            if (!_pending.TryRemove(session.SessionId, out _))
            {
                // The direct link already claimed this session.
                return;
            }

            var saved = await ReceiveAsync(stream, manifest, cancellationToken).ConfigureAwait(false);
            foreach (var path in saved)
            {
                FileReceived?.Invoke(path);
            }
            _log.Info($"P2P relay received {saved.Count} file(s) from {manifest.DeviceName}");
        }
        catch (OperationCanceledException)
        {
            // Shutdown or a direct connection won the race.
        }
        catch (Exception exception)
        {
            _log.Debug($"P2P relay standby closed: {exception.Message}");
        }
    }

    private async Task AcceptLoopAsync(TcpListener listener, CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            TcpClient client;
            try
            {
                client = await listener.AcceptTcpClientAsync(cancellationToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            catch (Exception exception)
            {
                _log.Warn("P2P accept failed", exception);
                continue;
            }

            _ = Task.Run(() => HandleAsync(client, cancellationToken), CancellationToken.None);
        }
    }

    private async Task HandleAsync(TcpClient client, CancellationToken cancellationToken)
    {
        using (client)
        await using (var ssl = new SslStream(client.GetStream(), leaveInnerStreamOpen: false))
        {
            try
            {
                client.NoDelay = true;
                await ssl.AuthenticateAsServerAsync(new SslServerAuthenticationOptions
                {
                    ServerCertificate = _identity.Certificate,
                    ClientCertificateRequired = true,
                    EnabledSslProtocols = SslProtocols.Tls12 | SslProtocols.Tls13,
                    CertificateRevocationCheckMode = X509RevocationMode.NoCheck,
                    // The TLS layer only proves key possession here; identity is
                    // pinned to the ticket's DeviceID right below.
                    RemoteCertificateValidationCallback = static (_, certificate, _, _) => certificate is not null,
                }, cancellationToken).ConfigureAwait(false);

                var peer = ssl.RemoteCertificate as X509Certificate2;
                if (peer is null)
                {
                    throw new InvalidDataException("peer did not present a client certificate");
                }

                var manifest = await P2pFrames.ReadManifestAsync(ssl, cancellationToken).ConfigureAwait(false);
                ValidateManifest(manifest);

                if (!_pending.TryGetValue(manifest.SessionId, out var session))
                {
                    throw new InvalidDataException("no pending P2P session for this connection");
                }
                if (!string.Equals(session.ToDeviceId, _selfDeviceId(), StringComparison.Ordinal))
                {
                    throw new InvalidDataException("session does not belong to this device");
                }
                if (!string.Equals(session.Ticket, manifest.Ticket, StringComparison.Ordinal) ||
                    !string.Equals(session.FromDeviceId, manifest.DeviceId, StringComparison.Ordinal))
                {
                    throw new InvalidDataException("session ticket does not match the manifest");
                }

                var fingerprint = Convert.ToHexString(SHA256.HashData(peer.RawData)).ToLowerInvariant();
                var pinned = !string.IsNullOrWhiteSpace(manifest.DeviceFingerprint)
                    ? manifest.DeviceFingerprint
                    : manifest.DeviceId;
                if (!DeviceView.IsFingerprint(pinned) ||
                    !string.Equals(fingerprint, pinned, StringComparison.OrdinalIgnoreCase))
                {
                    throw new InvalidDataException("peer certificate fingerprint does not match the sender");
                }

                // Consume the ticket only after the sender proved both the secret
                // and its certificate identity; the atomic remove also rejects a
                // second concurrent connection for the same session.
                if (!_pending.TryRemove(manifest.SessionId, out _))
                {
                    throw new InvalidDataException("session is already in use");
                }

                var saved = await ReceiveAsync(ssl, manifest, cancellationToken).ConfigureAwait(false);
                foreach (var path in saved)
                {
                    FileReceived?.Invoke(path);
                }
                _log.Info($"P2P received {saved.Count} file(s) from {manifest.DeviceName}");
            }
            catch (Exception exception)
            {
                _log.Warn("P2P receive failed", exception);
            }
        }
    }

    private static void ValidateManifest(P2pManifest manifest)
    {
        if (string.IsNullOrWhiteSpace(manifest.SessionId) || manifest.SessionId.Length > 128 ||
            string.IsNullOrWhiteSpace(manifest.Ticket) || manifest.Ticket.Length > 128 ||
            string.IsNullOrWhiteSpace(manifest.DeviceId) || manifest.DeviceId.Length > 128)
        {
            throw new InvalidDataException("P2P manifest identity fields are invalid");
        }
        if (manifest.Files.Count == 0 || manifest.Files.Count > P2pProtocol.MaxFiles)
        {
            throw new InvalidDataException("P2P manifest file count is invalid");
        }

        var total = 0L;
        foreach (var file in manifest.Files)
        {
            if (string.IsNullOrWhiteSpace(file.Name) || file.Name.Length > 255 || file.Size < 0)
            {
                throw new InvalidDataException("P2P manifest contains an invalid file");
            }
            if (file.Size > P2pProtocol.MaxTotalBytes || total > P2pProtocol.MaxTotalBytes - file.Size)
            {
                throw new InvalidDataException("P2P transfer exceeds the size limit");
            }
            total += file.Size;
        }
    }

    private async Task<IReadOnlyList<string>> ReceiveAsync(
        Stream stream,
        P2pManifest manifest,
        CancellationToken cancellationToken)
    {
        Directory.CreateDirectory(_dataDirectory);
        var staging = Path.Combine(_dataDirectory, "p2p", manifest.SessionId);
        Directory.CreateDirectory(staging);

        var targets = new string[manifest.Files.Count];
        var streams = new FileStream[manifest.Files.Count];
        var written = new long[manifest.Files.Count];
        var completed = 0;

        try
        {
            for (var index = 0; index < manifest.Files.Count; index++)
            {
                targets[index] = Path.Combine(staging, $"{index}-{Sanitize(manifest.Files[index].Name)}");
                streams[index] = new FileStream(
                    targets[index], FileMode.Create, FileAccess.Write, FileShare.None, 128 * 1024, useAsync: true);
                if (manifest.Files[index].Size == 0)
                {
                    // Empty files never produce a data frame, so they are complete on arrival.
                    await streams[index].DisposeAsync().ConfigureAwait(false);
                    completed++;
                }
            }

            while (completed < manifest.Files.Count)
            {
                var frame = await P2pFrames.ReadAsync(stream, cancellationToken).ConfigureAwait(false);
                switch (frame.Type)
                {
                    case P2pFrameType.Data:
                        if (frame.FileIndex < 0 || frame.FileIndex >= manifest.Files.Count)
                        {
                            throw new InvalidDataException("P2P frame index is invalid");
                        }
                        if (frame.Offset != written[frame.FileIndex])
                        {
                            throw new InvalidDataException("P2P frame offset is not contiguous");
                        }
                        await streams[frame.FileIndex]
                            .WriteAsync(frame.Payload, cancellationToken)
                            .ConfigureAwait(false);
                        written[frame.FileIndex] += frame.Payload.Length;
                        if (written[frame.FileIndex] > manifest.Files[frame.FileIndex].Size)
                        {
                            throw new InvalidDataException("P2P file exceeds its declared size");
                        }
                        if (written[frame.FileIndex] == manifest.Files[frame.FileIndex].Size)
                        {
                            await streams[frame.FileIndex].DisposeAsync().ConfigureAwait(false);
                            completed++;
                        }
                        break;

                    case P2pFrameType.Abort:
                        throw new InvalidDataException(
                            "sender aborted: " + System.Text.Encoding.UTF8.GetString(frame.Payload));

                    default:
                        throw new InvalidDataException("unknown P2P frame type");
                }
            }

            for (var index = 0; index < manifest.Files.Count; index++)
            {
                P2pFrames.WriteAck(stream, index);
            }
            await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            foreach (var fileStream in streams)
            {
                if (fileStream is not null)
                {
                    await fileStream.DisposeAsync().ConfigureAwait(false);
                }
            }
        }

        var saved = new List<string>(manifest.Files.Count);
        for (var index = 0; index < manifest.Files.Count; index++)
        {
            var meta = manifest.Files[index];
            if (written[index] != meta.Size)
            {
                throw new InvalidDataException($"{meta.Name} was not received completely");
            }
            var destination = UniqueDestination(_dataDirectory, Sanitize(meta.Name));
            File.Move(targets[index], destination, overwrite: false);
            saved.Add(destination);
        }

        try
        {
            Directory.Delete(staging, recursive: true);
        }
        catch (IOException)
        {
            // Staging cleanup is best effort.
        }
        return saved;
    }

    private static string UniqueDestination(string directory, string fileName)
    {
        var candidate = Path.Combine(directory, fileName);
        if (!File.Exists(candidate))
        {
            return candidate;
        }
        var stem = Path.GetFileNameWithoutExtension(fileName);
        var extension = Path.GetExtension(fileName);
        for (var index = 1; ; index++)
        {
            candidate = Path.Combine(directory, $"{stem} ({index}){extension}");
            if (!File.Exists(candidate))
            {
                return candidate;
            }
        }
    }

    internal static string Sanitize(string name)
    {
        var invalid = Path.GetInvalidFileNameChars().ToHashSet();
        var cleaned = new string(name.Select(character =>
            invalid.Contains(character) || char.IsControl(character) ? '_' : character).ToArray())
            .Trim()
            .TrimEnd('.');
        if (string.IsNullOrWhiteSpace(cleaned))
        {
            cleaned = "MoliSync-file";
        }
        return cleaned.Length <= 120 ? cleaned : cleaned[..120];
    }

    public async ValueTask DisposeAsync()
    {
        _cancellation.Cancel();
        _listener?.Stop();
        _listener = null;
        if (_acceptLoop is not null)
        {
            try
            {
                await _acceptLoop.ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                // Expected during shutdown.
            }
        }
        _cancellation.Dispose();
    }
}
