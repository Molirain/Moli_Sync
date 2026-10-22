using System.Text.Json;
using Xunit;

namespace MoliSync.Client.Tests;

public sealed class ClientOptionsTests
{
    [Fact]
    public void LoadsConfigurationAndDerivesEndpoints()
    {
        using var directory = new TempDirectory();
        var caPath = Path.Combine(directory.Path, "ca.crt");
        File.WriteAllText(caPath, "placeholder");

        var configPath = WriteConfig(directory.Path, new
        {
            serverUrl = "https://sync.example.com/base",
            caCertificatePath = caPath,
            expectedIssuerName = "CN=Molirain Root CA",
            clientCertificateThumbprint = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD",
            deviceName = "Desktop C",
            platform = "windows",
            downloadDirectory = Path.Combine(directory.Path, "downloads"),
            autoCopyText = true,
            connectTimeoutSeconds = 15,
            p2p = new { totalTimeoutMs = 1200, vpnStaggerMs = 300, candidateStaggerMs = 50 },
        });

        var options = ClientOptions.Load(configPath);

        Assert.Equal("wss://sync.example.com/base/api/v1/ws", options.WebSocketUri.ToString());
        Assert.Equal("https://sync.example.com/base/", options.HttpBaseUri.ToString());
        Assert.Equal("AABBCCDDEEFF00112233445566778899AABBCCDD", options.ClientCertificateThumbprint);
        Assert.Equal(15, options.ConnectTimeoutSeconds);
        Assert.Equal(1200, options.P2p.TotalTimeoutMs);
        Assert.True(Path.IsPathRooted(options.CaCertificatePath));
        Assert.True(Path.IsPathRooted(options.DownloadDirectory));
    }

    [Fact]
    public void AppliesDefaultsWhenOptionalFieldsAreOmitted()
    {
        using var directory = new TempDirectory();
        var configPath = WriteConfig(directory.Path, new
        {
            serverUrl = "https://sync.example.com",
            caCertificatePath = CreateDummyCa(directory.Path),
            deviceName = "PC",
            platform = "windows",
            downloadDirectory = Path.Combine(directory.Path, "downloads"),
        });

        var options = ClientOptions.Load(configPath);

        Assert.Equal(20, options.ConnectTimeoutSeconds);
        Assert.True(options.AutoCopyText);
        Assert.Equal(1200, options.P2p.TotalTimeoutMs);
        Assert.Equal(300, options.P2p.VpnStaggerMs);
        Assert.Equal(50, options.P2p.CandidateStaggerMs);
    }

    [Fact]
    public void RejectsPlainHttpServer()
    {
        using var directory = new TempDirectory();
        var configPath = WriteConfig(directory.Path, ValidConfig(directory.Path, serverUrl: "http://sync.example.com"));

        var exception = Assert.Throws<ConfigurationException>(() => ClientOptions.Load(configPath));
        Assert.Contains("https", exception.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void RejectsMissingCaCertificate()
    {
        using var directory = new TempDirectory();
        var configPath = WriteConfig(
            directory.Path,
            ValidConfig(directory.Path, caCertificatePath: Path.Combine(directory.Path, "missing.crt")));

        Assert.Throws<ConfigurationException>(() => ClientOptions.Load(configPath));
    }

    [Fact]
    public void RejectsInvalidP2PTimeout()
    {
        using var directory = new TempDirectory();
        var configPath = WriteConfig(directory.Path, new
        {
            serverUrl = "https://sync.example.com",
            caCertificatePath = CreateDummyCa(directory.Path),
            deviceName = "PC",
            platform = "windows",
            downloadDirectory = Path.Combine(directory.Path, "downloads"),
            p2p = new { totalTimeoutMs = 50, vpnStaggerMs = 300, candidateStaggerMs = 50 },
        });

        Assert.Throws<ConfigurationException>(() => ClientOptions.Load(configPath));
    }

    [Fact]
    public void RejectsInvalidPlatform()
    {
        using var directory = new TempDirectory();
        var configPath = WriteConfig(
            directory.Path,
            ValidConfig(directory.Path, platform: "Windows/../../"));

        Assert.Throws<ConfigurationException>(() => ClientOptions.Load(configPath));
    }

    [Fact]
    public void RejectsMissingConfigurationFile()
    {
        var path = Path.Combine(Path.GetTempPath(), $"molisync-missing-{Guid.NewGuid():N}.json");
        Assert.Throws<ConfigurationException>(() => ClientOptions.Load(path));
    }

    private static object ValidConfig(
        string directory,
        string serverUrl = "https://sync.example.com",
        string? caCertificatePath = null,
        string platform = "windows") => new
    {
        serverUrl,
        caCertificatePath = caCertificatePath ?? CreateDummyCa(directory),
        deviceName = "PC",
        platform,
        downloadDirectory = Path.Combine(directory, "downloads"),
        p2p = new { totalTimeoutMs = 1200, vpnStaggerMs = 300, candidateStaggerMs = 50 },
    };

    private static string CreateDummyCa(string directory)
    {
        var path = Path.Combine(directory, "ca.crt");
        if (!File.Exists(path))
        {
            File.WriteAllText(path, "placeholder");
        }
        return path;
    }

    private static string WriteConfig(string directory, object config)
    {
        var path = Path.Combine(directory, $"client-{Guid.NewGuid():N}.json");
        File.WriteAllText(
            path,
            JsonSerializer.Serialize(config, new JsonSerializerOptions { WriteIndented = true }));
        return path;
    }

    private sealed class TempDirectory : IDisposable
    {
        public TempDirectory()
        {
            Path = System.IO.Path.Combine(System.IO.Path.GetTempPath(), $"molisync-options-{Guid.NewGuid():N}");
            Directory.CreateDirectory(Path);
        }

        public string Path { get; }

        public void Dispose()
        {
            try
            {
                Directory.Delete(Path, recursive: true);
            }
            catch (IOException)
            {
                // Best-effort cleanup.
            }
        }
    }
}
