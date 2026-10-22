package com.molirain.molisync.transfer

import android.os.SystemClock

/**
 * Limits notification updates to a human friendly rate and derives the
 * instantaneous throughput from the delta since the previous update.
 */
class ProgressThrottler(private val minimumIntervalMs: Long = 400L) {
    private var lastBytes = 0L
    private var lastTimestamp = 0L
    private var lastPercent = -1

    fun update(progress: TransferProgress): TransferProgress? {
        val now = SystemClock.elapsedRealtime()
        val percent = if (progress.totalBytes <= 0) {
            0
        } else {
            ((progress.bytesSent * 100) / progress.totalBytes).toInt().coerceIn(0, 100)
        }

        if (lastTimestamp != 0L && now - lastTimestamp < minimumIntervalMs && percent < 100) {
            return null
        }

        val elapsed = now - lastTimestamp
        val speed = if (lastTimestamp == 0L || elapsed <= 0) {
            0L
        } else {
            ((progress.bytesSent - lastBytes) * 1000L) / elapsed
        }

        lastTimestamp = now
        lastBytes = progress.bytesSent
        lastPercent = percent
        return progress.copy(bytesPerSecond = speed.coerceAtLeast(0L))
    }

    fun lastPercentValue(): Int = lastPercent
}
