package com.molirain.molisync.transfer

import io.github.rctcwyvrn.blake3.Blake3
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

class ChunkedFileSenderTest {

    private data class Frame(
        val type: Int,
        val fileIndex: Int,
        val offset: Long,
        val length: Int,
        val hash: ByteArray,
        val payload: ByteArray,
    )

    private class RecordingListener : TransferListener {
        val progress = mutableListOf<TransferProgress>()
        val completed = mutableListOf<CompletedFile>()

        override fun onProgress(progress: TransferProgress) {
            this.progress += progress
        }

        override fun onFileCompleted(file: CompletedFile) {
            completed += file
        }
    }

    private fun blake3Hex(data: ByteArray): String =
        Blake3.newInstance().apply { update(data) }.hexdigest()

    private fun parseFrames(bytes: ByteArray): List<Frame> {
        val buffer = ByteBuffer.wrap(bytes).order(ByteOrder.BIG_ENDIAN)
        val frames = mutableListOf<Frame>()
        while (buffer.hasRemaining()) {
            val type = buffer.get().toInt() and 0xFF
            val fileIndex = buffer.int
            val offset = buffer.long
            val length = buffer.int
            val hash = ByteArray(DataPlaneFrameWriter.HASH_SIZE).also { buffer.get(it) }
            val payload = ByteArray(length).also { buffer.get(it) }
            frames += Frame(type, fileIndex, offset, length, hash, payload)
        }
        return frames
    }

    private fun send(
        content: ByteArray,
        declaredSize: Long = content.size.toLong(),
        chunkSize: Int = 4,
        fileIndex: Int = 0,
        fileCount: Int = 1,
        input: InputStream = ByteArrayInputStream(content),
    ): Pair<CompletedFile, List<Frame>> {
        val output = ByteArrayOutputStream()
        val listener = RecordingListener()
        val file = TransferFile("content://test/file", "a.txt", declaredSize)
        val completed = runBlocking {
            ChunkedFileSender(chunkSize).send(
                input = input,
                file = file,
                fileIndex = fileIndex,
                fileCount = fileCount,
                writer = DataPlaneFrameWriter(output),
                totalBytes = declaredSize,
                bytesSentBeforeFile = 0L,
                listener = listener,
            )
        }
        return completed to parseFrames(output.toByteArray())
    }

    @Test
    fun `empty file produces no data frames and the BLAKE3 empty vector`() {
        val (completed, frames) = send(ByteArray(0))

        assertEquals(0, frames.size)
        assertEquals(0L, completed.size)
        assertEquals(
            "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262",
            completed.blake3,
        )
    }

    @Test
    fun `hashes a single chunk with the BLAKE3 abc vector`() {
        val content = "abc".toByteArray()
        val (completed, frames) = send(content)

        assertEquals(1, frames.size)
        val frame = frames.single()
        assertEquals(FrameType.DATA, frame.type)
        assertEquals(0, frame.fileIndex)
        assertEquals(0L, frame.offset)
        assertEquals(content.size, frame.length)
        assertArrayEquals(content, frame.payload)
        assertArrayEquals(tinyBlake3(content), frame.hash)
        assertEquals(
            "6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85",
            completed.blake3,
        )
    }

    @Test
    fun `splits content into ordered chunks with per chunk hashes`() {
        val content = "abcdefghij".toByteArray()
        val (completed, frames) = send(content, fileIndex = 2, fileCount = 3)

        assertEquals(listOf(0L, 4L, 8L), frames.map { it.offset })
        assertEquals(listOf(4, 4, 2), frames.map { it.length })
        assertEquals(listOf(2, 2, 2), frames.map { it.fileIndex })
        assertEquals(content.toList(), frames.flatMap { it.payload.toList() })
        frames.forEach { frame ->
            assertArrayEquals(tinyBlake3(frame.payload), frame.hash)
        }
        assertEquals(content.size.toLong(), completed.size)
        assertEquals(2, completed.index)
        assertEquals(blake3Hex(content), completed.blake3)
    }

    @Test
    fun `reports progress and completion exactly once`() {
        val content = "abcdefghij".toByteArray()
        val output = ByteArrayOutputStream()
        val listener = RecordingListener()
        val file = TransferFile("content://test/file", "a.txt", content.size.toLong())

        runBlocking {
            ChunkedFileSender(4).send(
                input = ByteArrayInputStream(content),
                file = file,
                fileIndex = 0,
                fileCount = 1,
                writer = DataPlaneFrameWriter(output),
                totalBytes = 10,
                bytesSentBeforeFile = 5,
                listener = listener,
            )
        }

        assertEquals(listOf(9L, 13L, 15L), listener.progress.map { it.bytesSent })
        assertEquals(listOf(10L, 10L, 10L), listener.progress.map { it.totalBytes })
        assertEquals(1, listener.completed.size)
        assertEquals(blake3Hex(content), listener.completed.single().blake3)
    }

    @Test
    fun `reads across short InputStream reads without losing data`() {
        val content = "abcdefghij".toByteArray()
        val oneByteAtATime = object : InputStream() {
            private var position = 0

            override fun read(): Int =
                if (position < content.size) content[position++].toInt() and 0xFF else -1

            override fun read(b: ByteArray, off: Int, len: Int): Int {
                if (position >= content.size) {
                    return -1
                }
                b[off] = content[position++]
                return 1
            }
        }

        val (completed, frames) = send(content, input = oneByteAtATime)

        assertEquals(blake3Hex(content), completed.blake3)
        assertEquals(content.toList(), frames.flatMap { it.payload.toList() })
    }

    @Test
    fun `rejects a file that is shorter than declared`() {
        assertThrows(TransferException::class.java) {
            send("abc".toByteArray(), declaredSize = 5)
        }
    }

    @Test
    fun `rejects a file that is longer than declared`() {
        assertThrows(TransferException::class.java) {
            send("abc".toByteArray(), declaredSize = 2)
        }
    }

    @Test
    fun `rejects a non positive chunk size`() {
        assertThrows(IllegalArgumentException::class.java) {
            send("a".toByteArray(), chunkSize = 0)
        }
    }

    private fun tinyBlake3(data: ByteArray): ByteArray =
        Blake3.newInstance().apply { update(data) }.digest()
}
