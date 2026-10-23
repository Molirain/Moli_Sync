using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Markup.Xaml;
using Avalonia.Media.Imaging;
using Avalonia.Platform;
using Avalonia.Threading;
using MoliSync.Client.Logging;
using MoliSync.Client.Services;
using MoliSync.Client.Views;

namespace MoliSync.Client;

public partial class MainWindow : Window
{
    private readonly IClientLog _log;
    private readonly SettingsStore _settingsStore;
    private readonly ShellViewModel _shell;
    private readonly TextPage _textPage;
    private readonly ImagesPage _imagesPage;
    private readonly FilesPage _filesPage;
    private readonly SettingsPage _settingsPage;
    private readonly DispatcherTimer _toastTimer;
    private AppSettings _settings;
    private SetupPage? _setupPage;
    private TrayIcon? _tray;
    private bool _initialized;
    private bool _allowClose;

    public MainWindow()
    {
        InitializeComponent();
        _log = new FileClientLog();
        _settingsStore = new SettingsStore(_log);
        _settings = _settingsStore.Load();
        _shell = new ShellViewModel(_settings, _log);
        DataContext = _shell;

        _textPage = new TextPage { DataContext = _shell };
        _imagesPage = new ImagesPage { DataContext = _shell };
        _filesPage = new FilesPage { DataContext = _shell };
        _settingsPage = new SettingsPage { DataContext = _shell };

        _shell.Notification += ShowToast;
        _shell.PropertyChanged += Shell_OnPropertyChanged;
        _settingsPage.SettingsChanged += SettingsPage_OnSettingsChanged;
        _settingsPage.ReconfigureRequested += SettingsPage_OnReconfigureRequested;

        _toastTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(3) };
        _toastTimer.Tick += (_, _) =>
        {
            _toastTimer.Stop();
            ToastHost.IsVisible = false;
        };

        CreateTrayIcon();
    }

    protected override async void OnOpened(EventArgs e)
    {
        base.OnOpened(e);        if (_initialized)
        {
            return;
        }
        _initialized = true;
        await LoadAsync();
    }

    private async Task LoadAsync()
    {
        LoadingHost.IsVisible = true;
        SetupHost.IsVisible = false;
        ShellHost.IsVisible = false;

        if (!_settings.IsConfigured)
        {
            ShowSetup(null);
            return;
        }

        LoadingStatus.Text = "建立 mTLS 连接并读取云端记录";
        try
        {
            var session = await Task.Run(() => AppSession.Create(_settings, _log));
            var welcomed = await session.WaitForWelcomeAsync(TimeSpan.FromSeconds(20), CancellationToken.None);
            if (!welcomed)
            {
                await session.DisposeAsync();
                ShowSetup("连接超时。请检查服务器地址、反向代理和客户端证书。");
                return;
            }

            _shell.Attach(session);
            await _shell.RefreshAsync();
            ShowShell();
        }
        catch (Exception exception)
        {
            _log.Error("startup failed", exception);
            ShowSetup(exception.Message);
        }
    }

    private void ShowShell()
    {
        LoadingHost.IsVisible = false;
        SetupHost.IsVisible = false;
        ShellHost.IsVisible = true;
        SelectPage("text");
        if (Environment.GetCommandLineArgs().Contains("--background", StringComparer.OrdinalIgnoreCase) &&
            _settings.StartMinimized)
        {
            Hide();
        }
    }

    private void ShowSetup(string? error)
    {
        LoadingHost.IsVisible = false;
        ShellHost.IsVisible = false;
        if (_setupPage is null)
        {
            _setupPage = new SetupPage(_log, ConnectFromSetupAsync);
            _setupPage.Cancelled += (_, _) =>
            {
                if (_shell.Session is not null)
                {
                    ShowShell();
                }
            };
        }
        _setupPage.Load(_settings, error, _shell.Session is not null);
        SetupHost.Content = _setupPage;
        SetupHost.IsVisible = true;
    }

    private async Task<string?> ConnectFromSetupAsync(AppSettings settings)
    {
        settings.Normalize();
        settings.Validate();

        AppSession? session = null;
        try
        {
            session = await Task.Run(() => AppSession.Create(settings, _log));
            var welcomed = await session.WaitForWelcomeAsync(TimeSpan.FromSeconds(20), CancellationToken.None);
            if (!welcomed)
            {
                throw new TimeoutException("连接超时。请确认反向代理已启动，并且客户端证书由所选 CA 签发。");
            }

            await ReplaceSessionAsync(session).ConfigureAwait(true);
            session = null;
            _settings = settings;
            _shell.UpdateSettings(settings);
            _settingsStore.Save(settings);
            await _shell.RefreshAsync();
            ShowShell();
            return null;
        }
        catch (Exception exception)
        {
            _log.Error("setup connection failed", exception);
            if (session is not null)
            {
                await session.DisposeAsync();
            }
            return exception.Message;
        }
    }

    private async Task ReplaceSessionAsync(AppSession newSession)
    {
        var previous = _shell.Session;
        _shell.Detach();
        if (previous is not null)
        {
            await previous.DisposeAsync().ConfigureAwait(true);
        }
        _shell.Attach(newSession);
    }

    private void CreateTrayIcon()
    {
        try
        {
            var icon = new WindowIcon(AssetLoader.Open(new Uri("avares://MoliSync.Client/Assets/app.ico")));
            var menu = new NativeMenu();
            var openItem = new NativeMenuItem("打开 MoliSync");
            openItem.Click += (_, _) => ShowFromTray();
            var exitItem = new NativeMenuItem("退出");
            exitItem.Click += (_, _) => ExitApplication();
            menu.Items.Add(openItem);
            menu.Items.Add(new NativeMenuItemSeparator());
            menu.Items.Add(exitItem);

            _tray = new TrayIcon
            {
                Icon = icon,
                ToolTipText = "MoliSync",
                Menu = menu,
            };
            _tray.Clicked += (_, _) => ShowFromTray();
            TrayIcon.SetIcons(Application.Current!, new TrayIcons { _tray });
        }
        catch (Exception exception)
        {
            _log.Warn("tray icon is unavailable", exception);
        }
    }

    private void ShowFromTray()
    {
        Show();
        WindowState = WindowState.Normal;
        Activate();
    }

    private void ExitApplication()
    {
        _allowClose = true;
        Close();
    }

    private void SelectPage(string tag)
    {
        switch (tag)
        {
            case "images":
                PageHost.Content = _imagesPage;
                Title = "MoliSync — 图片";
                break;
            case "files":
                PageHost.Content = _filesPage;
                Title = "MoliSync — 文件";
                break;
            case "settings":
                PageHost.Content = _settingsPage;
                Title = "MoliSync — 设置";
                break;
            default:
                PageHost.Content = _textPage;
                Title = "MoliSync — 文字";
                break;
        }

        foreach (var button in new[] { NavText, NavImages, NavFiles, NavSettings })
        {
            button.Classes.Remove("active");
            if (string.Equals(button.Tag as string, tag, StringComparison.Ordinal))
            {
                button.Classes.Add("active");
            }
        }
    }

    private DevicesWindow? _devicesWindow;

    private void Devices_OnClick(object? sender, Avalonia.Interactivity.RoutedEventArgs e)
    {
        if (_devicesWindow is { } existing)
        {
            existing.Show();
            existing.Activate();
            return;
        }

        _devicesWindow = new DevicesWindow(_shell);
        _devicesWindow.Closed += (_, _) => _devicesWindow = null;
        _devicesWindow.Show(this);
    }

    private void Nav_OnClick(object? sender, Avalonia.Interactivity.RoutedEventArgs e)
    {
        if (sender is Button { Tag: string tag })
        {
            SelectPage(tag);
        }
    }

    private async void SettingsPage_OnSettingsChanged(object? sender, EventArgs e)
    {
        try
        {
            _settings.Normalize();
            _settingsStore.Save(_settings);
            _shell.Notify("设置已保存");
            if (_shell.Session is not null)
            {
                var session = await Task.Run(() => AppSession.Create(_settings, _log));
                var welcomed = await session.WaitForWelcomeAsync(TimeSpan.FromSeconds(20), CancellationToken.None);
                if (!welcomed)
                {
                    await session.DisposeAsync();
                    _shell.Notify("设置已保存，但重新连接超时");
                    return;
                }
                await ReplaceSessionAsync(session);
                await _shell.RefreshAsync();
            }
        }
        catch (Exception exception)
        {
            _log.Error("saving settings failed", exception);
            _shell.Notify($"保存设置失败：{exception.Message}");
        }
    }

    private void SettingsPage_OnReconfigureRequested(object? sender, EventArgs e) => ShowSetup(null);

    private void Shell_OnPropertyChanged(object? sender, System.ComponentModel.PropertyChangedEventArgs e)
    {
        if (e.PropertyName is not (nameof(ShellViewModel.HasUpdateNotification) or null))
        {
            return;
        }
        UpdateBanner.IsVisible = _shell.HasUpdateNotification;
        UpdateBannerText.Text = _shell.UpdateNotification ?? string.Empty;
    }

    private void UpdateBannerClose_OnClick(object? sender, Avalonia.Interactivity.RoutedEventArgs e) =>
        _shell.DismissUpdate();

    private void UpdateBannerDownload_OnClick(object? sender, Avalonia.Interactivity.RoutedEventArgs e) =>
        OpenExternal(_shell.UpdateUrl);

    internal static void OpenExternal(string? url)
    {
        if (string.IsNullOrWhiteSpace(url) ||
            !Uri.TryCreate(url, UriKind.Absolute, out var target) ||
            target.Scheme != Uri.UriSchemeHttps)
        {
            return;
        }
        try
        {
            System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo(target.AbsoluteUri)
            {
                UseShellExecute = true,
            });
        }
        catch (Exception)
        {
            // A missing browser association must not break the app.
        }
    }

    private void ShowToast(string message)
    {        ToastText.Text = message.Length > 160 ? message[..160] + "…" : message;
        ToastHost.IsVisible = true;
        _toastTimer.Stop();
        _toastTimer.Start();
    }

    protected override async void OnClosing(WindowClosingEventArgs e)
    {
        if (!_allowClose && _settings.CloseToTray)
        {
            e.Cancel = true;
            Hide();
            return;
        }
        base.OnClosing(e);
    }

    private async Task ShutdownAsync()
    {
        if (_allowClose && !IsVisible)
        {
            return;
        }
        _allowClose = true;
        _toastTimer.Stop();
        if (_tray is not null)
        {
            _tray.IsVisible = false;
            _tray.Dispose();
            _tray = null;
        }
        if (_shell.Session is { } session)
        {
            _shell.Detach();
            await session.DisposeAsync();
        }
        Close();
    }

    protected override void OnClosed(EventArgs e)
    {
        base.OnClosed(e);
        if (_tray is not null)
        {
            _tray.Dispose();
            _tray = null;
        }
        _ = Task.Run(async () =>
        {
            if (_shell.Session is { } session)
            {
                await session.DisposeAsync();
            }
        });
    }
}



