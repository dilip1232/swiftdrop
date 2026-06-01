package com.swiftdrop

import android.annotation.SuppressLint
import android.content.Context
import android.net.wifi.WifiManager
import android.os.PowerManager

/**
 * Holds a partial WakeLock + a high-performance WiFi lock while any transfer is
 * active, so sends/receives keep running with the screen off (otherwise Doze
 * suspends the CPU and WiFi radio). Reference-counted across concurrent
 * transfers; released when the last one finishes.
 */
object PowerLocks {
    private var wake: PowerManager.WakeLock? = null
    private var wifi: WifiManager.WifiLock? = null
    private var count = 0

    @SuppressLint("WakelockTimeout")
    @Synchronized
    fun begin() {
        if (count == 0) {
            val ctx = State.appContext
            val pm = ctx.getSystemService(Context.POWER_SERVICE) as PowerManager
            wake = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "swiftdrop:transfer").also {
                it.setReferenceCounted(false); it.acquire()
            }
            val wm = ctx.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
            wifi = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "swiftdrop:wifi").also {
                it.setReferenceCounted(false); it.acquire()
            }
        }
        count++
    }

    @Synchronized
    fun end() {
        count--
        if (count <= 0) {
            count = 0
            runCatching { wake?.release() }; wake = null
            runCatching { wifi?.release() }; wifi = null
        }
    }
}
