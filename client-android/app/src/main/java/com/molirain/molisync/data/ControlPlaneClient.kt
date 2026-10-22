package com.molirain.molisync.data

import android.content.Context
import android.util.Log
import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.security.KeyChainException
import com.molirain.molisync.security.MtlsClientFactory
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.encodeToString
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.io.IOException
import java.util.concurrent.atomic.AtomicLong
import kotlin.random.Random

sealed interface ConnectionState {
    data object NotConfigured : ConnectionState
    data object Connecting : ConnectionState
    data class Connected(val deviceId: String) : ConnectionState
    data class Error(val message: String) : ConnectionState
}

/**
 * Control-plane WebSocket used for device discovery. It reuses the KeyChain
 * mTLS client, registers the Android device and maintains the online device
 * table. Text/file sync itself is handled by other modules.
 */
class ControlPlaneClient(
    private val context: Context,
    private val settings: ClientSettings,
    private val keyChain: KeyChainAdapter,
    private val directory: DeviceDirectory,
    private val scope: CoroutineScope,
    private val syncEvents: MutableSharedFlow<Unit>? = null,
    private val endpoints: () -> List<EndpointDto> = { emptyList() },
) {
    private val _state = MutableStateFlow<ConnectionState>(ConnectionState.NotConfigured)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    private val _sessionTickets = MutableSharedFlow<SessionTicket>(
        extraBufferCapacity = 16,
        onBufferOverflow = kotlinx.coroutines.channels.BufferOverflow.DROP_OLDEST,
    )
    val sessionTickets: kotlinx.coroutines.flow.SharedFlow<SessionTicket> =
        _sessionTickets.asSharedFlow()

    @Volatile
    private var activeSocket: WebSocket? = null

    private val restartMutex = Mutex()
    private var sessionJob: Job? = null

    fun ensureStarted() {
        if (sessionJob?.isActive != true) {
            restart()
        }
    }

    fun restart() {
        scope.launch {
            restartMutex.withLock {
                sessionJob?.cancelAndJoin()
                directory.clear()

                val serverUrl = settings.serverUrl
                val alias = settings.certificateAlias
                if (serverUrl == null || alias == null) {
                    _state.value = ConnectionState.NotConfigured
                    return@withLock
                }

                _state.value = ConnectionState.Connecting
                sessionJob = scope.launch { runWithReconnect(serverUrl, alias) }
            }
        }
    }

    fun stop() {
        scope.launch {
            restartMutex.withLock {
                sessionJob?.cancelAndJoin()
                sessionJob = null
                directory.clear()
                _state.value = ConnectionState.NotConfigured
            }
        }
    }

    private suspend fun runWithReconnect(serverUrl: String, alias: String) {
        val mtls = try {
            withContext(Dispatchers.IO) {
                MtlsClientFactory.create(context, keyChain, alias)
            }
        } catch (cancellation: CancellationException) {
            throw cancellation
        } catch (keyChainError: KeyChainException) {
            // Re-authorization is required; retrying cannot fix it.
            _state.value = ConnectionState.Error(keyChainError.message ?: "证书不可用，请重新授权")
            return
        } catch (exception: Exception) {
            Log.e(TAG, "cannot initialize the mTLS client", exception)
            _state.value = ConnectionState.Error(exception.message ?: "无法初始化 TLS 客户端")
            return
        }

        try {
            var backoffMs = 1_000L
            while (true) {
                var wasWelcomed = false
                try {
                    wasWelcomed = runSession(mtls, serverUrl)
                } catch (cancellation: CancellationException) {
                    throw cancellation
                } catch (exception: Exception) {
                    Log.w(TAG, "control-plane session failed", exception)
                    _state.value = ConnectionState.Error(exception.message ?: "控制面连接失败")
                }

                directory.clear()
                if (wasWelcomed) {
                    backoffMs = 1_000L
                }
                delay(backoffMs + Random.nextLong(0, 500))
                backoffMs = (backoffMs * 2).coerceAtMost(30_000L)
            }
        } finally {
            // One OkHttpClient is reused across reconnect attempts; release its
            // dispatcher threads and idle TLS connections when this generation
            // of the session ends (restart/stop/certificate change).
            mtls.client.dispatcher.executorService.shutdown()
            mtls.client.connectionPool.evictAll()
        }
    }

    private suspend fun runSession(mtls: com.molirain.molisync.security.MtlsClient, serverUrl: String): Boolean =
        coroutineScope {
            val url = MtlsClientFactory.webSocketUrl(serverUrl)
            val welcomed = CompletableDeferred<Unit>()
            val closed = CompletableDeferred<Throwable?>()
            val heartbeatIntervalMs = AtomicLong(DEFAULT_HEARTBEAT_MS)

            val listener = object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    activeSocket = webSocket
                    val hello = HelloDto(
                        device = DeviceDescriptorDto(
                            name = settings.deviceName,
                            platform = "android",
                        ),
                        endpoints = endpoints(),
                    )
                    if (!webSocket.send(ProtocolJson.encodeToString(HelloDto.serializer(), hello))) {
                        welcomed.completeExceptionally(IOException("无法发送 hello"))
                        closed.complete(IOException("无法发送 hello"))
                    }
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    handleMessage(text, mtls.identity.deviceId, welcomed, heartbeatIntervalMs)
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, reason)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    val error = if (welcomed.isCompleted) null else IOException("服务器关闭了连接: $code $reason")
                    welcomed.completeExceptionally(error ?: IOException("连接已关闭"))
                    closed.complete(null)
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    welcomed.completeExceptionally(t)
                    closed.complete(t)
                }
            }

            val request = Request.Builder().url(url).build()
            val webSocket = mtls.client.newWebSocket(request, listener)

            try {
                withTimeout(WELCOME_TIMEOUT_MS) { welcomed.await() }
                _state.value = ConnectionState.Connected(mtls.identity.deviceId)

                val heartbeatJob = launch {
                    while (isActive) {
                        delay(heartbeatIntervalMs.get())
                        val payload = runCatching {
                            ProtocolJson.encodeToString(
                                HeartbeatDto.serializer(),
                                HeartbeatDto(endpoints = endpoints()),
                            )
                        }.getOrElse { HEARTBEAT_JSON }
                        if (!webSocket.send(payload)) {
                            break
                        }
                    }
                }

                try {
                    closed.await()?.let { throw it }
                } finally {
                    heartbeatJob.cancel()
                    webSocket.close(1000, "client session ended")
                }
                true
            } catch (timeout: TimeoutCancellationException) {
                webSocket.cancel()
                throw IOException("控制面在 ${WELCOME_TIMEOUT_MS / 1000} 秒内未返回 welcome", timeout)
            } finally {
                activeSocket = null
                webSocket.cancel()
            }
        }

    private fun handleMessage(
        text: String,
        selfDeviceId: String,
        welcomed: CompletableDeferred<Unit>,
        heartbeatIntervalMs: AtomicLong,
    ) {
        val envelope = runCatching {
            ProtocolJson.decodeFromString(EnvelopeDto.serializer(), text)
        }.onFailure { Log.w(TAG, "invalid control frame", it) }.getOrNull() ?: return

        when (envelope.type) {
            "welcome" -> {
                directory.replaceAll(
                    envelope.devices
                        ?.mapNotNull { it.toTarget() }
                        ?.filter { it.deviceId != selfDeviceId }
                        .orEmpty(),
                )
                directory.setSelf(
                    DeviceTarget(
                        deviceId = selfDeviceId,
                        name = settings.deviceName,
                        platform = "android",
                        endpoints = emptyList(),
                    ),
                )

                envelope.heartbeatIntervalMs?.let {
                    heartbeatIntervalMs.set(it.coerceIn(MIN_HEARTBEAT_MS, MAX_HEARTBEAT_MS))
                }
                _state.value = ConnectionState.Connected(selfDeviceId)
                welcomed.complete(Unit)
            }

            "presence" -> {
                val device = envelope.device ?: return
                if (device.deviceId == selfDeviceId) {
                    return
                }
                when (envelope.state) {
                    "online", "updated" -> device.toTarget()?.let(directory::upsert)
                    "offline" -> directory.remove(device.deviceId)
                }
            }

            "session_ticket" -> {
                val sessionId = envelope.sessionId
                val ticket = envelope.ticket
                val from = envelope.fromDeviceId
                val to = envelope.toDeviceId
                if (sessionId.isNullOrBlank() || ticket.isNullOrBlank() || from.isNullOrBlank() || to.isNullOrBlank()) {
                    Log.w(TAG, "ignored malformed session_ticket")
                    return
                }
                val targets = envelope.devices.orEmpty().mapNotNull { it.toTarget() }
                _sessionTickets.tryEmit(
                    SessionTicket(
                        sessionId = sessionId,
                        ticket = ticket,
                        fromDeviceId = from,
                        toDeviceId = to,
                        devices = targets,
                        relayUrl = envelope.relayUrl,
                        expiresAt = envelope.expiresAt ?: 0L,
                        transferName = null,
                    ),
                )
            }

            "error" -> Log.w(TAG, "server error ${envelope.code}: ${envelope.message}")
            "heartbeat_ack", "pong" -> Unit
            "sync_record" -> syncEvents?.tryEmit(Unit)
            else -> Log.d(TAG, "ignored control message ${envelope.type}")
        }
    }

    /** Asks the server for a transfer grant to [toDeviceId]. */
    fun requestSession(toDeviceId: String, transferName: String?): Boolean {
        val socket = activeSocket ?: return false
        val payload = runCatching {
            ProtocolJson.encodeToString(
                OpenSessionDto.serializer(),
                OpenSessionDto(toDeviceId = toDeviceId, transferName = transferName),
            )
        }.getOrNull() ?: return false
        return socket.send(payload)
    }

    /**
     * Requests a session and waits for the server-issued ticket that belongs to
     * this device as the sender.
     */
    suspend fun openSession(
        toDeviceId: String,
        transferName: String?,
        timeoutMs: Long = 8_000,
    ): SessionTicket {
        val selfId = (state.value as? ConnectionState.Connected)?.deviceId
            ?: throw IOException("控制面尚未连接")
        if (!requestSession(toDeviceId, transferName)) {
            throw IOException("控制面未连接，无法协商 P2P 会话")
        }
        return withTimeout(timeoutMs) {
            sessionTickets.first { it.fromDeviceId == selfId && it.toDeviceId == toDeviceId }
        }
    }

    companion object {
        private const val TAG = "MoliSyncControl"
        private const val WELCOME_TIMEOUT_MS = 30_000L
        private const val DEFAULT_HEARTBEAT_MS = 25_000L
        private const val MIN_HEARTBEAT_MS = 5_000L
        private const val MAX_HEARTBEAT_MS = 300_000L
        private val HEARTBEAT_JSON =
            ProtocolJson.encodeToString(HeartbeatDto.serializer(), HeartbeatDto())
    }
}
