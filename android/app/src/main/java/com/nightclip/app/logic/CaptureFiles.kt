package com.nightclip.app.logic

import com.nightclip.app.core.PeerInfo
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * "Take a photo and send it" without Android classes, so the decisions are unit-tested:
 * what the temporary photo is called, which old ones to clean up, and who receives it.
 *
 * The photo is written to `<cacheDir>/camera/` (the system camera app writes through our FileProvider),
 * handed to the core as a file descriptor like any other file, and left there: the core reads from the
 * descriptor, so nothing may delete the file while a transfer might still need it. Files older than
 * [KEEP_MILLIS] are removed at the start of the next capture.
 */
object CaptureFiles {
    /** Sub-folder of the cache directory; must match the `camera` entry in res/xml/update_paths.xml. */
    const val DIR = "camera"

    /** How long a captured photo stays in the cache. */
    const val KEEP_MILLIS = 24L * 60 * 60 * 1000

    private val STAMP = DateTimeFormatter.ofPattern("yyyyMMdd_HHmmss")

    /** `IMG_20261028_133012.jpg`: sorts by time and tells the receiver when it was taken. */
    fun photoName(nowMillis: Long, zone: ZoneId = ZoneId.systemDefault()): String =
        "IMG_" + STAMP.format(Instant.ofEpochMilli(nowMillis).atZone(zone)) + ".jpg"

    /** Names (of [files]: name to last-modified millis) that are older than [KEEP_MILLIS]. */
    fun expired(files: List<Pair<String, Long>>, nowMillis: Long): List<String> =
        files.filter { (_, modified) -> nowMillis - modified > KEEP_MILLIS }.map { it.first }

    /**
     * The devices a "send to everyone" capture goes to: the connected ones that may be sent to; when none is
     * connected, every device that may be sent to, so the user gets the real error ("offline") instead of silence.
     */
    fun everyoneTargets(peers: List<PeerInfo>): List<String> {
        val sendable = peers.filter { it.canSend }
        return sendable.filter { it.isConnected }.ifEmpty { sendable }.map { it.deviceId }
    }

    /** The saved-state form of a target list (device ids never contain a comma). */
    fun encodeTargets(ids: List<String>): String = ids.joinToString(",")

    fun decodeTargets(saved: String): List<String> = saved.split(',').filter { it.isNotBlank() }
}
