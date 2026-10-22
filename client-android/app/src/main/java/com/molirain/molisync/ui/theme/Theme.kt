package com.molirain.molisync.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val LightColors = lightColorScheme(
    primary = Color(0xFF2E6B5A),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFB2DFD1),
    onPrimaryContainer = Color(0xFF062019),
    secondary = Color(0xFF4A635B),
    background = Color(0xFFF7FBF8),
    surface = Color(0xFFF7FBF8),
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFF96D3C0),
    onPrimary = Color(0xFF00382C),
    primaryContainer = Color(0xFF135343),
    onPrimaryContainer = Color(0xFFB2DFD1),
    secondary = Color(0xFFB1CCC3),
    background = Color(0xFF0F1512),
    surface = Color(0xFF0F1512),
)

@Composable
fun MoliSyncTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) DarkColors else LightColors,
        content = content,
    )
}
