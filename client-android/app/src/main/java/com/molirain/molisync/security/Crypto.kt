package com.molirain.molisync.security

import java.security.MessageDigest

private val HEX = "0123456789abcdef".toCharArray()

fun ByteArray.toHexLower(): String {
    val output = CharArray(size * 2)
    for (index in indices) {
        val value = this[index].toInt() and 0xFF
        output[index * 2] = HEX[value ushr 4]
        output[index * 2 + 1] = HEX[value and 0x0F]
    }
    return String(output)
}

fun sha256Hex(data: ByteArray): String =
    MessageDigest.getInstance("SHA-256").digest(data).toHexLower()

fun isCertificateFingerprint(value: String): Boolean =
    value.length == 64 && value.all { it in '0'..'9' || it in 'a'..'f' }

fun constantTimeEquals(left: String, right: String): Boolean =
    MessageDigest.isEqual(left.toByteArray(Charsets.US_ASCII), right.toByteArray(Charsets.US_ASCII))
