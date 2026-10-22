using System.Net.Security;
using System.Security.Cryptography.X509Certificates;

namespace MoliSync.Client.Security;

/// <summary>
/// Validates the server certificate. When a CA was explicitly pinned, the chain
/// is rebuilt against only that root. Otherwise the normal Windows trust store
/// is used (the same roots the browser and Bitwarden-style clients trust), so
/// no CA file is required for a publicly trusted or centrally managed server.
/// Hostname verification performed by SslStream is always honoured.
/// </summary>
public sealed class PinnedServerCertificateValidator
{
    private readonly PinnedCertificateAuthority? _authority;
    private readonly Logging.IClientLog _log;

    public PinnedServerCertificateValidator(PinnedCertificateAuthority? authority, Logging.IClientLog log)
    {
        _authority = authority;
        _log = log;
    }

    public bool Validate(
        object? sender,
        X509Certificate? certificate,
        X509Chain? chain,
        SslPolicyErrors sslPolicyErrors)
    {
        _ = sender;
        _ = chain;

        if (certificate is null)
        {
            _log.Warn("server presented no certificate");
            return false;
        }

        if ((sslPolicyErrors & SslPolicyErrors.RemoteCertificateNameMismatch) != 0)
        {
            _log.Warn("server certificate does not match the configured host name");
            return false;
        }

        if (_authority is null)
        {
            if (sslPolicyErrors == SslPolicyErrors.None)
            {
                return true;
            }

            _log.Warn($"server certificate rejected by the Windows trust store: {sslPolicyErrors}");
            return false;
        }

        var leaf = certificate as X509Certificate2 ?? new X509Certificate2(certificate);
        var ownsLeaf = !ReferenceEquals(leaf, certificate);
        try
        {
            using var customChain = new X509Chain();
            customChain.ChainPolicy.TrustMode = X509ChainTrustMode.CustomRootTrust;
            customChain.ChainPolicy.CustomTrustStore.Add(_authority.Certificate);
            customChain.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
            customChain.ChainPolicy.DisableCertificateDownloads = true;
            customChain.ChainPolicy.VerificationTime = DateTime.Now;

            if (customChain.Build(leaf))
            {
                return true;
            }

            var statuses = customChain.ChainStatus
                .Select(status => status.StatusInformation.Trim())
                .Where(text => text.Length > 0);
            _log.Warn($"server certificate rejected: {string.Join("; ", statuses)}");
            return false;
        }
        finally
        {
            if (ownsLeaf)
            {
                leaf.Dispose();
            }
        }
    }
}
