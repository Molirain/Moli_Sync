using System.Diagnostics;
using System.Net;
using System.Net.Sockets;
using MoliSync.Client.Logging;
using MoliSync.Client.Net;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class HappyEyeballsTests
{
    private static HappyEyeballsOptions FastOptions(RelayFallbackHandler? fallback = null) => new()
    {
        TotalTimeout = TimeSpan.FromMilliseconds(400),
        VpnStaggerDelay = TimeSpan.FromMilliseconds(100),
        CandidateStagger = TimeSpan.FromMilliseconds(20),
        RelayFallback = fallback,
    };

    [Fact]
    public async Task LanCandidateWinsImmediately()
    {
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        try
        {
            var port = ((IPEndPoint)listener.LocalEndpoint).Port;
            var acceptTask = listener.AcceptSocketAsync();
            var connector = new HappyEyeballsConnector(FastOptions(), NullClientLog.Instance);

            await using var connection = await connector.ConnectAsync(
                new[] { new P2PEndpoint("127.0.0.1", port, EndpointNetwork.Lan, 100) },
                timeout.Token);

            Assert.Equal(ConnectionPath.Lan, connection.Path);
            Assert.NotNull(connection.Endpoint);

            var accepted = await acceptTask.WaitAsync(TimeSpan.FromSeconds(3));
            accepted.Dispose();
        }
        finally
        {
            listener.Stop();
        }
    }

    [Fact]
    public async Task VpnCandidateWinsAfterLanFails()
    {
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));

        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        try
        {
            var vpnPort = ((IPEndPoint)listener.LocalEndpoint).Port;
            var closedPort = ReserveClosedPort(excluding: vpnPort);
            var acceptTask = listener.AcceptSocketAsync();
            var connector = new HappyEyeballsConnector(FastOptions(), NullClientLog.Instance);

            var stopwatch = Stopwatch.StartNew();
            await using var connection = await connector.ConnectAsync(
                new[]
                {
                    new P2PEndpoint("127.0.0.1", closedPort, EndpointNetwork.Lan, 100),
                    new P2PEndpoint("127.0.0.1", vpnPort, EndpointNetwork.Vpn, 50),
                },
                timeout.Token);
            stopwatch.Stop();

            Assert.Equal(ConnectionPath.Vpn, connection.Path);
            Assert.True(
                stopwatch.Elapsed >= TimeSpan.FromMilliseconds(80),
                $"VPN probe started too early: {stopwatch.Elapsed.TotalMilliseconds:F0} ms");

            var accepted = await acceptTask.WaitAsync(TimeSpan.FromSeconds(3));
            accepted.Dispose();
        }
        finally
        {
            listener.Stop();
        }
    }

    [Fact]
    public async Task TimeoutInvokesRelayFallback()
    {
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        var fallbackContext = new TaskCompletionSource<RelayFallbackContext>(
            TaskCreationOptions.RunContinuationsAsynchronously);

        var options = FastOptions(async (context, _) =>
        {
            fallbackContext.TrySetResult(context);
            return PeerConnection.FromRelay(new MemoryStream());
        });
        var connector = new HappyEyeballsConnector(options, NullClientLog.Instance, NeverConnectAsync);

        var stopwatch = Stopwatch.StartNew();
        await using var connection = await connector.ConnectAsync(
            new[] { new P2PEndpoint("127.0.0.1", 65000, EndpointNetwork.Lan, 100) },
            timeout.Token);
        stopwatch.Stop();

        Assert.Equal(ConnectionPath.Relay, connection.Path);
        Assert.True(stopwatch.Elapsed >= TimeSpan.FromMilliseconds(300));

        var context = await fallbackContext.Task.WaitAsync(TimeSpan.FromSeconds(2));
        Assert.Equal(RelayFallbackReason.Timeout, context.Reason);
    }

    [Fact]
    public async Task AllCandidatesFailingStillFallsBack()
    {
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        var fallbackContext = new TaskCompletionSource<RelayFallbackContext>(
            TaskCreationOptions.RunContinuationsAsynchronously);
        var options = FastOptions((context, _) =>
        {
            fallbackContext.TrySetResult(context);
            return Task.FromResult(PeerConnection.FromRelay(new MemoryStream()));
        });
        var connector = new HappyEyeballsConnector(
            options,
            NullClientLog.Instance,
            static (_, _) => Task.FromException<Socket>(new SocketException((int)SocketError.ConnectionRefused)));

        await using var connection = await connector.ConnectAsync(
            new[] { new P2PEndpoint("127.0.0.1", 65001, EndpointNetwork.Lan, 100) },
            timeout.Token);

        Assert.Equal(ConnectionPath.Relay, connection.Path);
        var context = await fallbackContext.Task.WaitAsync(TimeSpan.FromSeconds(2));
        Assert.Equal(RelayFallbackReason.AllCandidatesFailed, context.Reason);
        Assert.NotEmpty(context.Failures);
    }

    [Fact]
    public async Task MissingRelayHandlerThrowsTypedException()
    {
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        var connector = new HappyEyeballsConnector(
            FastOptions(),
            NullClientLog.Instance,
            static (_, _) => Task.FromException<Socket>(new SocketException((int)SocketError.ConnectionRefused)));

        var exception = await Assert.ThrowsAsync<RelayFallbackRequiredException>(
            () => connector.ConnectAsync(
                new[] { new P2PEndpoint("127.0.0.1", 65002, EndpointNetwork.Lan, 100) },
                timeout.Token));

        Assert.Equal(RelayFallbackReason.AllCandidatesFailed, exception.Context.Reason);
    }

    [Fact]
    public async Task CancellationIsHonoured()
    {
        using var cancellation = new CancellationTokenSource(TimeSpan.FromMilliseconds(100));
        var connector = new HappyEyeballsConnector(FastOptions(), NullClientLog.Instance, NeverConnectAsync);

        await Assert.ThrowsAnyAsync<OperationCanceledException>(
            () => connector.ConnectAsync(
                new[] { new P2PEndpoint("127.0.0.1", 65003, EndpointNetwork.Lan, 100) },
                cancellation.Token));
    }

    [Fact]
    public async Task DuplicateCandidatesAreProbedOnce()
    {
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        try
        {
            var port = ((IPEndPoint)listener.LocalEndpoint).Port;
            var acceptTask = listener.AcceptSocketAsync();
            var attempts = 0;
            var connector = new HappyEyeballsConnector(
                FastOptions(),
                NullClientLog.Instance,
                async (_, cancellationToken) =>
                {
                    Interlocked.Increment(ref attempts);
                    var socket = new Socket(SocketType.Stream, ProtocolType.Tcp);
                    await socket.ConnectAsync(IPAddress.Loopback, port, cancellationToken);
                    return socket;
                });

            await using var connection = await connector.ConnectAsync(
                new[]
                {
                    new P2PEndpoint("127.0.0.1", 65004, EndpointNetwork.Lan, 100),
                    new P2PEndpoint("127.0.0.1", 65004, EndpointNetwork.Lan, 100),
                },
                timeout.Token);

            Assert.Equal(1, Volatile.Read(ref attempts));
            var accepted = await acceptTask.WaitAsync(TimeSpan.FromSeconds(3));
            accepted.Dispose();
        }
        finally
        {
            listener.Stop();
        }
    }

    private static int ReserveClosedPort(int excluding)
    {
        while (true)
        {
            var probe = new TcpListener(IPAddress.Loopback, 0);
            probe.Start();
            var port = ((IPEndPoint)probe.LocalEndpoint).Port;
            probe.Stop();
            if (port != excluding)
            {
                return port;
            }
        }
    }

    private static Task<Socket> NeverConnectAsync(IPEndPoint endPoint, CancellationToken cancellationToken)
    {
        _ = endPoint;
        var source = new TaskCompletionSource<Socket>(TaskCreationOptions.RunContinuationsAsynchronously);
        cancellationToken.Register(() => source.TrySetCanceled(cancellationToken));
        return source.Task;
    }
}
