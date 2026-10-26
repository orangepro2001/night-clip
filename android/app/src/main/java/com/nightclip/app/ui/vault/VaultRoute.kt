package com.nightclip.app.ui.vault

/** Where the vault tab is. The stack lives in [VaultViewModel] and is emptied whenever the vault locks. */
sealed interface VaultRoute {
    data class Detail(val id: String) : VaultRoute

    /** [id] null = a new entry of [kind]. */
    data class Editor(val id: String?, val kind: String) : VaultRoute
    data object Health : VaultRoute
    data object Backups : VaultRoute
    data object Import : VaultRoute
    data object Export : VaultRoute
    data object ChangeMaster : VaultRoute
    data object Settings : VaultRoute
}

/** Top level state of the vault tab, derived from `vault.status`. */
enum class VaultPhase { LOADING, NO_VAULT, LOCKED, UNLOCKED }
