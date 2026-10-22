using Avalonia.Controls;
using Avalonia.Input.Platform;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public partial class TextDetailWindow : Window
{
    public TextDetailWindow()
    {
        InitializeComponent();
    }

    public TextDetailWindow(RecordItem item)
        : this()
    {
        ArgumentNullException.ThrowIfNull(item);
        BodyText.Text = item.Text;
        MetaText.Text = string.IsNullOrWhiteSpace(item.TextMeta)
            ? item.Subtitle
            : $"{item.Subtitle} · {item.TextMeta}";
    }

    private async void CopyAll_OnClick(object? sender, RoutedEventArgs e)
    {
        if (Clipboard is { } clipboard)
        {
            await clipboard.SetTextAsync(BodyText.Text ?? string.Empty);
        }
    }

    private void Close_OnClick(object? sender, RoutedEventArgs e) => Close();
}
