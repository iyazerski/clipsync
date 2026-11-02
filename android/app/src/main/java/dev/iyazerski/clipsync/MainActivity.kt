package dev.iyazerski.clipsync

import android.Manifest
import android.app.Activity
import android.app.StatusBarManager
import android.content.ClipboardManager
import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.Icon
import android.net.Uri
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings as SystemSettings
import android.text.InputType
import android.view.Gravity
import android.view.HapticFeedbackConstants
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast

/**
 * The only screen: pair with the Macs (from the relay's pairing link, or by typing the relay and
 * the secret), see whether sync is connected, and set up the ways to send. Same black "liquid
 * glass" look as TG Prep ([Ui.kt][Backdrop]).
 */
class MainActivity : Activity() {
    private lateinit var settings: Settings
    private lateinit var backdrop: Backdrop
    private lateinit var content: LinearLayout
    private var status: TextView? = null
    private var dot: View? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        settings = Settings(this)
        buildUi()
        handlePairing(intent)
        if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            // Without it the service still runs, but its notification (and the Send action) is hidden.
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 0)
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handlePairing(intent)
    }

    override fun onResume() {
        super.onResume()
        SyncService.onStatus = ::showStatus
        if (settings.paired) SyncService.start(this)
        // The tile or the battery setting may have changed while the app was away.
        render()
    }

    override fun onPause() {
        SyncService.onStatus = null
        super.onPause()
    }

    /** Pair from a `clipsync://pair?relay=…&secret=…` link. */
    private fun handlePairing(intent: Intent?) {
        val link = intent?.data?.takeIf { intent.action == Intent.ACTION_VIEW } ?: return
        pair(link.getQueryParameter("relay").orEmpty(), link.getQueryParameter("secret").orEmpty())
    }

    private fun pair(relay: String, secret: String) {
        try {
            settings.pair(relay, secret)
        } catch (e: IllegalArgumentException) {
            Toast.makeText(this, e.message, Toast.LENGTH_LONG).show()
            return
        }
        Toast.makeText(this, "Paired", Toast.LENGTH_SHORT).show()
        SyncService.stop(this)
        SyncService.start(this)
        render()
    }

    private fun buildUi() {
        window.setDecorFitsSystemWindows(false)
        val root = FrameLayout(this)
        backdrop = Backdrop(this)
        root.addView(backdrop, FrameLayout.LayoutParams(MATCH, MATCH))
        content = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val scroll = ScrollView(this).apply {
            isFillViewport = true
            // Glass panels redraw the moving backdrop each frame, so they must follow scrolling too.
            setOnScrollChangeListener { _, _, _, _, _ -> backdrop.panels.forEach(View::invalidate) }
            addView(content, FrameLayout.LayoutParams(MATCH, WRAP))
        }
        root.addView(scroll, FrameLayout.LayoutParams(MATCH, MATCH))
        // Edge to edge: the backdrop fills the screen; content stays clear of the system bars and keyboard.
        root.setOnApplyWindowInsetsListener { _, insets ->
            val bars = insets.getInsets(
                WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout() or WindowInsets.Type.ime(),
            )
            val side = dpi(20)
            content.setPadding(bars.left + side, bars.top + dpi(12), bars.right + side, bars.bottom + dpi(16))
            insets
        }
        setContentView(root)
    }

    private fun render() {
        content.removeAllViews()
        status = null
        dot = null
        content.addView(text("clipsync", 28f, Style.TEXT, Style.BOLD), LinearLayout.LayoutParams(MATCH, WRAP))
        if (settings.paired) renderPaired() else renderUnpaired()
        renderFooter()
        // Cards rise into place, one after another.
        for (i in 1 until content.childCount) {
            content.getChildAt(i).apply {
                alpha = 0f
                translationY = dp(16f)
                animate().alpha(1f).translationY(0f).setStartDelay(i * 40L).setDuration(500).setInterpolator(Style.EASE).start()
            }
        }
    }

    private fun renderPaired() {
        val body = card(top = dpi(24))
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        dot = View(this).apply { background = GradientDrawable().apply { shape = GradientDrawable.OVAL } }
        row.addView(dot, LinearLayout.LayoutParams(dpi(10), dpi(10)).apply { marginEnd = dpi(12) })
        status = text("", 22f, Style.TEXT, Style.BOLD)
        row.addView(status, LinearLayout.LayoutParams(0, WRAP, 1f))
        body.addView(row)
        body.addView(text(Uri.parse(settings.relay).host.orEmpty(), 15f, Style.TEXT_DIM), below(dpi(6)))
        body.addView(text("What you copy on your Macs lands on this phone's clipboard.", 16f, Style.TEXT), below(dpi(16)))
        showStatus(SyncService.status)

        val send = card(top = dpi(12))
        send.addView(text("Send to your Macs", 18f, Style.TEXT, Style.BOLD))
        send.addView(
            text(
                "• Select text and pick \"Send to Macs\"\n" +
                    "• Share text to clipsync\n" +
                    "• Tap the tile or the notification button",
                15f, Style.TEXT_DIM,
            ).apply { setLineSpacing(0f, 1.3f) },
            below(dpi(10)),
        )

        content.addView(button("Send clipboard", accent = true) { sendClipboard() }, below(dpi(20)))
        if (!settings.tileAdded) {
            content.addView(button("Add the Quick Settings tile") { addTile() }, below(dpi(12)))
        }
        if (!getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(packageName)) {
            content.addView(button("Allow background activity") {
                startActivity(
                    Intent(SystemSettings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")),
                )
            }, below(dpi(12)))
            content.addView(
                text(
                    "So the phone doesn't stop clipsync to save battery. On ColorOS, also set App info → " +
                        "Battery usage → Allow background activity.",
                    13f, Style.TEXT_DIM,
                ).apply { setPadding(dpi(8), 0, dpi(8), 0) },
                below(dpi(8)),
            )
        }
    }

    /** The bottom of the screen: updates, unpairing, and the version. */
    private fun renderFooter() {
        content.addView(View(this), LinearLayout.LayoutParams(MATCH, 0, 1f))
        val update = button("Check for updates") {}
        update.setOnClickListener { checkForUpdates(update) }
        content.addView(update, below(dpi(24)))
        if (settings.paired) {
            content.addView(button("Unpair") {
                SyncService.stop(this)
                settings.unpair()
                render()
            }, below(dpi(12)))
        }
        val version = packageManager.getPackageInfo(packageName, 0).versionName
        content.addView(
            text("clipsync $version", 13f, Style.TEXT_DIM).apply { gravity = Gravity.CENTER },
            below(dpi(12)),
        )
    }

    private fun checkForUpdates(button: GlassButton) {
        if (!packageManager.canRequestPackageInstalls()) {
            // Android asks once per app before it may install anything, updates included.
            Toast.makeText(this, "Allow clipsync to install updates, then tap again", Toast.LENGTH_LONG).show()
            startActivity(
                Intent(SystemSettings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:$packageName")),
            )
            return
        }
        Updater.update(this) { state ->
            button.text = state ?: "Check for updates"
            button.isEnabled = state == null
        }
    }

    private fun renderUnpaired() {
        val body = card(top = dpi(24))
        body.addView(text("Pair with your Macs", 22f, Style.TEXT, Style.BOLD))
        body.addView(
            text(
                "On a Mac, run clipsync pair and scan its QR code with the camera. Or type the relay and the " +
                    "secret it shows.",
                15f, Style.TEXT_DIM,
            ).apply { setLineSpacing(0f, 1.2f) },
            below(dpi(10)),
        )
        val relay = GlassField(this).apply {
            backdrop = this@MainActivity.backdrop
            input.hint = "Relay, like https://clip.example.com"
            input.inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
        }
        val secret = GlassField(this).apply {
            backdrop = this@MainActivity.backdrop
            input.hint = "Secret"
            input.inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            // Password fields default to monospace; keep the screen's font.
            input.typeface = Typeface.DEFAULT
        }
        content.addView(relay, below(dpi(20)))
        content.addView(secret, below(dpi(12)))
        content.addView(button("Pair", accent = true) {
            pair(relay.input.text.toString(), secret.input.text.toString())
        }, below(dpi(20)))
    }

    private fun showStatus(text: String) {
        status?.text = text
        val connected = text.startsWith("Connected")
        (dot?.background as? GradientDrawable)?.setColor(if (connected) CONNECTED else Style.TEXT_DIM)
    }

    private fun sendClipboard() {
        val clip = getSystemService(ClipboardManager::class.java).primaryClip
        Sender.send(this, clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString())
    }

    private fun addTile() {
        getSystemService(StatusBarManager::class.java).requestAddTileService(
            ComponentName(this, SendTile::class.java),
            "Send clipboard",
            Icon.createWithResource(this, R.drawable.ic_tile),
            mainExecutor,
        ) { result ->
            if (result == StatusBarManager.TILE_ADD_REQUEST_RESULT_TILE_ADDED ||
                result == StatusBarManager.TILE_ADD_REQUEST_RESULT_TILE_ALREADY_ADDED
            ) {
                settings.tileAdded = true
                render()
            }
        }
    }

    /** Add a glass card to the screen and return the layout for its contents. */
    private fun card(top: Int): LinearLayout {
        val panel = GlassPanel(this).apply {
            backdrop = this@MainActivity.backdrop
            cornerRadius = dp(28f)
        }
        val body = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dpi(22), dpi(20), dpi(22), dpi(22))
        }
        panel.addView(body, FrameLayout.LayoutParams(MATCH, WRAP))
        content.addView(panel, below(top))
        return body
    }

    private fun text(value: String, size: Float, color: Int, face: Typeface? = null) = TextView(this).apply {
        text = value
        textSize = size
        setTextColor(color)
        if (face != null) typeface = face
    }

    private fun button(label: String, accent: Boolean = false, onClick: () -> Unit) = GlassButton(this).apply {
        text = label
        this.accent = accent
        backdrop = this@MainActivity.backdrop
        setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.CONTEXT_CLICK)
            onClick()
        }
    }

    /** Full-width layout params with [top] space above. */
    private fun below(top: Int) = LinearLayout.LayoutParams(MATCH, WRAP).apply { topMargin = top }

    private companion object {
        const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
        const val WRAP = ViewGroup.LayoutParams.WRAP_CONTENT
        /** A soft green, the one color on screen: sync is live. */
        const val CONNECTED = 0xFF5BD68A.toInt()
    }
}
