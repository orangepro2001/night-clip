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
