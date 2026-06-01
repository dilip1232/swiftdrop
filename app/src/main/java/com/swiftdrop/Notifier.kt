package com.swiftdrop

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.os.Handler
import android.os.Looper

/** Notification channels + dynamic service notification that reflects transfer state. */
object Notifier {
    const val SERVICE_CHANNEL = "swiftdrop_service"
    const val ALERT_CHANNEL = "swiftdrop_alerts"
    const val SERVICE_ID = 1
    private var alertId = 1000

    private var ctx: Context? = null
    private val handler = Handler(Looper.getMainLooper())
    private var polling = false

    fun ensureChannels(ctx: Context) {
        this.ctx = ctx.applicationContext
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(SERVICE_CHANNEL, "SwiftDrop running", NotificationManager.IMPORTANCE_LOW)
        )
        nm.createNotificationChannel(
            NotificationChannel(ALERT_CHANNEL, "Transfers", NotificationManager.IMPORTANCE_DEFAULT)
        )
    }

    /** Build the foreground service notification reflecting current transfer state. */
    fun serviceNotification(ctx: Context): Notification {
        val sending = State.transfers.count { it.status == "sending" && it.dir == "send" }
        val receiving = State.transfers.count { it.status == "sending" && it.dir == "recv" }

        val (icon, text) = when {
            sending > 0 && receiving > 0 -> android.R.drawable.stat_sys_upload_done to "Sending $sending · Receiving $receiving"
            sending > 0 -> android.R.drawable.stat_sys_upload to "Sending $sending file${if (sending > 1) "s" else ""}"
            receiving > 0 -> android.R.drawable.stat_sys_download to "Receiving $receiving file${if (receiving > 1) "s" else ""}"
            else -> android.R.drawable.stat_notify_sync_noanim to "Ready to send and receive"
        }

        return Notification.Builder(ctx, SERVICE_CHANNEL)
            .setContentTitle("SwiftDrop")
            .setContentText(text)
            .setSmallIcon(icon)
            .setOngoing(true)
            .build()
    }

    /** Start polling transfers and updating the service notification while transfers are active. */
    fun refreshServiceNotification() {
        val c = ctx ?: return
        val nm = c.getSystemService(NotificationManager::class.java)
        nm.notify(SERVICE_ID, serviceNotification(c))

        // Keep polling while there are active transfers.
        val hasActive = State.transfers.any { it.status == "sending" }
        if (hasActive && !polling) {
            polling = true
            pollLoop()
        }
    }

    private fun pollLoop() {
        handler.postDelayed({
            val c = ctx ?: return@postDelayed
            val nm = c.getSystemService(NotificationManager::class.java)
            nm.notify(SERVICE_ID, serviceNotification(c))
            val hasActive = State.transfers.any { it.status == "sending" }
            if (hasActive) {
                pollLoop()
            } else {
                polling = false
            }
        }, 1500)
    }

    fun show(ctx: Context, text: String) {
        val n = Notification.Builder(ctx, ALERT_CHANNEL)
            .setContentTitle("SwiftDrop")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.stat_sys_download_done)
            .setAutoCancel(true)
            .build()
        ctx.getSystemService(NotificationManager::class.java).notify(alertId++, n)
    }
}
