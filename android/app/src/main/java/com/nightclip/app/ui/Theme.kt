package com.nightclip.app.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// A fixed, compact tool palette. Dynamic wallpaper colors made every phone look different and
// made contrast unpredictable; the product now keeps one calm slate/blue identity.
private val Light = lightColorScheme(
    primary = Color(0xFF0F4C81),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFDCE9F7),
    onPrimaryContainer = Color(0xFF082F52),
    secondary = Color(0xFF0F6B66),
    onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFD8EEEB),
    onSecondaryContainer = Color(0xFF073B38),
    tertiary = Color(0xFF5B5D72),
    onTertiary = Color(0xFFFFFFFF),
    background = Color(0xFFF3F5F8),
    onBackground = Color(0xFF17212B),
    surface = Color(0xFFF9FAFC),
    onSurface = Color(0xFF17212B),
    surfaceVariant = Color(0xFFE6EBF1),
    onSurfaceVariant = Color(0xFF53616F),
    outline = Color(0xFFB5C0CB),
    outlineVariant = Color(0xFFD8E0E8),
    error = Color(0xFFB42318),
    onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFFDE8E7),
    onErrorContainer = Color(0xFF6A1410),
    inverseSurface = Color(0xFF28323C),
    inverseOnSurface = Color(0xFFF1F5F9),
    inversePrimary = Color(0xFF9CC7F0),
    scrim = Color(0xFF000000),
)

private val Dark = darkColorScheme(
    primary = Color(0xFF9CC7F0),
    onPrimary = Color(0xFF00315A),
    primaryContainer = Color(0xFF16466F),
    onPrimaryContainer = Color(0xFFD6E6F7),
    secondary = Color(0xFF8ED4CE),
    onSecondary = Color(0xFF003734),
    secondaryContainer = Color(0xFF17504D),
    onSecondaryContainer = Color(0xFFD3EFEC),
    tertiary = Color(0xFFC4C5DC),
    onTertiary = Color(0xFF2D2F42),
    background = Color(0xFF0C1116),
    onBackground = Color(0xFFE4E9EF),
    surface = Color(0xFF121920),
    onSurface = Color(0xFFE4E9EF),
    surfaceVariant = Color(0xFF1B242D),
    onSurfaceVariant = Color(0xFFAFBCC9),
    outline = Color(0xFF6A7886),
    outlineVariant = Color(0xFF2B3742),
    error = Color(0xFFFFB4AB),
    onError = Color(0xFF690005),
    errorContainer = Color(0xFF5C1A16),
    onErrorContainer = Color(0xFFFFDAD6),
    inverseSurface = Color(0xFFE4E9EF),
    inverseOnSurface = Color(0xFF1A222A),
    inversePrimary = Color(0xFF0F4C81),
    scrim = Color(0xFF000000),
)

private val AppTypography = Typography(
    displaySmall = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 28.sp, lineHeight = 32.sp, letterSpacing = (-0.3).sp),
    headlineSmall = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 22.sp, lineHeight = 27.sp, letterSpacing = (-0.2).sp),
    titleLarge = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 18.sp, lineHeight = 23.sp),
    titleMedium = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 15.sp, lineHeight = 20.sp),
    titleSmall = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 13.sp, lineHeight = 18.sp, letterSpacing = 0.1.sp),
    bodyLarge = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Normal, fontSize = 14.sp, lineHeight = 20.sp),
    bodyMedium = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Normal, fontSize = 13.sp, lineHeight = 18.sp),
    bodySmall = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Normal, fontSize = 12.sp, lineHeight = 16.sp),
    labelLarge = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Medium, fontSize = 13.sp, lineHeight = 18.sp),
    labelMedium = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Medium, fontSize = 11.sp, lineHeight = 15.sp, letterSpacing = 0.2.sp),
    labelSmall = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Medium, fontSize = 10.sp, lineHeight = 14.sp, letterSpacing = 0.3.sp),
)

private val AppShapes = Shapes(
    extraSmall = RoundedCornerShape(6.dp),
    small = RoundedCornerShape(8.dp),
    medium = RoundedCornerShape(12.dp),
    large = RoundedCornerShape(16.dp),
    extraLarge = RoundedCornerShape(24.dp),
)

@Composable
fun ClipBridgeTheme(content: @Composable () -> Unit) {
    val dark = isSystemInDarkTheme()
    MaterialTheme(
        colorScheme = if (dark) Dark else Light,
        typography = AppTypography,
        shapes = AppShapes,
        content = content,
    )
}
