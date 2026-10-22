package com.molirain.molisync

import android.app.Application
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.ControlPlaneClient
import com.molirain.molisync.data.DeviceDirectory
import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.transfer.TransferNotifications
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

class MoliSyncApp : Application() {
    lateinit var settings: ClientSettings
        private set

    lateinit var keyChain: KeyChainAdapter
        private set

    lateinit var deviceDirectory: DeviceDirectory
        private set

    lateinit var controlPlane: ControlPlaneClient
        private set

    private val applicationScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    override fun onCreate() {
        super.onCreate()
        settings = ClientSettings(this)
        keyChain = KeyChainAdapter(this)
        deviceDirectory = DeviceDirectory()
        controlPlane = ControlPlaneClient(
            context = this,
            settings = settings,
            keyChain = keyChain,
            directory = deviceDirectory,
            scope = applicationScope,
        )
        TransferNotifications.createChannel(this)
    }
}
