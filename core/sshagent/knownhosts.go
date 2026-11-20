package sshagent

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// KnownHosts answers two questions about a server's host key from the
// user's known_hosts files: which host names it is recorded under (for
// display), and whether it belongs to one of a key's allowed hosts (host
// binding). Hashed entries (HashKnownHosts yes) cannot be listed, but a
// literal host name can still be tested against them. Files are re-read when
// they change.
type KnownHosts struct {
	paths []string

	mu    sync.Mutex
	files map[string]*khFile
}

type khFile struct {
	mod    time.Time
	size   int64
	plain  map[string][]string // host key blob -> plain names
	hashed []hashedName
}

// hashedName is one "|1|salt|hash" name of a known_hosts line.
type hashedName struct {
	salt, hash []byte
	key        string // host key blob
}

const maxKnownHosts = 4 << 20

// ExactHostPrefix distinguishes an OpenSSH lookup name from a manual host
// pattern. It never falls back to the unqualified name of another port.
const ExactHostPrefix = "known_hosts:"

// NewKnownHosts reads the given files lazily; missing files are ignored.
func NewKnownHosts(paths ...string) *KnownHosts {
	var ps []string
	for _, p := range paths {
		if p != "" {
			ps = append(ps, p)
		}
	}
	return &KnownHosts{paths: ps, files: map[string]*khFile{}}
}

// load returns the parsed files that exist, refreshing changed ones.
func (k *KnownHosts) load() []*khFile {
	var out []*khFile
	for _, p := range k.paths {
		st, err := os.Stat(p)
		if err != nil || st.Size() > maxKnownHosts {
			delete(k.files, p)
			continue
		}
		f := k.files[p]
		if f == nil || !st.ModTime().Equal(f.mod) || st.Size() != f.size {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			f = parseKnownHosts(data)
			f.mod, f.size = st.ModTime(), st.Size()
			k.files[p] = f
		}
		out = append(out, f)
	}
	return out
}

// Names returns the plain host names recorded for hk, possibly none.
func (k *KnownHosts) Names(hk ssh.PublicKey) []string {
	if k == nil || hk == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	blob := string(hk.Marshal())
	var names []string
	for _, f := range k.load() {
		for _, n := range f.plain[blob] {
			if !contains(names, n) {
				names = append(names, n)
			}
		}
	}
	return names
}

// Matches reports whether the server with host key hk is one of the
// allowed hosts. A pattern is a host key fingerprint ("SHA256:…"), or a host
// name, optionally with * and ? wildcards, compared case-insensitively with
// the names known_hosts records for hk (with and without a "[host]:port"
// wrapper). Literal names also match hashed known_hosts entries.
func (k *KnownHosts) Matches(hk ssh.PublicKey, patterns []string) bool {
	if hk == nil {
		return false
	}
	fp := ssh.FingerprintSHA256(hk)
	var names []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "SHA256:") {
			if p == fp {
				return true
			}
			continue
		}
		if names == nil {
			names = append(k.Names(hk), "") // non-nil even when empty
		}
		if exact, ok := strings.CutPrefix(p, ExactHostPrefix); ok {
			for _, name := range names {
				if name != "" && strings.EqualFold(name, exact) {
					return true
				}
			}
			if k.hashedMatch(hk, exact) {
				return true
			}
			continue
		}
		for _, n := range names {
			if n != "" && (hostPatternMatch(p, n) || hostPatternMatch(p, bareHost(n))) {
				return true
			}
		}
		if !strings.ContainsAny(p, "*?") && k.hashedMatch(hk, p) {
			return true
		}
	}
	return false
}

func (k *KnownHosts) hashedMatch(hk ssh.PublicKey, name string) bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	blob := string(hk.Marshal())
	candidates := []string{strings.ToLower(name)}
	if name != candidates[0] {
		candidates = append(candidates, name)
	}
	for _, f := range k.load() {
		for _, h := range f.hashed {
			if h.key != blob {
				continue
			}
			for _, c := range candidates {
				m := hmac.New(sha1.New, h.salt)
				m.Write([]byte(c))
				if hmac.Equal(m.Sum(nil), h.hash) {
					return true
				}
			}
		}
	}
	return false
}

// bareHost turns "[host]:port" into "host".
func bareHost(n string) string {
	if strings.HasPrefix(n, "[") {
		if i := strings.Index(n, "]"); i > 0 {
			return n[1:i]
		}
	}
	return n
}

// hostPatternMatch matches OpenSSH-style host patterns: * and ? wildcards,
// case-insensitive.
func hostPatternMatch(pattern, name string) bool {
	p, s := strings.ToLower(pattern), strings.ToLower(name)
	// iterative wildcard match with backtracking on '*'
	pi, si, star, mark := 0, 0, -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == s[si]):
			pi++
			si++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

func parseKnownHosts(data []byte) *khFile {
	f := &khFile{plain: map[string][]string{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == '@' {
			continue
		}
		hosts, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(rest)))
		if err != nil {
			continue
		}
		key := string(pub.Marshal())
		for _, h := range strings.Split(hosts, ",") {
			switch {
			case h == "" || strings.HasPrefix(h, "!") || strings.ContainsAny(h, "*?"):
			case strings.HasPrefix(h, "|1|"):
				parts := strings.Split(h, "|")
				if len(parts) != 4 {
					continue
				}
				salt, err1 := base64.StdEncoding.DecodeString(parts[2])
				hash, err2 := base64.StdEncoding.DecodeString(parts[3])
				if err1 == nil && err2 == nil && len(hash) == sha1.Size {
					f.hashed = append(f.hashed, hashedName{salt: salt, hash: hash, key: key})
				}
			default:
				if !contains(f.plain[key], h) {
					f.plain[key] = append(f.plain[key], h)
				}
			}
		}
	}
	return f
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ValidHostPattern reports whether p can be stored as an allowed host: a
// "SHA256:" fingerprint, or a host name / address with optional * ? wildcards
// and an optional "[host]:port" form.
func ValidHostPattern(p string) bool {
	if p == "" || len(p) > 255 {
		return false
	}
	if strings.HasPrefix(p, "SHA256:") {
		b, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(p, "SHA256:"))
		return err == nil && len(b) == 32
	}
	if exact, ok := strings.CutPrefix(p, ExactHostPrefix); ok {
		if exact == "" || strings.ContainsAny(exact, "*?") {
			return false
		}
		p = exact
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(".-_*?:[]", r):
		default:
			return false
		}
	}
	return true
}
