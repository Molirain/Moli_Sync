package com.molirain.molisync.transfer

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.molirain.molisync.MainActivity
import com.molirain.molisync.R
import java.util.Locale

object TransferNotifications {
    const val CHANNEL_ID = "molisync_transfers"
    const val NOTIFICATION_ID = 4101

    fun createChannel(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java)
        val channel = NotificationChannel(
            CHANNEL_ID,
            context.getString(R.string.notification_channel_transfers),
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = context.getString(R.string.notification_channel_description)
            setShowBadge(false)
            setSound(null, null)
            enableVibration(false)
        }
        manager.createNotificationChannel(channel)
    }

    fun preparing(context: Context): Notification =
        baseBuilder(context)
            .setContentTitle("MoliSync 正在准备传输")
            .setContentText("正在校验文件与证书…")
            .setProgress(0, 0, true)
            .setOngoing(true)
            .build()

    fun progress(
        context: Context,
        fileName: String,
        sentBytes: Long,
        totalBytes: Long,
        bytesPerSecond: Long,
    ): Notification {
        val percent = if (totalBytes <= 0) 0 else ((sentBytes * 100) / totalBytes).toInt().coerceIn(0, 100)
        val text = buildString {
            append(fileName)
            append(" · ")
            append(percent)
            append("% · ")
            append(formatSpeed(bytesPerSecond))
            append(" · ")
            append(formatBytes(sentBytes))
            append('/')
            append(formatBytes(totalBytes))
        }
        return baseBuilder(context)
            .setContentTitle("MoliSync 正在发送")
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setProgress(100, percent, false)
            .setOngoing(true)
            .build()
    }

    fun result(context: Context, success: Boolean, message: String): Notification =
        baseBuilder(context, includeCancel = false)
            .setContentTitle(if (success) "MoliSync 传输完成" else "MoliSync 传输失败")
            .setContentText(message)
            .setStyle(NotificationCompat.BigTextStyle().bigText(message))
            .setOngoing(false)
            .setAutoCancel(true)
            .setProgress(0, 0, false)
            .build()

    fun notify(context: Context, notification: Notification) {
        try {
            NotificationManagerCompat.from(context).notify(NOTIFICATION_ID, notification)
        } catch (_: SecurityException) {
            // POST_NOTIFICATIONS denied: the FGS still runs and appears in the
            // system task manager.
        }
    }

    fun cancel(context: Context) {
        NotificationManagerCompat.from(context).cancel(NOTIFICATION_ID)
    }

    private fun baseBuilder(context: Context, includeCancel: Boolean = true): NotificationCompat.Builder {
        val contentIntent = PendingIntent.getActivity(
            context,
            0,
            Intent(context, MainActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
            },
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val cancelIntent = PendingIntent.getService(
            context,
            1,
            Intent(context, TransferService::class.java).setAction(TransferService.ACTION_CANCEL),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

        val builder = NotificationCompat.Builder(context, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentIntent(contentIntent)
            .setCategory(NotificationCompat.CATEGORY_PROGRESS)
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setColor(0xFF2E6B5A.toInt())
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
        if (includeCancel) {
            builder.addAction(R.drawable.ic_notification, "取消", cancelIntent)
        }
        return builder
    }

    fun formatBytes(bytes: Long): String {
        if (bytes < 1024) {
            return "$bytes B"
        }
        val units = arrayOf("KiB", "MiB", "GiB", "TiB")
        var value = bytes.toDouble() / 1024.0
        var index = 0
        while (value >= 1024.0 && index < units.lastIndex) {
            value /= 1024.0
            index++
        }
        return String.format(Locale.US, "%.1f %s", value, units[index])
    }

    fun formatSpeed(bytesPerSecond: Long): String =
        if (bytesPerSecond <= 0) "—" else "${formatBytes(bytesPerSecond)}/s"
}
