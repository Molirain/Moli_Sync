package com.molirain.molisync

import android.Manifest
import android.app.Activity
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
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
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.ConnectionState
import com.molirain.molisync.ui.theme.MoliSyncTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val app = application as MoliSyncApp
        setContent {
            MoliSyncTheme {
                SetupScreen(app)
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SetupScreen(app: MoliSyncApp) {
    val context = LocalContext.current
    val activity = context as? Activity
    val scope = rememberCoroutineScope()

    var serverUrl by rememberSaveable { mutableStateOf(app.settings.serverUrl.orEmpty()) }
    var alias by rememberSaveable { mutableStateOf(app.settings.certificateAlias) }
    var fingerprint by rememberSaveable { mutableStateOf(app.settings.certificateFingerprint) }
    var status by rememberSaveable { mutableStateOf<String?>(null) }
    var selecting by remember { mutableStateOf(false) }

    val connection by app.controlPlane.state.collectAsStateWithLifecycle()
    val devices by app.deviceDirectory.devices.collectAsStateWithLifecycle()

    val notificationPermission = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { }

    LaunchedEffect(Unit) {
        app.controlPlane.ensureStarted()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(
                context,
                Manifest.permission.POST_NOTIFICATIONS,
            ) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    Scaffold(
        contentWindowInsets = WindowInsets.safeDrawing,
        topBar = { TopAppBar(title = { Text("MoliSync") }) },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Text(
                text = "连接服务器",
                style = MaterialTheme.typography.headlineSmall,
            )
            Text(
                text = "首次使用需要输入服务器地址，并通过 Android 系统证书选择器授权客户端证书。私钥始终保存在系统 KeyChain 中。",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            OutlinedTextField(
                value = serverUrl,
                onValueChange = { serverUrl = it },
                modifier = Modifier.fillMaxWidth(),
                label = { Text("服务器 URL") },
                placeholder = { Text("https://sync.example.com") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            )

            OutlinedButton(
                onClick = {
                    val normalized = ClientSettings.normalizeServerUrl(serverUrl)
                    if (normalized == null) {
                        status = "请输入有效的 https:// 服务器地址"
                        return@OutlinedButton
                    }
                    if (activity == null) {
                        status = "当前界面无法打开系统证书选择器"
                        return@OutlinedButton
                    }
                    selecting = true
                    app.keyChain.chooseAlias(activity, Uri.parse(normalized)) { result ->
                        selecting = false
                        result.onFailure { failure ->
                            status = failure.message ?: "无法打开证书选择器"
                        }
                        result.onSuccess { chosen ->
                            if (chosen == null) {
                                status = "已取消证书选择"
                                return@onSuccess
                            }
                            selecting = true
                            scope.launch {
                                runCatching {
                                    withContext(Dispatchers.IO) { app.keyChain.loadIdentity(chosen) }
                                }.onSuccess { identity ->
                                    alias = chosen
                                    fingerprint = identity.deviceId
                                    app.settings.saveCertificate(chosen, identity.deviceId)
                                    status = "证书已授权：${identity.deviceId.take(16)}…"
                                }.onFailure { failure ->
                                    status = failure.message ?: "证书不可用"
                                }
                                selecting = false
                            }
                        }
                    }
                },
                modifier = Modifier.fillMaxWidth(),
                enabled = !selecting,
            ) {
                Text(if (alias == null) "选择客户端证书" else "重新选择客户端证书")
            }

            val authorizedAlias = alias
            if (authorizedAlias != null) {
                Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant)) {
                    Column(modifier = Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text("已授权证书", style = MaterialTheme.typography.labelLarge)
                        Text(authorizedAlias, style = MaterialTheme.typography.bodyMedium)
                        if (fingerprint != null) {
                            Text(
                                "SHA-256 ${fingerprint!!.take(24)}…",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }

            Button(
                onClick = {
                    val normalized = ClientSettings.normalizeServerUrl(serverUrl)
                    if (normalized == null) {
                        status = "请输入有效的 https:// 服务器地址"
                        return@Button
                    }
                    val currentAlias = alias
                    if (currentAlias == null) {
                        status = "请先选择客户端证书"
                        return@Button
                    }
                    scope.launch {
                        val currentFingerprint = fingerprint ?: runCatching {
                            withContext(Dispatchers.IO) { app.keyChain.loadIdentity(currentAlias).deviceId }
                        }.getOrNull()
                        if (currentFingerprint == null) {
                            status = "无法读取证书，请重新选择"
                            return@launch
                        }
                        if (!app.settings.saveConnection(normalized, currentAlias, currentFingerprint)) {
                            status = "保存设置失败"
                            return@launch
                        }
                        serverUrl = normalized
                        fingerprint = currentFingerprint
                        status = "设置已保存，正在连接控制面…"
                        app.controlPlane.restart()
                    }
                },
                modifier = Modifier.fillMaxWidth(),
                enabled = !selecting,
            ) {
                Text("保存并连接")
            }

            if (selecting) {
                LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
            }

            status?.let { message ->
                Text(
                    text = message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            HorizontalDivider()

            Text("连接状态", style = MaterialTheme.typography.titleMedium)
            ConnectionStatusCard(connection = connection, onlineDevices = devices.size)

            if (devices.isNotEmpty()) {
                Text("在线设备", style = MaterialTheme.typography.titleMedium)
                devices.values.sortedBy { it.name }.forEach { device ->
                    Card(modifier = Modifier.fillMaxWidth()) {
                        Column(modifier = Modifier.padding(14.dp)) {
                            Row(
                                modifier = Modifier.fillMaxWidth(),
                                horizontalArrangement = Arrangement.SpaceBetween,
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Text(device.name, style = MaterialTheme.typography.bodyLarge)
                                Text(
                                    device.platform,
                                    style = MaterialTheme.typography.labelMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            Text(
                                if (device.endpoints.isEmpty()) {
                                    "无可用 P2P 地址"
                                } else {
                                    "${device.endpoints.size} 个可用 P2P 地址"
                                },
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }

            Spacer(modifier = Modifier.height(8.dp))
            Text(
                text = "在系统图库或文件管理器中选中文件，点击分享并选择 MoliSync 即可发送。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun ConnectionStatusCard(connection: ConnectionState, onlineDevices: Int) {
    val (label, color) = when (connection) {
        ConnectionState.NotConfigured ->
            "未配置" to MaterialTheme.colorScheme.onSurfaceVariant
        ConnectionState.Connecting ->
            "正在连接…" to MaterialTheme.colorScheme.primary
        is ConnectionState.Connected ->
            "已连接 · ${onlineDevices} 台在线设备" to MaterialTheme.colorScheme.primary
        is ConnectionState.Error ->
            "连接失败：${connection.message}" to MaterialTheme.colorScheme.error
    }

    Card(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
    ) {
        Column(modifier = Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(label, color = color, style = MaterialTheme.typography.bodyMedium)
            if (connection is ConnectionState.Connected) {
                Text(
                    "设备 ID ${connection.deviceId.take(24)}…",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}
