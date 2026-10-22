using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;

namespace MoliSync.Client.Security;

/// <summary>
/// The authenticated device identity. The certificate is kept as a live Windows
/// certificate context whose private key is referenced through CNG; it is never
/// exported to a PFX/PEM file.
/// </summary>
public sealed class ClientIdentity : IDisposable
{
    public ClientIdentity(X509Certificate2 certificate)
    {
        ArgumentNullException.ThrowIfNull(certificate);
        if (!certificate.HasPrivateKey)
        {
            throw new ArgumentException("client certificate has no private key handle", nameof(certificate));
        }

        Certificate = certificate;
        DeviceId = certificate.GetCertHashString(HashAlgorithmName.SHA256).ToLowerInvariant();
        CommonName = certificate.GetNameInfo(X509NameType.SimpleName, forIssuer: false);
    }

    public X509Certificate2 Certificate { get; }

    /// <summary>Lowercase SHA-256 fingerprint, identical to the server's DeviceID.</summary>
    public string DeviceId { get; }

    public string CommonName { get; }

    public void Dispose() => Certificate.Dispose();
}
