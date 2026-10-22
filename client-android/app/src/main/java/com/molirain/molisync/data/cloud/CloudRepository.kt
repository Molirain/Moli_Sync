package com.molirain.molisync.data.cloud

import android.content.Context
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.security.MtlsClient
import com.molirain.molisync.security.MtlsClientFactory
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okio.BufferedSink
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.io.InputStream

/**
 * mTLS HTTP client for the durable record API. The TLS identity is the same
 * KeyChain certificate used by the control plane; the private key never leaves
 * the system keystore.
 */
class CloudRepository(
    private val context: Context,
    private val settings: ClientSettings,
    private val keyChain: KeyChainAdapter,
) {
    private val clientMutex = Mutex()
    private var cached: MtlsClient? = null

    suspend fun listRecords(limit: Int = 200): List<CloudRecord> = withContext(Dispatchers.IO) {
        val url = apiUrl("api", "v1", "records").newBuilder()
            .addQueryParameter("limit", limit.coerceIn(1, 200).toString())
            .build()
        val request = Request.Builder().url(url).get().build()
        execute(request) { response ->
            val body = response.body?.string().orEmpty()
            if (!response.isSuccessful) {
                throw CloudException(errorMessage(response.code, body))
            }
            CloudJson.decodeFromString(RecordsResponse.serializer(), body).records
        }
    }

    suspend fun postText(text: String): CloudRecord = withContext(Dispatchers.IO) {
        val payload = CloudJson.encodeToString(TextRecordRequest.serializer(), TextRecordRequest(text))
        val request = Request.Builder()
            .url(apiUrl("api", "v1", "records", "text"))
            .post(payload.toRequestBody(JSON_MEDIA_TYPE))
            .build()
        execute(request) { response ->
            val body = response.body?.string().orEmpty()
            if (!response.isSuccessful) {
                throw CloudException(errorMessage(response.code, body))
            }
            CloudJson.decodeFromString(CloudRecord.serializer(), body)
        }
    }

    /**
     * Streams a content:// URI to the server. The caller owns the stream
     * opener; nothing is copied into app storage.
     */
    suspend fun upload(
        kind: String,
        displayName: String,
        mimeType: String?,
        size: Long,
        blake3: String,
        open: () -> InputStream,
        onProgress: (Long, Long) -> Unit,
    ): CloudRecord = withContext(Dispatchers.IO) {
        require(kind == CloudKind.IMAGE || kind == CloudKind.FILE) { "only image and file records have blobs" }
        require(size > 0) { "size must be known before uploading" }

        val url = apiUrl("api", "v1", "records", "blob").newBuilder()
            .addQueryParameter("kind", kind)
            .addQueryParameter("name", displayName)
            .apply {
                if (!mimeType.isNullOrBlank()) {
                    addQueryParameter("mime", mimeType)
                }
            }
            .build()

        val mediaType = mimeType?.toMediaTypeOrNull() ?: OCTET_STREAM_MEDIA_TYPE
        val body = StreamRequestBody(size, mediaType, open, onProgress)
        val builder = Request.Builder().url(url).post(body)
        if (blake3.length == 64) {
            builder.header("X-Blake3", blake3)
        }
        execute(builder.build()) { response ->
            val responseBody = response.body?.string().orEmpty()
            if (!response.isSuccessful) {
                throw CloudException(errorMessage(response.code, responseBody))
            }
            CloudJson.decodeFromString(CloudRecord.serializer(), responseBody)
        }
    }

    /**
     * Downloads a blob into [targetDir], resuming an interrupted .part file
     * with an HTTP Range request. Returns the final file.
     */
    suspend fun download(
        record: CloudRecord,
        targetDir: File,
        onProgress: (Long, Long) -> Unit,
    ): File = withContext(Dispatchers.IO) {
        if (!targetDir.exists() && !targetDir.mkdirs()) {
            throw CloudException("无法创建下载目录")
        }
        val part = File(targetDir, "${record.recordId}.part")
        var existing = if (part.exists()) part.length() else 0L
        if (record.fileSize > 0 && existing > record.fileSize) {
            part.delete()
            existing = 0L
        }

        val builder = Request.Builder().url(apiUrl("api", "v1", "blobs", record.recordId)).get()
        if (existing > 0) {
            builder.header("Range", "bytes=$existing-")
        }

        var completed = false
        execute(builder.build()) { response ->
            val body = response.body ?: throw CloudException("服务器未返回文件内容")
            val append = response.code == 206
            if (response.code == 200 && existing > 0) {
                existing = 0L
            }
            if (response.code == 416) {
                if (record.fileSize == existing) {
                    completed = true
                    return@execute Unit
                }
                part.delete()
                throw CloudException("续传请求被服务器拒绝，请重试")
            }
            if (!response.isSuccessful) {
                throw CloudException(errorMessage(response.code, body.string()))
            }

            var received = if (append) existing else 0L
            onProgress(received, record.fileSize)
            FileOutputStream(part, append).use { output ->
                body.byteStream().use { input ->
                    val buffer = ByteArray(DOWNLOAD_BUFFER_SIZE)
                    while (true) {
                        val read = input.read(buffer)
                        if (read < 0) break
                        if (read == 0) continue
                        output.write(buffer, 0, read)
                        received += read
                        onProgress(received, record.fileSize)
                    }
                }
            }
            if (record.fileSize > 0 && part.length() != record.fileSize) {
                throw CloudException("下载不完整：${part.length()}/${record.fileSize} 字节")
            }
            completed = true
        }
        if (!completed) {
            throw CloudException("下载未完成，请重试")
        }

        val target = uniqueFile(targetDir, sanitizeFileName(record.displayName))
        if (!part.renameTo(target)) {
            part.copyTo(target, overwrite = false)
            part.delete()
        }
        target
    }

    suspend fun delete(recordId: String) = withContext(Dispatchers.IO) {
        val request = Request.Builder()
            .url(apiUrl("api", "v1", "records", recordId))
            .delete()
            .build()
        execute(request) { response ->
            if (!response.isSuccessful) {
                throw CloudException(errorMessage(response.code, response.body?.string().orEmpty()))
            }
        }
    }

    fun close() {
        val current = cached ?: return
        cached = null
        current.client.dispatcher.executorService.shutdown()
        current.client.connectionPool.evictAll()
    }

    private suspend fun client(): MtlsClient {
        cached?.let { return it }
        return clientMutex.withLock {
            cached ?: withContext(Dispatchers.IO) {
                val alias = settings.certificateAlias
                    ?: throw CloudException("尚未授权客户端证书")
                MtlsClientFactory.createHttp(context, keyChain, alias).also { cached = it }
            }
        }
    }

    private suspend fun <T> execute(request: Request, block: (okhttp3.Response) -> T): T {
        val http = client()
        return withContext(Dispatchers.IO) {
            try {
                http.client.newCall(request).execute().use(block)
            } catch (exception: CloudException) {
                throw exception
            } catch (exception: IOException) {
                throw CloudException(exception.message ?: "网络请求失败", exception)
            }
        }
    }

    private fun apiUrl(vararg segments: String): HttpUrl {
        val server = settings.serverUrl ?: throw CloudException("尚未配置服务器")
        return server.toHttpUrl().newBuilder().apply {
            segments.forEach(::addPathSegment)
        }.build()
    }

    private fun errorMessage(code: Int, body: String): String {
        val detail = runCatching {
            CloudJson.decodeFromString(ApiErrorEnvelope.serializer(), body).error.message
        }.getOrNull()
        return if (!detail.isNullOrBlank()) detail else "服务器返回 HTTP $code"
    }

    private class StreamRequestBody(
        private val length: Long,
        private val mediaType: MediaType,
        private val opener: () -> InputStream,
        private val onProgress: (Long, Long) -> Unit,
    ) : RequestBody() {
        override fun contentType(): MediaType = mediaType

        override fun contentLength(): Long = length

        override fun writeTo(sink: BufferedSink) {
            var sent = 0L
            opener().use { input ->
                val buffer = ByteArray(UPLOAD_BUFFER_SIZE)
                while (true) {
                    val read = input.read(buffer)
                    if (read < 0) break
                    if (read == 0) continue
                    sink.write(buffer, 0, read)
                    sent += read
                    onProgress(sent, length)
                }
            }
            if (sent != length) {
                throw IOException("文件在传输过程中被修改：$sent/$length 字节")
            }
        }
    }

    companion object {
        private const val UPLOAD_BUFFER_SIZE = 64 * 1024
        private const val DOWNLOAD_BUFFER_SIZE = 64 * 1024
        private val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        private val OCTET_STREAM_MEDIA_TYPE = "application/octet-stream".toMediaType()

        fun sanitizeFileName(value: String): String {
            val cleaned = value
                .substringAfterLast('/')
                .substringAfterLast('\\')
                .map { character ->
                    when {
                        character.isISOControl() -> ' '
                        character in "\\/:*?\"<>|" -> '_'
                        else -> character
                    }
                }
                .joinToString("")
                .trim()
                .trim('.')
                .take(120)
            return cleaned.ifEmpty { "file" }
        }

        private fun uniqueFile(directory: File, name: String): File {
            var candidate = File(directory, name)
            if (!candidate.exists()) return candidate
            val dot = name.lastIndexOf('.')
            val base = if (dot > 0) name.substring(0, dot) else name
            val extension = if (dot > 0) name.substring(dot) else ""
            var index = 1
            while (candidate.exists()) {
                candidate = File(directory, "$base ($index)$extension")
                index++
            }
            return candidate
        }
    }
}
