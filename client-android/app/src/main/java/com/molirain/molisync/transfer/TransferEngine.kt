package com.molirain.molisync.transfer

import android.os.ParcelFileDescriptor
import com.molirain.molisync.security.KeyChainAdapter
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Orchestrates a full send:
 *   PFD -> reusable 1 MiB buffer -> BLAKE3 -> framed TLS stream
 *
 * File descriptors are opened by ShareActivity while its temporary URI grant is
 * valid and handed to the service. The engine reads those descriptors directly;
 * nothing is copied into app storage.
 */
class TransferEngine(
    private val keyChain: KeyChainAdapter,
    private val connector: PeerConnector = PeerConnector(keyChain),
) {
    suspend fun run(
        request: TransferRequest,
        alias: String,
        descriptors: List<ParcelFileDescriptor>,
        listener: TransferListener,
    ): TransferSummary = withContext(Dispatchers.IO) {
        request.validate()
        if (descriptors.size != request.files.size) {
            throw TransferException("文件句柄数量与文件列表不一致")
        }

        val startedAt = System.nanoTime()
        val totalBytes = request.files.sumOf { it.size }
        var bytesSent = 0L
        var currentIndex = 0
        val completed = ArrayList<CompletedFile>(request.files.size)

        connector.connect(request, alias).use { session ->
            // Blocking socket writes do not react to coroutine cancellation.
            // Closing the session from a sibling coroutine unblocks them when
            // the user cancels or Android 15's dataSync timeout fires.
            val closeOnCancellation = launch {
                try {
                    awaitCancellation()
                } finally {
                    session.close()
                }
            }

            val writer = DataPlaneFrameWriter(session.output)
            val sender = ChunkedFileSender()

            try {
                request.files.forEachIndexed { index, file ->
                    currentIndex = index
                    val descriptor = descriptors[index]
                    val statSize = descriptor.statSize
                    if (statSize >= 0 && statSize != file.size) {
                        throw TransferException("${file.name} 的大小在分享后发生变化")
                    }

                    ParcelFileDescriptor.AutoCloseInputStream(descriptor).use { stream ->
                        val completedFile = sender.send(
                            input = stream,
                            file = file,
                            fileIndex = index,
                            fileCount = request.files.size,
                            writer = writer,
                            totalBytes = totalBytes,
                            bytesSentBeforeFile = bytesSent,
                            listener = listener,
                        )
                        completed.add(completedFile)
                        bytesSent += completedFile.size
                    }
                }

                writer.flush()
            } catch (cancellation: CancellationException) {
                runCatching { writer.writeAbort(currentIndex, "canceled") }
                runCatching { writer.flush() }
                throw cancellation
            } catch (failure: Throwable) {
                runCatching { writer.writeAbort(currentIndex, failure.message ?: "transfer failed") }
                runCatching { writer.flush() }
                if (!currentCoroutineContext().isActive) {
                    throw CancellationException("transfer canceled", failure)
                }
                throw failure
            } finally {
                closeOnCancellation.cancel()
            }
        }

        val elapsedMillis = (System.nanoTime() - startedAt) / 1_000_000
        TransferSummary(completed, totalBytes, elapsedMillis)
    }
}
