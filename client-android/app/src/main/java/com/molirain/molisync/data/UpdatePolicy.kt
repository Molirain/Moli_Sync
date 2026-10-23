package com.molirain.molisync.data

/**
 * When the app is allowed to contact GitHub again. One request per device per
 * interval stays far inside the anonymous 60 requests/hour limit, while a
 * release published today is still noticed the same day.
 */
internal object UpdatePolicy {

    const val CHECK_INTERVAL_MS: Long = 6L * 60L * 60L * 1000L

    /**
     * True when a lookup should really hit the network. A missing timestamp
     * (fresh install, or the previous attempt failed) always refreshes, so a
     * transient failure cannot hide a release for hours.
     */
    fun shouldRefresh(force: Boolean, now: Long, checkedAt: Long): Boolean =
        force || checkedAt <= 0L || now - checkedAt >= CHECK_INTERVAL_MS
}
