using System.Collections.ObjectModel;
using System.ComponentModel;
using System.Runtime.CompilerServices;
using MoliSync.Client.Sync;

namespace MoliSync.Client.Services;

public sealed class DeviceDirectory : INotifyPropertyChanged
{
    private readonly ObservableCollection<DeviceView> _peers = new();
    private DeviceView? _self;

    public event PropertyChangedEventHandler? PropertyChanged;

    public ReadOnlyObservableCollection<DeviceView> Peers => new(_peers);

    public DeviceView? Self => _self;

    public int OnlineCount => _peers.Count + (_self is null ? 0 : 1);

    public string OnlineText => OnlineCount switch
    {
        0 => "0 台在线",
        1 => "1 台在线",
        _ => $"{OnlineCount} 台在线",
    };

    public void Reset(DeviceView self, IEnumerable<DeviceView> peers)
    {
        _self = self;
        _peers.Clear();
        foreach (var peer in peers)
        {
            if (!string.Equals(peer.DeviceId, self.DeviceId, StringComparison.Ordinal))
            {
                _peers.Add(peer);
            }
        }
        NotifyAll();
    }

    public void Apply(PresenceUpdate update)
    {
        if (_self is not null &&
            string.Equals(update.Device.DeviceId, _self.DeviceId, StringComparison.Ordinal))
        {
            return;
        }

        var existing = _peers.FirstOrDefault(
            peer => string.Equals(peer.DeviceId, update.Device.DeviceId, StringComparison.Ordinal));
        switch (update.State)
        {
            case "offline":
                if (existing is not null)
                {
                    _peers.Remove(existing);
                }
                break;
            default:
                if (existing is null)
                {
                    _peers.Add(update.Device);
                }
                else
                {
                    var index = _peers.IndexOf(existing);
                    _peers[index] = update.Device;
                }
                break;
        }
        NotifyAll();
    }

    private void NotifyAll()
    {
        OnPropertyChanged(nameof(Self));
        OnPropertyChanged(nameof(OnlineCount));
        OnPropertyChanged(nameof(OnlineText));
    }

    private void OnPropertyChanged([CallerMemberName] string? name = null) =>
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
