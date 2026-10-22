package com.molirain.molisync.data.cloud

import android.content.Context
import android.net.Uri
import io.github.rctcwyvrn.blake3.Blake3
import java.io.InputStream

/** Streaming BLAKE3 helper shared by upload paths. */
object FileDigest {
    fun blake3(context: Context, uri: Uri): String {
        val hasher = Blake3.newInstance()
        val input: InputStream = context.contentResolver.openInputStream(uri)
            ?: throw CloudException("无法读取所选文件")
        input.use { stream ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val read = stream.read(buffer)
                if (read < 0) break
                if (read == 0) continue
                hasher.update(if (read == buffer.size) buffer else buffer.copyOf(read))
            }
        }
        return hasher.hexdigest()
    }
}
