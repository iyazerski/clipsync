package dev.iyazerski.clipsync

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.net.ConnectivityManager
import android.net.Network
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.SystemClock
import java.io.IOException
import java.net.HttpURLConnection
import java.text.DateFormat
import java.util.Date
import kotlin.concurrent.thread

/**
 * Keeps an event stream to the relay open and puts each clip from the Macs on the clipboard.
 *
 * A foreground service, so Android keeps it and its network access while the phone sleeps. It
 * reconnects when the stream drops, goes silent, or the phone switches networks. Writing the
 * clipboard works from the background; reading it doesn't (Android 10+), so sending goes through
 * [SendActivity].
 */
class SyncService : Service() {
    private lateinit var settings: Settings
    private val main = Handler(Looper.getMainLooper())
    private var listener: Thread? = null
    @Volatile private var running = false
    @Volatile private var connection: HttpURLConnection? = null
    /** Set by [kick] to cut a backoff wait short. */
    private var kicked = false
    private val lock = Object()

    private val network = object : ConnectivityManager.NetworkCallback() {
        private var current: Network? = null

        override fun onAvailable(network: Network) {
            // A new network (Wi-Fi to mobile, or back) leaves the old socket dead: reconnect now.
            if (current != null && current != network) kick()
            current = network
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        settings = Settings(this)
        val channel = NotificationChannel(CHANNEL, "Sync", NotificationManager.IMPORTANCE_MIN).apply {
            description = "Shows that clipsync is connected"
            setShowBadge(false)
        }
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
        startForeground(NOTIFICATION, notification(status), ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        getSystemService(ConnectivityManager::class.java).registerDefaultNetworkCallback(network)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (!settings.paired) {
            stopSelf()
            return START_NOT_STICKY
        }
        if (listener == null) {
            running = true
            listener = thread(name = "clipsync-listen") { listen() }
        } else {
            // Started again, say after pairing anew: reconnect with the current settings.
            kick()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        running = false
        getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(network)
        kick()
        setStatus("Stopped")
        super.onDestroy()
    }

    /** Drop the current stream or the backoff wait, so the listener reconnects at once. */
    private fun kick() {
        connection?.disconnect()
        synchronized(lock) {
            kicked = true
            lock.notifyAll()
        }
    }

    private fun listen() {
        var backoff = MIN_BACKOFF_MS
        while (running) {
            val started = SystemClock.elapsedRealtime()
            try {
                stream()
            } catch (e: Relay.Refused) {
                setStatus(e.message ?: "Refused")
                backoff = MAX_BACKOFF_MS
            } catch (e: IOException) {
                if (running) setStatus("Offline, retrying")
            } catch (e: IllegalArgumentException) {
                setStatus(e.message ?: "Bad pairing")
                backoff = MAX_BACKOFF_MS
            }
            if (SystemClock.elapsedRealtime() - started > STABLE_AFTER_MS) backoff = MIN_BACKOFF_MS
            synchronized(lock) {
                if (!kicked && running) lock.wait(backoff)
                kicked = false
            }
            backoff = (backoff * 2).coerceAtMost(MAX_BACKOFF_MS)
        }
    }

    /** Follow the relay's event stream until it ends or fails. */
    private fun stream() {
        val seal = Seal(settings.secret ?: throw IOException("Not paired"))
        val conn = Relay.connect(settings, seal, Relay.EVENTS_PATH).apply {
            // The relay sends a comment every 45 s on an idle stream.
            readTimeout = SILENCE_LIMIT_MS
            setRequestProperty("Accept", "text/event-stream")
            setRequestProperty("Last-Event-ID", settings.seen.toString())
        }
        connection = conn
        try {
            Relay.check(conn)
            setStatus("Connected")
            conn.inputStream.bufferedReader(Charsets.UTF_8).use { reader ->
                var id: Long? = null
                val data = StringBuilder()
                while (running) {
                    val line = reader.readLine() ?: return
                    when {
                        line.isEmpty() -> {
                            if (data.isNotEmpty()) received(seal, id, data.toString())
                            id = null
                            data.setLength(0)
                        }
                        line.startsWith("id:") -> id = line.substring(3).trim().toLongOrNull()
                        line.startsWith("data:") -> data.append(line.substring(5).trim())
                    }
                }
            }
        } finally {
            connection = null
            conn.disconnect()
        }
    }

    /** Open a clip and put it on the clipboard. */
    private fun received(seal: Seal, id: Long?, sealed: String) {
        if (id != null && id > settings.seen) settings.seen = id
        val text = try {
            seal.open(sealed)
        } catch (e: IllegalArgumentException) {
            setStatus(e.message ?: "Couldn't open a clip")
            return
        }
        main.post {
            getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("clipsync", text))
        }
        setStatus("Connected · last clip at ${DateFormat.getTimeInstance(DateFormat.SHORT).format(Date())}")
    }

    private fun setStatus(text: String) {
        main.post {
            if (status == text) return@post
            status = text
            onStatus?.invoke(text)
            if (running) getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification(text))
        }
    }

    private fun notification(text: String): Notification {
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
        )
        val send = Notification.Action.Builder(
            Icon.createWithResource(this, R.drawable.ic_tile), "Send clipboard", SendActivity.clipboardIntent(this),
        ).build()
        return Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_tile)
            .setContentTitle("clipsync")
            .setContentText(text)
            .setContentIntent(open)
            .addAction(send)
            .setOngoing(true)
            .build()
    }

    companion object {
        private const val CHANNEL = "sync"
        private const val NOTIFICATION = 1
        private const val MIN_BACKOFF_MS = 1_000L
        private const val MAX_BACKOFF_MS = 30_000L
        /** A stream that lasted this long worked, so the next reconnect starts fast again. */
        private const val STABLE_AFTER_MS = 60_000L
        /** Reconnect if nothing came from the relay for this long. */
        private const val SILENCE_LIMIT_MS = 100_000

        /** The service's state, for the main screen. Main thread only. */
        var status = "Connecting…"
            private set
        /** Called on the main thread when [status] changes. */
        var onStatus: ((String) -> Unit)? = null

        fun start(context: Context) {
            context.startForegroundService(Intent(context, SyncService::class.java))
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, SyncService::class.java))
        }
    }
}
