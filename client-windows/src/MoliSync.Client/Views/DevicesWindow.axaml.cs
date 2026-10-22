using System.Collections.ObjectModel;
using System.ComponentModel;
using Avalonia.Controls;
using MoliSync.Client.Services;

namespace MoliSync.Client.Views;

public partial class DevicesWindow : Window
{
    private readonly ObservableCollection<DeviceCard> _cards = new();
    private ShellViewModel? _shell;

    public DevicesWindow()
    {
        InitializeComponent();
        DeviceList.ItemsSource = _cards;
    }

    public DevicesWindow(ShellViewModel shell) : this() => Attach(shell);

    public void Attach(ShellViewModel shell)
    {
        _shell = shell;
        shell.PropertyChanged += Shell_OnPropertyChanged;
        Closed += (_, _) => shell.PropertyChanged -= Shell_OnPropertyChanged;
        Reload();
    }

    private void Shell_OnPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName is nameof(ShellViewModel.Peers) or nameof(ShellViewModel.OnlineText))
        {
            Reload();
        }
    }

    private void Reload()
    {
        _cards.Clear();
        if (_shell is not { } shell)
        {
            return;
        }

        if (shell.SelfDevice is { } self)
        {
            _cards.Add(new DeviceCard(self, isSelf: true));
        }
        foreach (var peer in shell.Peers)
        {
            _cards.Add(new DeviceCard(peer, isSelf: false));
        }

        var peers = shell.Peers.Count;
        SummaryText.Text = peers == 0
            ? "只有本机在线"
            : $"{peers + (shell.SelfDevice is null ? 0 : 1)} 台设备在线（含本机）";
        EmptyText.IsVisible = peers == 0;
    }
}
