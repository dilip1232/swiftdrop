package com.swiftdrop

import android.net.Uri
import android.provider.OpenableColumns
import java.io.BufferedOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest

/**
 * Streams a file (given as a content URI) straight to a peer's /inbox using a
 * fixed-length HTTP body — no buffering of the whole file, so large transfers
 * run at full LAN speed. Progress is reported through the [Transfer].
 */
object Sender {
    private const val BUF = 256 * 1024

    fun sendUri(peer: Peer, uri: Uri) {
        val cr = State.appContext.contentResolver

        var name = ""
        var size = -1L
        runCatching {
            cr.query(uri, null, null, null, null)?.use { c ->
                val ni = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                val si = c.getColumnIndex(OpenableColumns.SIZE)
                if (c.moveToFirst()) {
                    if (ni >= 0) c.getString(ni)?.let { name = it }
                    if (si >= 0 && !c.isNull(si)) size = c.getLong(si)
                }
            }
        }
        // Fall back to the URI's last path segment (e.g. for file:// URIs that
        // don't expose DISPLAY_NAME) so received files are never just "file".
        if (name.isBlank()) {
            name = uri.lastPathSegment?.substringAfterLast('/')?.takeIf { it.isNotBlank() } ?: "file"
        }

        val t = State.newTransfer(name, if (size < 0) 0 else size, peer.name, "send")
        Notifier.refreshServiceNotification()
        PowerLocks.begin()
        var conn: HttpURLConnection? = null
        try {
            conn = (URL("http://${peer.host}/inbox").openConnection() as HttpURLConnection).apply {
                requestMethod = "POST"
                doOutput = true
                setRequestProperty("Content-Type", "application/octet-stream")
                setRequestProperty("X-Filename", name)
                setRequestProperty("X-From", State.deviceName)
                setRequestProperty("X-From-ID", State.deviceId)
                if (size > 0) setRequestProperty("X-File-Size", size.toString())
                // Compute SHA-256 hash for integrity verification.
                val hash = cr.openInputStream(uri)?.use { hashInput ->
                    val md = MessageDigest.getInstance("SHA-256")
                    val hbuf = ByteArray(BUF)
                    while (true) {
                        val n = hashInput.read(hbuf)
                        if (n < 0) break
                        md.update(hbuf, 0, n)
                    }
                    md.digest().joinToString("") { "%02x".format(it) }
                }
                if (hash != null) setRequestProperty("X-SHA256", hash)
                connectTimeout = 8000
                readTimeout = 30_000 // detect stalled peers
                if (size >= 0) setFixedLengthStreamingMode(size) else setChunkedStreamingMode(BUF)
            }

            cr.openInputStream(uri).use { input ->
                requireNotNull(input) { "cannot open file" }
                BufferedOutputStream(conn.outputStream, BUF).use { out ->
                    val buf = ByteArray(BUF)
                    while (true) {
                        if (t.canceled) { conn.disconnect(); return }
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        t.sent.addAndGet(n.toLong())
                    }
                    out.flush()
                }
            }

            val code = conn.responseCode
            conn.disconnect()
            if (code != 200) {
                t.status = "error"; t.err = "peer returned $code"
            } else {
                t.status = "done"
            }
        } catch (e: Exception) {
            if (t.canceled) t.status = "canceled"
            else { t.status = "error"; t.err = e.message }
        } finally {
            Notifier.refreshServiceNotification()
            PowerLocks.end()
        }
    }
}
