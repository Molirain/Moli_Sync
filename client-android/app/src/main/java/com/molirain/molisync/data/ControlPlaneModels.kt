package com.molirain.molisync.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import com.molirain.molisync.security.isCertificateFingerprint
import java.net.InetAddress

internal val ProtocolJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    explicitNulls = false
}

@Serializable
data class EndpointDto(
    val network: String = "",
    val ip: String = "",
    val port: Int = 0,
    val priority: Int = 0,
)

@Serializable
data class DeviceDescriptorDto(
    val name: String,
    val platform: String = "android",
)

@Serializable
data class HelloDto(
    val type: String = "hello",
    val device: DeviceDescriptorDto,
    val endpoints: List<EndpointDto> = emptyList(),
)

@Serializable
data class HeartbeatDto(
    val type: String = "heartbeat",
    val endpoints: List<EndpointDto> = emptyList(),
)

@Serializable
data class DeviceDto(
    @SerialName("device_id") val deviceId: String,
    val name: String = "",
    val platform: String = "",
    val endpoints: List<EndpointDto> = emptyList(),
    val connections: Int = 0,
    val fingerprint: String = "",
) {
    /**
     * Digest used to pin the peer certificate. Server versions before 0.5 only
     * sent the fingerprint as the DeviceID, so fall back to it.
     */
    val pinFingerprint: String
        get() = fingerprint.ifBlank {
            if (isCertificateFingerprint(deviceId)) deviceId else ""
        }
}

@Serializable
data class EnvelopeDto(
    val type: String,
    @SerialName("protocol_version") val protocolVersion: Int? = null,
    @SerialName("device_id") val deviceId: String? = null,
    @SerialName("heartbeat_interval_ms") val heartbeatIntervalMs: Long? = null,
    val devices: List<DeviceDto>? = null,
    val state: String? = null,
    val device: DeviceDto? = null,
    val code: String? = null,
    val message: String? = null,
    @SerialName("session_id") val sessionId: String? = null,
    val ticket: String? = null,
    @SerialName("from_device_id") val fromDeviceId: String? = null,
    @SerialName("to_device_id") val toDeviceId: String? = null,
    @SerialName("relay_url") val relayUrl: String? = null,
    @SerialName("expires_at") val expiresAt: Long? = null,
)

/** Server-issued transfer grant shared by both participants. */
data class SessionTicket(
    val sessionId: String,
    val ticket: String,
    val fromDeviceId: String,
    val toDeviceId: String,
    val devices: List<DeviceTarget>,
    val relayUrl: String?,
    val expiresAt: Long,
    val transferName: String?,
) {
    val isExpired: Boolean
        get() = expiresAt > 0 && System.currentTimeMillis() > expiresAt
}

@Serializable
internal data class OpenSessionDto(
    val type: String = "open_session",
    @SerialName("to_device_id") val toDeviceId: String,
    @SerialName("transfer_name") val transferName: String? = null,
)

data class DeviceEndpoint(
    val network: String,
    val ip: String,
    val port: Int,
    val priority: Int,
)

data class DeviceTarget(
    val deviceId: String,
    val name: String,
    val platform: String,
    val endpoints: List<DeviceEndpoint>,
    val fingerprint: String = "",
) {
    /** Certificate digest used for pinning; empty when the peer is unverifiable. */
    val pinFingerprint: String
        get() = fingerprint.ifBlank {
            if (isCertificateFingerprint(deviceId)) deviceId else ""
        }
}

private val IPV4 = Regex("""^(\d{1,3}\.){3}\d{1,3}$""")
private val IPV6 = Regex("""^[0-9a-fA-F:]+$""")

internal fun isNumericIp(value: String): Boolean {
    if (!IPV4.matches(value) && !(value.contains(':') && IPV6.matches(value))) {
        return false
    }
    return runCatching { InetAddress.getByName(value) }.isSuccess
}

fun DeviceDto.toTarget(): DeviceTarget? {
    if (deviceId.isBlank()) {
        return null
    }

    val normalizedEndpoints = endpoints.mapNotNull { endpoint ->
        val network = endpoint.network.lowercase()
        if (network != "lan" && network != "vpn") {
            return@mapNotNull null
        }
        if (endpoint.port !in 1..65535 || !isNumericIp(endpoint.ip)) {
            return@mapNotNull null
        }
        val priority = if (endpoint.priority > 0) {
            endpoint.priority
        } else if (network == "lan") {
            100
        } else {
            50
        }
        DeviceEndpoint(network, endpoint.ip, endpoint.port, priority)
    }.sortedWith(compareByDescending<DeviceEndpoint> { it.priority }.thenBy { it.network })

    return DeviceTarget(
        deviceId = deviceId,
        name = name.ifBlank { "未命名设备" }.take(64),
        platform = platform.ifBlank { "unknown" },
        endpoints = normalizedEndpoints,
        fingerprint = pinFingerprint,
    )
}
