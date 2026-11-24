namespace MoonPassword.Windows.Logic;

/// <summary>Human-readable text for core error codes (docs/api-spec.md §1.4). Only the code is ever relied upon.</summary>
public static class ErrorMessages
{
    public static string ForCode(string? code) => code switch
    {
        "EXPIRED" => Strings.ErrExpired,
        "HASH_MISMATCH" => Strings.ErrHashMismatch,
        "TOO_LARGE" => Strings.ErrTooLarge,
        "NO_SPACE" => Strings.ErrNoSpace,
        "RATE_LIMITED" => Strings.ErrRateLimited,
        "INVALID" => Strings.ErrInvalid,
        "DB_UNAVAILABLE" => Strings.ErrDbUnavailable,
        "KEY_INVALID" => Strings.ErrKeyInvalid,
        "NOT_INITIALIZED" => Strings.ErrNotInitialized,
        "METHOD_NOT_FOUND" => Strings.ErrMethodNotFound,
        "BITWARDEN_ENCRYPTED" => Strings.ErrBitwardenEncrypted,
        "UPDATE_DISABLED" => Strings.ErrUpdateDisabled,
        "UPDATE_FAILED" => Strings.ErrUpdateFailed,
        "UPDATE_REJECTED" => Strings.ErrUpdateRejected,
        "INTERNAL" => Strings.ErrInternal,
        "INIT_FAILED" => Strings.ErrInitFailed,
        "NOT_FOUND" => Strings.ErrNotFound,
        "VAULT_LOCKED" or "VAULT_WRONG_CREDENTIALS" or "VAULT_NOT_FOUND" or "VAULT_EXISTS" or "VAULT_UNSUPPORTED" or "VAULT_NOT_EMPTY" or "VAULT_REVIEW" or "VAULT_BROKEN" => ForVault(code),
        "SSH_KEY_UNSUPPORTED" or "SSH_KEY_PASSPHRASE" or "SSH_KEY_CORRUPT" or "SSH_KEY_TOO_EXPENSIVE" or "SSH_KEY_DUPLICATE" or "SSH_AGENT_UNAVAILABLE" => ForSsh(code),
        null or "" => Strings.ErrorGeneric,
        _ => Strings.ErrorUnknown(code),
    };

    /// <summary>
    /// Message for a vault.* failure (docs/vault-api.md §2). <paramref name="retryAfterSeconds"/> is
    /// <c>details.retry_after_s</c> of a RATE_LIMITED error.
    /// </summary>
    public static string ForVault(string? code, int retryAfterSeconds = 0) => code switch
    {
        "OTP_INCOMPLETE" => "这次迁移还有二维码未添加。请扫描同一批次的全部二维码，再导入。",
        "VAULT_LOCKED" => Strings.ErrVaultLocked,
        "VAULT_WRONG_CREDENTIALS" => Strings.ErrVaultWrongCredentials,
        "VAULT_NOT_FOUND" => Strings.ErrVaultNotFound,
        "VAULT_EXISTS" => Strings.ErrVaultExists,
        "VAULT_UNSUPPORTED" => Strings.ErrVaultUnsupported,
        "VAULT_NOT_EMPTY" => Strings.ErrVaultNotEmpty,
        "VAULT_REVIEW" => Strings.ErrVaultReview,
        "VAULT_BROKEN" => Strings.ErrVaultBroken,
        "RATE_LIMITED" => retryAfterSeconds > 0 ? Strings.ErrVaultRateLimited(retryAfterSeconds) : Strings.ErrRateLimited,
        "TOO_LARGE" => Strings.ErrVaultTooLarge,
        "INVALID" => Strings.ErrVaultInvalid,
        _ => ForCode(code),
    };

    /// <summary>
    /// Message for a failed ssh.* call (docs/vault-api.md §3.9). <paramref name="reason"/> is details.reason of
    /// SSH_KEY_UNSUPPORTED; <paramref name="wrong"/> is details.wrong of SSH_KEY_PASSPHRASE.
    /// </summary>
    public static string ForSsh(string? code, string reason = "", bool wrong = false) => code switch
    {
        "SSH_KEY_UNSUPPORTED" => Strings.SshUnsupported(reason),
        "SSH_KEY_PASSPHRASE" => wrong ? Strings.ErrSshWrongPassphrase : Strings.ErrSshNeedsPassphrase,
        "SSH_KEY_CORRUPT" => Strings.ErrSshCorrupt,
        "SSH_KEY_TOO_EXPENSIVE" => Strings.ErrSshTooExpensive,
        "SSH_KEY_DUPLICATE" => Strings.ErrSshDuplicate,
        "SSH_AGENT_UNAVAILABLE" => Strings.ErrSshAgentUnavailable,
        "TOO_LARGE" => Strings.ErrSshTooLarge,
        _ => ForVault(code),
    };
}
