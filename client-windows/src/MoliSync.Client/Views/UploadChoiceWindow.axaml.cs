using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public enum UploadMode
{
    Cloud,
    PeerToPeer,
}

public partial class UploadChoiceWindow : Window
{
    public UploadChoiceWindow() => InitializeComponent();

    public UploadChoiceWindow(int fileCount, long totalBytes, bool cloudAllowed) : this()
    {
        SummaryText.Text =
            $"{fileCount} 个文件，共 {RecordItem.FormatBytes(totalBytes)}。请选择保存到服务器还是尝试 P2P 直连。";
        CloudButton.IsEnabled = cloudAllowed;
        HintText.Text = cloudAllowed
            ? "不超过阈值的文件可以保存到服务器，也可以选择 P2P。"
            : "总大小超过当前云端阈值，只能使用 P2P。";
    }

    private void Cloud_OnClick(object? sender, RoutedEventArgs e) => Close(UploadMode.Cloud);

    private void P2P_OnClick(object? sender, RoutedEventArgs e) => Close(UploadMode.PeerToPeer);

    private void Cancel_OnClick(object? sender, RoutedEventArgs e) => Close(null);
}

