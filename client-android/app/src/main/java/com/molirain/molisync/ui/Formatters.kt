package com.molirain.molisync.ui

import androidx.compose.ui.graphics.Color
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

fun formatTimestamp(millis: Long): String {
    if (millis <= 0) return "未知时间"
    val zone = ZoneId.systemDefault()
    val moment = Instant.ofEpochMilli(millis).atZone(zone)
    val today = LocalDate.now(zone)
    return when {
        moment.toLocalDate() == today ->
            moment.format(DateTimeFormatter.ofPattern("HH:mm"))
        moment.year == today.year ->
            moment.format(DateTimeFormatter.ofPattern("M月d日 HH:mm"))
        else ->
            moment.format(DateTimeFormatter.ofPattern("yyyy年M月d日"))
    }
}

fun fileExtension(name: String): String {
    val dot = name.lastIndexOf('.')
    if (dot <= 0 || dot == name.lastIndex) return "FILE"
    return name.substring(dot + 1).uppercase(Locale.US).take(4)
}

private val gradientPalettes = listOf(
    listOf(Color(0xFFF7C98A), Color(0xFFE8836A), Color(0xFF8E4B5C)),
    listOf(Color(0xFFBFE3D2), Color(0xFF5FA58C), Color(0xFF245E51)),
    listOf(Color(0xFFF6E7B4), Color(0xFFD2B05D), Color(0xFF7A6224)),
    listOf(Color(0xFFCBD8F4), Color(0xFF7089C6), Color(0xFF35406B)),
    listOf(Color(0xFFF1C7D6), Color(0xFFC0708C), Color(0xFF6B3348)),
    listOf(Color(0xFFD6E6C4), Color(0xFF8DAE74), Color(0xFF3E5A33)),
)

fun gradientFor(seed: String): List<Color> {
    if (seed.isEmpty()) return gradientPalettes.first()
    var hash = 0
    for (character in seed) {
        hash = hash * 31 + character.code
    }
    return gradientPalettes[Math.floorMod(hash, gradientPalettes.size)]
}
