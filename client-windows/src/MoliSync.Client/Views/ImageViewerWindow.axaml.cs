using System.Diagnostics;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Media.Imaging;
using Avalonia.Platform.Storage;

namespace MoliSync.Client.Views;

public partial class ImageViewerWindow : Window
{
    private readonly string _path;

    public ImageViewerWindow()
    {
        InitializeComponent();
        _path = string.Empty;
    }

    public ImageViewerWindow(string title, string path)
    {
        InitializeComponent();
        _path = path;
        Title = $"MoliSync — {title}";
        TitleText.Text = title;
        PreviewImage.Source = new Bitmap(path);
    }

    private void OpenExternal_OnClick(object? sender, RoutedEventArgs e)
    {
        if (File.Exists(_path))
        {
            Process.Start(new ProcessStartInfo { FileName = _path, UseShellExecute = true });
        }
    }

    private async void SaveAs_OnClick(object? sender, RoutedEventArgs e)
    {
        if (!File.Exists(_path) || TopLevel.GetTopLevel(this) is not { } topLevel)
        {
            return;
        }

        var target = await topLevel.StorageProvider.SaveFilePickerAsync(new FilePickerSaveOptions
        {
            Title = "另存图片",
            SuggestedFileName = Path.GetFileName(_path),
        });
        var destination = target?.TryGetLocalPath();
        if (!string.IsNullOrWhiteSpace(destination))
        {
            File.Copy(_path, destination, overwrite: true);
        }
    }

    private void Close_OnClick(object? sender, RoutedEventArgs e) => Close();
}
