package sshkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/subtle"
	"encoding/binary"
	"encoding/pem"
	"strings"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/poly1305" //nolint:staticcheck // the OpenSSH construction needs raw Poly1305
	"golang.org/x/crypto/ssh"

	"nightclip/core/sshkey/internal/bcryptpbkdf"
)

const opensshMagic = "openssh-key-v1\x00"

// opensshOuter is the openssh-key-v1 container after the magic. AEAD ciphers
// append their tag after the private block, outside the length-prefixed
// string, which is why Rest exists.
type opensshOuter struct {
	CipherName   string
	KdfName      string
	KdfOpts      string
	NumKeys      uint32
	PubKey       []byte
	PrivKeyBlock []byte
	Rest         []byte `ssh:"rest"`
}

type opensshCipher struct {
	keyLen, ivLen, tagLen, block int
	mode                         string // ctr, cbc, gcm, chacha, 3des
}

var opensshCiphers = map[string]opensshCipher{
	"aes128-ctr":                    {16, 16, 0, 16, "ctr"},
	"aes192-ctr":                    {24, 16, 0, 16, "ctr"},
	"aes256-ctr":                    {32, 16, 0, 16, "ctr"},
	"aes128-cbc":                    {16, 16, 0, 16, "cbc"},
	"aes192-cbc":                    {24, 16, 0, 16, "cbc"},
	"aes256-cbc":                    {32, 16, 0, 16, "cbc"},
	"aes128-gcm@openssh.com":        {16, 12, 16, 16, "gcm"},
	"aes256-gcm@openssh.com":        {32, 12, 16, 16, "gcm"},
	"chacha20-poly1305@openssh.com": {64, 0, 16, 8, "chacha"},
	"3des-cbc":                      {24, 8, 0, 8, "3des"},
}

func parseOpenSSH(blob, passphrase []byte, format Format) (*Key, error) {
	if len(blob) < len(opensshMagic) || string(blob[:len(opensshMagic)]) != opensshMagic {
		return nil, corrupt("missing openssh-key-v1 magic")
	}
	var w opensshOuter
	if err := ssh.Unmarshal(blob[len(opensshMagic):], &w); err != nil {
		return nil, corrupt("openssh container: %v", err)
	}
	if w.NumKeys != 1 {
		return nil, unsupported(ReasonMulti, "")
	}
	if err := checkKeyType(publicKeyType(w.PubKey)); err != nil {
		return nil, err
	}
	info := Info{Format: format}
	if pub, err := ssh.ParsePublicKey(w.PubKey); err == nil {
		info.Public = pub
	}

	plain := w.PrivKeyBlock
	if w.CipherName != "none" || w.KdfName != "none" {
		c, ok := opensshCiphers[w.CipherName]
		if !ok {
			return nil, unsupported(ReasonCipher, w.CipherName)
		}
		if w.KdfName != "bcrypt" {
			return nil, unsupported(ReasonCipher, "kdf "+w.KdfName)
		}
		info.Encrypted, info.Cipher, info.KDF = true, w.CipherName, "bcrypt"
		var opts struct {
			Salt   []byte
			Rounds uint32
		}
		if err := ssh.Unmarshal([]byte(w.KdfOpts), &opts); err != nil {
			return nil, corrupt("bcrypt options: %v", err)
		}
		if opts.Rounds > maxBcryptRounds {
			return nil, ErrTooExpensive
		}
		if len(passphrase) == 0 {
			return nil, &PassphraseError{Info: info}
		}
		if len(w.Rest) < c.tagLen {
			return nil, corrupt("missing authentication tag")
		}
		k, err := bcryptpbkdf.Key(passphrase, opts.Salt, int(opts.Rounds), c.keyLen+c.ivLen)
		if err != nil {
			return nil, corrupt("bcrypt: %v", err)
		}
		plain, err = opensshDecrypt(c, k[:c.keyLen], k[c.keyLen:], w.PrivKeyBlock, w.Rest[:c.tagLen])
		if pe, ok := err.(*PassphraseError); ok {
			pe.Info = info
			return nil, pe
		}
		if err != nil {
			return nil, err
		}
	} else if len(w.Rest) != 0 {
		return nil, corrupt("trailing data after private block")
	}

	if len(plain) < 8 || binary.BigEndian.Uint32(plain) != binary.BigEndian.Uint32(plain[4:]) {
		if info.Encrypted {
			return nil, &PassphraseError{Wrong: true, Info: info}
		}
		return nil, corrupt("check bytes differ")
	}
	comment, err := opensshComment(plain)
	if err != nil {
		return nil, err
	}

	// Re-wrap the plaintext as an unencrypted container so x/crypto's
	// well-tested field parsing and validation does the rest.
	unenc := ssh.Marshal(opensshOuter{CipherName: "none", KdfName: "none", NumKeys: 1, PubKey: w.PubKey, PrivKeyBlock: plain})
	raw, err := ssh.ParseRawPrivateKey(pem.EncodeToMemory(&pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: append([]byte(opensshMagic), unenc...),
	}))
	if err != nil {
		return nil, corrupt("openssh key: %v", err)
	}
	return finish(info, raw, comment)
}

func opensshDecrypt(c opensshCipher, key, iv, data, tag []byte) ([]byte, error) {
	if len(data)%c.block != 0 {
		return nil, corrupt("private block is not a whole number of cipher blocks")
	}
	out := make([]byte, len(data))
	switch c.mode {
	case "ctr", "cbc", "gcm":
		b, err := aes.NewCipher(key)
		if err != nil {
			return nil, corrupt("aes: %v", err)
		}
		switch c.mode {
		case "ctr":
			cipher.NewCTR(b, iv).XORKeyStream(out, data)
		case "cbc":
			cipher.NewCBCDecrypter(b, iv).CryptBlocks(out, data)
		case "gcm":
			g, err := cipher.NewGCM(b)
			if err != nil {
				return nil, corrupt("gcm: %v", err)
			}
			sealed := append(append([]byte{}, data...), tag...)
			if out, err = g.Open(out[:0], iv, sealed, nil); err != nil {
				return nil, &PassphraseError{Wrong: true}
			}
		}
	case "3des":
		b, err := des.NewTripleDESCipher(key)
		if err != nil {
			return nil, corrupt("3des: %v", err)
		}
		cipher.NewCBCDecrypter(b, iv).CryptBlocks(out, data)
	case "chacha":
		// OpenSSH's chacha20-poly1305: the first 32 key bytes drive the
		// payload stream, sequence number 0; block 0 yields the Poly1305 key,
		// the payload starts at block 1. The tag covers the ciphertext only
		// (no additional data in a key file).
		var nonce [chacha20.NonceSize]byte
		s, err := chacha20.NewUnauthenticatedCipher(key[:32], nonce[:])
		if err != nil {
			return nil, corrupt("chacha20: %v", err)
		}
		var polyKey [32]byte
		s.XORKeyStream(polyKey[:], polyKey[:])
		var mac [16]byte
		copy(mac[:], tag)
		if !poly1305.Verify(&mac, data, &polyKey) {
			return nil, &PassphraseError{Wrong: true}
		}
		s.SetCounter(1)
		s.XORKeyStream(out, data)
	}
	return out, nil
}

// opensshComment walks the decrypted private block to the comment field.
func opensshComment(plain []byte) (string, error) {
	r := wire(plain[8:])
	kt := r.str()
	var fields int
	switch {
	case kt == ssh.KeyAlgoED25519:
		fields = 2
	case kt == ssh.KeyAlgoRSA:
		fields = 6
	case strings.HasPrefix(kt, "ecdsa-sha2-"):
		fields = 3
	default:
		if err := checkKeyType(kt); err != nil {
			return "", err
		}
		return "", unsupported(ReasonUnknown, "key type "+kt)
	}
	for range fields {
		r.str()
	}
	c := r.str()
	if r.err {
		return "", corrupt("private block is truncated")
	}
	return c, nil
}

// publicKeyType returns the algorithm name at the start of an SSH wire blob.
func publicKeyType(blob []byte) string {
	r := wire(blob)
	t := r.str()
	if r.err {
		return ""
	}
	return t
}

// checkKeyType refuses key types that can be recognised but not used.
func checkKeyType(t string) error {
	switch {
	case t == "ssh-dss":
		return unsupported(ReasonDSA, "")
	case strings.HasPrefix(t, "sk-"):
		return unsupported(ReasonSK, t)
	case strings.HasSuffix(t, "-cert-v01@openssh.com"):
		return unsupported(ReasonCertificate, t)
	}
	return nil
}

// wire is a tiny reader for SSH wire-format strings. It records the first
// error and returns zero values afterwards so callers can check once.
type wireReader struct {
	b   []byte
	err bool
}

func wire(b []byte) *wireReader { return &wireReader{b: b} }

func (r *wireReader) bytes() []byte {
	if r.err || len(r.b) < 4 {
		r.err = true
		return nil
	}
	n := binary.BigEndian.Uint32(r.b)
	if uint64(n) > uint64(len(r.b)-4) {
		r.err = true
		return nil
	}
	v := r.b[4 : 4+n]
	r.b = r.b[4+n:]
	return v
}

func (r *wireReader) str() string { return string(r.bytes()) }

func (r *wireReader) uint32() uint32 {
	if r.err || len(r.b) < 4 {
		r.err = true
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

// constantTimeEqual is used for MAC checks.
func constantTimeEqual(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
