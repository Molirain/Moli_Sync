package com.molirain.molisync.ui

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.molirain.molisync.BuildConfig
import com.molirain.molisync.data.AppUpdate
import com.molirain.molisync.data.UpdateCheckState

/** Opens an https link in the browser; other schemes are ignored. */
internal fun openExternalUrl(context: Context, url: String) {
    val parsed = runCatching { Uri.parse(url) }.getOrNull() ?: return
    if (!parsed.scheme.equals("https", ignoreCase = true)) {
        return
    }
    val intent = Intent(Intent.ACTION_VIEW, parsed).apply {
        addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    }
    runCatching { context.startActivity(intent) }
}

/**
 * Update prompt rendered by the root screen so a manual check from the
 * settings page shows the dialog immediately.
 */
@Composable
fun AppUpdateDialog(
    update: AppUpdate,
    onDismiss: () -> Unit,
    onDownload: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
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
            TextButton(onClick = onDownload) { Text("前往下载") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("稍后") }
        },
    )
}

/** One-line result of the last release lookup. */
@Composable
fun UpdateStatusLine(state: UpdateCheckState, modifier: Modifier = Modifier) {
    val (message, isError) = when (state) {
        UpdateCheckState.Idle -> return
        UpdateCheckState.Checking -> "正在检查更新…" to false
        is UpdateCheckState.UpToDate -> "已是最新版本 ${state.version}" to false
        is UpdateCheckState.Available -> "发现新版本 ${state.update.version}" to false
        is UpdateCheckState.Failed -> "检查失败：${state.message}" to true
    }
    Row(
        modifier = modifier,
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        if (state == UpdateCheckState.Checking) {
            CircularProgressIndicator(
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.size(14.dp),
            )
        }
        Spacer(Modifier.width(0.dp))
        Text(
            message,
            style = MaterialTheme.typography.labelSmall,
            color = if (isError) {
                MaterialTheme.colorScheme.error
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
        )
    }
}
