package com.molirain.molisync.transfer

import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.security.KeyChainException
import com.molirain.molisync.security.PeerFingerprintTrustManager
import com.molirain.molisync.security.sha256Hex
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.security.cert.X509Certificate
import java.util.Collections
import java.util.concurrent.atomic.AtomicBoolean
import javax.net.ssl.SSLSocket

class PeerSession(
    private val closeable: AutoCloseable,
    input: java.io.InputStream,
    output: java.io.OutputStream,
) : AutoCloseable {
    val input: java.io.InputStream = input
    val output: java.io.OutputStream = output

    constructor(socket: SSLSocket) : this(socket, socket.inputStream, socket.outputStream)

    override fun close() {
        runCatching { closeable.close() }
    }
}

private data class RacedSocket(val socket: Socket, val endpoint: TransferEndpoint)

/**
 * Races LAN/VPN candidates (LAN head start, VPN after 300 ms, 1.2 s deadline),
 * then upgrades the winning TCP socket to mutual TLS. The peer certificate is
 * pinned to the target DeviceID (SHA-256 fingerprint) received from the
 * authenticated control plane.
 */
class PeerConnector(
    private val keyChain: KeyChainAdapter,
    private val totalTimeoutMs: Long = 1_200,
    private val vpnStaggerMs: Long = 300,
    private val candidateStaggerMs: Long = 50,
    private val handshakeTimeoutMs: Int = 30_000,
) {

    suspend fun connect(request: TransferRequest, alias: String): PeerSession {
        val raced = raceCandidates(request.endpoints)
        try {
            val trustManager = PeerFingerprintTrustManager(request.pinFingerprint)
            val (sslContext, _) = withContext(Dispatchers.IO) {
                keyChain.createSslContext(alias, trustManager)
            }

            val sslSocket = sslContext.socketFactory.createSocket(
                raced.socket,
                raced.endpoint.ip,
                raced.endpoint.port,
                true,
            ) as SSLSocket

            try {
                sslSocket.useClientMode = true
                sslSocket.soTimeout = handshakeTimeoutMs
                // The peer identity is its client certificate, whose SAN may not
                // match the private LAN/VPN IP. Identity is enforced by the
                // fingerprint trust manager instead of hostname verification.
                sslSocket.sslParameters = sslSocket.sslParameters.apply {
                    endpointIdentificationAlgorithm = null
                }
                sslSocket.startHandshake()

                val peer = sslSocket.session.peerCertificates.firstOrNull() as? X509Certificate
                    ?: throw TransferException("对端未提供证书")
                if (sha256Hex(peer.encoded) != request.pinFingerprint) {
                    throw TransferException("对端证书指纹与目标设备不匹配")
                }
                sslSocket.soTimeout = 0
                return PeerSession(sslSocket)
            } catch (exception: Exception) {
                runCatching { sslSocket.close() }
                throw exception
            }
        } catch (exception: KeyChainException) {
            runCatching { raced.socket.close() }
            throw TransferException(exception.message ?: "客户端证书不可用", exception)
        } catch (exception: Exception) {
            runCatching { raced.socket.close() }
            throw exception
        }
    }

    private suspend fun raceCandidates(endpoints: List<TransferEndpoint>): RacedSocket = coroutineScope {
        val lan = endpoints.filter { it.network == "lan" }.sortedByDescending { it.priority }
        val vpn = endpoints.filter { it.network == "vpn" }.sortedByDescending { it.priority }
        if (lan.isEmpty() && vpn.isEmpty()) {
            throw TransferException("没有可用的 P2P 地址")
        }

        val winner = CompletableDeferred<RacedSocket>()
        val sockets = Collections.synchronizedList(mutableListOf<Socket>())
        val won = AtomicBoolean(false)
        val jobs = mutableListOf<Job>()

        fun closeSockets(keep: Socket? = null) {
            val snapshot = synchronized(sockets) { sockets.toList() }
            snapshot.forEach { socket ->
                if (socket !== keep) {
                    runCatching { socket.close() }
                }
            }
        }

        suspend fun probe(endpoint: TransferEndpoint, delayMs: Long) {
            delay(delayMs)
            val socket = Socket()
            sockets.add(socket)
            try {
                socket.tcpNoDelay = true
                socket.connect(
                    InetSocketAddress(InetAddress.getByName(endpoint.ip), endpoint.port),
                    totalTimeoutMs.toInt(),
                )
                if (won.compareAndSet(false, true)) {
                    if (!winner.complete(RacedSocket(socket, endpoint))) {
                        runCatching { socket.close() }
                    }
                } else {
                    runCatching { socket.close() }
                }
            } catch (_: Exception) {
                runCatching { socket.close() }
            }
        }

        lan.forEachIndexed { index, endpoint ->
            jobs += launch(Dispatchers.IO) {
                probe(endpoint, index * candidateStaggerMs)
            }
        }
        vpn.forEachIndexed { index, endpoint ->
            jobs += launch(Dispatchers.IO) {
                probe(endpoint, vpnStaggerMs + index * candidateStaggerMs)
            }
        }

        val result = try {
            withTimeoutOrNull(totalTimeoutMs) { winner.await() }
        } catch (cancellation: CancellationException) {
            won.set(true)
            closeSockets()
            throw cancellation
        } finally {
            jobs.forEach { it.cancel() }
        }

        if (result == null) {
            won.set(true)
            closeSockets()
            throw TransferException("P2P 直连超时，请重试或等待中继通道可用")
        }

        closeSockets(keep = result.socket)
        result
    }
}
