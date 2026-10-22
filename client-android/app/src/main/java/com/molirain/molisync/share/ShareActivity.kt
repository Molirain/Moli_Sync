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
import com.molirain.molisync.security.isCertificateFingerprint
import com.molirain.molisync.transfer.TransferEndpoint
import com.molirain.molisync.transfer.TransferFile
import com.molirain.molisync.transfer.TransferNotifications
import com.molirain.molisync.transfer.TransferRequest
import com.molirain.molisync.transfer.TransferService
import com.molirain.molisync.ui.theme.MoliSyncTheme
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
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

    val availableDevices = remember(devices) {
        devices.values
            .filter { it.endpoints.isNotEmpty() && isCertificateFingerprint(it.deviceId) }
            .sortedBy { it.name.lowercase() }
    }

    LaunchedEffect(Unit) {
        app.controlPlane.ensureStarted()
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
        availableDevices.any { it.deviceId == selectedDeviceId }

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
                Text("没有可传输的文件", style = MaterialTheme.typography.titleMedium)
                payload.text?.let { text ->
                    Text(
                        text = text.take(500),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    "当前版本通过系统分享接收文件与图片。纯文本同步请使用云同步入口。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Button(onClick = { activity.finish() }, modifier = Modifier.fillMaxWidth()) {
                    Text("关闭")
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

            error?.let { message ->
                Text(
                    message,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                )
            }

            if (starting) {
                LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                Text("正在打开文件句柄…", style = MaterialTheme.typography.bodySmall)
            }

            Button(
                onClick = {
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
                },
                modifier = Modifier.fillMaxWidth(),
                enabled = canStart,
            ) {
                Text("开始传输")
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
                "${device.platform} · ${device.endpoints.size} 个 P2P 地址",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
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

        val request = TransferRequest(
            targetDeviceId = target.deviceId,
            targetName = target.name,
            endpoints = target.endpoints.map {
                TransferEndpoint(
                    network = it.network,
                    ip = it.ip,
                    port = it.port,
                    priority = it.priority,
                )
            },
            files = payload.files.map {
                TransferFile(
                    uri = it.uri.toString(),
                    name = it.displayName,
                    size = it.sizeBytes,
                    mimeType = it.mimeType,
                )
            },
        )
        request.validate()

        TransferService.start(activity.applicationContext, request, descriptors)
        Toast.makeText(activity, "已开始传输", Toast.LENGTH_SHORT).show()
        activity.finish()
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
