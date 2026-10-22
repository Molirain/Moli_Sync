package com.molirain.molisync.data.cloud

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.media.MediaScannerConnection
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import androidx.annotation.RequiresApi
import java.io.File

/**
 * Publishes finished downloads into the shared media collections so they show
 * up in the gallery / file manager exactly like WeChat's folders:
 *
 *   images -> Pictures/MoliSync
 *   files  -> Download/MoliSync
 *
 * Android 10+ uses MediaStore with IS_PENDING so a partially written file is
 * never visible. Older releases fall back to the legacy public directory and a
 * media scanner notification.
 */
object MediaStoreWriter {

    data class Published(val uri: String?, val displayPath: String)

    fun publish(
        context: Context,
        source: File,
        displayName: String,
        mimeType: String?,
        isImage: Boolean,
    ): Published {
        val name = displayName.ifBlank { source.name }
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            publishModern(context, source, name, mimeType, isImage)
        } else {
            publishLegacy(context, source, name, isImage)
        }
    }

    @RequiresApi(Build.VERSION_CODES.Q)
    private fun publishModern(
        context: Context,
        source: File,
        name: String,
        mimeType: String?,
        isImage: Boolean,
    ): Published {
        val resolver = context.contentResolver
        val collection = if (isImage) {
            MediaStore.Images.Media.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
        } else {
            MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
        }
        val relativePath = if (isImage) {
            "${Environment.DIRECTORY_PICTURES}/MoliSync"
        } else {
            "${Environment.DIRECTORY_DOWNLOADS}/MoliSync"
        }

        val resolvedMime = mimeType?.takeIf { it.isNotBlank() }
            ?: if (isImage) "image/*" else "application/octet-stream"
        val values = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, name)
            put(MediaStore.MediaColumns.MIME_TYPE, resolvedMime)
            put(MediaStore.MediaColumns.RELATIVE_PATH, relativePath)
            put(MediaStore.MediaColumns.IS_PENDING, 1)
        }

        val uri = resolver.insert(collection, values)
            ?: throw CloudException("无法在系统相册/下载目录中创建文件")

        try {
            resolver.openOutputStream(uri)?.use { output ->
                source.inputStream().use { input -> input.copyTo(output, DEFAULT_BUFFER_SIZE) }
            } ?: throw CloudException("无法写入系统媒体库")

            val done = ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) }
            resolver.update(uri, done, null, null)
        } catch (failure: Exception) {
            runCatching { resolver.delete(uri, null, null) }
            throw failure
        }

        val suffix = if (isImage) "Pictures/MoliSync" else "Download/MoliSync"
        return Published(uri.toString(), "$suffix/$name")
    }

    private fun publishLegacy(
        context: Context,
        source: File,
        name: String,
        isImage: Boolean,
    ): Published {
        val base = Environment.getExternalStoragePublicDirectory(
            if (isImage) Environment.DIRECTORY_PICTURES else Environment.DIRECTORY_DOWNLOADS,
        )
        val directory = File(base, "MoliSync")
        if (!directory.exists() && !directory.mkdirs()) {
            throw CloudException("无法创建 MoliSync 目录")
        }
        val target = uniqueFile(directory, name)
        source.copyTo(target, overwrite = false)

        MediaScannerConnection.scanFile(
            context,
            arrayOf(target.absolutePath),
            null,
            null,
        )
        val suffix = if (isImage) "Pictures/MoliSync" else "Download/MoliSync"
        return Published(null, "$suffix/${target.name}")
    }

    private fun uniqueFile(directory: File, name: String): File {
        val sanitized = sanitize(name)
        var candidate = File(directory, sanitized)
        if (!candidate.exists()) {
            return candidate
        }
        val base = sanitized.substringBeforeLast('.', sanitized)
        val extension = sanitized.substringAfterLast('.', "")
        var index = 1
        while (true) {
            val suffix = if (extension.isEmpty()) " ($index)" else " ($index).$extension"
            candidate = File(directory, base + suffix)
            if (!candidate.exists()) {
                return candidate
            }
            index++
        }
    }

    fun sanitize(name: String): String {
        val cleaned = name.map { character ->
            if (character in "\\/:*?\"<>|" || character.code < 0x20) '_' else character
        }.joinToString("").trim().trimEnd('.')

        val safe = cleaned.ifBlank { "MoliSync-file" }
        return if (safe.length <= 120) safe else safe.take(120)
    }

    fun openWith(context: Context, uri: String?, mimeType: String?) {
        val target = uri ?: return
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(Uri.parse(target), mimeType?.takeIf { it.isNotBlank() } ?: "*/*")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        runCatching { context.startActivity(intent) }
    }

    /** Best-effort "show this file in a file manager" intent. */
    fun revealInFileManager(context: Context, isImage: Boolean) {
        val folder = if (isImage) "Pictures" else "Download"
        val documentUri = Uri.parse(
            "content://com.android.externalstorage.documents/document/primary%3A$folder%2FMoliSync",
        )
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(documentUri, "vnd.android.document/directory")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        val opened = runCatching { context.startActivity(intent) }.isSuccess
        if (!opened) {
            // Fall back to the built-in file manager picker rooted at the folder.
            val fallback = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                addCategory(Intent.CATEGORY_OPENABLE)
                type = if (isImage) "image/*" else "*/*"
            }
            runCatching { context.startActivity(fallback) }
        }
    }
}
