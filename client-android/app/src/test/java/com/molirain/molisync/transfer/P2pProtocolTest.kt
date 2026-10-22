package com.molirain.molisync.transfer

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

class P2pProtocolTest {

    private val manifest = P2pManifest(
        sessionId = "session-1",
        ticket = "dGlja2V0",
        deviceId = "ab".repeat(32),
        deviceName = "书房电脑",
        files = listOf(P2pFileMeta("photo.png", 2048, "image/png")),
    )

    @Test
    fun `manifest round trip preserves every field`() {
        val output = ByteArrayOutputStream()
        P2pProtocol.writeManifest(output, manifest)

        val bytes = output.toByteArray()
        val length = ByteBuffer.wrap(bytes, 0, 4).order(ByteOrder.BIG_ENDIAN).int
        assertEquals(bytes.size - 4, length)

        val decoded = P2pProtocol.readManifest(ByteArrayInputStream(bytes))
        assertEquals(manifest, decoded)
    }

    @Test
    fun `manifest rejects invalid length prefixes`() {
        val zero = ByteBuffer.allocate(4).order(ByteOrder.BIG_ENDIAN).putInt(0).array()
        assertThrows(TransferException::class.java) {
            P2pProtocol.readManifest(ByteArrayInputStream(zero))
        }

        val oversized = ByteBuffer.allocate(4)
            .order(ByteOrder.BIG_ENDIAN)
            .putInt(P2pProtocol.MAX_MANIFEST_BYTES + 1)
            .array()
        assertThrows(TransferException::class.java) {
            P2pProtocol.readManifest(ByteArrayInputStream(oversized))
        }
    }

    @Test
    fun `frame round trip keeps header fields`() {
        val payload = byteArrayOf(1, 2, 3, 4)
        val output = ByteArrayOutputStream()
        val writer = DataPlaneFrameWriter(output)
        writer.writeData(2, 512, ByteArray(DataPlaneFrameWriter.HASH_SIZE) { 9 }, payload, payload.size)
        writer.flush()

        val frame = DataPlaneFrameReader(ByteArrayInputStream(output.toByteArray())).read()
        assertEquals(FrameType.DATA, frame.type)
        assertEquals(2, frame.fileIndex)
        assertEquals(512, frame.offset)
        assertEquals(9.toByte(), frame.hash[0])
        assertArrayEquals(payload, frame.payload)
    }

    @Test
    fun `reader rejects payloads above the frame limit`() {
        val header = ByteBuffer.allocate(DataPlaneFrameWriter.HEADER_SIZE).order(ByteOrder.BIG_ENDIAN)
        header.put(FrameType.DATA.toByte())
        header.putInt(0)
        header.putLong(0)
        header.putInt(DataPlaneFrameReader.MAX_PAYLOAD + 1)
        header.put(ByteArray(DataPlaneFrameWriter.HASH_SIZE))

        assertThrows(TransferException::class.java) {
            DataPlaneFrameReader(ByteArrayInputStream(header.array())).read()
        }
    }

    @Test
    fun `manifest validation rejects empty, negative and oversized files`() {
        assertThrows(TransferException::class.java) {
            manifest.copy(files = emptyList()).validate()
        }
        assertThrows(TransferException::class.java) {
            manifest.copy(files = listOf(P2pFileMeta("a.bin", -1))).validate()
        }
        assertThrows(TransferException::class.java) {
            manifest.copy(files = listOf(P2pFileMeta("", 1))).validate()
        }
        val half = P2pProtocol.MAX_TOTAL_BYTES / 2 + 1
        assertThrows(TransferException::class.java) {
            manifest.copy(
                files = listOf(P2pFileMeta("a.bin", half), P2pFileMeta("b.bin", half)),
            ).validate()
        }
    }
}
