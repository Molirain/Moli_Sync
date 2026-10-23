package com.molirain.molisync.data.cloud

import org.junit.Assert.assertEquals
import org.junit.Test

class ThumbnailLoaderTest {

    @Test
    fun `thumbnail file name strips path separators`() {
        assertEquals("abc-123.jpg", thumbnailFileName("abc-123"))
        assertEquals("abc123.jpg", thumbnailFileName("abc/../123"))
        assertEquals(".jpg", thumbnailFileName(""))
    }

    @Test
    fun `sample size halves until the preview fits the budget`() {
        assertEquals(4, sampleSize(4000, 4000, 640 * 640))
        assertEquals(1, sampleSize(320, 240, 640 * 640))
        assertEquals(1, sampleSize(0, 240, 640 * 640))
        assertEquals(1, sampleSize(320, 240, 0))
        assertEquals(2, sampleSize(1280, 1280, 640 * 640))
    }
}
