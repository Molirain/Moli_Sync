package com.molirain.molisync.security

import org.junit.Assert.assertEquals
import org.junit.Test

class MtlsClientFactoryTest {

    @Test
    fun `webSocketUrl keeps https and appends websocket path`() {
        val url = MtlsClientFactory.webSocketUrl("https://sync.svc.molirain.com:8443")

        assertEquals("https", url.scheme)
        assertEquals("sync.svc.molirain.com", url.host)
        assertEquals(8443, url.port)
        assertEquals("/api/v1/ws", url.encodedPath)
    }
}
