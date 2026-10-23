package com.molirain.molisync.data

import android.content.Context
import android.net.Uri
import android.os.Build

/**
 * Non-secret client settings. Certificate private keys never leave KeyChain,
 * so only the alias and public fingerprint are persisted.
 */
class ClientSettings(context: Context) {
    private val preferences = context.applicationContext.getSharedPreferences(
        PREFERENCES_NAME,
        Context.MODE_PRIVATE,
    )

    val serverUrl: String?
        get() = preferences.getString(KEY_SERVER_URL, null)

    val certificateAlias: String?
        get() = preferences.getString(KEY_CERT_ALIAS, null)

    val certificateFingerprint: String?
        get() = preferences.getString(KEY_CERT_FINGERPRINT, null)

    val deviceName: String
        get() = preferences.getString(KEY_DEVICE_NAME, null) ?: defaultDeviceName()

    val cloudThresholdBytes: Long
        get() = preferences.getLong(KEY_CLOUD_THRESHOLD, DEFAULT_CLOUD_THRESHOLD_BYTES)

    val isConfigured: Boolean
        get() = !serverUrl.isNullOrBlank() && !certificateAlias.isNullOrBlank()

    fun saveCloudThreshold(bytes: Long): Boolean {
        val clamped = bytes.coerceIn(MIN_CLOUD_THRESHOLD_BYTES, MAX_CLOUD_THRESHOLD_BYTES)
        return preferences.edit().putLong(KEY_CLOUD_THRESHOLD, clamped).commit()
    }

    fun saveConnection(serverUrl: String, alias: String, fingerprint: String): Boolean =
        preferences.edit()
            .putString(KEY_SERVER_URL, serverUrl)
            .putString(KEY_CERT_ALIAS, alias)
            .putString(KEY_CERT_FINGERPRINT, fingerprint)
            .commit()

    fun saveServerUrl(serverUrl: String): Boolean =
        preferences.edit().putString(KEY_SERVER_URL, serverUrl).commit()

    fun saveCertificate(alias: String, fingerprint: String): Boolean =
        preferences.edit()
            .putString(KEY_CERT_ALIAS, alias)
            .putString(KEY_CERT_FINGERPRINT, fingerprint)
            .commit()

    fun saveDeviceName(name: String): Boolean =
        preferences.edit().putString(KEY_DEVICE_NAME, sanitizeDisplayName(name)).commit()

    val updateCheckedAt: Long
        get() = preferences.getLong(KEY_UPDATE_CHECKED_AT, 0L)

    val cachedUpdateVersion: String?
        get() = preferences.getString(KEY_UPDATE_VERSION, null)

    val cachedUpdateUrl: String?
        get() = preferences.getString(KEY_UPDATE_URL, null)

    val dismissedUpdateVersion: String?
        get() = preferences.getString(KEY_UPDATE_DISMISSED, null)

    fun saveUpdateResult(checkedAt: Long, update: AppUpdate?): Boolean =
        preferences.edit()
            .putLong(KEY_UPDATE_CHECKED_AT, checkedAt)
            .putString(KEY_UPDATE_VERSION, update?.version)
            .putString(KEY_UPDATE_URL, update?.url)
            .commit()

    fun dismissUpdate(version: String): Boolean =
        preferences.edit().putString(KEY_UPDATE_DISMISSED, version).commit()

    fun clearCertificate(): Boolean =
        preferences.edit()
            .remove(KEY_CERT_ALIAS)
            .remove(KEY_CERT_FINGERPRINT)
            .commit()

    fun clear(): Boolean = preferences.edit().clear().commit()

    private fun defaultDeviceName(): String {
        val model = Build.MODEL?.trim().orEmpty()
        return sanitizeDisplayName(model.ifEmpty { "Android" })
    }

    companion object {
        private const val PREFERENCES_NAME = "molisync_client"
        private const val KEY_SERVER_URL = "server_url"
        private const val KEY_CERT_ALIAS = "cert_alias"
        private const val KEY_CERT_FINGERPRINT = "cert_fingerprint"
        private const val KEY_DEVICE_NAME = "device_name"
        private const val KEY_CLOUD_THRESHOLD = "cloud_threshold_bytes"
        private const val KEY_UPDATE_CHECKED_AT = "update_checked_at"
        private const val KEY_UPDATE_VERSION = "update_version"
        private const val KEY_UPDATE_URL = "update_url"
        private const val KEY_UPDATE_DISMISSED = "update_dismissed"

        const val DEFAULT_CLOUD_THRESHOLD_BYTES = 15L * 1024L * 1024L
        const val MIN_CLOUD_THRESHOLD_BYTES = 5L * 1024L * 1024L
        const val MAX_CLOUD_THRESHOLD_BYTES = 50L * 1024L * 1024L

        /**
         * Validates and canonicalizes an HTTPS base URL. Credentials, query
         * strings and fragments are rejected so the WebSocket URL cannot be
         * redirected to another authority by configuration.
         */
        fun normalizeServerUrl(input: String): String? {
            val value = input.trim()
            if (value.isEmpty()) {
                return null
            }
            val uri = runCatching { Uri.parse(value) }.getOrNull() ?: return null
            if (!uri.isHierarchical || !uri.scheme.equals("https", ignoreCase = true)) {
                return null
            }
            if (uri.host.isNullOrBlank() || uri.userInfo != null ||
                uri.query != null || uri.fragment != null
            ) {
                return null
            }
            val port = if (uri.port == -1) 443 else uri.port
            if (port !in 1..65535) {
                return null
            }
            val path = uri.path.orEmpty().trimEnd('/')
            val host = uri.host ?: return null
            val authority = if (host.contains(':')) "[$host]" else host
            return "https://$authority:$port$path"
        }

        fun sanitizeDisplayName(value: String): String {
            val cleaned = value.map { character ->
                if (character.isISOControl()) ' ' else character
            }.joinToString("").trim()
            return cleaned.ifEmpty { "Android" }.take(64)
        }
    }
}
