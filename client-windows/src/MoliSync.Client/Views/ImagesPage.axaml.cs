using System.Diagnostics;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using Avalonia.Platform.Storage;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public partial class ImagesPage : UserControl
{
    private static readonly FilePickerFileType ImageType = new("图片")
    {
        Patterns = new[] { "*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.bmp", "*.heic" },
        MimeTypes = new[] { "image/*" },
    };

    public ImagesPage() => InitializeComponent();

    private ShellViewModel? Shell => DataContext as ShellViewModel;

    private void OpenFolder_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is { } shell)
        {
            Directory.CreateDirectory(shell.Settings.DataDirectory);
            Process.Start(new ProcessStartInfo
            {
                FileName = shell.Settings.DataDirectory,
                UseShellExecute = true,
            });
        }
    }

    private async void Upload_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is not { } shell || TopLevel.GetTopLevel(this) is not { } topLevel)
        {
            return;
        }

        var files = await topLevel.StorageProvider.OpenFilePickerAsync(new FilePickerOpenOptions
        {
            Title = "选择要上传的图片",
            AllowMultiple = true,
            FileTypeFilter = new[] { ImageType },
        });
        var paths = files
            .Select(file => file.TryGetLocalPath())
            .Where(path => !string.IsNullOrWhiteSpace(path))
            .Select(path => path!)
            .ToArray();
        if (paths.Length == 0)
        {
            return;
        }

        var total = paths.Sum(path => new FileInfo(path).Length);
        var mode = await UploadDialog.ChooseModeAsync(
            TopLevel.GetTopLevel(this) as Window,
            paths.Length,
            total,
            shell.CanUseCloud(total));
        if (mode is null)
        {
            return;
        }

        var owner = TopLevel.GetTopLevel(this) as Window;
        if (mode == UploadMode.PeerToPeer)
        {
            if (owner is null)
            {
                return;
            }
            var peer = await new PeerPickerWindow(shell).ShowDialog<MoliSync.Client.Sync.DeviceView?>(owner);
            if (peer is null)
            {
                return;
            }

            var p2pTotal = paths.Sum(path => new FileInfo(path).Length);
            try
            {
                await UploadDialog.RunAsync(owner, "P2P 发送图片", async (progress, cancellationToken) =>
                {
                    await shell.SendP2pAsync(
                        paths,
                        peer,
                        new Progress<long>(sent => progress.Report(new UploadProgress(
                            0,
                            paths.Length,
                            sent,
                            p2pTotal))),
                        cancellationToken);
                });
                shell.Notify($"已通过 P2P 发送到 {peer.Name}");
            }
            catch (Exception exception)
            {
                shell.Notify($"P2P 发送失败：{exception.Message}");
            }
            return;
        }

        try
        {
            await UploadDialog.RunAsync(
                TopLevel.GetTopLevel(this) as Window,
                "上传图片",
                async (progress, cancellationToken) =>
                {
                    await shell.UploadAsync(
                        paths,
                        CloudKinds.Image,
                        new Progress<(int FileIndex, long Bytes, long Total)>(value =>
                            progress.Report(new UploadProgress(
                                value.FileIndex,
                                paths.Length,
                                value.Bytes,
                                value.Total))),
                        cancellationToken);
                });
            shell.Notify("图片已保存到服务器");
        }
        catch (Exception exception)
        {
            shell.Notify($"上传失败：{exception.Message}");
        }
    }

    private async void Preview_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is not { } shell || sender is not Control { DataContext: RecordItem item })
        {
            return;
        }

        if (item.LocalPath is { } downloaded && File.Exists(downloaded))
        {
            ShowViewer(item, downloaded);
            return;
        }

        try
        {
            var session = shell.Session ?? throw new InvalidOperationException("尚未连接服务器");
            var path = await session.Cloud.EnsureCachedAsync(item.Record, CancellationToken.None);
            item.SetThumbnail(path);
            item.MarkDownloadedIfEmpty(path);
            ShowViewer(item, path);        }
        catch (Exception exception)
        {
            shell.Notify($"预览失败：{exception.Message}");
        }
    }

    private void ShowViewer(RecordItem item, string path)
    {
        if (TopLevel.GetTopLevel(this) is not Window owner)
        {
            return;
        }
        new ImageViewerWindow(item.FileName, path).Show(owner);
    }

    private async void Download_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is not { } shell || sender is not Control { DataContext: RecordItem item })
        {
            return;
        }

        if (item.IsDownloaded && item.LocalPath is { } existing)
        {
            Process.Start(new ProcessStartInfo { FileName = existing, UseShellExecute = true });
            return;
        }

        try
        {
            await shell.DownloadAsync(item, null, CancellationToken.None);
            shell.Notify($"已保存到 {item.LocalPath}");
        }
        catch (Exception exception)
        {
            shell.Notify($"下载失败：{exception.Message}");
        }
    }
}

