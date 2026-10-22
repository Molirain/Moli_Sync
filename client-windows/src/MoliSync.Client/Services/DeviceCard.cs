using MoliSync.Client.Sync;

namespace MoliSync.Client.Services;

public sealed record DeviceEndpointRow(string Network, string Address)
{
    public string Label => Network switch
    {
        EndpointNetworks.Lan => "局域网",
        EndpointNetworks.Vpn => "VPN",
        _ => Network,
    };
}

public sealed class DeviceCard
{
    public DeviceCard(DeviceView device, bool isSelf)
    {
        Device = device;
        IsSelf = isSelf;
        Endpoints = device.Endpoints
            .Where(endpoint => !string.IsNullOrWhiteSpace(endpoint.Ip))
            .Select(endpoint => new DeviceEndpointRow(
                endpoint.Network,
                endpoint.Port > 0 ? $"{endpoint.Ip}:{endpoint.Port}" : endpoint.Ip))
            .ToArray();
    }

    public DeviceView Device { get; }

    public bool IsSelf { get; }

    public string Name => string.IsNullOrWhiteSpace(Device.Name) ? "未命名设备" : Device.Name;

    public string NameWithBadge => IsSelf ? $"{Name}（本机）" : Name;

    public string Platform => Device.Platform switch
    {
        "android" => "Android",
        "windows" => "Windows",
        _ => string.IsNullOrWhiteSpace(Device.Platform) ? "未知平台" : Device.Platform,
    };

    public string DeviceId => Device.DeviceId;

    public IReadOnlyList<DeviceEndpointRow> Endpoints { get; }

    public bool HasEndpoints => Endpoints.Count > 0;

    public string EndpointSummary => HasEndpoints
        ? $"{Endpoints.Count} 个候选地址 · {string.Join(" / ", Endpoints.Take(2).Select(row => row.Address))}" +
          (Endpoints.Count > 2 ? " …" : string.Empty)
        : "未上报候选地址（对方可能未开放 P2P 监听）";

    public string Detail => $"{Platform} · {EndpointSummary}";

    public string ShortId => DeviceId.Length > 16 ? DeviceId[..16] + "…" : DeviceId;
}
