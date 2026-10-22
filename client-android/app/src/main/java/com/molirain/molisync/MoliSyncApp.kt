package com.molirain.molisync

import android.app.Application
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.ControlPlaneClient
import com.molirain.molisync.data.DeviceDirectory
import com.molirain.molisync.data.cloud.CloudRepository
import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.transfer.TransferNotifications
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow

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
        controlPlane = ControlPlaneClient(
            context = this,
            settings = settings,
            keyChain = keyChain,
            directory = deviceDirectory,
            scope = applicationScope,
            syncEvents = syncEvents,
        )
        TransferNotifications.createChannel(this)
    }
}
