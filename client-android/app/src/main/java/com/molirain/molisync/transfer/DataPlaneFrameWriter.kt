package com.molirain.molisync.transfer

import java.io.OutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

object FrameType {
    const val DATA = 1
    const val RESUME_REQUEST = 2
    const val FILE_ACK = 3
    const val ABORT = 255
}

/**
 * Fixed data-plane frame, matching proto/sync.proto:
 *
 *   0  1B  frame type
 *   1  4B  file index, big endian
 *   5  8B  offset, big endian
 *   13 4B  payload length, big endian
 *   17 32B BLAKE3-256 of the payload (zero for control frames)
 *   49 NB  payload
 */
class DataPlaneFrameWriter(private val output: OutputStream) {
    private val header = ByteBuffer.allocate(HEADER_SIZE).order(ByteOrder.BIG_ENDIAN)
    private val zeroHash = ByteArray(HASH_SIZE)

    fun writeData(
        fileIndex: Int,
        offset: Long,
        chunkHash: ByteArray,
        payload: ByteArray,
        payloadLength: Int,
    ) {
        require(chunkHash.size == HASH_SIZE) { "chunk hash must be 32 bytes" }
        require(payloadLength in 0..payload.size) { "invalid payload length" }
        writeFrame(FrameType.DATA, fileIndex, offset, chunkHash, payload, payloadLength)
    }

    /** Receiver acknowledgement for a fully written file. */
    fun writeAck(fileIndex: Int) {
        writeFrame(FrameType.FILE_ACK, fileIndex, 0L, zeroHash, EMPTY_PAYLOAD, 0)
    }

    fun writeAbort(fileIndex: Int, reason: String) {
        val encoded = reason.toByteArray(Charsets.UTF_8)
        val payload = if (encoded.size <= MAX_ABORT_REASON) encoded else encoded.copyOf(MAX_ABORT_REASON)
        writeFrame(FrameType.ABORT, fileIndex, 0L, zeroHash, payload, payload.size)
    }

    fun flush() {
        output.flush()
    }

    private fun writeFrame(
        type: Int,
        fileIndex: Int,
        offset: Long,
        hash: ByteArray,
        payload: ByteArray,
        payloadLength: Int,
    ) {
        header.clear()
        header.put(type.toByte())
        header.putInt(fileIndex)
        header.putLong(offset)
        header.putInt(payloadLength)
        header.put(hash, 0, HASH_SIZE)
        output.write(header.array(), 0, HEADER_SIZE)
        if (payloadLength > 0) {
            output.write(payload, 0, payloadLength)
        }
    }

    companion object {
        const val HEADER_SIZE = 49
        const val HASH_SIZE = 32
        private const val MAX_ABORT_REASON = 1024
        private val EMPTY_PAYLOAD = ByteArray(0)
    }
}
