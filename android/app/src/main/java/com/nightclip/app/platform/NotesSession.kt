package com.nightclip.app.platform

import com.nightclip.app.ClipBridgeApp
import com.nightclip.app.core.CoreEvent
import com.nightclip.app.core.NotesApi
import com.nightclip.app.logic.NotesSyncLogic
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * Process-wide part of the notes feature: it asks the core for a notes sync whenever the app comes to the foreground
 * (Android is the dialing side, so a round only happens when this side starts it) and remembers the "deleted on another
 * device" notices until the user has seen them, also when no notes screen exists at the moment the event arrives.
 * Notes never post notifications in this version. Installed once from [ClipBridgeApp.onCreate].
 */
object NotesSession {
    private lateinit var app: ClipBridgeApp

    @Volatile private var lastAutoSyncMs = 0L

    private val _deletedElsewhere = MutableStateFlow<List<String>>(emptyList())

    /** Ids of notes another device deleted although this device edited them later (they were kept); oldest first. */
    val deletedElsewhere: StateFlow<List<String>> = _deletedElsewhere.asStateFlow()

    fun install(application: ClipBridgeApp) {
        app = application
        // Leftovers of an interrupted import, export or attachment, and pictures nobody looked at for a month.
        application.appScope.launch(Dispatchers.IO) { NotesFiles.sweep(application) }
        application.appScope.launch {
            ForegroundTracker.foreground.collect { foreground -> if (foreground) syncIfDue() }
        }
    }

    /** Called by [com.nightclip.app.EventRouter] for every `notes.*` event. */
    fun onEvent(ev: CoreEvent) {
        if (ev is CoreEvent.NotesSyncConflict && ev.reason == "deleted_elsewhere" && ev.noteId.isNotEmpty()) {
            _deletedElsewhere.update { if (ev.noteId in it) it else it + ev.noteId }
        }
    }

    fun dismissDeletedElsewhere(noteId: String) {
        _deletedElsewhere.update { it - noteId }
    }

    /**
     * Starts a sync round unless one was started a moment ago (the foreground and the screen opening both ask).
     * The answer is not used: progress and results arrive as events, and a core without peers simply does nothing.
     */
    suspend fun syncIfDue() {
        val now = System.currentTimeMillis()
        synchronized(this) {
            if (!NotesSyncLogic.shouldAutoSync(lastAutoSyncMs, now)) return
            lastAutoSyncMs = now
        }
        NotesApi.syncNow()
    }

    /** The manual "sync now" button: always starts a round. */
    suspend fun syncNow() = NotesApi.syncNow().also { lastAutoSyncMs = System.currentTimeMillis() }
}
