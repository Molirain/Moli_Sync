package com.molirain.molisync.transfer

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test

class TransferRequestTest {

    private val fingerprint = "ab".repeat(32)

    private fun validRequest() = TransferRequest(
        targetDeviceId = fingerprint,
        targetName = "Pixel 9",
        endpoints = listOf(
            TransferEndpoint(network = "lan", ip = "192.168.1.20", port = 47001, priority = 100),
            TransferEndpoint(network = "vpn", ip = "fd00::20", port = 47001),
        ),
        files = listOf(
            TransferFile(uri = "content://media/1", name = "photo.jpg", size = 2048, mimeType = "image/jpeg"),
        ),
    )

    @Test
    fun `valid request passes validation`() {
        validRequest().validate()
    }

    @Test
    fun `json round trip preserves every field`() {
        val original = validRequest().copy(sessionId = "session-1")

        val decoded = TransferRequest.fromJson(original.toJson())

        assertEquals(original, decoded)
        assertEquals("session-1", decoded.sessionId)
    }

    @Test
    fun `json tolerates unknown and absent optional fields`() {
        val json = """
            {
              "targetDeviceId": "$fingerprint",
              "targetName": "Pixel",
              "endpoints": [{"network": "lan", "ip": "10.0.0.2", "port": 9}],
              "files": [{"uri": "content://a", "name": "a.bin", "size": 1}],
              "futureField": 42
            }
        """.trimIndent()

        val request = TransferRequest.fromJson(json)

        assertNull(request.sessionId)
        assertEquals(0, request.endpoints.single().priority)
        assertNull(request.files.single().mimeType)
        request.validate()
    }

    @Test
    fun `rejects a device id that is not a certificate fingerprint`() {
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(targetDeviceId = "device-1").validate()
        }
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(targetDeviceId = "AB".repeat(32)).validate()
        }
    }

    @Test
    fun `rejects a target name with control characters`() {
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(targetName = "bad\u0000name").validate()
        }
    }

    @Test
    fun `rejects missing endpoints and unknown networks`() {
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(endpoints = emptyList()).validate()
        }
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(
                endpoints = listOf(TransferEndpoint("wan", "10.0.0.2", 1)),
            ).validate()
        }
    }

    @Test
    fun `rejects invalid endpoint ports and addresses`() {
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(
                endpoints = listOf(TransferEndpoint("lan", "10.0.0.2", 0)),
            ).validate()
        }
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(
                endpoints = listOf(TransferEndpoint("lan", "not-an-ip", 4000)),
            ).validate()
        }
    }

    @Test
    fun `rejects non content uris, unknown sizes and duplicate uris`() {
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(
                files = listOf(TransferFile("file:///tmp/a", "a", 1)),
            ).validate()
        }
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(
                files = listOf(TransferFile("content://a", "a", -1)),
            ).validate()
        }
        val duplicate = TransferFile("content://a", "a", 1)
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(files = listOf(duplicate, duplicate.copy(name = "b"))).validate()
        }
    }

    @Test
    fun `rejects more files than the protocol limit`() {
        val files = (0..100).map { index ->
            TransferFile("content://$index", "file-$index", 1)
        }
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(files = files).validate()
        }
    }

    @Test
    fun `rejects files above the transfer size cap`() {
        val oversized = TransferFile(
            uri = "content://huge",
            name = "huge.bin",
            size = (1L shl 40) + 1,
        )
        assertThrows(TransferValidationException::class.java) {
            validRequest().copy(files = listOf(oversized)).validate()
        }
    }
}
