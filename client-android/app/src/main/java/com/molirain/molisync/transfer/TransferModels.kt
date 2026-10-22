package com.molirain.molisync.transfer

import com.molirain.molisync.data.isNumericIp
import com.molirain.molisync.security.isCertificateFingerprint
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

private val TransferJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    explicitNulls = false
}

class TransferValidationException(message: String) : IllegalArgumentException(message)

@Serializable
data class TransferEndpoint(
    val network: String,
    val ip: String,
    val port: Int,
    val priority: Int = 0,
)

@Serializable
data class TransferFile(
    val uri: String,
    val name: String,
    val size: Long,
    val mimeType: String? = null,
)

/**
 * Everything the foreground service needs. It is passed as a serialized extra so
 * no Parcelable graph or Activity reference leaks into the service.
 */
@Serializable
data class TransferRequest(
    val targetDeviceId: String,
    val targetName: String,
    val endpoints: List<TransferEndpoint>,
    val files: List<TransferFile>,
    val sessionId: String? = null,
    val ticket: String? = null,
    val relayUrl: String? = null,
    val serverUrl: String? = null,
    val selfDeviceId: String? = null,
    val selfDeviceName: String? = null,
) {
    fun validate() {
        if (!isCertificateFingerprint(targetDeviceId)) {
            throw TransferValidationException(
                "目标设备没有指纹型 DeviceID，无法安全校验 P2P 对端证书",
            )
        }
        if (sessionId.isNullOrBlank() || ticket.isNullOrBlank()) {
            throw TransferValidationException("缺少服务器签发的 P2P 会话票据")
        }
        if (serverUrl.isNullOrBlank()) {
            throw TransferValidationException("缺少服务器地址，无法回退中继")
        }
        if (targetName.length > 128 || targetName.any { it.isISOControl() }) {
            throw TransferValidationException("目标设备名称不合法")
        }
        if (endpoints.isEmpty() && relayUrl.isNullOrBlank()) {
            throw TransferValidationException("目标设备没有可用的 P2P 地址，也没有可用的中继")
        }
        if (endpoints.size > MAX_ENDPOINTS) {
            throw TransferValidationException("P2P 地址数量超出限制")
        }
        endpoints.forEach { endpoint ->
            if (endpoint.network != "lan" && endpoint.network != "vpn") {
                throw TransferValidationException("未知的 P2P 网络类型")
            }
            if (endpoint.port !in 1..65535 || !isNumericIp(endpoint.ip)) {
                throw TransferValidationException("P2P 地址不合法")
            }
        }
        if (files.isEmpty()) {
            throw TransferValidationException("没有可传输的文件")
        }
        if (files.size > MAX_FILES) {
            throw TransferValidationException("一次最多传输 $MAX_FILES 个文件")
        }
        val seen = HashSet<String>()
        var totalBytes = 0L
        files.forEach { file ->
            if (!file.uri.startsWith("content://")) {
                throw TransferValidationException("只接受 content:// 文件 URI")
            }
            if (file.size < 0) {
                throw TransferValidationException("${file.name} 的大小未知，无法建立传输")
            }
            if (file.size > MAX_TOTAL_BYTES || totalBytes > MAX_TOTAL_BYTES - file.size) {
                throw TransferValidationException("文件总大小超出单次传输上限")
            }
            totalBytes += file.size
            if (file.name.isBlank() || file.name.length > 255) {
                throw TransferValidationException("文件名不合法")
            }
            if (!seen.add(file.uri)) {
                throw TransferValidationException("文件列表中存在重复 URI")
            }
        }
    }

    fun toJson(): String = TransferJson.encodeToString(serializer(), this)

    companion object {
        private const val MAX_FILES = 100
        private const val MAX_ENDPOINTS = 16
        private const val MAX_TOTAL_BYTES = 1L shl 40

        fun fromJson(json: String): TransferRequest =
            TransferJson.decodeFromString(serializer(), json)
    }
}

data class CompletedFile(
    val index: Int,
    val name: String,
    val size: Long,
    val blake3: String,
)

data class TransferProgress(
    val fileIndex: Int,
    val fileCount: Int,
    val fileName: String,
    val bytesSent: Long,
    val totalBytes: Long,
    val bytesPerSecond: Long = 0L,
)

data class TransferSummary(
    val files: List<CompletedFile>,
    val totalBytes: Long,
    val elapsedMillis: Long,
)

interface TransferListener {
    fun onProgress(progress: TransferProgress)

    fun onFileCompleted(file: CompletedFile)
}

class TransferException(message: String, cause: Throwable? = null) : Exception(message, cause)
