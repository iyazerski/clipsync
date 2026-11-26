@file:OptIn(ExperimentalMaterial3ExpressiveApi::class, ExperimentalTextApi::class)

package dev.iyazerski.clipsync

import androidx.compose.animation.core.Animatable
import androidx.compose.foundation.background
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialExpressiveTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.Typography
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.toPath
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.center
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Matrix
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.ExperimentalTextApi
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontVariation
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.graphics.shapes.Morph
import androidx.graphics.shapes.RoundedPolygon

private val Manrope = FontFamily(
    Font(R.font.manrope, FontWeight.Normal, variationSettings = FontVariation.Settings(FontVariation.weight(400))),
    Font(R.font.manrope, FontWeight.Medium, variationSettings = FontVariation.Settings(FontVariation.weight(500))),
    Font(R.font.manrope, FontWeight.SemiBold, variationSettings = FontVariation.Settings(FontVariation.weight(600))),
    Font(R.font.manrope, FontWeight.Bold, variationSettings = FontVariation.Settings(FontVariation.weight(700))),
    Font(R.font.manrope, FontWeight.ExtraBold, variationSettings = FontVariation.Settings(FontVariation.weight(800))),
)

private val BaseTypography = Typography(fontFamily = Manrope)

private val ClipsyncTypography = BaseTypography.copy(
    headlineLargeEmphasized = BaseTypography.headlineLargeEmphasized.copy(
        fontWeight = FontWeight.ExtraBold,
        letterSpacing = (-0.6).sp,
    ),
    headlineMediumEmphasized = BaseTypography.headlineMediumEmphasized.copy(
        fontWeight = FontWeight.ExtraBold,
        letterSpacing = (-0.5).sp,
    ),
    headlineSmallEmphasized = BaseTypography.headlineSmallEmphasized.copy(
        fontWeight = FontWeight.ExtraBold,
        letterSpacing = (-0.4).sp,
    ),
    titleLargeEmphasized = BaseTypography.titleLargeEmphasized.copy(
        fontWeight = FontWeight.Bold,
        letterSpacing = (-0.25).sp,
    ),
    titleMediumEmphasized = BaseTypography.titleMediumEmphasized.copy(
        fontWeight = FontWeight.Bold,
        letterSpacing = (-0.15).sp,
    ),
    titleSmallEmphasized = BaseTypography.titleSmallEmphasized.copy(
        fontWeight = FontWeight.Bold,
    ),
    bodyLarge = BaseTypography.bodyLarge.copy(
        fontWeight = FontWeight.Medium,
    ),
    labelLarge = BaseTypography.labelLarge.copy(
        fontWeight = FontWeight.Bold,
    ),
    labelMedium = BaseTypography.labelMedium.copy(
        fontWeight = FontWeight.Bold,
    ),
)

/**
 * Surface layering and harmonized semantic colors that stay recognizable across wallpapers:
 * emerald for live sync, warm amber for setup reminders.
 */
@Immutable
data class ClipsyncAccents(
    val canvas: Color,
    val card: Color,
    val chip: Color,
    val connectedContainer: Color,
    val onConnectedContainer: Color,
    val connectedSubtle: Color,
    val connectedAccent: Color,
    val onConnectedAccent: Color,
    val onlineIconContainer: Color,
    val onlineIconContent: Color,
    val warningContainer: Color,
    val onWarningContainer: Color,
    val warningIconContainer: Color,
    val warningIconContent: Color,
)

private fun buildAccents(scheme: ColorScheme, dark: Boolean): ClipsyncAccents {
    // Subtle pull toward the wallpaper's primary keeps semantic hues cohesive without losing meaning.
    fun harmonize(color: Color) = lerp(color, scheme.primary, 0.06f)
    return if (dark) {
        ClipsyncAccents(
            canvas = scheme.surface,
            card = scheme.surfaceContainer,
            chip = scheme.surfaceContainerHigh,
            connectedContainer = harmonize(Color(0xFF153324)),
            onConnectedContainer = Color(0xFFC5F2D6),
            connectedSubtle = Color(0xFF8FD4AC),
            connectedAccent = Color(0xFF5AD18B),
            onConnectedAccent = Color(0xFF072916),
            onlineIconContainer = harmonize(Color(0xFF1B3B2A)),
            onlineIconContent = Color(0xFF6FE39F),
            warningContainer = harmonize(Color(0xFF3A2A0E)),
            onWarningContainer = Color(0xFFFCE0A8),
            warningIconContainer = Color(0xFF503A12),
            warningIconContent = Color(0xFFF5B841),
        )
    } else {
        ClipsyncAccents(
            canvas = scheme.surfaceContainer,
            card = scheme.surfaceContainerLowest,
            chip = scheme.surfaceContainerHigh,
            connectedContainer = harmonize(Color(0xFFDCEFE3)),
            onConnectedContainer = Color(0xFF0F3D24),
            connectedSubtle = Color(0xFF275B3E),
            connectedAccent = Color(0xFF1E7E4B),
            onConnectedAccent = Color(0xFFFFFFFF),
            onlineIconContainer = harmonize(Color(0xFFD7F2E1)),
            onlineIconContent = Color(0xFF146C3C),
            warningContainer = harmonize(Color(0xFFFDECC8)),
            onWarningContainer = Color(0xFF5E3700),
            warningIconContainer = Color(0xFFF6DC9F),
            warningIconContent = Color(0xFFA85F00),
        )
    }
}

val LocalClipsyncAccents = staticCompositionLocalOf<ClipsyncAccents> {
    error("ClipsyncAccents not provided")
}

private val ClipsyncShapes = Shapes(
    large = RoundedCornerShape(22.dp),
)

/** Material 3 Expressive in the wallpaper's colors, with geometric typography and harmonized status hues. */
@Composable
fun ClipsyncTheme(content: @Composable () -> Unit) {
    val context = LocalContext.current
    val dark = isSystemInDarkTheme()
    val scheme = remember(context, dark) {
        if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
    }
    val accents = remember(scheme, dark) { buildAccents(scheme, dark) }
    CompositionLocalProvider(LocalClipsyncAccents provides accents) {
        MaterialExpressiveTheme(
            colorScheme = scheme,
            shapes = ClipsyncShapes,
            typography = ClipsyncTypography,
            content = content,
        )
    }
}

/** A [color] filled [shape] behind [content] that morphs with a spring when [shape] changes. */
@Composable
fun MorphingShape(
    shape: RoundedPolygon,
    color: Color,
    modifier: Modifier = Modifier,
    content: @Composable BoxScope.() -> Unit,
) {
    var from by remember { mutableStateOf(shape) }
    var to by remember { mutableStateOf(shape) }
    val progress = remember { Animatable(1f) }
    val spec = MaterialTheme.motionScheme.slowSpatialSpec<Float>()
    LaunchedEffect(shape) {
        if (shape == to) return@LaunchedEffect
        from = to
        to = shape
        progress.snapTo(0f)
        progress.animateTo(1f, spec)
    }
    val morph = remember(from, to) { Morph(from, to) }
    Box(
        modifier.drawWithCache {
            val path = Path()
            val scale = Matrix().apply { scale(size.width, size.height) }
            onDrawBehind {
                path.rewind()
                // The spring overshoots, and a morph past its ends turns inside out.
                morph.toPath(progress.value.coerceIn(0f, 1f), path)
                path.transform(scale)
                path.translate(size.center - path.getBounds().center)
                drawPath(path, color)
            }
        },
        contentAlignment = Alignment.Center,
        content = content,
    )
}

/** A Material Symbols icon from the drawables. */
@Composable
fun Symbol(
    icon: Int,
    modifier: Modifier = Modifier,
    tint: Color = LocalContentColor.current,
    description: String? = null,
) {
    Icon(painterResource(icon), contentDescription = description, modifier = modifier, tint = tint)
}

/** A rounded-square icon well for list rows. */
@Composable
fun IconBadge(
    icon: Int,
    containerColor: Color,
    contentColor: Color,
    modifier: Modifier = Modifier,
    size: Dp = 40.dp,
    iconSize: Dp = 20.dp,
) {
    Box(
        modifier.size(size).background(containerColor, RoundedCornerShape(14.dp)),
        contentAlignment = Alignment.Center,
    ) {
        Symbol(icon, Modifier.size(iconSize), tint = contentColor)
    }
}

/** A compact status dot. */
@Composable
fun StatusDot(color: Color, modifier: Modifier = Modifier, size: Dp = 7.dp) {
    Box(modifier.size(size).background(color, CircleShape))
}

/** A compact tonal pill for row tags and inline actions. */
@Composable
fun TagPill(
    text: String,
    icon: Int? = null,
    containerColor: Color = MaterialTheme.colorScheme.primaryContainer,
    contentColor: Color = MaterialTheme.colorScheme.onPrimaryContainer,
) {
    Surface(color = containerColor, contentColor = contentColor, shape = CircleShape) {
        Row(
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 5.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            if (icon != null) Symbol(icon, Modifier.size(15.dp))
            Text(
                text,
                style = MaterialTheme.typography.labelMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}
