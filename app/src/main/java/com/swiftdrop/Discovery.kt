package com.swiftdrop

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.util.Log
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.atomic.AtomicBoolean

/**
 * mDNS register + browse using the system NsdManager (no extra dependency).
 * Registers this device as `_swiftdrop._tcp` and keeps [State.peers] in sync
 * with other devices found on the LAN.
 */
class Discovery(ctx: Context) {
    private val nsd = ctx.getSystemService(Context.NSD_SERVICE) as NsdManager
    private val type = "_swiftdrop._tcp."
    private var regListener: NsdManager.RegistrationListener? = null
    private var discListener: NsdManager.DiscoveryListener? = null

    // NsdManager.resolveService handles one request at a time, so serialise.
    private val resolveQueue = ConcurrentLinkedQueue<NsdServiceInfo>()
    private val resolving = AtomicBoolean(false)

    fun start() {
        register()
        discover()
    }

    fun stop() {
        runCatching { regListener?.let { nsd.unregisterService(it) } }
        runCatching { discListener?.let { nsd.stopServiceDiscovery(it) } }
    }

    private fun register() {
        val info = NsdServiceInfo().apply {
            serviceName = "SwiftDrop-${State.deviceId}"
            serviceType = type
            port = State.PORT
            setAttribute("id", State.deviceId)
            setAttribute("name", State.deviceName)
            setAttribute("platform", State.PLATFORM)
        }
        regListener = object : NsdManager.RegistrationListener {
            override fun onServiceRegistered(p0: NsdServiceInfo?) {}
            override fun onRegistrationFailed(p0: NsdServiceInfo?, code: Int) {
                Log.w("SwiftDrop", "mDNS register failed: $code")
            }
            override fun onServiceUnregistered(p0: NsdServiceInfo?) {}
            override fun onUnregistrationFailed(p0: NsdServiceInfo?, code: Int) {}
        }
        nsd.registerService(info, NsdManager.PROTOCOL_DNS_SD, regListener)
    }

    private fun discover() {
        discListener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(p0: String?) {}
            override fun onDiscoveryStopped(p0: String?) {}
            override fun onStartDiscoveryFailed(p0: String?, code: Int) {}
            override fun onStopDiscoveryFailed(p0: String?, code: Int) {}
            override fun onServiceFound(info: NsdServiceInfo) {
                if (info.serviceType.contains("swiftdrop")) {
                    resolveQueue.add(info); pump()
                }
            }
            override fun onServiceLost(info: NsdServiceInfo) {
                info.serviceName?.removePrefix("SwiftDrop-")?.let { State.peers.remove(it) }
            }
        }
        nsd.discoverServices(type, NsdManager.PROTOCOL_DNS_SD, discListener)
    }

    private fun pump() {
        if (!resolving.compareAndSet(false, true)) return
        val info = resolveQueue.poll()
        if (info == null) {
            resolving.set(false)
            return
        }
        @Suppress("DEPRECATION")
        nsd.resolveService(info, object : NsdManager.ResolveListener {
            override fun onResolveFailed(p0: NsdServiceInfo?, code: Int) {
                resolving.set(false); pump()
            }
            override fun onServiceResolved(resolved: NsdServiceInfo) {
                handleResolved(resolved)
                resolving.set(false); pump()
            }
        })
    }

    private fun handleResolved(info: NsdServiceInfo) {
        val attrs = info.attributes ?: emptyMap()
        fun attr(k: String): String? = attrs[k]?.let { String(it) }

        val id = attr("id") ?: info.serviceName?.removePrefix("SwiftDrop-") ?: return
        if (id == State.deviceId) return
        if (State.ignored(id)) return  // recently removed by the user
        val host = info.host?.hostAddress ?: return
        val peer = Peer(
            id = id,
            name = attr("name") ?: info.serviceName ?: "Device",
            platform = attr("platform") ?: "device",
            host = "$host:${info.port}"
        )
        State.peers[id] = peer
        State.remember(peer) // so the prober keeps it visible even if mDNS goes quiet
    }
}
