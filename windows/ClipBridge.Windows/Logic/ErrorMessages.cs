namespace ClipBridge.Windows.Logic;

/// <summary>Human-readable text for core error codes (docs/api-spec.md §1.4). Only the code is ever relied upon.</summary>
public static class ErrorMessages
{
    public static string ForCode(string? code) => code switch
    {
        "NO_TAILNET" => Strings.ErrNoTailnet,
        "NOT_PAIRED" => Strings.ErrNotPaired,
        "PAIRING_EXPIRED" => Strings.ErrPairingExpired,
        "PAIRING_FAILED" => Strings.ErrPairingFailed,
        "PEER_OFFLINE" => Strings.ErrPeerOffline,
        "TARGET_MISMATCH" => Strings.ErrTargetMismatch,
        "SOURCE_MISMATCH" => Strings.ErrSourceMismatch,
        "EXPIRED" => Strings.ErrExpired,
        "HASH_MISMATCH" => Strings.ErrHashMismatch,
        "SEQUENCE_GAP" => Strings.ErrSequenceGap,
        "TOO_LARGE" => Strings.ErrTooLarge,
        "NO_SPACE" => Strings.ErrNoSpace,
        "RATE_LIMITED" => Strings.ErrRateLimited,
        "INVALID" => Strings.ErrInvalid,
        "DB_UNAVAILABLE" => Strings.ErrDbUnavailable,
        "KEY_INVALID" => Strings.ErrKeyInvalid,
        "NOT_INITIALIZED" => Strings.ErrNotInitialized,
        "METHOD_NOT_FOUND" => Strings.ErrMethodNotFound,
        "UPDATE_DISABLED" => Strings.ErrUpdateDisabled,
        "UPDATE_FAILED" => Strings.ErrUpdateFailed,
        "UPDATE_REJECTED" => Strings.ErrUpdateRejected,
        "INTERNAL" => Strings.ErrInternal,
        "INIT_FAILED" => Strings.ErrInitFailed,
        "NOT_FOUND" => Strings.ErrNotFound,
        "VAULT_LOCKED" or "VAULT_WRONG_CREDENTIALS" or "VAULT_NOT_FOUND" or "VAULT_EXISTS" or "VAULT_UNSUPPORTED" or "VAULT_BROKEN" => ForVault(code),
        null or "" => Strings.ErrorGeneric,
        _ => Strings.ErrorUnknown(code),
    };

    /// <summary>
    /// Message for a vault.* failure (docs/vault-api.md §2). <paramref name="retryAfterSeconds"/> is
    /// <c>details.retry_after_s</c> of a RATE_LIMITED error.
    /// </summary>
    public static string ForVault(string? code, int retryAfterSeconds = 0) => code switch
    {
        "VAULT_LOCKED" => Strings.ErrVaultLocked,
        "VAULT_WRONG_CREDENTIALS" => Strings.ErrVaultWrongCredentials,
        "VAULT_NOT_FOUND" => Strings.ErrVaultNotFound,
        "VAULT_EXISTS" => Strings.ErrVaultExists,
        "VAULT_UNSUPPORTED" => Strings.ErrVaultUnsupported,
        "VAULT_BROKEN" => Strings.ErrVaultBroken,
        "RATE_LIMITED" => retryAfterSeconds > 0 ? Strings.ErrVaultRateLimited(retryAfterSeconds) : Strings.ErrRateLimited,
        "TOO_LARGE" => Strings.ErrVaultTooLarge,
        "INVALID" => Strings.ErrVaultInvalid,
        _ => ForCode(code),
    };

    /// <summary>Message for a failed file transfer; checksum failures get the dedicated wording.</summary>
    public static string ForFile(string? code) => code switch
    {
        "HASH_MISMATCH" => Strings.ErrFileHashMismatch,
        "TOO_LARGE" => Strings.ErrFileTooLarge,
        _ => ForCode(code),
    };

    /// <summary>Message for a failed or rejected pairing result.</summary>
    public static string ForPairResult(string state, string? code) => state switch
    {
        "rejected" => Strings.PairRejected,
        "expired" => Strings.ErrPairingExpired,
        "failed" => string.IsNullOrEmpty(code) ? Strings.PairFailed : ForCode(code),
        _ => Strings.PairFailed,
    };

    /// <summary>
    /// The text for a clip.local_changed "suppressed" reason, or null when the user does
    /// not need to hear about it (echo, duplicate, empty, capture off, no peers).
    /// </summary>
    public static string? ForSuppressed(string? reason) => reason switch
    {
        "too_large" => Strings.TooLargeToSend,
        "sensitive_blocked" => Strings.SensitiveBlocked,
        _ => null,
    };
}
