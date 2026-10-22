using Avalonia.Controls;
using Avalonia.Input.Platform;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public partial class TextPage : UserControl
{
    public TextPage() => InitializeComponent();

    private ShellViewModel? Shell => DataContext as ShellViewModel;

    private async void Refresh_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is { } shell)
        {
            await shell.RefreshAsync();
        }
    }

    private async void Compose_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is not { } shell)
        {
            return;
        }

        var dialog = new TextComposeWindow();
        var text = await dialog.ShowDialog<string?>(TopLevel.GetTopLevel(this) as Window ?? throw new InvalidOperationException());
        if (string.IsNullOrWhiteSpace(text))
        {
            return;
        }

        try
        {
            await shell.PostTextAsync(text, CancellationToken.None);
            shell.Notify("文字已保存到服务器");
        }
        catch (Exception exception)
        {
            shell.Notify($"发送失败：{exception.Message}");
        }
    }

    private async void Expand_OnClick(object? sender, RoutedEventArgs e)
    {
        if (sender is not Control { DataContext: RecordItem item } ||
            TopLevel.GetTopLevel(this) is not Window owner)
        {
            return;
        }
        await new TextDetailWindow(item).ShowDialog(owner);
    }

    private async void Copy_OnClick(object? sender, RoutedEventArgs e)
    {
        if (sender is Control { DataContext: RecordItem item } &&
            TopLevel.GetTopLevel(this)?.Clipboard is { } clipboard)
        {
            await clipboard.SetTextAsync(item.Text);
            Shell?.Notify("已复制到剪贴板");
        }
    }

    private async void Delete_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Shell is not { } shell || sender is not Control { DataContext: RecordItem item })
        {
            return;
        }

        var owner = TopLevel.GetTopLevel(this) as Window;
        if (owner is null)
        {
            return;
        }
        var confirmed = await new ConfirmWindow(
            "删除记录",
            "确定要删除这条文字记录吗？删除后所有设备都将看不到它。",
            "删除").ShowDialog<bool>(owner);
        if (!confirmed)
        {
            return;
        }

        try
        {
            await shell.DeleteAsync(item, CancellationToken.None);
            shell.Notify("记录已删除");
        }
        catch (Exception exception)
        {
            shell.Notify($"删除失败：{exception.Message}");
        }
    }
}

