package com.nightclip.app.bridge;

/** Called by the clipboard host (shell process) whenever the clipboard text changes. */
oneway interface IClipCallback {
    void onClip(String text, long copiedAtMs);
}
