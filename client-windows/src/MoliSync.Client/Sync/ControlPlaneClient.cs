using System.Buffers;
using System.Diagnostics;
using System.Net.WebSockets;
using System.Text.Json;
using System.Text.Json.Serialization.Metadata;
using MoliSync.Client.Logging;
using MoliSync.Client.Security;

namespace MoliSync.Client.Sync;

/// <summary>
/// Long-lived WebSocket control-plane connection. Uses the client certificate
/// object from the Windows store directly through SslStream (no key export),
/// advertises the device, sends application heartbeats and reconnects with
/// bounded exponential backoff and jitter.
/// </summary>
public sealed class ControlPlaneClient
{
    private const int ReceiveBufferSize = 16 * 1024;
    private const int MaxMessageBytes = 1024 * 1024;

    private static readonly TimeSpan DefaultHeartbeat = TimeSpan.FromSeconds(25);
    private static readonly TimeSpan MinimumHeartbeat = TimeSpan.FromSeconds(5);
    private static readonly TimeSpan MaximumHeartbeat = TimeSpan.FromMinutes(5);

    private readonly ClientOptions _options;
    private readonly ClientIdentity _identity;
    private readonly PinnedServerCertificateValidator _serverValidator;
    private readonly SyncDispatcher _dispatcher;
    private readonly IClientLog _log;
    private readonly TimeSpan _connectTimeout;
    private long _heartbeatIntervalTicks = DefaultHeartbeat.Ticks;
    private readonly object _socketGate = new();
    private ClientWebSocket? _activeSocket;

    /// <summary>Supplies the local P2P candidate addresses advertised in hello.</summary>
    public Func<IReadOnlyList<EndpointModel>>? EndpointProvider { get; set; }

    /// <summary>Raised for every server-issued transfer ticket.</summary>
    public event Action<P2pSession>? SessionTicketReceived;

    public ControlPlaneClient(
        ClientOptions options,
        ClientIdentity identity,
        PinnedServerCertificateValidator serverValidator,
        SyncDispatcher dispatcher,
        IClientLog log)
    {
        _options = options ?? throw new ArgumentNullException(nameof(options));
        _identity = identity ?? throw new ArgumentNullException(nameof(identity));
        _serverValidator = serverValidator ?? throw new ArgumentNullException(nameof(serverValidator));
        _dispatcher = dispatcher ?? throw new ArgumentNullException(nameof(dispatcher));
        _log = log ?? throw new ArgumentNullException(nameof(log));
        _connectTimeout = TimeSpan.FromSeconds(options.ConnectTimeoutSeconds);
    }

    public async Task RunAsync(CancellationToken cancellationToken)
    {
        var backoff = TimeSpan.FromSeconds(1);

        while (!cancellationToken.IsCancellationRequested)
        {
            var started = Stopwatch.StartNew();
            var welcomed = false;

            try
            {
                welcomed = await RunSessionAsync(cancellationToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                break;
            }
            catch (Exception exception)
            {
                _log.Error("control session ended with an error", exception);
            }

            if (cancellationToken.IsCancellationRequested)
            {
                break;
            }

            if (welcomed || started.Elapsed >= TimeSpan.FromMinutes(1))
            {
                backoff = TimeSpan.FromSeconds(1);
            }

            var jitter = 0.8 + Random.Shared.NextDouble() * 0.4;
            var delay = TimeSpan.FromMilliseconds(backoff.TotalMilliseconds * jitter);
            _log.Info($"reconnecting the control plane in {delay.TotalSeconds:F1}s");

            try
            {
                await Task.Delay(delay, cancellationToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                break;
            }

            backoff = TimeSpan.FromSeconds(Math.Min(backoff.TotalSeconds * 2, 30));
        }
    }

    private async Task<bool> RunSessionAsync(CancellationToken cancellationToken)
    {
        using var socket = new ClientWebSocket();
        socket.Options.ClientCertificates.Add(_identity.Certificate);
        socket.Options.RemoteCertificateValidationCallback = _serverValidator.Validate;
        socket.Options.KeepAliveInterval = TimeSpan.FromSeconds(20);

        _log.Info($"connecting to {_options.WebSocketUri}");
        using (var connectTimeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken))
        {
            connectTimeout.CancelAfter(_connectTimeout);
            try
            {
                await socket.ConnectAsync(_options.WebSocketUri, connectTimeout.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                throw new TimeoutException($"mTLS connection to {_options.WebSocketUri} timed out");
            }
        }

        _log.Info($"control connection established for device {_identity.DeviceId}");

        using var sendGate = new SemaphoreSlim(1, 1);
        var hello = new HelloMessage
        {
            Device = new DeviceDescriptor
            {
                Name = _options.DeviceName,
                Platform = _options.Platform,
            },
            Endpoints = EndpointProvider?.Invoke()?.ToList() ?? new List<EndpointModel>(),
        };
        lock (_socketGate)
        {
            _activeSocket = socket;
        }
        await SendAsync(socket, sendGate, hello, ProtocolJsonContext.Default.HelloMessage, cancellationToken)
            .ConfigureAwait(false);

        using var sessionCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        var heartbeatTask = HeartbeatLoopAsync(socket, sendGate, sessionCancellation.Token);
        try
        {
            return await ReceiveLoopAsync(socket, cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            lock (_socketGate)
            {
                if (ReferenceEquals(_activeSocket, socket))
                {
                    _activeSocket = null;
                }
            }
            sessionCancellation.Cancel();
            try
            {
                await heartbeatTask.ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                // Expected when the session ends.
            }
            catch (Exception exception)
            {
                _log.Debug($"heartbeat loop stopped: {exception.Message}");
            }
        }
    }

    private async Task<bool> ReceiveLoopAsync(ClientWebSocket socket, CancellationToken cancellationToken)
    {
        var buffer = ArrayPool<byte>.Shared.Rent(ReceiveBufferSize);
        using var frame = new MemoryStream(16 * 1024);
        var welcomed = false;
        var binaryMessage = false;

        try
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                var result = await socket
                    .ReceiveAsync(buffer.AsMemory(0, ReceiveBufferSize), cancellationToken)
                    .ConfigureAwait(false);

                if (result.MessageType == WebSocketMessageType.Close)
                {
                    _log.Info(
                        $"control connection closed by server: {socket.CloseStatus} {socket.CloseStatusDescription}");
                    break;
                }

                if (result.MessageType == WebSocketMessageType.Binary)
                {
                    binaryMessage = true;
                }
                else if (!binaryMessage)
                {
                    if (frame.Length + result.Count > MaxMessageBytes)
                    {
                        throw new InvalidDataException($"control frame exceeds {MaxMessageBytes} bytes");
                    }
                    frame.Write(buffer, 0, result.Count);
                }

                if (!result.EndOfMessage)
                {
                    continue;
                }

                if (binaryMessage)
                {
                    _log.Warn("ignored a binary control frame; control plane frames must be JSON text");
                }
                else if (frame.Length > 0)
                {
                    ProcessFrame(frame.GetBuffer().AsSpan(0, (int)frame.Length), ref welcomed);
                }

                frame.SetLength(0);
                binaryMessage = false;
            }
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer);
        }

        return welcomed;
    }

    private void ProcessFrame(ReadOnlySpan<byte> payload, ref bool welcomed)
    {
        ServerEnvelope? envelope;
        try
        {
            envelope = JsonSerializer.Deserialize(payload, ProtocolJsonContext.Default.ServerEnvelope);
        }
        catch (JsonException exception)
        {
            _log.Warn("received an invalid JSON control frame", exception);
            return;
        }

        if (envelope is null)
        {
            return;
        }

        if (string.Equals(envelope.Type, ProtocolTypes.SessionTicket, StringComparison.Ordinal) &&
            !string.IsNullOrWhiteSpace(envelope.SessionId) &&
            !string.IsNullOrWhiteSpace(envelope.Ticket) &&
            !string.IsNullOrWhiteSpace(envelope.FromDeviceId) &&
            !string.IsNullOrWhiteSpace(envelope.ToDeviceId))
        {
            var session = new P2pSession(
                envelope.SessionId,
                envelope.Ticket,
                envelope.FromDeviceId,
                envelope.ToDeviceId,
                envelope.Devices ?? new List<DeviceView>(),
                envelope.RelayUrl,
                envelope.ExpiresAt ?? 0,
                (envelope.Devices ?? new List<DeviceView>())
                    .FirstOrDefault(device => string.Equals(device.DeviceId, envelope.ToDeviceId, StringComparison.Ordinal))
                    ?.PinFingerprint ?? string.Empty);
            try
            {
                SessionTicketReceived?.Invoke(session);
            }
            catch (Exception exception)
            {
                _log.Error("session ticket subscriber failed", exception);
            }
        }

        if (string.Equals(envelope.Type, ProtocolTypes.Welcome, StringComparison.Ordinal))
        {
            welcomed = true;
            var intervalMs = envelope.HeartbeatIntervalMs ?? (long)DefaultHeartbeat.TotalMilliseconds;
            var clamped = Math.Clamp(
                intervalMs,
                (long)MinimumHeartbeat.TotalMilliseconds,
                (long)MaximumHeartbeat.TotalMilliseconds);
            Volatile.Write(ref _heartbeatIntervalTicks, TimeSpan.FromMilliseconds(clamped).Ticks);
        }

        try
        {
            _dispatcher.Handle(envelope);
        }
        catch (Exception exception)
        {
            _log.Error("failed to dispatch a control frame", exception);
        }
    }

    /// <summary>
    /// Asks the server for a transfer grant to another online device. The
    /// resulting ticket arrives asynchronously through
    /// <see cref="SessionTicketReceived"/> on both participants.
    /// </summary>
    public async Task<bool> RequestSessionAsync(
        string toDeviceId,
        string? transferName,
        CancellationToken cancellationToken)
    {
        ClientWebSocket? socket;
        lock (_socketGate)
        {
            socket = _activeSocket;
        }
        if (socket is null || socket.State != WebSocketState.Open)
        {
            return false;
        }

        var payload = JsonSerializer.SerializeToUtf8Bytes(
            new OpenSessionMessage { ToDeviceId = toDeviceId, TransferName = transferName },
            ProtocolJsonContext.Default.OpenSessionMessage);
        await socket
            .SendAsync(payload.AsMemory(), WebSocketMessageType.Text, endOfMessage: true, cancellationToken)
            .ConfigureAwait(false);
        return true;
    }

    private async Task HeartbeatLoopAsync(
        ClientWebSocket socket,
        SemaphoreSlim sendGate,
        CancellationToken cancellationToken)
    {
        try
        {
            while (!cancellationToken.IsCancellationRequested)
            {
                var interval = TimeSpan.FromTicks(Volatile.Read(ref _heartbeatIntervalTicks));
                await Task.Delay(interval, cancellationToken).ConfigureAwait(false);

                await SendAsync(
                        socket,
                        sendGate,
                        new HeartbeatMessage(),
                        ProtocolJsonContext.Default.HeartbeatMessage,
                        cancellationToken)
                    .ConfigureAwait(false);

                _log.Debug("application heartbeat sent");
            }
        }
        catch (Exception) when (!cancellationToken.IsCancellationRequested)
        {
            // Unblock the receive loop so the session can reconnect.
            socket.Abort();
            throw;
        }
    }

    private static async Task SendAsync<T>(
        ClientWebSocket socket,
        SemaphoreSlim sendGate,
        T message,
        JsonTypeInfo<T> typeInfo,
        CancellationToken cancellationToken)
    {
        var payload = JsonSerializer.SerializeToUtf8Bytes(message, typeInfo);

        await sendGate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (socket.State != WebSocketState.Open)
            {
                throw new InvalidOperationException("control socket is not open");
            }
            await socket
                .SendAsync(payload.AsMemory(), WebSocketMessageType.Text, endOfMessage: true, cancellationToken)
                .ConfigureAwait(false);
        }
        finally
        {
            sendGate.Release();
        }
    }
}
