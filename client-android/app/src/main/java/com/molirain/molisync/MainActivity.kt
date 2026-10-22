package com.molirain.molisync

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import com.molirain.molisync.ui.HomeScreen
import com.molirain.molisync.ui.SettingsScreen
import com.molirain.molisync.ui.SetupScreen
import com.molirain.molisync.ui.theme.MoliSyncTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val app = application as MoliSyncApp
        setContent {
            MoliSyncTheme {
                RootScreen(app)
            }
        }
    }
}

@Composable
private fun RootScreen(app: MoliSyncApp) {
    val context = LocalContext.current
    var configured by rememberSaveable { mutableStateOf(app.settings.isConfigured) }
    var showSettings by rememberSaveable { mutableStateOf(false) }

    val notificationPermission = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { }

    LaunchedEffect(Unit) {
        app.startTransport()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(
                context,
                Manifest.permission.POST_NOTIFICATIONS,
            ) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    when {
        !configured -> SetupScreen(app) {
            configured = true
            showSettings = false
        }

        showSettings -> SettingsScreen(
            app = app,
            onBack = { showSettings = false },
            onSignOut = {
                configured = false
                showSettings = false
            },
        )

        else -> HomeScreen(app, onSettings = { showSettings = true })
    }
}
