using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;

namespace MoliSync.Client.Security;

public sealed class CertificateSelectionCriteria
{
    /// <summary>
    /// Optional Windows certificate thumbprint used to disambiguate when several
    /// certificates from the same CA are installed.
    /// </summary>
    public string? Thumbprint { get; init; }

    public DateTimeOffset Now { get; init; } = DateTimeOffset.UtcNow;

    public bool RequireClientAuthenticationUsage { get; init; } = true;
}

public sealed class CertificateSelectionResult
{
    public X509Certificate2? Selected { get; init; }

    public int MatchCount { get; init; }

    public IReadOnlyList<string> Diagnostics { get; init; } = Array.Empty<string>();
}

/// <summary>
/// Pure certificate selection logic, kept independent from the Windows store so
/// the security rules can be tested without touching the user's certificate store.
/// </summary>
public static class CertificateSelector
{
    public const string ClientAuthenticationOid = "1.3.6.1.5.5.7.3.2";

    public static CertificateSelectionResult Select(
        IReadOnlyCollection<X509Certificate2> candidates,
        X509Certificate2 authority,
        CertificateSelectionCriteria criteria)
    {
        ArgumentNullException.ThrowIfNull(candidates);
        ArgumentNullException.ThrowIfNull(authority);
        ArgumentNullException.ThrowIfNull(criteria);

        var wantedThumbprint = NormalizeThumbprint(criteria.Thumbprint);
        var diagnostics = new List<string>();
        var matches = new List<X509Certificate2>();

        foreach (var candidate in candidates)
        {
            var label = $"{candidate.GetNameInfo(X509NameType.SimpleName, forIssuer: false)} [{candidate.Thumbprint}]";

            if (!candidate.HasPrivateKey)
            {
                diagnostics.Add($"{label}: rejected, no private key");
                continue;
            }

            if (wantedThumbprint is not null &&
                !string.Equals(NormalizeThumbprint(candidate.Thumbprint), wantedThumbprint, StringComparison.Ordinal))
            {
                diagnostics.Add($"{label}: rejected, thumbprint does not match the configured pin");
                continue;
            }

            // The issuer DER must be byte-identical to the pinned CA subject.
            // Substring or CN-only matching would allow a look-alike CA name.
            if (!IssuerMatches(candidate, authority))
            {
                diagnostics.Add($"{label}: rejected, issuer does not exactly match the pinned CA subject");
                continue;
            }

            if (candidate.NotBefore.ToUniversalTime() > criteria.Now.UtcDateTime ||
                candidate.NotAfter.ToUniversalTime() <= criteria.Now.UtcDateTime)
            {
                diagnostics.Add($"{label}: rejected, certificate is outside its validity period");
                continue;
            }

            if (IsCertificateAuthority(candidate))
            {
                diagnostics.Add($"{label}: rejected, a CA certificate cannot be used as a client identity");
                continue;
            }

            if (criteria.RequireClientAuthenticationUsage && !HasClientAuthenticationUsage(candidate))
            {
                diagnostics.Add($"{label}: rejected, missing clientAuth EKU ({ClientAuthenticationOid})");
                continue;
            }

            if (!HasUsablePrivateKey(candidate))
            {
                diagnostics.Add($"{label}: rejected, private key handle is not accessible");
                continue;
            }

            if (!TryChainToAuthority(candidate, authority, criteria.Now.UtcDateTime, out var chainError))
            {
                diagnostics.Add($"{label}: rejected, chain validation against pinned CA failed: {chainError}");
                continue;
            }

            matches.Add(candidate);
        }

        if (matches.Count == 0)
        {
            diagnostics.Add("no certificate in CurrentUser\\MY matched the pinned CA");
            return new CertificateSelectionResult
            {
                Selected = null,
                MatchCount = 0,
                Diagnostics = diagnostics,
            };
        }

        matches.Sort(static (left, right) =>
        {
            var byExpiry = right.NotAfter.CompareTo(left.NotAfter);
            return byExpiry != 0 ? byExpiry : string.CompareOrdinal(left.Thumbprint, right.Thumbprint);
        });

        diagnostics.Add(
            $"matched {matches.Count} certificate(s); selected \"{matches[0].Subject}\" [{matches[0].Thumbprint}]");

        return new CertificateSelectionResult
        {
            Selected = matches[0],
            MatchCount = matches.Count,
            Diagnostics = diagnostics,
        };
    }

    /// <summary>
    /// Selects the best client identity without requiring a pinned CA. This is
    /// the Windows-store experience: the user installs a client certificate
    /// once, and MoliSync uses the newest usable one (or the remembered
    /// thumbprint) without asking for a PFX or a CA file.
    /// </summary>
    public static CertificateSelectionResult SelectAuto(
        IReadOnlyCollection<X509Certificate2> candidates,
        CertificateSelectionCriteria criteria)
    {
        ArgumentNullException.ThrowIfNull(candidates);
        ArgumentNullException.ThrowIfNull(criteria);

        var wantedThumbprint = NormalizeThumbprint(criteria.Thumbprint);
        var diagnostics = new List<string>();
        var usable = new List<X509Certificate2>();

        foreach (var candidate in candidates)
        {
            var label = $"{candidate.GetNameInfo(X509NameType.SimpleName, forIssuer: false)} [{candidate.Thumbprint}]";
            if (!IsUsableClientIdentity(candidate, criteria.Now, requireClientAuthenticationUsage: false, out var reason))
            {
                diagnostics.Add($"{label}: rejected, {reason}");
                continue;
            }

            usable.Add(candidate);
        }

        // Prefer certificates that explicitly carry clientAuth EKU. Certs with
        // no EKU at all are technically valid for every purpose, but the
        // personal store usually also contains unrelated self-signed identities.
        var explicitClientAuth = usable
            .Where(HasExplicitClientAuthenticationUsage)
            .ToList();
        var matches = explicitClientAuth.Count > 0 ? explicitClientAuth : usable;

        if (matches.Count == 0)
        {
            diagnostics.Add("no usable client certificate in CurrentUser\\MY");
            return new CertificateSelectionResult
            {
                Selected = null,
                MatchCount = 0,
                Diagnostics = diagnostics,
            };
        }

        matches.Sort((left, right) =>
        {
            if (wantedThumbprint is not null)
            {
                var leftPinned = string.Equals(
                    NormalizeThumbprint(left.Thumbprint), wantedThumbprint, StringComparison.Ordinal);
                var rightPinned = string.Equals(
                    NormalizeThumbprint(right.Thumbprint), wantedThumbprint, StringComparison.Ordinal);
                if (leftPinned != rightPinned)
                {
                    return leftPinned ? -1 : 1;
                }
            }

            var byExpiry = right.NotAfter.CompareTo(left.NotAfter);
            return byExpiry != 0 ? byExpiry : string.CompareOrdinal(left.Thumbprint, right.Thumbprint);
        });

        var selected = matches[0];
        if (wantedThumbprint is not null &&
            !string.Equals(NormalizeThumbprint(selected.Thumbprint), wantedThumbprint, StringComparison.Ordinal))
        {
            diagnostics.Add(
                $"configured thumbprint {wantedThumbprint} is not installed; using the newest usable certificate instead");
        }

        diagnostics.Add(
            $"matched {matches.Count} certificate(s); selected \"{selected.Subject}\" [{selected.Thumbprint}]");

        return new CertificateSelectionResult
        {
            Selected = selected,
            MatchCount = matches.Count,
            Diagnostics = diagnostics,
        };
    }

    public static bool IsUsableClientIdentity(
        X509Certificate2 candidate,
        DateTimeOffset now,
        bool requireClientAuthenticationUsage,
        out string reason)
    {
        ArgumentNullException.ThrowIfNull(candidate);

        if (!candidate.HasPrivateKey)
        {
            reason = "no private key";
            return false;
        }

        if (candidate.NotBefore.ToUniversalTime() > now.UtcDateTime ||
            candidate.NotAfter.ToUniversalTime() <= now.UtcDateTime)
        {
            reason = "certificate is outside its validity period";
            return false;
        }

        if (IsCertificateAuthority(candidate))
        {
            reason = "a CA certificate cannot be used as a client identity";
            return false;
        }

        if (requireClientAuthenticationUsage && !HasClientAuthenticationUsage(candidate))
        {
            reason = $"missing clientAuth EKU ({ClientAuthenticationOid})";
            return false;
        }

        if (!HasUsablePrivateKey(candidate))
        {
            reason = "private key handle is not accessible";
            return false;
        }

        reason = string.Empty;
        return true;
    }

    public static bool IssuerMatches(X509Certificate2 candidate, X509Certificate2 authority) =>
        candidate.IssuerName.RawData.AsSpan().SequenceEqual(authority.SubjectName.RawData);

    public static bool HasClientAuthenticationUsage(X509Certificate2 certificate)
    {
        var extension = certificate.Extensions
            .OfType<X509EnhancedKeyUsageExtension>()
            .FirstOrDefault();

        // A certificate without an EKU is valid for all purposes. When an EKU
        // is present, clientAuth must be explicitly allowed.
        if (extension is null)
        {
            return true;
        }

        foreach (var oid in extension.EnhancedKeyUsages)
        {
            if (string.Equals(oid.Value, ClientAuthenticationOid, StringComparison.Ordinal))
            {
                return true;
            }
        }
        return false;
    }

    public static bool HasExplicitClientAuthenticationUsage(X509Certificate2 certificate)
    {
        var extension = certificate.Extensions
            .OfType<X509EnhancedKeyUsageExtension>()
            .FirstOrDefault();
        if (extension is null)
        {
            return false;
        }

        foreach (var oid in extension.EnhancedKeyUsages)
        {
            if (string.Equals(oid.Value, ClientAuthenticationOid, StringComparison.Ordinal))
            {
                return true;
            }
        }
        return false;
    }

    public static bool IsCertificateAuthority(X509Certificate2 certificate)
    {
        var basicConstraints = certificate.Extensions
            .OfType<X509BasicConstraintsExtension>()
            .FirstOrDefault();
        return basicConstraints?.CertificateAuthority == true;
    }

    private static bool HasUsablePrivateKey(X509Certificate2 certificate)
    {
        try
        {
            // Acquiring the key only returns a safe handle to the CNG key. It
            // does not read or export key material.
            using var rsa = certificate.GetRSAPrivateKey();
            if (rsa is not null)
            {
                return true;
            }

            using var ecdsa = certificate.GetECDsaPrivateKey();
            return ecdsa is not null;
        }
        catch (CryptographicException)
        {
            // The key may be protected by a PIN/UI prompt or unavailable to
            // this logon session.
            return false;
        }
    }

    private static bool TryChainToAuthority(
        X509Certificate2 certificate,
        X509Certificate2 authority,
        DateTime verificationTime,
        out string error)
    {
        using var chain = new X509Chain();
        chain.ChainPolicy.TrustMode = X509ChainTrustMode.CustomRootTrust;
        chain.ChainPolicy.CustomTrustStore.Add(authority);
        // Revocation is enforced by the reverse proxy against the authoritative
        // CRL; local CRL files are therefore not required for certificate selection.
        chain.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
        chain.ChainPolicy.DisableCertificateDownloads = true;
        chain.ChainPolicy.VerificationTime = verificationTime;

        if (chain.Build(certificate))
        {
            error = string.Empty;
            return true;
        }

        error = string.Join(
            "; ",
            chain.ChainStatus
                .Select(status => status.StatusInformation.Trim())
                .Where(text => text.Length > 0));
        if (error.Length == 0)
        {
            error = "unknown chain error";
        }
        return false;
    }

    private static string? NormalizeThumbprint(string? value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return null;
        }
        return value
            .Replace(":", string.Empty, StringComparison.Ordinal)
            .Replace(" ", string.Empty, StringComparison.Ordinal)
            .ToUpperInvariant();
    }
}
