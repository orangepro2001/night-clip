package sshkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// Export formats.
const (
	ExportOpenSSH = "openssh"
	ExportPKCS8   = "pkcs8"
	ExportPEM     = "pem"
)

// pkcs8Iterations is the PBKDF2-HMAC-SHA256 work factor for exported
// PKCS#8 files.
const pkcs8Iterations = 600_000

// Generate creates a new key. kind is "ed25519", "rsa" or "ecdsa"; bits 0
// picks the default (RSA 3072, ECDSA 256).
func Generate(kind string, bits int, comment string) (*Key, error) {
	var raw any
	var err error
	switch kind {
	case "ed25519", "":
		_, raw, err = ed25519.GenerateKey(rand.Reader)
	case "rsa":
		if bits == 0 {
			bits = 3072
		}
		if bits != 3072 && bits != 4096 {
			return nil, fmt.Errorf("sshkey: rsa keys are 3072 or 4096 bits")
		}
		raw, err = rsa.GenerateKey(rand.Reader, bits)
	case "ecdsa":
		var c elliptic.Curve
		switch bits {
		case 0, 256:
			c = elliptic.P256()
		case 384:
			c = elliptic.P384()
		case 521:
			c = elliptic.P521()
		default:
			return nil, fmt.Errorf("sshkey: ecdsa keys are 256, 384 or 521 bits")
		}
		raw, err = ecdsa.GenerateKey(c, rand.Reader)
	default:
		return nil, fmt.Errorf("sshkey: unknown key type %q", kind)
	}
	if err != nil {
		return nil, err
	}
	return finish(Info{Format: FormatOpenSSH}, raw, comment)
}

// MarshalOpenSSH returns the key as an unencrypted openssh-key-v1 PEM with
// LF line endings and the comment embedded. This is how keys are stored in
// the vault: the vault is the encryption.
func MarshalOpenSSH(k *Key) ([]byte, error) {
	block, err := ssh.MarshalPrivateKey(k.Signer, k.Comment)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
}

// Export writes the key in one of the export formats. A non-empty
// passphrase encrypts it (openssh: bcrypt + aes256-ctr; pkcs8: PBES2 with
// PBKDF2-HMAC-SHA256 and AES-256-CBC). PEM is only written unencrypted,
// because its legacy encryption uses a single MD5 round.
func Export(k *Key, format string, passphrase []byte) ([]byte, error) {
	switch format {
	case ExportOpenSSH:
		if len(passphrase) == 0 {
			return MarshalOpenSSH(k)
		}
		block, err := ssh.MarshalPrivateKeyWithPassphrase(k.Signer, k.Comment, passphrase)
		if err != nil {
			return nil, err
		}
		return pem.EncodeToMemory(block), nil
	case ExportPKCS8:
		der, err := x509.MarshalPKCS8PrivateKey(k.Signer)
		if err != nil {
			return nil, err
		}
		if len(passphrase) == 0 {
			return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
		}
		enc, err := encryptPKCS8(der, passphrase)
		if err != nil {
			return nil, err
		}
		return pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: enc}), nil
	case ExportPEM:
		if len(passphrase) != 0 {
			return nil, fmt.Errorf("sshkey: PEM export is unencrypted only")
		}
		switch s := k.Signer.(type) {
		case *rsa.PrivateKey:
			return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(s)}), nil
		case *ecdsa.PrivateKey:
			der, err := x509.MarshalECPrivateKey(s)
			if err != nil {
				return nil, err
			}
			return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
		}
		return nil, fmt.Errorf("sshkey: PEM export needs an RSA or ECDSA key")
	}
	return nil, fmt.Errorf("sshkey: unknown export format %q", format)
}

func encryptPKCS8(der, passphrase []byte) ([]byte, error) {
	salt := make([]byte, 16)
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	key, err := pbkdf2.Key(sha256.New, string(passphrase), salt, pkcs8Iterations, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := pad(der, aes.BlockSize)
	ct := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, plain)

	kdfParams, err := asn1.Marshal(pbkdf2Params{
		Salt: salt, Iterations: pkcs8Iterations, KeyLength: 32,
		PRF: pkix.AlgorithmIdentifier{Algorithm: oidHMACSHA256, Parameters: asn1.NullRawValue},
	})
	if err != nil {
		return nil, err
	}
	ivDER, err := asn1.Marshal(iv)
	if err != nil {
		return nil, err
	}
	pbes2, err := asn1.Marshal(pbes2Params{
		KDF:    pkix.AlgorithmIdentifier{Algorithm: oidPBKDF2, Parameters: asn1.RawValue{FullBytes: kdfParams}},
		Scheme: pkix.AlgorithmIdentifier{Algorithm: oidAES256CBC, Parameters: asn1.RawValue{FullBytes: ivDER}},
	})
	if err != nil {
		return nil, err
	}
	return asn1.Marshal(encryptedPrivateKeyInfo{
		Algo:          pkix.AlgorithmIdentifier{Algorithm: oidPBES2, Parameters: asn1.RawValue{FullBytes: pbes2}},
		EncryptedData: ct,
	})
}
