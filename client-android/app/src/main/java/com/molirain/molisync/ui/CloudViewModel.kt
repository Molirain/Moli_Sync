package com.molirain.molisync.ui

import android.app.Application
import android.net.Uri
import android.os.Environment
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.molirain.molisync.MoliSyncApp
import com.molirain.molisync.data.cloud.CloudException
import com.molirain.molisync.data.cloud.CloudKind
import com.molirain.molisync.data.cloud.CloudRecord
import com.molirain.molisync.share.SharedFile
import com.molirain.molisync.data.cloud.FileDigest
import com.molirain.molisync.data.cloud.MediaStoreWriter
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.InputStream

data class UploadProgress(val name: String, val sent: Long, val total: Long) {
    val fraction: Float
        get() = if (total <= 0) 0f else (sent.toFloat() / total.toFloat()).coerceIn(0f, 1f)
}

data class DownloadProgress(val received: Long, val total: Long) {
    val fraction: Float
        get() = if (total <= 0) 0f else (received.toFloat() / total.toFloat()).coerceIn(0f, 1f)
}

data class DownloadedFile(
    val uri: String?,
    val path: String,
    val displayPath: String,
)

data class CloudUiState(
    val records: List<CloudRecord> = emptyList(),
    val loading: Boolean = false,
    val refreshing: Boolean = false,
    val error: String? = null,
    val uploading: UploadProgress? = null,
    val downloading: Map<String, DownloadProgress> = emptyMap(),
    val downloaded: Map<String, DownloadedFile> = emptyMap(),
    val message: String? = null,
) {
    fun ofKind(kind: String): List<CloudRecord> = records.filter { it.kind == kind }

    val cloudBytes: Long
        get() = records.sumOf { if (it.isText) 0L else it.fileSize }
}

/**
 * Loads and mutates the durable record list. Records live on the server, so
 * every mutation refreshes from HTTP instead of keeping a second local store.
 */
class CloudViewModel(application: Application) : AndroidViewModel(application) {
    private val app = application as MoliSyncApp
    private val repository = app.cloudRepository

    private val _state = MutableStateFlow(CloudUiState())
    val state: StateFlow<CloudUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            app.syncEvents.collect {
                delay(250)
                refresh(silent = true)
            }
        }
        refresh()
    }

    fun refresh(silent: Boolean = false) {
        if (_state.value.loading) {
            return
        }
        viewModelScope.launch {
            _state.update { it.copy(loading = true, refreshing = silent, error = null) }
            runCatching { repository.listRecords() }
                .onSuccess { records ->
                    _state.update {
                        it.copy(records = records, loading = false, refreshing = false, error = null)
                    }
                }
                .onFailure { failure ->
                    _state.update {
                        it.copy(
                            loading = false,
                            refreshing = false,
                            error = failure.message ?: "无法加载同步记录",
                        )
                    }
                }
        }
    }

    fun sendText(text: String) {
        val trimmed = text.trim()
        if (trimmed.isEmpty()) {
            return
        }
        viewModelScope.launch {
            _state.update { it.copy(message = null) }
            runCatching { repository.postText(trimmed) }
                .onSuccess { record ->
                    _state.update {
                        it.copy(
                            records = (listOf(record) + it.records).distinctBy(CloudRecord::recordId),
                            message = "文字已保存到服务器",
                        )
                    }
                }
                .onFailure { failure ->
                    _state.update { it.copy(message = failure.message ?: "发送失败") }
                }
        }
    }

    fun upload(uri: Uri, file: SharedFile, kind: String) {
        if (kind != CloudKind.IMAGE && kind != CloudKind.FILE) {
            return
        }
        viewModelScope.launch {
            _state.update {
                it.copy(uploading = UploadProgress(file.displayName, 0L, file.sizeBytes), message = null)
            }
            val result = runCatching {
                val hash = withContext(Dispatchers.IO) { blake3Of(uri) }
                repository.upload(
                    kind = kind,
                    displayName = file.displayName,
                    mimeType = file.mimeType,
                    size = file.sizeBytes,
                    blake3 = hash,
                    open = { openStream(uri) },
                    onProgress = { sent, total ->
                        _state.update { current ->
                            current.copy(uploading = UploadProgress(file.displayName, sent, total))
                        }
                    },
                )
            }
            result.onSuccess { record ->
                _state.update {
                    it.copy(
                        records = (listOf(record) + it.records).distinctBy(CloudRecord::recordId),
                        message = "${file.displayName} 已保存到服务器",
                    )
                }
            }.onFailure { failure ->
                _state.update { it.copy(message = failure.message ?: "上传失败") }
            }
            _state.update { it.copy(uploading = null) }
        }
    }

    fun download(record: CloudRecord) {
        if (!record.hasBlob) {
            _state.update { it.copy(message = "该记录没有可下载的文件") }
            return
        }
        if (_state.value.downloading.containsKey(record.recordId)) {
            // A second tap must never start a second transfer for the same blob.
            return
        }
        _state.value.downloaded[record.recordId]?.let { existing ->
            _state.update { it.copy(message = "已保存在 ${existing.displayPath}") }
            return
        }
        viewModelScope.launch {
            val directory = targetDirectory()
            _state.update {
                it.copy(
                    downloading = it.downloading + (record.recordId to DownloadProgress(0L, record.fileSize)),
                    message = null,
                )
            }
            runCatching {
                repository.download(record, directory) { received, total ->
                    _state.update { current ->
                        current.copy(
                            downloading = current.downloading +
                                (record.recordId to DownloadProgress(received, total)),
                        )
                    }
                }
            }.onSuccess { result ->
                _state.update {
                    it.copy(
                        downloading = it.downloading - record.recordId,
                        downloaded = it.downloaded + (
                            record.recordId to DownloadedFile(
                                uri = result.uri,
                                path = result.file.absolutePath,
                                displayPath = result.displayPath,
                            )
                            ),
                        message = "已保存到 ${result.displayPath}",
                    )
                }
            }.onFailure { failure ->
                _state.update {
                    it.copy(
                        downloading = it.downloading - record.recordId,
                        message = failure.message ?: "下载失败",
                    )
                }
            }
        }
    }

    fun open(record: CloudRecord) {
        val existing = _state.value.downloaded[record.recordId]
        if (existing == null) {
            download(record)
            return
        }
        if (existing.uri != null) {
            MediaStoreWriter.openWith(app, existing.uri, record.mimeType)
        } else {
            _state.update { it.copy(message = "文件位于 ${existing.displayPath}") }
        }
    }

    fun revealInFileManager(record: CloudRecord) {
        MediaStoreWriter.revealInFileManager(app, record.kind == CloudKind.IMAGE)
    }

    fun delete(record: CloudRecord) {
        viewModelScope.launch {
            runCatching { repository.delete(record.recordId) }
                .onSuccess {
                    _state.update { current ->
                        current.copy(
                            records = current.records.filterNot { it.recordId == record.recordId },
                            message = "已从服务器删除",
                        )
                    }
                }
                .onFailure { failure ->
                    _state.update { it.copy(message = failure.message ?: "删除失败") }
                }
        }
    }

    fun consumeMessage() {
        _state.update { it.copy(message = null) }
    }

    private fun openStream(uri: Uri): InputStream =
        app.contentResolver.openInputStream(uri) ?: throw CloudException("无法读取所选文件")

    private fun blake3Of(uri: Uri): String = FileDigest.blake3(app, uri)

    private fun targetDirectory(): File {
        val base = app.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS)
            ?: File(app.filesDir, "downloads")
        if (!base.exists()) {
            base.mkdirs()
        }
        return base
    }
}
