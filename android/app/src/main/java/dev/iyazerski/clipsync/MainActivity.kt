package dev.iyazerski.clipsync

import android.Manifest
import android.app.Activity
import android.app.StatusBarManager
import android.content.ClipboardManager
import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Typeface
import android.graphics.drawable.Icon
import android.net.Uri
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings as SystemSettings
import android.text.InputType
import android.view.View
import android.view.WindowInsets
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast

/**
 * The only screen: pair with the Macs (from the relay's pairing link, or by typing the relay and
 * the secret), see whether sync is connected, and set up the ways to send.
 */
class MainActivity : Activity() {
    private lateinit var settings: Settings
    private lateinit var content: LinearLayout
    private lateinit var status: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        settings = Settings(this)
        status = text("", 16f, DIM)
        content = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val scroll = ScrollView(this).apply {
            addView(content)
            setOnApplyWindowInsetsListener { view, insets ->
                val bars = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime())
                val pad = dp(24)
                view.setPadding(pad, bars.top + pad, pad, bars.bottom + pad)
                insets
            }
        }
        setContentView(scroll)
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
        SyncService.onStatus = { status.text = it }
        if (settings.paired) SyncService.start(this)
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

    private fun render() {
        content.removeAllViews()
        content.addView(text("clipsync", 34f, TEXT, bold = true))
        if (settings.paired) renderPaired() else renderUnpaired()
    }

    private fun renderUnpaired() {
        content.addView(gap(text("Not paired", 16f, DIM)))
        content.addView(gap(text(
            "On a Mac, run `clipsync pair` and scan its QR code with the camera. Or type the relay and the secret it shows:",
            16f, TEXT,
        )))
        val relay = field("Relay, like https://clip.example.com", InputType.TYPE_TEXT_VARIATION_URI)
        val secret = field("Secret", InputType.TYPE_TEXT_VARIATION_PASSWORD)
        content.addView(gap(relay))
        content.addView(gap(secret))
        content.addView(gap(button("Pair") { pair(relay.text.toString(), secret.text.toString()) }))
    }

    private fun renderPaired() {
        status.text = SyncService.status
        content.addView(gap(status))
        content.addView(gap(text("Relay: ${settings.relay}", 14f, DIM)))
        content.addView(gap(text(
            "What you copy on your Macs lands on this phone's clipboard.\n\n" +
                "To send from the phone, select text and pick \"Send to Macs\", share text to clipsync, " +
                "or tap the \"Send clipboard\" tile or notification action.",
            16f, TEXT,
        )))
        content.addView(gap(button("Send clipboard") {
            val clip = getSystemService(ClipboardManager::class.java).primaryClip
            Sender.send(this, clip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString())
        }))
        content.addView(gap(button("Add the Quick Settings tile") {
            getSystemService(StatusBarManager::class.java).requestAddTileService(
                ComponentName(this, SendTile::class.java),
                "Send clipboard",
                Icon.createWithResource(this, R.drawable.ic_tile),
                mainExecutor,
            ) {}
        }))
        if (!getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(packageName)) {
            content.addView(gap(text(
                "Let clipsync run in the background, or the phone may stop it to save battery. " +
                    "On ColorOS, also set App info → Battery usage → Allow background activity.",
                14f, DIM,
            )))
            content.addView(gap(button("Allow background activity") {
                startActivity(
                    Intent(SystemSettings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")),
                )
            }))
        }
        content.addView(gap(button("Unpair") {
            SyncService.stop(this)
            settings.unpair()
            render()
        }))
    }

    private fun text(value: String, size: Float, color: Int, bold: Boolean = false) = TextView(this).apply {
        text = value
        textSize = size
        setTextColor(color)
        if (bold) typeface = Typeface.DEFAULT_BOLD
    }

    private fun field(hint: String, variation: Int) = EditText(this).apply {
        this.hint = hint
        inputType = InputType.TYPE_CLASS_TEXT or variation
        setTextColor(TEXT)
        setHintTextColor(DIM)
        isSingleLine = true
    }

    private fun button(label: String, onClick: () -> Unit) = Button(this).apply {
        text = label
        isAllCaps = false
        setOnClickListener { onClick() }
    }

    /** [view] with space above it. */
    private fun <T : View> gap(view: T): T = view.apply {
        layoutParams = LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT,
        ).apply { topMargin = dp(16) }
    }

    private fun dp(value: Int): Int = (value * resources.displayMetrics.density + 0.5f).toInt()

    private companion object {
        const val TEXT = 0xFFF5F5F7.toInt()
        const val DIM = 0x99F5F5F7.toInt()
    }
}
