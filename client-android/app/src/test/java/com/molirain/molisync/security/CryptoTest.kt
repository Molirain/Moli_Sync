package com.molirain.molisync.security

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CryptoTest {

    @Test
    fun `sha256 matches the canonical abc vector`() {
        assertEquals(
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
            sha256Hex("abc".toByteArray()),
        )
    }

    @Test
    fun `hex encoding is lowercase and zero padded`() {
        assertEquals("00010aff", byteArrayOf(0, 1, 10, -1).toHexLower())
    }

    @Test
    fun `certificate fingerprint must be 64 lowercase hex characters`() {
        assertTrue(isCertificateFingerprint("ab".repeat(32)))
        assertTrue(isCertificateFingerprint("0123456789abcdef".repeat(4)))
        assertFalse(isCertificateFingerprint(""))
        assertFalse(isCertificateFingerprint("ab".repeat(31)))
        assertFalse(isCertificateFingerprint("AB".repeat(32)))
        assertFalse(isCertificateFingerprint("zz".repeat(32)))
        assertFalse(isCertificateFingerprint("ab".repeat(31) + "g0"))
    }

    @Test
    fun `constant time comparison still compares exact values`() {
        assertTrue(constantTimeEquals("abc", "abc"))
        assertFalse(constantTimeEquals("abc", "abd"))
        assertFalse(constantTimeEquals("abc", "ab"))
        assertFalse(constantTimeEquals("", "a"))
        assertTrue(constantTimeEquals("", ""))
    }
}
