package com.nightclip.app.logic

/**
 * Pure "is background auto send ready, and what is missing" logic for the opt-in Android 10+
 * background clipboard read (docs/adr/006-android-background-read.md). No Android classes here so
 * the setup rules can be unit-tested; the platform layer only supplies the facts.
 */
enum class BackgroundReadStep {
    /** Real-time mode keeps the process and the logcat watcher alive. */
    REALTIME,

    /** SYSTEM_ALERT_WINDOW: needed to take window focus for the actual clipboard read. */
    OVERLAY,

    /** READ_LOGS: needed only to notice that a copy happened while the app is in the background. */
    LOGS,
}

enum class BackgroundReadStatus {
    /** The user has not turned the feature on. */
    OFF,

    /** Android 9 or older: the denial-log signal does not exist, so the feature is not offered. */
    UNSUPPORTED,

    /** The user turned it on, but real-time mode is off, so nothing can run. */
    REALTIME_OFF,

    /** System overlay permission is missing. */
    NEEDS_OVERLAY,

    /** READ_LOGS is missing (or was revoked after being granted). */
    NEEDS_LOGS,

    /** Everything is granted, but the foreground service that hosts the watcher is not running. */
    WAITING_SERVICE,

    /** The service is running and the watcher is starting or restarting. */
    STARTING,

    /** The logcat watcher is running. */
    RUNNING,

    /** A permission/process failure was recorded; the UI shows the reason and retry action. */
    ERROR,
}

/** What the shell knows at a given moment. [hasError] is a code, never clipboard text. */
data class BackgroundReadFacts(
    val enabled: Boolean,
    val realtime: Boolean,
    val overlayGranted: Boolean,
    val logsGranted: Boolean,
    val serviceRunning: Boolean,
    val watcherRunning: Boolean,
    val hasError: Boolean,
    val supported: Boolean = true,
)

object BackgroundReadSetup {
    /** Only this combination is allowed to start the logcat watcher. */
    fun canRun(f: BackgroundReadFacts): Boolean =
        f.supported && f.enabled && f.realtime && f.overlayGranted && f.logsGranted && f.serviceRunning

    fun status(f: BackgroundReadFacts): BackgroundReadStatus = when {
        !f.enabled -> BackgroundReadStatus.OFF
        !f.supported -> BackgroundReadStatus.UNSUPPORTED
        f.hasError -> BackgroundReadStatus.ERROR
        !f.realtime -> BackgroundReadStatus.REALTIME_OFF
        !f.overlayGranted -> BackgroundReadStatus.NEEDS_OVERLAY
        !f.logsGranted -> BackgroundReadStatus.NEEDS_LOGS
        !f.serviceRunning -> BackgroundReadStatus.WAITING_SERVICE
        !f.watcherRunning -> BackgroundReadStatus.STARTING
        else -> BackgroundReadStatus.RUNNING
    }

    /** Steps the user has to finish, in the order the setup card shows them. */
    fun missing(f: BackgroundReadFacts): List<BackgroundReadStep> {
        if (!f.enabled || !f.supported) return emptyList()
        return buildList {
            if (!f.realtime) add(BackgroundReadStep.REALTIME)
            if (!f.overlayGranted) add(BackgroundReadStep.OVERLAY)
            if (!f.logsGranted) add(BackgroundReadStep.LOGS)
        }
    }

    /**
     * The one-time computer command. It is shown with the real package name so it can be copied
     * verbatim; READ_LOGS cannot be requested from inside the app. Granting it survives app updates
     * and reboots, but not uninstall/reinstall.
     */
    fun adbCommands(packageName: String): String = buildString {
        append("adb shell pm grant ").append(packageName)
        append(" android.permission.READ_LOGS\n")
        append("adb shell am force-stop ").append(packageName)
    }

    /** Overlay can be granted in Settings; this is the ADB alternative for locked-down ROMs. */
    fun overlayAdbCommand(packageName: String): String =
        "adb shell appops set $packageName SYSTEM_ALERT_WINDOW allow"
}
