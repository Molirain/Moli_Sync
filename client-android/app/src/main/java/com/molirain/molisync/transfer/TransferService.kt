package com.molirain.molisync.transfer

import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.os.PowerManager
import android.util.Log
import androidx.annotation.RequiresApi
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.molirain.molisync.MoliSyncApp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

/**
 * Foreground data-sync service owning the transfer lifetime.
 *
 * Android 14 requirements implemented here:
 *  - manifest foregroundServiceType="dataSync"
 *  - FOREGROUND_SERVICE_DATA_SYNC permission
 *  - ServiceCompat.startForeground(..., FOREGROUND_SERVICE_TYPE_DATA_SYNC)
 *  - an ongoing notification with live progress and a cancel action
 *
 * Android 15 dataSync timeout is handled by onTimeout(), which cancels the
 * transfer and stops the service cleanly.
 */
class TransferService : Service() {

    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    @Volatile
    private var transferJob: Job? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private val descriptorLock = Any()
    private var activeDescriptors: List<ParcelFileDescriptor>? = null
    @Volatile
    private var timeoutReason: String? = null

    private val app: MoliSyncApp
        get() = application as MoliSyncApp

    override fun onCreate() {
        super.onCreate()
        TransferNotifications.createChannel(this)
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_CANCEL) {
            val job = transferJob
            if (job?.isActive == true) {
                cancelActiveTransfer()
            } else {
                // A stale notification action arrived after the transfer had
                // already finished; stop the non-foreground service.
                TransferNotifications.cancel(this)
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
            return START_NOT_STICKY
        }

        val descriptors = readDescriptors(intent)
        if (transferJob?.isActive == true) {
            closeDescriptors(descriptors)
            Log.w(TAG, "transfer already running; rejecting a second request")
            return START_NOT_STICKY
        }

        // startForegroundService() requires a foreground notification within
        // seconds. Post the preparing state before parsing anything.
        val preparing = TransferNotifications.preparing(this)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ServiceCompat.startForeground(
                this,
                TransferNotifications.NOTIFICATION_ID,
                preparing,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
            )
        } else {
            startForeground(TransferNotifications.NOTIFICATION_ID, preparing)
        }

        val rawRequest = intent?.getStringExtra(EXTRA_REQUEST)
        if (rawRequest == null) {
            closeDescriptors(descriptors)
            finishFailure("无效的传输请求")
            return START_NOT_STICKY
        }

        val request = runCatching { TransferRequest.fromJson(rawRequest) }
            .onFailure { Log.e(TAG, "cannot parse transfer request", it) }
            .getOrElse {
                closeDescriptors(descriptors)
                finishFailure("传输请求解析失败")
                return START_NOT_STICKY
            }

        val validation = runCatching { request.validate() }
        if (validation.isFailure) {
            closeDescriptors(descriptors)
            finishFailure(validation.exceptionOrNull()?.message ?: "传输请求不合法")
            return START_NOT_STICKY
        }
        if (descriptors.size != request.files.size) {
            closeDescriptors(descriptors)
            finishFailure("文件句柄数量与文件列表不一致")
            return START_NOT_STICKY
        }

        acquireWakeLock()
        timeoutReason = null
        synchronized(descriptorLock) {
            activeDescriptors = descriptors
        }
        transferJob = serviceScope.launch {
            try {
                runTransfer(request, descriptors)
            } finally {
                releaseDescriptors(descriptors)
            }
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        transferJob?.cancel()
        serviceScope.cancel()
        releaseWakeLock()
        val pending = synchronized(descriptorLock) {
            activeDescriptors.also { activeDescriptors = null }
        }
        if (pending != null) {
            closeDescriptors(pending)
        }
        super.onDestroy()
    }

    /**
     * Android 15 introduces a time limit for dataSync foreground services.
     * Stop cleanly instead of being force-stopped by the system.
     */
    @RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
    override fun onTimeout(startId: Int, fgsType: Int) {
        Log.w(TAG, "dataSync foreground service timed out")
        cancelActiveTransfer("系统限制了后台传输时长，请重新发起传输")
        super.onTimeout(startId, fgsType)
    }

    private suspend fun runTransfer(request: TransferRequest, descriptors: List<ParcelFileDescriptor>) {
        val alias = app.settings.certificateAlias
        if (alias == null) {
            finishFailure("尚未授权客户端证书")
            return
        }

        val throttler = ProgressThrottler()
        try {
            val engine = TransferEngine(app.keyChain)
            val summary = engine.run(
                request = request,
                alias = alias,
                descriptors = descriptors,
                listener = object : TransferListener {
                    override fun onProgress(progress: TransferProgress) {
                        val throttled = throttler.update(progress) ?: return
                        TransferNotifications.notify(
                            this@TransferService,
                            TransferNotifications.progress(
                                this@TransferService,
                                fileName = throttled.fileName,
                                sentBytes = throttled.bytesSent,
                                totalBytes = throttled.totalBytes,
                                bytesPerSecond = throttled.bytesPerSecond,
                            ),
                        )
                    }

                    override fun onFileCompleted(file: CompletedFile) {
                        Log.i(TAG, "file ${file.index} ${file.name} complete, blake3=${file.blake3}")
                    }
                },
            )

            val seconds = summary.elapsedMillis.coerceAtLeast(1) / 1000.0
            val average = (summary.totalBytes / seconds).toLong()
            finishSuccess(
                "已发送 ${summary.files.size} 个文件 · " +
                    "${TransferNotifications.formatBytes(summary.totalBytes)} · " +
                    "平均 ${TransferNotifications.formatSpeed(average)}",
            )
        } catch (cancellation: CancellationException) {
            val reason = timeoutReason
            if (reason != null) {
                finishFailure(reason)
            } else {
                finishCanceled()
            }
            throw cancellation
        } catch (exception: Exception) {
            Log.e(TAG, "transfer failed", exception)
            finishFailure(exception.message ?: "传输失败")
        }
    }

    private fun finishSuccess(message: String) {
        TransferNotifications.notify(this, TransferNotifications.result(this, true, message))
        detachForeground()
        releaseWakeLock()
        stopSelf()
    }

    private fun finishFailure(message: String) {
        TransferNotifications.notify(this, TransferNotifications.result(this, false, message))
        releaseWakeLock()
        stopForeground(STOP_FOREGROUND_DETACH)
        stopSelf()
    }

    private fun finishCanceled() {
        TransferNotifications.cancel(this)
        releaseWakeLock()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun detachForeground() {
        stopForeground(STOP_FOREGROUND_DETACH)
    }

    private fun releaseDescriptors(descriptors: List<ParcelFileDescriptor>) {
        synchronized(descriptorLock) {
            if (activeDescriptors === descriptors) {
                activeDescriptors = null
            }
        }
        closeDescriptors(descriptors)
    }

    /**
     * Cancels the transfer and closes the content descriptors so a blocking
     * provider read cannot keep the coroutine alive after the user cancels or
     * Android 15 times out the dataSync service.
     */
    private fun cancelActiveTransfer(reason: String? = null) {
        if (reason != null) {
            timeoutReason = reason
        }
        val job = transferJob
        if (job?.isActive == true) {
            job.cancel(CancellationException(reason ?: "user canceled"))
        }
        val descriptors = synchronized(descriptorLock) { activeDescriptors }
        if (descriptors != null) {
            closeDescriptors(descriptors)
        }
    }

    private fun acquireWakeLock() {
        if (wakeLock?.isHeld == true) {
            return
        }
        val powerManager = getSystemService(PowerManager::class.java)
        wakeLock = powerManager.newWakeLock(
            PowerManager.PARTIAL_WAKE_LOCK,
            "MoliSync:Transfer",
        ).apply {
            setReferenceCounted(false)
            acquire(MAX_WAKE_LOCK_MS)
        }
    }

    private fun releaseWakeLock() {
        val lock = wakeLock ?: return
        if (lock.isHeld) {
            lock.release()
        }
        wakeLock = null
    }

    companion object {
        private const val TAG = "MoliSyncTransfer"
        private const val EXTRA_REQUEST = "com.molirain.molisync.transfer.REQUEST"
        private const val EXTRA_DESCRIPTORS = "com.molirain.molisync.transfer.DESCRIPTORS"
        const val ACTION_CANCEL = "com.molirain.molisync.transfer.CANCEL"
        private const val MAX_WAKE_LOCK_MS = 60L * 60L * 1000L

        fun start(
            context: Context,
            request: TransferRequest,
            descriptors: List<ParcelFileDescriptor>,
        ) {
            require(descriptors.size == request.files.size) {
                "one file descriptor is required per shared file"
            }
            val intent = Intent(context, TransferService::class.java)
                .putExtra(EXTRA_REQUEST, request.toJson())
                .putExtra(EXTRA_DESCRIPTORS, descriptors.toTypedArray())
            ContextCompat.startForegroundService(context, intent)
        }

        @Suppress("DEPRECATION")
        private fun readDescriptors(intent: Intent?): List<ParcelFileDescriptor> =
            intent?.getParcelableArrayExtra(EXTRA_DESCRIPTORS)
                ?.mapNotNull { it as? ParcelFileDescriptor }
                .orEmpty()

        private fun closeDescriptors(descriptors: List<ParcelFileDescriptor>) {
            descriptors.forEach { descriptor ->
                runCatching { descriptor.close() }
            }
        }
    }
}
