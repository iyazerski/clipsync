package dev.iyazerski.clipsync

import android.app.Activity
import android.app.PendingIntent
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.os.Bundle

/**
 * An invisible screen that sends text to the Macs and closes at once.
 *
 * It takes the text from the selection menu ("Send to Macs") or from Share. Opened from the tile or
 * the notification, it reads the clipboard instead: Android lets only the focused app do that, so
 * it waits until its window has focus.
 */
class SendActivity : Activity() {
    private var readClipboard = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val text = when (intent.action) {
            Intent.ACTION_PROCESS_TEXT -> intent.getCharSequenceExtra(Intent.EXTRA_PROCESS_TEXT)?.toString()
            Intent.ACTION_SEND -> intent.getStringExtra(Intent.EXTRA_TEXT)
            else -> {
                readClipboard = true
                return
            }
        }
        Sender.send(this, text)
        finish()
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus || !readClipboard) return
        readClipboard = false
        val clip = getSystemService(ClipboardManager::class.java).primaryClip
        Sender.send(this, clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString())
        finish()
    }

    companion object {
        private const val ACTION_CLIPBOARD = "dev.iyazerski.clipsync.SEND_CLIPBOARD"

        /** Opens this screen to send the clipboard. */
        fun clipboardIntent(context: Context): PendingIntent = PendingIntent.getActivity(
            context,
            1,
            Intent(context, SendActivity::class.java)
                .setAction(ACTION_CLIPBOARD)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE,
        )
    }
}
