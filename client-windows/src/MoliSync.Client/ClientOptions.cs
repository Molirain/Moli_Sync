using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.RegularExpressions;

namespace MoliSync.Client;

public sealed class ConfigurationException : Exception
{
    public ConfigurationException(string message) : base(message)
    {
    }

    public ConfigurationException(string message, Exception inner) : base(message, inner)
    {
    }
}

public sealed partial class ClientOptions
{
    [GeneratedRegex("^[a-z][a-z0-9_-]{0,15}$", RegexOptions.CultureInvariant)]
    private static partial Regex PlatformPattern();

    public string ServerUrl { get; set; } = string.Empty;

    public string CaCertificatePath { get; set; } = string.Empty;

    public string? ExpectedIssuerName { get; set; }

    public string? ClientCertificateThumbprint { get; set; }

    public string DeviceName { get; set; } = Environment.MachineName;

    public string Platform { get; set; } = "windows";

    public string DownloadDirectory { get; set; } = string.Empty;

    public bool AutoCopyText { get; set; } = true;

    public int ConnectTimeoutSeconds { get; set; } = 20;

    public P2POptions P2p { get; set; } = new();

    [JsonIgnore]
    public Uri HttpBaseUri => new(ServerUrl.TrimEnd('/') + "/", UriKind.Absolute);

    [JsonIgnore]
    public Uri WebSocketUri
    {
        get
        {
            var builder = new UriBuilder(HttpBaseUri)
            {
                Scheme = Uri.UriSchemeWss,
            };
            builder.Path = builder.Path.TrimEnd('/') + "/api/v1/ws";
            return builder.Uri;
        }
    }

    public static ClientOptions Load(string path)
    {
        if (string.IsNullOrWhiteSpace(path))
        {
            throw new ConfigurationException("a configuration file path is required");
        }

        var fullPath = Path.GetFullPath(path);
        if (!File.Exists(fullPath))
        {
            throw new ConfigurationException($"configuration file not found: {fullPath}");
        }

        ClientOptions? options;
        try
        {
            using var stream = new FileStream(fullPath, FileMode.Open, FileAccess.Read, FileShare.Read);
            options = JsonSerializer.Deserialize(stream, ClientConfigJsonContext.Default.ClientOptions);
        }
        catch (JsonException exception)
        {
            throw new ConfigurationException($"invalid JSON in {fullPath}: {exception.Message}", exception);
        }
        catch (IOException exception)
        {
            throw new ConfigurationException($"cannot read {fullPath}: {exception.Message}", exception);
        }

        if (options is null)
        {
            throw new ConfigurationException($"configuration file {fullPath} is empty");
        }

        options.Validate();
        return options;
    }

    public void Validate()
    {
        if (!Uri.TryCreate(ServerUrl, UriKind.Absolute, out var serverUri))
        {
            throw new ConfigurationException("serverUrl must be an absolute URL");
        }
        if (!string.Equals(serverUri.Scheme, Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase))
        {
            throw new ConfigurationException("serverUrl must use https (mTLS is mandatory)");
        }
        if (!string.IsNullOrEmpty(serverUri.Query) || !string.IsNullOrEmpty(serverUri.Fragment) ||
            !string.IsNullOrEmpty(serverUri.UserInfo))
        {
            throw new ConfigurationException("serverUrl must not contain credentials, a query or a fragment");
        }

        if (string.IsNullOrWhiteSpace(CaCertificatePath))
        {
            throw new ConfigurationException("caCertificatePath is required");
        }
        CaCertificatePath = Path.GetFullPath(CaCertificatePath);
        if (!File.Exists(CaCertificatePath))
        {
            throw new ConfigurationException($"CA certificate not found: {CaCertificatePath}");
        }

        if (string.IsNullOrWhiteSpace(DeviceName) || DeviceName.Length > 64 || DeviceName.Any(char.IsControl))
        {
            throw new ConfigurationException("deviceName must be 1-64 printable characters");
        }

        if (!PlatformPattern().IsMatch(Platform ?? string.Empty))
        {
            throw new ConfigurationException("platform must match ^[a-z][a-z0-9_-]{0,15}$");
        }

        if (string.IsNullOrWhiteSpace(DownloadDirectory))
        {
            throw new ConfigurationException("downloadDirectory is required");
        }
        DownloadDirectory = Path.GetFullPath(DownloadDirectory);

        if (ConnectTimeoutSeconds < 1 || ConnectTimeoutSeconds > 120)
        {
            throw new ConfigurationException("connectTimeoutSeconds must be between 1 and 120");
        }

        if (ClientCertificateThumbprint is not null)
        {
            var normalized = ClientCertificateThumbprint.Replace(":", string.Empty).Replace(" ", string.Empty);
            if (normalized.Length < 16 || normalized.Length > 128 || normalized.Any(c => !Uri.IsHexDigit(c)))
            {
                throw new ConfigurationException("clientCertificateThumbprint must be a hexadecimal thumbprint");
            }
            ClientCertificateThumbprint = normalized;
        }

        if (ExpectedIssuerName is { } issuer &&
            (issuer.Length > 512 || issuer.Any(char.IsControl)))
        {
            throw new ConfigurationException("expectedIssuerName must be at most 512 printable characters");
        }

        P2p ??= new P2POptions();
        P2p.Validate();
    }
}

public sealed class P2POptions
{
    public int TotalTimeoutMs { get; set; } = 1200;

    public int VpnStaggerMs { get; set; } = 300;

    public int CandidateStaggerMs { get; set; } = 50;

    public void Validate()
    {
        if (TotalTimeoutMs < 100 || TotalTimeoutMs > 10_000)
        {
            throw new ConfigurationException("p2p.totalTimeoutMs must be between 100 and 10000");
        }
        if (VpnStaggerMs < 0 || VpnStaggerMs >= TotalTimeoutMs)
        {
            throw new ConfigurationException("p2p.vpnStaggerMs must be non-negative and smaller than p2p.totalTimeoutMs");
        }
        if (CandidateStaggerMs < 0 || CandidateStaggerMs > 1000)
        {
            throw new ConfigurationException("p2p.candidateStaggerMs must be between 0 and 1000");
        }
    }
}

[JsonSourceGenerationOptions(
    PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase,
    ReadCommentHandling = JsonCommentHandling.Skip,
    AllowTrailingCommas = true,
    WriteIndented = true)]
[JsonSerializable(typeof(ClientOptions))]
internal sealed partial class ClientConfigJsonContext : JsonSerializerContext
{
}
