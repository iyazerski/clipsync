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
import dev.iyazerski.androidbridge.Androidbridge
import dev.iyazerski.androidbridge.Observer
import dev.iyazerski.androidbridge.Subscription
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
    @Suppress("PLATFORM_CLASS_MAPPED_TO_KOTLIN")
    private val lock = Object()
    private var subscription: Subscription? = null
    private var kicked = false
    private val receiver by lazy { Androidbridge.newReceiver() }

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
        synchronized(lock) {
            kicked = true
            subscription?.stop()
            lock.notifyAll()
        }
    }

    private fun listen() {
        var backoff = MIN_BACKOFF_MS
        while (running) {
            val started = SystemClock.elapsedRealtime()
            try {
                when (stream()) {
                    Androidbridge.Refused -> {
                        setStatus("The relay refused the secret: pair again")
                        backoff = MAX_BACKOFF_MS
                    }
                    Androidbridge.Offline -> if (running) setStatus("Offline, retrying")
                }
            } catch (e: Exception) {
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

    private fun stream(): Long {
        val relay = settings.relay ?: return 0
        val secret = settings.secret ?: return 0
        val active = Androidbridge.subscribe(relay, secret, settings.device)
        synchronized(lock) {
            if (!running || kicked) {
                active.stop()
                return 0
            }
            subscription = active
        }
        return try {
            active.run(receiver, settings.seen, object : Observer {
                override fun onConnected() = setStatus("Connected")

                override fun onClip(id: Long, text: String, problem: String) {
                    if (id > settings.seen) settings.seen = id
                    if (problem.isNotEmpty()) {
                        setStatus(problem)
                        return
                    }
                    main.post {
                        if (!running) return@post
                        getSystemService(ClipboardManager::class.java)
                            .setPrimaryClip(ClipData.newPlainText("clipsync", text))
                    }
                    setStatus("Connected · last clip at ${DateFormat.getTimeInstance(DateFormat.SHORT).format(Date())}")
                }
            })
        } finally {
            synchronized(lock) { subscription = null }
        }
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
