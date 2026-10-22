using System.Security.Cryptography.X509Certificates;
using MoliSync.Client.Security;

namespace MoliSync.Client.Services;

public sealed record CertificateChoice(
    string Subject,
    string Issuer,
    string Thumbprint,
    DateTimeOffset NotAfter)
{
    public string DisplayName => $"{Subject} · 到期 {NotAfter:yyyy-MM-dd}";
}

public static class CertificateCatalog
{
    /// <summary>
    /// Lists certificates Windows can use as an mTLS client identity. The
    /// private key stays in the CNG provider; only public metadata is read.
    /// </summary>
    public static IReadOnlyList<CertificateChoice> ListUsable()
    {
        using var store = new X509Store(StoreName.My, StoreLocation.CurrentUser);
        store.Open(OpenFlags.ReadOnly | OpenFlags.OpenExistingOnly);

        var usable = new List<CertificateChoice>();
        var explicitClientAuth = new List<CertificateChoice>();
        foreach (X509Certificate2 certificate in store.Certificates)
        {
            try
            {
                if (!CertificateSelector.IsUsableClientIdentity(
                        certificate,
                        DateTimeOffset.UtcNow,
                        requireClientAuthenticationUsage: false,
                        out _))
                {
                    continue;
                }

                var choice = new CertificateChoice(
                    certificate.GetNameInfo(X509NameType.SimpleName, forIssuer: false),
                    certificate.GetNameInfo(X509NameType.SimpleName, forIssuer: true),
                    certificate.Thumbprint,
                    certificate.NotAfter.ToUniversalTime());
                usable.Add(choice);
                if (CertificateSelector.HasExplicitClientAuthenticationUsage(certificate))
                {
                    explicitClientAuth.Add(choice);
                }
            }
            finally
            {
                certificate.Dispose();
            }
        }

        var result = explicitClientAuth.Count > 0 ? explicitClientAuth : usable;
        return result
            .OrderByDescending(choice => choice.NotAfter)
            .ThenBy(choice => choice.Subject, StringComparer.CurrentCulture)
            .ToArray();
    }

    public static CertificateChoice? Preferred(
        IReadOnlyList<CertificateChoice> choices,
        string? rememberedThumbprint)
    {
        if (choices.Count == 0)
        {
            return null;
        }

        var normalized = NormalizeThumbprint(rememberedThumbprint);
        if (normalized is not null)
        {
            var remembered = choices.FirstOrDefault(choice =>
                string.Equals(NormalizeThumbprint(choice.Thumbprint), normalized, StringComparison.Ordinal));
            if (remembered is not null)
            {
                return remembered;
            }
        }

        return choices[0];
    }

    private static string? NormalizeThumbprint(string? value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return null;
        }

        return value.Replace(":", string.Empty).Replace(" ", string.Empty).ToUpperInvariant();
    }
}
