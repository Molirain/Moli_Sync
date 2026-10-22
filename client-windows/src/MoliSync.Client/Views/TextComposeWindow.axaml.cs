using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;

namespace MoliSync.Client.Views;

public partial class TextComposeWindow : Window
{
    private const int MaximumLength = 64 * 1024;

    public TextComposeWindow()
    {
        InitializeComponent();
        Editor.TextChanged += (_, _) =>
        {
            var length = Editor.Text?.Length ?? 0;
            CountText.Text = $"{length} / {MaximumLength}";
            CountText.Foreground = length > MaximumLength
                ? Avalonia.Media.Brushes.Firebrick
                : Avalonia.Media.Brushes.Gray;
        };
    }

    private void Send_OnClick(object? sender, RoutedEventArgs e)
    {
        var text = Editor.Text ?? string.Empty;
        if (string.IsNullOrWhiteSpace(text))
        {
            return;
        }
        if (text.Length > MaximumLength)
        {
            CountText.Text = "文字超过 64 KiB，无法发送";
            return;
        }
        Close(text);
    }

    private void Cancel_OnClick(object? sender, RoutedEventArgs e) => Close(null);
}


