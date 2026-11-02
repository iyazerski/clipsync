package dev.iyazerski.clipsync

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/** Starts syncing after a reboot or an app update, if the phone is paired. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val known = intent.action == Intent.ACTION_BOOT_COMPLETED || intent.action == Intent.ACTION_MY_PACKAGE_REPLACED
        if (known && Settings(context).paired) SyncService.start(context)
    }
}
