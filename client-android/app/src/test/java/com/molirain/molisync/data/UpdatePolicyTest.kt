package com.molirain.molisync.data

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdatePolicyTest {

    private val now = 1_700_000_000_000L

    @Test
    fun `first launch checks immediately`() {
        assertTrue(UpdatePolicy.shouldRefresh(force = false, now = now, checkedAt = 0L))
    }

    @Test
    fun `a recent successful check is reused`() {
        assertFalse(
            UpdatePolicy.shouldRefresh(
                force = false,
                now = now,
                checkedAt = now - UpdatePolicy.CHECK_INTERVAL_MS / 2,
            ),
        )
    }

    @Test
    fun `a release published after the last check is noticed the same day`() {
        // A 24 hour cache would hide a release for a full day; the policy must
        // refresh well before that.
        assertTrue(
            UpdatePolicy.shouldRefresh(
                force = false,
                now = now,
                checkedAt = now - 24L * 60L * 60L * 1000L,
            ),
        )
    }

    @Test
    fun `the manual button always bypasses the cache`() {
        assertTrue(UpdatePolicy.shouldRefresh(force = true, now = now, checkedAt = now))
    }

    @Test
    fun `interval stays inside the anonymous GitHub rate limit`() {
        // 60 requests per hour per IP: 6 hours per device is 1/360 of the budget.
        assertTrue(UpdatePolicy.CHECK_INTERVAL_MS >= 60L * 60L * 1000L)
    }
}
