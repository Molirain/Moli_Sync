package com.molirain.molisync.ui

import com.molirain.molisync.data.cloud.CloudRepository
import org.junit.Assert.assertEquals
import org.junit.Test

class FormattersTest {

    @Test
    fun `file extension is upper cased and bounded`() {
        assertEquals("PDF", fileExtension("report.pdf"))
        assertEquals("GZ", fileExtension("archive.tar.gz"))
        assertEquals("JPEG", fileExtension("photo.jpeg"))
        assertEquals("FILE", fileExtension("README"))
        assertEquals("FILE", fileExtension("trailing."))
        assertEquals("FILE", fileExtension(""))
    }

    @Test
    fun `download file names cannot traverse directories`() {
        assertEquals("passwd", CloudRepository.sanitizeFileName("../../etc/passwd"))
        assertEquals("evil.txt", CloudRepository.sanitizeFileName("..\\..\\evil.txt"))
        assertEquals("a_b_c.txt", CloudRepository.sanitizeFileName("a:b*c.txt"))
        assertEquals("file", CloudRepository.sanitizeFileName("   "))
        assertEquals("file", CloudRepository.sanitizeFileName(".."))
    }

    @Test
    fun `long names are truncated but keep a usable value`() {
        val sanitized = CloudRepository.sanitizeFileName("x".repeat(500))
        assertEquals(120, sanitized.length)
    }
}
