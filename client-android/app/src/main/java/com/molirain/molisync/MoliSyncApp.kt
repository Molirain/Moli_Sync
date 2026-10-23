package com.molirain.molisync

import android.app.Application
import com.molirain.molisync.data.AppUpdate
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.ConnectionState
import com.molirain.molisync.data.ControlPlaneClient
import com.molirain.molisync.data.DeviceDirectory
import com.molirain.molisync.data.UpdateChecker
import com.molirain.molisync.data.UpdateCheckState
import com.molirain.molisync.data.UpdatePolicy
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

    /** Progress and outcome of the release lookup, shown in the settings page. */
    val updateCheckState = MutableStateFlow<UpdateCheckState>(UpdateCheckState.Idle)

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
        // The server may name this device after its certificate common name,
        // while the ticket always carries the server-assigned DeviceID.
        p2pListener.useServerDeviceId {
            (controlPlane.state.value as? ConnectionState.Connected)?.deviceId
        }

        applicationScope.launch {
            controlPlane.sessionTickets.collect { ticket ->
                if (!p2pListener.isForSelf(ticket)) {
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
     * Looks for a newer `android-v*` release. Automatic checks run at most once
     * per [UpdatePolicy.CHECK_INTERVAL_MS]; [force] bypasses the cache so the
     * settings button always asks GitHub again and reports a visible result.
     */
    fun checkForUpdates(force: Boolean) {
        val now = System.currentTimeMillis()
        val cachedVersion = settings.cachedUpdateVersion
        val cachedUrl = settings.cachedUpdateUrl

        if (!UpdatePolicy.shouldRefresh(force, now, settings.updateCheckedAt)) {
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

        if (updateCheckState.value == UpdateCheckState.Checking) {
            return
        }
        updateCheckState.value = UpdateCheckState.Checking

        applicationScope.launch {
            val result = runCatching { updateChecker.check(BuildConfig.VERSION_NAME) }
            val checkedAt = System.currentTimeMillis()
            result.onSuccess { found ->
                settings.saveUpdateResult(checkedAt, found)
                if (found == null) {
                    updateCheckState.value = UpdateCheckState.UpToDate(BuildConfig.VERSION_NAME)
                    pendingUpdate.value = null
                } else {
                    updateCheckState.value = UpdateCheckState.Available(found)
                    pendingUpdate.value = found.takeIf { it.version != settings.dismissedUpdateVersion }
                }
            }.onFailure { failure ->
                // A failed lookup must not consume the interval, otherwise a
                // flight-mode launch would hide updates for hours.
                updateCheckState.value = UpdateCheckState.Failed(
                    failure.message ?: "无法连接 GitHub",
                )
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
}
