package com.molirain.molisync.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdateCheckerTest {

    private val payload = """
        [
          {"tag_name":"desktop-v0.9.0","html_url":"https://example/desktop"},
          {"tag_name":"server-v0.9.0","html_url":"https://example/server"},
          {"tag_name":"android-v0.5.1","html_url":"https://example/android-051","body":"修复重复下载"},
          {"tag_name":"android-v0.5.2","html_url":"https://example/android-052"},
          {"tag_name":"android-v0.9.9","html_url":"https://example/draft","draft":true},
          {"tag_name":"android-v0.8.0","html_url":"https://example/pre","prerelease":true},
          {"tag_name":"android-vbroken","html_url":"https://example/broken"},
          {"tag_name":"v9.9.9","html_url":"https://example/umbrella"}
        ]
    """.trimIndent()

    @Test
    fun `selects only the android namespace and skips drafts`() {
        val latest = UpdateChecker.selectLatest(payload)

        assertEquals("0.5.2", latest?.version)
        assertEquals("https://example/android-052", latest?.url)
        assertEquals("android-v0.5.2", latest?.tag)
    }

    @Test
    fun `ignores other components even when their version is higher`() {
        val latest = UpdateChecker.selectLatest(payload)
        assertTrue(latest!!.version.startsWith("0.5."))
    }

    @Test
    fun `returns null for an empty or unparsable payload`() {
        assertNull(UpdateChecker.selectLatest("[]"))
        assertNull(UpdateChecker.selectLatest("not json"))
    }

    @Test
    fun `falls back to the releases page when html_url is missing`() {
        val latest = UpdateChecker.selectLatest("""[{"tag_name":"android-v0.5.1"}]""")
        assertEquals(UpdateChecker.RELEASES_PAGE, latest?.url)
    }

    @Test
    fun `compares semantic versions numerically`() {
        assertTrue(UpdateChecker.compareVersions("0.5.2", "0.5.1") > 0)
        assertTrue(UpdateChecker.compareVersions("0.10.0", "0.9.0") > 0)
        assertTrue(UpdateChecker.compareVersions("1.0.0", "0.99.99") > 0)
        assertEquals(0, UpdateChecker.compareVersions("v0.5.1", "0.5.1"))
        assertTrue(UpdateChecker.compareVersions("0.5.0", "0.5.1") < 0)
    }

    @Test
    fun `normalizes prefixes and pre-release suffixes`() {
        assertEquals("0.5.1", UpdateChecker.normalize("v0.5.1"))
        assertEquals("0.5.1", UpdateChecker.normalize("0.5.1-rc1"))
        assertEquals("0.5.1", UpdateChecker.normalize(" 0.5.1+build "))
    }

    @Test
    fun `requires three numeric components`() {
        assertTrue(UpdateChecker.isSemVer("0.5.1"))
        assertFalse(UpdateChecker.isSemVer("0.5"))
        assertFalse(UpdateChecker.isSemVer("0.5.1.2"))
        assertFalse(UpdateChecker.isSemVer("dev"))
    }
}
