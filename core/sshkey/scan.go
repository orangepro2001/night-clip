package sshkey

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Pub states reported by Scan.
const (
	PubOK         = "ok"         // <key>.pub exists and matches
	PubMissing    = "missing"    // no <key>.pub
	PubMismatch   = "mismatch"   // <key>.pub holds a different key
	PubUnverified = "unverified" // <key>.pub exists, key is encrypted with no public half to compare
)

// maxScanFile caps how much of each file Scan reads.
const maxScanFile = 64 << 10

// Ref points at a line in another file that names a key path.
type Ref struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// ScanItem describes one private key file. It never carries key material.
type ScanItem struct {
	Path         string   `json:"path"`
	Format       Format   `json:"format,omitempty"`
	Encrypted    bool     `json:"encrypted"`
	Algo         string   `json:"algo,omitempty"`
	Bits         int      `json:"bits,omitempty"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	Comment      string   `json:"comment,omitempty"`
	PubPath      string   `json:"pub_path"`
	PubState     string   `json:"pub_state"`
	ConfigHosts  []string `json:"config_hosts"`
	ReferencedBy []Ref    `json:"referenced_by"`
	Problem      string   `json:"problem,omitempty"`

	// Public is the key's public half when known (not serialised).
	Public ssh.PublicKey `json:"-"`
}

// ScanResult lists private keys and SSH host declarations found in one directory.
type ScanResult struct {
	Dir           string       `json:"dir"`
	Items         []ScanItem   `json:"items"`
	Skipped       int          `json:"skipped"`
	ConfigHosts   []ConfigHost `json:"config_hosts"`
	ConfigPartial bool         `json:"config_partial"`
}

// Scan looks at the regular files directly inside dir (no recursion),
// decides by content which are private keys, pairs each with its .pub file,
// and finds which ssh config Host blocks and which scripts in the same
// directory refer to it. home expands "~" and "%d" in the config.
func Scan(dir, home string) (ScanResult, error) {
	res := ScanResult{Dir: dir, Items: []ScanItem{}, ConfigHosts: []ConfigHost{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, err
	}
	var scripts []string
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, name)
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".ps1"), strings.HasSuffix(lower, ".sh"),
			strings.HasSuffix(lower, ".bat"), strings.HasSuffix(lower, ".cmd"):
			scripts = append(scripts, path)
			continue
		case strings.HasSuffix(lower, ".pub"), strings.HasPrefix(lower, "known_hosts"),
			strings.HasPrefix(lower, "authorized_keys"), strings.HasPrefix(lower, "config"):
			continue
		}
		data, big, err := readCapped(path, maxScanFile)
		if err != nil || big || !looksLikeKey(data) {
			res.Skipped++
			continue
		}
		item, ok := inspectFile(path, data)
		if !ok {
			res.Skipped++
			continue
		}
		res.Items = append(res.Items, item)
	}

	cfg := configScan{hosts: res.ConfigHosts, identities: map[string][]string{}, remaining: 64, bytesLeft: 1 << 20}
	cfg.read(filepath.Join(dir, "config"), dir, home, 0, []string{"*"}, -1)
	res.ConfigHosts, res.ConfigPartial = cfg.hosts, cfg.partial
	for i := range res.Items {
		it := &res.Items[i]
		it.ConfigHosts = cfg.identities[pathKey(it.Path)]
		if it.ConfigHosts == nil {
			it.ConfigHosts = []string{}
		}
		it.ReferencedBy = findRefs(scripts, filepath.Base(it.Path))
	}
	sort.Slice(res.Items, func(i, j int) bool { return res.Items[i].Path < res.Items[j].Path })
	return res, nil
}

func readCapped(path string, limit int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > limit {
		return nil, true, nil
	}
	return b, false, nil
}

// looksLikeKey is a cheap gate before the parsers run, so arbitrary files
// (notes, JSON credentials, recovery codes) are never parsed or reported.
func looksLikeKey(b []byte) bool {
	t := bytes.TrimSpace(normalize(b))
	if len(t) == 0 {
		return false
	}
	return bytes.Contains(t, []byte("PRIVATE KEY-----")) ||
		bytes.HasPrefix(t, []byte("PuTTY-User-Key-File-")) ||
		bytes.HasPrefix(t, []byte("openssh-key-v1\x00")) ||
		bytes.Contains(t, []byte("---- BEGIN SSH2 ENCRYPTED PRIVATE KEY ----")) ||
		(t[0] == 0x30 && len(t) < 16<<10 && (isEncryptedPKCS8(t) || derKey(t)))
}

func derKey(t []byte) bool {
	_, err := parseDER(t, nil)
	if err == nil {
		return true
	}
	if _, ok := err.(*PassphraseError); ok {
		return true
	}
	r, _ := IsUnsupported(err)
	return r != "" && r != ReasonUnknown && r != ReasonPublicOnly && r != ReasonCertificate
}

func inspectFile(path string, data []byte) (ScanItem, bool) {
	it := ScanItem{Path: path, PubPath: path + ".pub", PubState: PubMissing}
	k, err := Parse(data, nil)
	switch e := err.(type) {
	case nil:
		it.Format, it.Algo, it.Bits = k.Format, k.Algo(), k.Bits()
		it.Comment, it.Public = k.Comment, k.Public
	case *PassphraseError:
		it.Format, it.Encrypted, it.Public, it.Comment = e.Info.Format, true, e.Info.Public, e.Info.Comment
	default:
		reason, ok := IsUnsupported(err)
		switch {
		case ok && (reason == ReasonUnknown || reason == ReasonPublicOnly || reason == ReasonCertificate):
			return it, false
		case ok:
			it.Problem = reason
		default:
			it.Problem = "corrupt"
		}
	}

	if pub, _, err := readCapped(it.PubPath, maxScanFile); err == nil {
		filePub, comment, _, _, perr := ssh.ParseAuthorizedKey(pub)
		switch {
		case perr != nil:
			it.PubState = PubMismatch
		case it.Public == nil:
			it.PubState, it.Public = PubUnverified, filePub
			if it.Comment == "" {
				it.Comment = comment
			}
		case bytes.Equal(filePub.Marshal(), it.Public.Marshal()):
			it.PubState = PubOK
			if it.Comment == "" {
				it.Comment = comment
			}
		default:
			it.PubState = PubMismatch
		}
	}
	if it.Public != nil {
		it.Fingerprint = ssh.FingerprintSHA256(it.Public)
		if it.Algo == "" {
			it.Algo = it.Public.Type()
		}
	}
	return it, true
}

// findRefs reports lines in the given scripts that name the key file by a
// path ending in /name or \name (so "id_rsa" does not match "id_rsa.pub").
func findRefs(scripts []string, name string) []Ref {
	re := regexp.MustCompile(`[\\/]` + regexp.QuoteMeta(name) + `(?:$|[^\w.-])`)
	if runtime.GOOS == "windows" {
		re = regexp.MustCompile(`(?i)[\\/]` + regexp.QuoteMeta(name) + `(?:$|[^\w.-])`)
	}
	refs := []Ref{}
	for _, s := range scripts {
		b, big, err := readCapped(s, 1<<20)
		if err != nil || big {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(normalize(b)))
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for n := 1; sc.Scan(); n++ {
			if re.MatchString(sc.Text()) {
				refs = append(refs, Ref{File: s, Line: n})
			}
		}
	}
	return refs
}
