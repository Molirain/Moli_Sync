using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public partial class MigrationWindow : Window
{
    private readonly string _target;
    private readonly DataDirectoryManager _manager;
    private readonly CancellationTokenSource _cancellation = new();

    public MigrationWindow(string source, string target, DataDirectoryManager manager)
    {
        _target = target;
        _manager = manager;
        InitializeComponent();
        TargetText.Text = $"从 {source} 迁移到 {target}";
        Opened += async (_, _) => await RunAsync();
    }

    public string? LastError { get; private set; }

    private async Task RunAsync()
    {
        var progress = new Progress<MigrationProgress>(value =>
        {
            Progress.Value = value.Percent;
            StatusText.Text =
                $"{value.FilesCopied} / {value.TotalFiles} 个文件 · " +
                $"{RecordItem.FormatBytes(value.BytesCopied)} / {RecordItem.FormatBytes(value.TotalBytes)}";
            FileText.Text = value.CurrentFile;
        });

        try
        {
            var result = await Task.Run(
                () => _manager.MigrateAsync(_target, progress, _cancellation.Token),
                _cancellation.Token);
            Close(result);
        }
        catch (OperationCanceledException)
        {
            Close(null);
        }
        catch (Exception exception)
        {
            LastError = exception.Message;
            Close(null);
        }
    }

    private void Cancel_OnClick(object? sender, RoutedEventArgs e)
    {
        CancelButton.IsEnabled = false;
        StatusText.Text = "正在取消并回滚…";
        _cancellation.Cancel();
    }
}

