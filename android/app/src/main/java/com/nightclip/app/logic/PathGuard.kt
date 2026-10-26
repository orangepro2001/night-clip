package com.nightclip.app.logic

import java.io.File
import java.io.IOException

/** Guards against a path from a core event pointing outside the app's data directory. */
object PathGuard {
    /** True if [file] is a regular file located (after resolving `..` and links) strictly inside [base]. */
    fun isInside(base: File, file: File): Boolean = try {
        val b = base.canonicalFile
        val f = file.canonicalFile
        f.isFile && f.path.startsWith(b.path + File.separator)
    } catch (e: IOException) {
        false
    }
}
