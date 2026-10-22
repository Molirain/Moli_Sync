package com.molirain.molisync.data

import java.net.Inet4Address
import java.net.NetworkInterface
import java.util.Collections

/**
 * Enumerates locally reachable addresses so peers can attempt a direct
 * connection without any external discovery service. Only private/link-local
 * IPv4 addresses are reported; public addresses and loopback are skipped.
 */
object NetworkEndpoints {

    data class LocalAddress(val network: String, val ip: String, val priority: Int)

    fun candidates(port: Int): List<EndpointDto> {
        if (port !in 1..65535) {
            return emptyList()
        }

        val seen = HashSet<String>()
        val result = ArrayList<LocalAddress>()
        val interfaces = runCatching {
            Collections.list(NetworkInterface.getNetworkInterfaces())
        }.getOrDefault(emptyList())

        for (iface in interfaces) {
            val up = runCatching { iface.isUp && !iface.isLoopback }.getOrDefault(false)
            if (!up) {
                continue
            }
            val name = iface.name.lowercase()
            val network = when {
                name.startsWith("tun") || name.startsWith("ppp") ||
                    name.startsWith("wg") || name.contains("vpn") -> "vpn"
                else -> "lan"
            }
            val priority = if (network == "lan") 100 else 50

            for (address in Collections.list(iface.inetAddresses)) {
                if (address !is Inet4Address || address.isLoopbackAddress) {
                    continue
                }
                if (!address.isSiteLocalAddress && !isCarrierGradeNat(address)) {
                    continue
                }
                val ip = address.hostAddress ?: continue
                if (!seen.add(ip)) {
                    continue
                }
                result.add(LocalAddress(network, ip, priority))
            }
        }

        return result
            .sortedWith(compareByDescending<LocalAddress> { it.priority }.thenBy { it.ip })
            .take(8)
            .map { EndpointDto(network = it.network, ip = it.ip, port = port, priority = it.priority) }
    }

    /**
     * 100.64.0.0/10 is used by Tailscale and several carrier/VPN setups but is
     * not reported as a site-local address by the JDK.
     */
    private fun isCarrierGradeNat(address: Inet4Address): Boolean {
        val octets = address.address
        return octets.size == 4 &&
            (octets[0].toInt() and 0xFF) == 100 &&
            (octets[1].toInt() and 0xC0) == 64
    }
}
