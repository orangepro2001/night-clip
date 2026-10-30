// Package proto defines the clipbridge/v1 wire types: event envelope,
// canonical JSON, content hashing and stable error codes.
// See docs/api-spec.md §1–§2.
package proto

import "fmt"

// Code is a stable, machine-readable error code (docs/api-spec.md §1.4).
type Code string

const (
	CodeNotPaired      Code = "NOT_PAIRED"
	CodePairingExpired Code = "PAIRING_EXPIRED"
	CodePairingFailed  Code = "PAIRING_FAILED"
	CodeNoTailnet      Code = "NO_TAILNET"
	CodeListenFailed   Code = "LISTEN_FAILED" // a tailnet address exists but its port cannot be bound
	CodePeerOffline    Code = "PEER_OFFLINE"
	CodeTargetMismatch Code = "TARGET_MISMATCH"
	CodeSourceMismatch Code = "SOURCE_MISMATCH"
	CodeExpired        Code = "EXPIRED"
	CodeHashMismatch   Code = "HASH_MISMATCH"
	CodeSequenceGap    Code = "SEQUENCE_GAP"
	CodeTooLarge       Code = "TOO_LARGE"
	CodeNoSpace        Code = "NO_SPACE"
	CodeRateLimited    Code = "RATE_LIMITED"
	CodeDBUnavailable  Code = "DB_UNAVAILABLE"
	CodeKeyInvalid     Code = "KEY_INVALID"
	// Self-update (docs/update.md).
	CodeUpdateDisabled Code = "UPDATE_DISABLED" // no update URL or app version configured
	CodeUpdateFailed   Code = "UPDATE_FAILED"   // network / HTTP failure; retryable
	CodeUpdateRejected Code = "UPDATE_REJECTED" // manifest malformed or signature not trusted
	// Password vault (docs/plan-vault.md).
	CodeVaultLocked      Code = "VAULT_LOCKED"
	CodeVaultCredentials Code = "VAULT_WRONG_CREDENTIALS" // wrong master password / key file, or a damaged file
	CodeVaultNotFound    Code = "VAULT_NOT_FOUND"
	CodeVaultExists      Code = "VAULT_EXISTS"
	CodeVaultUnsupported Code = "VAULT_UNSUPPORTED"
	CodeVaultBroken      Code = "VAULT_BROKEN"
	CodeVaultNotEmpty    Code = "VAULT_NOT_EMPTY"
	CodeVaultReview      Code = "VAULT_REVIEW" // a sync or merge needs the user's approval (see vault.sync_status)
	// Notes (notes/api-spec.md §5).
	CodeNotFound          Code = "NOT_FOUND"
	CodeConflict          Code = "CONFLICT"           // the note cannot be changed as asked without a user decision
	CodeUnsupportedFormat Code = "UNSUPPORTED_FORMAT" // format_version is newer than this core understands
	CodeNotImplemented    Code = "NOT_IMPLEMENTED"
	CodeInvalid           Code = "INVALID"
	CodeMethodNotFound    Code = "METHOD_NOT_FOUND"
	CodeNotInitialized    Code = "NOT_INITIALIZED"
	CodeInternal          Code = "INTERNAL"
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
