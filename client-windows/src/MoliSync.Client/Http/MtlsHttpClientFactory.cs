using System.Net;
using System.Net.Security;
using System.Security.Authentication;
using System.Security.Cryptography.X509Certificates;
using MoliSync.Client.Security;

namespace MoliSync.Client.Http;

public static class MtlsHttpClientFactory
{
    /// <summary>
    /// Creates an HTTP client that authenticates with the certificate object
    /// from the Windows store. SslStream consumes the CNG private key handle;
    /// no PFX/PEM export occurs.
    /// </summary>
    public static HttpClient Create(
        ClientIdentity identity,
        PinnedServerCertificateValidator serverValidator,
        TimeSpan connectTimeout)
    {
        ArgumentNullException.ThrowIfNull(identity);
        ArgumentNullException.ThrowIfNull(serverValidator);

        var handler = new SocketsHttpHandler
        {
            // A redirect could leak the client certificate and request body to
            // an unintended origin, so redirects are never followed silently.
            AllowAutoRedirect = false,
            AutomaticDecompression = DecompressionMethods.None,
            ConnectTimeout = connectTimeout,
            PooledConnectionLifetime = TimeSpan.FromMinutes(10),
            MaxConnectionsPerServer = 8,
            SslOptions = new SslClientAuthenticationOptions
            {
                ClientCertificates = new X509CertificateCollection { identity.Certificate },
                RemoteCertificateValidationCallback = serverValidator.Validate,
                EnabledSslProtocols = SslProtocols.Tls12 | SslProtocols.Tls13,
                CertificateRevocationCheckMode = X509RevocationMode.NoCheck,
            },
        };

        var client = new HttpClient(handler, disposeHandler: true)
        {
            // Individual requests are bounded by CancellationToken; the
            // downloader needs an unbounded timeout for large blobs.
            Timeout = Timeout.InfiniteTimeSpan,
        };
        client.DefaultRequestHeaders.UserAgent.ParseAdd("MoliSync-Windows/0.1");
        return client;
    }
}
