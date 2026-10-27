package com.nightclip.app

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.util.Log
import androidx.activity.ComponentActivity
import androidx.lifecycle.lifecycleScope
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.NotesApi
import com.nightclip.app.logic.NoteShareLogic
import com.nightclip.app.logic.NoteSharePlan
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesTexts
import com.nightclip.app.platform.NotesTransferRunner
import kotlinx.coroutines.launch

/**
 * Share target "ClipBridge 笔记": shared text (a link from the browser, a paragraph from a chat) becomes a new note, and
 * shared files (a photo, a PDF) become a new note holding them as attachments. It is its own entry in the share sheet,
 * separate from [ShareActivity] (which sends to devices), so the send flow is untouched. No screen: a toast says what
 * happened; the sync loop of the core takes it from there.
 */
class NoteShareActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val app = this.app
        val uris = streamUris(intent)
        val plan = NoteShareLogic.plan(
            intent.action,
            intent.type,
            intent.getCharSequenceExtra(Intent.EXTRA_TEXT),
            intent.getCharSequenceExtra(Intent.EXTRA_SUBJECT),
            hasStream = uris.isNotEmpty(),
        )
        when (plan) {
            is NoteSharePlan.Create -> {
                // The core may still be starting (cold launch from the share sheet); the call waits for it.
                app.appScope.launch {
                    val message = when (val r = NotesApi.create(plan.title, plan.body)) {
                        is ApiResult.Ok -> NotesTexts.SHARE_SAVED
                        is ApiResult.Fail -> NotesErrors.message(r.code, NotesErrors.Context.SHARE)
                    }
                    app.toastOnMain(message)
                }
                finish()
            }
            is NoteSharePlan.Attach -> {
                app.toastOnMain(NotesTexts.SHARE_SAVING)
                // The activity stays until the files are copied: the read grant for the shared Uris ends with it.
                lifecycleScope.launch {
                    try {
                        app.toastOnMain(saveFiles(plan, uris))
                    } finally {
                        finish()
                    }
                }
            }
            NoteSharePlan.TooLarge -> {
                app.toastOnMain(NotesTexts.SHARE_TOO_LARGE)
                finish()
            }
            NoteSharePlan.Nothing -> {
                app.toastOnMain(NotesTexts.SHARE_NOTHING)
                finish()
            }
        }
    }

    /** Stores every file as an attachment and makes one note of them; the text says what came out of it. */
    private suspend fun saveFiles(plan: NoteSharePlan.Attach, uris: List<Uri>): String {
        val markdowns = mutableListOf<String>()
        var firstError: String? = null
        for (uri in uris.take(NoteShareLogic.MAX_FILES)) {
            when (val r = NotesTransferRunner.addAttachment(this, uri)) {
                is NotesTransferRunner.Outcome.Done -> markdowns += r.value.markdown
                is NotesTransferRunner.Outcome.Failed -> if (firstError == null) firstError = r.message
            }
        }
        if (markdowns.isEmpty()) return firstError ?: NotesTexts.SHARE_NOTHING
        val body = NoteShareLogic.attachmentBody(markdowns, plan.text)
        return when (val r = NotesApi.create(plan.title, body)) {
            is ApiResult.Ok -> if (firstError == null) NotesTexts.SHARE_SAVED else NotesTexts.SHARE_SAVED_PARTLY + firstError
            is ApiResult.Fail -> NotesErrors.message(r.code, NotesErrors.Context.SHARE)
        }
    }

    @Suppress("DEPRECATION")
    private fun streamUris(intent: Intent): List<Uri> {
        val found = LinkedHashSet<Uri>()
        try {
            val one = if (Build.VERSION.SDK_INT >= 33) intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
            else intent.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)
            one?.let { found.add(it) }
            intent.clipData?.let { clip ->
                for (i in 0 until clip.itemCount) clip.getItemAt(i).uri?.let { found.add(it) }
            }
        } catch (e: Exception) {
            // A malformed share from another app must not take this activity down.
            Log.e("ClipBridge", "note share: bad EXTRA_STREAM", e)
        }
        return found.toList()
    }
}
