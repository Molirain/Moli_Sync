using System.Runtime.Versioning;
using System.Security.Cryptography.X509Certificates;

namespace MoliSync.Client.Security;

/// <summary>
/// Enumerates the Windows CurrentUser\MY personal store.
///
/// X509Store is the managed façade over CryptoAPI's CertOpenSystemStore /
/// CertEnumCertificatesInStore on Windows. Certificates are referenced through
/// their system certificate context; calling this API never imports, copies or
/// exports the private key.
/// </summary>
[SupportedOSPlatform("windows")]
public sealed class WindowsCertificateStore
{
    private readonly Logging.IClientLog _log;

    public WindowsCertificateStore(Logging.IClientLog log)
    {
        _log = log;
    }

    public IReadOnlyList<X509Certificate2> EnumerateCurrentUserPersonal()
    {
        EnsureWindows();

        using var store = new X509Store(StoreName.My, StoreLocation.CurrentUser);
        store.Open(OpenFlags.ReadOnly | OpenFlags.OpenExistingOnly);

        var source = store.Certificates;
        var certificates = new List<X509Certificate2>(source.Count);
        foreach (X509Certificate2 certificate in source)
        {
            certificates.Add(certificate);
        }
        return certificates;
    }

    public ClientIdentity SelectIdentity(
        PinnedCertificateAuthority authority,
        CertificateSelectionCriteria criteria)
    {
        ArgumentNullException.ThrowIfNull(authority);
        ArgumentNullException.ThrowIfNull(criteria);

        var candidates = EnumerateCurrentUserPersonal();
        var selected = (X509Certificate2?)null;
        try
        {
            var result = CertificateSelector.Select(candidates, authority.Certificate, criteria);
            foreach (var diagnostic in result.Diagnostics)
            {
                _log.Debug(diagnostic);
            }

            if (result.Selected is null)
            {
                throw new ClientCertificateNotFoundException(
                    "no usable client certificate in CurrentUser\\MY matches the pinned CA. " +
                    "Import a certificate issued by the pinned CA with clientAuth EKU and an accessible private key.");
            }

            if (result.MatchCount > 1)
            {
                _log.Warn(
                    $"{result.MatchCount} client certificates match the pinned CA; " +
                    $"using the newest one [{result.Selected.Thumbprint}]. " +
                    "Set clientCertificateThumbprint to pin a specific certificate.");
            }

            selected = result.Selected;
            var identity = new ClientIdentity(selected);
            return identity;
        }
        finally
        {
            foreach (var candidate in candidates)
            {
                if (!ReferenceEquals(candidate, selected))
                {
                    candidate.Dispose();
                }
            }
        }
    }

    public ClientIdentity SelectIdentity(CertificateSelectionCriteria criteria)
    {
        ArgumentNullException.ThrowIfNull(criteria);

        var candidates = EnumerateCurrentUserPersonal();
        var selected = (X509Certificate2?)null;
        try
        {
            var result = CertificateSelector.SelectAuto(candidates, criteria);
            foreach (var diagnostic in result.Diagnostics)
            {
                _log.Debug(diagnostic);
            }

            if (result.Selected is null)
            {
                throw new ClientCertificateNotFoundException(
                    "Windows 个人证书库（CurrentUser\\MY）中没有可用的客户端证书。" +
                    "请先导入带私钥且启用 clientAuth 的客户端证书，再重新连接。");
            }

            if (result.MatchCount > 1)
            {
                _log.Warn(
                    $"{result.MatchCount} usable client certificates found; " +
                    $"using \"{result.Selected.Subject}\" [{result.Selected.Thumbprint}].");
            }

            selected = result.Selected;
            return new ClientIdentity(selected);
        }
        finally
        {
            foreach (var candidate in candidates)
            {
                if (!ReferenceEquals(candidate, selected))
                {
                    candidate.Dispose();
                }
            }
        }
    }

    private static void EnsureWindows()
    {
        if (!OperatingSystem.IsWindows())
        {
            throw new PlatformNotSupportedException("the MoliSync Windows client requires Windows");
        }
    }
}

public sealed class ClientCertificateNotFoundException : Exception
{
    public ClientCertificateNotFoundException(string message) : base(message)
    {
    }
}
