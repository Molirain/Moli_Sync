using System.Collections.ObjectModel;
using Avalonia.Controls;
using Avalonia.Interactivity;
using MoliSync.Client.Services;
using MoliSync.Client.Sync;

namespace MoliSync.Client.Views;

public partial class PeerPickerWindow : Window
{
    private readonly ObservableCollection<DeviceCard> _cards = new();

    public PeerPickerWindow()
    {
        InitializeComponent();
        PeerList.ItemsSource = _cards;
    }

    public PeerPickerWindow(ShellViewModel shell) : this()
    {
        var online = shell.Peers
            .Select(peer => new DeviceCard(peer, isSelf: false))
            .ToArray();
        foreach (var card in online)
        {
            _cards.Add(card);
        }

        SummaryText.Text = online.Length == 0
            ? "没有在线设备"
            : $"{online.Length} 台设备在线";
        EmptyText.IsVisible = online.Length == 0;
        if (online.Length > 0)
        {
            PeerList.SelectedIndex = 0;
        }
    }

    public DeviceView? SelectedDevice =>
        (PeerList.SelectedItem as DeviceCard)?.Device;

    private void Cancel_OnClick(object? sender, RoutedEventArgs e) => Close(null);

    private void Send_OnClick(object? sender, RoutedEventArgs e)
    {
        if (SelectedDevice is null)
        {
            SummaryText.Text = "请先选择一台设备";
            return;
        }
        Close(SelectedDevice);
    }
}
