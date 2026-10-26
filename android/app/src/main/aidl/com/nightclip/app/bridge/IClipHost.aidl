package com.nightclip.app.bridge;

import com.nightclip.app.bridge.IClipCallback;

/** The helper Shizuku runs as the shell user. 16777114 is the transaction Shizuku uses to stop it. */
interface IClipHost {
    void destroy() = 16777114;

    /** Starts watching the clipboard; the current text is reported once right away. */
    void setCallback(IClipCallback callback) = 1;
}
