package update

import (
	"crypto/ed25519"

	"relhub/pkg/relproto"
)

// trustedKeysB64 are the release public keys the shipped apps trust: the
// signing key of the local release-publish service (`relhub keys`), base64 of
// the 32-byte Ed25519 key. Rotating it means shipping an app that trusts both
// the old and the new key, switching the server, and dropping the old key in a
// later release (docs/update.md; release-publish docs/USAGE.md §8).
//
// scripts/release-local.sh refuses to publish when the running server does not
// sign with one of these keys: such a build could never update itself again.
var trustedKeysB64 = []string{
	"k4thZR+OVYSGAT/9j5F39csgGqTL0t3X6GYdNCmuRI8=", // release-publish, key id f5faa9f9858b789f
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
func ParsePublicKey(s string) (ed25519.PublicKey, error) { return relproto.ParsePublicKey(s) }
