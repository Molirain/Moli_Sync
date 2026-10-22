package com.molirain.molisync.ui

import android.app.Activity
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
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
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.molirain.molisync.MoliSyncApp
import com.molirain.molisync.data.ClientSettings
import com.molirain.molisync.data.ConnectionState
import com.molirain.molisync.ui.theme.MoliColors
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun SetupScreen(app: MoliSyncApp, onConfigured: () -> Unit) {
    val context = LocalContext.current
    val activity = context as? Activity
    val scope = rememberCoroutineScope()

    var serverUrl by rememberSaveable { mutableStateOf(app.settings.serverUrl.orEmpty()) }
    var alias by rememberSaveable { mutableStateOf(app.settings.certificateAlias) }
    var fingerprint by rememberSaveable { mutableStateOf(app.settings.certificateFingerprint) }
    var status by rememberSaveable { mutableStateOf<String?>(null) }
    var selecting by remember { mutableStateOf(false) }

    val connection by app.controlPlane.state.collectAsStateWithLifecycle()

    Scaffold(
        contentWindowInsets = WindowInsets.safeDrawing,
        containerColor = MaterialTheme.colorScheme.background,
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp, vertical = 18.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                androidx.compose.foundation.Image(
                    painter = androidx.compose.ui.res.painterResource(com.molirain.molisync.R.drawable.ic_launcher_foreground),
                    contentDescription = null,
                    modifier = Modifier
                        .padding(end = 10.dp)
                        .size(40.dp),
                )
                Column {
                    Text("MoliSync", style = MaterialTheme.typography.headlineSmall)
                    Text(
                        "安全优先的跨端同步",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }

            Text(
                "连接服务器",
                style = MaterialTheme.typography.headlineMedium,
                color = MoliColors.Pine,
            )
            Text(
                "首次使用需要输入服务器地址，并通过 Android 系统证书选择器授权客户端证书。私钥始终保存在系统 KeyChain 中。",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)) {
                Column(
                    modifier = Modifier.padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(14.dp),
                ) {
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
                                result.onFailure { failure ->
                                    selecting = false
                                    status = failure.message ?: "无法打开证书选择器"
                                }
                                result.onSuccess { chosen ->
                                    if (chosen == null) {
                                        selecting = false
                                        status = "已取消证书选择"
                                        return@onSuccess
                                    }
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
                        Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
                            Text("已授权证书", style = MaterialTheme.typography.labelLarge)
                            Text(authorizedAlias, style = MaterialTheme.typography.bodyMedium)
                            fingerprint?.let { value ->
                                Text(
                                    "SHA-256 ${value.take(24)}…",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
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
                        onConfigured()
                    }
                },
                modifier = Modifier.fillMaxWidth(),
                enabled = !selecting,
            ) {
                Text("保存并连接")
            }

            if (selecting || connection is ConnectionState.Connecting) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    CircularProgressIndicator(modifier = Modifier.padding(2.dp))
                    Text("正在处理…", style = MaterialTheme.typography.bodySmall)
                }
            }

            connectionErrorMessage(connection)?.let { message ->
                Text(message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            status?.let { message ->
                Text(
                    message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

private fun connectionErrorMessage(connection: ConnectionState): String? =
    (connection as? ConnectionState.Error)?.message
