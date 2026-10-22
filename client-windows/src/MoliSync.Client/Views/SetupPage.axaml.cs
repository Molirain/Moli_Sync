using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Platform.Storage;
using MoliSync.Client.Logging;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public sealed class SetupPageModel
{
    public string? Error { get; set; }
}

public partial class SetupPage : UserControl
{
    private readonly IClientLog _log;
    private readonly Func<AppSettings, Task<string?>> _connectAsync;
    private AppSettings _settings = AppSettings.CreateDefault();
    private string? _detectedThumbprint;
    private bool _busy;
    private bool _loadingCertificates;

    public SetupPage(IClientLog log, Func<AppSettings, Task<string?>> connectAsync)
    {
        _log = log;
        _connectAsync = connectAsync;
        InitializeComponent();
        DataContext = new SetupPageModel();
    }

    public event EventHandler? Cancelled;

    public void Load(AppSettings settings, string? error, bool canCancel = false)
    {
        _settings = new AppSettings
        {
            ServerUrl = settings.ServerUrl,
            CaCertificatePath = settings.CaCertificatePath,
            ExpectedIssuerName = settings.ExpectedIssuerName,
            ClientCertificateThumbprint = settings.ClientCertificateThumbprint,
            DeviceName = settings.DeviceName,
            DataDirectory = settings.DataDirectory,
            AutoCopyText = settings.AutoCopyText,
            AutoStart = settings.AutoStart,
            StartMinimized = settings.StartMinimized,
            CloseToTray = settings.CloseToTray,
            CloudThresholdBytes = settings.CloudThresholdBytes,
            ConnectTimeoutSeconds = settings.ConnectTimeoutSeconds,
        };

        ServerBox.Text = _settings.ServerUrl;
        DeviceBox.Text = _settings.DeviceName;
        DataBox.Text = _settings.DataDirectory;
        ShowError(error);
        CancelButton.IsVisible = canCancel;
        _ = LoadCertificatesAsync();
    }

    private void ShowError(string? error)
    {
        ErrorText.Text = error ?? string.Empty;
        ErrorText.IsVisible = !string.IsNullOrWhiteSpace(error);
    }

    private void Cancel_OnClick(object? sender, RoutedEventArgs e) => Cancelled?.Invoke(this, EventArgs.Empty);

    private async void RefreshCertificates_OnClick(object? sender, RoutedEventArgs e) =>
        await LoadCertificatesAsync();

    private async Task LoadCertificatesAsync()
    {
        if (_loadingCertificates)
        {
            return;
        }

        _loadingCertificates = true;
        CertificateText.Text = "正在读取 Windows 证书库…";
        CertificateDetailText.Text = string.Empty;
        ConnectButton.IsEnabled = false;
        try
        {
            var choices = await Task.Run(CertificateCatalog.ListUsable);
            var preferred = CertificateCatalog.Preferred(choices, _settings.ClientCertificateThumbprint);
            _detectedThumbprint = preferred?.Thumbprint;

            if (preferred is null)
            {
                CertificateText.Text = "未找到可用的客户端证书";
                CertificateDetailText.Text =
                    "请把带私钥、启用 clientAuth 的客户端证书导入“当前用户 → 个人 → 证书”，然后点击重新检测。";
                return;
            }

            CertificateText.Text = preferred.Subject;
            CertificateDetailText.Text =
                $"颁发者：{preferred.Issuer} · 有效期至 {preferred.NotAfter:yyyy-MM-dd}" +
                (choices.Count > 1 ? $" · 共检测到 {choices.Count} 张，已自动选择最新的一张" : string.Empty);
            ConnectButton.IsEnabled = true;
        }
        catch (Exception exception)
        {
            _log.Warn("loading certificates failed", exception);
            CertificateText.Text = "读取证书库失败";
            CertificateDetailText.Text = exception.Message;
        }
        finally
        {
            _loadingCertificates = false;
        }
    }

    private async void BrowseData_OnClick(object? sender, RoutedEventArgs e)
    {
        if (TopLevel.GetTopLevel(this) is not { } topLevel)
        {
            return;
        }
        var folders = await topLevel.StorageProvider.OpenFolderPickerAsync(new FolderPickerOpenOptions
        {
            Title = "选择数据目录",
            AllowMultiple = false,
        });
        var path = folders.Count > 0 ? folders[0].TryGetLocalPath() : null;
        if (!string.IsNullOrWhiteSpace(path))
        {
            DataBox.Text = path;
        }
    }

    private async void Connect_OnClick(object? sender, RoutedEventArgs e)
    {
        if (_busy)
        {
            return;
        }

        ShowError(null);
        var settings = new AppSettings
        {
            ServerUrl = ServerBox.Text?.Trim() ?? string.Empty,
            // A CA file is optional and only kept for deployments that
            // explicitly pin a private server CA. Normal Windows trust needs
            // no file at all.
            CaCertificatePath = File.Exists(_settings.CaCertificatePath)
                ? _settings.CaCertificatePath
                : string.Empty,
            ExpectedIssuerName = _settings.ExpectedIssuerName,
            ClientCertificateThumbprint = _detectedThumbprint,
            DeviceName = DeviceBox.Text?.Trim() ?? string.Empty,
            DataDirectory = DataBox.Text?.Trim() ?? string.Empty,
            AutoCopyText = _settings.AutoCopyText,
            AutoStart = _settings.AutoStart,
            StartMinimized = _settings.StartMinimized,
            CloseToTray = _settings.CloseToTray,
            CloudThresholdBytes = _settings.CloudThresholdBytes,
            ConnectTimeoutSeconds = _settings.ConnectTimeoutSeconds,
        };

        try
        {
            settings.Normalize();
            settings.Validate();
            if (string.IsNullOrWhiteSpace(_detectedThumbprint))
            {
                throw new ConfigurationException("Windows 证书库中没有可用的客户端证书");
            }
        }
        catch (Exception exception)
        {
            ShowError(exception.Message);
            return;
        }

        _busy = true;
        ConnectButton.IsEnabled = false;
        StatusText.IsVisible = true;
        StatusText.Text = "正在建立 mTLS 连接并等待服务器欢迎帧…";
        try
        {
            var error = await _connectAsync(settings);
            if (error is not null)
            {
                ShowError(error);
            }
        }
        finally
        {
            _busy = false;
            ConnectButton.IsEnabled = _detectedThumbprint is not null;
            StatusText.IsVisible = false;
        }
    }
}


