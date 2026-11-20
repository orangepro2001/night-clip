package com.nightclip.app.logic

/** How the unlock screen offers the key-file picker, from what `vault.status` says about this device's last unlock. */
enum class KeyFileHint {
    /** The last credentials on this device used a key file. */
    EXPECTED,

    /** Unknown (the vault was never unlocked on this device): offer it, optional. */
    OPTIONAL,

    /** The last credentials had none: the picker is tucked behind a link. */
    NOT_USED,
    ;

    companion object {
        fun of(keyFile: Boolean?): KeyFileHint = when (keyFile) {
            true -> EXPECTED
            null -> OPTIONAL
            false -> NOT_USED
        }
    }
}
