package dev.iyazerski.clipsync

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.widget.Toast
import androidx.core.content.edit
import dev.iyazerski.androidbridge.Androidbridge
import java.util.concurrent.Executors

/** What the phone keeps: the pairing, its device id, and the last clip it got. */
class Settings(context: Context) {
    private val prefs = context.getSharedPreferences("clipsync", Context.MODE_PRIVATE)

    val relay: String? get() = prefs.getString(RELAY, null)
    val secret: String? get() = prefs.getString(SECRET, null)
    val paired: Boolean get() = relay != null && secret != null

    /** This phone's id, so the relay doesn't send its own clips back. Made on first use. */
    val device: String
        get() = prefs.getString(DEVICE, null) ?: run {
            val id = Androidbridge.deviceID()
            prefs.edit { putString(DEVICE, id) }
            id
        }

    /** The id of the last clip from the relay, so a reconnect brings only what was missed. */
    var seen: Long
        get() = prefs.getLong(SEEN, 0)
        set(value) = prefs.edit { putLong(SEEN, value) }

    /** Whether the Send clipboard tile is in Quick Settings, as [SendTile] last heard. */
    var tileAdded: Boolean
        get() = prefs.getBoolean(TILE_ADDED, false)
        set(value) = prefs.edit { putBoolean(TILE_ADDED, value) }

    /** Save a pairing after checking the secret; clips from before now are old news. */
    fun pair(relay: String, secret: String) {
        val url = try {
            Androidbridge.validatePairing(relay, secret)
        } catch (e: Exception) {
            throw IllegalArgumentException(e.message, e)
        }
        prefs.edit {
            putString(RELAY, url)
            putString(SECRET, secret.trim())
            putLong(SEEN, System.currentTimeMillis())
        }
    }

    fun unpair() {
        prefs.edit { remove(RELAY).remove(SECRET) }
    }

    private companion object {
        const val RELAY = "relay"
        const val SECRET = "secret"
        const val DEVICE = "device"
        const val SEEN = "seen"
        const val TILE_ADDED = "tile_added"
    }
}

/** Sends text to the computers off the main thread and says how it went in a toast. */
object Sender {
    private val executor = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())

    fun send(context: Context, text: String?) {
        val app = context.applicationContext
        val settings = Settings(app)
        val relay = settings.relay
        val secret = settings.secret
        when {
            relay == null || secret == null -> {
                toast(app, "Pair with a computer first")
                app.startActivity(Intent(app, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }
            text.isNullOrBlank() -> toast(app, "Nothing to send: the clipboard has no text")
            text.toByteArray(Charsets.UTF_8).size > Androidbridge.MaxText -> toast(app, "Too long to send")
            else -> executor.execute {
                try {
                    Androidbridge.publish(relay, secret, settings.device, text)
                    toast(app, "Sent to your computers")
                } catch (e: Exception) {
                    toast(app, "Couldn't send: ${e.message}")
                }
            }
        }
    }

    private fun toast(context: Context, text: String) {
        main.post { Toast.makeText(context, text, Toast.LENGTH_SHORT).show() }
    }
}
