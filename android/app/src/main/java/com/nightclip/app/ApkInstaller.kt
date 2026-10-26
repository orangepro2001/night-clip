package com.nightclip.app

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.core.content.FileProvider
import java.io.File

/**
 * Hands a package the core has already verified (Ed25519 manifest signature,
 * size, SHA-256) to the system installer. Android itself refuses the install
 * unless the APK carries the same signing key as the installed app.
 */
object ApkInstaller {
    enum class Result { STARTED, NEEDS_PERMISSION, MISSING_FILE }

    /** Only files inside the core's update folder may be installed. */
    fun isInstallable(context: Context, path: String): Boolean {
        val updates = File(context.filesDir, "clipbridge/updates").canonicalFile
        val file = File(path).canonicalFile
        return file.parentFile == updates && file.isFile && file.name.endsWith(".apk")
    }

    fun install(context: Context, path: String): Result {
        if (!isInstallable(context, path)) return Result.MISSING_FILE
        if (!context.packageManager.canRequestPackageInstalls()) {
            // Send the user to the "install unknown apps" switch for this app; they tap Install again afterwards.
            context.startActivity(
                Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${context.packageName}"))
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
            return Result.NEEDS_PERMISSION
        }
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.updates", File(path))
        context.startActivity(
            Intent(Intent.ACTION_VIEW)
                .setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK),
        )
        return Result.STARTED
    }
}
