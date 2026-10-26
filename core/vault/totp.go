package vault

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TOTPSpec is a parsed one-time-password setting (RFC 6238).
type TOTPSpec struct {
	Secret    []byte
	Digits    int
	Period    int
	Algorithm string // SHA1, SHA256, SHA512
}

// ParseTOTP accepts an otpauth://totp/... URI (what KeePassXC and every authenticator app use) or a
// bare base32 secret (spaces, dashes and lower case allowed).
func ParseTOTP(spec string) (TOTPSpec, error) {
	spec = strings.TrimSpace(spec)
	t := TOTPSpec{Digits: 6, Period: 30, Algorithm: "SHA1"}
	secret := spec
	if strings.HasPrefix(strings.ToLower(spec), "otpauth://") {
		u, err := url.Parse(spec)
		if err != nil || !strings.EqualFold(u.Host, "totp") {
			return t, invalid("not a TOTP otpauth URI")
		}
		q := u.Query()
		secret = q.Get("secret")
		if d := q.Get("digits"); d != "" {
			n, err := strconv.Atoi(d)
			if err != nil || n < 6 || n > 8 {
				return t, invalid("digits must be 6 to 8")
			}
			t.Digits = n
		}
		if p := q.Get("period"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 3600 {
				return t, invalid("period must be 1 to 3600 seconds")
			}
			t.Period = n
		}
		if a := strings.ToUpper(q.Get("algorithm")); a != "" {
			switch a {
			case "SHA1", "SHA256", "SHA512":
				t.Algorithm = a
			default:
				return t, invalid("unsupported TOTP algorithm %q", a)
			}
		}
	}
	secret = strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(secret))
	if secret == "" {
		return t, invalid("the TOTP secret is empty")
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) < 10 {
		return t, invalid("the TOTP secret is not valid base32")
	}
	t.Secret = key
	return t, nil
}

// Code computes the code valid at time at, and how many seconds it stays valid.
func (t TOTPSpec) Code(at time.Time) (string, int) {
	counter := uint64(at.Unix()) / uint64(t.Period)
	var h func() hash.Hash
	switch t.Algorithm {
	case "SHA256":
		h = sha256.New
	case "SHA512":
		h = sha512.New
	default:
		h = sha1.New
	}
	mac := hmac.New(h, t.Secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < t.Digits; i++ {
		mod *= 10
	}
	left := t.Period - int(at.Unix()%int64(t.Period))
	return fmt.Sprintf("%0*d", t.Digits, bin%mod), left
}

// TOTP returns the current one-time code of an entry and the seconds it remains valid.
func (v *Vault) TOTP(id string) (code string, secondsLeft int, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return "", 0, err
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil {
		return "", 0, ErrNotFound
	}
	spec := entryGet(le, fOTP)
	if spec == "" {
		return "", 0, invalid("this entry has no one-time password")
	}
	t, err := ParseTOTP(spec)
	if err != nil {
		return "", 0, err
	}
	code, secondsLeft = t.Code(v.now())
	return code, secondsLeft, nil
}
