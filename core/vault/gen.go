package vault

import (
	"crypto/rand"
	_ "embed"
	"math"
	"math/big"
	"strings"
	"unicode"
)

// The word list is the EFF "long" list for passphrases (https://www.eff.org/dice), released under
// CC BY 3.0 US; see NOTICE.md.
//
//go:embed data/eff_large_wordlist.txt
var effWords string

var wordList = func() []string {
	var out []string
	for _, line := range strings.Split(effWords, "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			out = append(out, f[1])
		}
	}
	return out
}()

// PasswordOpts configures GeneratePassword. With no class selected, all four are used.
type PasswordOpts struct {
	Length           int    `json:"length"`
	Lower            bool   `json:"lower"`
	Upper            bool   `json:"upper"`
	Digits           bool   `json:"digits"`
	Symbols          bool   `json:"symbols"`
	ExcludeAmbiguous bool   `json:"exclude_ambiguous"` // 0 O o 1 l I | and friends
	Exclude          string `json:"exclude"`           // any extra characters to leave out
}

const (
	lowerSet   = "abcdefghijklmnopqrstuvwxyz"
	upperSet   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitSet   = "0123456789"
	symbolSet  = "!@#$%^&*()-_=+[]{};:,.<>/?~"
	ambiguous  = "0Oo1lI|`'\"" + "5S2Z8B"
	maxGenSize = 256
)

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic("vault: no randomness available: " + err.Error()) // the OS random source failing is not recoverable
	}
	return int(v.Int64())
}

func filterSet(set string, o PasswordOpts) string {
	var b strings.Builder
	for _, r := range set {
		if o.ExcludeAmbiguous && strings.ContainsRune(ambiguous, r) || strings.ContainsRune(o.Exclude, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// GeneratePassword makes a random password from the operating system's secure random source. Every
// selected class appears at least once; indexes are drawn without modulo bias.
func GeneratePassword(o PasswordOpts) (string, error) {
	if o.Length < 4 || o.Length > maxGenSize {
		return "", invalid("password length must be between 4 and %d", maxGenSize)
	}
	if !o.Lower && !o.Upper && !o.Digits && !o.Symbols {
		o.Lower, o.Upper, o.Digits, o.Symbols = true, true, true, true
	}
	var sets []string
	for _, c := range []struct {
		on  bool
		set string
	}{{o.Lower, lowerSet}, {o.Upper, upperSet}, {o.Digits, digitSet}, {o.Symbols, symbolSet}} {
		if c.on {
			if s := filterSet(c.set, o); s != "" {
				sets = append(sets, s)
			}
		}
	}
	if len(sets) == 0 || len(sets) > o.Length {
		return "", invalid("the character classes leave nothing to choose from, or the password is too short for them")
	}
	all := strings.Join(sets, "")
	pw := make([]rune, 0, o.Length)
	for _, s := range sets { // one from each class...
		r := []rune(s)
		pw = append(pw, r[randInt(len(r))])
	}
	ar := []rune(all)
	for len(pw) < o.Length { // ...the rest from everything
		pw = append(pw, ar[randInt(len(ar))])
	}
	for i := len(pw) - 1; i > 0; i-- { // Fisher-Yates
		j := randInt(i + 1)
		pw[i], pw[j] = pw[j], pw[i]
	}
	return string(pw), nil
}

// PassphraseOpts configures GeneratePassphrase.
type PassphraseOpts struct {
	Words      int    `json:"words"`
	Separator  string `json:"separator"`
	Capitalize bool   `json:"capitalize"`
	Number     bool   `json:"number"` // append one random digit to a random word
}

// GeneratePassphrase picks words from the 7776-word EFF list (12.9 bits each).
func GeneratePassphrase(o PassphraseOpts) (string, error) {
	if o.Words < 3 || o.Words > 20 {
		return "", invalid("a passphrase has 3 to 20 words")
	}
	if len(o.Separator) > 8 || strings.ContainsAny(o.Separator, "\r\n\x00") {
		return "", invalid("invalid separator")
	}
	words := make([]string, o.Words)
	for i := range words {
		w := wordList[randInt(len(wordList))]
		if o.Capitalize {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		words[i] = w
	}
	if o.Number {
		words[randInt(len(words))] += string(rune('0' + randInt(10)))
	}
	return strings.Join(words, o.Separator), nil
}

// StrengthInfo is a rough strength estimate. It is a heuristic for the strength meter and the health
// report, not a proof: random-looking text scores by its alphabet, obvious patterns are discounted.
type StrengthInfo struct {
	Bits  float64 `json:"bits"`
	Label string  `json:"label"` // very_weak, weak, fair, good, strong
}

var commonPatterns = []string{"password", "passwort", "qwerty", "asdfgh", "zxcvbn", "letmein", "welcome", "admin", "login", "iloveyou", "monkey", "dragon", "abc123", "123456", "111111", "000000", "secret", "master", "football", "baseball"}

// Strength estimates the entropy of pw in bits.
func Strength(pw string) StrengthInfo {
	if pw == "" {
		return StrengthInfo{Label: "very_weak"}
	}
	var lower, upper, digit, symbol, other bool
	uniq := map[rune]bool{}
	n := 0
	for _, r := range pw {
		n++
		uniq[r] = true
		switch {
		case unicode.IsLower(r) && r < 128:
			lower = true
		case unicode.IsUpper(r) && r < 128:
			upper = true
		case unicode.IsDigit(r) && r < 128:
			digit = true
		case r < 128:
			symbol = true
		default:
			other = true
		}
	}
	pool := 0
	for _, c := range []struct {
		on bool
		n  int
	}{{lower, 26}, {upper, 26}, {digit, 10}, {symbol, 32}, {other, 100}} {
		if c.on {
			pool += c.n
		}
	}
	// characters seen before add little: count a repeat as a quarter of a fresh character
	effective := float64(len(uniq)) + float64(n-len(uniq))*0.25
	bits := effective * math.Log2(float64(pool))
	low := strings.ToLower(pw)
	for _, p := range commonPatterns {
		if strings.Contains(low, p) {
			bits -= float64(len(p)) * 4
		}
	}
	// ascending / descending runs ("abcd", "4321")
	run := 1
	var prev rune
	for i, r := range []rune(low) {
		if i > 0 && (r == prev+1 || r == prev-1) {
			run++
			if run >= 3 {
				bits -= 3
			}
		} else {
			run = 1
		}
		prev = r
	}
	if bits < 0 {
		bits = 0
	}
	label := "strong"
	switch {
	case bits < 28:
		label = "very_weak"
	case bits < 36:
		label = "weak"
	case bits < 60:
		label = "fair"
	case bits < 80:
		label = "good"
	}
	return StrengthInfo{Bits: math.Round(bits*10) / 10, Label: label}
}
