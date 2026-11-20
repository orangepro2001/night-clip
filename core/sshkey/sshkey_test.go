package sshkey

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

const fixturePass = "correct horse battery"

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// expected maps fixture name -> "type base64" public key, as ssh-keygen -y
// printed it when the fixtures were generated.
func expected(t testing.TB) map[string]string {
	t.Helper()
	m := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(readFixture(t, "expected.txt")))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 3 {
			m[f[0]] = f[1] + " " + f[2]
		}
	}
	if len(m) < 25 {
		t.Fatalf("expected.txt has %d entries", len(m))
	}
	// DER copies of PEM fixtures.
	m["der_pkcs8_rsa2048"] = m["pkcs8_rsa2048"]
	m["der_pkcs1_rsa2048"] = m["pem_rsa2048"]
	m["der_sec1_ec256"] = m["pem_ec256"]
	return m
}

func pubLine(p ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(p)))
}

func TestFixturesParseWithCorrectPassphrase(t *testing.T) {
	for name, want := range expected(t) {
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, name)
			info, err := Inspect(data)
			if err != nil {
				t.Fatalf("inspect: %v", err)
			}
			k, err := Parse(data, []byte(fixturePass))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := pubLine(k.Public); got != want {
				t.Fatalf("public key\n got %s\nwant %s", got, want)
			}
			if k.Format != info.Format || k.Encrypted != info.Encrypted {
				t.Fatalf("parse info %+v differs from inspect %+v", k.Info, info)
			}
			if k.Bits() == 0 || !strings.HasPrefix(k.Fingerprint(), "SHA256:") {
				t.Fatalf("bits %d fingerprint %q", k.Bits(), k.Fingerprint())
			}
			if strings.HasPrefix(name, "openssh_") && !strings.HasPrefix(k.Comment, "fixture ") {
				t.Fatalf("comment %q", k.Comment)
			}
		})
	}
}

func TestEncryptedFixturesNeedTheRightPassphrase(t *testing.T) {
	for name := range expected(t) {
		data := readFixture(t, name)
		info, err := Inspect(data)
		if err != nil || !info.Encrypted {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if info.Cipher == "" || info.KDF == "" {
				t.Fatalf("info %+v", info)
			}
			var pe *PassphraseError
			if _, err := Parse(data, nil); !errors.As(err, &pe) || pe.Wrong {
				t.Fatalf("no passphrase: %v", err)
			}
			if _, err := Parse(data, []byte("Tr0ub4dor&3 wrong")); !errors.As(err, &pe) || !pe.Wrong {
				t.Fatalf("wrong passphrase: %v", err)
			}
		})
	}
}

func TestFixtureFormats(t *testing.T) {
	cases := map[string]struct {
		format Format
		cipher string
		kdf    string
	}{
		"openssh_ed25519":                   {FormatOpenSSH, "", ""},
		"openssh_ed25519_chacha20-poly1305": {FormatOpenSSH, "chacha20-poly1305@openssh.com", "bcrypt"},
		"openssh_ed25519_aes256-gcm":        {FormatOpenSSH, "aes256-gcm@openssh.com", "bcrypt"},
		"pem_rsa2048":                       {FormatPKCS1, "", ""},
		"pem_rsa2048_des3":                  {FormatPKCS1, "DES-EDE3-CBC", "md5"},
		"pem_ec256_aes256":                  {FormatSEC1, "AES-256-CBC", "md5"},
		"pkcs8_ed25519":                     {FormatPKCS8, "", ""},
		"pkcs8_ed25519_scrypt":              {FormatPKCS8Enc, "aes-256-cbc", "scrypt"},
		"pkcs8_rsa2048_pbkdf2-sha1":         {FormatPKCS8Enc, "aes-128-cbc", "pbkdf2-sha1"},
		"pkcs8_ec521_pbkdf2-sha512":         {FormatPKCS8Enc, "aes-192-cbc", "pbkdf2-sha512"},
		"pkcs8_ed25519_des3":                {FormatPKCS8Enc, "des-ede3-cbc", "pbkdf2-sha256"},
		"der_pkcs8_rsa2048":                 {FormatDERPKCS8, "", ""},
		"der_pkcs1_rsa2048":                 {FormatDERPKCS1, "", ""},
		"der_sec1_ec256":                    {FormatDERSEC1, "", ""},
	}
	for name, want := range cases {
		info, err := Inspect(readFixture(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.Format != want.format || info.Cipher != want.cipher || info.KDF != want.kdf {
			t.Errorf("%s: got %s/%s/%s want %+v", name, info.Format, info.Cipher, info.KDF, want)
		}
	}
}

func TestOpenSSHEncryptedExposesPublicKeyWithoutPassphrase(t *testing.T) {
	info, err := Inspect(readFixture(t, "openssh_ed25519_aes256-ctr"))
	if err != nil || info.Public == nil {
		t.Fatalf("info %+v err %v", info, err)
	}
	if got, want := pubLine(info.Public), expected(t)["openssh_ed25519_aes256-ctr"]; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestRejectedFixtures(t *testing.T) {
	cases := map[string]string{
		"reject_dsa_openssh":    ReasonDSA,
		"reject_dsa_pem":        ReasonDSA,
		"reject_pbes1":          ReasonPBES1,
		"reject_rsa1024":        ReasonRSASmall,
		"reject_secp256k1":      ReasonCurve,
		"reject_public_openssh": ReasonPublicOnly,
		"reject_public_rfc4716": ReasonPublicOnly,
		"reject_public_pem":     ReasonPublicOnly,
		"reject_certificate":    ReasonCertificate,
		"reject_sshcom":         ReasonSSHCom,
	}
	for name, want := range cases {
		_, err := Parse(readFixture(t, name), []byte(fixturePass))
		if got, ok := IsUnsupported(err); !ok || got != want {
			t.Errorf("%s: got %v (%q) want %s", name, err, got, want)
		}
	}
}

func TestSyntheticRejections(t *testing.T) {
	base := readFixture(t, "openssh_ed25519")
	block, _ := pem.Decode(base)
	var w opensshOuter
	if err := ssh.Unmarshal(block.Bytes[len(opensshMagic):], &w); err != nil {
		t.Fatal(err)
	}
	rewrap := func(w opensshOuter) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: append([]byte(opensshMagic), ssh.Marshal(w)...)})
	}

	sk := w
	sk.PubKey = ssh.Marshal(struct{ T, K, App string }{"sk-ssh-ed25519@openssh.com", strings.Repeat("k", 32), "ssh:"})
	if r, _ := IsUnsupported(func() error { _, err := Parse(rewrap(sk), nil); return err }()); r != ReasonSK {
		t.Errorf("sk: %q", r)
	}

	multi := w
	multi.NumKeys = 2
	if r, _ := IsUnsupported(func() error { _, err := Parse(rewrap(multi), nil); return err }()); r != ReasonMulti {
		t.Errorf("multi: %q", r)
	}

	pricey := w
	pricey.CipherName, pricey.KdfName = "aes256-ctr", "bcrypt"
	pricey.KdfOpts = string(ssh.Marshal(struct {
		Salt   []byte
		Rounds uint32
	}{[]byte("0123456789abcdef"), 100000}))
	if _, err := Parse(rewrap(pricey), []byte("x")); !errors.Is(err, ErrTooExpensive) {
		t.Errorf("expensive bcrypt: %v", err)
	}

	odd := w
	odd.CipherName, odd.KdfName = "twofish-cbc", "bcrypt"
	if r, _ := IsUnsupported(func() error { _, err := Parse(rewrap(odd), []byte("x")); return err }()); r != ReasonCipher {
		t.Errorf("unknown cipher: %q", r)
	}

	if r, _ := IsUnsupported(func() error {
		_, err := Parse([]byte("PuTTY-User-Key-File-1: ssh-rsa\n"), nil)
		return err
	}()); r != ReasonPuTTY1 {
		t.Errorf("putty1: %q", r)
	}
	if _, err := Parse(make([]byte, MaxInput+1), nil); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	for _, junk := range []string{"", "hello", `{"aws_access_key_id":"x"}`, "<EntrySettings></EntrySettings>"} {
		if r, _ := IsUnsupported(func() error { _, err := Parse([]byte(junk), nil); return err }()); r != ReasonUnknown {
			t.Errorf("%q: %q", junk, r)
		}
	}
}

func TestBOMAndCRLFAreTolerated(t *testing.T) {
	data := readFixture(t, "openssh_ed25519")
	crlf := bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	bom := append([]byte{0xEF, 0xBB, 0xBF}, crlf...)
	for _, in := range [][]byte{crlf, bom, encodeXMLText(string(crlf), true)} {
		if _, err := Parse(in, nil); err != nil {
			t.Fatalf("%v", err)
		}
	}
}

func TestGenerateExportRoundTrip(t *testing.T) {
	for _, g := range []struct {
		kind string
		bits int
	}{{"ed25519", 0}, {"rsa", 3072}, {"ecdsa", 256}, {"ecdsa", 384}, {"ecdsa", 521}} {
		k, err := Generate(g.kind, g.bits, "made by test")
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{ExportOpenSSH, ExportPKCS8, ExportPEM} {
			for _, pass := range []string{"", "export pass"} {
				if format == ExportPEM && (pass != "" || g.kind == "ed25519") {
					if _, err := Export(k, format, []byte(pass)); err == nil {
						t.Errorf("%s %s pass=%q: expected refusal", g.kind, format, pass)
					}
					continue
				}
				out, err := Export(k, format, []byte(pass))
				if err != nil {
					t.Fatalf("%s %s: %v", g.kind, format, err)
				}
				back, err := Parse(out, []byte(pass))
				if err != nil {
					t.Fatalf("%s %s pass=%q: %v", g.kind, format, pass, err)
				}
				if back.Fingerprint() != k.Fingerprint() || back.Encrypted != (pass != "") {
					t.Fatalf("%s %s: round trip changed the key", g.kind, format)
				}
				if format == ExportOpenSSH && back.Comment != "made by test" {
					t.Fatalf("comment lost: %q", back.Comment)
				}
			}
		}
	}
	if _, err := Generate("rsa", 1024, ""); err == nil {
		t.Fatal("rsa 1024 generated")
	}
}

func TestMarshalOpenSSHNormalizesEveryFormat(t *testing.T) {
	for name, want := range expected(t) {
		k, err := Parse(readFixture(t, name), []byte(fixturePass))
		if err != nil {
			t.Fatal(err)
		}
		out, err := MarshalOpenSSH(k)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if bytes.Contains(out, []byte("\r")) || !bytes.HasPrefix(out, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n")) {
			t.Fatalf("%s: not LF OpenSSH PEM", name)
		}
		back, err := Parse(out, nil)
		if err != nil || pubLine(back.Public) != want || back.Encrypted {
			t.Fatalf("%s: %v", name, err)
		}
		if back.Comment != k.Comment {
			t.Fatalf("%s: comment %q -> %q", name, k.Comment, back.Comment)
		}
	}
}

// --- PPK -------------------------------------------------------------------
//
// No puttygen is available where these tests were written, so the PPK files
// below are produced by a writer that follows PuTTY's published format
// description (PuTTY manual, appendix C). This checks the reader against the
// spec, not against puttygen output.

func writePPK(t *testing.T, version int, k *Key, pass, kdf string) []byte {
	t.Helper()
	algo := k.Public.Type()
	pub := k.Public.Marshal()
	var priv []byte
	switch s := k.Signer.(type) {
	case *rsa.PrivateKey:
		priv = ssh.Marshal(struct{ D, P, Q, Iqmp *big.Int }{s.D, s.Primes[0], s.Primes[1], s.Precomputed.Qinv})
	case ed25519.PrivateKey:
		le := bytes.TrimRight(s.Seed(), "\x00")
		priv = ssh.Marshal(struct{ K []byte }{le})
	case *ecdsa.PrivateKey:
		priv = ssh.Marshal(struct{ D *big.Int }{s.D})
	}
	enc := "none"
	var key, iv, macKey, salt []byte
	var newMAC func() hash.Hash = sha256.New
	if version == 2 {
		newMAC = sha1.New
	}
	if pass != "" {
		enc = "aes256-cbc"
		if n := len(priv) % 16; n != 0 {
			pad := make([]byte, 16-n)
			_, _ = rand.Read(pad)
			priv = append(priv, pad...)
		}
		if version == 2 {
			key, iv, macKey = ppk2Keys([]byte(pass))
		} else {
			salt = make([]byte, 16)
			_, _ = rand.Read(salt)
			key, iv, macKey = ppk3Keys(kdf, []byte(pass), salt, 2, 1024, 1)
		}
	} else if version == 2 {
		macKey = ppk2MACKey(nil)
	}
	m := hmac.New(newMAC, macKey)
	m.Write(ppkMACData(algo, enc, k.Comment, pub, priv))
	mac := m.Sum(nil)
	if pass != "" {
		b, _ := aes.NewCipher(key)
		ct := make([]byte, len(priv))
		cipher.NewCBCEncrypter(b, iv).CryptBlocks(ct, priv)
		priv = ct
	}
	lines := func(b []byte) string {
		s := base64.StdEncoding.EncodeToString(b)
		var out []string
		for len(s) > 64 {
			out = append(out, s[:64])
			s = s[64:]
		}
		out = append(out, s)
		return fmt.Sprintf("%d\r\n%s\r\n", len(out), strings.Join(out, "\r\n"))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "PuTTY-User-Key-File-%d: %s\r\nEncryption: %s\r\nComment: %s\r\nPublic-Lines: %s", version, algo, enc, k.Comment, lines(pub))
	if version == 3 && pass != "" {
		names := map[string]string{"argon2id": "Argon2id", "argon2i": "Argon2i", "argon2d": "Argon2d"}
		fmt.Fprintf(&sb, "Key-Derivation: %s\r\nArgon2-Memory: 1024\r\nArgon2-Passes: 2\r\nArgon2-Parallelism: 1\r\nArgon2-Salt: %s\r\n", names[kdf], hex.EncodeToString(salt))
	}
	fmt.Fprintf(&sb, "Private-Lines: %sPrivate-MAC: %s\r\n", lines(priv), hex.EncodeToString(mac))
	return []byte(sb.String())
}

func TestPPKVersionsAndKDFs(t *testing.T) {
	var keys []*Key
	for _, g := range []struct {
		kind string
		bits int
	}{{"ed25519", 0}, {"rsa", 3072}, {"ecdsa", 384}} {
		k, err := Generate(g.kind, g.bits, "ppk "+g.kind)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	type variant struct {
		version   int
		pass, kdf string
		format    Format
	}
	variants := []variant{
		{2, "", "", FormatPPK2}, {2, "pp", "", FormatPPK2},
		{3, "", "", FormatPPK3}, {3, "pp", "argon2id", FormatPPK3},
		{3, "pp", "argon2i", FormatPPK3}, {3, "pp", "argon2d", FormatPPK3},
	}
	for _, k := range keys {
		for _, v := range variants {
			file := writePPK(t, v.version, k, v.pass, v.kdf)
			got, err := Parse(file, []byte(v.pass))
			if err != nil {
				t.Fatalf("%s v%d %s: %v", k.Algo(), v.version, v.kdf, err)
			}
			if got.Fingerprint() != k.Fingerprint() || got.Format != v.format || got.Comment != k.Comment {
				t.Fatalf("%s v%d: mismatch %+v", k.Algo(), v.version, got.Info)
			}
			if v.pass == "" {
				continue
			}
			var pe *PassphraseError
			if _, err := Parse(file, nil); !errors.As(err, &pe) || pe.Wrong || pe.Info.Public == nil || pe.Info.Comment != k.Comment {
				t.Fatalf("no pass: %v", err)
			}
			if _, err := Parse(file, []byte("nope")); !errors.As(err, &pe) || !pe.Wrong {
				t.Fatalf("wrong pass: %v", err)
			}
		}
		// A flipped MAC on an unencrypted file is damage, not a passphrase problem.
		file := writePPK(t, 3, k, "", "")
		i := bytes.Index(file, []byte("Private-MAC: ")) + len("Private-MAC: ")
		file[i] ^= 1
		if _, err := Parse(file, nil); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("tampered MAC: %v", err)
		}
	}
}

func TestPPKArgon2CostCeiling(t *testing.T) {
	k, _ := Generate("ed25519", 0, "c")
	file := writePPK(t, 3, k, "pp", "argon2id")
	big := bytes.Replace(file, []byte("Argon2-Memory: 1024"), []byte("Argon2-Memory: 4194304"), 1)
	if _, err := Parse(big, []byte("pp")); !errors.Is(err, ErrTooExpensive) {
		t.Fatalf("got %v", err)
	}
}

// --- KeeAgent.settings ------------------------------------------------------

func TestAgentSettingsRoundTrip(t *testing.T) {
	b := NewAgentSettings("id_ed25519", true)
	if !bytes.HasPrefix(b, []byte{0xFF, 0xFE}) {
		t.Fatal("not UTF-16LE with BOM")
	}
	s, err := ParseAgentSettings(b)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled() || s.LocationType != "attachment" || s.AttachmentName != "id_ed25519" || !s.RemoveAtDatabaseClose {
		t.Fatalf("%+v", s)
	}
	off, err := SetAgentEnabled(b, false)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := ParseAgentSettings(off); s.Enabled() || !s.AllowUseOfSshKey {
		t.Fatalf("%+v", s)
	}
}

func TestAgentSettingsKeepsUnknownElementsAndEncoding(t *testing.T) {
	doc := `<?xml version="1.0" encoding="utf-8"?>
<EntrySettings>
  <AllowUseOfSshKey>true</AllowUseOfSshKey>
  <AddAtDatabaseOpen>false</AddAtDatabaseOpen>
  <UseDestinationConstraintsWhenAdding>true</UseDestinationConstraintsWhenAdding>
  <DestinationConstraints><Constraint>github.com</Constraint></DestinationConstraints>
  <Location><SelectedType>file</SelectedType><FileName>C:\keys\a &amp; b</FileName></Location>
</EntrySettings>`
	out, err := SetAgentEnabled([]byte(doc), true)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(out, []byte{0xFF, 0xFE}) {
		t.Fatal("UTF-8 input was re-encoded")
	}
	want := strings.Replace(doc, "<AddAtDatabaseOpen>false", "<AddAtDatabaseOpen>true", 1)
	if string(out) != want {
		t.Fatalf("document changed beyond the toggle:\n%s", out)
	}
	s, _ := ParseAgentSettings(out)
	if s.LocationType != "file" || s.FileName != `C:\keys\a & b` || !s.Enabled() {
		t.Fatalf("%+v", s)
	}

	// Missing element is inserted; UTF-16BE is read.
	sparse := `<EntrySettings><AllowUseOfSshKey>true</AllowUseOfSshKey></EntrySettings>`
	out, err = SetAgentEnabled([]byte(sparse), true)
	if err != nil || !strings.Contains(string(out), "<AddAtDatabaseOpen>true</AddAtDatabaseOpen>") {
		t.Fatalf("%v %s", err, out)
	}
	be := []byte{0xFE, 0xFF}
	for _, r := range sparse {
		be = append(be, byte(r>>8), byte(r))
	}
	if s, err := ParseAgentSettings(be); err != nil || !s.AllowUseOfSshKey {
		t.Fatalf("utf-16be: %v %+v", err, s)
	}
	if _, err := ParseAgentSettings([]byte("<nope")); err == nil {
		t.Fatal("garbage accepted")
	}
}

// --- scanning --------------------------------------------------------------

func TestScanFindsKeysAndNeverReturnsContent(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Dir(dir)
	put := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ed := readFixture(t, "openssh_ed25519")
	exp := expected(t)
	put("id_ed25519", ed)
	put("id_ed25519.pub", []byte(exp["openssh_ed25519"]+" me@pc\n"))
	put("cloud.pem", readFixture(t, "pem_rsa2048"))
	put("enc_key", readFixture(t, "openssh_ed25519_aes256-ctr"))
	put("enc_key.pub", []byte(exp["openssh_ecdsa256"]+"\n")) // wrong half on purpose
	put("legacy_pem", readFixture(t, "pem_rsa2048_aes128"))
	put("legacy_pem.pub", []byte(exp["pem_rsa2048_aes128"]+"\n"))
	put("old_dsa", readFixture(t, "reject_dsa_openssh"))
	put("aws-credentials.json", []byte(`{"secret":"AKIAABCDEFGHIJKLMNOP"}`))
	put("recovery-codes.txt", []byte("abcd-1234\nefgh-5678\n"))
	put("known_hosts", []byte("github.com ssh-ed25519 AAAA\n"))
	put("config", []byte("IdentityFile ~/"+filepath.Base(dir)+"/id_ed25519\n\nHost egg\n  IdentityFile \""+filepath.Join(dir, "cloud.pem")+"\"\n  IdentitiesOnly yes\nHost a b\n  IdentityFile %d/"+filepath.Base(dir)+"/cloud.pem\n"))
	put("mount.ps1", []byte("& sshfs.exe u@h:/ Y: -o \"IdentityFile=/cygdrive/c/x/.ssh/cloud.pem,reconnect\"\n# id_ed25519.pub is not a reference\n"))

	res, err := Scan(dir, home)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ScanItem{}
	for _, it := range res.Items {
		byName[filepath.Base(it.Path)] = it
	}
	if len(byName) != 5 {
		t.Fatalf("items: %v", byName)
	}
	if res.Skipped != 2 {
		t.Fatalf("skipped %d", res.Skipped)
	}
	if len(res.ConfigHosts) != 2 || strings.Join(res.ConfigHosts[1].Aliases, ",") != "a,b" {
		t.Fatalf("configured hosts: %+v", res.ConfigHosts)
	}
	if it := byName["id_ed25519"]; it.PubState != PubOK || it.Fingerprint == "" || strings.Join(it.ConfigHosts, ",") != "*" || len(it.ReferencedBy) != 0 {
		t.Fatalf("id_ed25519: %+v", it)
	}
	if it := byName["cloud.pem"]; it.PubState != PubMissing || it.Fingerprint == "" || it.Format != FormatPKCS1 ||
		strings.Join(it.ConfigHosts, ",") != "egg,a,b" || len(it.ReferencedBy) != 1 || it.ReferencedBy[0].Line != 1 {
		t.Fatalf("cloud.pem: %+v", it)
	}
	if it := byName["enc_key"]; !it.Encrypted || it.PubState != PubMismatch || it.Fingerprint == "" {
		t.Fatalf("enc_key: %+v", it)
	}
	if it := byName["legacy_pem"]; !it.Encrypted || it.PubState != PubUnverified || it.Fingerprint == "" {
		t.Fatalf("legacy_pem: %+v", it)
	}
	if it := byName["old_dsa"]; it.Problem != ReasonDSA {
		t.Fatalf("old_dsa: %+v", it)
	}

	j, _ := json.Marshal(res)
	for _, secret := range []string{"PRIVATE KEY", "AKIAABCDEFGHIJKLMNOP", "abcd-1234", string(ed[40:80])} {
		if bytes.Contains(j, []byte(secret)) {
			t.Fatalf("scan result leaks %q", secret)
		}
	}
}

func TestScanConfigWithoutPrivateKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "\uFEFFInclude hosts/*.conf\r\nHost * !excluded\r\n  User default\r\nHost local\r\n  HostName 127.0.0.1\r\n"
	include := "Host \"work\" backup *.example !excluded # comment\n  HostName server.example\n  User=tester\n  Port 2222\n  IdentityFile \"~/.ssh/work key\" # comment\nMatch exec \"never evaluated\"\n  HostName ignored\n"
	for name, content := range map[string]string{"config": config, "hosts/work.conf": include} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Scan(dir, filepath.Dir(dir))
	if err != nil || res.ConfigPartial || len(res.Items) != 0 || len(res.ConfigHosts) != 2 {
		t.Fatalf("scan: %+v, error: %v", res, err)
	}
	got := res.ConfigHosts[0]
	if strings.Join(got.Aliases, ",") != "work,backup" || got.HostName != "server.example" ||
		got.User != "tester" || got.Port != "2222" || strings.Join(got.IdentityFiles, ",") != "~/.ssh/work key" {
		t.Fatalf("included hosts: %+v", got)
	}
	if local := res.ConfigHosts[1]; local.Aliases[0] != "local" || local.HostName != "127.0.0.1" || local.User != "" {
		t.Fatalf("local host: %+v", local)
	}
}

func TestConfigTargetsResolveKnownHostsNames(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	for name, content := range map[string]string{
		"config":       "Include aliases.conf\nHost office maintenance\n  HostName office.example\n  Port 2222\nHost lab !office\n  HostName %h.example\nHost office\n  Port 9999\nHost *\n  Port 22\nHost dynamic\nMatch exec \"exit 1\"\n  HostName dynamic.example\n",
		"aliases.conf": "Host maintenance\n  HostKeyAlias office-keys\nHost ipv6\n  HostName 2001:db8::1\n  Port 2200\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	targets, partial := ConfigTargets(config, dir, filepath.Dir(dir))
	want := map[string]string{
		"maintenance": "office-keys", "ipv6": "[2001:db8::1]:2200",
		"office": "[office.example]:2222", "lab": "lab.example", "dynamic": "",
	}
	if partial || len(targets) != len(want) {
		t.Fatalf("targets: %+v, partial: %v", targets, partial)
	}
	for _, target := range targets {
		if name, ok := want[target.Alias]; !ok || target.KnownHost != name {
			t.Fatalf("unexpected target %+v", target)
		}
	}
}

func TestConfigIdentities(t *testing.T) {
	home := filepath.FromSlash("/home/u")
	cfg := `# comment
IdentityFile=~/.ssh/global
Host one two
    identityfile "~/.ssh/k 1"
Match host x
    IdentityFile %d/.ssh/m
Host *
    IdentityFile none
`
	scan := configScan{identities: map[string][]string{}}
	scan.parse(cfg, "", home, 0, []string{"*"}, -1, nil)
	got := scan.identities
	want := map[string][]string{
		pathKey(filepath.Join(home, ".ssh", "global")): {"*"},
		pathKey(filepath.Join(home, ".ssh", "k 1")):    {"one", "two"},
		pathKey(filepath.Join(home, ".ssh", "m")):      {"Match host x"},
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if strings.Join(got[k], "|") != strings.Join(v, "|") {
			t.Fatalf("%s: %v", k, got[k])
		}
	}
}

// --- fuzzing ---------------------------------------------------------------

func FuzzParse(f *testing.F) {
	entries, _ := os.ReadDir("testdata")
	for _, e := range entries {
		if b, err := os.ReadFile(filepath.Join("testdata", e.Name())); err == nil && len(b) < 8<<10 {
			f.Add(b, []byte(fixturePass))
		}
	}
	f.Fuzz(func(t *testing.T, data, pass []byte) {
		k, err := Parse(data, pass)
		if err == nil && (k.Signer == nil || k.Public == nil) {
			t.Fatal("nil key without error")
		}
	})
}

func FuzzAgentSettings(f *testing.F) {
	f.Add(NewAgentSettings("id", true))
	f.Add([]byte(`<EntrySettings><AddAtDatabaseOpen/></EntrySettings>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if _, err := ParseAgentSettings(b); err != nil {
			return
		}
		out, err := SetAgentEnabled(b, true)
		if err != nil {
			return
		}
		if s, err := ParseAgentSettings(out); err != nil || !s.AddAtDatabaseOpen {
			t.Fatalf("toggle lost: %v %+v", err, s)
		}
	})
}
