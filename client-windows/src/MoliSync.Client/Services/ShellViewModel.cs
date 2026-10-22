using System.Collections.ObjectModel;
using System.ComponentModel;
using System.Runtime.CompilerServices;
using Avalonia.Media;
using Avalonia.Threading;
using MoliSync.Client.Logging;
using MoliSync.Client.Sync;

namespace MoliSync.Client.Services;

public sealed class RecordItem : INotifyPropertyChanged
{
    private static readonly (Color Start, Color End)[] Palette =
    {
        (Color.Parse("#3E7D68"), Color.Parse("#9FD3B8")),
        (Color.Parse("#B4644C"), Color.Parse("#E7B98D")),
        (Color.Parse("#3F617D"), Color.Parse("#A9C7DA")),
        (Color.Parse("#6D5D8C"), Color.Parse("#C9B8DC")),
        (Color.Parse("#8A6A2F"), Color.Parse("#E4C88E")),
        (Color.Parse("#2F5E55"), Color.Parse("#7FB6A3")),
    };

    private double _downloadPercent;
    private bool _isDownloading;
    private string? _localPath;
    private string? _error;

    public RecordItem(CloudRecord record, string senderName, bool isMine)
    {
        Record = record;
        SenderName = senderName;
        IsMine = isMine;
        var index = Math.Abs(record.RecordId.GetHashCode(StringComparison.Ordinal)) % Palette.Length;
        var colors = Palette[index];
        AccentBrush = new LinearGradientBrush
        {
            StartPoint = new Avalonia.RelativePoint(0, 0, Avalonia.RelativeUnit.Relative),
            EndPoint = new Avalonia.RelativePoint(1, 1, Avalonia.RelativeUnit.Relative),
            GradientStops =
            {
                new GradientStop(colors.Start, 0),
                new GradientStop(colors.End, 1),
            },
        };
    }

    public event PropertyChangedEventHandler? PropertyChanged;

    public CloudRecord Record { get; }

    public string RecordId => Record.RecordId;

    public bool IsMine { get; }

    public bool IsText => Record.IsText;

    public bool IsImage => Record.IsImage;

    public bool HasBlob => Record.HasBlob;

    public string SenderName { get; }

    public string Initial => SenderName.Length > 0 ? SenderName[..1] : "?";

    public string Text => Record.TextContent ?? string.Empty;

    public string FileName => string.IsNullOrWhiteSpace(Record.FileName) ? Record.RecordId : Record.FileName;

    public string KindLabel => IsText
        ? "TEXT"
        : Path.GetExtension(FileName).TrimStart('.').ToUpperInvariant() is { Length: > 0 } extension
            ? extension[..Math.Min(4, extension.Length)]
            : IsImage ? "IMG" : "FILE";

    public string TimeText => DateTimeOffset
        .FromUnixTimeMilliseconds(Record.CreatedAt)
        .ToLocalTime()
        .ToString("MM-dd HH:mm");

    public string SizeText => FormatBytes(Record.FileSize);

    public string TextMeta => IsText ? $"{Text.Length} 字符" : SizeText;

    public string Subtitle => $"{SenderName} · {TimeText}";

    public string SizeAndSender => HasBlob
        ? $"{SizeText} · {SenderName}"
        : $"来自 {SenderName}";

    public IBrush AccentBrush { get; }

    public double DownloadPercent
    {
        get => _downloadPercent;
        set => Set(ref _downloadPercent, value);
    }

    public bool IsDownloading
    {
        get => _isDownloading;
        set => Set(ref _isDownloading, value);
    }

    public string? LocalPath
    {
        get => _localPath;
        set => Set(ref _localPath, value);
    }

    public string? Error
    {
        get => _error;
        set => Set(ref _error, value);
    }

    public bool IsDownloaded => !string.IsNullOrWhiteSpace(LocalPath) && File.Exists(LocalPath);

    public string DownloadLabel => IsDownloaded ? "打开" : "下载";

    public void MarkDownloaded(string path)
    {
        LocalPath = path;
        DownloadPercent = 100;
        IsDownloading = false;
        OnPropertyChanged(nameof(IsDownloaded));
        OnPropertyChanged(nameof(DownloadLabel));
    }

    public void MarkFailed(string error)
    {
        Error = error;
        IsDownloading = false;
    }

    public void MarkDownloading()
    {
        Error = null;
        DownloadPercent = 0;
        IsDownloading = true;
    }

    public void ReportDownload(long bytes)
    {
        if (Record.FileSize <= 0)
        {
            return;
        }
        DownloadPercent = Math.Clamp(bytes * 100.0 / Record.FileSize, 0, 100);
        OnPropertyChanged(nameof(DownloadPercent));
    }

    public static string FormatBytes(long bytes)
    {
        if (bytes < 1024)
        {
            return $"{bytes} B";
        }
        if (bytes < 1024L * 1024)
        {
            return $"{bytes / 1024.0:F1} KB";
        }
        if (bytes < 1024L * 1024 * 1024)
        {
            return $"{bytes / (1024.0 * 1024):F1} MB";
        }
        return $"{bytes / (1024.0 * 1024 * 1024):F2} GB";
    }

    private void Set<T>(ref T field, T value, [CallerMemberName] string? name = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value))
        {
            return;
        }
        field = value;
        OnPropertyChanged(name);
    }

    private void OnPropertyChanged([CallerMemberName] string? name = null) =>
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}

public sealed class ShellViewModel : INotifyPropertyChanged
{
    private readonly IClientLog _log;
    private readonly SemaphoreSlim _refreshGate = new(1, 1);
    private AppSession? _session;
    private string _statusText = "未连接";
    private bool _isRefreshing;
    private double _thresholdMiB;

    public ShellViewModel(AppSettings settings, IClientLog log)
    {
        Settings = settings;
        _log = log;
        _thresholdMiB = Math.Max(5, settings.CloudThresholdBytes / (1024.0 * 1024));
        Devices = new DeviceDirectory();
    }

    public event PropertyChangedEventHandler? PropertyChanged;

    public event Action<string>? Notification;

    public AppSettings Settings { get; private set; }

    public DeviceDirectory Devices { get; }

    public ObservableCollection<RecordItem> TextRecords { get; } = new();

    public ObservableCollection<RecordItem> ImageRecords { get; } = new();

    public ObservableCollection<RecordItem> FileRecords { get; } = new();

    public AppSession? Session => _session;

    public string DeviceId => _session?.DeviceId ?? string.Empty;

    public string OnlineText => Devices.OnlineText;

    public string StatusText
    {
        get => _statusText;
        private set => Set(ref _statusText, value);
    }

    public bool IsRefreshing
    {
        get => _isRefreshing;
        private set => Set(ref _isRefreshing, value);
    }

    public double ThresholdMiB
    {
        get => _thresholdMiB;
        set
        {
            var clamped = Math.Clamp(value, 5, 50);
            if (Math.Abs(_thresholdMiB - clamped) < 0.01)
            {
                return;
            }
            _thresholdMiB = clamped;
            Settings.CloudThresholdBytes = (long)(clamped * 1024 * 1024);
            OnPropertyChanged();
            OnPropertyChanged(nameof(ThresholdText));
        }
    }

    public string ThresholdText => $"{ThresholdMiB:0} MiB";

    public string ServerDisplay => Settings.ServerUrl;

    public string DataDirectory => Settings.DataDirectory;

    public string CertificateDisplay => _session is { } session
        ? $"{session.Identity.CommonName}（{session.Identity.Issuer}）"
        : "连接后自动读取 Windows 证书库";

    public string Version => typeof(ShellViewModel).Assembly.GetName().Version?.ToString(3) ?? "0.4.0";

    public string DeviceName
    {
        get => Settings.DeviceName;
        set
        {
            if (Settings.DeviceName == value)
            {
                return;
            }
            Settings.DeviceName = value;
            OnPropertyChanged();
        }
    }

    public void Attach(AppSession session)
    {
        ArgumentNullException.ThrowIfNull(session);
        Detach();
        _session = session;
        UpdateSettings(session.Settings);
        _session.Dispatcher.WelcomeReceived += OnWelcome;
        _session.Dispatcher.PresenceChanged += OnPresence;
        _session.Dispatcher.RecordReceived += OnRecordReceived;

        var self = new DeviceView
        {
            DeviceId = session.DeviceId,
            Name = Settings.DeviceName,
            Platform = "windows",
            CommonName = session.Identity.CommonName,
        };
        Devices.Reset(self, Array.Empty<DeviceView>());
        StatusText = "已连接";
        OnPropertyChanged(nameof(Session));
        OnPropertyChanged(nameof(DeviceId));
        OnPropertyChanged(nameof(CertificateDisplay));
        OnPropertyChanged(nameof(ServerDisplay));
        OnPropertyChanged(nameof(DataDirectory));
        OnPropertyChanged(nameof(DeviceName));
        OnPropertyChanged(nameof(OnlineText));
    }

    public void Detach()
    {
        if (_session is null)
        {
            return;
        }
        _session.Dispatcher.WelcomeReceived -= OnWelcome;
        _session.Dispatcher.PresenceChanged -= OnPresence;
        _session.Dispatcher.RecordReceived -= OnRecordReceived;
        _session = null;
        TextRecords.Clear();
        ImageRecords.Clear();
        FileRecords.Clear();
        Devices.Reset(new DeviceView { DeviceId = string.Empty, Name = string.Empty }, Array.Empty<DeviceView>());
        StatusText = "未连接";
        OnPropertyChanged(nameof(Session));
        OnPropertyChanged(nameof(DeviceId));
        OnPropertyChanged(nameof(OnlineText));
    }

    public void UpdateSettings(AppSettings settings)
    {
        ArgumentNullException.ThrowIfNull(settings);
        settings.Normalize();
        Settings = settings;
        _thresholdMiB = Math.Clamp(settings.CloudThresholdBytes / (1024.0 * 1024), 5, 50);
        OnPropertyChanged(nameof(Settings));
        OnPropertyChanged(nameof(ThresholdMiB));
        OnPropertyChanged(nameof(ThresholdText));
        OnPropertyChanged(nameof(ServerDisplay));
        OnPropertyChanged(nameof(DataDirectory));
        OnPropertyChanged(nameof(CertificateDisplay));
        OnPropertyChanged(nameof(DeviceName));
    }

    public async Task RefreshAsync(CancellationToken cancellationToken = default)
    {
        var session = _session;
        if (session is null)
        {
            return;
        }

        await _refreshGate.WaitAsync(cancellationToken).ConfigureAwait(true);
        try
        {
            IsRefreshing = true;
            var records = await session.Cloud.ListAsync(null, 100, cancellationToken).ConfigureAwait(true);
            ApplyRecords(records, session.DeviceId);
            StatusText = $"已同步 · {DateTime.Now:HH:mm}";
        }
        catch (OperationCanceledException)
        {
            throw;
        }
        catch (Exception exception)
        {
            _log.Warn("loading cloud records failed", exception);
            StatusText = "同步失败";
            Notification?.Invoke(exception.Message);
        }
        finally
        {
            IsRefreshing = false;
            _refreshGate.Release();
        }
    }

    public async Task PostTextAsync(string text, CancellationToken cancellationToken)
    {
        var session = _session ?? throw new InvalidOperationException("尚未连接服务器");
        if (string.IsNullOrWhiteSpace(text))
        {
            throw new InvalidOperationException("文字不能为空");
        }
        if (text.Length > 64 * 1024)
        {
            throw new InvalidOperationException("文字不能超过 64 KiB");
        }

        await session.Cloud.PostTextAsync(text, cancellationToken).ConfigureAwait(true);
        await RefreshAsync(cancellationToken).ConfigureAwait(true);
    }

    public async Task UploadAsync(
        IReadOnlyList<string> files,
        string kind,
        IProgress<(int FileIndex, long Bytes, long Total)>? progress,
        CancellationToken cancellationToken)
    {
        var session = _session ?? throw new InvalidOperationException("尚未连接服务器");
        var total = files.Sum(path => new FileInfo(path).Length);
        var completedBytes = 0L;
        for (var index = 0; index < files.Count; index++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            var path = files[index];
            var length = new FileInfo(path).Length;
            await using var stream = new FileStream(
                path,
                FileMode.Open,
                FileAccess.Read,
                FileShare.Read,
                128 * 1024,
                FileOptions.Asynchronous | FileOptions.SequentialScan);
            var fileIndex = index;
            var completed = completedBytes;
            var fileProgress = new Progress<long>(bytes =>
                progress?.Report((fileIndex, completed + bytes, total)));
            await session.Cloud.UploadAsync(
                kind,
                Path.GetFileName(path),
                MimeTypes.FromFileName(path),
                stream,
                stream.Length,
                fileProgress,
                cancellationToken).ConfigureAwait(true);
            completedBytes += length;
        }
        await RefreshAsync(cancellationToken).ConfigureAwait(true);
    }

    public async Task<string> DownloadAsync(
        RecordItem item,
        IProgress<long>? progress,
        CancellationToken cancellationToken)
    {
        var session = _session ?? throw new InvalidOperationException("尚未连接服务器");
        item.MarkDownloading();
        try
        {
            var downloadProgress = new Progress<long>(bytes =>
            {
                item.ReportDownload(bytes);
                progress?.Report(bytes);
            });
            var result = await session.Cloud
                .DownloadAsync(item.Record, downloadProgress, cancellationToken)
                .ConfigureAwait(true);
            item.MarkDownloaded(result.FilePath);
            return result.FilePath;
        }
        catch (Exception exception)
        {
            item.MarkFailed(exception.Message);
            throw;
        }
    }

    public async Task DeleteAsync(RecordItem item, CancellationToken cancellationToken)
    {
        var session = _session ?? throw new InvalidOperationException("尚未连接服务器");
        await session.Cloud.DeleteAsync(item.RecordId, cancellationToken).ConfigureAwait(true);
        TextRecords.Remove(item);
        ImageRecords.Remove(item);
        FileRecords.Remove(item);
    }

    public bool CanUseCloud(long size) => size <= Settings.CloudThresholdBytes;

    public void Notify(string message) => Notification?.Invoke(message);

    private void ApplyRecords(IReadOnlyList<CloudRecord> records, string localDeviceId)
    {
        TextRecords.Clear();
        ImageRecords.Clear();
        FileRecords.Clear();
        foreach (var record in records)
        {
            var sender = string.IsNullOrWhiteSpace(record.SenderName)
                ? record.SenderDeviceId[..Math.Min(8, record.SenderDeviceId.Length)]
                : record.SenderName;
            var item = new RecordItem(
                record,
                sender,
                string.Equals(record.SenderDeviceId, localDeviceId, StringComparison.Ordinal));
            if (record.IsText)
            {
                TextRecords.Add(item);
            }
            else if (record.IsImage)
            {
                ImageRecords.Add(item);
            }
            else
            {
                FileRecords.Add(item);
            }
        }
    }

    private void OnWelcome(WelcomeState welcome)
    {
        Dispatcher.UIThread.Post(() =>
        {
            var self = new DeviceView
            {
                DeviceId = welcome.DeviceId,
                Name = Settings.DeviceName,
                Platform = "windows",
                CommonName = _session?.Identity.CommonName,
            };
            Devices.Reset(self, welcome.Devices);
            StatusText = "已连接";
            OnPropertyChanged(nameof(OnlineText));
            _ = RefreshAsync();
        });
    }

    private void OnPresence(PresenceUpdate update) =>
        Dispatcher.UIThread.Post(() =>
        {
            Devices.Apply(update);
            OnPropertyChanged(nameof(OnlineText));
        });

    private void OnRecordReceived() =>
        Dispatcher.UIThread.Post(() => _ = RefreshAsync());

    private bool Set<T>(ref T field, T value, [CallerMemberName] string? name = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value))
        {
            return false;
        }
        field = value;
        OnPropertyChanged(name);
        return true;
    }

    private void OnPropertyChanged([CallerMemberName] string? name = null) =>
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
