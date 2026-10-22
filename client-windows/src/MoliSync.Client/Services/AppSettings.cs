using System.Text.Json;
using System.Text.Json.Serialization;
using Microsoft.Win32;
using MoliSync.Client.Logging;

namespace MoliSync.Client.Services;

public sealed class AppSettings
{
    public string ServerUrl { get; set; } = string.Empty;

    public string CaCertificatePath { get; set; } = string.Empty;

    public string? ExpectedIssuerName { get; set; }

    public string? ClientCertificateThumbprint { get; set; }

    public string DeviceName { get; set; } = Environment.MachineName;

    public string DataDirectory { get; set; } = AppPaths.DefaultDataDirectory;

    public bool AutoCopyText { get; set; } = true;

    public bool AutoStart { get; set; }

    public bool StartMinimized { get; set; }

    public bool CloseToTray { get; set; } = true;

    public long CloudThresholdBytes { get; set; } = 15L * 1024 * 1024;

    public int ConnectTimeoutSeconds { get; set; } = 20;

    [JsonIgnore]
    public bool IsConfigured =>
        !string.IsNullOrWhiteSpace(ServerUrl) &&
        !string.IsNullOrWhiteSpace(DataDirectory);

    public static AppSettings CreateDefault() => new();

    public void Normalize()
    {
        ServerUrl = (ServerUrl ?? string.Empty).Trim().TrimEnd('/');
        CaCertificatePath = string.IsNullOrWhiteSpace(CaCertificatePath)
            ? string.Empty
            : Path.GetFullPath(CaCertificatePath.Trim());
        DataDirectory = string.IsNullOrWhiteSpace(DataDirectory)
            ? AppPaths.DefaultDataDirectory
            : Path.GetFullPath(DataDirectory.Trim());
        DeviceName = (DeviceName ?? string.Empty).Trim();
        if (DeviceName.Length == 0)
        {
            DeviceName = Environment.MachineName;
        }
        CloudThresholdBytes = Math.Clamp(CloudThresholdBytes, 5L * 1024 * 1024, 50L * 1024 * 1024);
        ConnectTimeoutSeconds = Math.Clamp(ConnectTimeoutSeconds, 5, 120);
        ExpectedIssuerName = string.IsNullOrWhiteSpace(ExpectedIssuerName) ? null : ExpectedIssuerName.Trim();
        ClientCertificateThumbprint = string.IsNullOrWhiteSpace(ClientCertificateThumbprint)
            ? null
            : ClientCertificateThumbprint.Replace(":", string.Empty).Replace(" ", string.Empty).ToUpperInvariant();
    }

    public void Validate()
    {
        if (!Uri.TryCreate(ServerUrl, UriKind.Absolute, out var serverUri))
        {
            throw new ConfigurationException("服务器地址必须是完整的 https:// URL");
        }
        if (!string.Equals(serverUri.Scheme, Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase))
        {
            throw new ConfigurationException("服务器地址必须使用 https（mTLS 是强制要求）");
        }
        if (!string.IsNullOrEmpty(serverUri.Query) ||
            !string.IsNullOrEmpty(serverUri.Fragment) ||
            !string.IsNullOrEmpty(serverUri.UserInfo))
        {
            throw new ConfigurationException("服务器地址不能包含用户名、查询串或片段");
        }
        if (!string.IsNullOrWhiteSpace(CaCertificatePath) && !File.Exists(CaCertificatePath))
        {
            throw new ConfigurationException("已配置的 CA 根证书文件不存在；请删除 settings.json 中的 caCertificatePath 或重新选择文件");
        }
        if (string.IsNullOrWhiteSpace(DataDirectory))
        {
            throw new ConfigurationException("数据目录不能为空");
        }
        if (DeviceName.Length is < 1 or > 64 || DeviceName.Any(char.IsControl))
        {
            throw new ConfigurationException("设备名称必须是 1–64 个可打印字符");
        }
    }

    public ClientOptions ToClientOptions() => new()
    {
        ServerUrl = ServerUrl,
        CaCertificatePath = CaCertificatePath,
        ExpectedIssuerName = ExpectedIssuerName,
        ClientCertificateThumbprint = ClientCertificateThumbprint,
        DeviceName = DeviceName,
        Platform = "windows",
        DownloadDirectory = DataDirectory,
        AutoCopyText = AutoCopyText,
        ConnectTimeoutSeconds = ConnectTimeoutSeconds,
    };

    public static AppSettings FromLegacy(ClientOptions options) => new()
    {
        ServerUrl = options.ServerUrl,
        CaCertificatePath = options.CaCertificatePath,
        ExpectedIssuerName = options.ExpectedIssuerName,
        ClientCertificateThumbprint = options.ClientCertificateThumbprint,
        DeviceName = options.DeviceName,
        DataDirectory = string.IsNullOrWhiteSpace(options.DownloadDirectory)
            ? AppPaths.DefaultDataDirectory
            : options.DownloadDirectory,
        AutoCopyText = options.AutoCopyText,
        ConnectTimeoutSeconds = options.ConnectTimeoutSeconds,
    };
}

public sealed class SettingsStore
{
    private const string RegistryKeyPath = @"Software\MoliSync";
    private readonly IClientLog _log;

    public SettingsStore(IClientLog log)
    {
        _log = log ?? throw new ArgumentNullException(nameof(log));
    }

    public string DataDirectoryFromInstaller()
    {
        try
        {
            using var key = Registry.CurrentUser.OpenSubKey(RegistryKeyPath);
            return key?.GetValue("DataDirectory") as string ?? string.Empty;
        }
        catch (Exception exception) when (exception is UnauthorizedAccessException or IOException)
        {
            _log.Warn("cannot read the installer data directory", exception);
            return string.Empty;
        }
    }

    public AppSettings Load()
    {
        if (File.Exists(AppPaths.SettingsFile))
        {
            try
            {
                using var stream = new FileStream(
                    AppPaths.SettingsFile, FileMode.Open, FileAccess.Read, FileShare.Read);
                var stored = JsonSerializer.Deserialize(stream, AppSettingsJsonContext.Default.AppSettings)
                    ?? throw new ConfigurationException("settings.json 为空");
                stored.Normalize();
                return stored;
            }
            catch (JsonException exception)
            {
                throw new ConfigurationException($"settings.json 格式无效：{exception.Message}", exception);
            }
        }

        if (File.Exists(AppPaths.LegacyConfigFile))
        {
            try
            {
                var legacy = ClientOptions.Load(AppPaths.LegacyConfigFile);
                var imported = AppSettings.FromLegacy(legacy);
                imported.Normalize();
                _log.Info("imported the legacy client.json configuration");
                return imported;
            }
            catch (ConfigurationException exception)
            {
                _log.Warn($"legacy client.json could not be imported: {exception.Message}");
            }
        }

        var settings = AppSettings.CreateDefault();
        var installerDataDirectory = DataDirectoryFromInstaller();
        if (!string.IsNullOrWhiteSpace(installerDataDirectory))
        {
            settings.DataDirectory = Path.GetFullPath(installerDataDirectory);
        }
        settings.Normalize();
        return settings;
    }

    public void Save(AppSettings settings)
    {
        ArgumentNullException.ThrowIfNull(settings);
        settings.Normalize();

        Directory.CreateDirectory(AppPaths.SettingsDirectory);
        var temporary = AppPaths.SettingsFile + ".tmp";
        using (var stream = new FileStream(temporary, FileMode.Create, FileAccess.Write, FileShare.None))
        {
            JsonSerializer.Serialize(stream, settings, AppSettingsJsonContext.Default.AppSettings);
            stream.Flush(flushToDisk: true);
        }
        File.Move(temporary, AppPaths.SettingsFile, overwrite: true);

        WriteInstallerDataDirectory(settings.DataDirectory);
    }

    private void WriteInstallerDataDirectory(string path)
    {
        if (AppPaths.IsPackaged)
        {
            return;
        }

        try
        {
            using var key = Registry.CurrentUser.CreateSubKey(RegistryKeyPath);
            key?.SetValue("DataDirectory", path, RegistryValueKind.String);
        }
        catch (Exception exception) when (exception is UnauthorizedAccessException or IOException)
        {
            _log.Warn("cannot persist the data directory for the uninstaller", exception);
        }
    }
}

[JsonSourceGenerationOptions(
    PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase,
    PropertyNameCaseInsensitive = true,
    WriteIndented = true)]
[JsonSerializable(typeof(AppSettings))]
internal sealed partial class AppSettingsJsonContext : JsonSerializerContext
{
}
