package com.molirain.molisync.data.cloud

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

internal val CloudJson = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
}

object CloudKind {
    const val TEXT = "text"
    const val IMAGE = "image"
    const val FILE = "file"
}

/**
 * One record as returned by GET /api/v1/records.
 *
 * The HTTP representation is flat; the WebSocket sync_record frame nests file
 * metadata under file_meta. The Android client uses HTTP as the source of
 * truth and treats WebSocket records as a refresh signal, so only this shape
 * needs to be decoded.
 */
@Serializable
data class CloudRecord(
    @SerialName("record_id") val recordId: String,
    @SerialName("sender_device_id") val senderDeviceId: String = "",
    @SerialName("sender_name") val senderName: String = "",
    @SerialName("created_at") val createdAt: Long = 0L,
    @SerialName("type") val kind: String = "",
    @SerialName("text_content") val text: String = "",
    @SerialName("blob_id") val blobId: String = "",
    @SerialName("file_name") val fileName: String = "",
    @SerialName("file_size") val fileSize: Long = 0L,
    @SerialName("mime_type") val mimeType: String = "",
    @SerialName("blake3_hash") val blake3: String = "",
) {
    val isText: Boolean get() = kind == CloudKind.TEXT
    val isImage: Boolean get() = kind == CloudKind.IMAGE
    val isFile: Boolean get() = kind == CloudKind.FILE
    val hasBlob: Boolean get() = blobId.isNotEmpty()
    val displayName: String get() = fileName.ifBlank { "未命名文件" }
}

@Serializable
internal data class RecordsResponse(
    val records: List<CloudRecord> = emptyList(),
)

@Serializable
internal data class TextRecordRequest(val text: String)

@Serializable
internal data class ApiErrorEnvelope(
    val error: ApiErrorBody = ApiErrorBody(),
)

@Serializable
internal data class ApiErrorBody(
    val code: String = "",
    val message: String = "",
)

class CloudException(message: String, cause: Throwable? = null) : Exception(message, cause)
