package com.molirain.molisync.transfer

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

class DataPlaneFrameWriterTest {

    @Test
    fun `writes a data frame with big endian header fields`() {
        val output = ByteArrayOutputStream()
        val writer = DataPlaneFrameWriter(output)
        val hash = ByteArray(DataPlaneFrameWriter.HASH_SIZE) { it.toByte() }
        val payload = "hello".toByteArray()

        writer.writeData(
            fileIndex = 7,
            offset = 1_000_000_000_123L,
            chunkHash = hash,
            payload = payload,
            payloadLength = payload.size,
        )

        val frame = ByteBuffer.wrap(output.toByteArray()).order(ByteOrder.BIG_ENDIAN)
        assertEquals(DataPlaneFrameWriter.HEADER_SIZE + payload.size, frame.remaining())
        assertEquals(FrameType.DATA.toByte(), frame.get())
        assertEquals(7, frame.int)
        assertEquals(1_000_000_000_123L, frame.long)
        assertEquals(payload.size, frame.int)
        val actualHash = ByteArray(DataPlaneFrameWriter.HASH_SIZE)
        frame.get(actualHash)
        assertArrayEquals(hash, actualHash)
        val actualPayload = ByteArray(payload.size)
        frame.get(actualPayload)
        assertArrayEquals(payload, actualPayload)
        assertEquals(0, frame.remaining())
    }

    @Test
    fun `payloadLength restricts how many payload bytes are written`() {
        val output = ByteArrayOutputStream()
        val writer = DataPlaneFrameWriter(output)
        val hash = ByteArray(DataPlaneFrameWriter.HASH_SIZE)

        writer.writeData(0, 0L, hash, "abcdef".toByteArray(), payloadLength = 3)

        val bytes = output.toByteArray()
        assertEquals(DataPlaneFrameWriter.HEADER_SIZE + 3, bytes.size)
        val frame = ByteBuffer.wrap(bytes).order(ByteOrder.BIG_ENDIAN)
        frame.position(13)
        assertEquals(3, frame.int)
        frame.position(DataPlaneFrameWriter.HEADER_SIZE)
        val payload = ByteArray(3)
        frame.get(payload)
        assertArrayEquals("abc".toByteArray(), payload)
    }

    @Test
    fun `rejects a chunk hash with the wrong length`() {
        val writer = DataPlaneFrameWriter(ByteArrayOutputStream())
        assertThrows(IllegalArgumentException::class.java) {
            writer.writeData(0, 0L, ByteArray(16), ByteArray(1), 1)
        }
    }

    @Test
    fun `rejects a payload length larger than the payload`() {
        val writer = DataPlaneFrameWriter(ByteArrayOutputStream())
        assertThrows(IllegalArgumentException::class.java) {
            writer.writeData(0, 0L, ByteArray(32), ByteArray(4), 5)
        }
    }

    @Test
    fun `abort frame uses a zero hash and truncates long reasons`() {
        val output = ByteArrayOutputStream()
        val writer = DataPlaneFrameWriter(output)

        writer.writeAbort(fileIndex = 3, reason = "x".repeat(2000))

        val bytes = output.toByteArray()
        assertEquals(DataPlaneFrameWriter.HEADER_SIZE + 1024, bytes.size)
        val frame = ByteBuffer.wrap(bytes).order(ByteOrder.BIG_ENDIAN)
        assertEquals(FrameType.ABORT.toByte(), frame.get())
        assertEquals(3, frame.int)
        assertEquals(0L, frame.long)
        assertEquals(1024, frame.int)
        repeat(DataPlaneFrameWriter.HASH_SIZE) {
            assertEquals(0, frame.get().toInt())
        }
        val reason = ByteArray(1024)
        frame.get(reason)
        assertArrayEquals("x".repeat(1024).toByteArray(), reason)
    }
}
