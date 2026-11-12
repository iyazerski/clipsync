package dev.iyazerski.clipsync

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.widget.Toast
import androidx.core.content.edit
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.security.SecureRandom
import java.util.Base64
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
            val id = "phone-" + Base64.getUrlEncoder().withoutPadding()
                .encodeToString(ByteArray(9).also(SecureRandom()::nextBytes))
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
        val url = relay.trim().trimEnd('/')
        require(url.startsWith("https://")) { "The relay should be an https:// address" }
        Seal(secret)
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

/** The relay's HTTP API, as `proto/src/lib.rs` describes it. */
object Relay {
    const val CLIP_PATH = "/clip"
    const val EVENTS_PATH = "/events"
    const val DEVICE_HEADER = "x-clipsync-device"
    /** The longest text, in UTF-8 bytes, that the relay passes on. */
    const val MAX_TEXT = 256 * 1024
    private const val TIMEOUT_MS = 15_000

    /** Thrown when the relay doesn't know this pairing's token. */
    class Refused : IOException("The relay refused the secret: pair again")

    /** A request to [path] with this phone's token and device id. */
    fun connect(settings: Settings, seal: Seal, path: String): HttpURLConnection {
        val relay = settings.relay ?: throw IOException("Not paired")
        return (URL(relay + path).openConnection() as HttpURLConnection).apply {
            connectTimeout = TIMEOUT_MS
            readTimeout = TIMEOUT_MS
            setRequestProperty("Authorization", "Bearer ${seal.token}")
            setRequestProperty(DEVICE_HEADER, settings.device)
        }
    }

    /** Throw unless [connection] got a 200. */
    fun check(connection: HttpURLConnection) {
        when (val code = connection.responseCode) {
            HttpURLConnection.HTTP_OK -> {}
            HttpURLConnection.HTTP_UNAUTHORIZED -> throw Refused()
            else -> throw IOException("The relay answered $code")
        }
    }

    /** Seal [text] and publish it. Blocks on the network. */
    fun publish(settings: Settings, text: String) {
        val seal = Seal(settings.secret ?: throw IOException("Not paired"))
        val connection = connect(settings, seal, CLIP_PATH)
        try {
            connection.requestMethod = "POST"
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "text/plain")
            connection.outputStream.use { it.write(seal.seal(text).toByteArray(Charsets.US_ASCII)) }
            check(connection)
        } finally {
            connection.disconnect()
        }
    }
}

/** Sends text to the Macs off the main thread and says how it went in a toast. */
object Sender {
    private val executor = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())

    fun send(context: Context, text: String?) {
        val app = context.applicationContext
        val settings = Settings(app)
        when {
            !settings.paired -> {
                toast(app, "Pair with a Mac first")
                app.startActivity(Intent(app, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }
            text.isNullOrBlank() -> toast(app, "Nothing to send: the clipboard has no text")
            text.toByteArray(Charsets.UTF_8).size > Relay.MAX_TEXT -> toast(app, "Too long to send")
            else -> executor.execute {
                try {
                    Relay.publish(settings, text)
                    toast(app, "Sent to your Macs")
                } catch (e: IOException) {
                    toast(app, "Couldn't send: ${e.message}")
                }
            }
        }
    }

    private fun toast(context: Context, text: String) {
        main.post { Toast.makeText(context, text, Toast.LENGTH_SHORT).show() }
    }
}
