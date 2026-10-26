package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// trustedKeysB64 are the release public keys the shipped apps trust
// (base64 of the 32-byte Ed25519 key). Rotating a key means shipping an app
// that trusts both, then removing the old one in a later release.
var trustedKeysB64 = []string{
	"ULpC0tcOTD65hpuxrx2nwruhrNPtgInsl4DC2Qsjt/s=",
}

// TrustedKeys returns the compiled-in release keys.
func TrustedKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, s := range trustedKeysB64 {
		if k, err := ParsePublicKey(s); err == nil {
			out = append(out, k)
		}
	}
	return out
}

// ParsePublicKey decodes a base64 Ed25519 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("not a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(b), nil
}

// GenerateKeyFile creates a new release key pair. The private seed is written
// (mode 0600, never overwriting) to path; the base64 public key is returned.
func GenerateKeyFile(path string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return "", werr
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// LoadPrivateKey reads a key file written by GenerateKeyFile.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s is not a base64 Ed25519 seed", path)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
