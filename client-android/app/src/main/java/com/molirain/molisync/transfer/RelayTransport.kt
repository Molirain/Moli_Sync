package com.molirain.molisync.transfer

import android.content.Context
import com.molirain.molisync.security.KeyChainAdapter
import com.molirain.molisync.security.MtlsClientFactory
import java.io.BufferedInputStream
import java.io.InputStream
import java.io.OutputStream
import java.net.URI
import javax.net.ssl.SSLSocket

/**
 * Opens a relayed data stream through the MoliSync server when no direct LAN or
 * VPN path works. The connection is a normal mTLS HTTPS request whose body is a
 * raw byte stream; the server pairs both participants by session id and copies
 * bytes in both directions.
 */
object RelayTransport {

    class Streams(
        val socket: SSLSocket,
        val input: InputStream,
        val output: OutputStream,
    ) : AutoCloseable {
        override fun close() {
            runCatching { socket.close() }
        }
    }

    fun connect(
        context: Context,
        keyChain: KeyChainAdapter,
        alias: String,
        serverUrl: String,
        relayUrl: String,
        ticket: String,
    ): Streams {
        val server = runCatching { URI(serverUrl) }.getOrNull()
            ?: throw TransferException("服务器地址无效，无法建立中继")
        val host = server.host ?: throw TransferException("服务器地址缺少主机名")
        val port = if (server.port > 0) server.port else 443

        val mtls = MtlsClientFactory.create(context, keyChain, alias)
        val socket = (mtls.client.socketFactory.createSocket(host, port) as SSLSocket).apply {
            useClientMode = true
            soTimeout = RELAY_TIMEOUT_MS
            startHandshake()
        }

        val path = if (relayUrl.startsWith("/")) relayUrl else "/$relayUrl"
        val request = buildString {
            append("GET ").append(path).append(" HTTP/1.1\r\n")
            append("Host: ").append(host).append(':').append(port).append("\r\n")
            append("X-Session-Ticket: ").append(ticket).append("\r\n")
            append("Connection: close\r\n")
            append("User-Agent: MoliSync-Android\r\n")
            append("\r\n")
        }
        socket.outputStream.write(request.toByteArray(Charsets.US_ASCII))
        socket.outputStream.flush()

        val input = BufferedInputStream(socket.inputStream, 16 * 1024)
        val statusLine = readLine(input)
        val status = statusLine.split(' ').getOrNull(1)?.toIntOrNull() ?: 0
        var header: String
        do {
            header = readLine(input)
        } while (header.isNotEmpty())

        if (status != 200) {
            runCatching { socket.close() }
            throw TransferException("中继不可用（HTTP $status）")
        }

        socket.soTimeout = 0
        return Streams(socket, input, socket.outputStream)
    }

    private fun readLine(input: InputStream): String {
        val builder = StringBuilder()
        while (true) {
            val value = input.read()
            if (value < 0) {
                throw TransferException("中继连接在应答完成前关闭")
            }
            if (value == '\n'.code) {
                break
            }
            if (value != '\r'.code) {
                builder.append(value.toChar())
            }
            if (builder.length > 8 * 1024) {
                throw TransferException("中继应答行过长")
            }
        }
        return builder.toString()
    }

    private const val RELAY_TIMEOUT_MS = 20_000
}
