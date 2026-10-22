package com.molirain.molisync.security

import android.annotation.SuppressLint
import android.content.Context
import java.security.KeyStore
import java.security.cert.CertificateException
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

/**
 * Server trust configuration.
 *
 * If app/src/main/assets/molisync_ca.crt is packaged, the server chain is
 * validated exclusively against that pinned private CA. Otherwise the Android
 * system trust store is used (for deployments with a publicly trusted
 * certificate). There is deliberately no trust-all fallback.
 */
object ServerTrust {
    const val CA_ASSET = "molisync_ca.crt"

    fun create(context: Context): X509TrustManager {
        val assetPresent = runCatching {
            context.assets.list("")?.contains(CA_ASSET) == true
        }.getOrDefault(false)

        return if (assetPresent) pinnedTrustManager(context) else systemTrustManager()
    }

    private fun pinnedTrustManager(context: Context): X509TrustManager {
        val certificates = context.assets.open(CA_ASSET).use { input ->
            CertificateFactory.getInstance("X.509")
                .generateCertificates(input)
                .filterIsInstance<X509Certificate>()
        }
        require(certificates.isNotEmpty()) { "molisync_ca.crt does not contain a certificate" }

        val keyStore = KeyStore.getInstance(KeyStore.getDefaultType()).apply {
            load(null)
            certificates.forEachIndexed { index, certificate ->
                setCertificateEntry("molisync-ca-$index", certificate)
            }
        }
        val factory = TrustManagerFactory
            .getInstance(TrustManagerFactory.getDefaultAlgorithm())
            .apply { init(keyStore) }
        return factory.trustManagers.filterIsInstance<X509TrustManager>().single()
    }

    private fun systemTrustManager(): X509TrustManager {
        val factory = TrustManagerFactory
            .getInstance(TrustManagerFactory.getDefaultAlgorithm())
            .apply { init(null as KeyStore?) }
        return factory.trustManagers.filterIsInstance<X509TrustManager>().single()
    }
}

/**
 * P2P peer verification by pinned SHA-256 certificate fingerprint. The expected
 * fingerprint comes from the authenticated control-plane presence, so this is a
 * certificate pin rather than a trust-all shortcut. Expired certificates are
 * rejected before the fingerprint comparison.
 */
@SuppressLint("CustomX509TrustManager")
class PeerFingerprintTrustManager(expectedFingerprint: String) : X509TrustManager {
    private val expected = expectedFingerprint.lowercase()

    init {
        require(isCertificateFingerprint(expected)) {
            "peer fingerprint must be a lowercase SHA-256 fingerprint"
        }
    }

    @Throws(CertificateException::class)
    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        val leaf = chain?.firstOrNull()
            ?: throw CertificateException("peer presented no certificate")
        leaf.checkValidity()

        val actual = sha256Hex(leaf.encoded)
        if (!constantTimeEquals(actual, expected)) {
            throw CertificateException("peer certificate fingerprint does not match the target device")
        }
    }

    @Throws(CertificateException::class)
    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        throw CertificateException("this trust manager only validates P2P servers")
    }

    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
}
