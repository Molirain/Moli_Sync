package com.molirain.molisync.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

/** Brand palette shared by the app shell and the static review mock. */
object MoliColors {
    val Pine = Color(0xFF173D36)
    val PineSoft = Color(0xFF1F5049)
    val Mint = Color(0xFFCFE9DC)
    val MintStrong = Color(0xFF8FD3B6)
    val Paper = Color(0xFFF2EDE2)
    val Card = Color(0xFFFFFCF6)
    val Ink = Color(0xFF18211D)
    val Muted = Color(0xFF6C7A72)
    val Line = Color(0xFFE6DFD1)
    val Amber = Color(0xFFD99A3E)
    val Clay = Color(0xFFA6403B)
}

private val LightColors = lightColorScheme(
    primary = MoliColors.Pine,
    onPrimary = Color(0xFFFFF9E9),
    primaryContainer = MoliColors.Mint,
    onPrimaryContainer = MoliColors.Pine,
    secondary = MoliColors.Amber,
    onSecondary = MoliColors.Ink,
    secondaryContainer = Color(0xFFF7E7C8),
    onSecondaryContainer = Color(0xFF5C4110),
    tertiary = MoliColors.PineSoft,
    background = MoliColors.Paper,
    onBackground = MoliColors.Ink,
    surface = MoliColors.Card,
    onSurface = MoliColors.Ink,
    surfaceVariant = Color(0xFFEAE4D6),
    onSurfaceVariant = MoliColors.Muted,
    outline = MoliColors.Line,
    outlineVariant = MoliColors.Line,
    error = MoliColors.Clay,
    onError = Color.White,
)

private val DarkColors = darkColorScheme(
    primary = MoliColors.MintStrong,
    onPrimary = MoliColors.Pine,
    primaryContainer = MoliColors.PineSoft,
    onPrimaryContainer = MoliColors.Mint,
    secondary = MoliColors.Amber,
    background = Color(0xFF101815),
    onBackground = Color(0xFFE9EFEA),
    surface = Color(0xFF18211D),
    onSurface = Color(0xFFE9EFEA),
    surfaceVariant = Color(0xFF25302B),
    onSurfaceVariant = Color(0xFFA9B6AE),
    outline = Color(0xFF3A473F),
)

private val MoliShapes = Shapes(
    extraSmall = RoundedCornerShape(8.dp),
    small = RoundedCornerShape(12.dp),
    medium = RoundedCornerShape(18.dp),
    large = RoundedCornerShape(24.dp),
    extraLarge = RoundedCornerShape(30.dp),
)

@Composable
fun MoliSyncTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) DarkColors else LightColors,
        shapes = MoliShapes,
        content = content,
    )
}
