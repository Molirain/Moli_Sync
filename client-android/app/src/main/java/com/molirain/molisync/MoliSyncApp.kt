package com.molirain.molisync

import android.app.Application
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.ControlPlaneClient
import com.molirain.molisync.data.DeviceDirectory
import com.molirain.molisync.data.cloud.CloudRepository
import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.transfer.P2pListener
import com.molirain.molisync.transfer.TransferNotifications
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.launch

class MoliSyncApp : Application() {
    lateinit var settings: ClientSettings
        private set

    lateinit var keyChain: KeyChainAdapter
        private set

    lateinit var deviceDirectory: DeviceDirectory
        private set

    lateinit var controlPlane: ControlPlaneClient
        private set

    lateinit var cloudRepository: CloudRepository
        private set

    lateinit var p2pListener: P2pListener
        private set

    /**
     * Emits whenever another device publishes a record. The UI uses this as a
     * cheap invalidation signal and refreshes from the HTTP API.
     */
    val syncEvents = MutableSharedFlow<Unit>(
        extraBufferCapacity = 8,
        onBufferOverflow = BufferOverflow.DROP_OLDEST,
    )

    private val applicationScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    override fun onCreate() {
        super.onCreate()
        settings = ClientSettings(this)
        keyChain = KeyChainAdapter(this)
        deviceDirectory = DeviceDirectory()
        cloudRepository = CloudRepository(this, settings, keyChain)
        p2pListener = P2pListener(this, keyChain, settings)
        controlPlane = ControlPlaneClient(
            context = this,
            settings = settings,
            keyChain = keyChain,
            directory = deviceDirectory,
            scope = applicationScope,
            syncEvents = syncEvents,
            endpoints = { p2pListener.endpoints() },
        )
        TransferNotifications.createChannel(this)

        applicationScope.launch {
            controlPlane.sessionTickets.collect { ticket ->
                if (ticket.toDeviceId != p2pListener.selfDeviceId) {
                    return@collect
                }
                p2pListener.register(ticket)
                settings.serverUrl?.let { server ->
                    p2pListener.openRelayStandby(ticket, server)
                }
            }
        }

        if (settings.isConfigured) {
            startTransport()
        }
    }

    /** Starts the P2P listener and the control plane with the current identity. */
    fun startTransport() {
        p2pListener.start()
        controlPlane.ensureStarted()
    }

    /** Applies a new certificate or device name without restarting the app. */
    fun restartTransport() {
        p2pListener.stop()
        startTransport()
        controlPlane.restart()
    }
}
