using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;

namespace MoliSync.Client.Security;

/// <summary>
/// A pinned trust anchor loaded from disk. Only the public certificate is ever
/// read from this file; private key material is not loaded.
/// </summary>
public sealed class PinnedCertificateAuthority : IDisposable
{
    private PinnedCertificateAuthority(X509Certificate2 certificate)
    {
        Certificate = certificate;
        SubjectName = certificate.SubjectName.Name;
        CommonName = certificate.GetNameInfo(X509NameType.SimpleName, forIssuer: false);
    }

    public X509Certificate2 Certificate { get; }

    public string SubjectName { get; }

    public string CommonName { get; }

    public static PinnedCertificateAuthority Load(string path, string? expectedIssuerName)
    {
        if (string.IsNullOrWhiteSpace(path))
        {
            throw new ArgumentException("CA certificate path is required", nameof(path));
        }

        var fullPath = Path.GetFullPath(path);
        if (!File.Exists(fullPath))
        {
            throw new FileNotFoundException("CA certificate not found", fullPath);
        }

        X509Certificate2 certificate;
        try
        {
            // Reads the first CERTIFICATE block; any PRIVATE KEY block in the
            // same file is ignored by this API.
            certificate = X509Certificate2.CreateFromPemFile(fullPath);
        }
        catch (CryptographicException)
        {
            certificate = new X509Certificate2(fullPath);
        }

        if (certificate.HasPrivateKey)
        {
            certificate.Dispose();
            throw new ConfigurationException("caCertificatePath must not contain a private key");
        }

        var basicConstraints = certificate.Extensions
            .OfType<X509BasicConstraintsExtension>()
            .FirstOrDefault();
        if (basicConstraints is null || !basicConstraints.CertificateAuthority)
        {
            certificate.Dispose();
            throw new ConfigurationException("caCertificatePath is not a CA certificate (basicConstraints CA=false)");
        }

        if (!string.IsNullOrWhiteSpace(expectedIssuerName) && !MatchesExpectedName(certificate, expectedIssuerName))
        {
            var actual = certificate.SubjectName.Name;
            certificate.Dispose();
            throw new ConfigurationException(
                $"expectedIssuerName \"{expectedIssuerName}\" does not match the pinned CA subject \"{actual}\"");
        }

        return new PinnedCertificateAuthority(certificate);
    }

    public void Dispose() => Certificate.Dispose();

    private static bool MatchesExpectedName(X509Certificate2 certificate, string expected)
    {
        var value = expected.Trim();

        // A CN-only value compares against the CA's common name; a full DN
        // compares against the complete subject. Neither uses substring matching.
        if (value.StartsWith("CN=", StringComparison.OrdinalIgnoreCase) &&
            !value.Contains(',', StringComparison.Ordinal))
        {
            var commonName = certificate.GetNameInfo(X509NameType.SimpleName, forIssuer: false);
            return string.Equals(commonName, value[3..].Trim(), StringComparison.OrdinalIgnoreCase);
        }

        return string.Equals(
            NormalizeDistinguishedName(certificate.SubjectName.Name),
            NormalizeDistinguishedName(value),
            StringComparison.OrdinalIgnoreCase);
    }

    private static string NormalizeDistinguishedName(string value) =>
        string.Join(
            ",",
            value.Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries));
}
