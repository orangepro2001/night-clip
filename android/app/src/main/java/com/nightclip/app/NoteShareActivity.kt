package com.nightclip.app

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import com.nightclip.app.core.ApiResult
import com.nightclip.app.core.NotesApi
import com.nightclip.app.logic.NoteShareLogic
import com.nightclip.app.logic.NoteSharePlan
import com.nightclip.app.logic.NotesErrors
import com.nightclip.app.logic.NotesTexts
import kotlinx.coroutines.launch

/**
 * Share target "ClipBridge 笔记": shared text (a link from the browser, a paragraph from a chat) becomes a new note.
 * It is its own entry in the share sheet, separate from [ShareActivity] (which sends to devices), so the send flow is
 * untouched. No screen: the note is created and a toast says so; the sync loop of the core takes it from there.
 */
class NoteShareActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val app = this.app
        val plan = NoteShareLogic.plan(
            intent.action,
            intent.type,
            intent.getCharSequenceExtra(Intent.EXTRA_TEXT),
            intent.getCharSequenceExtra(Intent.EXTRA_SUBJECT),
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
            }
            NoteSharePlan.TooLarge -> app.toastOnMain(NotesTexts.SHARE_TOO_LARGE)
            NoteSharePlan.Nothing -> app.toastOnMain(NotesTexts.SHARE_NOTHING)
        }
        finish()
    }
}
