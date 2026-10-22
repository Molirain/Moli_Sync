using System.Net.Security;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using MoliSync.Client.Logging;
using MoliSync.Client.Security;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class CertificateSelectorTests
{
    private static readonly DateTimeOffset Now = DateTimeOffset.UtcNow;

    [Fact]
    public void SelectsClientCertificateIssuedByPinnedAuthority()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var client = CreateClient(authority, "Windows-PC");

        var result = CertificateSelector.Select(
            new[] { client },
            authority,
            new CertificateSelectionCriteria { Now = Now });

        Assert.Same(client, result.Selected);
        Assert.Equal(1, result.MatchCount);
    }

    [Fact]
    public void RejectsCertificateWithoutPrivateKey()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var client = CreateClient(authority, "Windows-PC", withPrivateKey: false);

        var result = Select(client, authority);

        Assert.Null(result.Selected);
        Assert.Contains(result.Diagnostics, line => line.Contains("no private key", StringComparison.Ordinal));
    }

    [Fact]
    public void RejectsCertificateWithoutClientAuthenticationEku()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var client = CreateClient(authority, "Windows-PC", clientAuthentication: false);

        var result = Select(client, authority);

        Assert.Null(result.Selected);
        Assert.Contains(result.Diagnostics, line => line.Contains("clientAuth", StringComparison.Ordinal));
    }

    [Fact]
    public void RejectsExpiredCertificate()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var client = CreateClient(authority, "Windows-PC", expired: true);

        var result = Select(client, authority);

        Assert.Null(result.Selected);
        Assert.Contains(result.Diagnostics, line => line.Contains("validity", StringComparison.Ordinal));
    }

    [Fact]
    public void RejectsCertificateFromDifferentAuthority()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var otherAuthority = CreateAuthority("Someone Else Root CA");
        using var client = CreateClient(otherAuthority, "Windows-PC");

        var result = Select(client, authority);

        Assert.Null(result.Selected);
        Assert.Contains(result.Diagnostics, line => line.Contains("issuer", StringComparison.Ordinal));
    }

    [Fact]
    public void RejectsLookalikeAuthorityWithSameSubject()
    {
        // Same DN, different key: a name-only match would accept this
        // certificate, but chain validation against the pinned key must fail.
        using var authority = CreateAuthority("Molirain Root CA");
        using var lookalike = CreateAuthority("Molirain Root CA");
        using var client = CreateClient(lookalike, "Windows-PC");

        var result = Select(client, authority);

        Assert.Null(result.Selected);
        Assert.Contains(result.Diagnostics, line => line.Contains("chain", StringComparison.Ordinal));
    }

    [Fact]
    public void RejectsAuthorityCertificateAsClientIdentity()
    {
        using var authority = CreateAuthority("Molirain Root CA");

        var result = Select(authority, authority);

        Assert.Null(result.Selected);
        Assert.Contains(result.Diagnostics, line => line.Contains("CA certificate", StringComparison.Ordinal));
    }

    [Fact]
    public void ThumbprintPinSelectsRequestedCertificate()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var first = CreateClient(authority, "PC-1", notAfter: Now.AddYears(2));
        using var second = CreateClient(authority, "PC-2", notAfter: Now.AddYears(1));

        var result = CertificateSelector.Select(
            new[] { first, second },
            authority,
            new CertificateSelectionCriteria { Now = Now, Thumbprint = second.Thumbprint });

        Assert.Same(second, result.Selected);
        Assert.Equal(1, result.MatchCount);
    }

    [Fact]
    public void ChoosesNewestCertificateWhenNoPinIsConfigured()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var newer = CreateClient(authority, "PC-new", notAfter: Now.AddYears(3));
        using var older = CreateClient(authority, "PC-old", notAfter: Now.AddYears(1));

        var result = CertificateSelector.Select(
            new[] { older, newer },
            authority,
            new CertificateSelectionCriteria { Now = Now });

        Assert.Same(newer, result.Selected);
        Assert.Equal(2, result.MatchCount);
    }

    [Fact]
    public void ClientIdentityUsesSha256FingerprintAsDeviceId()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var certificate = CreateClient(authority, "Windows-PC");
        using var identity = new ClientIdentity(certificate);

        Assert.Equal(
            certificate.GetCertHashString(HashAlgorithmName.SHA256).ToLowerInvariant(),
            identity.DeviceId);
        Assert.Equal("Windows-PC", identity.CommonName);
    }

    [Fact]
    public void PinnedAuthorityAcceptsMatchingConfiguredName()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        var path = WriteCertificate(authority);

        try
        {
            using var loaded = PinnedCertificateAuthority.Load(path, "CN=Molirain Root CA");
            Assert.Equal("Molirain Root CA", loaded.CommonName);
        }
        finally
        {
            File.Delete(path);
        }
    }

    [Fact]
    public void PinnedAuthorityRejectsMismatchedConfiguredName()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        var path = WriteCertificate(authority);

        try
        {
            Assert.Throws<ConfigurationException>(
                () => PinnedCertificateAuthority.Load(path, "CN=Some Other CA"));
        }
        finally
        {
            File.Delete(path);
        }
    }

    [Fact]
    public void PinnedAuthorityRejectsLeafCertificate()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var leaf = CreateClient(authority, "Windows-PC");
        var path = WriteCertificate(leaf);

        try
        {
            Assert.Throws<ConfigurationException>(() => PinnedCertificateAuthority.Load(path, null));
        }
        finally
        {
            File.Delete(path);
        }
    }

    [Fact]
    public void ServerValidatorRejectsUntrustedServerCertificate()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var rogue = CreateAuthority("Rogue Root CA");
        using var serverCertificate = CreateServerCertificate(rogue);
        using var pinned = Wrap(authority);

        var validator = new PinnedServerCertificateValidator(pinned, NullClientLog.Instance);

        Assert.False(
            validator.Validate(
                sender: null,
                serverCertificate,
                chain: null,
                SslPolicyErrors.None));
    }

    [Fact]
    public void ServerValidatorAcceptsCertificateFromPinnedAuthority()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var serverCertificate = CreateServerCertificate(authority);
        using var pinned = Wrap(authority);

        var validator = new PinnedServerCertificateValidator(pinned, NullClientLog.Instance);

        Assert.True(
            validator.Validate(
                sender: null,
                serverCertificate,
                chain: null,
                SslPolicyErrors.RemoteCertificateChainErrors));
    }

    [Fact]
    public void ServerValidatorHonoursHostnameMismatch()
    {
        using var authority = CreateAuthority("Molirain Root CA");
        using var serverCertificate = CreateServerCertificate(authority);
        using var pinned = Wrap(authority);
        var validator = new PinnedServerCertificateValidator(pinned, NullClientLog.Instance);

        Assert.False(
            validator.Validate(
                sender: null,
                serverCertificate,
                chain: null,
                SslPolicyErrors.RemoteCertificateNameMismatch));
    }

    private static CertificateSelectionResult Select(X509Certificate2 certificate, X509Certificate2 authority) =>
        CertificateSelector.Select(
            new[] { certificate },
            authority,
            new CertificateSelectionCriteria { Now = Now });

    private static PinnedCertificateAuthority Wrap(X509Certificate2 authority)
    {
        // PinnedCertificateAuthority only has a file based factory; tests use a
        // short-lived file with the public certificate.
        var path = WriteCertificate(authority);
        try
        {
            return PinnedCertificateAuthority.Load(path, null);
        }
        finally
        {
            File.Delete(path);
        }
    }

    private static string WriteCertificate(X509Certificate2 certificate)
    {
        var path = Path.Combine(Path.GetTempPath(), $"molisync-test-{Guid.NewGuid():N}.cer");
        File.WriteAllBytes(path, certificate.Export(X509ContentType.Cert));
        return path;
    }

    private static readonly List<ECDsa> RetainedKeys = new();

    private static TestAuthority CreateAuthority(
        string commonName,
        DateTimeOffset? notAfter = null,
        bool isCertificateAuthority = true) =>
        new(commonName, notAfter, isCertificateAuthority);

    private static X509Certificate2 CreateClient(
        X509Certificate2 authority,
        string commonName,
        bool clientAuthentication = true,
        bool expired = false,
        bool withPrivateKey = true,
        DateTimeOffset? notAfter = null)
    {
        var key = ECDsa.Create(ECCurve.NamedCurves.nistP256);
        RetainedKeys.Add(key);
        var request = new CertificateRequest($"CN={commonName}", key, HashAlgorithmName.SHA256);
        request.CertificateExtensions.Add(new X509BasicConstraintsExtension(false, false, 0, true));
        request.CertificateExtensions.Add(
            new X509KeyUsageExtension(X509KeyUsageFlags.DigitalSignature, false));

        var usage = clientAuthentication
            ? CertificateSelector.ClientAuthenticationOid
            : "1.3.6.1.5.5.7.3.1"; // serverAuth intentionally makes it a non-client cert
        request.CertificateExtensions.Add(
            new X509EnhancedKeyUsageExtension(new OidCollection { new Oid(usage) }, false));
        request.CertificateExtensions.Add(new X509SubjectKeyIdentifierExtension(request.PublicKey, false));

        var notBefore = expired ? Now.AddHours(-12) : Now.AddMinutes(-5);
        var expiry = expired ? Now.AddHours(-1) : notAfter ?? Now.AddMonths(6);
        var issued = request.Create(authority, notBefore, expiry, CreateSerialNumber());
        if (!withPrivateKey)
        {
            return issued;
        }

        var withKey = issued.CopyWithPrivateKey(key);
        issued.Dispose();
        return withKey;
    }

    private static X509Certificate2 CreateServerCertificate(X509Certificate2 authority)
    {
        var key = ECDsa.Create(ECCurve.NamedCurves.nistP256);
        RetainedKeys.Add(key);
        var request = new CertificateRequest("CN=sync.molirain.com", key, HashAlgorithmName.SHA256);
        request.CertificateExtensions.Add(new X509BasicConstraintsExtension(false, false, 0, true));
        request.CertificateExtensions.Add(
            new X509KeyUsageExtension(X509KeyUsageFlags.DigitalSignature, false));
        request.CertificateExtensions.Add(
            new X509EnhancedKeyUsageExtension(
                new OidCollection { new Oid("1.3.6.1.5.5.7.3.1") },
                false));
        request.CertificateExtensions.Add(new X509SubjectKeyIdentifierExtension(request.PublicKey, false));

        var issued = request.Create(authority, Now.AddDays(-1), Now.AddMonths(6), CreateSerialNumber());
        var withKey = issued.CopyWithPrivateKey(key);
        issued.Dispose();
        return withKey;
    }

    private static byte[] CreateSerialNumber()
    {
        var serial = new byte[16];
        RandomNumberGenerator.Fill(serial);
        serial[0] &= 0x7F;
        return serial;
    }

    private sealed class TestAuthority : IDisposable
    {
        public TestAuthority(string commonName, DateTimeOffset? notAfter, bool isCertificateAuthority)
        {
            Key = ECDsa.Create(ECCurve.NamedCurves.nistP256);
            var request = new CertificateRequest($"CN={commonName}", Key, HashAlgorithmName.SHA256);
            request.CertificateExtensions.Add(
                new X509BasicConstraintsExtension(isCertificateAuthority, false, 0, true));
            request.CertificateExtensions.Add(
                new X509KeyUsageExtension(
                    X509KeyUsageFlags.KeyCertSign | X509KeyUsageFlags.CrlSign | X509KeyUsageFlags.DigitalSignature,
                    true));
            request.CertificateExtensions.Add(new X509SubjectKeyIdentifierExtension(request.PublicKey, false));
            Certificate = request.CreateSelfSigned(Now.AddDays(-2), notAfter ?? Now.AddYears(5));
        }

        public ECDsa Key { get; }

        public X509Certificate2 Certificate { get; }

        public static implicit operator X509Certificate2(TestAuthority authority) => authority.Certificate;

        public void Dispose()
        {
            Certificate.Dispose();
            Key.Dispose();
        }
    }
}
