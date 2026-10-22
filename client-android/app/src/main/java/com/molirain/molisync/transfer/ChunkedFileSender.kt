package com.molirain.molisync.transfer

import io.github.rctcwyvrn.blake3.Blake3
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import java.io.InputStream

/**
 * Streams one content:// backed file into data-plane frames without ever
 * copying the whole file into app storage. A single 1 MiB buffer is reused; only
 * a final partial chunk is copied so the BLAKE3 API receives an exact-sized
 * array.
 */
class ChunkedFileSender(private val chunkSize: Int = DEFAULT_CHUNK_SIZE) {

    suspend fun send(
        input: InputStream,
        file: TransferFile,
        fileIndex: Int,
        fileCount: Int,
        writer: DataPlaneFrameWriter,
        totalBytes: Long,
        bytesSentBeforeFile: Long,
        listener: TransferListener,
    ): CompletedFile {
        require(chunkSize > 0)

        val buffer = ByteArray(chunkSize)
        val fileHasher = Blake3.newInstance()
        var offset = 0L

        while (true) {
            currentCoroutineContext().ensureActive()
            val read = readFully(input, buffer)
            if (read == 0) {
                break
            }
            if (offset + read > file.size) {
                throw TransferException("${file.name} 的实际内容超过声明大小")
            }

            val hashInput = if (read == buffer.size) buffer else buffer.copyOf(read)
            val chunkHasher = Blake3.newInstance()
            chunkHasher.update(hashInput)
            val chunkHash = chunkHasher.digest()
            fileHasher.update(hashInput)

            writer.writeData(fileIndex, offset, chunkHash, buffer, read)
            writer.flush()

            offset += read
            listener.onProgress(
                TransferProgress(
                    fileIndex = fileIndex,
                    fileCount = fileCount,
                    fileName = file.name,
                    bytesSent = bytesSentBeforeFile + offset,
                    totalBytes = totalBytes,
                ),
            )
        }

        if (offset != file.size) {
            throw TransferException("${file.name} 在传输结束前被截断：$offset/${file.size} 字节")
        }

        val completed = CompletedFile(
            index = fileIndex,
            name = file.name,
            size = file.size,
            blake3 = fileHasher.hexdigest(),
        )
        listener.onFileCompleted(completed)
        return completed
    }

    /**
     * Fills the buffer up to its capacity or EOF. A single InputStream.read()
     * may return a short read even when more data is available.
     */
    private fun readFully(input: InputStream, buffer: ByteArray): Int {
        var total = 0
        while (total < buffer.size) {
            val read = input.read(buffer, total, buffer.size - total)
            if (read < 0) {
                break
            }
            if (read == 0) {
                val single = input.read()
                if (single < 0) {
                    break
                }
                buffer[total++] = single.toByte()
            } else {
                total += read
            }
        }
        return total
    }

    companion object {
        // 1 MiB keeps peak memory low on Android while amortizing frame overhead.
        const val DEFAULT_CHUNK_SIZE = 1 shl 20
    }
}
