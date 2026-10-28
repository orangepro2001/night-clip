package proto

import (
	"encoding/json"
	"regexp"
	"time"
)

const (
	// Version is the envelope version carried in Envelope.Version.
	Version = 1
	// MaxClipboardBytes is the largest clipboard text payload (docs/api-spec.md §2.1).
	MaxClipboardBytes = 64 * 1024
	// MaxFilePayloadBytes and MaxControlPayloadBytes bound the payload of the two other
	// kinds: an offer and a control message are a few hundred bytes of JSON, so anything
	// larger is an attempt to fill the receiver's database.
	MaxFilePayloadBytes    = 8 * 1024
	MaxControlPayloadBytes = 2 * 1024
	// MaxEventLifetime is the longest expires_at - created_at a receiver accepts.
	MaxEventLifetime = 7 * 24 * time.Hour
)

// Kind is the event kind.
type Kind string

const (
	KindClipboard Kind = "clipboard"
	KindFile      Kind = "file"
	KindControl   Kind = "control"
)

// Envelope is one event on one link (source -> target), docs/api-spec.md §2.
type Envelope struct {
	Version        int             `json:"version"`
	EventID        string          `json:"event_id"`
	Kind           Kind            `json:"kind"`
	SourceDeviceID string          `json:"source_device_id"`
	TargetDeviceID string          `json:"target_device_id"`
	Sequence       uint64          `json:"sequence"`
	CreatedAt      time.Time       `json:"created_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
	ContentHash    string          `json:"content_hash"`
	Sensitive      bool            `json:"sensitive"`
	Payload        json.RawMessage `json:"payload"`
}

// ClipboardPayload is the payload of KindClipboard events.
type ClipboardPayload struct {
	MimeType string `json:"mime_type"`
	Content  string `json:"content"`
	Size     int    `json:"size"`
}

// MimeText is the only clipboard MIME type supported in v1.
const MimeText = "text/plain; charset=utf-8"

var (
	eventIDRe  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	deviceIDRe = regexp.MustCompile(`^dev_[a-z2-7]{26}$`)
	hashRe     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ValidDeviceID reports whether s has the form produced by identity.DeviceID.
func ValidDeviceID(s string) bool { return deviceIDRe.MatchString(s) }

// Validate checks structure, expiry and content_hash. It does not check the
// peer relationship (TLS fingerprint vs SourceDeviceID, target == self);
// that belongs to the peer layer. now is injected for testability.
func (e *Envelope) Validate(now time.Time) error {
	if e.Version != Version {
		return Errorf(CodeInvalid, "unsupported version %d", e.Version)
	}
	if !eventIDRe.MatchString(e.EventID) {
		return Errorf(CodeInvalid, "bad event_id")
	}
	switch e.Kind {
	case KindClipboard, KindFile, KindControl:
	default:
		return Errorf(CodeInvalid, "bad kind %q", e.Kind)
	}
	if !deviceIDRe.MatchString(e.SourceDeviceID) || !deviceIDRe.MatchString(e.TargetDeviceID) {
		return Errorf(CodeInvalid, "bad device id")
	}
	if e.SourceDeviceID == e.TargetDeviceID {
		return Errorf(CodeInvalid, "source equals target")
	}
	if e.Sequence < 1 {
		return Errorf(CodeInvalid, "sequence must be >= 1")
	}
	if e.CreatedAt.IsZero() || !e.ExpiresAt.After(e.CreatedAt) {
		return Errorf(CodeInvalid, "expires_at must be after created_at")
	}
	if e.ExpiresAt.Sub(e.CreatedAt) > MaxEventLifetime {
		return Errorf(CodeInvalid, "event lifetime exceeds %s", MaxEventLifetime)
	}
	if !now.Before(e.ExpiresAt) {
		return Errorf(CodeExpired, "event expired at %s", e.ExpiresAt.Format(time.RFC3339))
	}
	if !hashRe.MatchString(e.ContentHash) {
		return Errorf(CodeInvalid, "bad content_hash format")
	}
	if len(e.Payload) == 0 {
		return Errorf(CodeInvalid, "missing payload")
	}
	got, err := ContentHash(e.Payload)
	if err != nil {
		return Errorf(CodeInvalid, "payload: %v", err)
	}
	if got != e.ContentHash {
		return Errorf(CodeHashMismatch, "content_hash does not match payload")
	}
	switch {
	case e.Kind == KindFile && len(e.Payload) > MaxFilePayloadBytes,
		e.Kind == KindControl && len(e.Payload) > MaxControlPayloadBytes:
		return Errorf(CodeTooLarge, "%s payload exceeds its size limit", e.Kind)
	}
	if e.Kind == KindClipboard {
		var p ClipboardPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return Errorf(CodeInvalid, "clipboard payload: %v", err)
		}
		if p.MimeType != MimeText {
			return Errorf(CodeInvalid, "unsupported mime_type %q", p.MimeType)
		}
		if p.Size != len(p.Content) {
			return Errorf(CodeInvalid, "size does not match content length")
		}
		if p.Size > MaxClipboardBytes {
			return Errorf(CodeTooLarge, "clipboard text exceeds %d bytes", MaxClipboardBytes)
		}
	}
	return nil
}

// Device platforms, as reported at pairing and stored for each peer.
// Desktops (Windows, macOS) accept connections on their tailnet address; Android only dials.
const (
	PlatformWindows = "windows"
	PlatformAndroid = "android"
	PlatformMacOS   = "macos"
)

// KnownPlatform reports whether p is a platform this core understands.
func KnownPlatform(p string) bool {
	return p == PlatformWindows || p == PlatformAndroid || p == PlatformMacOS
}

// DesktopPlatform reports whether p is a desktop platform (it listens for peers).
func DesktopPlatform(p string) bool { return p == PlatformWindows || p == PlatformMacOS }
