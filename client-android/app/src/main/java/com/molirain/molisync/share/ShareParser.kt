package com.molirain.molisync.share

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.DocumentsContract
import android.provider.OpenableColumns

data class SharedFile(
    val uri: Uri,
    val displayName: String,
    val sizeBytes: Long,
    val mimeType: String,
)

data class SharePayload(
    val files: List<SharedFile>,
    val text: String?,
)

/**
 * Parses ACTION_SEND / ACTION_SEND_MULTIPLE without touching file content.
 *
 * Only content:// URIs are accepted. Metadata is read through OpenableColumns;
 * if a provider does not expose a size, the entry is kept so the UI can explain
 * why the transfer cannot start. ParcelFileDescriptors are opened later, when
 * the user actually starts the transfer, and handed to the FGS before the
 * activity finishes.
 */
object ShareParser {

    fun parse(context: Context, intent: Intent): SharePayload {
        val uris = LinkedHashSet<Uri>()

        when (intent.action) {
            Intent.ACTION_SEND -> {
                streamUri(intent)?.let(uris::add)
            }

            Intent.ACTION_SEND_MULTIPLE -> {
                streamUris(intent).forEach(uris::add)
            }
        }

        intent.clipData?.let { clip ->
            for (index in 0 until clip.itemCount) {
                clip.getItemAt(index).uri?.let(uris::add)
            }
        }

        val files = uris.mapNotNull { uri -> describe(context, uri) }
        val text = intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()
        return SharePayload(files, text)
    }

    @Suppress("DEPRECATION")
    private fun streamUri(intent: Intent): Uri? =
        intent.getParcelableExtra(Intent.EXTRA_STREAM) as? Uri

    @Suppress("DEPRECATION")
    private fun streamUris(intent: Intent): List<Uri> =
        intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM).orEmpty()

    /**
     * Reads OpenableColumns metadata for one content URI. Exposed so the main
     * screen can describe a file chosen through the document picker without
     * duplicating provider-specific logic.
     */
    fun describe(context: Context, uri: Uri): SharedFile? {
        if (!uri.scheme.equals("content", ignoreCase = true)) {
            return null
        }
        if (runCatching { context.contentResolver.getType(uri) }
                .getOrNull() == DocumentsContract.Document.MIME_TYPE_DIR
        ) {
            return null
        }

        var displayName: String? = null
        var size = -1L
        try {
            context.contentResolver.query(
                uri,
                arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE),
                null,
                null,
                null,
            )?.use { cursor ->
                if (cursor.moveToFirst()) {
                    val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                    if (nameIndex >= 0 && !cursor.isNull(nameIndex)) {
                        displayName = cursor.getString(nameIndex)
                    }
                    val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)
                    if (sizeIndex >= 0 && !cursor.isNull(sizeIndex)) {
                        size = cursor.getLong(sizeIndex)
                    }
                }
            }
        } catch (_: Exception) {
            // Fall back to the descriptor/path below.
        }

        if (size < 0) {
            size = runCatching {
                context.contentResolver.openAssetFileDescriptor(uri, "r")?.use { it.length } ?: -1L
            }.getOrDefault(-1L)
        }

        val mimeType = runCatching { context.contentResolver.getType(uri) }
            .getOrNull()
            ?: "application/octet-stream"
        val rawName = (displayName ?: uri.lastPathSegment ?: "file")
            .substringAfterLast('/')
            .substringAfterLast('\\')
        val name = rawName
            .map { character -> if (character.isISOControl()) ' ' else character }
            .joinToString("")
            .trim()
            .take(255)
            .let { candidate ->
                if (candidate.isEmpty() || candidate == "." || candidate == "..") "file" else candidate
            }

        return SharedFile(
            uri = uri,
            displayName = name,
            sizeBytes = size,
            mimeType = mimeType,
        )
    }
}
