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
 * Keeps an event stream to the relay open and puts each clip from the computers on the clipboard.
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
    /** Coordinates backoff waits with [wake] and [reconnectNow]. */
    private val lock = Object()
    private var kicked = false
    private var waiting = false
    /** Kept across streams, so a reconnect can't bring a clip twice. Listener thread only. */
    private val opened = Seal.Opened()

    private val network = object : ConnectivityManager.NetworkCallback() {
        private var current: Network? = null

        override fun onAvailable(network: Network) {
            val previous = current
            current = network
            if (previous == null) {
                // If the service started offline (e.g. after reboot), cut the backoff wait without
                // dropping an initial connect that's already underway.
                wake()
            } else if (previous != network) {
                // A new network (Wi-Fi to mobile, or back) leaves the old socket dead: reconnect now.
                reconnectNow()
            }
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
        updateState(SyncState.Connecting, notify = false)
        startForeground(NOTIFICATION, notification(state), ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
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
        } else if (intent?.action == ACTION_RECONNECT) {
            updateState(SyncState.Connecting, notify = true)
            reconnectNow()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        running = false
        getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(network)
        reconnectNow()
        updateState(SyncState.Stopped, notify = false)
        super.onDestroy()
    }

    /** Cut an active backoff wait short without touching a live stream. */
    private fun wake() {
        synchronized(lock) {
            if (waiting) {
                kicked = true
                lock.notifyAll()
            }
        }
    }

    /** Drop the current stream or the backoff wait, so the listener reconnects at once. */
    private fun reconnectNow() {
        synchronized(lock) {
            kicked = true
            lock.notifyAll()
        }
        connection?.disconnect()
    }

    private fun listen() {
        var backoff = MIN_BACKOFF_MS
        while (running) {
            synchronized(lock) { kicked = false }
            val started = SystemClock.elapsedRealtime()
            try {
                stream()
            } catch (e: Relay.Refused) {
                if (running) setState(SyncState.Error(e.message ?: "Refused"))
                backoff = MAX_BACKOFF_MS
            } catch (e: IOException) {
                val deliberate = synchronized(lock) { kicked }
                if (running) setState(if (deliberate) SyncState.Connecting else SyncState.Offline)
            } catch (e: IllegalArgumentException) {
                if (running) setState(SyncState.Error(e.message ?: "Bad pairing"))
                backoff = MAX_BACKOFF_MS
            }
            if (SystemClock.elapsedRealtime() - started > STABLE_AFTER_MS) backoff = MIN_BACKOFF_MS
            var woke = false
            synchronized(lock) {
                if (!kicked && running) {
                    waiting = true
                    try {
                        lock.wait(backoff)
                    } finally {
                        waiting = false
                    }
                }
                woke = kicked
                kicked = false
            }
            backoff = if (woke) MIN_BACKOFF_MS else (backoff * 2).coerceAtMost(MAX_BACKOFF_MS)
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
            var lastClipAt: Long? = null
            setState(SyncState.Connected())
            conn.inputStream.bufferedReader(Charsets.UTF_8).use { reader ->
                var id: Long? = null
                val data = StringBuilder()
                while (running) {
                    val line = reader.readLine() ?: return
                    when {
                        line.isEmpty() -> {
                            if (data.isNotEmpty()) lastClipAt = received(seal, id, data.toString(), lastClipAt)
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

    /** Open a clip and put it on the clipboard, returning the latest received timestamp. */
    private fun received(seal: Seal, id: Long?, sealed: String, lastClipAt: Long?): Long? {
        if (id != null && id > settings.seen) settings.seen = id
        val text = try {
            seal.open(sealed, opened)
        } catch (e: IllegalArgumentException) {
            setState(SyncState.Connected(lastClipAt, warning = e.message ?: "Couldn't open a clip"))
            return lastClipAt
        }
        main.post {
            if (!running) return@post
            getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("clipsync", text))
        }
        val receivedAt = System.currentTimeMillis()
        setState(SyncState.Connected(receivedAt))
        return receivedAt
    }

    private fun setState(next: SyncState) {
        main.post {
            if (running) updateState(next, notify = true)
        }
    }

    private fun updateState(next: SyncState, notify: Boolean) {
        if (state == next) return
        state = next
        onState?.invoke(next)
        if (notify) getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification(next))
    }

    private fun notification(current: SyncState): Notification {
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
        )
        val send = Notification.Action.Builder(
            Icon.createWithResource(this, R.drawable.ic_tile), "Send clipboard", SendActivity.clipboardIntent(this),
        ).build()
        return Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_tile)
            .setContentTitle("clipsync")
            .setContentText(current.notificationText())
            .setContentIntent(open)
            .addAction(send)
            .setOngoing(true)
            .build()
    }

    companion object {
        private const val CHANNEL = "sync"
        private const val NOTIFICATION = 1
        private const val ACTION_RECONNECT = "dev.iyazerski.clipsync.RECONNECT"
        private const val MIN_BACKOFF_MS = 1_000L
        private const val MAX_BACKOFF_MS = 30_000L
        /** A stream that lasted this long worked, so the next reconnect starts fast again. */
        private const val STABLE_AFTER_MS = 60_000L
        /** Reconnect if nothing came from the relay for this long. */
        private const val SILENCE_LIMIT_MS = 100_000

        /** The service's state, for the main screen. Main thread only. */
        var state: SyncState = SyncState.Connecting
            private set
        /** Called on the main thread when [state] changes. */
        var onState: ((SyncState) -> Unit)? = null

        fun start(context: Context) {
            context.startForegroundService(Intent(context, SyncService::class.java))
        }

        fun reconnect(context: Context) {
            context.startForegroundService(Intent(context, SyncService::class.java).setAction(ACTION_RECONNECT))
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, SyncService::class.java))
        }
    }
}

sealed interface SyncState {
    data object Connecting : SyncState
    data class Connected(val lastClipAt: Long? = null, val warning: String? = null) : SyncState
    data object Offline : SyncState
    data object Stopped : SyncState
    data class Error(val message: String) : SyncState

    fun notificationText(): String = when (this) {
        Connecting -> "Connecting…"
        is Connected -> when {
            warning != null -> "Connected · $warning"
            lastClipAt != null -> "Connected · last clip at ${formatClipTime(lastClipAt)}"
            else -> "Connected"
        }
        Offline -> "Offline, retrying"
        Stopped -> "Stopped"
        is Error -> message
    }
}

fun formatClipTime(epochMs: Long): String = DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(epochMs))
