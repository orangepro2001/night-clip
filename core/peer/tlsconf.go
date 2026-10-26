package peer

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"nightclip/core/identity"
)

// ServerTLS is the listener configuration: TLS 1.3 only, a client certificate
// is mandatory but is NOT chain-verified. Trust is decided afterwards by looking
// the certificate's fingerprint up in the peers table (docs/api-spec.md §1.2).
func ServerTLS(id *identity.Identity) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{id.TLSCertificate()},
		ClientAuth:   tls.RequireAnyClientCert,
	}
}

// ErrFingerprint is returned when the server presents a different certificate
// than the one pinned for it.
var ErrFingerprint = errors.New("server certificate does not match the pinned fingerprint")

// ClientTLS is the dialer configuration. It pins expectedFP: the handshake
// fails unless the server's certificate hashes to it. System roots and host
// names play no role.
func ClientTLS(id *identity.Identity, expectedFP string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{id.TLSCertificate()},
		InsecureSkipVerify: true, // replaced by the pin below; there is no CA to verify against
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || identity.Fingerprint(raw[0]) != expectedFP {
				return ErrFingerprint
			}
			return nil
		},
	}
}

// Capture records the fingerprint a server presented. It backs trust-on-first-use
// pairing without a QR code, where the user then compares the SAS code.
type Capture struct {
	mu sync.Mutex
	fp string
}

// FP returns the captured fingerprint ("" before a handshake).
func (c *Capture) FP() string { c.mu.Lock(); defer c.mu.Unlock(); return c.fp }

// CaptureTLS is ClientTLS without a pin: it accepts any server certificate and
// remembers its fingerprint in c. Use it only for the pairing handshake.
func CaptureTLS(id *identity.Identity, c *Capture) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{id.TLSCertificate()},
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return ErrFingerprint
			}
			c.mu.Lock()
			c.fp = identity.Fingerprint(raw[0])
			c.mu.Unlock()
			return nil
		},
	}
}

// HTTPClient returns an http.Client that speaks mTLS with cfg, never uses a
// proxy and refuses redirects (a peer must not bounce us elsewhere).
func HTTPClient(cfg *tls.Config, dialTimeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     cfg,
			DialContext:         (&net.Dialer{Timeout: dialTimeout}).DialContext,
			TLSHandshakeTimeout: dialTimeout,
			ForceAttemptHTTP2:   false,
			DisableKeepAlives:   false,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
