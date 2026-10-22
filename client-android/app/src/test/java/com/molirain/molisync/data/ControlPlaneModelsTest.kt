package com.molirain.molisync.data

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ControlPlaneModelsTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `welcome envelope maps snake case fields and device views`() {
        val fingerprint = "ab".repeat(32)
        val envelope = json.decodeFromString<EnvelopeDto>(
            """
            {
              "type": "welcome",
              "protocol_version": 1,
              "device_id": "$fingerprint",
              "heartbeat_interval_ms": 20000,
              "devices": [
                {
                  "device_id": "$fingerprint",
                  "name": "Pixel",
                  "platform": "android",
                  "connections": 2,
                  "endpoints": [
                    {"network": "LAN", "ip": "192.168.1.5", "port": 47001},
                    {"network": "vpn", "ip": "fd00::5", "port": 47001, "priority": 5},
                    {"network": "wan", "ip": "10.0.0.1", "port": 47001},
                    {"network": "lan", "ip": "192.168.1.5", "port": 70000},
                    {"network": "lan", "ip": "example.com", "port": 47001}
                  ]
                }
              ]
            }
            """.trimIndent(),
        )

        assertEquals("welcome", envelope.type)
        assertEquals(1, envelope.protocolVersion)
        assertEquals(20_000L, envelope.heartbeatIntervalMs)
        val view = envelope.devices!!.single()
        assertEquals(2, view.connections)

        val target = view.toTarget()!!
        assertEquals(fingerprint, target.deviceId)
        assertEquals("Pixel", target.name)
        assertEquals("android", target.platform)
        assertEquals(listOf("lan", "vpn"), target.endpoints.map { it.network })
        assertEquals(listOf("192.168.1.5", "fd00::5"), target.endpoints.map { it.ip })
        assertEquals(listOf(100, 5), target.endpoints.map { it.priority })
    }

    @Test
    fun `device without an id is ignored`() {
        val view = json.decodeFromString<DeviceDto>(
            """{"device_id": "", "name": "anon"}""",
        )
        assertNull(view.toTarget())
    }

    @Test
    fun `missing optional device fields fall back to safe defaults`() {
        val view = json.decodeFromString<DeviceDto>(
            """{"device_id": "${"cd".repeat(32)}"}""",
        )

        val target = view.toTarget()!!

        assertEquals("未命名设备", target.name)
        assertEquals("unknown", target.platform)
        assertEquals(emptyList<String>(), target.endpoints.map { it.ip })
    }

    @Test
    fun `presence envelope state and error envelope fields are decoded`() {
        val presence = json.decodeFromString<EnvelopeDto>(
            """{"type":"presence","state":"online","device":{"device_id":"${"ef".repeat(32)}"}}""",
        )
        assertEquals("online", presence.state)
        assertEquals("ef".repeat(32), presence.device?.deviceId)

        val error = json.decodeFromString<EnvelopeDto>(
            """{"type":"error","code":"invalid_message","message":"bad"}""",
        )
        assertEquals("invalid_message", error.code)
        assertEquals("bad", error.message)
    }
}
