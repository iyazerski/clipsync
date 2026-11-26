@file:OptIn(ExperimentalMaterial3ExpressiveApi::class)

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
import androidx.activity.viewModels
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.Crossfade
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.input.TextFieldLineLimits
import androidx.compose.foundation.text.input.rememberTextFieldState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.DropdownMenuGroup
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.DropdownMenuPopup
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.FloatingActionButtonDefaults
import androidx.compose.material3.IconButton
import androidx.compose.material3.LargeFlexibleTopAppBar
import androidx.compose.material3.LinearWavyProgressIndicator
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.LoadingIndicator
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialShapes
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.MediumExtendedFloatingActionButton
import androidx.compose.material3.MenuDefaults
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedListItem
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.toShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.graphics.shapes.RoundedPolygon
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.net.URI

/**
 * The only screen: pair with the computers (from the relay's pairing link, or by typing the relay and
 * the secret), see whether sync is connected, and set up the ways to send.
 */
class MainActivity : ComponentActivity() {
    private lateinit var settings: Settings
    private val snackbar = SnackbarHostState()
    private var setup by mutableStateOf(Setup())
    private var syncState by mutableStateOf(SyncService.state)
    /** The update check's progress, or null when idle. */
    private var updating by mutableStateOf<String?>(null)
    private val joining: JoinModel by viewModels()
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
        setContent { ClipsyncTheme { Screen() } }
        if (savedInstanceState == null) handlePairing(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handlePairing(intent)
    }

    override fun onResume() {
        super.onResume()
        SyncService.onState = { syncState = it }
        if (settings.paired) SyncService.start(this)
        // The tile, battery, or notification settings may have changed while the app was away.
        setup = readSetup()
        // State changes while paused don't reach the screen.
        syncState = SyncService.state
    }

    override fun onPause() {
        SyncService.onState = null
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
        if (intent == null || intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY != 0) return
        val link = intent.data?.takeIf { intent.action == Intent.ACTION_VIEW } ?: return
        intent.data = null
        pair(link.getQueryParameter("relay").orEmpty(), link.getQueryParameter("secret").orEmpty())
    }

    private fun pair(relay: String, secret: String) {
        try {
            settings.pair(relay, secret)
        } catch (e: Invalid) {
            notify(e.reason.read(this))
            return
        }
        notify(getString(R.string.paired))
        // Android lets only a visible app start the service; onResume starts it otherwise.
        if (lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) SyncService.reconnect(this)
        setup = readSetup()
    }

    /** Pair from a pairing link at once, or from a code through [relay]. */
    private fun join(relay: String, text: String) {
        CodePairing.parseLink(text)?.let { return pair(it.relay, it.secret) }
        try {
            joining.join(Relay.address(relay), CodePairing.parse(text) ?: throw Invalid(R.string.code_invalid))
        } catch (e: Invalid) {
            joining.error = e.reason
        }
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
            getString(R.string.send_clipboard),
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
            Toast.makeText(this, R.string.allow_installs, Toast.LENGTH_LONG).show()
            startActivity(Intent(SystemSettings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, "package:$packageName".toUri()))
            return
        }
        Updater.update(this) { updating = it }
    }

    @Composable
    private fun Screen() {
        // Each screen keeps its own scroll, so pairing from deep in the form, or unpairing, starts the other at its top.
        val (bar, scroll) = key(setup.paired) { TopAppBarDefaults.exitUntilCollapsedScrollBehavior() to rememberScrollState() }
        // The menu starts updates from any scroll position, and their progress shows at the top.
        LaunchedEffect(updating != null) {
            if (updating != null) scroll.animateScrollTo(0)
        }
        val motion = MaterialTheme.motionScheme
        val haptics = LocalHapticFeedback.current
        var confirmUnpair by rememberSaveable { mutableStateOf(false) }
        LaunchedEffect(joining.joined) {
            val joined = joining.joined ?: return@LaunchedEffect
            joining.joined = null
            if (!settings.paired) pair(joined.relay, joined.secret)
        }
        // Lists and cards in the bright surface color stand out from it, in light and dark alike.
        val background = MaterialTheme.colorScheme.surfaceContainer
        Scaffold(
            modifier = Modifier.nestedScroll(bar.nestedScrollConnection),
            containerColor = background,
            topBar = {
                LargeFlexibleTopAppBar(
                    title = { Text(stringResource(R.string.app_name)) },
                    subtitle = if (setup.paired) ({ Text(setup.relayHost) }) else null,
                    actions = { Menu(onUnpair = { confirmUnpair = true }) },
                    colors = TopAppBarDefaults.topAppBarColors(
                        containerColor = background,
                        scrolledContainerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
                    ),
                    scrollBehavior = bar,
                )
            },
            floatingActionButton = {
                AnimatedVisibility(
                    setup.paired,
                    enter = scaleIn(motion.defaultSpatialSpec()) + fadeIn(motion.defaultEffectsSpec()),
                    exit = scaleOut(motion.fastSpatialSpec()) + fadeOut(motion.fastEffectsSpec()),
                ) {
                    MediumExtendedFloatingActionButton(onClick = {
                        haptics.performHapticFeedback(HapticFeedbackType.Confirm)
                        sendClipboard()
                    }) {
                        Symbol(R.drawable.sym_content_paste_go, Modifier.size(FloatingActionButtonDefaults.MediumIconSize))
                        Spacer(Modifier.width(12.dp))
                        Text(stringResource(R.string.send_clipboard))
                    }
                }
            },
            snackbarHost = { SnackbarHost(snackbar) },
        ) { insets ->
            Column(
                Modifier
                    .fillMaxSize()
                    .consumeWindowInsets(insets)
                    .imePadding()
                    .verticalScroll(scroll)
                    // Inside the scroll, so the list scrolls behind the navigation bar.
                    .padding(insets)
                    .padding(horizontal = 16.dp),
                verticalArrangement = Arrangement.spacedBy(24.dp),
            ) {
                UpdateProgress(updating)
                AnimatedContent(
                    targetState = setup.paired,
                    transitionSpec = {
                        (fadeIn(motion.defaultEffectsSpec()) + slideInVertically(motion.defaultSpatialSpec()) { it / 8 }) togetherWith
                            fadeOut(motion.fastEffectsSpec())
                    },
                    label = "screen",
                ) { paired ->
                    Column(verticalArrangement = Arrangement.spacedBy(24.dp)) {
                        // The form stays on screen while it fades out, so a late Done mustn't replace a new pairing.
                        if (paired) Paired() else Unpaired(joining.busy, joining.error, { joining.error = null }) { relay, text -> if (!setup.paired) join(relay, text) }
                    }
                }
                // Room to scroll the last row out from under the Send button.
                Spacer(Modifier.height(if (setup.paired) 96.dp else 8.dp))
            }
        }
        if (confirmUnpair) {
            AlertDialog(
                onDismissRequest = { confirmUnpair = false },
                icon = { Symbol(R.drawable.sym_link_off) },
                title = { Text(stringResource(R.string.unpair_title)) },
                text = { Text(stringResource(R.string.unpair_text)) },
                confirmButton = {
                    TextButton(
                        onClick = {
                            confirmUnpair = false
                            unpair()
                        },
                        colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error),
                    ) { Text(stringResource(R.string.unpair)) }
                },
                dismissButton = { TextButton(onClick = { confirmUnpair = false }) { Text(stringResource(R.string.cancel)) } },
            )
        }
    }

    /** The top bar's overflow menu: updates, the version, and unpairing. */
    @Composable
    private fun Menu(onUnpair: () -> Unit) {
        var open by remember { mutableStateOf(false) }
        val versionName = remember { packageManager.getPackageInfo(packageName, 0).versionName }
        val count = if (setup.paired) 2 else 1
        Box {
            IconButton(onClick = { open = true }) { Symbol(R.drawable.sym_more_vert, description = stringResource(R.string.more)) }
            DropdownMenuPopup(expanded = open, onDismissRequest = { open = false }) {
                DropdownMenuGroup(shapes = MenuDefaults.groupShape(0, 1)) {
                    DropdownMenuItem(
                        onClick = {
                            open = false
                            checkForUpdates()
                        },
                        text = { Text(stringResource(R.string.check_for_updates)) },
                        supportingText = { Text(stringResource(R.string.version, versionName.orEmpty())) },
                        shape = MenuDefaults.itemShape(0, count).shape,
                        leadingIcon = { Symbol(R.drawable.sym_system_update) },
                        enabled = updating == null,
                    )
                    if (setup.paired) {
                        val error = MaterialTheme.colorScheme.error
                        DropdownMenuItem(
                            onClick = {
                                open = false
                                onUnpair()
                            },
                            text = { Text(stringResource(R.string.unpair)) },
                            shape = MenuDefaults.itemShape(1, count).shape,
                            leadingIcon = { Symbol(R.drawable.sym_link_off) },
                            colors = MenuDefaults.itemColors(textColor = error, leadingIconColor = error),
                        )
                    }
                }
            }
        }
    }

    @Composable
    private fun Paired() {
        StatusHero(syncState)
        val fixes = buildList {
            if (!setup.battery) {
                add(
                    Fix(
                        R.drawable.sym_battery_android_full,
                        stringResource(R.string.fix_background),
                        stringResource(R.string.fix_background_detail),
                        ::allowBackground,
                    ),
                )
            }
            if (!setup.notifications) {
                add(
                    Fix(
                        R.drawable.sym_notifications,
                        stringResource(R.string.fix_notifications),
                        stringResource(R.string.fix_notifications_detail),
                        ::allowNotifications,
                    ),
                )
            }
        }
        if (fixes.isNotEmpty()) {
            val colors = MaterialTheme.colorScheme
            Section(stringResource(R.string.finish_setup)) {
                fixes.forEachIndexed { index, fix ->
                    SegmentedListItem(
                        onClick = fix.onClick,
                        shapes = ListItemDefaults.segmentedShapes(index, fixes.size),
                        colors = ListItemDefaults.segmentedColors(
                            containerColor = colors.tertiaryContainer,
                            contentColor = colors.onTertiaryContainer,
                            leadingContentColor = colors.onTertiaryContainer,
                            trailingContentColor = colors.onTertiaryContainer,
                            supportingContentColor = colors.onTertiaryContainer,
                        ),
                        leadingContent = { Symbol(fix.icon) },
                        trailingContent = { Symbol(R.drawable.sym_chevron_right) },
                        supportingContent = { Text(fix.detail) },
                    ) { Text(fix.title) }
                }
            }
        }
        Section(stringResource(R.string.more_ways)) {
            InfoRow(0, 4, R.drawable.sym_text_select_start, stringResource(R.string.way_select), stringResource(R.string.way_select_detail))
            InfoRow(1, 4, R.drawable.sym_share, stringResource(R.string.way_share), stringResource(R.string.way_share_detail))
            InfoRow(2, 4, R.drawable.sym_notifications, stringResource(R.string.way_notification), stringResource(R.string.way_notification_detail))
            if (setup.tileAdded) {
                InfoRow(3, 4, R.drawable.sym_widgets, stringResource(R.string.way_tile), stringResource(R.string.way_tile_detail)) {
                    Symbol(R.drawable.sym_check, tint = MaterialTheme.colorScheme.primary, description = stringResource(R.string.added))
                }
            } else {
                SegmentedListItem(
                    onClick = ::addTile,
                    shapes = ListItemDefaults.segmentedShapes(3, 4),
                    colors = rowColors(),
                    leadingContent = { Symbol(R.drawable.sym_widgets) },
                    trailingContent = { Symbol(R.drawable.sym_add, description = stringResource(R.string.add)) },
                    supportingContent = { Text(stringResource(R.string.way_tile_add)) },
                ) { Text(stringResource(R.string.way_tile)) }
            }
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

/** A setting that sync needs, and how to open it. */
private class Fix(val icon: Int, val title: String, val detail: String, val onClick: () -> Unit)

/** How a [SyncState] reads on screen. */
private class StatusView(
    val title: String,
    val detail: String,
    val shape: RoundedPolygon,
    val icon: Int,
    val container: Color,
    val accent: Color,
    val onAccent: Color,
)

@Composable
private fun statusView(state: SyncState): StatusView {
    val colors = MaterialTheme.colorScheme
    return when (state) {
        is SyncState.Connected -> StatusView(
            stringResource(R.string.status_connected),
            when {
                state.warning != null -> state.warning.text()
                state.lastClipAt != null -> stringResource(R.string.status_last_clip, formatClipTime(state.lastClipAt))
                else -> stringResource(R.string.status_waiting)
            },
            MaterialShapes.Cookie9Sided,
            R.drawable.sym_check,
            colors.primaryContainer,
            colors.primary,
            colors.onPrimary,
        )
        // A loading indicator takes the place of the shape and icon.
        SyncState.Connecting -> StatusView(
            stringResource(R.string.status_connecting),
            stringResource(R.string.status_reaching),
            MaterialShapes.Cookie9Sided,
            R.drawable.sym_check,
            colors.surfaceBright,
            colors.primary,
            colors.onPrimary,
        )
        SyncState.Offline -> StatusView(
            stringResource(R.string.status_offline),
            stringResource(R.string.status_retrying),
            MaterialShapes.Ghostish,
            R.drawable.sym_cloud_off,
            colors.surfaceBright,
            colors.secondary,
            colors.onSecondary,
        )
        SyncState.Stopped -> StatusView(
            stringResource(R.string.status_stopped),
            stringResource(R.string.status_resume),
            MaterialShapes.Ghostish,
            R.drawable.sym_cloud_off,
            colors.surfaceBright,
            colors.secondary,
            colors.onSecondary,
        )
        is SyncState.Error -> StatusView(
            stringResource(R.string.status_not_syncing),
            state.reason.text(),
            MaterialShapes.SoftBurst,
            R.drawable.sym_priority_high,
            colors.errorContainer,
            colors.error,
            colors.onError,
        )
    }
}

/** The sync state, big and first: a shape that morphs with it, and what it means. */
@Composable
private fun StatusHero(state: SyncState) {
    val view = statusView(state)
    val motion = MaterialTheme.motionScheme
    // While Connecting fades the badge out, it keeps the shape and icon it had.
    var badge by remember { mutableStateOf(view) }
    SideEffect { if (state != SyncState.Connecting) badge = view }
    val container by animateColorAsState(view.container, motion.defaultEffectsSpec(), label = "container")
    val accent by animateColorAsState(view.accent, motion.defaultEffectsSpec(), label = "accent")
    Surface(
        color = container,
        shape = MaterialTheme.shapes.extraLargeIncreased,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(24.dp)) {
            Crossfade(state == SyncState.Connecting, Modifier.size(96.dp), motion.defaultEffectsSpec(), label = "badge") { connecting ->
                if (connecting) {
                    LoadingIndicator(Modifier.fillMaxSize(), color = accent)
                } else {
                    val shown = if (state == SyncState.Connecting) badge else view
                    MorphingShape(shown.shape, accent, Modifier.fillMaxSize()) {
                        Symbol(shown.icon, Modifier.size(40.dp), tint = shown.onAccent)
                    }
                }
            }
            Spacer(Modifier.height(20.dp))
            Text(view.title, style = MaterialTheme.typography.headlineLargeEmphasized)
            Text(view.detail, style = MaterialTheme.typography.bodyLarge)
            Spacer(Modifier.height(16.dp))
            Text(
                stringResource(R.string.status_about),
                style = MaterialTheme.typography.bodyMedium,
                color = LocalContentColor.current.copy(alpha = 0.75f),
            )
        }
    }
}

/** The update check's progress, shown while there is some. */
@Composable
private fun UpdateProgress(progress: String?) {
    // Keeps the last text on screen while the card collapses.
    var last by remember { mutableStateOf("") }
    SideEffect { if (progress != null) last = progress }
    AnimatedVisibility(progress != null, enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
        Surface(
            color = MaterialTheme.colorScheme.secondaryContainer,
            shape = MaterialTheme.shapes.large,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(
                    progress ?: last,
                    style = MaterialTheme.typography.titleMediumEmphasized,
                    modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                )
                LinearWavyProgressIndicator(Modifier.fillMaxWidth())
            }
        }
    }
}

/** Joining by code, which outlives the activity's recreation so that a rotation doesn't lose the pairing. */
class JoinModel : ViewModel() {
    var busy by mutableStateOf(false)
        private set
    var error by mutableStateOf<Message?>(null)
    /** The pairing that the code brought, for the activity to save. */
    var joined by mutableStateOf<CodePairing.Pairing?>(null)

    fun join(relay: String, code: String) {
        busy = true
        error = null
        viewModelScope.launch {
            val result = withContext(Dispatchers.IO) { runCatching { CodePairing.join(relay, code) } }
            busy = false
            result.onSuccess { joined = it }.onFailure { e ->
                error = when (e) {
                    is Failed -> e.reason
                    is IOException -> Message(R.string.code_offline, listOf(URI(relay).host))
                    else -> throw e
                }
            }
        }
    }
}

@Composable
private fun Unpaired(joining: Boolean, error: Message?, onEdit: () -> Unit, onJoin: (relay: String, code: String) -> Unit) {
    Column(Modifier.fillMaxWidth().padding(top = 8.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        MorphingShape(MaterialShapes.Cookie12Sided, MaterialTheme.colorScheme.primaryContainer, Modifier.size(136.dp)) {
            Symbol(R.drawable.sym_qr_code_scanner, Modifier.size(56.dp), tint = MaterialTheme.colorScheme.onPrimaryContainer)
        }
        Spacer(Modifier.height(24.dp))
        Text(
            stringResource(R.string.pair_title),
            style = MaterialTheme.typography.headlineMediumEmphasized,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(8.dp))
        Text(
            stringResource(R.string.pair_intro),
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
    }
    Section(stringResource(R.string.pair_steps)) {
        val button = MaterialTheme.colorScheme.primary
        // The button's label goes where the translation puts it, in its own style.
        val (before, after) = stringResource(R.string.pair_step_open).split("%1\$s")
        val label = stringResource(R.string.pair_add_device)
        Step(
            1,
            buildAnnotatedString {
                append(before)
                withStyle(SpanStyle(fontWeight = FontWeight.SemiBold, color = button)) { append(label) }
                append(after)
            },
            stringResource(R.string.pair_step_run_detail),
        )
        Step(2, AnnotatedString(stringResource(R.string.pair_step_scan)), stringResource(R.string.pair_step_scan_detail))
    }
    CodeEntry(joining, error, onEdit, onJoin)
}

/** Typing the code that the computer shows, for when the camera can't scan the QR code. */
@Composable
private fun CodeEntry(joining: Boolean, error: Message?, onEdit: () -> Unit, onJoin: (relay: String, code: String) -> Unit) {
    var open by rememberSaveable { mutableStateOf(false) }
    val motion = MaterialTheme.motionScheme
    AnimatedContent(
        targetState = open,
        transitionSpec = { fadeIn(motion.defaultEffectsSpec()) togetherWith fadeOut(motion.fastEffectsSpec()) },
        label = "manual",
    ) { typing ->
        if (!typing) {
            Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                OutlinedButton(onClick = { open = true }, shapes = ButtonDefaults.shapes()) {
                    Symbol(R.drawable.sym_keyboard, Modifier.size(ButtonDefaults.IconSize))
                    Spacer(Modifier.width(ButtonDefaults.IconSpacing))
                    Text(stringResource(R.string.pair_manually))
                }
            }
            return@AnimatedContent
        }
        val code = rememberTextFieldState()
        val relay = rememberTextFieldState("https://clip.iyazerski.dev")
        LaunchedEffect(code.text) { onEdit() }
        val focus = remember { FocusRequester() }
        LaunchedEffect(Unit) { focus.requestFocus() }
        val ready = !joining && relay.text.isNotBlank() && code.text.isNotBlank()
        val join = { if (ready) onJoin(relay.text.toString(), code.text.toString()) }
        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
            OutlinedTextField(
                state = code,
                label = { Text(stringResource(R.string.code)) },
                placeholder = { Text("42 183 059") },
                isError = error != null,
                supportingText = error?.let { { Text(it.text()) } },
                enabled = !joining,
                lineLimits = TextFieldLineLimits.SingleLine,
                textStyle = MaterialTheme.typography.titleLarge.copy(textDirection = TextDirection.Ltr),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number, imeAction = ImeAction.Done, autoCorrectEnabled = false),
                onKeyboardAction = { join() },
                modifier = Modifier.fillMaxWidth().focusRequester(focus),
            )
            OutlinedTextField(
                state = relay,
                label = { Text(stringResource(R.string.relay)) },
                supportingText = { Text(stringResource(R.string.relay_detail)) },
                enabled = !joining,
                lineLimits = TextFieldLineLimits.SingleLine,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, autoCorrectEnabled = false),
                modifier = Modifier.fillMaxWidth(),
            )
            val height = ButtonDefaults.MediumContainerHeight
            Button(
                onClick = join,
                enabled = ready,
                shapes = ButtonDefaults.shapesFor(height),
                contentPadding = ButtonDefaults.contentPaddingFor(height),
                modifier = Modifier.fillMaxWidth().heightIn(min = height).padding(top = 8.dp),
            ) {
                if (joining) {
                    LoadingIndicator(Modifier.size(ButtonDefaults.IconSize * 1.5f), color = LocalContentColor.current)
                    Spacer(Modifier.width(ButtonDefaults.IconSpacing))
                }
                Text(stringResource(if (joining) R.string.joining else R.string.pair), style = ButtonDefaults.textStyleFor(height))
            }
        }
    }
}

@Composable
private fun Message.text() = stringResource(id, *args.toTypedArray())

/** A titled group of rows that join into one rounded block. */
@Composable
private fun Section(title: String, content: @Composable ColumnScope.() -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(ListItemDefaults.SegmentedGap)) {
        Text(
            title,
            style = MaterialTheme.typography.titleSmallEmphasized,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(start = 16.dp, bottom = 8.dp),
        )
        content()
    }
}

/** Colors for the rows of a [Section]. */
@Composable
private fun rowColors() = ListItemDefaults.segmentedColors(containerColor = MaterialTheme.colorScheme.surfaceBright)

/** Row [index] of [count] in a [Section], which only explains. */
@Composable
private fun InfoRow(index: Int, count: Int, icon: Int, title: String, detail: String, trailing: (@Composable () -> Unit)? = null) {
    SegmentedListItem(
        shapes = ListItemDefaults.segmentedShapes(index, count),
        colors = rowColors(),
        leadingContent = { Symbol(icon) },
        trailingContent = trailing,
        supportingContent = { Text(detail) },
    ) { Text(title) }
}

/** A numbered pairing step in a [Section] of two. */
@Composable
private fun Step(number: Int, title: AnnotatedString, detail: String) {
    val colors = MaterialTheme.colorScheme
    SegmentedListItem(
        shapes = ListItemDefaults.segmentedShapes(number - 1, 2),
        colors = rowColors(),
        leadingContent = {
            Box(
                Modifier.size(40.dp).background(colors.primary, MaterialShapes.Cookie4Sided.toShape()),
                contentAlignment = Alignment.Center,
            ) { Text("$number", style = MaterialTheme.typography.titleMediumEmphasized, color = colors.onPrimary) }
        },
        supportingContent = { Text(detail) },
    ) { Text(title) }
}
