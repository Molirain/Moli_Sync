package com.molirain.molisync.share

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.ParcelFileDescriptor
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.molirain.molisync.MainActivity
import com.molirain.molisync.MoliSyncApp
import com.molirain.molisync.data.ConnectionState
import com.molirain.molisync.data.DeviceTarget
import com.molirain.molisync.data.cloud.CloudKind
import com.molirain.molisync.data.cloud.FileDigest
import com.molirain.molisync.transfer.TransferEndpoint
import com.molirain.molisync.transfer.TransferFile
import com.molirain.molisync.transfer.TransferNotifications
import com.molirain.molisync.transfer.TransferRequest
import com.molirain.molisync.transfer.TransferService
import com.molirain.molisync.ui.theme.MoliSyncTheme
import com.molirain.molisync.ui.theme.MoliColors
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

class ShareActivity : ComponentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        val payload = ShareParser.parse(this, intent)
        val app = application as MoliSyncApp
        setContent {
            MoliSyncTheme {
                ShareScreen(app = app, payload = payload, activity = this)
            }
        }
    }
}

private enum class ShareMode { CLOUD, P2P }

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ShareScreen(app: MoliSyncApp, payload: SharePayload, activity: Activity) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    val connection by app.controlPlane.state.collectAsStateWithLifecycle()
    val devices by app.deviceDirectory.devices.collectAsStateWithLifecycle()

    var selectedDeviceId by rememberSaveable { mutableStateOf<String?>(null) }
    var starting by remember { mutableStateOf(false) }
    var error by rememberSaveable { mutableStateOf<String?>(null) }
    var cloudProgress by remember { mutableFloatStateOf(0f) }

    val thresholdBytes = app.settings.cloudThresholdBytes
    val cloudEligible = payload.files.isNotEmpty() &&
        payload.files.all { it.sizeBytes in 1..thresholdBytes }
    var mode by rememberSaveable {
        mutableStateOf(if (cloudEligible) ShareMode.CLOUD.name else ShareMode.P2P.name)
    }

    // Devices are listed even before they report endpoints, so the user sees
    // why a transfer cannot start instead of an empty list.
    val availableDevices = remember(devices) {
        devices.values.sortedBy { it.name.lowercase() }
    }

    LaunchedEffect(Unit) {
        app.startTransport()
    }

    LaunchedEffect(availableDevices, selectedDeviceId) {
        if (selectedDeviceId != null && availableDevices.none { it.deviceId == selectedDeviceId }) {
            selectedDeviceId = null
        }
        if (selectedDeviceId == null && availableDevices.size == 1) {
            selectedDeviceId = availableDevices.first().deviceId
        }
    }

    val totalBytes = payload.files.sumOf { it.sizeBytes.coerceAtLeast(0L) }
    val hasUnknownSize = payload.files.any { it.sizeBytes < 0 }
    val canStart = payload.files.isNotEmpty() &&
        !hasUnknownSize &&
        !starting &&
        app.settings.isConfigured &&
        (
            if (mode == ShareMode.CLOUD.name) {
                cloudEligible
            } else {
                availableDevices.any { it.deviceId == selectedDeviceId }
            }
            )

    Scaffold(
        contentWindowInsets = WindowInsets.safeDrawing,
        topBar = {
            TopAppBar(
                title = { Text("分享到 MoliSync") },
                actions = {
                    TextButton(onClick = { activity.finish() }) {
                        Text("取消")
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            if (payload.files.isEmpty()) {
                Text("分享内容", style = MaterialTheme.typography.titleMedium)
                payload.text?.takeIf { it.isNotBlank() }?.let { text ->
                    Card {
                        Text(
                            text = safeLabel(text.take(2000)),
                            style = MaterialTheme.typography.bodyMedium,
                            modifier = Modifier.padding(14.dp),
                        )
                    }
                }
                if (payload.text.isNullOrBlank()) {
                    Text(
                        "没有可分享的文字或文件。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Button(onClick = { activity.finish() }, modifier = Modifier.fillMaxWidth()) {
                        Text("关闭")
                    }
                } else {
                    Text(
                        "文字始终保存到服务器，并实时同步给所有在线设备。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Button(
                        onClick = {
                            val text = payload.text ?: return@Button
                            error = null
                            starting = true
                            scope.launch {
                                runCatching { app.cloudRepository.postText(text) }
                                    .onSuccess {
                                        Toast.makeText(activity, "文字已保存到服务器", Toast.LENGTH_SHORT).show()
                                        activity.finish()
                                    }
                                    .onFailure { failure ->
                                        error = failure.message ?: "保存失败"
                                        starting = false
                                    }
                            }
                        },
                        modifier = Modifier.fillMaxWidth(),
                        enabled = app.settings.isConfigured && !starting,
                    ) {
                        Text("保存文字到服务器")
                    }
                    if (!app.settings.isConfigured) {
                        OutlinedButton(
                            onClick = { context.startActivity(Intent(context, MainActivity::class.java)) },
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Text("完成服务器与证书设置")
                        }
                    }
                    if (starting) {
                        LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                    }
                    error?.let { message ->
                        Text(message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                    }
                }
                return@Column
            }

            Text(
                text = "${payload.files.size} 个文件 · ${TransferNotifications.formatBytes(totalBytes)}",
                style = MaterialTheme.typography.titleMedium,
            )

            Card {
                Column(modifier = Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    payload.files.forEach { file ->
                        Column {
                            Text(
                                text = safeLabel(file.displayName),
                                style = MaterialTheme.typography.bodyLarge,
                                maxLines = 2,
                            )
                            Text(
                                text = if (file.sizeBytes >= 0) {
                                    "${file.mimeType} · ${TransferNotifications.formatBytes(file.sizeBytes)}"
                                } else {
                                    "${file.mimeType} · 大小未知"
                                },
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }

            if (hasUnknownSize) {
                Text(
                    "存在大小未知的文件，无法建立 P2P 传输。请换用其他方式分享该文件。",
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                )
            }

            HorizontalDivider()

            Text("发送方式", style = MaterialTheme.typography.titleMedium)
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.fillMaxWidth()) {
                ModeCard(
                    title = "保存到服务器",
                    body = "永久保留 · 所有设备可见",
                    selected = mode == ShareMode.CLOUD.name,
                    enabled = cloudEligible,
                    modifier = Modifier.weight(1f),
                    onClick = { mode = ShareMode.CLOUD.name },
                )
                ModeCard(
                    title = "P2P 直连",
                    body = "不经过服务器存储",
                    selected = mode == ShareMode.P2P.name,
                    enabled = true,
                    modifier = Modifier.weight(1f),
                    onClick = { mode = ShareMode.P2P.name },
                )
            }
            if (!cloudEligible) {
                Text(
                    "存在超过阈值（${TransferNotifications.formatBytes(thresholdBytes)}）或大小未知的文件，" +
                        "无法保存到服务器，只能使用 P2P。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            if (mode == ShareMode.P2P.name) {
                Text("选择接收设备", style = MaterialTheme.typography.titleMedium)

            if (!app.settings.isConfigured) {
                Text(
                    "尚未配置服务器或授权客户端证书。",
                    style = MaterialTheme.typography.bodyMedium,
                )
                OutlinedButton(
                    onClick = {
                        context.startActivity(Intent(context, MainActivity::class.java))
                    },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("完成服务器与证书设置")
                }
            } else {
                when (val state = connection) {
                    ConnectionState.Connecting -> {
                        LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                        Text("正在连接控制面…", style = MaterialTheme.typography.bodySmall)
                    }

                    ConnectionState.NotConfigured -> {
                        Text("未配置控制面连接。", style = MaterialTheme.typography.bodySmall)
                    }

                    is ConnectionState.Error -> {
                        Text(
                            "连接失败：${state.message}",
                            color = MaterialTheme.colorScheme.error,
                            style = MaterialTheme.typography.bodySmall,
                        )
                        OutlinedButton(
                            onClick = { app.controlPlane.restart() },
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Text("重新连接")
                        }
                    }

                    is ConnectionState.Connected -> Unit
                }

                if (availableDevices.isEmpty()) {
                    Text(
                        "没有在线且支持安全 P2P 的设备。请确认另一台设备已登录 MoliSync。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    OutlinedButton(
                        onClick = { app.controlPlane.restart() },
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Text("刷新设备列表")
                    }
                } else {
                    Card {
                        Column {
                            availableDevices.forEachIndexed { index, device ->
                                DeviceRow(
                                    device = device,
                                    selected = device.deviceId == selectedDeviceId,
                                    onSelect = { selectedDeviceId = device.deviceId },
                                )
                                if (index != availableDevices.lastIndex) {
                                    HorizontalDivider()
                                }
                            }
                        }
                    }
                }
            }
            }

            error?.let { message ->
                Text(
                    message,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                )
            }

            if (starting) {
                if (mode == ShareMode.CLOUD.name) {
                    LinearProgressIndicator(
                        progress = { cloudProgress },
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Text(
                        "正在上传… ${(cloudProgress * 100).toInt()}%",
                        style = MaterialTheme.typography.bodySmall,
                    )
                } else {
                    LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                    Text("正在打开文件句柄…", style = MaterialTheme.typography.bodySmall)
                }
            }

            Button(
                onClick = {
                    if (mode == ShareMode.CLOUD.name) {
                        if (!cloudEligible) {
                            error = "文件超过同步阈值，无法保存到服务器"
                            return@Button
                        }
                        error = null
                        starting = true
                        cloudProgress = 0f
                        scope.launch {
                            runCatching {
                                uploadToCloud(app, activity, payload) { fraction ->
                                    activity.runOnUiThread { cloudProgress = fraction }
                                }
                            }.onSuccess {
                                Toast.makeText(activity, "已保存到服务器", Toast.LENGTH_SHORT).show()
                                activity.finish()
                            }.onFailure { failure ->
                                error = failure.message ?: "上传失败"
                                starting = false
                            }
                        }
                    } else {
                        val target = availableDevices.firstOrNull { it.deviceId == selectedDeviceId }
                        if (target == null) {
                            error = "请选择接收设备"
                            return@Button
                        }
                        error = null
                        starting = true
                        scope.launch {
                            startTransfer(
                                app = app,
                                activity = activity,
                                target = target,
                                payload = payload,
                                onError = { message ->
                                    error = message
                                    starting = false
                                },
                            )
                        }
                    }
                },
                modifier = Modifier.fillMaxWidth(),
                enabled = canStart,
            ) {
                Text(if (mode == ShareMode.CLOUD.name) "保存到服务器" else "开始传输")
            }

            Text(
                "文件将从系统 content:// 句柄流式读取，不会复制到应用缓存目录。" +
                    "传输由前台服务接管，可在通知栏查看速率与进度。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun DeviceRow(device: DeviceTarget, selected: Boolean, onSelect: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 8.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RadioButton(selected = selected, onClick = onSelect)
        Column(modifier = Modifier.padding(start = 4.dp)) {
            Text(device.name, style = MaterialTheme.typography.bodyLarge)
            Text(
                // A device without a certificate fingerprint can only be
                // reached through the server relay, because the peer cannot be
                // pinned during a direct TLS handshake.
                if (device.pinFingerprint.isBlank()) {
                    "${device.platform} · 经服务端中继传输"
                } else {
                    "${device.platform} · ${device.endpoints.size} 个 P2P 地址"
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun ModeCard(
    title: String,
    body: String,
    selected: Boolean,
    enabled: Boolean,
    modifier: Modifier = Modifier,
    onClick: () -> Unit,
) {
    Card(
        modifier = modifier.clickable(enabled = enabled, onClick = onClick),
        colors = CardDefaults.cardColors(
            containerColor = if (selected) MoliColors.Mint else MaterialTheme.colorScheme.surface,
        ),
        border = BorderStroke(
            if (selected) 2.dp else 1.dp,
            if (selected) MoliColors.Pine else MaterialTheme.colorScheme.outline,
        ),
    ) {
        Column(
            modifier = Modifier.padding(12.dp),
            verticalArrangement = Arrangement.spacedBy(3.dp),
        ) {
            Text(
                title,
                style = MaterialTheme.typography.bodyMedium,
                color = if (enabled) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                body,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (!enabled) {
                Text(
                    "超过阈值",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }
}

private suspend fun uploadToCloud(
    app: MoliSyncApp,
    activity: Activity,
    payload: SharePayload,
    onProgress: (Float) -> Unit,
) = withContext(Dispatchers.IO) {
    val files = payload.files
    files.forEachIndexed { index, file ->
        val kind = if (file.mimeType.startsWith("image/")) CloudKind.IMAGE else CloudKind.FILE
        val hash = FileDigest.blake3(activity, file.uri)
        app.cloudRepository.upload(
            kind = kind,
            displayName = file.displayName,
            mimeType = file.mimeType,
            size = file.sizeBytes,
            blake3 = hash,
            open = {
                activity.contentResolver.openInputStream(file.uri)
                    ?: throw IOException("无法打开 ${file.displayName}")
            },
            onProgress = { sent, total ->
                val fileFraction = if (total > 0) sent.toFloat() / total.toFloat() else 0f
                onProgress(((index + fileFraction) / files.size).coerceIn(0f, 1f))
            },
        )
    }
    onProgress(1f)
}

private suspend fun startTransfer(
    app: MoliSyncApp,
    activity: Activity,
    target: DeviceTarget,
    payload: SharePayload,
    onError: (String) -> Unit,
) {
    val descriptors = ArrayList<ParcelFileDescriptor>(payload.files.size)
    try {
        withContext(Dispatchers.IO) {
            payload.files.forEach { file ->
                val descriptor = activity.contentResolver.openFileDescriptor(file.uri, "r")
                    ?: throw IOException("无法打开 ${file.displayName}")
                descriptors.add(descriptor)
            }
        }

        // The server issues the transfer ticket and the freshest endpoint list
        // for both participants. No data leaves the app before this succeeds.
        val session = app.controlPlane.openSession(
            toDeviceId = target.deviceId,
            transferName = payload.files.firstOrNull()?.displayName,
        )
        val freshest = session.devices.firstOrNull { it.deviceId == target.deviceId } ?: target
        val selfId = (app.controlPlane.state.value as? ConnectionState.Connected)?.deviceId
            ?: app.settings.certificateFingerprint
            ?: throw IOException("本机身份不可用")

        val request = TransferRequest(
            targetDeviceId = target.deviceId,
            targetFingerprint = target.fingerprint.ifBlank { freshest.pinFingerprint },
            targetName = target.name,
            // Without a pinnable fingerprint the peer cannot be authenticated
            // on a direct TLS link, so the session uses the server relay.
            endpoints = if (target.pinFingerprint.isBlank()) {
                emptyList()
            } else {
                freshest.endpoints.map {
                    TransferEndpoint(
                        network = it.network,
                        ip = it.ip,
                        port = it.port,
                        priority = it.priority,
                    )
                }
            },
            files = payload.files.map {
                TransferFile(
                    uri = it.uri.toString(),
                    name = it.displayName,
                    size = it.sizeBytes,
                    mimeType = it.mimeType,
                )
            },
            sessionId = session.sessionId,
            ticket = session.ticket,
            relayUrl = session.relayUrl,
            serverUrl = app.settings.serverUrl,
            selfDeviceId = selfId,
            selfFingerprint = app.settings.certificateFingerprint,
            selfDeviceName = app.settings.deviceName,
        )
        request.validate()

        TransferService.start(activity.applicationContext, request, descriptors)
        Toast.makeText(activity, "已开始传输", Toast.LENGTH_SHORT).show()
        activity.finish()
    } catch (timeout: TimeoutCancellationException) {
        // A ticket timeout is a CancellationException, so it must be handled
        // before the cancellation branch or the screen would spin forever.
        closeDescriptors(descriptors)
        onError("协商传输会话超时，请确认对端在线后重试")
    } catch (cancellation: CancellationException) {
        closeDescriptors(descriptors)
        throw cancellation
    } catch (exception: Exception) {
        closeDescriptors(descriptors)
        onError(exception.message ?: "无法开始传输")
    }
}

private fun closeDescriptors(descriptors: List<ParcelFileDescriptor>) {
    descriptors.forEach { descriptor -> runCatching { descriptor.close() } }
}

private fun safeLabel(value: String): String =
    value.map { if (it.isISOControl()) ' ' else it }.joinToString("").trim().ifEmpty { "文件" }
