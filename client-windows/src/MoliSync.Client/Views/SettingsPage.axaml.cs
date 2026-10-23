using System.Diagnostics;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using Avalonia.Platform.Storage;
using Avalonia.Threading;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public partial class SettingsPage : UserControl
{
    private readonly AutoStartManager _autoStart = new();

    public SettingsPage()
    {
        InitializeComponent();
        Loaded += (_, _) => RefreshToggles();
    }

    public event EventHandler? SettingsChanged;

    public event EventHandler? ReconfigureRequested;

    private ShellViewModel? Shell => DataContext as ShellViewModel;

    private void RefreshToggles()
    {
        if (Shell is not { } shell)
        {
            return;
        }
        AutoStartToggle.IsChecked = _autoStart.IsEnabled;
        StartMinimizedToggle.IsChecked = shell.Settings.StartMinimized;
        CloseToTrayToggle.IsChecked = shell.Settings.CloseToTray;
        AutoCopyToggle.IsChecked = shell.Settings.AutoCopyText;
    }

    private async void CheckUpdates_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is not { } shell)
        {
            return;
        }
        await shell.CheckForUpdatesAsync(force: true);
        if (shell.UpdateUrl is { Length: > 0 } url && shell.HasUpdateNotification)
        {
            MainWindow.OpenExternal(url);
        }
    }

    private async void Save_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is not { } shell)
        {
            return;
        }

        var settings = shell.Settings;
        settings.AutoCopyText = AutoCopyToggle.IsChecked == true;
        settings.StartMinimized = StartMinimizedToggle.IsChecked == true;
        settings.CloseToTray = CloseToTrayToggle.IsChecked == true;
        settings.CloudThresholdBytes = (long)(shell.ThresholdMiB * 1024 * 1024);

        var autoStartRequested = AutoStartToggle.IsChecked == true;
        var result = await _autoStart.SetEnabledAsync(autoStartRequested, settings.StartMinimized);
        if (result.Error is not null)
        {
            AutoStartToggle.IsChecked = _autoStart.IsEnabled;
            shell.Notify(result.Error);
            return;
        }

        settings.AutoStart = result.Enabled;
        shell.UpdateSettings(settings);
        SettingsChanged?.Invoke(this, EventArgs.Empty);
    }

    private async void ChangeFolder_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is not { } shell || TopLevel.GetTopLevel(this) is not { } topLevel)
        {
            return;
        }

        var folders = await topLevel.StorageProvider.OpenFolderPickerAsync(new FolderPickerOpenOptions
        {
            Title = "选择空文件夹作为新的数据目录",
            AllowMultiple = false,
        });
        var target = folders.Count > 0 ? folders[0].TryGetLocalPath() : null;
        if (string.IsNullOrWhiteSpace(target))
        {
            return;
        }

        try
        {
            DataDirectoryManager.ValidateTarget(shell.Settings.DataDirectory, target);
        }
        catch (Exception exception)
        {
            shell.Notify(exception.Message);
            return;
        }

        var window = TopLevel.GetTopLevel(this) as Window;
        if (window is null)
        {
            return;
        }

        var manager = shell.Session?.DataDirectory ?? new DataDirectoryManager(shell.Settings.DataDirectory);
        var migration = new MigrationWindow(shell.Settings.DataDirectory, target, manager);
        var result = await migration.ShowDialog<MigrationResult?>(window);
        if (result is null)
        {
            if (!string.IsNullOrWhiteSpace(migration.LastError))
            {
                shell.Notify($"迁移失败：{migration.LastError}");
            }
            return;
        }

        shell.Settings.DataDirectory = result.DataDirectory;
        shell.UpdateSettings(shell.Settings);
        shell.Notify(result.CleanupWarning ?? "数据目录已迁移，旧目录已删除");
        SettingsChanged?.Invoke(this, EventArgs.Empty);
    }

    private void OpenFolder_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is { } shell)
        {
            Directory.CreateDirectory(shell.Settings.DataDirectory);
            Process.Start(new ProcessStartInfo { FileName = shell.Settings.DataDirectory, UseShellExecute = true });
        }
    }

    private void OpenLogs_OnClick(object? sender, RoutedEventArgs e)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(AppPaths.LogFile)!);
        Process.Start(new ProcessStartInfo
        {
            FileName = Path.GetDirectoryName(AppPaths.LogFile)!,
            UseShellExecute = true,
        });
    }

    private void Reconfigure_OnClick(object? sender, RoutedEventArgs e) =>
        ReconfigureRequested?.Invoke(this, EventArgs.Empty);
}

