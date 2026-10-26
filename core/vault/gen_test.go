package vault

import (
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestTOTPRFC6238Vectors(t *testing.T) {
	// RFC 6238 appendix B (8 digits)
	cases := []struct {
		alg    string
		secret string
		at     int64
		want   string
	}{
		{"SHA1", "12345678901234567890", 59, "94287082"},
		{"SHA1", "12345678901234567890", 1111111109, "07081804"},
		{"SHA1", "12345678901234567890", 20000000000, "65353130"},
		{"SHA256", "12345678901234567890123456789012", 59, "46119246"},
		{"SHA256", "12345678901234567890123456789012", 1111111109, "68084774"},
		{"SHA512", "1234567890123456789012345678901234567890123456789012345678901234", 59, "90693936"},
		{"SHA512", "1234567890123456789012345678901234567890123456789012345678901234", 1111111109, "25091201"},
	}
	for _, c := range cases {
		spec := TOTPSpec{Secret: []byte(c.secret), Digits: 8, Period: 30, Algorithm: c.alg}
		if got, _ := spec.Code(time.Unix(c.at, 0)); got != c.want {
			t.Errorf("%s at %d = %s, want %s", c.alg, c.at, got, c.want)
		}
	}
}

func TestParseTOTP(t *testing.T) {
	s, err := ParseTOTP("otpauth://totp/Ex:me@x?secret=jbsw y3dp-ehpk3pxp&issuer=Ex&digits=8&period=60&algorithm=sha256")
	if err != nil || s.Digits != 8 || s.Period != 60 || s.Algorithm != "SHA256" || string(s.Secret) != "Hello!\xde\xad\xbe\xef" {
		// "JBSWY3DPEHPK3PXP" decodes to "Hello!\xde\xad\xbe\xef"
		t.Fatalf("ParseTOTP = %+v, %v", s, err)
	}
	if s, err := ParseTOTP("JBSWY3DPEHPK3PXP"); err != nil || s.Digits != 6 || s.Period != 30 {
		t.Fatalf("bare secret: %+v %v", s, err)
	}
	for _, bad := range []string{"", "otpauth://hotp/x?secret=JBSWY3DPEHPK3PXP", "otpauth://totp/x", "not base32!!", "AAAA", "otpauth://totp/x?secret=JBSWY3DPEHPK3PXP&digits=12"} {
		if _, err := ParseTOTP(bad); err == nil {
			t.Errorf("ParseTOTP(%q) accepted", bad)
		}
	}
	v := newTestVault(t)
	id, _ := v.AddEntry(Entry{Title: "t", TOTP: "JBSWY3DPEHPK3PXP"})
	code, left, err := v.TOTP(id)
	if err != nil || len(code) != 6 || left < 1 || left > 30 {
		t.Fatalf("TOTP = %q %d %v", code, left, err)
	}
	id2, _ := v.AddEntry(Entry{Title: "no totp"})
	if _, _, err := v.TOTP(id2); err == nil {
		t.Fatal("entry without TOTP produced a code")
	}
}

func TestGeneratePassword(t *testing.T) {
	for i := 0; i < 200; i++ {
		pw, err := GeneratePassword(PasswordOpts{Length: 12, Lower: true, Upper: true, Digits: true, Symbols: true, ExcludeAmbiguous: true})
		if err != nil || len([]rune(pw)) != 12 {
			t.Fatalf("%q %v", pw, err)
		}
		var l, u, d, s bool
		for _, r := range pw {
			switch {
			case unicode.IsLower(r):
				l = true
			case unicode.IsUpper(r):
				u = true
			case unicode.IsDigit(r):
				d = true
			default:
				s = true
			}
			if strings.ContainsRune(ambiguous, r) {
				t.Fatalf("ambiguous character %q in %q", r, pw)
			}
		}
		if !l || !u || !d || !s {
			t.Fatalf("missing a class in %q", pw)
		}
	}
	pw, _ := GeneratePassword(PasswordOpts{Length: 40, Digits: true})
	if strings.Trim(pw, digitSet) != "" {
		t.Fatalf("digits only: %q", pw)
	}
	pw, _ = GeneratePassword(PasswordOpts{Length: 30, Lower: true, Exclude: "aeiou"})
	if strings.ContainsAny(pw, "aeiou") {
		t.Fatalf("excluded characters present: %q", pw)
	}
	for _, bad := range []PasswordOpts{{Length: 3}, {Length: 1000}, {Length: 4, Lower: true, Exclude: lowerSet}} {
		if _, err := GeneratePassword(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	a, _ := GeneratePassword(PasswordOpts{Length: 32})
	b, _ := GeneratePassword(PasswordOpts{Length: 32})
	if a == b {
		t.Fatal("two generated passwords are equal")
	}
}

func TestGenerateDistributionIsNotObviouslyBiased(t *testing.T) {
	counts := map[rune]int{}
	for i := 0; i < 4000; i++ {
		pw, _ := GeneratePassword(PasswordOpts{Length: 8, Digits: true})
		for _, r := range pw {
			counts[r]++
		}
	}
	for _, r := range digitSet {
		// 32000 draws over 10 digits: 3200 expected; allow a wide band, this only catches gross bias
		if c := counts[r]; c < 2900 || c > 3500 {
			t.Errorf("digit %c drawn %d times", r, c)
		}
	}
}

func TestPassphraseAndWordList(t *testing.T) {
	if len(wordList) != 7776 {
		t.Fatalf("word list has %d words", len(wordList))
	}
	p, err := GeneratePassphrase(PassphraseOpts{Words: 6, Separator: "-", Capitalize: true, Number: true})
	if err != nil || len(strings.Split(p, "-")) != 6 {
		t.Fatalf("%q %v", p, err)
	}
	if _, err := GeneratePassphrase(PassphraseOpts{Words: 2}); err == nil {
		t.Fatal("2 words accepted")
	}
	if _, err := GeneratePassphrase(PassphraseOpts{Words: 5, Separator: "\n"}); err == nil {
		t.Fatal("newline separator accepted")
	}
}

func TestStrength(t *testing.T) {
	rank := map[string]int{"very_weak": 0, "weak": 1, "fair": 2, "good": 3, "strong": 4}
	check := func(pw, min, max string) {
		t.Helper()
		s := Strength(pw)
		if rank[s.Label] < rank[min] || rank[s.Label] > rank[max] {
			t.Errorf("Strength(%q) = %+v, want between %s and %s", pw, s, min, max)
		}
	}
	check("", "very_weak", "very_weak")
	check("123456", "very_weak", "very_weak")
	check("password", "very_weak", "weak")
	check("Password123!", "very_weak", "fair")
	check("aaaaaaaaaaaaaaaa", "very_weak", "weak")
	check("abcdefgh", "very_weak", "weak")
	check("xK9#mQ2$vL7@nR4!", "good", "strong")
	g, _ := GeneratePassword(PasswordOpts{Length: 24})
	check(g, "strong", "strong")
	pp, _ := GeneratePassphrase(PassphraseOpts{Words: 6, Separator: "-"})
	check(pp, "good", "strong")
}
