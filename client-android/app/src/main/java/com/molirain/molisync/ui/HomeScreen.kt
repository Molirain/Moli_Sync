package com.molirain.molisync.ui

import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items as gridItems
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Notes
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Cloud
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.SwapHoriz
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.molirain.molisync.BuildConfig
import androidx.lifecycle.viewmodel.compose.viewModel
import com.molirain.molisync.MoliSyncApp
import com.molirain.molisync.data.ConnectionState
import com.molirain.molisync.data.cloud.CloudKind
import com.molirain.molisync.data.cloud.CloudRecord
import com.molirain.molisync.share.ShareActivity
import com.molirain.molisync.share.ShareParser
import com.molirain.molisync.share.SharedFile
import com.molirain.molisync.transfer.TransferNotifications
import com.molirain.molisync.ui.theme.MoliColors
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private enum class MoliTab(
    val label: String,
    val title: String,
    val subtitle: String,
    val icon: ImageVector,
    val addLabel: String,
) {
    TEXT("文字", "文字", "永久保存在服务器，所有设备实时可见", Icons.AutoMirrored.Outlined.Notes, "写文字"),
    IMAGES("图片", "图片", "缩略图常驻云端，原图按需下载", Icons.Outlined.Image, "添加图片"),
    FILES("文件", "文件", "断点续传，完成前不占用正式文件名", Icons.Outlined.Folder, "添加文件"),
}

private data class PendingSelection(
    val uri: Uri,
    val file: SharedFile,
    val kind: String,
)

@Composable
fun HomeScreen(app: MoliSyncApp, onSettings: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val viewModel: CloudViewModel = viewModel()
    val state by viewModel.state.collectAsStateWithLifecycle()
    val connection by app.controlPlane.state.collectAsStateWithLifecycle()
    val self by app.deviceDirectory.self.collectAsStateWithLifecycle()
    val others by app.deviceDirectory.devices.collectAsStateWithLifecycle()
    val availableUpdate by app.pendingUpdate.collectAsStateWithLifecycle()

    var tab by rememberSaveable { mutableStateOf(MoliTab.TEXT.name) }
    val selectedTab = MoliTab.valueOf(tab)
    var showComposer by remember { mutableStateOf(false) }
    var composerText by remember { mutableStateOf("") }
    var pending by remember { mutableStateOf<PendingSelection?>(null) }
    var deleteTarget by remember { mutableStateOf<CloudRecord?>(null) }
    var textDetail by remember { mutableStateOf<CloudRecord?>(null) }
    var actionsTarget by remember { mutableStateOf<CloudRecord?>(null) }
    var showDevices by remember { mutableStateOf(false) }
    var deviceDetail by remember { mutableStateOf<com.molirain.molisync.data.DeviceTarget?>(null) }
    val snackbarHostState = remember { SnackbarHostState() }

    val threshold = app.settings.cloudThresholdBytes
    val connected = connection is ConnectionState.Connected
    val onlineCount = others.size + if (self != null) 1 else 0
    val p2pTargets = others.values.count { it.endpoints.isNotEmpty() }

    LaunchedEffect(state.message) {
        val message = state.message
        if (message != null) {
            snackbarHostState.showSnackbar(message)
            viewModel.consumeMessage()
        }
    }

    val imagePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        handlePicked(context, scope, uri, CloudKind.IMAGE) { pending = it }
    }
    val filePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        handlePicked(context, scope, uri, CloudKind.FILE) { pending = it }
    }

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        topBar = {
            HeroHeader(
                tab = selectedTab,
                recordCount = state.records.size,
                onlineCount = onlineCount,
                connected = connected,
                refreshing = state.refreshing,
                onRefresh = { viewModel.refresh(silent = true) },
                onSettings = onSettings,
                onDevices = { showDevices = true },
            )
        },
        bottomBar = {
            BottomBar(
                selected = selectedTab,
                onSelect = { tab = it.name },
            )
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = {
                    when (selectedTab) {
                        MoliTab.TEXT -> {
                            composerText = ""
                            showComposer = true
                        }
                        MoliTab.IMAGES -> imagePicker.launch(arrayOf("image/*"))
                        MoliTab.FILES -> filePicker.launch(arrayOf("*/*"))
                    }
                },
                icon = { Icon(Icons.Outlined.Add, contentDescription = null) },
                text = { Text(selectedTab.addLabel) },
                containerColor = MoliColors.Pine,
                contentColor = Color(0xFFFFF9E9),
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            when (selectedTab) {
                MoliTab.TEXT -> TextTab(
                    records = state.ofKind(CloudKind.TEXT),
                    selfDeviceId = self?.deviceId,
                    onOpen = { textDetail = it },
                    onCopy = { record ->
                        copyToClipboard(context, record.text)
                        scope.launch { snackbarHostState.showSnackbar("已复制到剪贴板") }
                    },
                    onDelete = { deleteTarget = it },
                )

                MoliTab.IMAGES -> ImageTab(
                    records = state.ofKind(CloudKind.IMAGE),
                    downloading = state.downloading,
                    downloaded = state.downloaded,
                    onOpen = viewModel::open,
                    onActions = { actionsTarget = it },
                )

                MoliTab.FILES -> FileTab(
                    records = state.ofKind(CloudKind.FILE),
                    downloading = state.downloading,
                    downloaded = state.downloaded,
                    onOpen = viewModel::open,
                    onActions = { actionsTarget = it },
                )
            }

            state.error?.let { message ->
                Surface(
                    color = MaterialTheme.colorScheme.errorContainer,
                    shape = RoundedCornerShape(14.dp),
                    modifier = Modifier.fillMaxWidth().padding(16.dp),
                ) {
                    Text(
                        message,
                        color = MaterialTheme.colorScheme.onErrorContainer,
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.padding(12.dp),
                    )
                }
            }
        }
    }

    availableUpdate?.let { update ->
        AlertDialog(
            onDismissRequest = { app.dismissUpdate(update) },
            title = { Text("发现新版本 ${update.version}") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(
                        "当前版本 ${BuildConfig.VERSION_NAME}",
                        style = MaterialTheme.typography.bodySmall,
                    )
                    update.notes?.let { notes ->
                        Text(
                            notes.take(400),
                            style = MaterialTheme.typography.bodySmall,
                            maxLines = 8,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        openUrl(context, update.url)
                        app.dismissUpdate(update)
                    },
                ) { Text("前往下载") }
            },
            dismissButton = {
                TextButton(onClick = { app.dismissUpdate(update) }) { Text("稍后") }
            },
        )
    }

    if (showComposer) {
        TextComposerDialog(            value = composerText,
            onValueChange = { composerText = it },
            onDismiss = { showComposer = false },
            onSend = {
                viewModel.sendText(composerText)
                composerText = ""
                showComposer = false
            },
        )
    }

    pending?.let { selection ->
        SendChoiceDialog(
            selection = selection,
            thresholdBytes = threshold,
            p2pAvailable = p2pTargets > 0,
            onCloud = {
                viewModel.upload(selection.uri, selection.file, selection.kind)
                pending = null
            },
            onP2P = {
                startP2p(context, selection.uri, selection.file.mimeType)
                pending = null
            },
            onDismiss = { pending = null },
        )
    }

    state.uploading?.let { uploading ->
        AlertDialog(
            onDismissRequest = {},
            title = { Text("正在上传") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text(uploading.name, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    LinearProgressIndicator(
                        progress = { uploading.fraction },
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Text(
                        "${TransferNotifications.formatBytes(uploading.sent)} / " +
                            TransferNotifications.formatBytes(uploading.total),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            },
            confirmButton = {},
        )
    }

    deleteTarget?.let { target ->
        AlertDialog(
            onDismissRequest = { deleteTarget = null },
            title = { Text("从服务器删除？") },
            text = {
                Text(
                    if (target.isText) "这条文字记录将从服务器永久删除。" else
                        "“${target.displayName}”及其云端文件将被永久删除。",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.delete(target)
                    deleteTarget = null
                }) { Text("删除", color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = {
                TextButton(onClick = { deleteTarget = null }) { Text("取消") }
            },
        )
    }

    textDetail?.let { record ->
        TextDetailDialog(
            record = record,
            canDelete = record.senderDeviceId == self?.deviceId,
            onCopy = {
                copyToClipboard(context, record.text)
                scope.launch { snackbarHostState.showSnackbar("已复制全文") }
            },
            onDelete = {
                textDetail = null
                deleteTarget = record
            },
            onDismiss = { textDetail = null },
        )
    }

    actionsTarget?.let { record ->
        ItemActionsDialog(
            record = record,
            isDownloaded = state.downloaded.containsKey(record.recordId),
            onOpen = {
                viewModel.open(record)
                actionsTarget = null
            },
            onReveal = {
                viewModel.revealInFileManager(record)
                actionsTarget = null
            },
            onDelete = {
                actionsTarget = null
                deleteTarget = record
            },
            onDismiss = { actionsTarget = null },
        )
    }

    if (showDevices) {
        DeviceListDialog(
            self = self,
            others = others.values.sortedBy { it.name.lowercase() },
            onPick = {
                deviceDetail = it
                showDevices = false
            },
            onDismiss = { showDevices = false },
        )
    }

    deviceDetail?.let { device ->
        DeviceDetailDialog(device = device, onDismiss = { deviceDetail = null })
    }
}

@Composable
private fun TextDetailDialog(
    record: CloudRecord,
    canDelete: Boolean,
    onCopy: () -> Unit,
    onDelete: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("文字详情") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    "${record.senderName.ifBlank { "未命名设备" }} · ${formatTimestamp(record.createdAt)}" +
                        " · ${record.text.length} 字符",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                SelectionContainer {
                    Text(
                        record.text,
                        style = MaterialTheme.typography.bodyLarge,
                        modifier = Modifier
                            .heightIn(max = 380.dp)
                            .verticalScroll(rememberScrollState()),
                    )
                }
                Text(
                    "长按文字可选取任意片段。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            Row {
                TextButton(onClick = onCopy) { Text("复制全文") }
                TextButton(onClick = onDismiss) { Text("关闭") }
            }
        },
        dismissButton = if (canDelete) {
            { TextButton(onClick = onDelete) { Text("删除", color = MaterialTheme.colorScheme.error) } }
        } else {
            null
        },
    )
}

@Composable
private fun ItemActionsDialog(
    record: CloudRecord,
    isDownloaded: Boolean,
    onOpen: () -> Unit,
    onReveal: () -> Unit,
    onDelete: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(record.displayName, maxLines = 2, overflow = TextOverflow.Ellipsis) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(
                    if (isDownloaded) "已保存到 ${if (record.kind == CloudKind.IMAGE) "Pictures/MoliSync" else "Download/MoliSync"}"
                    else "尚未下载，点“打开”会下载到 MoliSync 相册/文件夹。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                TextButton(onClick = onOpen, modifier = Modifier.fillMaxWidth()) {
                    Text("打开")
                }
                TextButton(onClick = onReveal, modifier = Modifier.fillMaxWidth()) {
                    Text("在文件管理器打开所在文件夹")
                }
                TextButton(onClick = onDelete, modifier = Modifier.fillMaxWidth()) {
                    Text("从服务器删除", color = MaterialTheme.colorScheme.error)
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

@Composable
private fun DeviceListDialog(
    self: com.molirain.molisync.data.DeviceTarget?,
    others: List<com.molirain.molisync.data.DeviceTarget>,
    onPick: (com.molirain.molisync.data.DeviceTarget) -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("在线设备") },
        text = {
            Column(
                verticalArrangement = Arrangement.spacedBy(6.dp),
                modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
            ) {
                if (self == null) {
                    Text("本机尚未注册到控制面。", style = MaterialTheme.typography.bodySmall)
                } else {
                    DeviceRow(target = self, isSelf = true, onClick = { onPick(self) })
                }
                if (others.isEmpty()) {
                    Text(
                        "没有其他设备在线。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                } else {
                    others.forEach { device ->
                        DeviceRow(target = device, isSelf = false, onClick = { onPick(device) })
                    }
                }
                Text(
                    "点击设备可查看它上报的 LAN / VPN 候选地址。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("关闭") } },
    )
}

@Composable
private fun DeviceRow(
    target: com.molirain.molisync.data.DeviceTarget,
    isSelf: Boolean,
    onClick: () -> Unit,
) {
    Surface(
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(12.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
        modifier = Modifier.fillMaxWidth().clickable(onClick = onClick),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
        ) {
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    if (isSelf) "${target.name}（本机）" else target.name,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    "${if (target.platform == "android") "Android" else target.platform} · " +
                        "${target.endpoints.size} 个地址",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Text("›", fontSize = 18.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun DeviceDetailDialog(
    device: com.molirain.molisync.data.DeviceTarget,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(device.name) },
        text = {
            Column(
                verticalArrangement = Arrangement.spacedBy(6.dp),
                modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
            ) {
                Text(
                    "平台：${device.platform}",
                    style = MaterialTheme.typography.bodySmall,
                )
                Text(
                    "DeviceID：${device.deviceId}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (device.endpoints.isEmpty()) {
                    Text(
                        "对端尚未上报候选地址，P2P 直连不可用。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                } else {
                    device.endpoints.forEach { endpoint ->
                        Surface(
                            color = MoliColors.Mint,
                            shape = RoundedCornerShape(9.dp),
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Row(modifier = Modifier.padding(10.dp)) {
                                Text(
                                    if (endpoint.network == "lan") "局域网" else "VPN",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MoliColors.Pine,
                                    fontWeight = FontWeight.SemiBold,
                                    modifier = Modifier.width(56.dp),
                                )
                                Text(
                                    "${endpoint.ip}:${endpoint.port}",
                                    style = MaterialTheme.typography.bodySmall,
                                )
                            }
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("关闭") } },
    )
}

@Composable
private fun HeroHeader(
    tab: MoliTab,
    recordCount: Int,
    onlineCount: Int,
    connected: Boolean,
    refreshing: Boolean,
    onRefresh: () -> Unit,
    onSettings: () -> Unit,
    onDevices: () -> Unit,
) {
    Surface(
        color = MoliColors.Pine,
        shape = RoundedCornerShape(bottomStart = 28.dp, bottomEnd = 28.dp),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .statusBarsPadding()
                .padding(start = 18.dp, end = 12.dp, top = 10.dp, bottom = 18.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Surface(
                    color = Color(0xFFFFF9E9),
                    shape = RoundedCornerShape(11.dp),
                    modifier = Modifier.size(34.dp),
                ) {
                    Icon(
                        painter = androidx.compose.ui.res.painterResource(
                            com.molirain.molisync.R.drawable.ic_launcher_foreground,
                        ),
                        contentDescription = null,
                        tint = Color.Unspecified,
                        modifier = Modifier.padding(4.dp),
                    )
                }
                Spacer(Modifier.width(10.dp))
                Text(
                    "MoliSync",
                    color = Color(0xFFF7F3E8),
                    fontSize = 19.sp,
                    fontWeight = FontWeight.Bold,
                )
                Spacer(Modifier.weight(1f))
                OnlinePill(count = onlineCount, connected = connected, onClick = onDevices)
                IconButton(onClick = onSettings) {
                    Icon(
                        Icons.Outlined.Settings,
                        contentDescription = "设置",
                        tint = Color(0xFFEAF3ED),
                    )
                }
            }
            Spacer(Modifier.height(14.dp))
            Text(
                tab.title,
                color = Color(0xFFF7F3E8),
                fontSize = 26.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = (-0.5).sp,
            )
            Text(
                tab.subtitle,
                color = Color(0xFFBFD6CB),
                fontSize = 12.sp,
            )
            Spacer(Modifier.height(12.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    "$recordCount 条记录",
                    color = Color(0xFFD6E9DF),
                    fontSize = 11.sp,
                    modifier = Modifier
                        .background(Color(0x14FFFFFF), CircleShape)
                        .padding(horizontal = 10.dp, vertical = 4.dp),
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    if (connected) "端到端 mTLS" else "控制面未连接",
                    color = Color(0xFF9FBCAE),
                    fontSize = 11.sp,
                )
                Spacer(Modifier.weight(1f))
                if (refreshing) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(18.dp),
                        strokeWidth = 2.dp,
                        color = Color(0xFFCFE9DC),
                    )
                } else {
                    IconButton(onClick = onRefresh, modifier = Modifier.size(32.dp)) {
                        Icon(
                            Icons.Outlined.Refresh,
                            contentDescription = "刷新",
                            tint = Color(0xFFD6E9DF),
                            modifier = Modifier.size(18.dp),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun OnlinePill(count: Int, connected: Boolean, onClick: (() -> Unit)? = null) {
    Surface(
        color = Color(0x24CFE9DC),
        shape = CircleShape,
        border = BorderStroke(1.dp, Color(0x47CFE9DC)),
        modifier = if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier,
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 5.dp),
        ) {
            Box(
                modifier = Modifier
                    .size(6.dp)
                    .background(
                        if (connected) MoliColors.MintStrong else Color(0xFFD99A3E),
                        CircleShape,
                    ),
            )
            Spacer(Modifier.width(6.dp))
            Text(
                if (connected) "$count 在线" else "离线",
                color = Color(0xFFDDF3E7),
                fontSize = 11.sp,
                fontWeight = FontWeight.SemiBold,
            )
        }
    }
}

@Composable
private fun BottomBar(selected: MoliTab, onSelect: (MoliTab) -> Unit) {
    Surface(
        color = MaterialTheme.colorScheme.surface,
        shape = RoundedCornerShape(22.dp),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
        shadowElevation = 8.dp,
        modifier = Modifier
            .navigationBarsPadding()
            .padding(horizontal = 14.dp, vertical = 10.dp),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().height(58.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            MoliTab.entries.forEach { tab ->
                val active = tab == selected
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                    modifier = Modifier
                        .weight(1f)
                        .padding(vertical = 6.dp)
                        .clip(RoundedCornerShape(16.dp))
                        .clickable { onSelect(tab) },
                ) {
                    Box(
                        modifier = Modifier
                            .background(
                                if (active) MoliColors.Mint else Color.Transparent,
                                CircleShape,
                            )
                            .padding(horizontal = 14.dp, vertical = 3.dp),
                    ) {
                        Icon(
                            tab.icon,
                            contentDescription = tab.label,
                            tint = if (active) MoliColors.Pine else MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.size(19.dp),
                        )
                    }
                    Spacer(Modifier.height(2.dp))
                    Text(
                        tab.label,
                        fontSize = 9.5.sp,
                        fontWeight = if (active) FontWeight.Bold else FontWeight.Normal,
                        color = if (active) MoliColors.Pine else MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun TextTab(
    records: List<CloudRecord>,
    selfDeviceId: String?,
    onOpen: (CloudRecord) -> Unit,
    onCopy: (CloudRecord) -> Unit,
    onDelete: (CloudRecord) -> Unit,
) {
    if (records.isEmpty()) {
        EmptyState(
            icon = Icons.AutoMirrored.Outlined.Notes,
            title = "还没有文字",
            body = "点右下角“写文字”。文字会永久保存在服务器，并同步给所有在线设备。",
        )
        return
    }
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 96.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        items(records, key = CloudRecord::recordId) { record ->
            Card(
                colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
                shape = RoundedCornerShape(18.dp),
                modifier = Modifier.clickable { onOpen(record) },
            ) {
                Column(modifier = Modifier.padding(start = 14.dp, end = 6.dp, top = 13.dp, bottom = 6.dp)) {
                    Text(
                        record.text,
                        style = MaterialTheme.typography.bodyLarge,
                        maxLines = 5,
                        overflow = TextOverflow.Ellipsis,
                    )
                    if (record.text.length > 120 || record.text.count { it == '\n' } >= 5) {
                        Text(
                            "点按展开全文，可自由选择部分文字",
                            style = MaterialTheme.typography.labelSmall,
                            color = MoliColors.Pine,
                            modifier = Modifier.padding(top = 4.dp),
                        )
                    }
                    Spacer(Modifier.height(8.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        CloudBadge()
                        Text(
                            "  ${record.senderName.ifBlank { "未命名设备" }} · ${formatTimestamp(record.createdAt)}",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.weight(1f))
                        IconButton(onClick = { onCopy(record) }, modifier = Modifier.size(32.dp)) {
                            Icon(
                                Icons.Outlined.ContentCopy,
                                contentDescription = "复制",
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.size(16.dp),
                            )
                        }
                        if (record.senderDeviceId == selfDeviceId) {
                            IconButton(onClick = { onDelete(record) }, modifier = Modifier.size(32.dp)) {
                                Icon(
                                    Icons.Outlined.Delete,
                                    contentDescription = "删除",
                                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.size(16.dp),
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ImageTab(
    records: List<CloudRecord>,
    downloading: Map<String, DownloadProgress>,
    downloaded: Map<String, DownloadedFile>,
    onOpen: (CloudRecord) -> Unit,
    onActions: (CloudRecord) -> Unit,
) {
    if (records.isEmpty()) {
        EmptyState(
            icon = Icons.Outlined.Image,
            title = "还没有图片",
            body = "点“添加图片”选择文件；小图保存到服务器，大图走 P2P。",
        )
        return
    }
    LazyVerticalGrid(
        columns = GridCells.Fixed(2),
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 96.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        gridItems(records, key = CloudRecord::recordId) { record ->
            val progress = downloading[record.recordId]
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .aspectRatio(1f)
                    .clip(RoundedCornerShape(18.dp))
                    .background(Brush.linearGradient(gradientFor(record.recordId)))
                    .combinedClickable(
                        onClick = { onOpen(record) },
                        onLongClick = { onActions(record) },
                    ),
            ) {
                Icon(
                    Icons.Outlined.Image,
                    contentDescription = null,
                    tint = Color.White.copy(alpha = 0.85f),
                    modifier = Modifier.align(Alignment.Center).size(34.dp),
                )
                Surface(
                    color = Color.White.copy(alpha = 0.9f),
                    shape = CircleShape,
                    modifier = Modifier.align(Alignment.TopEnd).padding(8.dp).size(24.dp),
                ) {
                    Icon(
                        Icons.Outlined.Cloud,
                        contentDescription = "云端",
                        tint = MoliColors.Pine,
                        modifier = Modifier.padding(5.dp),
                    )
                }
                Column(
                    modifier = Modifier
                        .align(Alignment.BottomStart)
                        .fillMaxWidth()
                        .background(
                            Brush.verticalGradient(
                                listOf(Color.Transparent, Color.Black.copy(alpha = 0.45f)),
                            ),
                        )
                        .padding(10.dp),
                ) {
                    Text(
                        record.displayName,
                        color = Color.White,
                        fontSize = 12.sp,
                        fontWeight = FontWeight.SemiBold,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        TransferNotifications.formatBytes(record.fileSize),
                        color = Color.White.copy(alpha = 0.85f),
                        fontSize = 10.sp,
                    )
                }
                if (progress != null) {
                    LinearProgressIndicator(
                        progress = { progress.fraction },
                        color = MoliColors.MintStrong,
                        trackColor = Color.Black.copy(alpha = 0.25f),
                        modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(3.dp),
                    )
                }
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun FileTab(
    records: List<CloudRecord>,
    downloading: Map<String, DownloadProgress>,
    downloaded: Map<String, DownloadedFile>,
    onOpen: (CloudRecord) -> Unit,
    onActions: (CloudRecord) -> Unit,
) {
    if (records.isEmpty()) {
        EmptyState(
            icon = Icons.Outlined.Folder,
            title = "还没有文件",
            body = "点“添加文件”选择文档或安装包；支持断点续传下载。",
        )
        return
    }
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 96.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        items(records, key = CloudRecord::recordId) { record ->
            val progress = downloading[record.recordId]
            Card(
                colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
                shape = RoundedCornerShape(18.dp),
                modifier = Modifier.combinedClickable(
                    onClick = { onOpen(record) },
                    onLongClick = { onActions(record) },
                ),
            ) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.padding(14.dp),
                ) {
                    Box(
                        modifier = Modifier
                            .size(42.dp)
                            .clip(RoundedCornerShape(12.dp))
                            .background(MoliColors.Mint),
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(
                            fileExtension(record.displayName),
                            fontSize = 9.sp,
                            fontWeight = FontWeight.ExtraBold,
                            color = MoliColors.Pine,
                        )
                    }
                    Spacer(Modifier.width(12.dp))
                    Column(modifier = Modifier.weight(1f)) {
                        Text(
                            record.displayName,
                            style = MaterialTheme.typography.bodyLarge,
                            fontWeight = FontWeight.SemiBold,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            "${TransferNotifications.formatBytes(record.fileSize)} · " +
                                "${record.senderName.ifBlank { "未命名设备" }} · " +
                                formatTimestamp(record.createdAt),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                        )
                        if (progress != null) {
                            LinearProgressIndicator(
                                progress = { progress.fraction },
                                modifier = Modifier.fillMaxWidth().padding(top = 6.dp).height(4.dp),
                            )
                        }
                    }
                    Spacer(Modifier.width(8.dp))
                    if (progress == null) {
                        Icon(
                            Icons.Outlined.Download,
                            contentDescription = "下载",
                            tint = MoliColors.Pine,
                            modifier = Modifier.size(20.dp),
                        )
                    } else {
                        CircularProgressIndicator(
                            modifier = Modifier.size(20.dp),
                            strokeWidth = 2.dp,
                            color = MoliColors.Pine,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun CloudBadge() {
    Surface(color = MoliColors.Mint, shape = CircleShape) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.padding(horizontal = 7.dp, vertical = 3.dp),
        ) {
            Icon(
                Icons.Outlined.Cloud,
                contentDescription = null,
                tint = Color(0xFF276B53),
                modifier = Modifier.size(10.dp),
            )
            Spacer(Modifier.width(3.dp))
            Text(
                "云端",
                color = Color(0xFF276B53),
                fontSize = 9.5.sp,
                fontWeight = FontWeight.SemiBold,
            )
        }
    }
}

@Composable
private fun EmptyState(icon: ImageVector, title: String, body: String) {
    Column(
        modifier = Modifier.fillMaxSize().padding(horizontal = 36.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Box(
            modifier = Modifier
                .size(64.dp)
                .clip(CircleShape)
                .background(MoliColors.Mint),
            contentAlignment = Alignment.Center,
        ) {
            Icon(icon, contentDescription = null, tint = MoliColors.Pine, modifier = Modifier.size(28.dp))
        }
        Spacer(Modifier.height(14.dp))
        Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
        Spacer(Modifier.height(6.dp))
        Text(
            body,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun TextComposerDialog(
    value: String,
    onValueChange: (String) -> Unit,
    onDismiss: () -> Unit,
    onSend: () -> Unit,
) {
    val bytes = value.encodeToByteArray().size
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("写文字") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = value,
                    onValueChange = onValueChange,
                    modifier = Modifier.fillMaxWidth(),
                    minLines = 4,
                    maxLines = 8,
                    placeholder = { Text("输入要同步的文字…") },
                )
                Text(
                    "文字会永久保存在服务器，并同步给所有在线设备。$bytes / 65536 字节",
                    style = MaterialTheme.typography.labelSmall,
                    color = if (bytes > 65536) MaterialTheme.colorScheme.error
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            TextButton(
                enabled = value.isNotBlank() && bytes <= 65536,
                onClick = onSend,
            ) { Text("发送") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

@Composable
private fun SendChoiceDialog(
    selection: PendingSelection,
    thresholdBytes: Long,
    p2pAvailable: Boolean,
    onCloud: () -> Unit,
    onP2P: () -> Unit,
    onDismiss: () -> Unit,
) {
    val size = selection.file.sizeBytes
    val tooLarge = size > thresholdBytes
    val sizeKnown = size > 0
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("发送「${selection.file.displayName}」") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    "大小 ${TransferNotifications.formatBytes(size.coerceAtLeast(0))}",
                    style = MaterialTheme.typography.bodyMedium,
                )
                Text(
                    when {
                        !sizeKnown -> "无法读取文件大小，不能发起传输。"
                        tooLarge ->
                            "超过同步阈值 ${TransferNotifications.formatBytes(thresholdBytes)}，" +
                                "只能通过 P2P 直连发送。"
                        else ->
                            "可以保存到服务器（永久保留，所有设备可见），" +
                                "也可以直接 P2P 发送。"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            Column(
                verticalArrangement = Arrangement.spacedBy(8.dp),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Button(
                    onClick = onCloud,
                    enabled = sizeKnown && !tooLarge,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Icon(Icons.Outlined.Cloud, contentDescription = null, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text("上传到服务器")
                }
                OutlinedButton(
                    onClick = onP2P,
                    enabled = sizeKnown && p2pAvailable,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Icon(Icons.Outlined.SwapHoriz, contentDescription = null, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(8.dp))
                    Text(if (p2pAvailable) "P2P 发送" else "P2P（无在线设备）")
                }
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

private fun handlePicked(
    context: Context,
    scope: kotlinx.coroutines.CoroutineScope,
    uri: Uri?,
    kind: String,
    onReady: (PendingSelection) -> Unit,
) {
    if (uri == null) return
    runCatching {
        context.contentResolver.takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }
    scope.launch {
        val file = withContext(Dispatchers.IO) { ShareParser.describe(context, uri) }
        if (file != null) {
            onReady(PendingSelection(uri, file, kind))
        }
    }
}

private fun startP2p(context: Context, uri: Uri, mimeType: String) {
    val intent = Intent(context, ShareActivity::class.java).apply {
        action = Intent.ACTION_SEND
        putExtra(Intent.EXTRA_STREAM, uri)
        type = mimeType
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }
    context.startActivity(intent)
}

/** Opens an https release page in the browser; never uses another scheme. */
private fun openUrl(context: Context, url: String) {
    val parsed = runCatching { Uri.parse(url) }.getOrNull() ?: return
    if (!parsed.scheme.equals("https", ignoreCase = true)) {
        return
    }
    val intent = Intent(Intent.ACTION_VIEW, parsed).apply {
        addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    }
    runCatching { context.startActivity(intent) }
}

private fun copyToClipboard(context: Context, text: String) {    val manager = context.getSystemService(ClipboardManager::class.java)
    manager?.setPrimaryClip(ClipData.newPlainText("MoliSync", text))
}
