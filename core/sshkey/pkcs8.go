package sshkey

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"hash"

	"golang.org/x/crypto/scrypt"
)

var (
	oidPBES2  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
	oidPBKDF2 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}
	oidScrypt = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11591, 4, 11}

	oidPKCS5  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5}
	oidPKCS12 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1}

	oidHMACSHA1   = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 7}
	oidHMACSHA224 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 8}
	oidHMACSHA256 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 9}
	oidHMACSHA384 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 10}
	oidHMACSHA512 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 11}

	oidAES128CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 2}
	oidAES192CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 22}
	oidAES256CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
	oidDESEDE3CBC = asn1.ObjectIdentifier{1, 2, 840, 113549, 3, 7}
)

type encryptedPrivateKeyInfo struct {
	Algo          pkix.AlgorithmIdentifier
	EncryptedData []byte
}

type pbes2Params struct {
	KDF    pkix.AlgorithmIdentifier
	Scheme pkix.AlgorithmIdentifier
}

type pbkdf2Params struct {
	Salt       []byte
	Iterations int
	KeyLength  int                      `asn1:"optional"`
	PRF        pkix.AlgorithmIdentifier `asn1:"optional"`
}

type scryptParams struct {
	Salt      []byte
	N         int
	R         int
	P         int
	KeyLength int `asn1:"optional"`
}

type pkcs8Prf struct {
	name string
	h    func() hash.Hash
}

var pkcs8Prfs = []struct {
	oid asn1.ObjectIdentifier
	prf pkcs8Prf
}{
	{oidHMACSHA1, pkcs8Prf{"pbkdf2-sha1", sha1.New}},
	{oidHMACSHA224, pkcs8Prf{"pbkdf2-sha224", sha256.New224}},
	{oidHMACSHA256, pkcs8Prf{"pbkdf2-sha256", sha256.New}},
	{oidHMACSHA384, pkcs8Prf{"pbkdf2-sha384", sha512.New384}},
	{oidHMACSHA512, pkcs8Prf{"pbkdf2-sha512", sha512.New}},
}

var pkcs8Schemes = []struct {
	oid    asn1.ObjectIdentifier
	name   string
	keyLen int
}{
	{oidAES128CBC, "aes-128-cbc", 16},
	{oidAES192CBC, "aes-192-cbc", 24},
	{oidAES256CBC, "aes-256-cbc", 32},
	{oidDESEDE3CBC, "des-ede3-cbc", 24},
}

func hasPrefix(oid, prefix asn1.ObjectIdentifier) bool {
	return len(oid) > len(prefix) && oid[:len(prefix)].Equal(prefix)
}

// isEncryptedPKCS8 reports whether der is an EncryptedPrivateKeyInfo with a
// password-based scheme we can at least name.
func isEncryptedPKCS8(der []byte) bool {
	var e encryptedPrivateKeyInfo
	rest, err := asn1.Unmarshal(der, &e)
	if err != nil || len(rest) != 0 {
		return false
	}
	a := e.Algo.Algorithm
	return a.Equal(oidPBES2) || hasPrefix(a, oidPKCS5) || hasPrefix(a, oidPKCS12)
}

func parseEncryptedPKCS8(der, passphrase []byte, format Format) (*Key, error) {
	info := Info{Format: format, Encrypted: true}
	var e encryptedPrivateKeyInfo
	if rest, err := asn1.Unmarshal(der, &e); err != nil || len(rest) != 0 {
		return nil, corrupt("encrypted pkcs8 envelope")
	}
	if !e.Algo.Algorithm.Equal(oidPBES2) {
		if hasPrefix(e.Algo.Algorithm, oidPKCS5) || hasPrefix(e.Algo.Algorithm, oidPKCS12) {
			return nil, unsupported(ReasonPBES1, e.Algo.Algorithm.String())
		}
		return nil, unsupported(ReasonCipher, e.Algo.Algorithm.String())
	}
	var p pbes2Params
	if _, err := asn1.Unmarshal(e.Algo.Parameters.FullBytes, &p); err != nil {
		return nil, corrupt("pbes2 parameters")
	}

	keyLen := 0
	scheme := ""
	for _, s := range pkcs8Schemes {
		if p.Scheme.Algorithm.Equal(s.oid) {
			keyLen, scheme = s.keyLen, s.name
		}
	}
	if scheme == "" {
		return nil, unsupported(ReasonCipher, p.Scheme.Algorithm.String())
	}
	info.Cipher = scheme
	var iv []byte
	if _, err := asn1.Unmarshal(p.Scheme.Parameters.FullBytes, &iv); err != nil {
		return nil, corrupt("cipher iv")
	}

	var derive func() ([]byte, error)
	switch {
	case p.KDF.Algorithm.Equal(oidPBKDF2):
		var kp pbkdf2Params
		if _, err := asn1.Unmarshal(p.KDF.Parameters.FullBytes, &kp); err != nil {
			return nil, corrupt("pbkdf2 parameters")
		}
		prf := pkcs8Prf{"pbkdf2-sha1", sha1.New}
		if len(kp.PRF.Algorithm) != 0 {
			found := false
			for _, c := range pkcs8Prfs {
				if kp.PRF.Algorithm.Equal(c.oid) {
					prf, found = c.prf, true
				}
			}
			if !found {
				return nil, unsupported(ReasonCipher, "prf "+kp.PRF.Algorithm.String())
			}
		}
		info.KDF = prf.name
		if kp.Iterations < 1 {
			return nil, corrupt("pbkdf2 iteration count")
		}
		if kp.Iterations > maxPBKDF2Iter {
			return nil, ErrTooExpensive
		}
		if kp.KeyLength != 0 && kp.KeyLength != keyLen {
			return nil, corrupt("pbkdf2 key length")
		}
		derive = func() ([]byte, error) {
			return pbkdf2.Key(prf.h, string(passphrase), kp.Salt, kp.Iterations, keyLen)
		}
	case p.KDF.Algorithm.Equal(oidScrypt):
		var sp scryptParams
		if _, err := asn1.Unmarshal(p.KDF.Parameters.FullBytes, &sp); err != nil {
			return nil, corrupt("scrypt parameters")
		}
		info.KDF = "scrypt"
		if sp.N < 2 || sp.N&(sp.N-1) != 0 || sp.R < 1 || sp.P < 1 {
			return nil, corrupt("scrypt parameters")
		}
		if sp.N > maxScryptN || sp.P > maxArgon2Lanes || 128*sp.N*sp.R > maxScryptMemory {
			return nil, ErrTooExpensive
		}
		derive = func() ([]byte, error) {
			return scrypt.Key(passphrase, sp.Salt, sp.N, sp.R, sp.P, keyLen)
		}
	default:
		return nil, unsupported(ReasonCipher, "kdf "+p.KDF.Algorithm.String())
	}

	if len(passphrase) == 0 {
		return nil, &PassphraseError{Info: info}
	}
	key, err := derive()
	if err != nil {
		return nil, corrupt("key derivation: %v", err)
	}
	var block cipher.Block
	if scheme == "des-ede3-cbc" {
		block, err = des.NewTripleDESCipher(key)
	} else {
		block, err = aes.NewCipher(key)
	}
	if err != nil {
		return nil, corrupt("cipher: %v", err)
	}
	bs := block.BlockSize()
	if len(iv) != bs || len(e.EncryptedData) == 0 || len(e.EncryptedData)%bs != 0 {
		return nil, corrupt("ciphertext length")
	}
	plain := make([]byte, len(e.EncryptedData))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, e.EncryptedData)
	plain, ok := unpad(plain, bs)
	if !ok {
		return nil, &PassphraseError{Wrong: true, Info: info}
	}
	if alg, ok := pkcs8Algorithm(plain); ok && alg.Equal(oidDSA) {
		return nil, unsupported(ReasonDSA, "")
	}
	raw, err := x509.ParsePKCS8PrivateKey(plain)
	if err != nil {
		if r := curveOrDSA(err); r != nil {
			return nil, r
		}
		return nil, &PassphraseError{Wrong: true, Info: info}
	}
	return finish(info, raw, "")
}

// unpad removes PKCS#7 padding.
func unpad(b []byte, bs int) ([]byte, bool) {
	if len(b) == 0 {
		return nil, false
	}
	n := int(b[len(b)-1])
	if n == 0 || n > bs || n > len(b) {
		return nil, false
	}
	if !bytes.Equal(b[len(b)-n:], bytes.Repeat([]byte{byte(n)}, n)) {
		return nil, false
	}
	return b[:len(b)-n], true
}

func pad(b []byte, bs int) []byte {
	n := bs - len(b)%bs
	return append(append([]byte{}, b...), bytes.Repeat([]byte{byte(n)}, n)...)
}
