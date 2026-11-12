package dev.iyazerski.clipsync

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import dev.chrisbanes.haze.HazeInput
import dev.chrisbanes.haze.HazeState
import dev.chrisbanes.haze.hazeSource
import dev.chrisbanes.haze.rememberHazeState
import dev.chrisbanes.haze.glass.GlassStyle
import dev.chrisbanes.haze.glass.hazeGlass
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.max
import kotlin.math.sin

/** Green for a live connection, the one fixed color on screen. */
val Connected = Color(0xFF5BD68A)

/** The backdrop that glass surfaces below it in the tree refract. */
private val LocalBackdrop = staticCompositionLocalOf<HazeState> { error("No GlassScreen above") }

/** Material 3 theme in the wallpaper's colors, light or dark as the system is. */
@Composable
fun ClipsyncTheme(content: @Composable () -> Unit) {
    val context = LocalContext.current
    // Glass follows the system appearance too, so the theme must not override it.
    val dark = isSystemInDarkTheme()
    val scheme = remember(context, dark) {
        if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
    }
    MaterialTheme(colorScheme = scheme, content = content)
}

/** Full screen [content] over a slowly drifting backdrop that [GlassCard]s refract. */
@Composable
fun GlassScreen(content: @Composable BoxScope.() -> Unit) {
    val backdrop = rememberHazeState()
    Box(Modifier.fillMaxSize()) {
        Backdrop(Modifier.fillMaxSize().hazeSource(backdrop))
        CompositionLocalProvider(LocalBackdrop provides backdrop) { content() }
    }
}

/** The theme's background with soft glows in its colors that drift slowly, so glass has something to bend. */
@Composable
private fun Backdrop(modifier: Modifier) {
    val colors = MaterialTheme.colorScheme
    val glows = remember(colors) {
        listOf(
            Glow(colors.primary, 0.15f, 0.12f, 0.75f, 0f),
            Glow(colors.tertiary, 0.95f, 0.40f, 0.65f, 2.1f),
            Glow(colors.secondary, 0.10f, 0.85f, 0.70f, 4.2f),
        )
    }
    val turn by rememberInfiniteTransition(label = "backdrop").animateFloat(
        initialValue = 0f,
        targetValue = (2 * PI).toFloat(),
        animationSpec = infiniteRepeatable(tween(DRIFT_MS, easing = LinearEasing), RepeatMode.Restart),
        label = "drift",
    )
    Canvas(modifier) {
        drawRect(colors.background)
        val extent = max(size.width, size.height)
        for (glow in glows) {
            val center = Offset(
                size.width * glow.x + extent * 0.08f * cos(turn + glow.phase),
                size.height * glow.y + extent * 0.06f * sin(turn * 2 + glow.phase),
            )
            val radius = extent * glow.size
            drawCircle(
                Brush.radialGradient(
                    0f to glow.color.copy(alpha = 0.45f),
                    0.5f to glow.color.copy(alpha = 0.15f),
                    1f to Color.Transparent,
                    center = center,
                    radius = radius,
                ),
                radius = radius,
                center = center,
            )
        }
    }
}

private class Glow(val color: Color, val x: Float, val y: Float, val size: Float, val phase: Float)

/** A rounded liquid glass surface over the screen's backdrop. */
@Composable
fun GlassCard(modifier: Modifier = Modifier, content: @Composable BoxScope.() -> Unit) {
    val shape = RoundedCornerShape(28.dp)
    val style = remember { GlassStyle.regular.then { shape(shape) } }
    Box(
        modifier
            .hazeGlass(HazeInput.Backdrop(LocalBackdrop.current), style)
            .clip(shape)
            .padding(20.dp),
        content = content,
    )
}

/** One full drift of the backdrop, slow enough to read as still at a glance. */
private const val DRIFT_MS = 40_000
