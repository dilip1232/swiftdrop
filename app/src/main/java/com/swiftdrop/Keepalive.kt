package com.swiftdrop

import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * Makes the device list self-healing and independent of mDNS reliability: every
 * few seconds it probes each *known* device (seen via mDNS or added by IP,
 * persisted across restarts) and shows the ones reachable right now. So a
 * reachable device always (re)appears automatically — after a restart, after a
 * removal, or after a peer's IP changed. Devices briefly removed by the user
 * are skipped until their ignore window expires.
 */
class Keepalive {
    @Volatile private var running = false
    private var thread: Thread? = null

    fun start() {
        running = true
        thread = Thread {
            while (running) {
                try { Thread.sleep(3000) } catch (e: InterruptedException) { break }
                for (k in State.known.values.toList()) {
                    if (k.id == State.deviceId || State.ignored(k.id)) continue
                    val probed = probe(k.host)
                    if (probed != null && probed.id == k.id) {
                        State.peers[k.id] = probed.copy(manual = State.isManual(k.id))
                        State.remember(probed)
                        try { announceToRemote(k.host) } catch (_: Exception) {}
                    } else {
                        State.peers.remove(k.id) // unreachable here; mDNS may re-find at a new host
                    }
                }
            }
        }.also { it.start() }
    }

    fun stop() {
        running = false
        thread?.interrupt()
    }

    private fun announceToRemote(peerHost: String) {
        val selfIP = State.localIp() ?: return
        val selfHost = "$selfIP:${State.PORT}"
        val c = (URL("http://$peerHost/api/peers/add").openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"; connectTimeout = 2000; readTimeout = 2000
            setRequestProperty("Content-Type", "application/json")
            doOutput = true
        }
        c.outputStream.use { it.write("""{"host":"$selfHost"}""".toByteArray()) }
        c.inputStream.close(); c.disconnect()
    }

    private fun probe(host: String): Peer? = try {
        val c = (URL("http://$host/api/me").openConnection() as HttpURLConnection).apply {
            connectTimeout = 2000; readTimeout = 2000
        }
        val text = c.inputStream.bufferedReader().use { it.readText() }
        c.disconnect()
        val o = JSONObject(text)
        Peer(o.getString("id"), o.optString("name", "Device"), o.optString("platform", "device"), host, false)
    } catch (e: Exception) {
        null
    }
}
