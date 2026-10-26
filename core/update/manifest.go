// Package update implements ClipBridge's self-update: a signed release
// manifest, a verifying downloader with resume, a background manager the
// facade drives, and the small distribution server and publisher used on the
// release machine. See docs/update.md.
package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nightclip/core/proto"
)

// Platforms that can be updated.
const (
	PlatformAndroid = "android"
	PlatformWindows = "windows"
)

const (
	signaturePrefix = "clipbridge-release-v1"
	// MaxPackageBytes bounds what a manifest may announce; a signed manifest
	// is trusted, but a bug or a typo must not fill the disk.
	MaxPackageBytes = 512 << 20
)

var (
	fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	sha256Re   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Manifest describes the single current release of one platform. Everything
// except Signature is covered by the signature (Notes via its hash).
type Manifest struct {
	Platform       string `json:"platform"`
	VersionCode    int64  `json:"versionCode"`
	VersionName    string `json:"versionName"`
	File           string `json:"file"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	MinVersionCode int64  `json:"minVersionCode"`
	Notes          string `json:"notes,omitempty"`
	PublishedAt    string `json:"publishedAt"`
	Signature      string `json:"signature"`
}

// ValidPlatform reports whether p is an updatable platform.
func ValidPlatform(p string) bool { return p == PlatformAndroid || p == PlatformWindows }

// ValidFileName reports whether name is safe to use as a bare file name.
func ValidFileName(name string) bool {
	return fileNameRe.MatchString(name) && !strings.Contains(name, "..")
}

// Validate checks the shape of a manifest (not its signature).
func (m *Manifest) Validate() error {
	switch {
	case !ValidPlatform(m.Platform):
		return rejected("unknown platform %q", m.Platform)
	case m.VersionCode < 1:
		return rejected("versionCode must be positive")
	case m.VersionName == "" || len(m.VersionName) > 64 || strings.ContainsAny(m.VersionName, "\r\n"):
		return rejected("bad versionName")
	case !ValidFileName(m.File):
		return rejected("bad file name %q", m.File)
	case m.Size < 1 || m.Size > MaxPackageBytes:
		return rejected("size %d out of range", m.Size)
	case !sha256Re.MatchString(m.SHA256):
		return rejected("sha256 must be 64 lowercase hex characters")
	case m.MinVersionCode < 0 || m.MinVersionCode > m.VersionCode:
		return rejected("minVersionCode must be between 0 and versionCode")
	case len(m.Notes) > 8192:
		return rejected("notes too long")
	case strings.ContainsAny(m.PublishedAt, "\r\n") || len(m.PublishedAt) > 64:
		return rejected("bad publishedAt")
	}
	if m.PublishedAt != "" {
		if _, err := time.Parse(time.RFC3339, m.PublishedAt); err != nil {
			return rejected("publishedAt is not RFC 3339")
		}
	}
	return nil
}

// signingPayload is the exact byte string that is signed: one `key=value`
// line per field, so no field can bleed into another (Validate rejects
// newlines in every free-form field and notes enter as a hash).
func (m *Manifest) signingPayload() []byte {
	notes := sha256.Sum256([]byte(m.Notes))
	var b strings.Builder
	b.WriteString(signaturePrefix + "\n")
	fmt.Fprintf(&b, "platform=%s\n", m.Platform)
	fmt.Fprintf(&b, "versionCode=%d\n", m.VersionCode)
	fmt.Fprintf(&b, "versionName=%s\n", m.VersionName)
	fmt.Fprintf(&b, "file=%s\n", m.File)
	fmt.Fprintf(&b, "size=%s\n", strconv.FormatInt(m.Size, 10))
	fmt.Fprintf(&b, "sha256=%s\n", m.SHA256)
	fmt.Fprintf(&b, "minVersionCode=%d\n", m.MinVersionCode)
	fmt.Fprintf(&b, "publishedAt=%s\n", m.PublishedAt)
	fmt.Fprintf(&b, "notesSha256=%s\n", hex.EncodeToString(notes[:]))
	return []byte(b.String())
}

// Sign validates the manifest and stores the Ed25519 signature.
func (m *Manifest) Sign(priv ed25519.PrivateKey) error {
	if err := m.Validate(); err != nil {
		return err
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m.signingPayload()))
	return nil
}

// Verify checks shape and requires a valid signature from any trusted key.
func (m *Manifest) Verify(keys []ed25519.PublicKey) error {
	if err := m.Validate(); err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return rejected("malformed signature")
	}
	payload := m.signingPayload()
	for _, k := range keys {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, payload, sig) {
			return nil
		}
	}
	return rejected("signature is not from a trusted release key")
}

// Mandatory reports whether a client at currentCode must update.
func (m *Manifest) Mandatory(currentCode int64) bool { return m.MinVersionCode > currentCode }

func rejected(format string, args ...any) error {
	return proto.Errorf(proto.CodeUpdateRejected, "manifest: "+format, args...)
}
