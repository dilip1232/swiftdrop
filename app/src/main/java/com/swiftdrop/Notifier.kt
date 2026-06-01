package com.swiftdrop

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context

/** Notification channels + a helper for "file received" alerts. */
object Notifier {
    const val SERVICE_CHANNEL = "swiftdrop_service"
    const val ALERT_CHANNEL = "swiftdrop_alerts"
    private var alertId = 1000

    fun ensureChannels(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(SERVICE_CHANNEL, "SwiftDrop running", NotificationManager.IMPORTANCE_LOW)
        )
        nm.createNotificationChannel(
            NotificationChannel(ALERT_CHANNEL, "Transfers", NotificationManager.IMPORTANCE_DEFAULT)
        )
    }

    fun serviceNotification(ctx: Context): Notification =
        Notification.Builder(ctx, SERVICE_CHANNEL)
            .setContentTitle("SwiftDrop")
            .setContentText("Ready to send and receive")
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setOngoing(true)
            .build()

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
