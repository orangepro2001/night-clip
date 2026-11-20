// Package sshkey reads SSH private keys in the formats people actually have
// lying around (OpenSSH, PEM, PKCS#8, raw DER, PuTTY PPK), decrypts them, and
// writes them back out in a small set of formats. It knows nothing about the
// vault; callers hand it bytes and get keys back.
//
// Detection is by content, never by file name. Every decryption path has a
// cost ceiling so a hostile file cannot make the core spend minutes or
// gigabytes deriving a key.
package sshkey

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// MaxInput is the largest file the parsers will look at.
const MaxInput = 1 << 20

// Format names the container a key was read from.
type Format string

const (
	FormatOpenSSH     Format = "openssh"      // -----BEGIN OPENSSH PRIVATE KEY-----
	FormatPKCS1       Format = "pem-pkcs1"    // -----BEGIN RSA PRIVATE KEY-----
	FormatSEC1        Format = "pem-sec1"     // -----BEGIN EC PRIVATE KEY-----
	FormatPKCS8       Format = "pkcs8"        // -----BEGIN PRIVATE KEY-----
	FormatPKCS8Enc    Format = "pkcs8-enc"    // -----BEGIN ENCRYPTED PRIVATE KEY-----
	FormatDERPKCS8    Format = "der-pkcs8"    // binary PKCS#8 (plain or encrypted)
	FormatDERPKCS1    Format = "der-pkcs1"    // binary PKCS#1
	FormatDERSEC1     Format = "der-sec1"     // binary SEC1
	FormatPPK2        Format = "ppk2"         // PuTTY-User-Key-File-2
	FormatPPK3        Format = "ppk3"         // PuTTY-User-Key-File-3
	FormatOpenSSHBlob Format = "openssh-blob" // openssh-key-v1 without PEM armour
)

// Info is what can be learned about a key file without its passphrase.
type Info struct {
	Format    Format
	Encrypted bool
	Cipher    string // e.g. "aes256-ctr", "AES-128-CBC", "aes-256-cbc"; empty when not encrypted
	KDF       string // e.g. "bcrypt", "pbkdf2-sha256", "scrypt", "argon2id", "md5", "sha1"
	// Public is set when the container stores the public key in the clear
	// (OpenSSH, PPK). Comment likewise for PPK.
	Public  ssh.PublicKey
	Comment string
}

// Key is a decrypted private key.
type Key struct {
	Info
	Signer  crypto.Signer // *rsa.PrivateKey, *ecdsa.PrivateKey or ed25519.PrivateKey
	Public  ssh.PublicKey
	Comment string
}

// Algo returns the SSH key type, e.g. "ssh-ed25519".
func (k *Key) Algo() string { return k.Public.Type() }

// Bits returns the key size in bits.
func (k *Key) Bits() int { return KeyBits(k.Signer) }

// Fingerprint returns the SHA256 fingerprint as ssh-keygen -l prints it.
func (k *Key) Fingerprint() string { return ssh.FingerprintSHA256(k.Public) }

// AuthorizedKey returns the one-line public key with the comment appended.
func (k *Key) AuthorizedKey() string { return AuthorizedLine(k.Public, k.Comment) }

// AuthorizedLine renders "type base64 comment" without a trailing newline.
func AuthorizedLine(pub ssh.PublicKey, comment string) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if c := strings.TrimSpace(comment); c != "" {
		line += " " + c
	}
	return line
}

// KeyBits returns the size of a supported private key.
func KeyBits(s crypto.Signer) int {
	switch k := s.(type) {
	case *rsa.PrivateKey:
		return k.N.BitLen()
	case *ecdsa.PrivateKey:
		return k.Curve.Params().BitSize
	case ed25519.PrivateKey:
		return 256
	}
	return 0
}

// --- errors -----------------------------------------------------------------

// Unsupported reasons, stable strings that cross the core API.
const (
	ReasonDSA         = "dsa"         // DSA keys (removed from OpenSSH 10)
	ReasonSK          = "sk"          // FIDO security-key handles (sk-ssh-ed25519@openssh.com …)
	ReasonSSHCom      = "sshcom"      // ---- BEGIN SSH2 ENCRYPTED PRIVATE KEY ----
	ReasonPuTTY1      = "putty1"      // PuTTY-User-Key-File-1
	ReasonPBES1       = "pbes1"       // PKCS#8 with PBES1 / PKCS#12 PBE
	ReasonPublicOnly  = "public_only" // a public key, no private half
	ReasonCertificate = "certificate" // OpenSSH or X.509 certificate
	ReasonRSASmall    = "rsa_small"   // RSA below 2048 bits
	ReasonCurve       = "curve"       // EC curve other than P-256/384/521
	ReasonMulti       = "multi"       // more than one key in one file
	ReasonCipher      = "cipher"      // unknown cipher or KDF
	ReasonUnknown     = "unknown"     // not a key file we recognise
)

// UnsupportedError means the input was understood well enough to know it
// cannot be used.
type UnsupportedError struct {
	Reason string
	Detail string
}

func (e *UnsupportedError) Error() string {
	if e.Detail != "" {
		return "sshkey: unsupported (" + e.Reason + "): " + e.Detail
	}
	return "sshkey: unsupported (" + e.Reason + ")"
}

func unsupported(reason, detail string) error {
	return &UnsupportedError{Reason: reason, Detail: detail}
}

// PassphraseError is returned when a key is encrypted and either no
// passphrase was given (Wrong=false) or the given one does not decrypt it.
type PassphraseError struct {
	Wrong bool
	Info  Info
}

func (e *PassphraseError) Error() string {
	if e.Wrong {
		return "sshkey: wrong passphrase"
	}
	return "sshkey: passphrase required"
}

var (
	// ErrCorrupt means the file claims a known format but is damaged.
	ErrCorrupt = errors.New("sshkey: key file is damaged")
	// ErrTooExpensive means the file asks for a key derivation above the
	// limits below.
	ErrTooExpensive = errors.New("sshkey: key derivation parameters are too expensive")
	// ErrTooLarge means the input is above MaxInput.
	ErrTooLarge = errors.New("sshkey: input is too large")
)

func corrupt(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrCorrupt}, a...)...)
}

// IsUnsupported reports the reason when err is an UnsupportedError.
func IsUnsupported(err error) (string, bool) {
	var u *UnsupportedError
	if errors.As(err, &u) {
		return u.Reason, true
	}
	return "", false
}

// Key-derivation ceilings. Real tools stay far below these.
const (
	maxBcryptRounds   = 512
	maxPBKDF2Iter     = 10_000_000
	maxScryptN        = 1 << 20
	maxScryptMemory   = 256 << 20
	maxArgon2MemoryKB = 256 << 10
	maxArgon2Passes   = 100
	maxArgon2Lanes    = 16
)

// --- key policy -------------------------------------------------------------

// accept turns whatever a parser produced into a crypto.Signer the rest of
// the core can use, and enforces the key-type policy: Ed25519, RSA 2048–16384,
// ECDSA on P-256/384/521.
func accept(raw any) (crypto.Signer, error) {
	switch k := raw.(type) {
	case *ed25519.PrivateKey:
		return accept(*k)
	case ed25519.PrivateKey:
		if len(k) != ed25519.PrivateKeySize {
			return nil, corrupt("ed25519 key has %d bytes", len(k))
		}
		return k, nil
	case *rsa.PrivateKey:
		if k.N.BitLen() < 2048 {
			return nil, unsupported(ReasonRSASmall, fmt.Sprintf("%d bits", k.N.BitLen()))
		}
		if k.N.BitLen() > 16384 {
			return nil, corrupt("rsa modulus too large")
		}
		if err := k.Validate(); err != nil {
			return nil, corrupt("rsa: %v", err)
		}
		k.Precompute()
		return k, nil
	case *ecdsa.PrivateKey:
		switch k.Curve {
		case elliptic.P256(), elliptic.P384(), elliptic.P521():
			return k, nil
		}
		return nil, unsupported(ReasonCurve, k.Curve.Params().Name)
	}
	if isDSA(raw) {
		return nil, unsupported(ReasonDSA, "")
	}
	return nil, unsupported(ReasonUnknown, fmt.Sprintf("key type %T", raw))
}

// finish builds a Key from a parsed private key.
func finish(info Info, raw any, comment string) (*Key, error) {
	signer, err := accept(raw)
	if err != nil {
		return nil, err
	}
	pub, err := ssh.NewPublicKey(signer.Public())
	if err != nil {
		return nil, corrupt("public key: %v", err)
	}
	if info.Public != nil && string(info.Public.Marshal()) != string(pub.Marshal()) {
		return nil, corrupt("stored public key does not match the private key")
	}
	if comment == "" {
		comment = info.Comment
	}
	return &Key{Info: info, Signer: signer, Public: pub, Comment: strings.TrimSpace(comment)}, nil
}
