// Package proto defines what every part of the core shares with the shells: the
// stable error codes and the platform names. See docs/api-spec.md §1.
package proto

import "fmt"

// Code is a stable, machine-readable error code (docs/api-spec.md §1.4).
type Code string

const (
	CodeExpired       Code = "EXPIRED"
	CodeHashMismatch  Code = "HASH_MISMATCH"
	CodeTooLarge      Code = "TOO_LARGE"
	CodeNoSpace       Code = "NO_SPACE"
	CodeRateLimited   Code = "RATE_LIMITED"
	CodeDBUnavailable Code = "DB_UNAVAILABLE"
	CodeKeyInvalid    Code = "KEY_INVALID"

	CodeUpdateDisabled Code = "UPDATE_DISABLED" // no update URL or app version configured
	CodeUpdateFailed   Code = "UPDATE_FAILED"   // network / HTTP failure; retryable
	CodeUpdateRejected Code = "UPDATE_REJECTED" // manifest malformed or signature not trusted

	CodeVaultLocked      Code = "VAULT_LOCKED"
	CodeVaultCredentials Code = "VAULT_WRONG_CREDENTIALS" // wrong master password / key file, or a damaged file
	CodeVaultNotFound    Code = "VAULT_NOT_FOUND"
	CodeVaultExists      Code = "VAULT_EXISTS"
	CodeVaultUnsupported Code = "VAULT_UNSUPPORTED"
	CodeVaultBroken      Code = "VAULT_BROKEN"
	CodeVaultNotEmpty    Code = "VAULT_NOT_EMPTY"
	CodeVaultReview      Code = "VAULT_REVIEW" // a sync or merge needs the user's approval

	CodeSSHKeyUnsupported   Code = "SSH_KEY_UNSUPPORTED"   // recognised but unusable: details.reason (dsa, sk, public_only, …)
	CodeSSHKeyPassphrase    Code = "SSH_KEY_PASSPHRASE"    // encrypted key: details.wrong says whether a passphrase was tried
	CodeSSHKeyCorrupt       Code = "SSH_KEY_CORRUPT"       // the file claims a known format but is damaged
	CodeSSHKeyTooExpensive  Code = "SSH_KEY_TOO_EXPENSIVE" // key derivation parameters above the core's limits
	CodeSSHKeyDuplicate     Code = "SSH_KEY_DUPLICATE"     // the key is already in the vault: details.entry_id
	CodeSSHAgentUnavailable Code = "SSH_AGENT_UNAVAILABLE" // the agent is off, unsupported here, or its pipe is taken

	CodeNotFound       Code = "NOT_FOUND"
	CodeConflict       Code = "CONFLICT"
	CodeNotImplemented Code = "NOT_IMPLEMENTED"
	CodeInvalid        Code = "INVALID"
	CodeMethodNotFound Code = "METHOD_NOT_FOUND"
	CodeNotInitialized Code = "NOT_INITIALIZED"
	CodeInternal       Code = "INTERNAL"
)

// Error carries a Code plus a human-readable message. Clients must depend on
// Code only; Message may change.
type Error struct {
	Code      Code           `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Errorf builds an *Error.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Device platforms a shell reports at Init.
const (
	PlatformWindows = "windows"
	PlatformAndroid = "android"
	PlatformMacOS   = "macos"
)

// KnownPlatform reports whether p is a platform this core understands.
func KnownPlatform(p string) bool {
	return p == PlatformWindows || p == PlatformAndroid || p == PlatformMacOS
}

// DesktopPlatform reports whether p is a desktop platform.
func DesktopPlatform(p string) bool { return p == PlatformWindows || p == PlatformMacOS }
