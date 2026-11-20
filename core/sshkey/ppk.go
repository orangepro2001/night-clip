package sshkey

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"math/big"
	"strconv"
	"strings"

	"github.com/tobischo/argon2"
	"golang.org/x/crypto/ssh"
)

// ppkFile is a parsed PuTTY key file (versions 2 and 3).
type ppkFile struct {
	version    int
	algo       string
	encryption string
	comment    string
	public     []byte
	private    []byte
	mac        []byte
	kdf        string // Argon2id / Argon2i / Argon2d (v3 only)
	memoryKB   uint32
	passes     uint32
	lanes      uint32
	salt       []byte
}

func readPPK(text []byte) (*ppkFile, error) {
	lines := strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n")
	f := &ppkFile{}
	i := 0
	next := func() (string, string, bool) {
		for i < len(lines) {
			l := strings.TrimSpace(lines[i])
			i++
			if l == "" {
				continue
			}
			k, v, ok := strings.Cut(l, ":")
			return strings.TrimSpace(k), strings.TrimSpace(v), ok
		}
		return "", "", false
	}
	block := func(n string) ([]byte, error) {
		count, err := strconv.Atoi(n)
		if err != nil || count < 0 || count > 1024 || i+count > len(lines) {
			return nil, corrupt("ppk line count %q", n)
		}
		var sb strings.Builder
		for _, l := range lines[i : i+count] {
			sb.WriteString(strings.TrimSpace(l))
		}
		i += count
		b, err := base64.StdEncoding.DecodeString(sb.String())
		if err != nil {
			return nil, corrupt("ppk base64: %v", err)
		}
		return b, nil
	}

	k, v, ok := next()
	if !ok || !strings.HasPrefix(k, "PuTTY-User-Key-File-") {
		return nil, corrupt("ppk header")
	}
	switch strings.TrimPrefix(k, "PuTTY-User-Key-File-") {
	case "1":
		return nil, unsupported(ReasonPuTTY1, "")
	case "2":
		f.version = 2
	case "3":
		f.version = 3
	default:
		return nil, unsupported(ReasonUnknown, k)
	}
	f.algo = v
	for {
		k, v, ok := next()
		if !ok {
			break
		}
		var err error
		switch k {
		case "Encryption":
			f.encryption = v
		case "Comment":
			f.comment = v
		case "Public-Lines":
			f.public, err = block(v)
		case "Private-Lines":
			f.private, err = block(v)
		case "Private-MAC":
			f.mac, err = hex.DecodeString(v)
		case "Private-Hash":
			return nil, unsupported(ReasonPuTTY1, "Private-Hash")
		case "Key-Derivation":
			f.kdf = v
		case "Argon2-Memory":
			f.memoryKB, err = parseU32(v)
		case "Argon2-Passes":
			f.passes, err = parseU32(v)
		case "Argon2-Parallelism":
			f.lanes, err = parseU32(v)
		case "Argon2-Salt":
			f.salt, err = hex.DecodeString(v)
		}
		if err != nil {
			return nil, corrupt("ppk field %s: %v", k, err)
		}
	}
	if f.public == nil || f.private == nil || f.mac == nil {
		return nil, corrupt("ppk is missing a section")
	}
	return f, nil
}

func parseU32(s string) (uint32, error) {
	n, err := strconv.ParseUint(s, 10, 32)
	return uint32(n), err
}

func parsePPK(text, passphrase []byte) (*Key, error) {
	f, err := readPPK(text)
	if err != nil {
		return nil, err
	}
	if err := checkKeyType(f.algo); err != nil {
		return nil, err
	}
	info := Info{Format: FormatPPK2, Comment: f.comment}
	if f.version == 3 {
		info.Format = FormatPPK3
	}
	if pub, err := ssh.ParsePublicKey(f.public); err == nil {
		info.Public = pub
	}

	var macKey []byte
	var newMAC func() hash.Hash
	plain := f.private
	switch f.encryption {
	case "none":
		if f.version == 2 {
			macKey = ppk2MACKey(nil)
		}
	case "aes256-cbc":
		info.Encrypted, info.Cipher = true, "aes256-cbc"
		var key, iv []byte
		if f.version == 2 {
			info.KDF = "sha1"
		} else {
			info.KDF = strings.ToLower(f.kdf)
			if f.memoryKB > maxArgon2MemoryKB || f.passes > maxArgon2Passes || f.lanes > maxArgon2Lanes {
				return nil, ErrTooExpensive
			}
			if f.passes < 1 || f.lanes < 1 || f.memoryKB < 8*f.lanes || len(f.salt) == 0 {
				return nil, corrupt("argon2 parameters")
			}
			switch info.KDF {
			case "argon2id", "argon2i", "argon2d":
			default:
				return nil, unsupported(ReasonCipher, "kdf "+f.kdf)
			}
		}
		if len(passphrase) == 0 {
			return nil, &PassphraseError{Info: info}
		}
		if f.version == 2 {
			key, iv, macKey = ppk2Keys(passphrase)
		} else {
			key, iv, macKey = ppk3Keys(info.KDF, passphrase, f.salt, f.passes, f.memoryKB, f.lanes)
		}
		if len(f.private) == 0 || len(f.private)%aes.BlockSize != 0 {
			return nil, corrupt("ppk private blob length")
		}
		b, err := aes.NewCipher(key)
		if err != nil {
			return nil, corrupt("aes: %v", err)
		}
		plain = make([]byte, len(f.private))
		cipher.NewCBCDecrypter(b, iv).CryptBlocks(plain, f.private)
	default:
		return nil, unsupported(ReasonCipher, f.encryption)
	}
	if f.version == 2 {
		newMAC = sha1.New
	} else {
		newMAC = sha256.New
	}

	m := hmac.New(newMAC, macKey)
	m.Write(ppkMACData(f.algo, f.encryption, f.comment, f.public, plain))
	if !constantTimeEqual(m.Sum(nil), f.mac) {
		if info.Encrypted {
			return nil, &PassphraseError{Wrong: true, Info: info}
		}
		return nil, corrupt("ppk MAC does not match")
	}

	raw, err := ppkPrivateKey(f.algo, f.public, plain)
	if err != nil {
		return nil, err
	}
	return finish(info, raw, f.comment)
}

func ppkMACData(algo, enc, comment string, public, private []byte) []byte {
	var b bytes.Buffer
	for _, s := range [][]byte{[]byte(algo), []byte(enc), []byte(comment), public, private} {
		b.Write(ssh.Marshal(struct{ S []byte }{s}))
	}
	return b.Bytes()
}

func ppk2MACKey(passphrase []byte) []byte {
	h := sha1.New()
	h.Write([]byte("putty-private-key-file-mac-key"))
	h.Write(passphrase)
	return h.Sum(nil)
}

// ppk2Keys derives the v2 AES key (two SHA-1 blocks), the all-zero IV and
// the MAC key.
func ppk2Keys(passphrase []byte) (key, iv, macKey []byte) {
	for seq := range 2 {
		h := sha1.New()
		h.Write([]byte{0, 0, 0, byte(seq)})
		h.Write(passphrase)
		key = h.Sum(key)
	}
	return key[:32], make([]byte, aes.BlockSize), ppk2MACKey(passphrase)
}

// ppk3Keys derives 80 bytes with Argon2: AES key, IV, MAC key.
func ppk3Keys(kdf string, passphrase, salt []byte, passes, memoryKB, lanes uint32) (key, iv, macKey []byte) {
	var out []byte
	switch kdf {
	case "argon2id":
		out = argon2.IDKey(passphrase, salt, passes, memoryKB, uint8(lanes), 80)
	case "argon2i":
		out = argon2.Key(passphrase, salt, passes, memoryKB, uint8(lanes), 80)
	default:
		out = argon2.DKey(passphrase, salt, passes, memoryKB, uint8(lanes), 80)
	}
	return out[:32], out[32:48], out[48:80]
}

// ppkPrivateKey assembles a private key from PuTTY's public and private
// blobs. Trailing bytes in the private blob are the random padding.
func ppkPrivateKey(algo string, public, private []byte) (any, error) {
	pr := wire(public)
	if pr.str() != algo {
		return nil, corrupt("ppk public blob type")
	}
	sr := wire(private)
	switch {
	case algo == ssh.KeyAlgoRSA:
		e, n := mpint(pr), mpint(pr)
		d, p, q := mpint(sr), mpint(sr), mpint(sr)
		mpint(sr) // iqmp, recomputed by Precompute
		if pr.err || sr.err || !e.IsInt64() {
			return nil, corrupt("ppk rsa fields")
		}
		return &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: n, E: int(e.Int64())}, D: d, Primes: []*big.Int{p, q}}, nil
	case algo == ssh.KeyAlgoED25519:
		pub := pr.bytes()
		le := sr.bytes() // the 32-byte seed as a little-endian integer, high zero bytes dropped
		if pr.err || sr.err || len(le) > ed25519.SeedSize || len(pub) != ed25519.PublicKeySize {
			return nil, corrupt("ppk ed25519 fields")
		}
		seed := make([]byte, ed25519.SeedSize)
		copy(seed, le)
		return ed25519.NewKeyFromSeed(seed), nil
	case strings.HasPrefix(algo, "ecdsa-sha2-"):
		var curve elliptic.Curve
		switch pr.str() {
		case "nistp256":
			curve = elliptic.P256()
		case "nistp384":
			curve = elliptic.P384()
		case "nistp521":
			curve = elliptic.P521()
		default:
			return nil, unsupported(ReasonCurve, algo)
		}
		pr.bytes() // the public point; finish() compares it with the derived one
		d := mpint(sr)
		if pr.err || sr.err {
			return nil, corrupt("ppk ecdsa fields")
		}
		size := (curve.Params().BitSize + 7) / 8
		if len(d.Bytes()) > size {
			return nil, corrupt("ppk ecdsa scalar")
		}
		k, err := ecdsa.ParseRawPrivateKey(curve, d.FillBytes(make([]byte, size)))
		if err != nil {
			return nil, corrupt("ppk ecdsa scalar: %v", err)
		}
		return k, nil
	}
	return nil, unsupported(ReasonUnknown, "key type "+algo)
}

func mpint(r *wireReader) *big.Int {
	return new(big.Int).SetBytes(r.bytes())
}
