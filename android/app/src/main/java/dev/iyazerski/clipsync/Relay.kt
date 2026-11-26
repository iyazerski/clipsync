package dev.iyazerski.clipsync

import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.widget.Toast
import androidx.core.content.edit
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URI
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

    /** The pairing's other devices, as [SyncService] last heard of them. */
    var devices: List<KnownDevice>
        get() = Devices.decode(prefs.getString(DEVICES, null))
        set(value) = prefs.edit { putString(DEVICES, Devices.encode(value)) }

    /** Whether the Send clipboard tile is in Quick Settings, as [SendTile] last heard. */
    var tileAdded: Boolean
        get() = prefs.getBoolean(TILE_ADDED, false)
        set(value) = prefs.edit { putBoolean(TILE_ADDED, value) }

    /** Save a pairing after checking the secret; clips from before now are old news. */
    fun pair(relay: String, secret: String) {
        val url = Relay.address(relay)
        Seal(secret)
        prefs.edit {
            putString(RELAY, url)
            putString(SECRET, secret.trim())
            putLong(SEEN, System.currentTimeMillis())
            remove(DEVICES)
        }
    }

    fun unpair() {
        prefs.edit { remove(RELAY).remove(SECRET).remove(DEVICES) }
    }

    private companion object {
        const val RELAY = "relay"
        const val SECRET = "secret"
        const val DEVICE = "device"
        const val SEEN = "seen"
        const val DEVICES = "devices"
        const val TILE_ADDED = "tile_added"
    }
}

object Relay {
    const val CLIP_PATH = "/clip"
    const val EVENTS_PATH = "/events"
    const val DEVICE_HEADER = "x-clipsync-device"
    /** The longest text, in UTF-8 bytes, that the relay passes on. */
    const val MAX_TEXT = 256 * 1024
    private const val TIMEOUT_MS = 15_000

    /** [relay] as the app keeps it, after checking that it's an https:// address. */
    fun address(relay: String): String {
        val url = relay.trim().trimEnd('/')
        val address = runCatching { URI(url) }.getOrNull()
        if (address?.scheme != "https" || address.host == null || address.userInfo != null ||
            address.rawQuery != null || address.rawFragment != null
        ) {
            throw Invalid(R.string.relay_https)
        }
        return url
    }

    /** Thrown when the relay doesn't know this pairing's token. */
    class Refused : Failed(R.string.relay_refused)

    /** A request to [path] with this phone's token and device id. */
    fun connect(settings: Settings, seal: Seal, path: String): HttpURLConnection =
        connect(settings.relay ?: throw Failed(R.string.pair_first), settings.device, seal, path)

    private fun connect(relay: String, device: String, seal: Seal, path: String) =
        (URL(relay + path).openConnection() as HttpURLConnection).apply {
            connectTimeout = TIMEOUT_MS
            readTimeout = TIMEOUT_MS
            instanceFollowRedirects = false
            setRequestProperty("Authorization", "Bearer ${seal.token}")
            setRequestProperty(DEVICE_HEADER, device)
        }

    /** Tell the other devices that this one unpaired. Blocks on the network. */
    fun leave(relay: String, secret: String, device: String) {
        val connection = connect(relay, device, Seal(secret), Devices.PATH)
        try {
            connection.requestMethod = "DELETE"
            if (connection.responseCode != HttpURLConnection.HTTP_NO_CONTENT) throw Failed(R.string.relay_answered, connection.responseCode)
        } finally {
            connection.disconnect()
        }
    }

    /** Throw unless [connection] got a 200. */
    fun check(connection: HttpURLConnection) {
        when (val code = connection.responseCode) {
            HttpURLConnection.HTTP_OK -> {}
            HttpURLConnection.HTTP_UNAUTHORIZED -> throw Refused()
            else -> throw Failed(R.string.relay_answered, code)
        }
    }

    /** Seal [text] and publish it. Blocks on the network. */
    fun publish(settings: Settings, text: String) {
        val seal = Seal(settings.secret ?: throw Failed(R.string.pair_first))
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

/** Sends text to the computers off the main thread and says how it went in a toast. */
object Sender {
    private val executor = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())

    fun send(context: Context, text: String?) {
        val app = context.applicationContext
        val settings = Settings(app)
        when {
            !settings.paired -> {
                toast(app, app.getString(R.string.pair_first))
                app.startActivity(Intent(app, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }
            text.isNullOrBlank() -> toast(app, app.getString(R.string.nothing_to_send))
            text.toByteArray(Charsets.UTF_8).size > Relay.MAX_TEXT -> toast(app, app.getString(R.string.too_long))
            else -> executor.execute {
                try {
                    Relay.publish(settings, text)
                    toast(app, app.getString(R.string.sent))
                } catch (e: IOException) {
                    toast(app, app.getString(R.string.couldnt_send, app.reason(e)))
                }
            }
        }
    }

    private fun toast(context: Context, text: String) {
        main.post { Toast.makeText(context, text, Toast.LENGTH_SHORT).show() }
    }
}
