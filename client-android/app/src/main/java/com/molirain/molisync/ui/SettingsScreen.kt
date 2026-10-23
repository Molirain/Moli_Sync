package com.molirain.molisync.ui

import android.app.Activity
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Slider
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.molirain.molisync.BuildConfig
import com.molirain.molisync.MoliSyncApp
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.ConnectionState
import com.molirain.molisync.data.UpdateCheckState
import com.molirain.molisync.transfer.TransferNotifications
import com.molirain.molisync.ui.theme.MoliColors
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlin.math.roundToInt

@Composable
fun SettingsScreen(
    app: MoliSyncApp,
    onBack: () -> Unit,
    onSignOut: () -> Unit,
) {
    val context = LocalContext.current
    val activity = context as? Activity
    val scope = rememberCoroutineScope()
    val viewModel: CloudViewModel = viewModel()
    val state by viewModel.state.collectAsStateWithLifecycle()
    val connection by app.controlPlane.state.collectAsStateWithLifecycle()
    val self by app.deviceDirectory.self.collectAsStateWithLifecycle()
    val others by app.deviceDirectory.devices.collectAsStateWithLifecycle()
    val updateCheck by app.updateCheckState.collectAsStateWithLifecycle()

    var thresholdMb by remember {
        mutableFloatStateOf(app.settings.cloudThresholdBytes / (1024f * 1024f))
    }
    var deviceName by remember { mutableStateOf(app.settings.deviceName) }
    val status = remember { mutableStateOf<String?>(null) }

    val maxThresholdMb = ClientSettings.MAX_CLOUD_THRESHOLD_BYTES / (1024f * 1024f)
    val minThresholdMb = ClientSettings.MIN_CLOUD_THRESHOLD_BYTES / (1024f * 1024f)
    BackHandler(onBack = onBack)

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        contentWindowInsets = WindowInsets.safeDrawing,
        topBar = {
            Surface(color = MoliColors.Pine, shape = RoundedCornerShape(bottomStart = 28.dp, bottomEnd = 28.dp)) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier
                        .fillMaxWidth()
                        .statusBarsPadding()
                        .padding(horizontal = 8.dp, vertical = 10.dp),
                ) {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Outlined.ArrowBack,
                            contentDescription = "返回",
                            tint = Color(0xFFF7F3E8),
                        )
                    }
                    Text(
                        "设置",
                        color = Color(0xFFF7F3E8),
                        fontSize = 20.sp,
                        fontWeight = FontWeight.Bold,
                    )
                    Spacer(Modifier.weight(1f))
                    OnlinePillSettings(
                        connected = connection is ConnectionState.Connected,
                        count = others.size + if (self != null) 1 else 0,
                    )
                    Spacer(Modifier.width(10.dp))
                }
            }
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 12.dp)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            SettingsCard {
                SettingRow("服务器", app.settings.serverUrl.orEmpty())
                HorizontalDivider(color = MaterialTheme.colorScheme.outline)
                SettingRow("本机", "${app.settings.deviceName} · ${if (self != null) "在线" else "离线"}")
                val fingerprint = app.settings.certificateFingerprint
                if (fingerprint != null) {
                    Text(
                        "SHA-256 ${fingerprint.chunked(4).take(6).joinToString(" ")}…",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outline)
                SettingRow(
                    "在线设备",
                    "${others.size + if (self != null) 1 else 0} 台（含本机）",
                )
                HorizontalDivider(color = MaterialTheme.colorScheme.outline)
                SettingRow("控制面", connectionLabel(connection))
            }

            SettingsCard {
                Text(
                    "设备名称",
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.SemiBold,
                )
                OutlinedTextField(
                    value = deviceName,
                    onValueChange = { if (it.length <= 64) deviceName = it },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    placeholder = { Text("其他设备看到的名字") },
                )
                Text(
                    "这个名称会显示给其他在线设备，最多 64 个字符。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Button(
                    onClick = {
                        val saved = app.settings.saveDeviceName(deviceName)
                        deviceName = app.settings.deviceName
                        if (saved) {
                            app.controlPlane.restart()
                            status.value = "设备名称已更新，正在重新注册"
                        } else {
                            status.value = "无法保存设备名称"
                        }
                    },
                    enabled = deviceName.isNotBlank() && deviceName != app.settings.deviceName,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("保存名称")
                }
            }

            SettingsCard {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("同步阈值", style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold)
                    Spacer(Modifier.weight(1f))
                    Text(
                        "${thresholdMb.roundToInt()} MB",
                        style = MaterialTheme.typography.bodyLarge,
                        fontWeight = FontWeight.Bold,
                        color = MoliColors.Pine,
                    )
                }
                Slider(
                    value = thresholdMb,
                    onValueChange = { thresholdMb = it },
                    valueRange = minThresholdMb..maxThresholdMb,
                    steps = (maxThresholdMb - minThresholdMb).roundToInt() - 1,
                    onValueChangeFinished = {
                        app.settings.saveCloudThreshold((thresholdMb * 1024f * 1024f).toLong())
                        status.value = "阈值已保存为 ${thresholdMb.roundToInt()} MB"
                    },
                )
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Text("${minThresholdMb.roundToInt()} MB", style = MaterialTheme.typography.labelSmall)
                    Text("${maxThresholdMb.roundToInt()} MB", style = MaterialTheme.typography.labelSmall)
                }
                Text(
                    "小于或等于阈值的图片/文件可以保存到服务器并广播给所有设备；超过阈值的项目只走 P2P。" +
                        "文字始终保存到服务器。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            SettingsCard {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("云端存储", style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold)
                    Spacer(Modifier.weight(1f))
                    Text(
                        "${TransferNotifications.formatBytes(state.cloudBytes)} · ${state.records.size} 条",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                LinearProgressIndicator(
                    progress = {
                        (state.cloudBytes.toFloat() / (1024f * 1024f * 1024f)).coerceIn(0f, 1f)
                    },
                    modifier = Modifier.fillMaxWidth(),
                    color = MoliColors.PineSoft,
                )
                Text(
                    "记录保存在 Docker 卷中，容器重建不会丢失。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            OutlinedButton(
                onClick = {
                    val serverUrl = app.settings.serverUrl
                    if (serverUrl == null || activity == null) {
                        status.value = "尚未配置服务器"
                        return@OutlinedButton
                    }
                    app.keyChain.chooseAlias(activity, Uri.parse(serverUrl)) { result ->
                        result.onFailure { failure -> status.value = failure.message ?: "无法打开证书选择器" }
                        result.onSuccess { alias ->
                            if (alias == null) {
                                status.value = "已取消证书选择"
                                return@onSuccess
                            }
                            scope.launch {
                                runCatching {
                                    withContext(Dispatchers.IO) { app.keyChain.loadIdentity(alias) }
                                }.onSuccess { identity ->
                                    app.settings.saveCertificate(alias, identity.deviceId)
                                                app.cloudRepository.close()
                                                app.restartTransport()
                                                status.value = "证书已更新，正在重连"
                                }.onFailure { failure ->
                                                status.value = failure.message ?: "证书不可用"
                                }
                            }
                        }
                    }
                },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("重新授权客户端证书")
            }

            OutlinedButton(
                onClick = { viewModel.refresh() },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("刷新记录")
            }

            Button(
                onClick = {
                    app.settings.clear()
                    app.cloudRepository.close()
                    app.controlPlane.stop()
                    onSignOut()
                },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("断开并清除设置")
            }

            status.value?.let { message ->
                Text(
                    message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            Text(
                "MoliSync ${BuildConfig.VERSION_NAME} · 私钥始终留在系统 KeyChain",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(bottom = 8.dp),
            )

            OutlinedButton(
                onClick = { app.checkForUpdates(force = true) },
                enabled = updateCheck != UpdateCheckState.Checking,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("检查更新")
            }
            UpdateStatusLine(updateCheck)
        }
    }
}

@Composable
private fun SettingsCard(content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit) {
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
        shape = RoundedCornerShape(18.dp),
    ) {
        Column(
            modifier = Modifier.padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
            content = content,
        )
    }
}

@Composable
private fun SettingRow(label: String, value: String) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(label, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold)
        Spacer(Modifier.weight(1f))
        Text(
            value,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun OnlinePillSettings(connected: Boolean, count: Int) {
    Surface(
        color = Color(0x24CFE9DC),
        shape = CircleShape,
        border = BorderStroke(1.dp, Color(0x47CFE9DC)),
    ) {
        Text(
            if (connected) "$count 在线" else "离线",
            color = Color(0xFFDDF3E7),
            fontSize = 11.sp,
            fontWeight = FontWeight.SemiBold,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 5.dp),
        )
    }
}

private fun connectionLabel(connection: ConnectionState): String = when (connection) {
    ConnectionState.NotConfigured -> "未配置"
    ConnectionState.Connecting -> "正在连接"
    is ConnectionState.Connected -> "已连接"
    is ConnectionState.Error -> "失败：${connection.message}"
}
