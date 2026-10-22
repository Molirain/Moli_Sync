package com.molirain.molisync.security

import android.content.Context
import okhttp3.ConnectionSpec
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.TlsVersion
import java.util.concurrent.TimeUnit

data class MtlsClient(
    val client: OkHttpClient,
    val identity: KeyChainIdentity,
)

/**
 * Builds the control-plane HTTP/WebSocket client. The certificate object and
 * private key handle come straight from Android KeyChain; no PFX/PEM file is
 * ever written.
 */
object MtlsClientFactory {

    /**
     * Blocking; call on Dispatchers.IO.
     */
    fun create(context: Context, keyChain: KeyChainAdapter, alias: String): MtlsClient {
        val trustManager = ServerTrust.create(context)
        val (sslContext, identity) = keyChain.createSslContext(alias, trustManager)

        val tlsSpec = ConnectionSpec.Builder(ConnectionSpec.MODERN_TLS)
            .tlsVersions(TlsVersion.TLS_1_3, TlsVersion.TLS_1_2)
            .build()

        val client = OkHttpClient.Builder()
            .sslSocketFactory(sslContext.socketFactory, trustManager)
            .connectionSpecs(listOf(tlsSpec))
            // A redirect must never receive the client certificate or request body.
            .followRedirects(false)
            .followSslRedirects(false)
            .connectTimeout(20, TimeUnit.SECONDS)
            // WebSocket sessions are long lived; liveness is enforced by
            // application heartbeats and OkHttp ping frames.
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .pingInterval(20, TimeUnit.SECONDS)
            .build()

        return MtlsClient(client, identity)
    }

    fun webSocketUrl(serverUrl: String): HttpUrl {
        val base = serverUrl.toHttpUrl()
        require(base.isHttps) { "server URL must use https" }
        return base.newBuilder()
            .addPathSegment("api")
            .addPathSegment("v1")
            .addPathSegment("ws")
            .build()
    }
}
