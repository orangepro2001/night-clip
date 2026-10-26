package com.nightclip.app.logic

/**
 * Pure "is the Shizuku-hosted background clipboard read ready, and what is missing" logic. No Android
 * classes so the rules can be unit-tested; the platform layer only supplies the facts.
 */
enum class ShizukuStatus {
    /** The user has not turned the feature on. */
    OFF,

    /** Real-time mode (the foreground service) keeps the connection alive; it is off. */
    REALTIME_OFF,

    /** The Shizuku app is not installed. */
    NOT_INSTALLED,

    /** Shizuku is installed but its service is not running (it must be started after every reboot). */
    NOT_RUNNING,

    /** Shizuku runs, but ClipBridge has not been allowed to use it. */
    NEEDS_PERMISSION,

    /** Everything is granted, but the foreground service that hosts the connection is not running. */
    WAITING_SERVICE,

    /** The helper is being started. */
    CONNECTING,

    /** The helper runs and reports clipboard changes. */
    RUNNING,

    /** A failure code was recorded; the UI shows it with a retry action. */
    ERROR,
}

data class ShizukuFacts(
    val enabled: Boolean,
    val realtime: Boolean,
    val installed: Boolean,
    val shizukuRunning: Boolean,
    val permitted: Boolean,
    val serviceRunning: Boolean,
    val connected: Boolean,
    val hasError: Boolean,
)

object ShizukuSetup {
    /** Only this combination may start the helper. */
    fun canRun(f: ShizukuFacts): Boolean =
        f.enabled && f.realtime && f.installed && f.shizukuRunning && f.permitted && f.serviceRunning

    fun status(f: ShizukuFacts): ShizukuStatus = when {
        !f.enabled -> ShizukuStatus.OFF
        !f.realtime -> ShizukuStatus.REALTIME_OFF
        !f.installed -> ShizukuStatus.NOT_INSTALLED
        !f.shizukuRunning -> ShizukuStatus.NOT_RUNNING
        !f.permitted -> ShizukuStatus.NEEDS_PERMISSION
        !f.serviceRunning -> ShizukuStatus.WAITING_SERVICE
        f.hasError -> ShizukuStatus.ERROR
        !f.connected -> ShizukuStatus.CONNECTING
        else -> ShizukuStatus.RUNNING
    }
}
