package com.molirain.molisync.transfer

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.EOFException
import java.io.InputStream
import java.io.OutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

/**
 * Wire format for the direct data plane.
 *
 * After the TLS handshake the sender writes one manifest frame:
 *
 *   0 4B  manifest length, big endian
 *   4 NB  UTF-8 JSON manifest
 *
 * followed by the fixed 49-byte data frames defined in [DataPlaneFrameWriter].
 * The receiver answers with FILE_ACK frames once every file is complete.
 */
object P2pProtocol {
    const val MAGIC = "MOLISYNC/1"
    const val MAX_MANIFEST_BYTES = 64 * 1024
    const val MAX_FILES = 100
    const val MAX_TOTAL_BYTES = 1L shl 40

    private val json = Json {
        ignoreUnknownKeys = true
        encodeDefaults = true
        explicitNulls = false
    }

    fun writeManifest(output: OutputStream, manifest: P2pManifest) {
        val payload = json.encodeToString(P2pManifest.serializer(), manifest).toByteArray(Charsets.UTF_8)
        require(payload.size <= MAX_MANIFEST_BYTES) { "manifest is too large" }
        val header = ByteBuffer.allocate(4).order(ByteOrder.BIG_ENDIAN).putInt(payload.size).array()
        output.write(header)
        output.write(payload)
        output.flush()
    }

    fun readManifest(input: InputStream): P2pManifest {
        val header = readExactly(input, 4)
        val length = ByteBuffer.wrap(header).order(ByteOrder.BIG_ENDIAN).int
        if (length <= 0 || length > MAX_MANIFEST_BYTES) {
            throw TransferException("P2P 清单长度不合法")
        }
        val payload = readExactly(input, length)
        return json.decodeFromString(P2pManifest.serializer(), payload.toString(Charsets.UTF_8))
    }

    fun readExactly(input: InputStream, size: Int): ByteArray {
        val buffer = ByteArray(size)
        var read = 0
        while (read < size) {
            val count = input.read(buffer, read, size - read)
            if (count < 0) {
                throw EOFException("连接在对端发送完整数据前关闭")
            }
            read += count
        }
        return buffer
    }
}

@Serializable
data class P2pFileMeta(
    val name: String,
    val size: Long,
    val mimeType: String? = null,
)

@Serializable
data class P2pManifest(
    val version: Int = 1,
    val sessionId: String,
    val ticket: String,
    val deviceId: String,
    val deviceName: String = "",
    val files: List<P2pFileMeta>,
) {
    fun validate() {
        if (sessionId.isBlank() || sessionId.length > 128) {
            throw TransferException("P2P 会话 ID 不合法")
        }
        if (ticket.isBlank() || ticket.length > 128) {
            throw TransferException("P2P 会话票据不合法")
        }
        if (deviceId.isBlank() || deviceId.length > 128) {
            throw TransferException("P2P 发送方身份不合法")
        }
        if (files.isEmpty() || files.size > P2pProtocol.MAX_FILES) {
            throw TransferException("P2P 文件数量不合法")
        }
        var total = 0L
        files.forEach { file ->
            if (file.name.isBlank() || file.name.length > 255) {
                throw TransferException("P2P 文件名不合法")
            }
            if (file.size < 0 || file.size > P2pProtocol.MAX_TOTAL_BYTES) {
                throw TransferException("P2P 文件大小不合法")
            }
            if (total > P2pProtocol.MAX_TOTAL_BYTES - file.size) {
                throw TransferException("P2P 总大小超出上限")
            }
            total += file.size
        }
    }
}

/** Reads the fixed data-plane frames. */
class DataPlaneFrameReader(private val input: InputStream) {

    data class Frame(
        val type: Int,
        val fileIndex: Int,
        val offset: Long,
        val hash: ByteArray,
        val payload: ByteArray,
    )

    fun read(): Frame {
        val header = P2pProtocol.readExactly(input, DataPlaneFrameWriter.HEADER_SIZE)
        val buffer = ByteBuffer.wrap(header).order(ByteOrder.BIG_ENDIAN)
        val type = buffer.get().toInt() and 0xFF
        val fileIndex = buffer.int
        val offset = buffer.long
        val length = buffer.int
        if (length < 0 || length > MAX_PAYLOAD) {
            throw TransferException("P2P 分片长度不合法")
        }
        val hash = ByteArray(DataPlaneFrameWriter.HASH_SIZE)
        buffer.get(hash)
        val payload = if (length == 0) ByteArray(0) else P2pProtocol.readExactly(input, length)
        return Frame(type, fileIndex, offset, hash, payload)
    }

    companion object {
        const val MAX_PAYLOAD = 4 * 1024 * 1024
    }
}
