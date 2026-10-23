using MoliSync.Client.Http;
using MoliSync.Client.Logging;
using MoliSync.Client.Platform;
using MoliSync.Client.Security;
using MoliSync.Client.Sync;

namespace MoliSync.Client.Services;

// AppSession owns the full connected identity: HTTP data plane, control plane
// and the P2P listener bound to the same certificate.

/// <summary>
/// A complete connected identity: certificate, HTTP data plane, control-plane
/// socket, clipboard dispatcher and data directory. Replacing connection
/// settings disposes this object and creates a new one.
/// </summary>
public sealed class AppSession : IAsyncDisposable
{
    private readonly PinnedCertificateAuthority? _authority;
    private readonly HttpClient _http;
    private readonly WindowsClipboardSink _clipboard;
    private readonly PinnedServerCertificateValidator _serverValidator;
    private readonly IClientLog _log;
    private readonly CancellationTokenSource _cancellation = new();
    private Task? _controlTask;
    private bool _disposed;
    private string? _assignedDeviceId;

    private string SelfDeviceId() =>
        Volatile.Read(ref _assignedDeviceId) is { Length: > 0 } assigned ? assigned : Identity.DeviceId;

    private void OnWelcome(WelcomeState state)
    {
        if (!string.IsNullOrWhiteSpace(state.DeviceId))
        {
            Volatile.Write(ref _assignedDeviceId, state.DeviceId);
        }
    }

    private void OnSessionTicketReceived(P2pSession ticket)
    {
        if (string.Equals(ticket.ToDeviceId, SelfDeviceId(), StringComparison.Ordinal))
        {
            P2p.Register(ticket);
        }
    }
    private AppSession(
        AppSettings settings,
        PinnedCertificateAuthority? authority,
        ClientIdentity identity,
        HttpClient http,
        WindowsClipboardSink clipboard,
        SyncDispatcher dispatcher,
        CloudRepository cloud,
        DataDirectoryManager dataDirectory,
        ControlPlaneClient control,
        P2pListener p2p,
        PinnedServerCertificateValidator serverValidator,
        UpdateChecker updates,
        IClientLog log)
    {
        Settings = settings;
        _authority = authority;
        Identity = identity;
        _http = http;
        _clipboard = clipboard;
        Dispatcher = dispatcher;
        Cloud = cloud;
        DataDirectory = dataDirectory;
        Control = control;
        P2p = p2p;
        Updates = updates;
        _serverValidator = serverValidator;
        _log = log;
    }

    public AppSettings Settings { get; }

    public ClientIdentity Identity { get; }

    public SyncDispatcher Dispatcher { get; }

    public CloudRepository Cloud { get; }

    public DataDirectoryManager DataDirectory { get; }

    public ControlPlaneClient Control { get; }

    public P2pListener P2p { get; }

    /// <summary>Desktop-only release check; the cache lives in the data directory.</summary>
    public UpdateChecker Updates { get; }

    /// <summary>
    /// DeviceID this client has on the server. It matches the certificate
    /// fingerprint unless the server derives identity from the common name.
    /// </summary>
    public string DeviceId => SelfDeviceId();

    /// <summary>Sender bound to this session's certificate and trust policy.</summary>
    public P2pSender CreateP2pSender() =>
        new(
            Identity,
            _serverValidator,
            new Uri(Settings.ServerUrl.TrimEnd('/') + "/", UriKind.Absolute),
            _log,
            SelfDeviceId);

    public static AppSession Create(AppSettings settings, IClientLog log)
    {
        ArgumentNullException.ThrowIfNull(settings);
        settings.Normalize();
        settings.Validate();

        PinnedCertificateAuthority? authority = null;
        if (!string.IsNullOrWhiteSpace(settings.CaCertificatePath))
        {
            authority = PinnedCertificateAuthority.Load(
                settings.CaCertificatePath,
                settings.ExpectedIssuerName);
        }

        ClientIdentity? identity = null;
        HttpClient? http = null;
        WindowsClipboardSink? clipboard = null;
        try
        {
            var certificateStore = new WindowsCertificateStore(log);
            var criteria = new CertificateSelectionCriteria { Thumbprint = settings.ClientCertificateThumbprint };
            identity = authority is null
                ? certificateStore.SelectIdentity(criteria)
                : certificateStore.SelectIdentity(authority, criteria);

            var validator = new PinnedServerCertificateValidator(authority, log);
            http = MtlsHttpClientFactory.Create(
                identity,
                validator,
                TimeSpan.FromSeconds(settings.ConnectTimeoutSeconds));
            clipboard = new WindowsClipboardSink(log);
            var syncStore = new SyncItemStore(log);
            var dispatcher = new SyncDispatcher(syncStore, clipboard, log, settings.AutoCopyText);
            var options = settings.ToClientOptions();
            var control = new ControlPlaneClient(options, identity, validator, dispatcher, log);
            var dataDirectory = new DataDirectoryManager(settings.DataDirectory);
            var cloud = new CloudRepository(http, settings.ServerUrl, dataDirectory, log);

            // Release checks talk to the public GitHub API directly, so they
            // must not reuse the mTLS client: the client certificate is bound
            // to the MoliSync server and would leak the identity elsewhere.
            var updateHttp = new HttpClient { Timeout = TimeSpan.FromSeconds(15) };
            var updates = new UpdateChecker(
                updateHttp,
                log,
                Path.Combine(settings.DataDirectory, "update-desktop.json"),
                typeof(AppSession).Assembly.GetName().Version?.ToString(3) ?? "0.0.0");

            // The listener is created before the control plane starts so the
            // first hello already advertises a real port.
            var p2p = new P2pListener(identity, settings.DataDirectory, log);
            p2p.Start();
            control.EndpointProvider = () => p2p.Endpoints();
            // The welcome frame carries the DeviceID the server assigned, which
            // is derived from the certificate common name in dn_cn mode.
            var appSession = new AppSession(
                settings,
                authority,
                identity,
                http,
                clipboard,
                dispatcher,
                cloud,
                dataDirectory,
                control,
                p2p,
                validator,
                updates,
                log);
            control.SessionTicketReceived += appSession.OnSessionTicketReceived;
            dispatcher.WelcomeReceived += appSession.OnWelcome;
            p2p.UseSelfDeviceId(appSession.SelfDeviceId);
            p2p.UseRelay(appSession.CreateP2pSender().OpenRelayStreamAsync);

            return appSession;
        }
        catch
        {
            http?.Dispose();
            clipboard?.DisposeAsync().AsTask().GetAwaiter().GetResult();
            identity?.Dispose();
            authority?.Dispose();
            throw;
        }
    }

    public Task StartAsync()
    {
        _controlTask ??= Task.Run(() => Control.RunAsync(_cancellation.Token));
        return Task.CompletedTask;
    }

    public async Task<bool> WaitForWelcomeAsync(TimeSpan timeout, CancellationToken cancellationToken)
    {
        var completion = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        void OnWelcome(WelcomeState _) => completion.TrySetResult(true);
        Dispatcher.WelcomeReceived += OnWelcome;
        try
        {
            await StartAsync().ConfigureAwait(false);
            using var timeoutCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeoutCancellation.CancelAfter(timeout);
            using var registration = timeoutCancellation.Token.Register(
                static state => ((TaskCompletionSource<bool>)state!).TrySetResult(false),
                completion);
            return await completion.Task.ConfigureAwait(false);
        }
        finally
        {
            Dispatcher.WelcomeReceived -= OnWelcome;
        }
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }
        _disposed = true;

        _cancellation.Cancel();
        if (_controlTask is not null)
        {
            try
            {
                await _controlTask.ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                // Expected during shutdown.
            }
            catch (Exception)
            {
                // The connection is already going away.
            }
        }

        await _clipboard.DisposeAsync().ConfigureAwait(false);
        await P2p.DisposeAsync().ConfigureAwait(false);
        _http.Dispose();
        Identity.Dispose();
        _authority?.Dispose();
        _cancellation.Dispose();
    }
}
