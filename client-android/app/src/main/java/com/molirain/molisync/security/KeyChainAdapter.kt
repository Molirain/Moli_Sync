package com.molirain.molisync.security

import android.app.Activity
import android.content.Context
import android.net.Uri
import android.security.KeyChain
import java.security.Principal
import java.security.PrivateKey
import java.security.SecureRandom
import java.security.cert.X509Certificate
import javax.net.ssl.KeyManager
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

class KeyChainException(message: String, cause: Throwable? = null) : Exception(message, cause)

/**
 * Immutable handle to the user-authorized KeyChain identity. The private key is
 * a system key handle; this class never serializes or exports it.
 */
class KeyChainIdentity(
    val alias: String,
    val chain: Array<X509Certificate>,
    val privateKey: PrivateKey,
    val deviceId: String,
) {
    init {
        require(chain.isNotEmpty()) { "certificate chain is empty" }
        require(isCertificateFingerprint(deviceId)) { "device id must be a SHA-256 fingerprint" }
    }

    val leaf: X509Certificate
        get() = chain[0]
}

/**
 * Android KeyChain adapter.
 *
 * The user picks an existing certificate through the system credential UI.
 * Afterwards the app obtains the certificate chain and a PrivateKey handle via
 * KeyChain.getPrivateKey(). All TLS signing happens inside the system keystore.
 */
class KeyChainAdapter(private val context: Context) {

    fun chooseAlias(
        activity: Activity,
        serverUrl: Uri,
        onResult: (Result<String?>) -> Unit,
    ) {
        val host = serverUrl.host
        if (host.isNullOrBlank()) {
            onResult(Result.failure(KeyChainException("服务器地址缺少主机名")))
            return
        }
        val port = if (serverUrl.port > 0) serverUrl.port else 443
        try {
            KeyChain.choosePrivateKeyAlias(
                activity,
                { alias -> onResult(Result.success(alias)) },
                arrayOf("RSA", "EC"),
                null,
                host,
                port,
                null,
            )
        } catch (exception: Exception) {
            onResult(Result.failure(KeyChainException("无法打开系统证书选择器", exception)))
        }
    }

    /**
     * Blocking KeyChain read. Call from Dispatchers.IO.
     */
    @Throws(KeyChainException::class)
    fun loadIdentity(alias: String): KeyChainIdentity {
        val chain = try {
            KeyChain.getCertificateChain(context, alias)
        } catch (exception: Exception) {
            throw KeyChainException("读取客户端证书链失败", exception)
        } ?: throw KeyChainException("证书别名无效或已被移除，请重新授权")

        val privateKey = try {
            KeyChain.getPrivateKey(context, alias)
        } catch (exception: Exception) {
            throw KeyChainException("无法获取私钥句柄，请重新授权", exception)
        } ?: throw KeyChainException("系统中没有该证书的私钥访问权限")

        if (chain.isEmpty()) {
            throw KeyChainException("证书链为空")
        }

        val leaf = chain[0]
        try {
            leaf.checkValidity()
        } catch (exception: Exception) {
            throw KeyChainException("客户端证书已过期或尚未生效", exception)
        }

        if (!keyMatchesCertificate(privateKey, leaf)) {
            throw KeyChainException("私钥算法与证书公钥不匹配")
        }

        return KeyChainIdentity(
            alias = alias,
            chain = chain,
            privateKey = privateKey,
            deviceId = sha256Hex(leaf.encoded),
        )
    }

    @Throws(KeyChainException::class)
    fun createSslContext(
        identity: KeyChainIdentity,
        trustManager: X509TrustManager,
    ): SSLContext {
        return try {
            SSLContext.getInstance("TLS").apply {
                init(
                    arrayOf<KeyManager>(KeyChainKeyManager(identity)),
                    arrayOf<TrustManager>(trustManager),
                    SecureRandom(),
                )
            }
        } catch (exception: Exception) {
            throw KeyChainException("无法初始化 TLS 上下文", exception)
        }
    }

    @Throws(KeyChainException::class)
    fun createSslContext(alias: String, trustManager: X509TrustManager): Pair<SSLContext, KeyChainIdentity> {
        val identity = loadIdentity(alias)
        return createSslContext(identity, trustManager) to identity
    }

    /**
     * SSLContext for the P2P listener. The same KeyChain identity is presented
     * as the server certificate; peer certificates are checked against the
     * session's expected fingerprint by [trustManager].
     */
    @Throws(KeyChainException::class)
    fun createServerSslContext(
        identity: KeyChainIdentity,
        trustManager: X509TrustManager,
    ): SSLContext {
        return try {
            SSLContext.getInstance("TLS").apply {
                init(
                    arrayOf<KeyManager>(KeyChainServerKeyManager(identity)),
                    arrayOf<TrustManager>(trustManager),
                    SecureRandom(),
                )
            }
        } catch (exception: Exception) {
            throw KeyChainException("无法初始化 P2P 监听 TLS 上下文", exception)
        }
    }

    private fun keyMatchesCertificate(privateKey: PrivateKey, certificate: X509Certificate): Boolean {
        val keyAlgorithm = privateKey.algorithm.replace("-", "").uppercase()
        val certAlgorithm = certificate.publicKey.algorithm.replace("-", "").uppercase()
        return keyAlgorithm == certAlgorithm ||
            (keyAlgorithm == "EC" && certAlgorithm == "ECDSA") ||
            (keyAlgorithm == "ECDSA" && certAlgorithm == "EC")
    }
}

/**
 * Key manager for the P2P listener. It exposes one server alias backed by the
 * KeyChain identity; the private key remains a system handle.
 */
class KeyChainServerKeyManager(private val identity: KeyChainIdentity) : javax.net.ssl.X509ExtendedKeyManager() {

    override fun getClientAliases(keyType: String?, issuers: Array<out Principal>?): Array<String>? = null

    override fun chooseClientAlias(
        keyTypes: Array<out String>?,
        issuers: Array<out Principal>?,
        socket: java.net.Socket?,
    ): String? = null

    override fun getServerAliases(keyType: String?, issuers: Array<out Principal>?): Array<String>? {
        return if (keyType != null && supports(keyType)) arrayOf(identity.alias) else null
    }

    override fun chooseServerAlias(
        keyType: String?,
        issuers: Array<out Principal>?,
        socket: java.net.Socket?,
    ): String? {
        return if (keyType != null && supports(keyType)) identity.alias else null
    }

    override fun chooseEngineServerAlias(
        keyType: String?,
        issuers: Array<out Principal>?,
        engine: javax.net.ssl.SSLEngine?,
    ): String? = chooseServerAlias(keyType, issuers, null)

    override fun chooseEngineClientAlias(
        keyType: Array<out String>?,
        issuers: Array<out Principal>?,
        engine: javax.net.ssl.SSLEngine?,
    ): String? = null

    override fun getCertificateChain(alias: String?): Array<X509Certificate>? =
        if (alias == identity.alias) identity.chain.copyOf() else null

    override fun getPrivateKey(alias: String?): PrivateKey? =
        if (alias == identity.alias) identity.privateKey else null

    private fun supports(keyType: String): Boolean {
        val algorithm = identity.privateKey.algorithm
        return keyType.equals(algorithm, ignoreCase = true) ||
            (algorithm.equals("EC", ignoreCase = true) && keyType.equals("ECDSA", ignoreCase = true))
    }
}

/**
 * Key manager that presents exactly one KeyChain identity for outbound mTLS.
 */
class KeyChainKeyManager(private val identity: KeyChainIdentity) : javax.net.ssl.X509ExtendedKeyManager() {

    override fun getClientAliases(keyType: String?, issuers: Array<out Principal>?): Array<String>? {
        return if (keyType != null && supports(keyType)) arrayOf(identity.alias) else null
    }

    override fun chooseClientAlias(
        keyTypes: Array<out String>?,
        issuers: Array<out Principal>?,
        socket: java.net.Socket?,
    ): String? {
        if (keyTypes == null) {
            return null
        }
        return if (keyTypes.any(::supports)) identity.alias else null
    }

    override fun chooseEngineClientAlias(
        keyType: Array<out String>?,
        issuers: Array<out Principal>?,
        engine: javax.net.ssl.SSLEngine?,
    ): String? = chooseClientAlias(keyType, issuers, null)

    override fun getServerAliases(keyType: String?, issuers: Array<out Principal>?): Array<String>? = null

    override fun chooseServerAlias(
        keyType: String?,
        issuers: Array<out Principal>?,
        socket: java.net.Socket?,
    ): String? = null

    override fun chooseEngineServerAlias(
        keyType: String?,
        issuers: Array<out Principal>?,
        engine: javax.net.ssl.SSLEngine?,
    ): String? = null

    override fun getCertificateChain(alias: String?): Array<X509Certificate>? =
        if (alias == identity.alias) identity.chain.copyOf() else null

    override fun getPrivateKey(alias: String?): PrivateKey? =
        if (alias == identity.alias) identity.privateKey else null

    private fun supports(keyType: String): Boolean {
        val algorithm = identity.privateKey.algorithm
        return keyType.equals(algorithm, ignoreCase = true) ||
            (algorithm.equals("EC", ignoreCase = true) && keyType.equals("ECDSA", ignoreCase = true))
    }
}
