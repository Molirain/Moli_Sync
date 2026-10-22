using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using MoliSync.Client.Sync;

namespace MoliSync.Client.Net;

/// <summary>
/// Reports the machine's private IPv4 addresses so other devices can attempt a
/// direct connection. Only LAN/VPN ranges are advertised.
/// </summary>
public static class LocalEndpoints
{
    public static IReadOnlyList<EndpointModel> Candidates(int port)
    {
        if (port is < 1 or > 65535)
        {
            return Array.Empty<EndpointModel>();
        }

        var results = new List<EndpointModel>();
        var seen = new HashSet<string>(StringComparer.Ordinal);

        foreach (var networkInterface in NetworkInterface.GetAllNetworkInterfaces())
        {
            if (networkInterface.OperationalStatus != OperationalStatus.Up ||
                networkInterface.NetworkInterfaceType == NetworkInterfaceType.Loopback)
            {
                continue;
            }

            var name = networkInterface.Name.ToLowerInvariant();
            var description = networkInterface.Description.ToLowerInvariant();
            var isVpn = networkInterface.NetworkInterfaceType == NetworkInterfaceType.Tunnel ||
                name.Contains("vpn") || name.Contains("tun") || name.Contains("tap") ||
                name.Contains("wireguard") || description.Contains("vpn");
            var network = isVpn ? EndpointNetworks.Vpn : EndpointNetworks.Lan;
            var priority = isVpn ? 50 : 100;

            IPInterfaceProperties properties;
            try
            {
                properties = networkInterface.GetIPProperties();
            }
            catch (NetworkInformationException)
            {
                continue;
            }

            foreach (var unicast in properties.UnicastAddresses)
            {
                if (unicast.Address.AddressFamily != AddressFamily.InterNetwork ||
                    IPAddress.IsLoopback(unicast.Address))
                {
                    continue;
                }
                if (!IsPrivate(unicast.Address))
                {
                    continue;
                }

                var ip = unicast.Address.ToString();
                if (!seen.Add(ip))
                {
                    continue;
                }
                results.Add(new EndpointModel
                {
                    Network = network,
                    Ip = ip,
                    Port = port,
                    Priority = priority,
                });
            }
        }

        return results
            .OrderByDescending(endpoint => endpoint.Priority)
            .ThenBy(endpoint => endpoint.Ip, StringComparer.Ordinal)
            .Take(8)
            .ToArray();
    }

    internal static bool IsPrivate(IPAddress address)
    {
        var bytes = address.GetAddressBytes();
        return bytes[0] switch
        {
            10 => true,
            172 => bytes[1] >= 16 && bytes[1] <= 31,
            192 => bytes[1] == 168,
            100 => bytes[1] >= 64 && bytes[1] <= 127,
            _ => false,
        };
    }
}
