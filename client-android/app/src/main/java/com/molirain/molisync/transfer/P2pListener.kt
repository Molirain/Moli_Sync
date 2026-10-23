package com.molirain.molisync.transfer

import android.content.Context
import android.util.Log
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.NetworkEndpoints
import com.molirain.molisync.data.SessionTicket
import com.molirain.molisync.data.cloud.MediaStoreWriter
import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.security.KeyChainException
import com.molirain.molisync.security.PinnedPeerTrustManager
import com.molirain.molisync.security.isCertificateFingerprint
import com.molirain.molisync.security.sha256Hex
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.io.BufferedInputStream
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.net.InetSocketAddress
import java.security.cert.X509Certificate
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.SSLServerSocket
import javax.net.ssl.SSLSocket

/**
 * Inbound half of the P2P data plane.
 *
 * The listener binds an ephemeral TLS port, advertises it through the control
 * plane, and only accepts a connection when the sender presents a server-issued
 * session ticket AND the peer certificate fingerprint matches the sender
 * DeviceID carried in that manifest.
 */
class P2pListener(
    private val context: Context,
    private val keyChain: KeyChainAdapter,
    private val settings: ClientSettings,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val pending = ConcurrentHashMap<String, SessionTicket>()
    private var serverSocket: SSLServerSocket? = null
    private var acceptJob: Job? = null

    @Volatile
    var port: Int = 0
        private set

    @Volatile
    var selfDeviceId: String? = null
        private set

    fun endpoints() = NetworkEndpoints.candidates(port)

    fun start() {
        if (acceptJob?.isActive == true) {
            return
        }
        val alias = settings.certificateAlias ?: return
        acceptJob = scope.launch {
            try {
                val loaded = keyChain.loadIdentity(alias)
                val context = keyChain.createServerSslContext(loaded, PinnedPeerTrustManager)
                val socket = context.serverSocketFactory.createServerSocket() as SSLServerSocket
                socket.reuseAddress = true
                socket.needClientAuth = true
                socket.bind(InetSocketAddress(0), 16)
                serverSocket = socket
                selfDeviceId = loaded.deviceId
                port = socket.localPort
                Log.i(TAG, "P2P listener on port $port for ${loaded.deviceId.take(12)}…")
                acceptLoop(socket)
            } catch (cancellation: kotlinx.coroutines.CancellationException) {
                throw cancellation
            } catch (keyError: KeyChainException) {
                Log.w(TAG, "P2P listener cannot load the certificate", keyError)
            } catch (failure: Exception) {
                Log.w(TAG, "P2P listener failed", failure)
            } finally {
                runCatching { serverSocket?.close() }
                serverSocket = null
                port = 0
            }
        }
    }

    fun stop() {
        runCatching { serverSocket?.close() }
        serverSocket = null
        acceptJob?.cancel()
        acceptJob = null
        pending.clear()
        port = 0
        selfDeviceId = null
    }

    fun register(ticket: SessionTicket) {
        if (ticket.isExpired) {
            return
        }
        pending[ticket.sessionId] = ticket
        if (pending.size > 32) {
            val now = System.currentTimeMillis()
            pending.entries.removeIf { it.value.isExpired || now - it.value.expiresAt > 0 }
        }
    }

    /**
     * Standby relay connection for a ticket addressed to this device. It waits
     * for the sender to arrive on the server relay; if a direct connection wins
     * first the standby socket simply times out.
     */
    fun openRelayStandby(ticket: SessionTicket, serverUrl: String) {
        val relayUrl = ticket.relayUrl ?: return
        val alias = settings.certificateAlias ?: return
        scope.launch {
            val streams = runCatching {
                RelayTransport.connect(context, keyChain, alias, serverUrl, relayUrl, ticket.ticket)
            }.getOrNull() ?: return@launch
            streams.use {
                runCatching { receive(streams, expectedSession = ticket) }
                    .onFailure { error -> Log.w(TAG, "relay receive failed", error) }
            }
        }
    }

    private suspend fun acceptLoop(socket: SSLServerSocket) {
        while (scope.isActive && !socket.isClosed) {
            val client = try {
                socket.accept() as SSLSocket
            } catch (failure: IOException) {
                if (socket.isClosed) {
                    return
                }
                Log.w(TAG, "P2P accept failed", failure)
                continue
            }
            scope.launch(Dispatchers.IO) {
                runCatching { handle(client) }
                    .onFailure { error -> Log.w(TAG, "P2P receive failed", error) }
            }
        }
    }

    private fun handle(socket: SSLSocket) {
        socket.use {
            it.soTimeout = HANDSHAKE_TIMEOUT_MS
            it.startHandshake()
            val peer = it.session.peerCertificates.firstOrNull() as? X509Certificate
                ?: throw TransferException("对端没有提供客户端证书")
            it.soTimeout = 0

            val manifest = P2pProtocol.readManifest(BufferedInputStream(it.inputStream, 64 * 1024))
            manifest.validate()

            val ticket = pending[manifest.sessionId]
                ?: throw TransferException("没有等待中的 P2P 会话")
            if (selfDeviceId != null && ticket.toDeviceId != selfDeviceId) {
                throw TransferException("P2P 会话不属于本机")
            }
            if (ticket.ticket != manifest.ticket) {
                throw TransferException("P2P 会话票据不匹配")
            }
            if (ticket.fromDeviceId != manifest.deviceId) {
                throw TransferException("P2P 发送方与票据不一致")
            }
            val pinned = manifest.deviceFingerprint.ifBlank { manifest.deviceId }
            if (!isCertificateFingerprint(pinned)) {
                throw TransferException("对端未提供可校验的证书指纹")
            }
            if (sha256Hex(peer.encoded) != pinned) {
                throw TransferException("对端证书指纹与会话声明不一致")
            }
            if (pending.remove(manifest.sessionId) == null) {
                throw TransferException("P2P 会话已被占用")
            }

            receiveOnSocket(it, manifest)
        }
    }

    private suspend fun receive(streams: RelayTransport.Streams, expectedSession: SessionTicket) {
        val input = BufferedInputStream(streams.input, 64 * 1024)
        val manifest = P2pProtocol.readManifest(input)
        manifest.validate()
        if (manifest.sessionId != expectedSession.sessionId || manifest.ticket != expectedSession.ticket) {
            throw TransferException("中继清单与票据不一致")
        }
        if (manifest.deviceId != expectedSession.fromDeviceId) {
            throw TransferException("中继发送方与票据不一致")
        }
        receiveFrames(input, streams.output, manifest)
    }

    private fun receiveOnSocket(socket: SSLSocket, manifest: P2pManifest) {
        receiveFrames(
            BufferedInputStream(socket.inputStream, 64 * 1024),
            socket.outputStream,
            manifest,
        )
    }

    private fun receiveFrames(
        input: java.io.InputStream,
        output: java.io.OutputStream,
        manifest: P2pManifest,
    ) {
        val sessionDir = File(context.cacheDir, "p2p/${manifest.sessionId}")
        if (!sessionDir.exists() && !sessionDir.mkdirs()) {
            throw TransferException("无法创建 P2P 接收目录")
        }

        val targets = manifest.files.mapIndexed { index, file ->
            File(sessionDir, "${index}-${MediaStoreWriter.sanitize(file.name)}")
        }
        val streams = targets.map { FileOutputStream(it) }
        val written = LongArray(manifest.files.size)
        var completed = 0

        manifest.files.forEachIndexed { index, file ->
            if (file.size == 0L) {
                // An empty file never produces a data frame, so it is complete on arrival.
                streams[index].close()
                completed++
            }
        }

        try {
            val reader = DataPlaneFrameReader(input)
            while (completed < manifest.files.size) {
                val frame = reader.read()
                when (frame.type) {
                    FrameType.DATA -> {
                        val index = frame.fileIndex
                        if (index !in manifest.files.indices) {
                            throw TransferException("P2P 分片索引不合法")
                        }
                        if (frame.offset != written[index]) {
                            throw TransferException("P2P 分片偏移不连续")
                        }
                        streams[index].write(frame.payload)
                        written[index] += frame.payload.size
                        if (written[index] > manifest.files[index].size) {
                            throw TransferException("P2P 文件超出声明大小")
                        }
                        if (written[index] == manifest.files[index].size) {
                            streams[index].close()
                            completed++
                        }
                    }

                    FrameType.ABORT -> {
                        val reason = frame.payload.toString(Charsets.UTF_8)
                        throw TransferException("发送方中止了传输：$reason")
                    }

                    else -> throw TransferException("未知的 P2P 分片类型")
                }
            }

            val ack = DataPlaneFrameWriter(output)
            manifest.files.indices.forEach { ack.writeAck(it) }
            ack.flush()
        } finally {
            streams.forEach { runCatching { it.close() } }
        }

        val saved = ArrayList<String>()
        manifest.files.forEachIndexed { index, file ->
            val source = targets[index]
            if (written[index] != file.size) {
                throw TransferException("${file.name} 接收不完整")
            }
            val published = runCatching {
                MediaStoreWriter.publish(
                    context = context,
                    source = source,
                    displayName = file.name,
                    mimeType = file.mimeType,
                    isImage = file.mimeType?.startsWith("image/") == true,
                )
            }.getOrElse { failure ->
                Log.w(TAG, "cannot publish ${file.name}", failure)
                MediaStoreWriter.Published(null, source.absolutePath)
            }
            saved.add(published.displayPath)
        }

        TransferNotifications.notify(
            context,
            TransferNotifications.result(
                context,
                success = true,
                message = "已接收 ${saved.size} 个文件：${saved.joinToString("、")}",
            ),
        )

        runCatching { sessionDir.deleteRecursively() }
        Log.i(TAG, "P2P receive complete: ${saved.joinToString()}")
    }

    companion object {
        private const val TAG = "MoliSyncP2p"
        private const val HANDSHAKE_TIMEOUT_MS = 30_000
    }
}
