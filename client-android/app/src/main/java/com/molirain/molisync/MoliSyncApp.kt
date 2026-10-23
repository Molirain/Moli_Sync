package com.molirain.molisync

import android.app.Application
import com.molirain.molisync.data.AppUpdate
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.ControlPlaneClient
import com.molirain.molisync.data.DeviceDirectory
import com.molirain.molisync.data.UpdateChecker
import com.molirain.molisync.data.cloud.CloudRepository
import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.transfer.P2pListener
import com.molirain.molisync.transfer.TransferNotifications
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
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

    /** Server-rendered image previews, cached in memory and on disk. */
    lateinit var thumbnails: com.molirain.molisync.data.cloud.ThumbnailLoader
        private set

    lateinit var p2pListener: P2pListener
        private set

    /** A newer Android build that the user has not dismissed yet. */
    val pendingUpdate = MutableStateFlow<AppUpdate?>(null)

    private val updateChecker = UpdateChecker()

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
        thumbnails = com.molirain.molisync.data.cloud.ThumbnailLoader(
            directory = java.io.File(cacheDir, "thumbs"),
        ) { recordId -> runCatching { cloudRepository.thumbnail(recordId) }.getOrNull() }
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
            checkForUpdates(force = false)
        }
    }

    /**
     * Looks for a newer `android-v*` release. Results are cached for 24 hours so
     * repeated launches stay well inside the anonymous GitHub rate limit.
     */
    fun checkForUpdates(force: Boolean) {
        if (!settings.isConfigured) {
            return
        }
        val now = System.currentTimeMillis()
        val cachedVersion = settings.cachedUpdateVersion
        val cachedUrl = settings.cachedUpdateUrl

        if (!force && now - settings.updateCheckedAt < UPDATE_INTERVAL_MS) {
            pendingUpdate.value = if (
                cachedVersion != null &&
                cachedUrl != null &&
                UpdateChecker.compareVersions(cachedVersion, BuildConfig.VERSION_NAME) > 0 &&
                cachedVersion != settings.dismissedUpdateVersion
            ) {
                AppUpdate(cachedVersion, "", cachedUrl, null)
            } else {
                null
            }
            return
        }

        applicationScope.launch {
            val update = runCatching { updateChecker.check(BuildConfig.VERSION_NAME) }
            if (update.isSuccess) {
                val found = update.getOrNull()
                settings.saveUpdateResult(now, found)
                pendingUpdate.value = found?.takeIf { it.version != settings.dismissedUpdateVersion }
            }
        }
    }

    fun dismissUpdate(update: AppUpdate) {
        settings.dismissUpdate(update.version)
        pendingUpdate.value = null
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

    companion object {
        /** 24 hours between automatic update checks. */
        private const val UPDATE_INTERVAL_MS = 24L * 60L * 60L * 1000L
    }
}
