using System.Diagnostics;
using System.Net;
using System.Net.Sockets;
using System.Threading.Channels;
using MoliSync.Client.Logging;

namespace MoliSync.Client.Net;

public sealed class HappyEyeballsOptions
{
    /// <summary>Hard deadline after which the connection falls back to the relay.</summary>
    public TimeSpan TotalTimeout { get; init; } = TimeSpan.FromMilliseconds(1200);

    /// <summary>Head start given to LAN probes before VPN probes are launched.</summary>
    public TimeSpan VpnStaggerDelay { get; init; } = TimeSpan.FromMilliseconds(300);

    /// <summary>Spacing between probes within the same network class.</summary>
    public TimeSpan CandidateStagger { get; init; } = TimeSpan.FromMilliseconds(50);

    /// <summary>
    /// Invoked when no direct connection wins. The handler is responsible for
    /// opening the server relay transport; if it is null a
    /// <see cref="RelayFallbackRequiredException"/> is thrown.
    /// </summary>
    public RelayFallbackHandler? RelayFallback { get; init; }

    internal void Validate()
    {
        if (TotalTimeout < TimeSpan.FromMilliseconds(100) || TotalTimeout > TimeSpan.FromSeconds(30))
        {
            throw new ArgumentOutOfRangeException(nameof(TotalTimeout), "total timeout must be between 100ms and 30s");
        }
        if (VpnStaggerDelay < TimeSpan.Zero || VpnStaggerDelay >= TotalTimeout)
        {
            throw new ArgumentOutOfRangeException(nameof(VpnStaggerDelay), "VPN stagger must be non-negative and smaller than the total timeout");
        }
        if (CandidateStagger < TimeSpan.Zero || CandidateStagger > TimeSpan.FromSeconds(1))
        {
            throw new ArgumentOutOfRangeException(nameof(CandidateStagger), "candidate stagger must be between 0 and 1s");
        }
    }
}

/// <summary>
/// Happy Eyeballs style racer for the multi-homed P2P candidate pool.
///
/// LAN probes start immediately, staggered per candidate. VPN probes start
/// after the VPN stagger delay (300 ms by default) while LAN probes are still
/// running. The first successful TCP connection wins and all other probes are
/// cancelled. If no direct path succeeds within the total deadline (1.2 s by
/// default), the relay fallback handler is invoked; without a handler the
/// caller receives a <see cref="RelayFallbackRequiredException"/>.
/// </summary>
public sealed class HappyEyeballsConnector
{
    private readonly HappyEyeballsOptions _options;
    private readonly IClientLog _log;
    private readonly Func<IPEndPoint, CancellationToken, Task<Socket>> _connect;

    public HappyEyeballsConnector(
        HappyEyeballsOptions options,
        IClientLog log,
        Func<IPEndPoint, CancellationToken, Task<Socket>>? connect = null)
    {
        ArgumentNullException.ThrowIfNull(options);
        ArgumentNullException.ThrowIfNull(log);
        options.Validate();

        _options = options;
        _log = log;
        _connect = connect ?? ConnectTcpAsync;
    }

    public async Task<PeerConnection> ConnectAsync(
        IReadOnlyList<P2PEndpoint> candidates,
        CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(candidates);

        var started = Stopwatch.StartNew();
        var failures = new List<string>();
        var endpoints = PrepareCandidates(candidates, failures);

        if (endpoints.Count == 0)
        {
            return await FallbackAsync(
                new RelayFallbackContext(
                    RelayFallbackReason.AllCandidatesFailed,
                    started.Elapsed,
                    endpoints,
                    failures),
                cancellationToken).ConfigureAwait(false);
        }

        using var raceCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        var results = Channel.CreateUnbounded<AttemptResult>(new UnboundedChannelOptions
        {
            SingleReader = true,
            SingleWriter = false,
            AllowSynchronousContinuations = false,
        });

        var attempts = new List<Task>(endpoints.Count);
        var lanIndex = 0;
        var vpnIndex = 0;
        foreach (var endpoint in endpoints)
        {
            TimeSpan delay;
            if (endpoint.Network == EndpointNetwork.Lan)
            {
                delay = _options.CandidateStagger * lanIndex++;
            }
            else
            {
                delay = _options.VpnStaggerDelay + _options.CandidateStagger * vpnIndex++;
            }
            attempts.Add(AttemptAsync(endpoint, delay, results.Writer, raceCancellation.Token));
        }

        var pending = endpoints.Count;
        var deadline = Task.Delay(_options.TotalTimeout, CancellationToken.None);
        AttemptResult? winner = null;
        var timedOut = false;

        while (pending > 0)
        {
            var readTask = results.Reader.ReadAsync(CancellationToken.None).AsTask();
            var completed = await Task.WhenAny(readTask, deadline).ConfigureAwait(false);
            if (completed != readTask)
            {
                timedOut = true;
                break;
            }

            var result = await readTask.ConfigureAwait(false);
            pending--;

            if (result.Socket is not null)
            {
                winner = result;
                break;
            }
            if (!string.IsNullOrEmpty(result.Failure))
            {
                failures.Add(result.Failure);
                _log.Debug($"P2P probe failed: {result.Failure}");
            }
        }

        raceCancellation.Cancel();
        await Task.WhenAll(attempts).ConfigureAwait(false);

        // Probes that completed after the race was decided still own sockets.
        while (results.Reader.TryRead(out var leftover))
        {
            leftover.Socket?.Dispose();
        }

        cancellationToken.ThrowIfCancellationRequested();

        if (winner?.Socket is not null)
        {
            _log.Info(
                $"P2P direct connection established via {winner.Endpoint!.Network.ToString().ToUpperInvariant()} " +
                $"{winner.Endpoint.IpAddress}:{winner.Endpoint.Port} in {started.Elapsed.TotalMilliseconds:F0} ms");
            return PeerConnection.FromSocket(winner.Endpoint, winner.Socket);
        }

        var reason = timedOut ? RelayFallbackReason.Timeout : RelayFallbackReason.AllCandidatesFailed;
        return await FallbackAsync(
            new RelayFallbackContext(reason, started.Elapsed, endpoints, failures),
            cancellationToken).ConfigureAwait(false);
    }

    private List<P2PEndpoint> PrepareCandidates(
        IReadOnlyList<P2PEndpoint> candidates,
        List<string> failures)
    {
        var prepared = new List<P2PEndpoint>(candidates.Count);
        var seen = new HashSet<string>(StringComparer.Ordinal);

        foreach (var candidate in candidates)
        {
            if (candidate is null)
            {
                continue;
            }
            if (candidate.Port is < 1 or > 65535)
            {
                failures.Add($"{candidate.IpAddress}: invalid port {candidate.Port}");
                continue;
            }
            if (!IPAddress.TryParse(candidate.IpAddress, out var address))
            {
                failures.Add($"{candidate.IpAddress}: invalid IP address");
                continue;
            }
            if (address.AddressFamily == AddressFamily.InterNetworkV6 && address.IsIPv6LinkLocal)
            {
                failures.Add($"{candidate.IpAddress}: link-local IPv6 requires a scope and is not supported");
                continue;
            }

            var key = $"{address}|{candidate.Port}";
            if (!seen.Add(key))
            {
                continue;
            }

            prepared.Add(new P2PEndpoint(
                address.ToString(),
                candidate.Port,
                candidate.Network,
                candidate.Priority));
        }

        prepared.Sort(static (left, right) =>
        {
            var byPriority = right.Priority.CompareTo(left.Priority);
            if (byPriority != 0)
            {
                return byPriority;
            }
            var byNetwork = left.Network.CompareTo(right.Network);
            if (byNetwork != 0)
            {
                return byNetwork;
            }
            var byAddress = string.CompareOrdinal(left.IpAddress, right.IpAddress);
            return byAddress != 0 ? byAddress : left.Port.CompareTo(right.Port);
        });
        return prepared;
    }

    private async Task AttemptAsync(
        P2PEndpoint endpoint,
        TimeSpan delay,
        ChannelWriter<AttemptResult> writer,
        CancellationToken cancellationToken)
    {
        Socket? socket = null;
        var label = $"{endpoint.IpAddress}:{endpoint.Port}";
        try
        {
            if (delay > TimeSpan.Zero)
            {
                await Task.Delay(delay, cancellationToken).ConfigureAwait(false);
            }

            socket = await _connect(endpoint.ToIPEndPoint(), cancellationToken).ConfigureAwait(false);
            await writer.WriteAsync(new AttemptResult(endpoint, socket, null), CancellationToken.None)
                .ConfigureAwait(false);
            socket = null; // ownership transferred to the race result
        }
        catch (OperationCanceledException)
        {
            await TryWriteAsync(writer, new AttemptResult(endpoint, null, $"{label}: canceled"))
                .ConfigureAwait(false);
        }
        catch (Exception exception)
        {
            await TryWriteAsync(
                    writer,
                    new AttemptResult(endpoint, null, $"{label}: {exception.GetType().Name}: {exception.Message}"))
                .ConfigureAwait(false);
        }
        finally
        {
            socket?.Dispose();
        }
    }

    private async Task<PeerConnection> FallbackAsync(
        RelayFallbackContext context,
        CancellationToken cancellationToken)
    {
        _log.Warn(
            $"no direct P2P path ({context.Reason}) after {context.Elapsed.TotalMilliseconds:F0} ms; " +
            "switching to server relay");

        if (_options.RelayFallback is null)
        {
            throw new RelayFallbackRequiredException(context);
        }

        var connection = await _options.RelayFallback(context, cancellationToken).ConfigureAwait(false);
        return connection ?? throw new InvalidOperationException("relay fallback handler returned no connection");
    }

    private static async Task TryWriteAsync(ChannelWriter<AttemptResult> writer, AttemptResult result)
    {
        try
        {
            await writer.WriteAsync(result, CancellationToken.None).ConfigureAwait(false);
        }
        catch (ChannelClosedException)
        {
            // The race has already finished; the result is no longer needed.
        }
    }

    private static async Task<Socket> ConnectTcpAsync(IPEndPoint endpoint, CancellationToken cancellationToken)
    {
        var socket = new Socket(endpoint.AddressFamily, SocketType.Stream, ProtocolType.Tcp)
        {
            NoDelay = true,
        };
        try
        {
            await socket.ConnectAsync(endpoint, cancellationToken).ConfigureAwait(false);
            return socket;
        }
        catch
        {
            socket.Dispose();
            throw;
        }
    }

    private sealed record AttemptResult(P2PEndpoint Endpoint, Socket? Socket, string? Failure);
}
