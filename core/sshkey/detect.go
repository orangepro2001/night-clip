package sshkey

import (
	"bytes"
	"encoding/pem"
	"unicode/utf16"

	"golang.org/x/crypto/ssh"
)

// Inspect reports what kind of key the input holds without decrypting it.
// For an unencrypted key it also checks that the key parses.
func Inspect(data []byte) (Info, error) {
	k, err := Parse(data, nil)
	if err == nil {
		return k.Info, nil
	}
	if pe, ok := err.(*PassphraseError); ok && !pe.Wrong {
		return pe.Info, nil
	}
	return Info{}, err
}

// Parse decodes a private key. An encrypted key with a nil or empty
// passphrase returns *PassphraseError{Wrong:false} carrying what is known
// about the file; a passphrase that fails returns Wrong:true.
func Parse(data, passphrase []byte) (*Key, error) {
	if len(data) > MaxInput {
		return nil, ErrTooLarge
	}
	text := normalize(data)
	trimmed := bytes.TrimSpace(text)
	switch {
	case len(trimmed) == 0:
		return nil, unsupported(ReasonUnknown, "empty input")
	case bytes.HasPrefix(trimmed, []byte("PuTTY-User-Key-File-")):
		return parsePPK(trimmed, passphrase)
	case bytes.HasPrefix(trimmed, []byte("openssh-key-v1\x00")):
		return parseOpenSSH(trimmed, passphrase, FormatOpenSSHBlob)
	case bytes.Contains(trimmed, []byte("---- BEGIN SSH2 ENCRYPTED PRIVATE KEY ----")):
		return nil, unsupported(ReasonSSHCom, "")
	case bytes.Contains(trimmed, []byte("---- BEGIN SSH2 PUBLIC KEY ----")):
		return nil, unsupported(ReasonPublicOnly, "")
	case bytes.Contains(trimmed, []byte("-----BEGIN ")):
		return parsePEM(trimmed, passphrase)
	case trimmed[0] == 0x30:
		return parseDER(data, passphrase)
	}
	if reason, ok := publicLine(trimmed); ok {
		return nil, unsupported(reason, "")
	}
	if bytes.Contains(trimmed, []byte("<EntrySettings")) {
		return nil, unsupported(ReasonUnknown, "KeeAgent settings, not a key")
	}
	return nil, unsupported(ReasonUnknown, "")
}

// normalize strips byte-order marks and decodes UTF-16 text, which some
// Windows editors produce when a key is pasted and saved.
func normalize(data []byte) []byte {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return data[3:]
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return decodeUTF16(data[2:], false)
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return decodeUTF16(data[2:], true)
	}
	return data
}

func decodeUTF16(b []byte, bigEndian bool) []byte {
	u := make([]uint16, len(b)/2)
	for i := range u {
		if bigEndian {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return []byte(string(utf16.Decode(u)))
}

// publicLine recognises an authorized_keys / .pub line.
func publicLine(b []byte) (string, bool) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return "", false
	}
	if _, ok := pub.(*ssh.Certificate); ok {
		return ReasonCertificate, true
	}
	return ReasonPublicOnly, true
}

// parsePEM walks the PEM blocks, skipping parameter blocks that openssl
// writes ahead of EC and DSA keys, and dispatches on the first key block.
func parsePEM(text, passphrase []byte) (*Key, error) {
	rest := text
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			return nil, unsupported(ReasonUnknown, "no key block in PEM data")
		}
		rest = next
		switch block.Type {
		case "EC PARAMETERS", "DSA PARAMETERS":
			continue
		case "OPENSSH PRIVATE KEY":
			return parseOpenSSH(block.Bytes, passphrase, FormatOpenSSH)
		case "RSA PRIVATE KEY":
			return parseLegacyPEM(block, passphrase, FormatPKCS1)
		case "EC PRIVATE KEY":
			return parseLegacyPEM(block, passphrase, FormatSEC1)
		case "DSA PRIVATE KEY":
			return nil, unsupported(ReasonDSA, "")
		case "PRIVATE KEY":
			return parsePKCS8(block.Bytes, FormatPKCS8)
		case "ENCRYPTED PRIVATE KEY":
			return parseEncryptedPKCS8(block.Bytes, passphrase, FormatPKCS8Enc)
		case "PUBLIC KEY", "RSA PUBLIC KEY", "SSH2 PUBLIC KEY":
			return nil, unsupported(ReasonPublicOnly, "")
		case "CERTIFICATE", "X509 CERTIFICATE", "TRUSTED CERTIFICATE":
			return nil, unsupported(ReasonCertificate, "")
		default:
			return nil, unsupported(ReasonUnknown, block.Type)
		}
	}
}
