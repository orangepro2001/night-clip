// Package identity owns the device key pair, the self-signed certificate used
// for mutual TLS, and the derived device_id / certificate fingerprint.
// See docs/architecture.md §3.1.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const certLifetime = 10 * 365 * 24 * time.Hour

var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// Identity is this device's long-term identity.
type Identity struct {
	Name     string
	DeviceID string
	CertDER  []byte
	key      ed25519.PrivateKey
}

// Generate creates a new Ed25519 key pair and a 10-year self-signed
// certificate for it.
func Generate(name string) (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "clipbridge:" + name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return nil, err
	}
	return FromParts(name, der, priv)
}

// FromParts rebuilds an Identity from a stored certificate and private key.
func FromParts(name string, certDER []byte, key ed25519.PrivateKey) (*Identity, error) {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok || !pub.Equal(key.Public()) {
		return nil, fmt.Errorf("identity: certificate does not match private key")
	}
	return &Identity{Name: name, DeviceID: DeviceID(cert.RawSubjectPublicKeyInfo), CertDER: certDER, key: key}, nil
}

// PrivateKeyPKCS8 returns the private key for storage. The caller (store) is
// responsible for encrypting it at rest.
func (i *Identity) PrivateKeyPKCS8() ([]byte, error) { return x509.MarshalPKCS8PrivateKey(i.key) }

// ParsePrivateKeyPKCS8 is the inverse of PrivateKeyPKCS8.
func ParsePrivateKeyPKCS8(b []byte) (ed25519.PrivateKey, error) {
	k, err := x509.ParsePKCS8PrivateKey(b)
	if err != nil {
		return nil, err
	}
	pk, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("identity: not an Ed25519 key")
	}
	return pk, nil
}

// Fingerprint returns base64url(sha256(CertDER)), the value pinned by peers.
func (i *Identity) Fingerprint() string { return Fingerprint(i.CertDER) }

// TLSCertificate returns the certificate/key pair for crypto/tls.
func (i *Identity) TLSCertificate() tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{i.CertDER}, PrivateKey: i.key}
}

// DeviceID derives "dev_" + base32(sha256(SPKI DER))[:26], lower case.
func DeviceID(spkiDER []byte) string {
	sum := sha256.Sum256(spkiDER)
	return "dev_" + strings.ToLower(base32NoPad.EncodeToString(sum[:]))[:26]
}

// Fingerprint returns base64url(sha256(certDER)) without padding.
func Fingerprint(certDER []byte) string {
	sum := sha256.Sum256(certDER)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
