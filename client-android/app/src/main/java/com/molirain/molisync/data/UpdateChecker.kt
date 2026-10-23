package com.molirain.molisync.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.util.concurrent.TimeUnit

/** A newer Android build found in the `android-v*` release namespace. */
data class AppUpdate(
    val version: String,
    val tag: String,
    val url: String,
    val notes: String?,
)

/**
 * Reads the Android releases from the public GitHub API.
 *
 * Only tags named `android-vX.Y.Z` are considered, so publishing a desktop or
 * server fix never shows up as an Android update. The request carries no client
 * certificate: the MoliSync identity is bound to the user's own server and must
 * never be presented to a third party.
 */
class UpdateChecker(
    private val client: OkHttpClient = defaultClient(),
    private val repository: String = REPOSITORY,
    private val apiBase: String = API_BASE,
) {
    /**
     * Returns the newest release when it is newer than [currentVersion].
     *
     * @throws IOException when the API is unreachable or answers with an error.
     */
    suspend fun check(currentVersion: String): AppUpdate? {
        val request = Request.Builder()
            .url("$apiBase/repos/$repository/releases?per_page=50")
            .header("Accept", "application/vnd.github+json")
            .header("X-GitHub-Api-Version", "2022-11-28")
            .header("User-Agent", "MoliSync-Android/$currentVersion")
            .get()
            .build()

        val payload = client.newCall(request).execute().use { response ->
            if (!response.isSuccessful) {
                throw IOException("GitHub 返回 HTTP ${response.code}")
            }
            response.body?.string() ?: throw IOException("GitHub 返回了空响应")
        }

        val update = selectLatest(payload) ?: return null
        return update.takeIf { compareVersions(it.version, currentVersion) > 0 }
    }

    companion object {
        const val REPOSITORY = "Molirain/Moli_Sync"
        const val API_BASE = "https://api.github.com"
        const val TAG_PREFIX = "android-v"
        const val RELEASES_PAGE = "https://github.com/$REPOSITORY/releases"

        private val json = Json { ignoreUnknownKeys = true }
        private val releasesSerializer = ListSerializer(GitHubRelease.serializer())

        private fun defaultClient(): OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(10, TimeUnit.SECONDS)
            .readTimeout(15, TimeUnit.SECONDS)
            .callTimeout(20, TimeUnit.SECONDS)
            .retryOnConnectionFailure(true)
            .build()

        /** Parses a release list and returns the highest `android-v` entry. */
        internal fun selectLatest(payload: String): AppUpdate? {
            val releases = runCatching { json.decodeFromString(releasesSerializer, payload) }
                .getOrNull() ?: return null

            return releases
                .asSequence()
                .filter { !it.draft && !it.prerelease }
                .filter { it.tagName.startsWith(TAG_PREFIX) }
                .mapNotNull { release ->
                    val version = normalize(release.tagName.removePrefix(TAG_PREFIX))
                    if (!isSemVer(version)) {
                        null
                    } else {
                        AppUpdate(
                            version = version,
                            tag = release.tagName,
                            url = release.htmlUrl.ifBlank { RELEASES_PAGE },
                            notes = release.body?.takeIf { it.isNotBlank() },
                        )
                    }
                }
                .maxWithOrNull { left, right -> compareVersions(left.version, right.version) }
        }

        /** "v0.5.1" / "0.5.1-rc1" -> "0.5.1". */
        internal fun normalize(value: String): String =
            value.trim().removePrefix("v").removePrefix("V").substringBefore('-').substringBefore('+')

        internal fun isSemVer(value: String): Boolean {
            val parts = value.split('.')
            return parts.size == 3 && parts.all { part -> part.isNotEmpty() && part.all(Char::isDigit) }
        }

        /** Compares two MAJOR.MINOR.PATCH strings. */
        internal fun compareVersions(left: String, right: String): Int {
            val a = parse(left)
            val b = parse(right)
            for (index in 0..2) {
                if (a[index] != b[index]) {
                    return a[index].compareTo(b[index])
                }
            }
            return 0
        }

        private fun parse(value: String): IntArray {
            val parts = normalize(value).split('.')
            return IntArray(3) { index -> parts.getOrNull(index)?.toIntOrNull() ?: 0 }
        }
    }

    @Serializable
    internal data class GitHubRelease(
        @SerialName("tag_name") val tagName: String = "",
        @SerialName("html_url") val htmlUrl: String = "",
        @SerialName("body") val body: String? = null,
        val draft: Boolean = false,
        val prerelease: Boolean = false,
    )
}
