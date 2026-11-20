package sshkey

import (
	"crypto/dsa" //nolint:staticcheck // only to recognise and refuse DSA keys
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"strings"
)

// parseLegacyPEM handles "RSA PRIVATE KEY" and "EC PRIVATE KEY" blocks,
// including OpenSSL's traditional Proc-Type/DEK-Info encryption.
func parseLegacyPEM(block *pem.Block, passphrase []byte, format Format) (*Key, error) {
	info := Info{Format: format}
	der := block.Bytes
	//nolint:staticcheck // legacy PEM encryption is exactly what we are reading
	if x509.IsEncryptedPEMBlock(block) {
		info.Encrypted, info.KDF = true, "md5"
		if dek := block.Headers["DEK-Info"]; dek != "" {
			info.Cipher, _, _ = strings.Cut(dek, ",")
		}
		if len(passphrase) == 0 {
			return nil, &PassphraseError{Info: info}
		}
		//nolint:staticcheck
		plain, err := x509.DecryptPEMBlock(block, passphrase)
		if err != nil {
			if errors.Is(err, x509.IncorrectPasswordError) {
				return nil, &PassphraseError{Wrong: true, Info: info}
			}
			if strings.Contains(err.Error(), "unknown encryption mode") {
				return nil, unsupported(ReasonCipher, info.Cipher)
			}
			return nil, corrupt("legacy PEM: %v", err)
		}
		der = plain
	}
	var raw any
	var err error
	if format == FormatPKCS1 {
		raw, err = x509.ParsePKCS1PrivateKey(der)
	} else {
		raw, err = x509.ParseECPrivateKey(der)
	}
	if err != nil {
		if r := curveOrDSA(err); r != nil {
			return nil, r
		}
		if info.Encrypted {
			// Padding happened to check out but the DER is garbage: the
			// passphrase was wrong.
			return nil, &PassphraseError{Wrong: true, Info: info}
		}
		return nil, corrupt("%s: %v", format, err)
	}
	return finish(info, raw, "")
}

// parseDER handles unarmoured binary keys by trying each structure in turn.
func parseDER(der, passphrase []byte) (*Key, error) {
	if alg, ok := pkcs8Algorithm(der); ok {
		if alg.Equal(oidDSA) {
			return nil, unsupported(ReasonDSA, "")
		}
		return parsePKCS8(der, FormatDERPKCS8)
	}
	if isEncryptedPKCS8(der) {
		return parseEncryptedPKCS8(der, passphrase, FormatDERPKCS8)
	}
	if raw, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return finish(Info{Format: FormatDERPKCS1}, raw, "")
	}
	if raw, err := x509.ParseECPrivateKey(der); err == nil {
		return finish(Info{Format: FormatDERSEC1}, raw, "")
	} else if r := curveOrDSA(err); r != nil {
		return nil, r
	}
	if _, err := x509.ParseCertificate(der); err == nil {
		return nil, unsupported(ReasonCertificate, "")
	}
	if _, err := x509.ParsePKIXPublicKey(der); err == nil {
		return nil, unsupported(ReasonPublicOnly, "")
	}
	return nil, unsupported(ReasonUnknown, "binary data")
}

var oidDSA = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 1}

// pkcs8Algorithm reads the algorithm OID of a PrivateKeyInfo without
// parsing the key itself.
func pkcs8Algorithm(der []byte) (asn1.ObjectIdentifier, bool) {
	var p struct {
		Version    int
		Algo       pkix.AlgorithmIdentifier
		PrivateKey []byte
	}
	rest, err := asn1.Unmarshal(der, &p)
	if err != nil || len(rest) != 0 {
		// PKCS#8 v2 (OneAsymmetricKey) carries optional trailing fields.
		var p2 struct {
			Version    int
			Algo       pkix.AlgorithmIdentifier
			PrivateKey []byte
			Rest       asn1.RawValue `asn1:"optional,tag:0"`
			Pub        asn1.RawValue `asn1:"optional,tag:1"`
		}
		if _, err := asn1.Unmarshal(der, &p2); err != nil {
			return nil, false
		}
		return p2.Algo.Algorithm, true
	}
	return p.Algo.Algorithm, true
}

func parsePKCS8(der []byte, format Format) (*Key, error) {
	if alg, ok := pkcs8Algorithm(der); ok && alg.Equal(oidDSA) {
		return nil, unsupported(ReasonDSA, "")
	}
	raw, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		if r := curveOrDSA(err); r != nil {
			return nil, r
		}
		return nil, corrupt("pkcs8: %v", err)
	}
	return finish(Info{Format: format}, raw, "")
}

// curveOrDSA maps stdlib parse errors that mean "valid but not usable".
func curveOrDSA(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unknown elliptic curve"), strings.Contains(msg, "unsupported elliptic curve"):
		return unsupported(ReasonCurve, "")
	case strings.Contains(msg, "1.2.840.10040.4.1"):
		return unsupported(ReasonDSA, "")
	}
	return nil
}

func isDSA(raw any) bool {
	_, ok := raw.(*dsa.PrivateKey)
	return ok
}
