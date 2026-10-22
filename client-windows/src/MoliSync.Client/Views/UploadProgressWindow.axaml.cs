using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public sealed record UploadProgress(int FileIndex, int FileCount, long Bytes, long Total);

public partial class UploadProgressWindow : Window
{
    private readonly Func<IProgress<UploadProgress>, CancellationToken, Task> _action;
    private readonly CancellationTokenSource _cancellation = new();
    private Exception? _error;

    public UploadProgressWindow(
        string title,
        Func<IProgress<UploadProgress>, CancellationToken, Task> action)
    {
        _action = action;
        InitializeComponent();
        TitleText.Text = title;
        Opened += async (_, _) => await RunAsync();
    }

    private async Task RunAsync()
    {
        var progress = new Progress<UploadProgress>(value =>
        {
            FileText.Text = $"文件 {value.FileIndex + 1} / {value.FileCount}";
            Progress.Value = value.Total <= 0
                ? 0
                : Math.Clamp(value.Bytes * 100.0 / value.Total, 0, 100);
            StatusText.Text = $"{RecordItem.FormatBytes(value.Bytes)} / {RecordItem.FormatBytes(value.Total)}";
        });

        try
        {
            await _action(progress, _cancellation.Token);
            Close();
        }
        catch (OperationCanceledException)
        {
            Close();
        }
        catch (Exception exception)
        {
            _error = exception;
            Close();
        }
    }

    public void ThrowIfFailed()
    {
        if (_error is not null)
        {
            throw new Exception(_error.Message, _error);
        }
    }

    private void Cancel_OnClick(object? sender, RoutedEventArgs e)
    {
        CancelButton.IsEnabled = false;
        StatusText.Text = "正在取消…";
        _cancellation.Cancel();
    }
}

