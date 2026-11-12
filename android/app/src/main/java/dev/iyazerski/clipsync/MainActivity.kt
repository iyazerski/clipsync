package dev.iyazerski.clipsync

import android.Manifest
import android.app.StatusBarManager
import android.content.ClipboardManager
import android.content.ComponentName
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.drawable.Icon
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings as SystemSettings
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon as M3Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.launch

/**
 * The only screen: pair with the Macs (from the relay's pairing link, or by typing the relay and
 * the secret), see whether sync is connected, and set up the ways to send.
 */
class MainActivity : ComponentActivity() {
    private lateinit var settings: Settings
    private val snackbar = SnackbarHostState()
    private var setup by mutableStateOf(Setup())
    private var status by mutableStateOf(SyncService.status)
    /** The update check's progress, or null when idle. */
    private var updating by mutableStateOf<String?>(null)
    private val notifications = registerForActivityResult(ActivityResultContracts.RequestPermission()) {
        setup = readSetup()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        settings = Settings(this)
        setup = readSetup()
        if (!setup.notifications) {
            // Without it the service still runs, but its notification (and the Send action) is hidden.
            notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        setContent { ClipsyncTheme { GlassScreen { Screen() } } }
        handlePairing(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handlePairing(intent)
    }

    override fun onResume() {
        super.onResume()
        SyncService.onStatus = { status = it }
        if (settings.paired) SyncService.start(this)
        // The tile, battery, or notification settings may have changed while the app was away.
        setup = readSetup()
        // Status changes while paused don't reach the screen.
        status = SyncService.status
    }

    override fun onPause() {
        SyncService.onStatus = null
        super.onPause()
    }

    private fun readSetup() = Setup(
        paired = settings.paired,
        relayHost = settings.relay?.toUri()?.host.orEmpty(),
        tileAdded = settings.tileAdded,
        battery = getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(packageName),
        notifications = checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED,
    )

    /** Pair from a `clipsync://pair?relay=…&secret=…` link. */
    private fun handlePairing(intent: Intent?) {
        val link = intent?.data?.takeIf { intent.action == Intent.ACTION_VIEW } ?: return
        pair(link.getQueryParameter("relay").orEmpty(), link.getQueryParameter("secret").orEmpty())
    }

    private fun pair(relay: String, secret: String) {
        try {
            settings.pair(relay, secret)
        } catch (e: IllegalArgumentException) {
            notify(e.message ?: "Couldn't pair")
            return
        }
        notify("Paired")
        SyncService.stop(this)
        SyncService.start(this)
        setup = readSetup()
    }

    private fun unpair() {
        SyncService.stop(this)
        settings.unpair()
        setup = readSetup()
    }

    private fun notify(message: String) {
        lifecycleScope.launch { snackbar.showSnackbar(message) }
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
                setup = readSetup()
            }
        }
    }

    private fun allowBackground() {
        startActivity(Intent(SystemSettings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, "package:$packageName".toUri()))
    }

    private fun allowNotifications() {
        // After a denial Android may not ask again, so send the user to the app's notification settings.
        startActivity(Intent(SystemSettings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(SystemSettings.EXTRA_APP_PACKAGE, packageName))
    }

    private fun checkForUpdates() {
        if (!packageManager.canRequestPackageInstalls()) {
            // Android asks once per app before it may install anything, updates included.
            Toast.makeText(this, "Allow clipsync to install updates, then tap again", Toast.LENGTH_LONG).show()
            startActivity(Intent(SystemSettings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, "package:$packageName".toUri()))
            return
        }
        Updater.update(this) { updating = it }
    }

    @Composable
    private fun Screen() {
        Scaffold(
            containerColor = Color.Transparent,
            contentColor = MaterialTheme.colorScheme.onBackground,
            snackbarHost = { SnackbarHost(snackbar) },
        ) { insets ->
            Column(
                Modifier
                    .fillMaxSize()
                    .padding(insets)
                    .imePadding()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = 20.dp, vertical = 12.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(
                    "clipsync",
                    style = MaterialTheme.typography.headlineMedium,
                    fontWeight = FontWeight.Bold,
                    modifier = Modifier.padding(start = 4.dp, bottom = 12.dp),
                )
                AnimatedContent(
                    targetState = setup.paired,
                    transitionSpec = { (fadeIn() + slideInVertically { it / 8 }) togetherWith fadeOut() },
                    label = "screen",
                ) { paired ->
                    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        if (paired) Paired() else Unpaired(::pair)
                    }
                }
                Spacer(Modifier.weight(1f))
                Footer()
            }
        }
    }

    @Composable
    private fun Paired() {
        StatusCard(status, setup.relayHost)
        if (!setup.battery || !setup.notifications) SetupCard()
        SendCard()
    }

    @Composable
    private fun SendCard() {
        val haptics = LocalHapticFeedback.current
        GlassCard(Modifier.fillMaxWidth()) {
            Column {
                Text("Send to your Macs", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(16.dp))
                Button(
                    onClick = {
                        haptics.performHapticFeedback(HapticFeedbackType.Confirm)
                        sendClipboard()
                    },
                    modifier = Modifier.fillMaxWidth().height(56.dp),
                    contentPadding = ButtonDefaults.ButtonWithIconContentPadding,
                ) {
                    Symbol(R.drawable.sym_content_paste_go, Modifier.size(ButtonDefaults.IconSize))
                    Spacer(Modifier.width(ButtonDefaults.IconSpacing))
                    Text("Send clipboard")
                }
                Spacer(Modifier.height(8.dp))
                Item(R.drawable.sym_text_select_start, "Select text", "Then pick Send to Macs")
                Item(R.drawable.sym_share, "Share", "Share text to clipsync")
                Item(R.drawable.sym_notifications, "Notification", "Tap Send clipboard")
                Item(R.drawable.sym_widgets, "Quick Settings tile", "One tap from anywhere") {
                    if (setup.tileAdded) {
                        Symbol(R.drawable.sym_check, tint = Connected)
                    } else {
                        FilledTonalButton(onClick = ::addTile) { Text("Add") }
                    }
                }
            }
        }
    }

    @Composable
    private fun SetupCard() {
        GlassCard(Modifier.fillMaxWidth()) {
            Column {
                Text("Finish setup", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                if (!setup.battery) {
                    Item(
                        R.drawable.sym_battery_android_full,
                        "Run in the background",
                        "So Android doesn't stop sync to save battery. On ColorOS, also allow background activity " +
                            "in App info → Battery usage.",
                    ) { FilledTonalButton(onClick = ::allowBackground) { Text("Allow") } }
                }
                if (!setup.notifications) {
                    Item(R.drawable.sym_notifications, "Notifications", "Shows the sync status and a Send button") {
                        FilledTonalButton(onClick = ::allowNotifications) { Text("Allow") }
                    }
                }
            }
        }
    }

    /** The bottom of the screen: updates, unpairing, and the version. */
    @Composable
    private fun Footer() {
        var confirmUnpair by rememberSaveable { mutableStateOf(false) }
        Column(Modifier.fillMaxWidth().padding(top = 12.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                TextButton(onClick = ::checkForUpdates, enabled = updating == null) {
                    Symbol(R.drawable.sym_system_update, Modifier.size(ButtonDefaults.IconSize))
                    Spacer(Modifier.width(ButtonDefaults.IconSpacing))
                    Text(updating ?: "Check for updates")
                }
                if (setup.paired) {
                    TextButton(
                        onClick = { confirmUnpair = true },
                        colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error),
                    ) {
                        Symbol(R.drawable.sym_link_off, Modifier.size(ButtonDefaults.IconSize))
                        Spacer(Modifier.width(ButtonDefaults.IconSpacing))
                        Text("Unpair")
                    }
                }
            }
            Text(
                "clipsync ${packageManager.getPackageInfo(packageName, 0).versionName}",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (confirmUnpair) {
            AlertDialog(
                onDismissRequest = { confirmUnpair = false },
                icon = { Symbol(R.drawable.sym_link_off) },
                title = { Text("Unpair this phone?") },
                text = { Text("Sync stops. To pair again, run clipsync pair on a Mac.") },
                confirmButton = {
                    TextButton(onClick = {
                        confirmUnpair = false
                        unpair()
                    }) { Text("Unpair") }
                },
                dismissButton = { TextButton(onClick = { confirmUnpair = false }) { Text("Cancel") } },
            )
        }
    }
}

/** What the screen shows, read from settings and system state. */
private data class Setup(
    val paired: Boolean = false,
    val relayHost: String = "",
    val tileAdded: Boolean = false,
    val battery: Boolean = true,
    val notifications: Boolean = true,
)

/** How a [SyncService.status] text reads on screen. */
private class StatusView(val icon: Int, val title: String, val detail: String, val tint: Color?)

@Composable
private fun statusView(status: String, host: String): StatusView = when {
    status.startsWith("Connected") -> StatusView(
        R.drawable.sym_check_circle,
        "Connected",
        status.substringAfter(" · ", "").replaceFirstChar(Char::uppercase).ifEmpty { "Waiting for clips from $host" },
        Connected,
    )
    status == "Connecting…" -> StatusView(R.drawable.sym_sync, "Connecting…", host, null)
    status.startsWith("Offline") -> StatusView(R.drawable.sym_cloud_off, "Offline", "Retrying soon", null)
    status == "Stopped" -> StatusView(R.drawable.sym_cloud_off, "Stopped", "Open clipsync to resume", null)
    else -> StatusView(R.drawable.sym_error, "Not syncing", status, MaterialTheme.colorScheme.error)
}

@Composable
private fun StatusCard(status: String, host: String) {
    val view = statusView(status, host)
    val tint by animateColorAsState(view.tint ?: MaterialTheme.colorScheme.onSurfaceVariant, label = "tint")
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier.size(48.dp).background(tint.copy(alpha = 0.16f), CircleShape),
                    contentAlignment = Alignment.Center,
                ) { Symbol(view.icon, tint = tint) }
                Spacer(Modifier.width(16.dp))
                Column {
                    Text(view.title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
                    Text(
                        view.detail,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.height(16.dp))
            Text("What you copy on your Macs lands on this phone's clipboard.", style = MaterialTheme.typography.bodyMedium)
        }
    }
}

@Composable
private fun Unpaired(onPair: (relay: String, secret: String) -> Unit) {
    var relay by rememberSaveable { mutableStateOf("") }
    var secret by rememberSaveable { mutableStateOf("") }
    var showSecret by rememberSaveable { mutableStateOf(false) }
    val ready = relay.isNotBlank() && secret.isNotBlank()
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Box(
                Modifier.size(48.dp).background(MaterialTheme.colorScheme.primaryContainer, CircleShape),
                contentAlignment = Alignment.Center,
            ) { Symbol(R.drawable.sym_qr_code_scanner, tint = MaterialTheme.colorScheme.onPrimaryContainer) }
            Spacer(Modifier.height(16.dp))
            Text("Pair with your Macs", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
            Spacer(Modifier.height(16.dp))
            Step(1) {
                Text("On a Mac, run ", style = MaterialTheme.typography.bodyLarge)
                Text(
                    "clipsync pair",
                    style = MaterialTheme.typography.bodyMedium,
                    fontFamily = FontFamily.Monospace,
                    modifier = Modifier
                        .background(MaterialTheme.colorScheme.surfaceContainerHighest, RoundedCornerShape(6.dp))
                        .padding(horizontal = 6.dp, vertical = 2.dp),
                )
            }
            Spacer(Modifier.height(12.dp))
            Step(2) { Text("Scan its QR code with this phone's camera", style = MaterialTheme.typography.bodyLarge) }
            Row(Modifier.padding(vertical = 20.dp), verticalAlignment = Alignment.CenterVertically) {
                HorizontalDivider(Modifier.weight(1f))
                Text(
                    "or type what it shows",
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 12.dp),
                )
                HorizontalDivider(Modifier.weight(1f))
            }
            OutlinedTextField(
                value = relay,
                onValueChange = { relay = it },
                label = { Text("Relay") },
                placeholder = { Text("https://clip.example.com") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next, autoCorrectEnabled = false),
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(8.dp))
            OutlinedTextField(
                value = secret,
                onValueChange = { secret = it },
                label = { Text("Secret") },
                singleLine = true,
                visualTransformation = if (showSecret) VisualTransformation.None else PasswordVisualTransformation(),
                trailingIcon = {
                    IconButton(onClick = { showSecret = !showSecret }) {
                        Symbol(
                            if (showSecret) R.drawable.sym_visibility_off else R.drawable.sym_visibility,
                            description = if (showSecret) "Hide secret" else "Show secret",
                        )
                    }
                },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { if (ready) onPair(relay, secret) }),
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(20.dp))
            Button(
                onClick = { onPair(relay, secret) },
                enabled = ready,
                modifier = Modifier.fillMaxWidth().height(56.dp),
            ) { Text("Pair") }
        }
    }
}

/** A numbered pairing step. */
@Composable
private fun Step(number: Int, content: @Composable () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(
            Modifier.size(28.dp).background(MaterialTheme.colorScheme.secondaryContainer, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Text(
                "$number",
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
            )
        }
        Spacer(Modifier.width(12.dp))
        Row(verticalAlignment = Alignment.CenterVertically) { content() }
    }
}

/** A list row inside a card: icon, title, explanation, and an optional action at the end. */
@Composable
private fun Item(icon: Int, title: String, detail: String, action: (@Composable () -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().padding(PaddingValues(vertical = 10.dp)), verticalAlignment = Alignment.CenterVertically) {
        Symbol(icon, tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(16.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(detail, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (action != null) {
            Spacer(Modifier.width(12.dp))
            action()
        }
    }
}

/** A Material Symbols icon from the drawables. */
@Composable
private fun Symbol(
    icon: Int,
    modifier: Modifier = Modifier,
    tint: Color = LocalContentColor.current,
    description: String? = null,
) {
    M3Icon(painterResource(icon), contentDescription = description, modifier = modifier, tint = tint)
}
