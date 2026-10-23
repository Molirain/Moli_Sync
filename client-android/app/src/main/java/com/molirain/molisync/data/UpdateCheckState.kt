package com.molirain.molisync.data

/**
 * State of the release lookup. The settings screen renders it so a manual
 * "check for updates" always produces visible feedback instead of silently
 * reusing a cached result.
 */
sealed interface UpdateCheckState {
    /** Nothing has been checked yet in this process. */
    data object Idle : UpdateCheckState

    data object Checking : UpdateCheckState

    /** The server answered and the installed build is current. */
    data class UpToDate(val version: String) : UpdateCheckState

    /** A newer `android-v*` release exists; [pendingUpdate] also carries it. */
    data class Available(val update: AppUpdate) : UpdateCheckState

    /** The lookup failed, e.g. offline or rate limited. */
    data class Failed(val message: String) : UpdateCheckState
}
